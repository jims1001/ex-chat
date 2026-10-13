package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const messageRetryProfile = "msg.retry_attempt.v1"

var ErrMessageNotRetryable = errors.New("only undeleted failed messages or an exact accepted retry can be retried")

// RetryActivityIntent contains no body or provider response. Sequence is a
// per-message accepted-attempt sequence, NOT a Message object version.
type RetryActivityIntent struct {
	Profile         string    `json:"profile"`
	AttemptID       string    `json:"attempt_id"`
	Sequence        int       `json:"sequence"`
	AccountID       uint      `json:"account_id"`
	ConversationID  uint      `json:"conversation_id"`
	MessageID       uint      `json:"message_id"`
	ActorID         uint      `json:"actor_id"`
	MessageType     string    `json:"message_type"`
	Private         bool      `json:"private"`
	SenderType      string    `json:"sender_type"`
	ContentType     string    `json:"content_type"`
	Outcome         string    `json:"local_outcome"`
	OccurredAt      time.Time `json:"occurred_at"`
	ExpectedVersion int       `json:"expected_version"`
	ObservedStatus  string    `json:"observed_status"`
}

type RetryActivityReceipt struct {
	Intent        RetryActivityIntent `json:"intent"`
	Result        string              `json:"result"`
	VersionBefore int                 `json:"version_before"`
	VersionAfter  int                 `json:"version_after"`
	StatusBefore  string              `json:"status_before"`
	StatusAfter   string              `json:"status_after"`
}

type messageRetryActivityPort interface {
	ObserveRetryActivity(context.Context, uint, uint) (int, string, error)
	ApplyRetryActivity(context.Context, RetryActivityIntent) (RetryActivityReceipt, error)
}

// Public outgoing work is pending transport. Incoming/activity records and
// private notes are local records: accepting their retry never resends them.
func retryLocalOutcome(messageType string, private bool) string {
	if private || messageType != domain.MessageTypeOutgoing {
		return domain.MessageStatusSaved
	}
	return domain.MessageStatusQueued
}
func retryIdentity(prefix, attempt string) string {
	sum := sha256.Sum256([]byte(prefix + ":" + attempt))
	return prefix + "-" + hex.EncodeToString(sum[:])[:40]
}
func validRetryIntent(i RetryActivityIntent) bool {
	return i.Profile == messageRetryProfile && validRetryUUID(i.AttemptID) && i.Sequence > 0 && i.AccountID > 0 && i.ConversationID > 0 && i.MessageID > 0 && i.ExpectedVersion > 0 && conversationAutomationStatus(i.ObservedStatus) && !i.OccurredAt.IsZero() && i.Outcome == retryLocalOutcome(i.MessageType, i.Private) && (i.MessageType == domain.MessageTypeIncoming || i.MessageType == domain.MessageTypeOutgoing || i.MessageType == domain.MessageTypeActivity)
}

// RetryMessageAccepted commits only local acceptance. accepted=false is an exact
// replay of an already accepted durable attempt and must not repeat best-effort
// WS/webhook/push callbacks. No cross-owner call occurs inside the MSG transaction.
func (r *MessageRepository) RetryMessageAccepted(ctx context.Context, accountID, conversationID, messageID uint) (*domain.Message, bool, error) {
	return r.retryMessageAccepted(ctx, accountID, conversationID, messageID, nil)
}

func (r *MessageRepository) retryMessageAccepted(ctx context.Context, accountID, conversationID, messageID uint, outbound *domain.OutboundMessageAttempt) (*domain.Message, bool, error) {
	var version int
	var status string
	var owner messageRetryActivityPort
	if r.automationCapture {
		var ok bool
		owner, ok = r.conversation.(messageRetryActivityPort)
		if !ok {
			return nil, false, gorm.ErrInvalidData
		}
		// A response-loss replay can return its local receipt during a CON outage.
		var current domain.Message
		if err := r.db.WithContext(ctx).Where("account_id = ? AND conversation_id = ? AND id = ?", accountID, conversationID, messageID).First(&current).Error; err != nil {
			return nil, false, err
		}
		if current.Status != domain.MessageStatusFailed {
			if replay, _, err := checkOutboundRetry(r.db.WithContext(ctx), current, outbound); err != nil {
				return nil, false, err
			} else if replay {
				return &current, false, nil
			}
			if err := r.acceptedRetryReplay(r.db.WithContext(ctx), current); err != nil {
				return nil, false, err
			}
			return &current, false, nil
		}
		var err error
		version, status, err = owner.ObserveRetryActivity(ctx, accountID, conversationID)
		if err != nil {
			return nil, false, err
		}
	}
	var message domain.Message
	var intent RetryActivityIntent
	accepted := false
	db := r.db.WithContext(ctx)
	if outbound != nil {
		db = r.outboundDB(ctx)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND conversation_id = ? AND id = ?", accountID, conversationID, messageID).First(&message).Error; err != nil {
			return err
		}
		if message.Deleted {
			return ErrMessageNotRetryable
		}
		replay, outboundSequence, err := checkOutboundRetry(tx, message, outbound)
		if err != nil {
			return err
		}
		if replay {
			return nil
		}
		if message.Status != domain.MessageStatusFailed {
			if r.automationCapture {
				return r.acceptedRetryReplay(tx, message)
			}
			return ErrMessageNotRetryable
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		outcome := retryLocalOutcome(message.MessageType, message.Private)
		result := tx.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ? AND id = ? AND status = ? AND deleted = ?", accountID, conversationID, messageID, domain.MessageStatusFailed, false).Updates(map[string]any{"status": outcome, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrMessageNotRetryable
		}
		message.Status, message.UpdatedAt = outcome, now
		if outbound != nil {
			if err := persistOutboundIntent(tx, &message, outbound, outboundSequence); err != nil {
				return err
			}
		}
		if r.automationCapture {
			var sequence int
			if err := tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ?", "MSG", accountID, "Message", messageID, "retry_attempt").Select("COALESCE(MAX(object_version_after), 0)").Scan(&sequence).Error; err != nil {
				return err
			}
			intent = RetryActivityIntent{Profile: messageRetryProfile, AttemptID: foundation.GenerateUUID(), Sequence: sequence + 1, AccountID: accountID, ConversationID: conversationID, MessageID: messageID, ActorID: message.SenderID, MessageType: message.MessageType, Private: message.Private, SenderType: message.SenderType, ContentType: message.ContentType, Outcome: outcome, OccurredAt: now, ExpectedVersion: version, ObservedStatus: status}
			if !validRetryIntent(intent) {
				return gorm.ErrInvalidData
			}
			raw, err := json.Marshal(intent)
			if err != nil {
				return err
			}
			attempt := domain.LocalChangeJournal{ChangeID: intent.AttemptID, SourceModule: "MSG", AccountID: accountID, ObjectType: "Message", ObjectID: messageID, ObjectVersionBefore: sequence, ObjectVersionAfter: sequence + 1, Action: "retry_attempt", SourceType: messageRetryProfile, CommandID: intent.AttemptID, CorrelationID: intent.AttemptID, AfterState: string(raw), ChangedFields: "status", Status: "pending", Result: outcome, OccurredAt: now}
			if err := NewJournalRepository(tx).RecordLocalChange(tx, &attempt); err != nil {
				return err
			}
			event := retryAttemptEvent(intent)
			eventRaw, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if err := NewJournalRepository(tx).RecordLocalChange(tx, &domain.LocalChangeJournal{ChangeID: event.EventID, SourceModule: "MSG", AccountID: accountID, ObjectType: "Message", ObjectID: messageID, ObjectVersionAfter: intent.Sequence, Action: "automation_trigger", SourceType: event.EventType, AfterState: string(eventRaw), EventID: event.EventID, CausationID: intent.AttemptID, CorrelationID: intent.AttemptID, Status: "pending", Result: outcome, OccurredAt: now}); err != nil {
				return err
			}
		}
		accepted = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if accepted && r.automationCapture {
		if _, err := r.completeRetryActivity(ctx, intent); err != nil {
			// The MSG commit is successful. The capture source recovers only local CON
			// activity/AUT; it does not replay unaudited external fanout.
			logger.WithComponent("message").Error("accepted retry activity pending", "message_id", message.ID, "error", err.Error())
		}
	}
	return &message, accepted, nil
}

func (r *MessageRepository) acceptedRetryReplay(db *gorm.DB, message domain.Message) error {
	if message.Deleted {
		return ErrMessageNotRetryable
	}
	var row domain.LocalChangeJournal
	if err := db.Where("source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ?", "MSG", message.AccountID, "Message", message.ID, "retry_attempt").Order("object_version_after DESC").First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMessageNotRetryable
		}
		return err
	}
	intent, err := parseRetryAttempt(row)
	if err != nil {
		return err
	}
	if message.ConversationID != intent.ConversationID || !retryMessageMetadataMatches(message, intent) || message.Status != intent.Outcome || !message.UpdatedAt.Equal(intent.OccurredAt) || row.Status == "cancelled" {
		return ErrMessageNotRetryable
	}
	return nil
}
func parseRetryAttempt(row domain.LocalChangeJournal) (RetryActivityIntent, error) {
	var i RetryActivityIntent
	if json.Unmarshal([]byte(row.AfterState), &i) != nil || !validRetryIntent(i) || row.SourceModule != "MSG" || row.SourceType != messageRetryProfile || row.Action != "retry_attempt" || row.AccountID != i.AccountID || row.ObjectType != "Message" || row.ObjectID != i.MessageID || row.ChangeID != i.AttemptID || row.CommandID != i.AttemptID || row.CorrelationID != i.AttemptID || row.ObjectVersionAfter != i.Sequence || row.ObjectVersionBefore != i.Sequence-1 || row.Result != i.Outcome || (row.Status != "pending" && row.Status != "delivered" && row.Status != "cancelled") || !row.OccurredAt.Equal(i.OccurredAt) {
		return i, gorm.ErrInvalidData
	}
	return i, nil
}
func retryAttemptEvent(i RetryActivityIntent) foundation.EventEnvelope {
	return foundation.EventEnvelope{EventID: retryIdentity("msg-retry", i.AttemptID), EventType: "msg.message_status.changed.v1", EventVersion: "v1", SourceModule: "MSG", AccountID: i.AccountID, AggregateType: "Message", AggregateID: strconv.FormatUint(uint64(i.MessageID), 10), AggregateVersion: uint64(i.Sequence), OccurredAt: i.OccurredAt, PublishedAt: i.OccurredAt, Actor: foundation.ContractActor{Type: "System", ID: "monolith", AuthSource: "module"}, CorrelationID: i.AttemptID, CausationID: i.AttemptID, ChangedFields: []string{"status"}, Payload: map[string]any{"operation": "retry", "retry_profile": messageRetryProfile, "retry_attempt_id": i.AttemptID, "retry_sequence": i.Sequence, "message_id": i.MessageID, "conversation_id": i.ConversationID, "message_type": i.MessageType, "private": i.Private, "sender_type": i.SenderType, "sender_id": i.ActorID, "content_type": i.ContentType, "status": i.Outcome, "local_outcome": i.Outcome}, DataClassification: domain.ClassificationInternal}
}
func (r *MessageRepository) completeRetryActivity(ctx context.Context, i RetryActivityIntent) (RetryActivityReceipt, error) {
	owner, ok := r.conversation.(messageRetryActivityPort)
	if !ok {
		return RetryActivityReceipt{}, gorm.ErrInvalidData
	}
	receipt, err := owner.ApplyRetryActivity(ctx, i)
	if err != nil {
		return receipt, err
	}
	if !validRetryReceipt(receipt, i) {
		return receipt, gorm.ErrInvalidData
	}
	err = r.db.WithContext(ctx).Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND change_id = ? AND action = ? AND status = ?", "MSG", i.AccountID, i.AttemptID, "retry_attempt", "pending").Update("status", "delivered").Error
	return receipt, err
}
func validRetryReceipt(r RetryActivityReceipt, i RetryActivityIntent) bool {
	if !reflect.DeepEqual(r.Intent, i) || r.VersionBefore != i.ExpectedVersion || r.StatusBefore != i.ObservedStatus {
		return false
	}
	if r.Result == "stale" {
		return r.VersionAfter == i.ExpectedVersion && r.StatusAfter == i.ObservedStatus
	}
	if r.Result != "applied" || r.VersionAfter != i.ExpectedVersion+1 {
		return false
	}
	after := i.ObservedStatus
	if i.MessageType == domain.MessageTypeIncoming && after == domain.ConversationStatusResolved {
		after = domain.ConversationStatusOpen
	}
	return r.StatusAfter == after
}
func (r *MessageRepository) prepareRetryAutomationMessage(ctx context.Context, row domain.LocalChangeJournal, event *foundation.EventEnvelope) (bool, error) {
	var attempt domain.LocalChangeJournal
	if err := r.db.WithContext(ctx).Where("source_module = ? AND account_id = ? AND change_id = ? AND action = ?", "MSG", row.AccountID, event.CausationID, "retry_attempt").First(&attempt).Error; err != nil {
		return false, err
	}
	i, err := parseRetryAttempt(attempt)
	if err != nil {
		return false, err
	}
	expected := retryAttemptEvent(i)
	left, _ := json.Marshal(expected)
	right, _ := json.Marshal(event)
	if string(left) != string(right) || row.ObjectID != i.MessageID || row.ObjectVersionAfter != i.Sequence || row.CorrelationID != i.AttemptID || row.Result != i.Outcome || row.SourceType != event.EventType || !row.OccurredAt.Equal(i.OccurredAt) {
		return false, gorm.ErrInvalidData
	}
	if attempt.Status == "cancelled" {
		return false, gorm.ErrRecordNotFound
	}
	var message domain.Message
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ? AND conversation_id = ? AND deleted = ?", i.AccountID, i.MessageID, i.ConversationID, false).First(&message).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = r.cancelRetryAttempt(ctx, i)
		}
		return false, err
	}
	if !retryMessageMetadataMatches(message, i) {
		return false, gorm.ErrInvalidData
	}
	receipt, err := r.completeRetryActivity(ctx, i)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		_ = r.cancelRetryAttempt(ctx, i)
	}
	if err != nil {
		return false, err
	}
	event.Payload["object_version"] = receipt.VersionAfter
	event.Payload["retry_receipt_verified"] = true
	event.Payload["retry_expected_version"] = i.ExpectedVersion
	event.Payload["retry_observed_status"] = receipt.StatusBefore
	event.Payload["retry_activity_result"] = receipt.Result
	if receipt.StatusBefore != receipt.StatusAfter {
		event.Payload["status_before"], event.Payload["status_after"] = receipt.StatusBefore, receipt.StatusAfter
	}
	return true, nil
}
func (r *MessageRepository) cancelRetryAttempt(ctx context.Context, i RetryActivityIntent) error {
	// Existing messages are never deleted or reverted by retry recovery.
	return r.db.WithContext(ctx).Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND change_id = ? AND action = ?", "MSG", i.AccountID, i.AttemptID, "retry_attempt").Update("status", "cancelled").Error
}

func validRetryUUID(s string) bool { _, err := uuid.Parse(s); return err == nil }

func retryMessageMetadataMatches(m domain.Message, i RetryActivityIntent) bool {
	return m.MessageType == i.MessageType && m.Private == i.Private && m.SenderType == i.SenderType && m.SenderID == i.ActorID && m.ContentType == i.ContentType
}
