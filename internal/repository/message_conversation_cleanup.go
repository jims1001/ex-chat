package repository

import (
	"fmt"
	"os"
	"path/filepath"
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
		return tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND object_type = ? AND action = ? AND status = ? AND object_id IN ?", "MSG", accountID, "Message", "create", "pending", ids).Update("status", "superseded").Error
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
