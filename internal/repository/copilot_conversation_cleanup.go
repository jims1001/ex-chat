package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type aicConversationCleanup struct{ db *gorm.DB }

func newAICConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &aicConversationCleanup{db: db}
}
func (*aicConversationCleanup) Owner() string { return "AIC" }
func (c *aicConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.CopilotThread{}).Error
}
