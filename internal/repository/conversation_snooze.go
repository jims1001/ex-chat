package repository

import (
	"context"
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Recheck the scanned version and deadline under the owner row lock. Never
// rebase a stale scan onto a manual edit, resolution, deletion or re-snooze.
func (r *ConversationRepository) resumeExpiredSnoozedCandidate(ctx context.Context, candidate domain.Conversation, cutoff time.Time) (*domain.Conversation, error) {
	var current domain.Conversation
	committed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_id = ? AND id = ?", candidate.AccountID, candidate.ID).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.ObjectVersion != candidate.ObjectVersion ||
			current.Status != domain.ConversationStatusSnoozed ||
			current.SnoozedUntil == nil || current.SnoozedUntil.After(cutoff) {
			return nil
		}
		if err := r.applyConversationAutomationUpdate(tx, &current, map[string]any{
			"status":           domain.ConversationStatusOpen,
			"snoozed_until":    nil,
			"last_activity_at": cutoff,
		}, []string{"status", "snoozed_until", "last_activity_at"}); err != nil {
			return err
		}
		// GORM may retain a previous non-nil pointer when reloading SQL NULL
		// into an existing struct. The successful update cleared this deadline.
		current.SnoozedUntil = nil
		committed = true
		return nil
	})
	if err != nil || !committed {
		return nil, err
	}
	return &current, nil
}
