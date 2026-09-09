package service

import (
	"context"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type RoutingService struct {
	db       *gorm.DB
	convRepo *repository.ConversationRepository
	hub      *ws.Hub
	mu       sync.Mutex
}

func NewRoutingService(db *gorm.DB, convRepo *repository.ConversationRepository, hub *ws.Hub) *RoutingService {
	return &RoutingService{
		db:       db,
		convRepo: convRepo,
		hub:      hub,
	}
}

// IsWithinWorkingHours checks if given time falls within configured inbox working hours
func (s *RoutingService) IsWithinWorkingHours(inbox *domain.Inbox, t time.Time) bool {
	return IsWithinWorkingHours(inbox, t)
}

// CheckAgentCapacity determines if an agent has remaining capacity for a conversation in a specific inbox
func (s *RoutingService) CheckAgentCapacity(accountID, inboxID, agentID uint, assignmentPolicy *domain.AssignmentPolicy, excludeConvID uint) (hasCapacity bool, currentLoad int64) {
	// 1. First check if agent is assigned to an AgentCapacityPolicy (per-inbox capacity limits)
	var accountUser domain.AccountUser
	if err := s.db.Where("account_id = ? AND user_id = ?", accountID, agentID).First(&accountUser).Error; err == nil {
		if accountUser.AgentCapacityPolicyID != nil && *accountUser.AgentCapacityPolicyID > 0 {
			var inboxLimit domain.InboxCapacityLimit
			if err := s.db.Where("agent_capacity_policy_id = ? AND inbox_id = ?", *accountUser.AgentCapacityPolicyID, inboxID).First(&inboxLimit).Error; err == nil {
				// Specific limit for this inbox configured!
				var inboxCount int64
				q := s.db.Model(&domain.Conversation{}).
					Where("account_id = ? AND inbox_id = ? AND assignee_id = ? AND status != ?", accountID, inboxID, agentID, domain.ConversationStatusResolved)
				if excludeConvID > 0 {
					q = q.Where("id != ?", excludeConvID)
				}
				q.Count(&inboxCount)
				return inboxCount < int64(inboxLimit.ConversationLimit), inboxCount
			}
			// If no specific limit for this inbox under this policy, agent has unlimited capacity for this inbox
			var totalCount int64
			s.db.Model(&domain.Conversation{}).Where("account_id = ? AND assignee_id = ? AND status != ?", accountID, agentID, domain.ConversationStatusResolved).Count(&totalCount)
			return true, totalCount
		}
	}

	// 2. Next check if AssignmentPolicy has an AgentCapacityLimit
	var count int64
	cntQuery := s.db.Model(&domain.Conversation{}).
		Where("assignee_id = ? AND status != ?", agentID, domain.ConversationStatusResolved)
	if excludeConvID > 0 {
		cntQuery = cntQuery.Where("id != ?", excludeConvID)
	}
	if accountID > 0 {
		cntQuery = cntQuery.Where("account_id = ?", accountID)
	}
	cntQuery.Count(&count)

	limit := 0
	if assignmentPolicy != nil && assignmentPolicy.AgentCapacityLimit > 0 {
		limit = assignmentPolicy.AgentCapacityLimit
	} else {
		// Legacy / general CapacityPolicy fallback
		var capPolicy domain.CapacityPolicy
		foundPolicy := false
		if accountID > 0 {
			if err := s.db.Where("account_id = ? AND user_id = ?", accountID, agentID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				foundPolicy = true
			}
		}
		if !foundPolicy {
			if err := s.db.Where("user_id = ?", agentID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				foundPolicy = true
			} else if accountID > 0 {
				if err := s.db.Where("account_id = ? AND user_id = 0", accountID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
					foundPolicy = true
				}
			}
		}
		if foundPolicy {
			limit = capPolicy.ConversationLimit
		}
	}

	if limit > 0 && count >= int64(limit) {
		return false, count
	}
	return true, count
}

// AutoAssign attempts to assign an open conversation to an available, online agent in the inbox
func (s *RoutingService) AutoAssign(conv *domain.Conversation) (*domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure AccountID is resolved from inbox if missing
	if conv.AccountID == 0 && conv.InboxID > 0 {
		var inbox domain.Inbox
		if err := s.db.Select("account_id").First(&inbox, conv.InboxID).Error; err == nil {
			conv.AccountID = inbox.AccountID
		}
	}

	var inbox domain.Inbox
	var policy *domain.AssignmentPolicy
	if conv.InboxID != 0 {
		if err := s.db.Preload("AssignmentPolicy").First(&inbox, conv.InboxID).Error; err == nil {
			if inbox.AssignmentPolicy != nil {
				policy = inbox.AssignmentPolicy
			}
		}
	}

	// If policy is bound, check enabled status
	if policy != nil && !policy.Enabled {
		return nil, nil // Policy is disabled, do not auto-assign
	}

	// Check working hours
	shouldCheckWorkingHours := false
	if policy != nil && policy.WorkingHoursOnly {
		shouldCheckWorkingHours = true
	} else if inbox.WorkingHoursEnabled {
		shouldCheckWorkingHours = true
	}

	if shouldCheckWorkingHours && !s.IsWithinWorkingHours(&inbox, time.Now()) {
		// Off-hours! Do not route to agents. Send out-of-office response if configured.
		if inbox.OutOfOfficeMessage != "" {
			var count int64
			s.db.Model(&domain.Message{}).
				Where("conversation_id = ? AND content = ?", conv.ID, inbox.OutOfOfficeMessage).
				Count(&count)
			if count == 0 {
				_ = s.db.Create(&domain.Message{
					AccountID:      conv.AccountID,
					ConversationID: conv.ID,
					MessageType:    domain.MessageTypeOutgoing,
					Content:        inbox.OutOfOfficeMessage,
					CreatedAt:      time.Now().UTC(),
					UpdatedAt:      time.Now().UTC(),
				}).Error
			}
		}
		return nil, nil
	}

	// If already assigned, check if existing assignee still has available capacity
	if conv.AssigneeID != nil && *conv.AssigneeID > 0 {
		agentID := *conv.AssigneeID
		hasCap, _ := s.CheckAgentCapacity(conv.AccountID, conv.InboxID, agentID, policy, conv.ID)
		if hasCap {
			var existing domain.User
			if err := s.db.First(&existing, agentID).Error; err == nil {
				return &existing, nil
			}
		}
		// Over capacity! Clear assignee so AutoAssign can find an available agent or keep unassigned
		conv.AssigneeID = nil
		if conv.ID > 0 {
			_ = s.convRepo.Assign(conv.AccountID, conv.ID, nil)
		}
	}

	// 1. Find all members of the inbox who are currently 'online'
	var eligibleAgents []domain.User
	err := s.db.Joins("JOIN inbox_members ON inbox_members.user_id = users.id").
		Where("inbox_members.inbox_id = ? AND users.availability = ?", conv.InboxID, domain.AvailabilityOnline).
		Order("users.id ASC").
		Find(&eligibleAgents).Error

	if err != nil {
		return nil, err
	}

	// 2. Filter available agents based on capacity
	var availableAgents []domain.User
	agentLoad := make(map[uint]int64)

	for i := range eligibleAgents {
		agent := &eligibleAgents[i]
		hasCap, load := s.CheckAgentCapacity(conv.AccountID, conv.InboxID, agent.ID, policy, conv.ID)
		agentLoad[agent.ID] = load

		if !hasCap {
			continue // agent has reached maximum capacity
		}

		availableAgents = append(availableAgents, *agent)
	}

	if len(availableAgents) == 0 {
		// No online agents currently available or all reached capacity. Check fallback!
		if policy != nil {
			if policy.FallbackAssigneeID != nil && *policy.FallbackAssigneeID > 0 {
				var fallbackUser domain.User
				if err := s.db.First(&fallbackUser, *policy.FallbackAssigneeID).Error; err == nil {
					_ = s.convRepo.Assign(conv.AccountID, conv.ID, &fallbackUser.ID)
					conv.AssigneeID = &fallbackUser.ID
					conv.Assignee = &fallbackUser
					if s.hub != nil {
						s.hub.Broadcast(&ws.Event{
							Name:           ws.EventConversationUpdated,
							AccountID:      conv.AccountID,
							ConversationID: conv.ID,
							Data: ginH{
								"id":          conv.ID,
								"assignee_id": fallbackUser.ID,
								"assignee":    fallbackUser,
							},
						})
					}
					return &fallbackUser, nil
				}
			}
			if policy.FallbackTeamID != nil && *policy.FallbackTeamID > 0 {
				conv.TeamID = policy.FallbackTeamID
				_ = s.db.Model(&domain.Conversation{}).
					Where("account_id = ? AND id = ?", conv.AccountID, conv.ID).
					Update("team_id", policy.FallbackTeamID)
				return nil, nil
			}
		}
		return nil, nil
	}

	// 3. Select agent according to policy strategy (Round-Robin vs Least-Active)
	var bestAgent *domain.User
	if policy != nil && policy.StrategyType == domain.StrategyRoundRobin {
		var lastConv domain.Conversation
		var lastAssigneeID uint
		q := s.db.Model(&domain.Conversation{}).
			Where("inbox_id = ? AND assignee_id IS NOT NULL", conv.InboxID)
		if conv.ID > 0 {
			q = q.Where("id != ?", conv.ID)
		}
		if err := q.Order("id DESC").First(&lastConv).Error; err == nil && lastConv.AssigneeID != nil {
			lastAssigneeID = *lastConv.AssigneeID
		}

		bestIndex := 0
		if lastAssigneeID > 0 {
			for idx, ag := range availableAgents {
				if ag.ID == lastAssigneeID {
					bestIndex = (idx + 1) % len(availableAgents)
					break
				}
			}
		}
		bestAgent = &availableAgents[bestIndex]
	} else {
		// Least-active (workload) strategy
		minLoad := int64(-1)
		for i := range availableAgents {
			ag := &availableAgents[i]
			load := agentLoad[ag.ID]
			if minLoad == -1 || load < minLoad {
				minLoad = load
				bestAgent = ag
			}
		}
	}

	// 3. Assign conversation to selected agent
	if err := s.convRepo.Assign(conv.AccountID, conv.ID, &bestAgent.ID); err != nil {
		return nil, err
	}
	conv.AssigneeID = &bestAgent.ID
	conv.Assignee = bestAgent

	logger.WithComponent("routing").Info("auto-assigned conversation to agent",
		"conversation_id", conv.ID,
		"account_id", conv.AccountID,
		"inbox_id", conv.InboxID,
		"agent_id", bestAgent.ID,
		"agent_email", bestAgent.Email,
	)

	// 4. Broadcast event via WebSocket if hub is provided
	if s.hub != nil {
		s.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationUpdated,
			AccountID:      conv.AccountID,
			ConversationID: conv.ID,
			Data: ginH{
				"id":          conv.ID,
				"assignee_id": bestAgent.ID,
				"assignee":    bestAgent,
			},
		})
	}

	return bestAgent, nil
}

// ResumeSnoozedConversations checks for any snoozed conversations whose snooze time has expired and re-opens them
func (s *RoutingService) ResumeSnoozedConversations(ctx context.Context) ([]domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	var snoozed []domain.Conversation
	err := s.db.WithContext(ctx).
		Where("status = ? AND snoozed_until IS NOT NULL AND snoozed_until <= ?", domain.ConversationStatusSnoozed, now).
		Find(&snoozed).Error
	if err != nil {
		return nil, err
	}

	var resumed []domain.Conversation
	for i := range snoozed {
		conv := &snoozed[i]
		updates := map[string]any{
			"status":           domain.ConversationStatusOpen,
			"snoozed_until":    nil,
			"last_activity_at": now,
		}
		if err := s.db.WithContext(ctx).Model(&domain.Conversation{}).
			Where("id = ? AND account_id = ?", conv.ID, conv.AccountID).
			Updates(updates).Error; err == nil {
			conv.Status = domain.ConversationStatusOpen
			conv.SnoozedUntil = nil
			conv.LastActivityAt = now
			resumed = append(resumed, *conv)

			logger.WithComponent("routing").Info("resumed expired snoozed conversation",
				"conversation_id", conv.ID,
				"account_id", conv.AccountID,
			)

			if s.hub != nil {
				s.hub.Broadcast(&ws.Event{
					Name:           ws.EventConversationUpdated,
					AccountID:      conv.AccountID,
					ConversationID: conv.ID,
					Data: ginH{
						"id":            conv.ID,
						"status":        domain.ConversationStatusOpen,
						"snoozed_until": nil,
					},
				})
			}
		}
	}
	return resumed, nil
}

// StartSnoozeScheduler continuously scans for snoozed conversations to re-open
func (s *RoutingService) StartSnoozeScheduler(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.ResumeSnoozedConversations(ctx)
			}
		}
	}()
}

type ginH = map[string]any

