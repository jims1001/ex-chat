package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// OutboundConnection is a CHN-owner-local read for the text owner adapter.
// Its encrypted connection must never cross into MSG, public payloads or logs.
func (r *InboxRepository) OutboundConnection(ctx context.Context, accountID, inboxID uint) (*domain.ChannelConnection, error) {
	var inbox domain.Inbox
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, err
	}
	provider := ""
	switch inbox.ChannelType {
	case "Channel::Whatsapp":
		provider = "whatsapp"
	case "Channel::TwilioSms":
		provider = "twilio"
	default:
		return nil, gorm.ErrInvalidData
	}
	var rows []domain.ChannelConnection
	if err := r.db.WithContext(ctx).Where("account_id = ? AND inbox_id = ? AND provider = ? AND revoked_at IS NULL", accountID, inboxID, provider).Limit(2).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 1 || rows[0].SecretCiphertext == "" {
		return nil, gorm.ErrRecordNotFound
	}
	return &rows[0], nil
}
