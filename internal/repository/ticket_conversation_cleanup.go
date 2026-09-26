package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type tktConversationCleanup struct{ db *gorm.DB }

func newTKTConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &tktConversationCleanup{db: db}
}
func (*tktConversationCleanup) Owner() string { return "TKT" }
func (c *tktConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Model(&domain.Ticket{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Update("conversation_id", nil).Error
}
