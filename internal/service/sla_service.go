package service

import (
	"context"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type SLAService struct {
	db *gorm.DB
}

func NewSLAService(db *gorm.DB) *SLAService {
	return &SLAService{db: db}
}

// resolvePolicy selects the most specific SLA policy for a conversation
func (s *SLAService) resolvePolicy(conv *domain.Conversation, policies []domain.SLAPolicy) domain.SLAPolicy {
	if len(policies) == 0 {
		return domain.SLAPolicy{}
	}

	// 0. Match explicit SLA policy ID if configured
	if conv.SLAPolicyID != nil && *conv.SLAPolicyID > 0 {
		for _, p := range policies {
			if p.ID == *conv.SLAPolicyID {
				return p
			}
		}
	}

	// 1. Try matching conversation priority in policy name or description
	convPriority := strings.ToLower(conv.Priority)
	for _, p := range policies {
		combined := strings.ToLower(p.Name + " " + p.Description)
		if convPriority != "" && strings.Contains(combined, convPriority) {
			return p
		}
	}

	// 2. Default to first policy
	return policies[0]
}

// EvaluateAccountSLAs scans open conversations and logs breaches according to applicable SLA policies
func (s *SLAService) EvaluateAccountSLAs(accountID uint) ([]domain.SLABreachLog, error) {
	var policies []domain.SLAPolicy
	if err := s.db.Where("account_id = ?", accountID).Find(&policies).Error; err != nil {
		return nil, err
	}
	if len(policies) == 0 {
		return nil, nil
	}

	var openConversations []domain.Conversation
	err := s.db.Where("account_id = ? AND status != ?", accountID, domain.ConversationStatusResolved).
		Find(&openConversations).Error
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var breaches []domain.SLABreachLog

	for _, conv := range openConversations {
		elapsedSeconds := int(now.Sub(conv.CreatedAt).Seconds())
		applicablePolicy := s.resolvePolicy(&conv, policies)
		if applicablePolicy.ID == 0 {
			continue
		}

		// 1. Check First Response Time (strictly ignore internal private notes / activity notes)
		var agentMsgCount int64
		_ = s.db.Model(&domain.Message{}).
			Where("conversation_id = ? AND message_type = ? AND (private = 0 OR private = false OR private IS NULL) AND (content_type NOT IN ('activity', 'internal_note') OR content_type IS NULL) AND sender_type NOT IN ('Contact', 'contact')",
				conv.ID, domain.MessageTypeOutgoing).
			Count(&agentMsgCount).Error

		startTime := conv.CreatedAt
		var firstCustomerMsg domain.Message
		if err := s.db.Where("conversation_id = ? AND (message_type = ? OR sender_type IN ('Contact', 'contact'))", conv.ID, domain.MessageTypeIncoming).
			Order("created_at ASC, id ASC").First(&firstCustomerMsg).Error; err == nil && !firstCustomerMsg.CreatedAt.IsZero() {
			startTime = firstCustomerMsg.CreatedAt
		}
		firstResponseElapsed := int(now.Sub(startTime).Seconds())

		if agentMsgCount == 0 && applicablePolicy.FirstResponseTimeThreshold > 0 && firstResponseElapsed >= applicablePolicy.FirstResponseTimeThreshold {
			var existing int64
			_ = s.db.Model(&domain.SLABreachLog{}).
				Where("account_id = ? AND conversation_id = ? AND breach_type = ?", accountID, conv.ID, "first_response").
				Count(&existing).Error

			if existing == 0 {
				breach := domain.SLABreachLog{
					AccountID:        accountID,
					ConversationID:   conv.ID,
					SLAPolicyID:      applicablePolicy.ID,
					BreachType:       "first_response",
					ThresholdSeconds: applicablePolicy.FirstResponseTimeThreshold,
					ActualSeconds:    firstResponseElapsed,
					CreatedAt:        now,
				}
				if err := s.db.Create(&breach).Error; err == nil {
					breaches = append(breaches, breach)
					_ = s.db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Update("sla_status", "breached").Error
				}
			}
		}

		// 2. Check Resolution Time
		if applicablePolicy.ResolutionTimeThreshold > 0 && elapsedSeconds > applicablePolicy.ResolutionTimeThreshold {
			var existing int64
			_ = s.db.Model(&domain.SLABreachLog{}).
				Where("account_id = ? AND conversation_id = ? AND breach_type = ?", accountID, conv.ID, "resolution").
				Count(&existing).Error

			if existing == 0 {
				breach := domain.SLABreachLog{
					AccountID:        accountID,
					ConversationID:   conv.ID,
					SLAPolicyID:      applicablePolicy.ID,
					BreachType:       "resolution",
					ThresholdSeconds: applicablePolicy.ResolutionTimeThreshold,
					ActualSeconds:    elapsedSeconds,
					CreatedAt:        now,
				}
				if err := s.db.Create(&breach).Error; err == nil {
					breaches = append(breaches, breach)
					_ = s.db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Update("sla_status", "breached").Error
				}
			}
		}
	}

	return breaches, nil
}

// ListBreaches returns recorded SLA breach logs for auditing
func (s *SLAService) ListBreaches(accountID uint) ([]domain.SLABreachLog, error) {
	var logs []domain.SLABreachLog
	err := s.db.Where("account_id = ?", accountID).Order("id DESC").Find(&logs).Error
	return logs, err
}

// StartSLAScheduler periodically processes SLA breaches across all accounts
func (s *SLAService) StartSLAScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				s.ProcessAllBreaches()
			}
		}
	}()
}

// ProcessAllBreaches runs SLA checks across all tenant accounts
func (s *SLAService) ProcessAllBreaches() int {
	var accounts []domain.Account
	if err := s.db.Find(&accounts).Error; err != nil {
		return 0
	}
	totalBreaches := 0
	for _, acc := range accounts {
		if b, err := s.EvaluateAccountSLAs(acc.ID); err == nil {
			totalBreaches += len(b)
		}
	}
	return totalBreaches
}
