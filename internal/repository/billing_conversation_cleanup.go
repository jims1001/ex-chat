package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type bilConversationCleanup struct{ db *gorm.DB }

func newBILConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &bilConversationCleanup{db: db}
}
func (*bilConversationCleanup) Owner() string { return "BIL" }
func (c *bilConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Model(&domain.Order{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Update("conversation_id", nil).Error
}
