package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// CreateChannelConnection atomically creates the inbox and its encrypted,
// globally unambiguous provider recipient mapping in CHN's store.
func (r *InboxRepository) CreateChannelConnection(ctx context.Context, inbox *domain.Inbox, connection *domain.ChannelConnection) error {
	if inbox == nil || connection == nil || inbox.AccountID == 0 || inbox.AccountID != connection.AccountID || connection.SecretCiphertext == "" || connection.RecipientKey == "" {
		return gorm.ErrInvalidData
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Members", "AssignmentPolicy", "AgentBotInbox").Create(inbox).Error; err != nil {
			return err
		}
		connection.InboxID = inbox.ID
		return tx.Create(connection).Error
	})
}

func (r *InboxRepository) ResolveChannelConnection(ctx context.Context, accountID uint, provider, providerAccountID, recipientID string) (*domain.ChannelConnection, error) {
	var matches []domain.ChannelConnection
	err := r.db.WithContext(ctx).Where("provider = ? AND recipient_id = ?", provider, recipientID).Limit(2).Find(&matches).Error
	if err != nil {
		return nil, err
	}
	if len(matches) != 1 || matches[0].RevokedAt != nil || matches[0].SecretCiphertext == "" || matches[0].AccountID != accountID || matches[0].ProviderAccountID != providerAccountID {
		return nil, gorm.ErrRecordNotFound
	}
	connection := matches[0]
	var inbox domain.Inbox
	if err := r.db.WithContext(ctx).Where("id = ? AND account_id = ?", connection.InboxID, accountID).First(&inbox).Error; err != nil {
		return nil, err
	}
	expected := "Channel::Whatsapp"
	if provider == "twilio" {
		expected = "Channel::TwilioSms"
	}
	if inbox.ChannelType != expected {
		return nil, gorm.ErrRecordNotFound
	}
	return &connection, nil
}

func (r *InboxRepository) ListWhatsAppConnections(ctx context.Context, accountID uint) ([]domain.ChannelConnection, error) {
	var connections []domain.ChannelConnection
	if err := r.db.WithContext(ctx).Where("account_id = ? AND provider = ? AND revoked_at IS NULL AND secret_ciphertext <> ?", accountID, "whatsapp", "").Find(&connections).Error; err != nil {
		return nil, err
	}
	return connections, nil
}
