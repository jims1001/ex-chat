package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ObserveRetryActivity is an owner-only snapshot, frozen before MSG acceptance.
func (r *ConversationRepository) ObserveRetryActivity(ctx context.Context, accountID, conversationID uint) (int, string, error) {
	var c domain.Conversation
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ? AND status <> ?", accountID, conversationID, domain.ConversationStatusDeleting).First(&c).Error
	if err != nil {
		return 0, "", err
	}
	if c.ObjectVersion < 1 || !conversationAutomationStatus(c.Status) {
		return 0, "", gorm.ErrInvalidData
	}
	return c.ObjectVersion, c.Status, nil
}

// ApplyRetryActivity is deliberately separate from creation activity: retries
// never add unread/participants. Legacy retry reopens incoming/resolved even if
// muted or blocked; CUS is not consulted. Receipt and mutation commit atomically.
// It emits no second CON fact; the verified MSG retry fact owns the alias batch.
func (r *ConversationRepository) ApplyRetryActivity(ctx context.Context, i RetryActivityIntent) (RetryActivityReceipt, error) {
	var receipt RetryActivityReceipt
	if !validRetryIntent(i) {
		return receipt, gorm.ErrInvalidData
	}
	key := retryIdentity("con-retry", i.AttemptID)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c domain.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ? AND status <> ?", i.AccountID, i.ConversationID, domain.ConversationStatusDeleting).First(&c).Error; err != nil {
			return err
		}
		var prior domain.LocalChangeJournal
		err := tx.Where("change_id = ?", key).First(&prior).Error
		if err == nil {
			if json.Unmarshal([]byte(prior.AfterState), &receipt) != nil || !validRetryReceipt(receipt, i) || prior.SourceModule != "CON" || prior.AccountID != i.AccountID || prior.ObjectType != "Conversation" || prior.ObjectID != i.ConversationID || prior.Action != "retry_activity" || prior.SourceType != messageRetryProfile || prior.CommandID != i.AttemptID || prior.CorrelationID != i.AttemptID || prior.CausationID != i.AttemptID || prior.ActorID != i.ActorID || prior.Status != "published" || prior.ChangedFields != retryReceiptFields(receipt) || prior.ObjectVersionBefore != receipt.VersionBefore || prior.ObjectVersionAfter != receipt.VersionAfter || prior.Result != receipt.Result || !prior.OccurredAt.Equal(i.OccurredAt) {
				return gorm.ErrInvalidData
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		receipt = RetryActivityReceipt{Intent: i, Result: "applied", VersionBefore: i.ExpectedVersion, VersionAfter: i.ExpectedVersion + 1, StatusBefore: i.ObservedStatus, StatusAfter: i.ObservedStatus}
		fields := "last_activity_at"
		if c.ObjectVersion != i.ExpectedVersion {
			// Never adopt a newer manual version. AUT gets the original stale target,
			// not an invented success at the current conversation version.
			if c.ObjectVersion < i.ExpectedVersion {
				return ErrConversationVersionConflict
			}
			receipt.Result, receipt.VersionAfter = "stale", i.ExpectedVersion
			fields = ""
		} else {
			if c.Status != i.ObservedStatus {
				return gorm.ErrInvalidData
			}
			updates := map[string]any{"object_version": i.ExpectedVersion + 1}
			if c.LastActivityAt.Before(i.OccurredAt) {
				updates["last_activity_at"] = i.OccurredAt
			}
			if i.MessageType == domain.MessageTypeIncoming && c.Status == domain.ConversationStatusResolved {
				updates["status"], updates["snoozed_until"] = domain.ConversationStatusOpen, nil
				receipt.StatusAfter = domain.ConversationStatusOpen
				fields += ",status,snoozed_until"
			}
			result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND object_version = ? AND status = ?", i.AccountID, i.ConversationID, i.ExpectedVersion, i.ObservedStatus).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrConversationVersionConflict
			}
		}
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		return NewJournalRepository(tx).RecordLocalChange(tx, &domain.LocalChangeJournal{ChangeID: key, SourceModule: "CON", AccountID: i.AccountID, ObjectType: "Conversation", ObjectID: i.ConversationID, ObjectVersionBefore: receipt.VersionBefore, ObjectVersionAfter: receipt.VersionAfter, Action: "retry_activity", SourceType: messageRetryProfile, CommandID: i.AttemptID, CorrelationID: i.AttemptID, CausationID: i.AttemptID, ActorID: i.ActorID, ChangedFields: fields, AfterState: string(raw), OccurredAt: i.OccurredAt, Result: receipt.Result})
	})
	return receipt, err
}

func retryReceiptFields(r RetryActivityReceipt) string {
	if r.Result == "stale" {
		return ""
	}
	if r.StatusBefore != r.StatusAfter {
		return "last_activity_at,status,snoozed_until"
	}
	return "last_activity_at"
}
