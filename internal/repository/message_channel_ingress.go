package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrChannelReplayConflict = errors.New("provider event conflicts with durable receipt")

func (r *MessageRepository) HasChannelMessage(ctx context.Context, key, digest string, accountID uint) (bool, error) {
	var receipt domain.ChannelMessageReceipt
	err := r.db.WithContext(ctx).First(&receipt, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if receipt.AccountID != accountID || receipt.Digest != digest {
		return false, ErrChannelReplayConflict
	}
	return true, nil
}

// CreateChannelMessage atomically commits the replay receipt, text and MSG
// journal. Cross-owner activity follows the existing durable reconciliation path.
func (r *MessageRepository) CreateChannelMessage(ctx context.Context, key, digest string, msg *domain.Message) (bool, error) {
	if key == "" || digest == "" || msg == nil || r.conversation == nil {
		return false, gorm.ErrInvalidData
	}
	exists, err := r.conversation.MessageTargetExists(msg.AccountID, msg.ConversationID)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, gorm.ErrRecordNotFound
	}
	created := false
	sum := sha256.Sum256([]byte("channel-message:" + key))
	changeID := hex.EncodeToString(sum[:])
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		receipt := domain.ChannelMessageReceipt{Key: key, AccountID: msg.AccountID, Digest: digest}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.First(&receipt, "key = ?", key).Error; err != nil {
				return err
			}
			if receipt.AccountID != msg.AccountID || receipt.Digest != digest {
				return ErrChannelReplayConflict
			}
			return nil
		}
		if err := tx.Create(msg).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.ChannelMessageReceipt{}).Where("key = ?", key).Update("message_id", msg.ID).Error; err != nil {
			return err
		}
		change := domain.LocalChangeJournal{ChangeID: changeID, SourceModule: "MSG", AccountID: msg.AccountID, ObjectType: "Message", ObjectID: msg.ID, ObjectVersionAfter: 1, Action: "create", ChangedFields: "message", ActorType: msg.SenderType, ActorID: msg.SenderID, SourceType: "msg.message.create.v1", CommandID: changeID, CorrelationID: changeID, Status: "pending", OccurredAt: msg.CreatedAt}
		if err := NewJournalRepository(tx).RecordLocalChange(tx, &change); err != nil {
			return err
		}
		if err := r.captureAutomationMessage(tx, msg, changeID); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if created {
		// A failed CON activity update leaves the persisted MSG journal pending.
		// No token, message text, request body or raw provider error is logged here.
		if err := r.applyMessageActivity(msg, changeID); err != nil {
			if exists, checkErr := r.conversation.MessageTargetExists(msg.AccountID, msg.ConversationID); checkErr == nil && !exists {
				if cancelErr := r.cancelOrphanedCreate(msg, changeID); cancelErr != nil {
					return false, cancelErr
				}
				// Keep the durable receipt as a deletion tombstone; retries must not
				// resurrect intentionally deleted conversation content.
				return false, gorm.ErrRecordNotFound
			}
		}
	}
	return created, nil
}
