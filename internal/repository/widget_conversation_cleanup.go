package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type apiConversationCleanup struct{ db *gorm.DB }

func newAPIConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &apiConversationCleanup{db: db}
}
func (*apiConversationCleanup) Owner() string { return "API" }
func (c *apiConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.WidgetEvent{}).Error
}
