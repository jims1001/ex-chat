package service

import (
	"errors"
	"sync"

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

// AutoAssign attempts to assign an open conversation to an available, online agent in the inbox
func (s *RoutingService) AutoAssign(conv *domain.Conversation) (*domain.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if conv.AssigneeID != nil {
		var existing domain.User
		if err := s.db.First(&existing, *conv.AssigneeID).Error; err == nil {
			return &existing, nil
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

	// 2. Select agent with minimum active/open conversations (least workload)
	var bestAgent *domain.User
	minLoad := int64(-1)

	for i := range eligibleAgents {
		agent := &eligibleAgents[i]
		var count int64
		s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id = ? AND status = ?", conv.AccountID, agent.ID, domain.ConversationStatusOpen).
			Count(&count)

		if minLoad == -1 || count < minLoad {
			minLoad = count
			bestAgent = agent
		}
	}

	if bestAgent == nil {
		return nil, errors.New("failed to find suitable agent for assignment")
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

type ginH = map[string]any
