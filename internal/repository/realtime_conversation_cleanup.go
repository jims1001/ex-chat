package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type rtmConversationCleanup struct{ db *gorm.DB }

func newRTMConversationCleanup(db *gorm.DB) conversationCleanupCommand {
	return &rtmConversationCleanup{db: db}
}
func (*rtmConversationCleanup) Owner() string { return "RTM" }
func (c *rtmConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	return c.db.Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Model(&domain.Call{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) != 0 {
			if err := tx.Where("call_id IN ?", ids).Delete(&domain.CallICECandidate{}).Error; err != nil {
				return err
			}
			if err := tx.Where("account_id = ? AND id IN ?", accountID, ids).Delete(&domain.Call{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.Conference{}).Error
	})
}
