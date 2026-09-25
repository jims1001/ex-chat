package repository

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type MessageRepository struct {
	db           *gorm.DB
	conversation messageConversationPort
	users        messageUserPort
	contacts     messageContactPort
	reconcileMu  sync.Mutex
	reconcileID  uint
}

type messageUserPort interface {
	FindByID(id uint) (*domain.User, error)
}

type messageContactPort interface {
	FindByID(accountID, id uint) (*domain.Contact, error)
}

type messageConversationPort interface {
	MessageTargetExists(accountID, conversationID uint) (bool, error)
	ApplyMessageActivity(accountID, conversationID, actorID uint, incoming bool, occurredAt time.Time, messageChangeID string) error
}

func NewMessageRepository(db *gorm.DB) *MessageRepository {
	return NewMessageRepositoryWithConversationPort(db, NewConversationRepository(db))
}

// NewMessageRepositoryWithConversationPort allows CON and MSG to use separate
// stores while preserving the owner boundary at the repository call site.
func NewMessageRepositoryWithConversationPort(db *gorm.DB, conversation messageConversationPort) *MessageRepository {
	return NewMessageRepositoryWithPorts(db, conversation, NewUserRepository(db), NewContactRepository(db))
}

// NewMessageRepositoryWithPorts routes sender enrichment to the IAM/CUS owners.
func NewMessageRepositoryWithPorts(db *gorm.DB, conversation messageConversationPort, users messageUserPort, contacts messageContactPort) *MessageRepository {
	return &MessageRepository{db: db, conversation: conversation, users: users, contacts: contacts}
}

func (r *MessageRepository) Create(msg *domain.Message) error {
	if r.conversation == nil {
		return gorm.ErrInvalidData
	}
	exists, err := r.conversation.MessageTargetExists(msg.AccountID, msg.ConversationID)
	if err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	changeID := foundation.GenerateUUID()
	err = r.db.Transaction(func(tx *gorm.DB) error {
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
		change := domain.LocalChangeJournal{
			ChangeID: changeID, SourceModule: "MSG", AccountID: msg.AccountID,
			ObjectType: "Message", ObjectID: msg.ID, ObjectVersionBefore: 0, ObjectVersionAfter: 1,
			Action: "create", ChangedFields: "message", ActorType: msg.SenderType,
			ActorID: msg.SenderID, SourceType: "msg.message.create.v1",
			CommandID: changeID, CorrelationID: changeID, Status: "pending",
			OccurredAt: msg.CreatedAt,
		}
		return NewJournalRepository(tx).RecordLocalChange(tx, &change)
	})
	if err != nil {
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
	// The message and local journal are authoritative once committed. CON's
	// activity update can be replayed safely if the other store is unavailable.
	if err := r.applyMessageActivity(msg, changeID); err != nil {
		if removed, checkErr := r.conversation.MessageTargetExists(msg.AccountID, msg.ConversationID); checkErr == nil && !removed {
			if cancelErr := r.cancelOrphanedCreate(msg, changeID); cancelErr != nil {
				return cancelErr
			}
			return gorm.ErrRecordNotFound
		}
		logger.WithComponent("message").Warn("conversation activity deferred", "message_id", msg.ID, "account_id", msg.AccountID)
	}
	return nil
}

// A CON deletion can race with MSG's initial existence check. If the owner
// confirms the target is gone, cancel the locally committed create atomically.
func (r *MessageRepository) cancelOrphanedCreate(msg *domain.Message, changeID string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ? AND id = ?", msg.AccountID, msg.ID).Delete(&domain.Message{}).Error; err != nil {
			return err
		}
		return tx.Model(&domain.LocalChangeJournal{}).
			Where("account_id = ? AND change_id = ? AND status = ?", msg.AccountID, changeID, "pending").
			Updates(map[string]any{"status": "superseded", "result": "rejected"}).Error
	})
}

func (r *MessageRepository) applyMessageActivity(msg *domain.Message, changeID string) error {
	if err := r.conversation.ApplyMessageActivity(msg.AccountID, msg.ConversationID, msg.SenderID, msg.MessageType == domain.MessageTypeIncoming, msg.CreatedAt, changeID); err != nil {
		return err
	}
	return r.db.Model(&domain.LocalChangeJournal{}).Where("change_id = ? AND source_module = ?", changeID, "MSG").Update("status", "delivered").Error
}

// ReconcilePendingActivity resumes committed MSG changes after CON recovers.
// The CON command is idempotent on the MSG change ID.
func (r *MessageRepository) ReconcilePendingActivity(limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		return 0, gorm.ErrInvalidData
	}
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	var pending []domain.LocalChangeJournal
	query := func(after uint) *gorm.DB {
		q := r.db.Where("source_module = ? AND object_type = ? AND action = ? AND status = ?", "MSG", "Message", "create", "pending")
		if after != 0 {
			q = q.Where("id > ?", after)
		}
		return q.Order("id ASC").Limit(limit)
	}
	if err := query(r.reconcileID).Find(&pending).Error; err != nil {
		return 0, err
	}
	if len(pending) == 0 && r.reconcileID != 0 {
		r.reconcileID = 0
		if err := query(0).Find(&pending).Error; err != nil {
			return 0, err
		}
	}
	if len(pending) != 0 {
		r.reconcileID = pending[len(pending)-1].ID
	}
	processed := 0
	var firstError error
	for _, change := range pending {
		var msg domain.Message
		if err := r.db.Where("account_id = ? AND id = ?", change.AccountID, change.ObjectID).First(&msg).Error; err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		if err := r.applyMessageActivity(&msg, change.ChangeID); err != nil {
			if exists, checkErr := r.conversation.MessageTargetExists(msg.AccountID, msg.ConversationID); checkErr == nil && !exists {
				if cancelErr := r.cancelOrphanedCreate(&msg, change.ChangeID); cancelErr == nil {
					processed++
					continue
				} else {
					err = cancelErr
				}
			}
			if firstError == nil {
				firstError = err
			}
			continue
		}
		processed++
	}
	return processed, firstError
}

// CreateImported persists historical message state without applying live
// delivery side effects such as unread increments or activity timestamps.
func (r *MessageRepository) CreateImported(msg *domain.Message) error {
	exists, err := r.conversation.MessageTargetExists(msg.AccountID, msg.ConversationID)
	if err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(msg).Error; err != nil {
			return err
		}
		change := domain.LocalChangeJournal{
			ChangeID: foundation.GenerateUUID(), SourceModule: "MSG", AccountID: msg.AccountID,
			ObjectType: "Message", ObjectID: msg.ID, ObjectVersionAfter: 1,
			Action: "migrate", ChangedFields: "message", ActorType: "system",
			SourceType: "migration", OccurredAt: msg.CreatedAt,
		}
		return NewJournalRepository(tx).RecordLocalChange(tx, &change)
	})
}

func (r *MessageRepository) SaveDraft(accountID, conversationID, userID uint, message string) (*domain.DraftMessage, error) {
	var draft domain.DraftMessage
	err := r.db.Where("account_id = ? AND conversation_id = ? AND user_id = ?", accountID, conversationID, userID).First(&draft).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		draft = domain.DraftMessage{AccountID: accountID, ConversationID: conversationID, UserID: userID, Message: message}
		err = r.db.Create(&draft).Error
	} else if err == nil {
		draft.Message = message
		err = r.db.Save(&draft).Error
	}
	return &draft, err
}

func (r *MessageRepository) DeleteDraft(accountID, conversationID, userID uint) error {
	return r.db.Where("account_id = ? AND conversation_id = ? AND user_id = ?", accountID, conversationID, userID).Delete(&domain.DraftMessage{}).Error
}

// IncomingUnreadWindow is an MSG-owned query for CON's mark-unread command.
func (r *MessageRepository) IncomingUnreadWindow(accountID, conversationID uint) (time.Time, int64, bool, error) {
	var latest domain.Message
	err := r.db.Where("account_id = ? AND conversation_id = ? AND message_type = ?", accountID, conversationID, domain.MessageTypeIncoming).
		Order("created_at DESC, id DESC").First(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, 0, false, nil
	}
	if err != nil {
		return time.Time{}, 0, false, err
	}
	seenAt := latest.CreatedAt.Add(-1 * time.Second)
	var count int64
	err = r.db.Model(&domain.Message{}).
		Where("account_id = ? AND conversation_id = ? AND message_type = ? AND created_at > ?", accountID, conversationID, domain.MessageTypeIncoming, seenAt).
		Count(&count).Error
	return latest.CreatedAt, count, true, err
}

func (r *MessageRepository) ListConversationAttachments(accountID, conversationID uint) ([]domain.Attachment, error) {
	var attachments []domain.Attachment
	err := r.db.Model(&domain.Attachment{}).
		Joins("JOIN messages ON messages.id = attachments.message_id").
		Where("messages.account_id = ? AND messages.conversation_id = ? AND attachments.account_id = ?", accountID, conversationID, accountID).
		Order("attachments.id DESC").Find(&attachments).Error
	return attachments, err
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

	// Hydrate sender through the owning repositories.
	for i := range messages {
		if messages[i].SenderType == domain.SenderTypeUser {
			if r.users != nil {
				if user, err := r.users.FindByID(messages[i].SenderID); err == nil && user != nil {
					messages[i].Sender = *user
				}
			}
		} else if messages[i].SenderType == domain.SenderTypeContact {
			if r.contacts != nil {
				if contact, err := r.contacts.FindByID(accountID, messages[i].SenderID); err == nil && contact != nil {
					messages[i].Sender = *contact
				}
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

func (r *MessageRepository) FindCSATMessage(accountID, conversationID uint) (*domain.Message, error) {
	var message domain.Message
	if err := r.db.Where("account_id = ? AND conversation_id = ? AND content_type = ?", accountID, conversationID, "input_csat").First(&message).Error; err != nil {
		return nil, err
	}
	return &message, nil
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

func (r *MessageRepository) UpdateContentAttributes(accountID, conversationID, messageID uint, attributes string) error {
	result := r.db.Model(&domain.Message{}).
		Where("account_id = ? AND conversation_id = ? AND id = ?", accountID, conversationID, messageID).
		Update("content_attributes", attributes)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
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
	// Clean up attachments and physical files associated with deleted message (Chatwoot standard)
	var attachments []domain.Attachment
	if err := r.db.Where("message_id = ?", msg.ID).Find(&attachments).Error; err == nil {
		for _, att := range attachments {
			if strings.HasPrefix(att.DataURL, "/uploads/") || strings.HasPrefix(att.DataURL, "uploads/") {
				cleanPath := strings.TrimPrefix(att.DataURL, "/")
				_ = os.Remove(cleanPath)
			}
		}
		_ = r.db.Where("message_id = ?", msg.ID).Delete(&domain.Attachment{}).Error
	}

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

func (r *MessageRepository) UpdateTranslations(accountID, id uint, translations string) error {
	return r.db.Model(&domain.Message{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("translations", translations).Error
}
