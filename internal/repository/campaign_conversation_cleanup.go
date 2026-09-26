package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type opsConversationCleanup struct{ db *gorm.DB }

func newOPSConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &opsConversationCleanup{db: db}
}
func (*opsConversationCleanup) Owner() string { return "OPS" }
func (c *opsConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.CampaignDelivery{}).Error
}
