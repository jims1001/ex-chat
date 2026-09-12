package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	err := r.db.Preload("Contact").Preload("Inbox").Preload("Assignee").Preload("Labels").Preload("Team").
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
	err := query.Preload("Contact").Preload("Inbox").Preload("Assignee").Preload("Labels").Preload("Team").
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

func (r *ConversationRepository) AssignTeam(accountID, id uint, teamID *uint) error {
	updates := map[string]any{
		"team_id":          teamID,
		"last_activity_at": time.Now().UTC(),
	}
	return r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(updates).Error
}

func (r *ConversationRepository) AssignWithTeam(accountID, id uint, hasAssignee bool, assigneeID *uint, hasTeam bool, teamID *uint) error {
	updates := map[string]any{
		"last_activity_at": time.Now().UTC(),
	}
	if hasAssignee {
		if assigneeID != nil && *assigneeID > 0 {
			updates["assignee_id"] = assigneeID
		} else {
			updates["assignee_id"] = nil
		}
	}
	if hasTeam {
		if teamID != nil && *teamID > 0 {
			updates["team_id"] = teamID
		} else {
			updates["team_id"] = nil
		}
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

// Delete cascades deletion of conversation and associated records
func (r *ConversationRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Find message IDs to delete attachments
		var msgIDs []uint
		tx.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", accountID, id).Pluck("id", &msgIDs)
		if len(msgIDs) > 0 {
			if err := tx.Where("account_id = ? AND message_id IN (?)", accountID, msgIDs).Delete(&domain.Attachment{}).Error; err != nil {
				return err
			}
		}

		// Delete messages
		if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, id).Delete(&domain.Message{}).Error; err != nil {
			return err
		}

		// Delete conversation labels
		if err := tx.Where("conversation_id = ?", id).Delete(&domain.ConversationLabel{}).Error; err != nil {
			return err
		}

		// Delete conversation participants
		if err := tx.Where("conversation_id = ?", id).Delete(&domain.ConversationParticipant{}).Error; err != nil {
			return err
		}

		// Delete draft messages
		if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, id).Delete(&domain.DraftMessage{}).Error; err != nil {
			return err
		}

		// Delete applied SLAs & events
		if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, id).Delete(&domain.AppliedSLA{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, id).Delete(&domain.SLAEvent{}).Error; err != nil {
			return err
		}

		// Delete conversation
		res := tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Conversation{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// GetMeta calculates conversation count summaries across dimensions
func (r *ConversationRepository) GetMeta(accountID uint, status string, inboxID, teamID *uint, currentUserID uint) (map[string]int64, error) {
	if status == "" {
		status = domain.ConversationStatusOpen
	}

	baseQuery := func() *gorm.DB {
		q := r.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, status)
		if inboxID != nil && *inboxID > 0 {
			q = q.Where("inbox_id = ?", *inboxID)
		}
		if teamID != nil && *teamID > 0 {
			q = q.Where("team_id = ?", *teamID)
		}
		return q
	}

	var mineCount, unassignedCount, allCount, assignedCount int64

	if err := baseQuery().Where("assignee_id = ?", currentUserID).Count(&mineCount).Error; err != nil {
		return nil, err
	}
	if err := baseQuery().Where("assignee_id IS NULL").Count(&unassignedCount).Error; err != nil {
		return nil, err
	}
	if err := baseQuery().Count(&allCount).Error; err != nil {
		return nil, err
	}
	if err := baseQuery().Where("assignee_id IS NOT NULL").Count(&assignedCount).Error; err != nil {
		return nil, err
	}

	return map[string]int64{
		"mine_count":       mineCount,
		"unassigned_count": unassignedCount,
		"all_count":        allCount,
		"assigned_count":   assignedCount,
	}, nil
}

// GetUnreadCounts returns aggregated unread conversation counts
func (r *ConversationRepository) GetUnreadCounts(accountID, currentUserID uint) (map[string]int64, error) {
	var totalUnread, mineUnread, unassignedUnread int64

	row := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusOpen).
		Select("COALESCE(SUM(unread_count), 0)").
		Row()
	_ = row.Scan(&totalUnread)

	rowMine := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND status = ? AND assignee_id = ?", accountID, domain.ConversationStatusOpen, currentUserID).
		Select("COALESCE(SUM(unread_count), 0)").
		Row()
	_ = rowMine.Scan(&mineUnread)

	rowUnassigned := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND status = ? AND assignee_id IS NULL", accountID, domain.ConversationStatusOpen).
		Select("COALESCE(SUM(unread_count), 0)").
		Row()
	_ = rowUnassigned.Scan(&unassignedUnread)

	return map[string]int64{
		"total":                    totalUnread,
		"mine":                     mineUnread,
		"unassigned":               unassignedUnread,
		"total_unread_count":       totalUnread,
		"mine_unread_count":        mineUnread,
		"unassigned_unread_count":  unassignedUnread,
	}, nil
}

// Filter searches conversations matching flexible structured rules
func (r *ConversationRepository) Filter(accountID uint, filters []domain.FilterRule, page, pageSize int) ([]domain.Conversation, int64, error) {
	var conversations []domain.Conversation
	var total int64

	query := r.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)

	for _, rule := range filters {
		op := strings.ToLower(rule.FilterOperator)
		key := strings.ToLower(rule.AttributeKey)
		switch key {
		case "status":
			if op == "equal_to" {
				query = query.Where("status IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("status NOT IN (?)", rule.Values)
			}
		case "priority":
			if op == "equal_to" {
				query = query.Where("priority IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("priority NOT IN (?)", rule.Values)
			}
		case "assignee_id":
			switch op {
			case "is_present":
				query = query.Where("assignee_id IS NOT NULL")
			case "is_not_present":
				query = query.Where("assignee_id IS NULL")
			case "equal_to":
				query = query.Where("assignee_id IN (?)", rule.Values)
			case "not_equal_to":
				query = query.Where("assignee_id NOT IN (?)", rule.Values)
			}
		case "inbox_id":
			if op == "equal_to" {
				query = query.Where("inbox_id IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("inbox_id NOT IN (?)", rule.Values)
			}
		case "team_id":
			switch op {
			case "is_present":
				query = query.Where("team_id IS NOT NULL")
			case "is_not_present":
				query = query.Where("team_id IS NULL")
			case "equal_to":
				query = query.Where("team_id IN (?)", rule.Values)
			case "not_equal_to":
				query = query.Where("team_id NOT IN (?)", rule.Values)
			}
		case "contact_id":
			if op == "equal_to" {
				query = query.Where("contact_id IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("contact_id NOT IN (?)", rule.Values)
			}
		case "labels":
			if op == "equal_to" || op == "contains" {
				query = query.Where("id IN (SELECT conversation_id FROM conversation_labels JOIN labels ON conversation_labels.label_id = labels.id WHERE labels.title IN (?))", rule.Values)
			}
		case "custom_attributes":
			for _, v := range rule.Values {
				query = query.Where("custom_attributes LIKE ?", fmt.Sprintf("%%%v%%", v))
			}
		}
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Contact").Preload("Inbox").Preload("Assignee").Preload("Labels").Preload("Team").
		Offset(offset).Limit(pageSize).
		Order("last_activity_at DESC, id DESC").
		Find(&conversations).Error

	return conversations, total, err
}

// UpdateCustomAttributes updates or merges key-values into conversation custom attributes
func (r *ConversationRepository) UpdateCustomAttributes(accountID, id uint, attrs map[string]any) (map[string]any, error) {
	var conv domain.Conversation
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&conv).Error; err != nil {
		return nil, err
	}

	merged := make(map[string]any)
	if conv.CustomAttributes != "" {
		_ = json.Unmarshal([]byte(conv.CustomAttributes), &merged)
	}
	for k, v := range attrs {
		merged[k] = v
	}

	bytes, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}

	conv.CustomAttributes = string(bytes)
	conv.LastActivityAt = time.Now().UTC()
	if err := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Updates(map[string]any{
			"custom_attributes": conv.CustomAttributes,
			"last_activity_at":  conv.LastActivityAt,
		}).Error; err != nil {
		return nil, err
	}

	return merged, nil
}

// ListAttachments returns all attachments belonging to messages in the conversation
func (r *ConversationRepository) ListAttachments(accountID, conversationID uint) ([]domain.Attachment, error) {
	var attachments []domain.Attachment
	err := r.db.Table("attachments").
		Joins("INNER JOIN messages ON attachments.message_id = messages.id").
		Where("messages.account_id = ? AND messages.conversation_id = ?", accountID, conversationID).
		Order("attachments.id DESC").
		Find(&attachments).Error
	return attachments, err
}

