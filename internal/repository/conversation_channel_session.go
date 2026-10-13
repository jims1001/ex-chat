package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureChannelConversation serializes session selection inside CON. Existing
// open/pending/snoozed sessions are reused; a resolved session starts a new one.
func (r *ConversationRepository) EnsureChannelConversation(ctx context.Context, key string, accountID, inboxID, contactID uint) (*domain.Conversation, error) {
	if key == "" || accountID == 0 || inboxID == 0 || contactID == 0 || r.inboxes == nil || r.contacts == nil {
		return nil, gorm.ErrInvalidData
	}
	inboxOK, err := r.inboxes.Exists(accountID, inboxID)
	if err != nil {
		return nil, err
	}
	contactOK, err := r.contacts.Exists(accountID, contactID)
	if err != nil {
		return nil, err
	}
	if !inboxOK || !contactOK {
		return nil, gorm.ErrRecordNotFound
	}
	var conversation domain.Conversation
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {

		// A dedicated account coordination row serializes display-ID allocation
		// between different inbound senders. Session rows alone are too narrow.
		allocator := domain.ChannelConversationSession{Key: fmt.Sprintf("account-display-%d", accountID), AccountID: accountID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&allocator).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&allocator, "key = ?", allocator.Key).Error; err != nil {
			return err
		}
		session := domain.ChannelConversationSession{Key: key, AccountID: accountID, InboxID: inboxID, ContactID: contactID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&session).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "key = ?", key).Error; err != nil {
			return err
		}
		if session.AccountID != accountID || session.InboxID != inboxID || session.ContactID != contactID {
			return gorm.ErrInvalidData
		}
		var existing []domain.Conversation
		if err := tx.Where("account_id = ? AND inbox_id = ? AND contact_id = ? AND status IN ?", accountID, inboxID, contactID, []string{"open", "pending", "snoozed"}).Order("id DESC").Limit(1).Find(&existing).Error; err != nil {
			return err
		}
		if len(existing) == 1 {
			conversation = existing[0]
		} else {
			var maxID uint
			if err := tx.Model(&domain.Conversation{}).Where("account_id = ?", accountID).Select("COALESCE(MAX(display_id), 0)").Scan(&maxID).Error; err != nil {
				return err
			}
			conversation = domain.Conversation{AccountID: accountID, InboxID: inboxID, ContactID: contactID, DisplayID: maxID + 1, Status: domain.ConversationStatusOpen, Priority: "medium", LastActivityAt: time.Now().UTC()}
			if err := tx.Omit(clause.Associations).Create(&conversation).Error; err != nil {
				return err
			}
		}
		return tx.Model(&domain.ChannelConversationSession{}).Where("key = ?", key).Update("conversation_id", conversation.ID).Error
	})
	return &conversation, err
}
