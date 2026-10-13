package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type OutboundMessageTarget struct {
	ID, AccountID, InboxID, ContactID uint
	RecipientID                       string
}

type outboundRecipientPort interface {
	OutboundChannelRecipient(context.Context, uint, uint, uint) (string, error)
}

// OutboundMessageTarget composes CON routing with the CUS identity owner port.
// It never reads CUS storage or substitutes a generic current phone number.
func (r *ConversationRepository) OutboundMessageTarget(ctx context.Context, accountID, conversationID uint) (*OutboundMessageTarget, error) {
	var target domain.Conversation
	err := r.db.WithContext(ctx).Select("id", "account_id", "inbox_id", "contact_id").Where("account_id = ? AND id = ? AND status <> ?", accountID, conversationID, domain.ConversationStatusDeleting).First(&target).Error
	if err != nil {
		return nil, err
	}
	owner, ok := r.contacts.(outboundRecipientPort)
	if !ok {
		return nil, gorm.ErrInvalidData
	}
	recipient, err := owner.OutboundChannelRecipient(ctx, accountID, target.InboxID, target.ContactID)
	if err != nil {
		return nil, err
	}
	return &OutboundMessageTarget{ID: target.ID, AccountID: target.AccountID, InboxID: target.InboxID, ContactID: target.ContactID, RecipientID: recipient}, nil
}
