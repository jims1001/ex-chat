package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrChannelContactMergeUnsupported = errors.New("merging verified channel contacts is not supported")

// EnsureChannelContact serializes by a durable CUS row, then checks the full
// source mapping for ambiguity. Storage errors are never interpreted as absence.
func (r *ContactRepository) EnsureChannelContact(ctx context.Context, key string, accountID, inboxID uint, source, name string) (*domain.Contact, error) {
	if key == "" || accountID == 0 || inboxID == 0 || source == "" {
		return nil, gorm.ErrInvalidData
	}
	var contact domain.Contact
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		identity := domain.ChannelContactIdentity{Key: key, AccountID: accountID, InboxID: inboxID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&identity).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&identity, "key = ?", key).Error; err != nil {
			return err
		}
		if identity.AccountID != accountID || identity.InboxID != inboxID {
			return gorm.ErrInvalidData
		}
		var mappings []domain.ContactInbox
		if err := tx.Where("inbox_id = ? AND source_id = ?", inboxID, source).Limit(2).Find(&mappings).Error; err != nil {
			return err
		}
		if len(mappings) > 1 {
			return gorm.ErrInvalidData
		}
		if len(mappings) == 1 {
			if identity.ContactID != 0 && identity.ContactID != mappings[0].ContactID {
				return gorm.ErrInvalidData
			}
			if err := tx.Where("account_id = ? AND id = ?", accountID, mappings[0].ContactID).First(&contact).Error; err != nil {
				return err
			}
		} else {
			// A missing previously established relation means revocation/deletion, not
			// permission to recreate an identity from a retried provider event.
			if identity.ContactID != 0 {
				return gorm.ErrRecordNotFound
			}
			// A generic Identifier can be visitor-supplied in another inbox. Never
			// use it as proof that the sender owns an existing contact.
			contact = domain.Contact{AccountID: accountID, Identifier: fmt.Sprintf("channel:%d:%s", inboxID, source), Name: name, PhoneNumber: source}
			if err := tx.Omit("Company", "Labels").Create(&contact).Error; err != nil {
				return err
			}

			mapping := domain.ContactInbox{ContactID: contact.ID, InboxID: inboxID, SourceID: source}
			if err := tx.Omit("Contact", "Inbox").Create(&mapping).Error; err != nil {
				return err
			}
		}
		if contact.Blocked {
			return errors.New("channel contact unavailable")
		}
		return tx.Model(&domain.ChannelContactIdentity{}).Where("key = ?", key).Update("contact_id", contact.ID).Error
	})
	return &contact, err
}
