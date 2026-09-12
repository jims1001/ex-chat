package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type MessageRepository struct {
	db *gorm.DB
}

func NewMessageRepository(db *gorm.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

func (r *MessageRepository) Create(msg *domain.Message) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(msg).Error; err != nil {
			logger.WithComponent("message").Error("failed to persist message",
				"account_id", msg.AccountID,
				"conversation_id", msg.ConversationID,
				"sender_type", msg.SenderType,
				"sender_id", msg.SenderID,
				"message_type", msg.MessageType,
				"error", err.Error(),
			)
			return err
		}

		updates := map[string]any{
			"last_activity_at": time.Now().UTC(),
		}
		if msg.MessageType == domain.MessageTypeIncoming {
			updates["unread_count"] = gorm.Expr("unread_count + 1")
		}

		if err := tx.Model(&domain.Conversation{}).
			Where("account_id = ? AND id = ?", msg.AccountID, msg.ConversationID).
			Updates(updates).Error; err != nil {
			return err
		}

		logger.WithComponent("message").Info("message persisted",
			"message_id", msg.ID,
			"account_id", msg.AccountID,
			"conversation_id", msg.ConversationID,
			"sender_type", msg.SenderType,
			"sender_id", msg.SenderID,
			"message_type", msg.MessageType,
			"content_type", msg.ContentType,
			"private", msg.Private,
		)

		return nil
	})
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

func (r *MessageRepository) FindByIDAndConversation(accountID, conversationID, messageID uint) (*domain.Message, error) {
	var msg domain.Message
	err := r.db.Where("account_id = ? AND conversation_id = ? AND id = ?", accountID, conversationID, messageID).First(&msg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &msg, nil
}

func (r *MessageRepository) UpdateContent(accountID, conversationID, messageID uint, content string) (*domain.Message, error) {
	msg, err := r.FindByIDAndConversation(accountID, conversationID, messageID)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, gorm.ErrRecordNotFound
	}
	if msg.Deleted {
		return nil, errors.New("cannot edit a deleted message")
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"content":   content,
		"edited_at": now,
	}
	if err := r.db.Model(&domain.Message{}).Where("id = ?", msg.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	msg.Content = content
	msg.EditedAt = &now
	msg.UpdatedAt = now
	return msg, nil
}

func (r *MessageRepository) DeleteMessage(accountID, conversationID, messageID uint) (*domain.Message, error) {
	msg, err := r.FindByIDAndConversation(accountID, conversationID, messageID)
	if err != nil {
		return nil, err
	}
	if msg == nil || msg.Deleted {
		return nil, gorm.ErrRecordNotFound
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"deleted":    true,
		"deleted_at": now,
		"content":    "[此消息已被撤回/删除]",
		"updated_at": now,
	}
	if err := r.db.Model(&domain.Message{}).Where("id = ?", msg.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	// Clean up attachments associated with deleted message (Chatwoot standard)
	_ = r.db.Where("message_id = ?", msg.ID).Delete(&domain.Attachment{}).Error

	msg.Deleted = true
	msg.DeletedAt = &now
	msg.Content = "[此消息已被撤回/删除]"
	msg.UpdatedAt = now
	return msg, nil
}

func (r *MessageRepository) RetryMessage(accountID, conversationID, messageID uint) (*domain.Message, error) {
	msg, err := r.FindByIDAndConversation(accountID, conversationID, messageID)
	if err != nil {
		return nil, err
	}
	if msg == nil {
		return nil, gorm.ErrRecordNotFound
	}
	if msg.Deleted {
		return nil, errors.New("cannot retry a deleted message")
	}
	if msg.Status != domain.MessageStatusFailed {
		return nil, errors.New("only failed messages can be retried")
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"status":     domain.MessageStatusSent,
		"updated_at": now,
	}
	if err := r.db.Model(&domain.Message{}).Where("id = ?", msg.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	msg.Status = domain.MessageStatusSent
	msg.UpdatedAt = now
	return msg, nil
}

func (r *MessageRepository) GetDB() *gorm.DB {
	return r.db
}

func (r *MessageRepository) UpdateTranslations(accountID, id uint, translations string) error {
	return r.db.Model(&domain.Message{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("translations", translations).Error
}

