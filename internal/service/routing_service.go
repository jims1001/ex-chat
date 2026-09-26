package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type RoutingService struct {
	convRepo           *repository.ConversationRepository
	msgRepo            *repository.MessageRepository
	inboxRepo          *repository.InboxRepository
	accountRepo        *repository.AccountRepository
	teamRepo           *repository.TeamRepository
	policyRepo         *repository.AssignmentPolicyRepository
	capacityPolicyRepo *repository.AgentCapacityPolicyRepository
	hub                *ws.Hub
	mu                 sync.Mutex
}

func NewRoutingService(db *gorm.DB, convRepo *repository.ConversationRepository, hub *ws.Hub) *RoutingService {
	return &RoutingService{
		convRepo:           convRepo,
		msgRepo:            repository.NewMessageRepository(db),
		inboxRepo:          repository.NewInboxRepository(db),
		accountRepo:        repository.NewAccountRepository(db),
		teamRepo:           repository.NewTeamRepository(db),
		policyRepo:         repository.NewAssignmentPolicyRepository(db),
		capacityPolicyRepo: repository.NewAgentCapacityPolicyRepository(db),
		hub:                hub,
	}
}

func (s *RoutingService) SetOwnerRepositories(inboxes *repository.InboxRepository, accounts *repository.AccountRepository, teams *repository.TeamRepository, policies *repository.AssignmentPolicyRepository, capacityPolicies *repository.AgentCapacityPolicyRepository, messages *repository.MessageRepository) {
	s.inboxRepo = inboxes
	s.accountRepo = accounts
	s.teamRepo = teams
	s.policyRepo = policies
	s.capacityPolicyRepo = capacityPolicies
	s.msgRepo = messages
}

// IsWithinWorkingHours checks if given time falls within configured inbox working hours
func (s *RoutingService) IsWithinWorkingHours(inbox *domain.Inbox, t time.Time) bool {
	return IsWithinWorkingHours(inbox, t)
}

// CheckAgentCapacity determines if an agent has remaining capacity for a conversation in a specific inbox
func (s *RoutingService) CheckAgentCapacity(accountID, inboxID, agentID uint, assignmentPolicy *domain.AssignmentPolicy, excludeConvID uint) (hasCapacity bool, currentLoad int64, err error) {
	if assignmentPolicy == nil && inboxID > 0 {
		if s.inboxRepo == nil || s.policyRepo == nil {
			return false, 0, gorm.ErrInvalidData
		}
		inbox, findErr := s.inboxRepo.FindSummaryByID(accountID, inboxID)
		if findErr != nil {
			return false, 0, findErr
		}
		if inbox == nil {
			return false, 0, gorm.ErrRecordNotFound
		}
		if inbox.AssignmentPolicyID != nil && *inbox.AssignmentPolicyID > 0 {
			assignmentPolicy, findErr = s.policyRepo.FindSummaryByID(accountID, *inbox.AssignmentPolicyID)
			if findErr != nil {
				return false, 0, findErr
			}
		}
	}

	if s.accountRepo == nil || s.capacityPolicyRepo == nil || s.convRepo == nil {
		return false, 0, gorm.ErrInvalidData
	}
	membership, err := s.accountRepo.GetMembership(accountID, agentID)
	if err != nil {
		return false, 0, err
	}
	if membership != nil && membership.AgentCapacityPolicyID != nil && *membership.AgentCapacityPolicyID > 0 {
		capacityPolicyID := *membership.AgentCapacityPolicyID
		inboxLimit, findErr := s.capacityPolicyRepo.FindInboxLimit(capacityPolicyID, inboxID)
		if findErr != nil {
			return false, 0, findErr
		}
		if inboxLimit != nil {
			var cutoff *time.Time
			var excludedLabels []string
			capacityPolicy, findErr := s.capacityPolicyRepo.FindSummaryByID(accountID, capacityPolicyID)
			if findErr != nil {
				return false, 0, findErr
			}
			if capacityPolicy == nil {
				return false, 0, gorm.ErrRecordNotFound
			}
			if capacityPolicy.ExclusionRules != "" {
				var rules struct {
					ExcludeOlderThanHours int      `json:"exclude_older_than_hours"`
					ExcludedLabels        []string `json:"excluded_labels"`
				}
				if json.Unmarshal([]byte(capacityPolicy.ExclusionRules), &rules) == nil {
					if rules.ExcludeOlderThanHours > 0 {
						value := time.Now().UTC().Add(-time.Duration(rules.ExcludeOlderThanHours) * time.Hour)
						cutoff = &value
					}
					excludedLabels = rules.ExcludedLabels
				}
			}
			count, countErr := s.convRepo.CountActiveAssigned(accountID, inboxID, agentID, excludeConvID, cutoff, excludedLabels)
			if countErr != nil {
				return false, 0, countErr
			}
			return count < int64(inboxLimit.ConversationLimit), count, nil
		}
		if assignmentPolicy != nil && assignmentPolicy.AgentCapacityLimit > 0 {
			count, countErr := s.convRepo.CountActiveAssigned(accountID, inboxID, agentID, excludeConvID, nil, nil)
			return count < int64(assignmentPolicy.AgentCapacityLimit), count, countErr
		}
		count, countErr := s.convRepo.CountActiveAssigned(accountID, 0, agentID, excludeConvID, nil, nil)
		return countErr == nil, count, countErr
	}

	count, err := s.convRepo.CountActiveAssigned(accountID, 0, agentID, excludeConvID, nil, nil)
	if err != nil {
		return false, 0, err
	}

	limit := 0
	if assignmentPolicy != nil && assignmentPolicy.AgentCapacityLimit > 0 {
		limit = assignmentPolicy.AgentCapacityLimit
	} else {
		// Legacy / general CapacityPolicy fallback
		capPolicy, findErr := s.capacityPolicyRepo.FindLegacyCapacityPolicy(accountID, agentID)
		if findErr != nil {
			return false, 0, findErr
		}
		if capPolicy != nil {
			limit = capPolicy.ConversationLimit
		}
	}

	if limit > 0 && count >= int64(limit) {
		return false, count, nil
	}
	return true, count, nil
}

// AutoAssign attempts to assign an open conversation to an available, online agent in the inbox
func (s *RoutingService) AutoAssign(conv *domain.Conversation) (*domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure AccountID is resolved from inbox if missing
	if conv.AccountID == 0 && conv.InboxID > 0 {
		if s.inboxRepo == nil {
			return nil, gorm.ErrInvalidData
		}
		inbox, err := s.inboxRepo.FindByGlobalID(conv.InboxID)
		if err != nil {
			return nil, err
		}
		conv.AccountID = inbox.AccountID
	}

	var inbox domain.Inbox
	var policy *domain.AssignmentPolicy
	if conv.InboxID != 0 {
		if s.inboxRepo == nil || s.policyRepo == nil {
			return nil, gorm.ErrInvalidData
		}
		ownedInbox, err := s.inboxRepo.FindSummaryByID(conv.AccountID, conv.InboxID)
		if err != nil {
			return nil, err
		}
		if ownedInbox == nil {
			return nil, gorm.ErrRecordNotFound
		}
		inbox = *ownedInbox
		if inbox.AssignmentPolicyID != nil && *inbox.AssignmentPolicyID > 0 {
			policy, err = s.policyRepo.FindSummaryByID(conv.AccountID, *inbox.AssignmentPolicyID)
			if err != nil {
				return nil, err
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
			if s.msgRepo == nil {
				return nil, gorm.ErrInvalidData
			}
			exists, err := s.msgRepo.ConversationContentExists(conv.AccountID, conv.ID, inbox.OutOfOfficeMessage)
			if err != nil {
				return nil, err
			}
			if !exists {
				if err := s.msgRepo.Create(&domain.Message{
					AccountID:      conv.AccountID,
					ConversationID: conv.ID,
					MessageType:    domain.MessageTypeOutgoing,
					Content:        inbox.OutOfOfficeMessage,
					CreatedAt:      time.Now().UTC(),
					UpdatedAt:      time.Now().UTC(),
				}); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	}

	// If already assigned, check if existing assignee still has available capacity
	if conv.AssigneeID != nil && *conv.AssigneeID > 0 {
		agentID := *conv.AssigneeID
		hasCap, _, err := s.CheckAgentCapacity(conv.AccountID, conv.InboxID, agentID, policy, conv.ID)
		if err != nil {
			return nil, err
		}
		if hasCap {
			if s.accountRepo == nil {
				return nil, gorm.ErrInvalidData
			}
			membership, err := s.accountRepo.FindMember(conv.AccountID, agentID)
			if err != nil {
				return nil, err
			}
			if membership != nil && membership.User != nil {
				return membership.User, nil
			}
		}
		// Over capacity! Clear assignee so AutoAssign can find an available agent or keep unassigned
		conv.AssigneeID = nil
		if conv.ID > 0 {
			_ = s.convRepo.Assign(conv.AccountID, conv.ID, nil)
		}
	}

	// 1. Find all members of the inbox who are currently 'online'
	if s.inboxRepo == nil {
		return nil, gorm.ErrInvalidData
	}
	members, err := s.inboxRepo.ListMembers(conv.InboxID)
	if err != nil {
		return nil, err
	}
	eligibleAgents := make([]domain.User, 0, len(members))
	for _, member := range members {
		if member.Availability == domain.AvailabilityOnline {
			eligibleAgents = append(eligibleAgents, member)
		}
	}

	// 2. Filter available agents based on capacity
	var availableAgents []domain.User
	agentLoad := make(map[uint]int64)

	for i := range eligibleAgents {
		agent := &eligibleAgents[i]
		hasCap, load, err := s.CheckAgentCapacity(conv.AccountID, conv.InboxID, agent.ID, policy, conv.ID)
		if err != nil {
			return nil, err
		}
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
				if s.accountRepo == nil {
					return nil, gorm.ErrInvalidData
				}
				membership, err := s.accountRepo.FindMember(conv.AccountID, *policy.FallbackAssigneeID)
				if err != nil {
					return nil, err
				}
				if membership == nil || membership.User == nil {
					return nil, gorm.ErrRecordNotFound
				}
				fallbackUser := membership.User
				if err := s.convRepo.Assign(conv.AccountID, conv.ID, &fallbackUser.ID); err != nil {
					return nil, err
				}
				conv.AssigneeID = &fallbackUser.ID
				conv.Assignee = fallbackUser
				if s.hub != nil {
					s.hub.Broadcast(&ws.Event{
						Name:           ws.EventConversationUpdated,
						AccountID:      conv.AccountID,
						ConversationID: conv.ID,
						Data: ginH{
							"id":          conv.ID,
							"assignee_id": fallbackUser.ID,
							"assignee":    *fallbackUser,
						},
					})
				}
				return fallbackUser, nil
			}
			if policy.FallbackTeamID != nil && *policy.FallbackTeamID > 0 {
				if s.teamRepo == nil {
					return nil, gorm.ErrInvalidData
				}
				team, err := s.teamRepo.FindSummaryByID(conv.AccountID, *policy.FallbackTeamID)
				if err != nil {
					return nil, err
				}
				if team == nil {
					return nil, gorm.ErrRecordNotFound
				}
				conv.TeamID = policy.FallbackTeamID
				if err := s.convRepo.AssignTeam(conv.AccountID, conv.ID, policy.FallbackTeamID); err != nil {
					return nil, err
				}
				return nil, nil
			}
		}
		return nil, nil
	}

	// 3. Select agent according to policy strategy (Round-Robin vs Least-Active)
	var bestAgent *domain.User
	if policy != nil && policy.StrategyType == domain.StrategyRoundRobin {
		lastAssigneeID, err := s.convRepo.LastAssignedAgent(conv.AccountID, conv.InboxID, conv.ID)
		if err != nil {
			return nil, err
		}

		bestIndex := 0
		if lastAssigneeID > 0 {
			found := false
			for idx, ag := range availableAgents {
				if ag.ID == lastAssigneeID {
					bestIndex = (idx + 1) % len(availableAgents)
					found = true
					break
				}
			}
			if !found {
				for idx, ag := range availableAgents {
					if ag.ID > lastAssigneeID {
						bestIndex = idx
						found = true
						break
					}
				}
				if !found {
					bestIndex = 0
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
	if s.convRepo == nil {
		return nil, gorm.ErrInvalidData
	}
	resumed, err := s.convRepo.ResumeExpiredSnoozed(ctx, now)
	if err != nil {
		return nil, err
	}

	for i := range resumed {
		conv := &resumed[i]
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
