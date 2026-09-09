package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type SLAService struct {
	db             *gorm.DB
	appliedSLARepo *repository.AppliedSLARepository
}

func NewSLAService(db *gorm.DB) *SLAService {
	return &SLAService{
		db:             db,
		appliedSLARepo: repository.NewAppliedSLARepository(db),
	}
}

func (s *SLAService) SetAppliedSLARepo(repo *repository.AppliedSLARepository) {
	s.appliedSLARepo = repo
}

func (s *SLAService) GetAppliedSLARepo() *repository.AppliedSLARepository {
	return s.appliedSLARepo
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

// CalculateDeadline calculates the deadline based on Inbox timezone and working hours
func (s *SLAService) CalculateDeadline(inbox *domain.Inbox, startTime time.Time, thresholdSeconds int) time.Time {
	return CalculateDeadline(inbox, startTime, thresholdSeconds)
}

// CalculateBusinessSeconds calculates total business seconds between startTime and endTime
func (s *SLAService) CalculateBusinessSeconds(inbox *domain.Inbox, startTime, endTime time.Time) int {
	return CalculateBusinessSeconds(inbox, startTime, endTime)
}

// EvaluateNextResponse analyzes conversation message history to calculate next response deadline and breach status
func (s *SLAService) EvaluateNextResponse(conv *domain.Conversation, effectiveInbox *domain.Inbox, threshold int, now time.Time) (nrtDeadline *time.Time, isNRTBreached bool, actualSec int, breachTime time.Time) {
	if threshold <= 0 || conv == nil {
		return nil, false, 0, time.Time{}
	}

	var messages []domain.Message
	_ = s.db.Where("conversation_id = ?", conv.ID).
		Order("created_at ASC, id ASC").
		Find(&messages).Error

	type turnMsg struct {
		isCustomer bool
		createdAt  time.Time
	}
	var seq []turnMsg
	for _, m := range messages {
		if m.MessageType == domain.MessageTypeIncoming || strings.EqualFold(m.SenderType, "contact") {
			seq = append(seq, turnMsg{isCustomer: true, createdAt: m.CreatedAt})
		} else if m.MessageType == domain.MessageTypeOutgoing && !m.Private && m.ContentType != "activity" && m.ContentType != "internal_note" && !strings.EqualFold(m.SenderType, "contact") {
			seq = append(seq, turnMsg{isCustomer: false, createdAt: m.CreatedAt})
		}
	}

	firstAgentIdx := -1
	for i, item := range seq {
		if !item.isCustomer {
			firstAgentIdx = i
			break
		}
	}

	// If no agent reply has occurred yet, Next Response Time does not apply (First Response Time is active)
	if firstAgentIdx == -1 {
		return nil, false, 0, time.Time{}
	}

	var pendingCustomerMsgTime *time.Time

	for _, item := range seq[firstAgentIdx+1:] {
		if item.isCustomer {
			if pendingCustomerMsgTime == nil {
				t := item.createdAt
				pendingCustomerMsgTime = &t
			}
		} else {
			// Agent replied
			if pendingCustomerMsgTime != nil {
				turnStart := *pendingCustomerMsgTime
				turnDeadline := s.CalculateDeadline(effectiveInbox, turnStart, threshold)
				turnSec := s.CalculateBusinessSeconds(effectiveInbox, turnStart, item.createdAt)
				if item.createdAt.After(turnDeadline) || turnSec > threshold {
					isNRTBreached = true
					actualSec = turnSec
					breachTime = item.createdAt
				}
				pendingCustomerMsgTime = nil
			}
		}
	}

	if pendingCustomerMsgTime != nil {
		turnStart := *pendingCustomerMsgTime
		due := s.CalculateDeadline(effectiveInbox, turnStart, threshold)
		nrtDeadline = &due
		elapsed := s.CalculateBusinessSeconds(effectiveInbox, turnStart, now)
		if !now.Before(due) || elapsed > threshold {
			isNRTBreached = true
			if actualSec == 0 || elapsed > actualSec {
				actualSec = elapsed
				breachTime = now
			}
		}
	}

	return nrtDeadline, isNRTBreached, actualSec, breachTime
}

// GetConversationSLADeadlines computes deadlines and current breach status for a single conversation
func (s *SLAService) GetConversationSLADeadlines(conv *domain.Conversation) (frtDeadline, nrtDeadline, resDeadline *time.Time, isFRTBreached, isNRTBreached, isResBreached bool, policy *domain.SLAPolicy) {
	if conv == nil {
		return nil, nil, nil, false, false, false, nil
	}

	var policies []domain.SLAPolicy
	_ = s.db.Where("account_id = ?", conv.AccountID).Find(&policies).Error
	applicablePolicy := s.resolvePolicy(conv, policies)
	if applicablePolicy.ID == 0 {
		return nil, nil, nil, false, false, false, nil
	}
	policy = &applicablePolicy

	if conv.Inbox == nil && conv.InboxID > 0 {
		var inbox domain.Inbox
		if err := s.db.Where("id = ?", conv.InboxID).First(&inbox).Error; err == nil {
			conv.Inbox = &inbox
		}
	}

	var effectiveInbox *domain.Inbox
	if applicablePolicy.OnlyDuringBusinessHours {
		effectiveInbox = conv.Inbox
	}

	now := time.Now().UTC()

	// 1. First Response Deadline
	startTime := conv.CreatedAt
	var firstCustomerMsg domain.Message
	if err := s.db.Where("conversation_id = ? AND (message_type = ? OR sender_type IN ('Contact', 'contact'))", conv.ID, domain.MessageTypeIncoming).
		Order("created_at ASC, id ASC").First(&firstCustomerMsg).Error; err == nil && !firstCustomerMsg.CreatedAt.IsZero() {
		startTime = firstCustomerMsg.CreatedAt
	}

	if applicablePolicy.FirstResponseTimeThreshold > 0 {
		fDue := s.CalculateDeadline(effectiveInbox, startTime, applicablePolicy.FirstResponseTimeThreshold)
		frtDeadline = &fDue

		var existingBreach int64
		_ = s.db.Model(&domain.SLABreachLog{}).
			Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "first_response").
			Count(&existingBreach).Error

		if existingBreach > 0 {
			isFRTBreached = true
		} else {
			var firstAgentMsg domain.Message
			err := s.db.Where("conversation_id = ? AND message_type = ? AND (private = 0 OR private = false OR private IS NULL) AND (content_type NOT IN ('activity', 'internal_note') OR content_type IS NULL) AND sender_type NOT IN ('Contact', 'contact') AND created_at >= ?",
				conv.ID, domain.MessageTypeOutgoing, startTime).
				Order("created_at ASC, id ASC").First(&firstAgentMsg).Error

			if err != nil || firstAgentMsg.CreatedAt.IsZero() {
				// No agent reply yet
				if !now.Before(fDue) {
					isFRTBreached = true
				}
			} else {
				// Agent replied: check if first reply was sent after deadline or elapsed business time exceeded threshold
				actualSec := s.CalculateBusinessSeconds(effectiveInbox, startTime, firstAgentMsg.CreatedAt)
				if firstAgentMsg.CreatedAt.After(fDue) || actualSec > applicablePolicy.FirstResponseTimeThreshold {
					isFRTBreached = true
				}
			}
		}
	}

	// 2. Next Response Deadline
	if applicablePolicy.NextResponseTimeThreshold > 0 {
		var existingNRTBreach int64
		_ = s.db.Model(&domain.SLABreachLog{}).
			Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "next_response").
			Count(&existingNRTBreach).Error

		nDue, breached, _, _ := s.EvaluateNextResponse(conv, effectiveInbox, applicablePolicy.NextResponseTimeThreshold, now)
		nrtDeadline = nDue
		if existingNRTBreach > 0 || breached {
			isNRTBreached = true
		}
	}

	// 3. Resolution Deadline
	if applicablePolicy.ResolutionTimeThreshold > 0 {
		rDue := s.CalculateDeadline(effectiveInbox, conv.CreatedAt, applicablePolicy.ResolutionTimeThreshold)
		resDeadline = &rDue

		var existingResBreach int64
		_ = s.db.Model(&domain.SLABreachLog{}).
			Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "resolution").
			Count(&existingResBreach).Error

		if existingResBreach > 0 {
			isResBreached = true
		} else if conv.Status != domain.ConversationStatusResolved {
			if !now.Before(rDue) {
				isResBreached = true
			}
		} else {
			actualResSec := s.CalculateBusinessSeconds(effectiveInbox, conv.CreatedAt, conv.UpdatedAt)
			if conv.UpdatedAt.After(rDue) || actualResSec > applicablePolicy.ResolutionTimeThreshold {
				isResBreached = true
			}
		}
	}

	return frtDeadline, nrtDeadline, resDeadline, isFRTBreached, isNRTBreached, isResBreached, policy
}

// EvaluateConversation evaluates SLA breaches for a single conversation and records logs/updates status
func (s *SLAService) EvaluateConversation(conv *domain.Conversation) ([]domain.SLABreachLog, error) {
	if conv == nil {
		return nil, nil
	}

	var policies []domain.SLAPolicy
	if err := s.db.Where("account_id = ?", conv.AccountID).Find(&policies).Error; err != nil {
		return nil, err
	}
	applicablePolicy := s.resolvePolicy(conv, policies)
	if applicablePolicy.ID == 0 {
		return nil, nil
	}

	if conv.Inbox == nil && conv.InboxID > 0 {
		var inbox domain.Inbox
		if err := s.db.Where("id = ?", conv.InboxID).First(&inbox).Error; err == nil {
			conv.Inbox = &inbox
		}
	}

	var effectiveInbox *domain.Inbox
	if applicablePolicy.OnlyDuringBusinessHours {
		effectiveInbox = conv.Inbox
	}

	now := time.Now().UTC()
	var breaches []domain.SLABreachLog

	var appliedSLA *domain.AppliedSLA
	if s.appliedSLARepo != nil {
		appliedSLA, _ = s.appliedSLARepo.EnsureAppliedSLA(conv.AccountID, conv.ID, applicablePolicy.ID)
	}

	// 1. First Response Time Evaluation
	startTime := conv.CreatedAt
	var firstCustomerMsg domain.Message
	if err := s.db.Where("conversation_id = ? AND (message_type = ? OR sender_type IN ('Contact', 'contact'))", conv.ID, domain.MessageTypeIncoming).
		Order("created_at ASC, id ASC").First(&firstCustomerMsg).Error; err == nil && !firstCustomerMsg.CreatedAt.IsZero() {
		startTime = firstCustomerMsg.CreatedAt
	}

	frtDeadline := s.CalculateDeadline(effectiveInbox, startTime, applicablePolicy.FirstResponseTimeThreshold)
	resDeadline := s.CalculateDeadline(effectiveInbox, conv.CreatedAt, applicablePolicy.ResolutionTimeThreshold)

	updates := map[string]any{
		"first_response_due_at": frtDeadline,
		"resolution_due_at":    resDeadline,
	}

	isFRTBreached := false
	if applicablePolicy.FirstResponseTimeThreshold > 0 {
		var firstAgentMsg domain.Message
		hasAgentReply := false
		err := s.db.Where("conversation_id = ? AND message_type = ? AND (private = 0 OR private = false OR private IS NULL) AND (content_type NOT IN ('activity', 'internal_note') OR content_type IS NULL) AND sender_type NOT IN ('Contact', 'contact') AND created_at >= ?",
			conv.ID, domain.MessageTypeOutgoing, startTime).
			Order("created_at ASC, id ASC").First(&firstAgentMsg).Error
		if err == nil && !firstAgentMsg.CreatedAt.IsZero() {
			hasAgentReply = true
		}

		var actualSec int
		var breachTime time.Time

		if !hasAgentReply {
			// No agent reply yet: check if current time has exceeded deadline
			if !now.Before(frtDeadline) {
				isFRTBreached = true
				actualSec = s.CalculateBusinessSeconds(effectiveInbox, startTime, now)
				breachTime = now
			}
		} else {
			// Agent has replied: check if first response was sent after deadline or duration exceeded threshold
			actualSec = s.CalculateBusinessSeconds(effectiveInbox, startTime, firstAgentMsg.CreatedAt)
			if firstAgentMsg.CreatedAt.After(frtDeadline) || actualSec > applicablePolicy.FirstResponseTimeThreshold {
				isFRTBreached = true
				breachTime = firstAgentMsg.CreatedAt
			}
		}

		if isFRTBreached {
			if appliedSLA != nil && s.appliedSLARepo != nil {
				_ = s.appliedSLARepo.RecordSLAEvent(&domain.SLAEvent{
					AppliedSLAID:   appliedSLA.ID,
					ConversationID: conv.ID,
					AccountID:      conv.AccountID,
					SLAPolicyID:    applicablePolicy.ID,
					InboxID:        conv.InboxID,
					EventType:      "frt",
					CreatedAt:      breachTime,
				})
			}

			var existing int64
			_ = s.db.Model(&domain.SLABreachLog{}).
				Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "first_response").
				Count(&existing).Error

			if existing == 0 {
				breach := domain.SLABreachLog{
					AccountID:        conv.AccountID,
					ConversationID:   conv.ID,
					SLAPolicyID:      applicablePolicy.ID,
					BreachType:       "first_response",
					ThresholdSeconds: applicablePolicy.FirstResponseTimeThreshold,
					ActualSeconds:    actualSec,
					CreatedAt:        breachTime,
				}
				if err := s.db.Create(&breach).Error; err == nil {
					breaches = append(breaches, breach)
					updates["sla_status"] = "breached"
				}
			} else {
				updates["sla_status"] = "breached"
				if hasAgentReply {
					_ = s.db.Model(&domain.SLABreachLog{}).
						Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "first_response").
						Update("actual_seconds", actualSec).Error
				}
			}
		}
	}

	// 2. Next Response Time Evaluation
	isNRTBreached := false
	if applicablePolicy.NextResponseTimeThreshold > 0 {
		nDue, breached, actualNRTSec, nrtBreachTime := s.EvaluateNextResponse(conv, effectiveInbox, applicablePolicy.NextResponseTimeThreshold, now)
		isNRTBreached = breached
		updates["next_response_due_at"] = nDue

		if isNRTBreached {
			if appliedSLA != nil && s.appliedSLARepo != nil {
				var lastIncomingMsg domain.Message
				_ = s.db.Where("conversation_id = ? AND (message_type = ? OR sender_type IN ('Contact', 'contact'))", conv.ID, domain.MessageTypeIncoming).
					Order("created_at DESC, id DESC").First(&lastIncomingMsg).Error
				nrtMeta := ""
				if lastIncomingMsg.ID > 0 {
					nrtMeta = fmt.Sprintf("{\"message_id\":%d}", lastIncomingMsg.ID)
				}
				_ = s.appliedSLARepo.RecordSLAEvent(&domain.SLAEvent{
					AppliedSLAID:   appliedSLA.ID,
					ConversationID: conv.ID,
					AccountID:      conv.AccountID,
					SLAPolicyID:    applicablePolicy.ID,
					InboxID:        conv.InboxID,
					EventType:      "nrt",
					Meta:           nrtMeta,
					CreatedAt:      nrtBreachTime,
				})
			}

			var existing int64
			_ = s.db.Model(&domain.SLABreachLog{}).
				Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "next_response").
				Count(&existing).Error

			if existing == 0 {
				breach := domain.SLABreachLog{
					AccountID:        conv.AccountID,
					ConversationID:   conv.ID,
					SLAPolicyID:      applicablePolicy.ID,
					BreachType:       "next_response",
					ThresholdSeconds: applicablePolicy.NextResponseTimeThreshold,
					ActualSeconds:    actualNRTSec,
					CreatedAt:        nrtBreachTime,
				}
				if err := s.db.Create(&breach).Error; err == nil {
					breaches = append(breaches, breach)
					updates["sla_status"] = "breached"
				}
			} else {
				updates["sla_status"] = "breached"
				if actualNRTSec > 0 {
					_ = s.db.Model(&domain.SLABreachLog{}).
						Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "next_response").
						Update("actual_seconds", actualNRTSec).Error
				}
			}
		}
	}

	// 3. Resolution Time Evaluation
	isResBreached := false
	if applicablePolicy.ResolutionTimeThreshold > 0 {
		var actualResSec int
		var resBreachTime time.Time

		if conv.Status != domain.ConversationStatusResolved {
			// Open conversation: check if current time has exceeded resolution deadline
			if !now.Before(resDeadline) {
				isResBreached = true
				actualResSec = s.CalculateBusinessSeconds(effectiveInbox, conv.CreatedAt, now)
				resBreachTime = now
			}
		} else {
			// Resolved conversation: check if resolved after deadline
			actualResSec = s.CalculateBusinessSeconds(effectiveInbox, conv.CreatedAt, conv.UpdatedAt)
			if conv.UpdatedAt.After(resDeadline) || actualResSec > applicablePolicy.ResolutionTimeThreshold {
				isResBreached = true
				resBreachTime = conv.UpdatedAt
			}
		}

		if isResBreached {
			if appliedSLA != nil && s.appliedSLARepo != nil {
				_ = s.appliedSLARepo.RecordSLAEvent(&domain.SLAEvent{
					AppliedSLAID:   appliedSLA.ID,
					ConversationID: conv.ID,
					AccountID:      conv.AccountID,
					SLAPolicyID:    applicablePolicy.ID,
					InboxID:        conv.InboxID,
					EventType:      "rt",
					CreatedAt:      resBreachTime,
				})
			}

			var existing int64
			_ = s.db.Model(&domain.SLABreachLog{}).
				Where("account_id = ? AND conversation_id = ? AND breach_type = ?", conv.AccountID, conv.ID, "resolution").
				Count(&existing).Error

			if existing == 0 {
				breach := domain.SLABreachLog{
					AccountID:        conv.AccountID,
					ConversationID:   conv.ID,
					SLAPolicyID:      applicablePolicy.ID,
					BreachType:       "resolution",
					ThresholdSeconds: applicablePolicy.ResolutionTimeThreshold,
					ActualSeconds:    actualResSec,
					CreatedAt:        resBreachTime,
				}
				if err := s.db.Create(&breach).Error; err == nil {
					breaches = append(breaches, breach)
					updates["sla_status"] = "breached"
					logger.WithComponent("sla").Warn("sla resolution breach recorded",
						"conversation_id", conv.ID,
						"account_id", conv.AccountID,
						"policy_id", applicablePolicy.ID,
						"threshold_seconds", applicablePolicy.ResolutionTimeThreshold,
						"actual_seconds", actualResSec,
					)
				}
			} else {
				updates["sla_status"] = "breached"
			}
		}
	}

	// 4. Status determination for AppliedSLA and Conversation
	hasAnyBreach := isFRTBreached || isNRTBreached || isResBreached
	if !hasAnyBreach && appliedSLA != nil {
		var count int64
		_ = s.db.Model(&domain.SLAEvent{}).Where("applied_sla_id = ?", appliedSLA.ID).Count(&count).Error
		if count > 0 {
			hasAnyBreach = true
		}
	}

	var appliedStatus string
	var convStatus string

	if conv.Status == domain.ConversationStatusResolved {
		if hasAnyBreach {
			appliedStatus = "missed"
			convStatus = "missed"
		} else {
			appliedStatus = "hit"
			convStatus = "hit"
		}
	} else {
		// Open, pending, snoozed
		if hasAnyBreach {
			appliedStatus = "active_with_misses"
			convStatus = "breached"
		} else {
			appliedStatus = "active"
			convStatus = "active"
		}
	}

	if appliedSLA != nil && s.appliedSLARepo != nil {
		_ = s.appliedSLARepo.UpdateSLAStatus(conv.AccountID, appliedSLA.ID, appliedStatus)
	}
	updates["sla_status"] = convStatus

	_ = s.db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Updates(updates).Error
	return breaches, nil
}

// EvaluateAccountSLAs scans open (and unbreached resolved) conversations and logs breaches according to applicable SLA policies
func (s *SLAService) EvaluateAccountSLAs(accountID uint) ([]domain.SLABreachLog, error) {
	var conversations []domain.Conversation
	err := s.db.Preload("Inbox").
		Where("account_id = ? AND (status != ? OR sla_status != 'breached' OR sla_status IS NULL)", accountID, domain.ConversationStatusResolved).
		Find(&conversations).Error
	if err != nil {
		return nil, err
	}

	var allBreaches []domain.SLABreachLog
	for _, conv := range conversations {
		breaches, err := s.EvaluateConversation(&conv)
		if err == nil && len(breaches) > 0 {
			allBreaches = append(allBreaches, breaches...)
		}
	}
	return allBreaches, nil
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
	logger.WithComponent("sla").Info("sla periodic evaluation executed",
		"accounts_count", len(accounts),
		"breaches_count", totalBreaches,
	)
	return totalBreaches
}
