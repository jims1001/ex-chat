package repository

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// LockOrdinaryEmailAccount serializes managed admission with owner cleanup.
// Lock order is account, conversation tombstone (when present), then intents.
// Call only inside the transaction that performs those writes.
func LockOrdinaryEmailAccount(tx *gorm.DB, accountID uint) error {
	if tx == nil || accountID == 0 {
		return gorm.ErrInvalidData
	}
	if tx.Statement == nil {
		return gorm.ErrInvalidTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return gorm.ErrInvalidTransaction
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("msg:ordinary-email-account:%d", accountID)))
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(binary.BigEndian.Uint64(h[:8]))).Error
}

var errOrdinaryEmailSubmissionInFlight = errors.New("ordinary email submission is still in flight")

func cleanupOrdinaryEmailConversation(tx *gorm.DB, accountID, conversationID uint) (err error) {
	defer func() {
		if err != nil && !errors.Is(err, errOrdinaryEmailSubmissionInFlight) {
			err = errors.New("ordinary email cleanup checkpoint is unavailable")
		}
	}()
	tx = tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var logIDs []uint
	if err := tx.Model(&domain.EmailLog{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Pluck("id", &logIDs).Error; err != nil {
		return err
	}
	if len(logIDs) == 0 {
		return nil
	}
	var intents []domain.OrdinaryEmailIntent
	q := tx.Where("account_id = ? AND email_log_id IN ?", accountID, logIDs).Order("id ASC")
	if tx.Dialector.Name() == "postgres" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Find(&intents).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	ids := make([]uint, 0, len(intents))
	for _, item := range intents {
		if item.State == "submitting" && item.LeaseUntil != nil && item.LeaseUntil.After(now) {
			return errOrdinaryEmailSubmissionInFlight
		}
		ids = append(ids, item.ID)
	}
	// Hard deletion is this existing conversation lifecycle's contract. Removing
	// the durable lease fences stale callbacks; it cannot retract accepted mail.
	if len(ids) > 0 {
		if err := tx.Where("account_id = ? AND intent_id IN ?", accountID, ids).Delete(&domain.OrdinaryEmailAttempt{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND id IN ?", accountID, ids).Delete(&domain.OrdinaryEmailIntent{}).Error; err != nil {
			return err
		}
	}
	displayIDs := make([]string, 0, len(logIDs))
	for _, id := range logIDs {
		displayIDs = append(displayIDs, strconv.FormatUint(uint64(id), 10))
	}
	return tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND object_type = ? AND action = ? AND status = ? AND object_display_id IN ?", "MSG", accountID, "EmailLog", "event_outbox", "pending", displayIDs).Updates(map[string]any{"status": "superseded", "result": "rejected"}).Error
}
