package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrMessageConversationDeleted prevents a stale remote authorization from
// recreating MSG state after this owner's conversation cleanup has committed.
var ErrMessageConversationDeleted = errors.New("message conversation was deleted")

// LockMessageConversation serializes message creation with conversation cleanup
// in the MSG store. It must run in the same open transaction as the protected
// writes. A deleting call permanently tombstones the target; an ordinary call
// rejects an existing tombstone. No CON query or process-local lock is involved.
//
// The stable LocalChangeJournal row is owner state, not an outbox item. It stays
// in owner snapshots after all message rows have gone, so restored instances
// cannot resurrect a deleted conversation from a previously issued grant.
func LockMessageConversation(tx *gorm.DB, accountID, conversationID uint, deleting bool) error {
	if tx == nil || accountID == 0 || conversationID == 0 {
		return gorm.ErrInvalidData
	}
	if tx.Statement == nil {
		return gorm.ErrInvalidTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return gorm.ErrInvalidTransaction
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("MSG:conversation:%d:%d", accountID, conversationID)))
	key := "msg-con-fence-" + hex.EncodeToString(sum[:])[:48]
	now := time.Now().UTC()
	row := domain.LocalChangeJournal{ChangeID: key, SourceModule: "MSG", AccountID: accountID, ObjectType: "ConversationMessageFence", ObjectID: conversationID, Action: "message_conversation_fence", ActorType: "System", SourceType: "owner_fence", Status: "active", Result: "applied", DataClassification: domain.ClassificationInternal, RetentionClass: domain.RetentionBusinessChange, OccurredAt: now, RecordedAt: now, IngestedAt: now, CreatedAt: now}
	// On PostgreSQL this waits for an in-flight insert of the same key before
	// taking its row lock. SQLite obtains its writer lock before reading the row,
	// preventing a read/check/write race between separate database connections.
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("change_id = ?", key).First(&row).Error; err != nil {
		return err
	}
	if row.SourceModule != "MSG" || row.AccountID != accountID || row.ObjectType != "ConversationMessageFence" || row.ObjectID != conversationID || row.Action != "message_conversation_fence" {
		return gorm.ErrInvalidData
	}
	if row.Status == "deleted" {
		if deleting {
			return nil
		}
		return ErrMessageConversationDeleted
	}
	if row.Status != "active" {
		return gorm.ErrInvalidData
	}
	if !deleting {
		return nil
	}
	result := tx.Model(&domain.LocalChangeJournal{}).Where("id = ? AND change_id = ? AND status = ?", row.ID, key, "active").Updates(map[string]any{"status": "deleted", "result": "deleted", "recorded_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrInvalidData
	}
	return nil
}
