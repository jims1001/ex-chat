package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type msgConversationCleanup struct{ db *gorm.DB }

func newMSGConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &msgConversationCleanup{db: db}
}
func (*msgConversationCleanup) Owner() string { return "MSG" }

func (c *msgConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	var paths []string
	err := c.db.Transaction(func(tx *gorm.DB) error {
		if err := LockOrdinaryEmailAccount(tx, accountID); err != nil {
			return errors.New("ordinary email cleanup checkpoint is unavailable")
		}
		if err := LockMessageConversation(tx, accountID, conversationID, true); err != nil {
			return err
		}
		if err := cleanupOrdinaryEmailConversation(tx, accountID, conversationID); err != nil {
			return err
		}
		var ids []uint
		if err := tx.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) != 0 {
			var attachments []domain.Attachment
			if err := tx.Where("account_id = ? AND message_id IN ?", accountID, ids).Find(&attachments).Error; err != nil {
				return err
			}
			for _, attachment := range attachments {
				paths = append(paths, attachment.DataURL)
			}
			if err := tx.Where("account_id = ? AND message_id IN ?", accountID, ids).Delete(&domain.Attachment{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.Message{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.DraftMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.EmailLog{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND object_type = ? AND action = ? AND status = ? AND object_id IN ?", "MSG", accountID, "Message", "create", "pending", ids).Update("status", "superseded").Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND object_type = ? AND action = ? AND status = ? AND object_id IN ?", "MSG", accountID, "Message", "automation_trigger", "pending", ids).Updates(map[string]any{"status": "superseded", "result": "rejected"}).Error; err != nil {
			return err
		}
		// Contract outbox aggregates use display IDs rather than ObjectID.
		displayIDs := make([]string, 0, len(ids))
		for _, id := range ids {
			displayIDs = append(displayIDs, strconv.FormatUint(uint64(id), 10))
		}
		return tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND object_type = ? AND action = ? AND status = ? AND object_display_id IN ?", "MSG", accountID, "Message", "event_outbox", "pending", displayIDs).Updates(map[string]any{"status": "superseded", "result": "rejected"}).Error
	})
	if err != nil {
		return err
	}
	for _, path := range paths {
		if strings.HasPrefix(path, "/uploads/") || strings.HasPrefix(path, "uploads/") {
			clean := filepath.Clean(strings.TrimPrefix(path, "/"))
			if clean != "uploads" && strings.HasPrefix(clean, "uploads"+string(filepath.Separator)) {
				_ = os.Remove(clean)
			}
		}
	}
	return os.RemoveAll(filepath.Join("uploads", fmt.Sprintf("account_%d", accountID), fmt.Sprintf("conv_%d", conversationID)))
}
