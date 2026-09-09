package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ConversationRepository struct {
	db *gorm.DB
}

func NewConversationRepository(db *gorm.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

func (r *ConversationRepository) GetDB() *gorm.DB {
	return r.db
}

func (r *ConversationRepository) Create(c *domain.Conversation) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var maxDisplayID uint
		row := tx.Model(&domain.Conversation{}).
			Where("account_id = ?", c.AccountID).
			Select("COALESCE(MAX(display_id), 0)").
			Row()
		_ = row.Scan(&maxDisplayID)

		c.DisplayID = maxDisplayID + 1
		c.LastActivityAt = time.Now().UTC()

		return tx.Create(c).Error
	})
}

func (r *ConversationRepository) FindByID(accountID, id uint) (*domain.Conversation, error) {
	var conv domain.Conversation
	err := r.db.Preload("Contact").Preload("Inbox").Preload("Assignee").Preload("Labels").
		Where("account_id = ? AND id = ?", accountID, id).
		First(&conv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &conv, nil
}

func (r *ConversationRepository) FindOpenByContactAndInbox(accountID, contactID, inboxID uint) (*domain.Conversation, error) {
	var conv domain.Conversation
	err := r.db.Preload("Contact").Preload("Inbox").Preload("Assignee").
		Where("account_id = ? AND contact_id = ? AND inbox_id = ? AND status = ?", accountID, contactID, inboxID, domain.ConversationStatusOpen).
		Order("id DESC").
		First(&conv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &conv, nil
}

func (r *ConversationRepository) List(accountID uint, status, priority string, assigneeID *uint, inboxID *uint, page, pageSize int) ([]domain.Conversation, int64, error) {
	var conversations []domain.Conversation
	var total int64

	query := r.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)

	if status != "" {
		query = query.Where("status = ?", status)
	}
	if priority != "" {
		query = query.Where("priority = ?", priority)
	}
	if assigneeID != nil {
		query = query.Where("assignee_id = ?", *assigneeID)
	}
	if inboxID != nil {
		query = query.Where("inbox_id = ?", *inboxID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Contact").Preload("Inbox").Preload("Assignee").Preload("Labels").
		Offset(offset).Limit(pageSize).
		Order("last_activity_at DESC").
		Find(&conversations).Error

	return conversations, total, err
}

func (r *ConversationRepository) Update(c *domain.Conversation) error {
	return r.db.Save(c).Error
}

func (r *ConversationRepository) UpdateStatus(accountID, id uint, status string, snoozedUntil *time.Time) error {
	updates := map[string]any{
		"status":           status,
		"snoozed_until":    snoozedUntil,
		"last_activity_at": time.Now().UTC(),
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

func (r *ConversationRepository) Assign(accountID, id uint, assigneeID *uint) error {
	updates := map[string]any{
		"assignee_id":      assigneeID,
		"last_activity_at": time.Now().UTC(),
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

func (r *ConversationRepository) AssignWithTeam(accountID, id uint, assigneeID, teamID *uint) error {
	updates := map[string]any{
		"last_activity_at": time.Now().UTC(),
	}
	if assigneeID != nil {
		updates["assignee_id"] = assigneeID
	}
	if teamID != nil {
		updates["team_id"] = teamID
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

func (r *ConversationRepository) TouchActivity(accountID, id uint) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("last_activity_at", time.Now().UTC()).Error
}

func (r *ConversationRepository) UpdatePriority(accountID, id uint, priority string) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(map[string]any{
			"priority":         priority,
			"last_activity_at": time.Now().UTC(),
		}).Error
}

// UpdateLastSeen updates agent read position and clears unread count
func (r *ConversationRepository) UpdateLastSeen(accountID, id, userID uint, seenAt time.Time) error {
	var conv domain.Conversation
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&conv).Error; err != nil {
		return err
	}

	updates := map[string]any{
		"agent_last_seen_at": seenAt,
		"unread_count":       0,
	}
	if conv.AssigneeID != nil && *conv.AssigneeID == userID {
		updates["assignee_last_seen_at"] = seenAt
	}

	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

// MarkUnread rewinds the agent's read position before the latest incoming message
func (r *ConversationRepository) MarkUnread(accountID, id uint) error {
	var latestMsg domain.Message
	err := r.db.Where("account_id = ? AND conversation_id = ? AND message_type = ?", accountID, id, domain.MessageTypeIncoming).
		Order("created_at DESC, id DESC").
		First(&latestMsg).Error

	updates := map[string]any{}
	if err == nil {
		seenAt := latestMsg.CreatedAt.Add(-1 * time.Second)
		var unreadCount int64
		_ = r.db.Model(&domain.Message{}).
			Where("account_id = ? AND conversation_id = ? AND message_type = ? AND created_at > ?", accountID, id, domain.MessageTypeIncoming, seenAt).
			Count(&unreadCount)
		if unreadCount < 1 {
			unreadCount = 1
		}
		updates["agent_last_seen_at"] = seenAt
		updates["unread_count"] = int(unreadCount)
	} else {
		updates["agent_last_seen_at"] = nil
		updates["unread_count"] = 1
	}

	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

// UpdateContactLastSeen updates the customer's read position
func (r *ConversationRepository) UpdateContactLastSeen(accountID, id uint, seenAt time.Time) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("contact_last_seen_at", seenAt).Error
}

// ToggleMute updates conversation mute state
func (r *ConversationRepository) ToggleMute(accountID, id uint, muted bool) error {
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("muted", muted).Error
}

