package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type rptConversationCleanup struct{ db *gorm.DB }

func newRPTConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &rptConversationCleanup{db: db}
}
func (*rptConversationCleanup) Owner() string { return "RPT" }
func (c *rptConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{&domain.AppliedSLA{}, &domain.SLAEvent{}, &domain.SLABreachLog{}, &domain.ReportingEvent{}} {
			if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(model).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
