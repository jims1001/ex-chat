package service

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ReportService struct {
	db *gorm.DB
}

func NewReportService(db *gorm.DB) *ReportService {
	return &ReportService{db: db}
}

type AccountSummaryReport struct {
	TotalConversations    int64 `json:"total_conversations"`
	OpenConversations     int64 `json:"open_conversations"`
	ResolvedConversations int64 `json:"resolved_conversations"`
	PendingConversations  int64 `json:"pending_conversations"`
	TotalMessages         int64 `json:"total_messages"`
	TotalContacts         int64 `json:"total_contacts"`
}

type AgentMetric struct {
	UserID                uint   `json:"user_id"`
	Name                  string `json:"name"`
	Email                 string `json:"email"`
	AssignedConversations int64  `json:"assigned_conversations"`
	ResolvedConversations int64  `json:"resolved_conversations"`
}

func (s *ReportService) GetAccountSummary(accountID uint) (*AccountSummaryReport, error) {
	var summary AccountSummaryReport

	s.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID).Count(&summary.TotalConversations)
	s.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusOpen).Count(&summary.OpenConversations)
	s.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusResolved).Count(&summary.ResolvedConversations)
	s.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusPending).Count(&summary.PendingConversations)

	s.db.Model(&domain.Message{}).Where("account_id = ?", accountID).Count(&summary.TotalMessages)
	s.db.Model(&domain.Contact{}).Where("account_id = ?", accountID).Count(&summary.TotalContacts)

	return &summary, nil
}

func (s *ReportService) GetAgentMetrics(accountID uint) ([]AgentMetric, error) {
	var members []domain.AccountUser
	if err := s.db.Preload("User").Where("account_id = ?", accountID).Find(&members).Error; err != nil {
		return nil, err
	}

	metrics := make([]AgentMetric, 0, len(members))
	for _, m := range members {
		if m.User == nil {
			continue
		}

		var assignedCount int64
		var resolvedCount int64

		s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id = ?", accountID, m.UserID).
			Count(&assignedCount)

		s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id = ? AND status = ?", accountID, m.UserID, domain.ConversationStatusResolved).
			Count(&resolvedCount)

		metrics = append(metrics, AgentMetric{
			UserID:                m.UserID,
			Name:                  m.User.Name,
			Email:                 m.User.Email,
			AssignedConversations: assignedCount,
			ResolvedConversations: resolvedCount,
		})
	}

	return metrics, nil
}
