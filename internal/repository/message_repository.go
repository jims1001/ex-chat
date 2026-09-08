package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type MessageRepository struct {
	db *gorm.DB
}

func NewMessageRepository(db *gorm.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

func (r *MessageRepository) Create(msg *domain.Message) error {
	return r.db.Create(msg).Error
}

func (r *MessageRepository) ListByConversation(accountID, conversationID uint, includePrivate bool, page, pageSize int) ([]domain.Message, int64, error) {
	var messages []domain.Message
	var total int64

	query := r.db.Model(&domain.Message{}).
		Where("account_id = ? AND conversation_id = ?", accountID, conversationID)

	if !includePrivate {
		query = query.Where("private = ?", false)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("id ASC").Find(&messages).Error

	// Hydrate sender
	for i := range messages {
		if messages[i].SenderType == domain.SenderTypeUser {
			var user domain.User
			if r.db.First(&user, messages[i].SenderID).Error == nil {
				messages[i].Sender = user
			}
		} else if messages[i].SenderType == domain.SenderTypeContact {
			var contact domain.Contact
			if r.db.First(&contact, messages[i].SenderID).Error == nil {
				messages[i].Sender = contact
			}
		}
	}

	return messages, total, err
}

func (r *MessageRepository) FindByID(accountID, id uint) (*domain.Message, error) {
	var msg domain.Message
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&msg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &msg, nil
}

func (r *MessageRepository) UpdateStatus(id uint, status string) error {
	return r.db.Model(&domain.Message{}).Where("id = ?", id).Update("status", status).Error
}
