package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// AttachmentRepository manages message attachments
type AttachmentRepository struct {
	db            *gorm.DB
	messages      attachmentMessageOwner
	conversations attachmentConversationOwner
	contacts      attachmentContactOwner
	inboxes       attachmentInboxOwner
}

type attachmentMessageOwner interface {
	FindByID(uint, uint) (*domain.Message, error)
}
type attachmentConversationOwner interface {
	FindSummaryByID(uint, uint) (*domain.Conversation, error)
}
type attachmentContactOwner interface {
	FindSummaryByID(uint, uint) (*domain.Contact, error)
	FindUploadIdentity(context.Context, uint, uint, string) (*domain.ContactInbox, error)
}
type attachmentInboxOwner interface {
	FindByGlobalID(uint) (*domain.Inbox, error)
	FindSummaryByID(uint, uint) (*domain.Inbox, error)
}

func NewAttachmentRepository(db *gorm.DB) *AttachmentRepository {
	return NewAttachmentRepositoryWithOwnerPorts(db, &MessageRepository{db: db}, &ConversationRepository{db: db}, &ContactRepository{db: db}, &InboxRepository{db: db})
}

func NewAttachmentRepositoryWithOwnerPorts(db *gorm.DB, messages attachmentMessageOwner, conversations attachmentConversationOwner, contacts attachmentContactOwner, inboxes attachmentInboxOwner) *AttachmentRepository {
	return &AttachmentRepository{db: db, messages: messages, conversations: conversations, contacts: contacts, inboxes: inboxes}
}

func (r *AttachmentRepository) Create(ctx context.Context, att *domain.Attachment) error {
	var messageCount int64
	if err := r.db.WithContext(ctx).Model(&domain.Message{}).
		Where("account_id = ? AND id = ?", att.AccountID, att.MessageID).
		Count(&messageCount).Error; err != nil {
		return err
	}
	if messageCount != 1 {
		return gorm.ErrRecordNotFound
	}
	if err := r.validateLocalAttachment(ctx, att); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Create(att).Error
}

func (r *AttachmentRepository) ListByMessage(ctx context.Context, accountID, messageID uint) ([]domain.Attachment, error) {
	var list []domain.Attachment
	err := r.db.WithContext(ctx).Where("account_id = ? AND message_id = ?", accountID, messageID).Find(&list).Error
	return list, err
}
