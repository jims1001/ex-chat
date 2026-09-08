package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
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
	if inbox == nil || !inbox.WorkingHoursEnabled {
		return true
	}
	tz := inbox.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	localTime := t.In(loc)

	if inbox.WorkingHours == "" {
		return true
	}

	var schedules []domain.WorkingHourConfig
	if err := json.Unmarshal([]byte(inbox.WorkingHours), &schedules); err != nil {
		return true
	}

	day := int(localTime.Weekday())
	for _, sc := range schedules {
		if sc.DayOfWeek == day {
			if sc.Closed {
				return false
			}
			curMin := localTime.Hour()*60 + localTime.Minute()
			openMin := sc.OpenHour*60 + sc.OpenMinute
			closeMin := sc.CloseHour*60 + sc.CloseMinute
			if curMin < openMin || curMin >= closeMin {
				return false
			}
			return true
		}
	}
	return true
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

	// Check working hours of inbox if configured
	if conv.InboxID != 0 {
		var inbox domain.Inbox
		if err := s.db.First(&inbox, conv.InboxID).Error; err == nil {
			if inbox.WorkingHoursEnabled && !s.IsWithinWorkingHours(&inbox, time.Now()) {
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
		}
	}

	// If already assigned, check if existing assignee still has available capacity
	if conv.AssigneeID != nil && *conv.AssigneeID > 0 {
		agentID := *conv.AssigneeID
		var count int64
		cntQuery := s.db.Model(&domain.Conversation{}).
			Where("assignee_id = ? AND status != ?", agentID, domain.ConversationStatusResolved)
		if conv.ID > 0 {
			cntQuery = cntQuery.Where("id != ?", conv.ID)
		}
		if conv.AccountID > 0 {
			cntQuery = cntQuery.Where("account_id = ?", conv.AccountID)
		}
		cntQuery.Count(&count)

		var capPolicy domain.CapacityPolicy
		foundPolicy := false
		if conv.AccountID > 0 {
			if err := s.db.Where("account_id = ? AND user_id = ?", conv.AccountID, agentID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				foundPolicy = true
			}
		}
		if !foundPolicy {
			if err := s.db.Where("user_id = ?", agentID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				foundPolicy = true
			} else if conv.AccountID > 0 {
				if err := s.db.Where("account_id = ? AND user_id = 0", conv.AccountID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
					foundPolicy = true
				}
			}
		}

		if !foundPolicy || capPolicy.ConversationLimit <= 0 || count < int64(capPolicy.ConversationLimit) {
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
		Find(&eligibleAgents).Error

	if err != nil {
		return nil, err
	}

	if len(eligibleAgents) == 0 {
		// No online agents currently available in this inbox
		return nil, nil
	}

	// 2. Select agent with minimum active/open conversations (least workload) within capacity limit
	var bestAgent *domain.User
	minLoad := int64(-1)

	for i := range eligibleAgents {
		agent := &eligibleAgents[i]
		var count int64
		cntQuery := s.db.Model(&domain.Conversation{}).
			Where("assignee_id = ? AND status != ?", agent.ID, domain.ConversationStatusResolved)
		if conv.ID > 0 {
			cntQuery = cntQuery.Where("id != ?", conv.ID)
		}
		if conv.AccountID > 0 {
			cntQuery = cntQuery.Where("account_id = ?", conv.AccountID)
		}
		cntQuery.Count(&count)

		// Check capacity policy limit if configured
		var capPolicy domain.CapacityPolicy
		foundPolicy := false
		if conv.AccountID > 0 {
			if err := s.db.Where("account_id = ? AND user_id = ?", conv.AccountID, agent.ID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				foundPolicy = true
			}
		}
		if !foundPolicy {
			if err := s.db.Where("user_id = ?", agent.ID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				foundPolicy = true
			} else if conv.AccountID > 0 {
				if err := s.db.Where("account_id = ? AND user_id = 0", conv.AccountID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
					foundPolicy = true
				}
			}
		}

		if foundPolicy && capPolicy.ConversationLimit > 0 && count >= int64(capPolicy.ConversationLimit) {
			continue // agent has reached maximum capacity
		}

		if minLoad == -1 || count < minLoad {
			minLoad = count
			bestAgent = agent
		}
	}

	if bestAgent == nil {
		// All eligible agents have reached capacity limits or none available
		return nil, nil
	}

	// 3. Assign conversation to selected agent
	if err := s.convRepo.Assign(conv.AccountID, conv.ID, &bestAgent.ID); err != nil {
		return nil, err
	}
	conv.AssigneeID = &bestAgent.ID
	conv.Assignee = bestAgent

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

