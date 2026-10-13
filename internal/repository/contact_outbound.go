package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// OutboundChannelRecipient requires the existing, unambiguous CUS channel
// identity. Generic Contact.PhoneNumber is not authorization to send.
func (r *ContactRepository) OutboundChannelRecipient(ctx context.Context, accountID, inboxID, contactID uint) (string, error) {
	var contact domain.Contact
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ? AND blocked = ?", accountID, contactID, false).First(&contact).Error; err != nil {
		return "", err
	}
	var mappings []domain.ContactInbox
	if err := r.db.WithContext(ctx).Where("inbox_id = ? AND contact_id = ?", inboxID, contactID).Limit(2).Find(&mappings).Error; err != nil {
		return "", err
	}
	if len(mappings) != 1 || mappings[0].SourceID == "" {
		return "", gorm.ErrRecordNotFound
	}
	var sameSource int64
	if err := r.db.WithContext(ctx).Model(&domain.ContactInbox{}).Where("inbox_id = ? AND source_id = ?", inboxID, mappings[0].SourceID).Count(&sameSource).Error; err != nil {
		return "", err
	}
	if sameSource != 1 {
		return "", gorm.ErrInvalidData
	}
	return mappings[0].SourceID, nil
}
