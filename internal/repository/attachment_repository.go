package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// AttachmentRepository manages message attachments
type AttachmentRepository struct {
	db *gorm.DB
}

func NewAttachmentRepository(db *gorm.DB) *AttachmentRepository {
	return &AttachmentRepository{db: db}
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
	return r.db.WithContext(ctx).Create(att).Error
}

func (r *AttachmentRepository) ListByMessage(ctx context.Context, accountID, messageID uint) ([]domain.Attachment, error) {
	var list []domain.Attachment
	err := r.db.WithContext(ctx).Where("account_id = ? AND message_id = ?", accountID, messageID).Find(&list).Error
	return list, err
}
