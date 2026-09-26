package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type qltConversationCleanup struct{ db *gorm.DB }

func newQLTConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &qltConversationCleanup{db: db}
}
func (*qltConversationCleanup) Owner() string { return "QLT" }
func (c *qltConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.CSATSurvey{}).Error
}
