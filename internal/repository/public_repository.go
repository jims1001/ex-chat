package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// PublicRepository encapsulates database operations for customer-facing Public API
type PublicRepository struct {
	db *gorm.DB
}

// NewPublicRepository creates a new PublicRepository instance
func NewPublicRepository(db *gorm.DB) *PublicRepository {
	return &PublicRepository{db: db}
}

// FindInboxByIdentifier retrieves an inbox by website_token or primary numeric ID
func (r *PublicRepository) FindInboxByIdentifier(ctx context.Context, identifier string) (*domain.Inbox, error) {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return nil, errors.New("inbox identifier is required")
	}

	var inbox domain.Inbox
	err := r.db.WithContext(ctx).
		Where("website_token = ? OR id = ?", trimmed, trimmed).
		First(&inbox).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inbox, nil
}

// ResolveContact locates a contact by numeric ID, contact_inbox source_id, or contact identifier
func (r *PublicRepository) ResolveContact(ctx context.Context, inboxID, accountID uint, idOrSourceID string) (*domain.Contact, *domain.ContactInbox, error) {
	trimmed := strings.TrimSpace(idOrSourceID)
	if trimmed == "" {
		return nil, nil, nil
	}

	// 1. Try parsing as numeric contact ID
	if numID, err := strconv.ParseUint(trimmed, 10, 64); err == nil && numID > 0 {
		var contact domain.Contact
		if err := r.db.WithContext(ctx).Where("id = ? AND account_id = ?", uint(numID), accountID).First(&contact).Error; err == nil {
			var ci domain.ContactInbox
			_ = r.db.WithContext(ctx).Where("contact_id = ? AND inbox_id = ?", contact.ID, inboxID).First(&ci).Error
			return &contact, &ci, nil
		}
	}

	// 2. Try looking up by contact_inbox source_id within this inbox
	var ci domain.ContactInbox
	err := r.db.WithContext(ctx).Preload("Contact").
		Where("inbox_id = ? AND source_id = ?", inboxID, trimmed).
		First(&ci).Error
	if err == nil && ci.Contact != nil && ci.Contact.AccountID == accountID {
		return ci.Contact, &ci, nil
	}

	// 3. Fallback: lookup by contact's custom identifier
	var contact domain.Contact
	err = r.db.WithContext(ctx).
		Where("account_id = ? AND identifier = ?", accountID, trimmed).
		First(&contact).Error
	if err == nil {
		var existingCI domain.ContactInbox
		_ = r.db.WithContext(ctx).Where("contact_id = ? AND inbox_id = ?", contact.ID, inboxID).First(&existingCI).Error
		return &contact, &existingCI, nil
	}

	return nil, nil, nil
}

// UpdateContact partially updates contact fields and shallow-merges custom attributes
func (r *PublicRepository) UpdateContact(ctx context.Context, accountID, contactID uint, updates map[string]any, customAttrs map[string]any) (*domain.Contact, error) {
	var contact domain.Contact
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		return nil, err
	}

	if updates == nil {
		updates = make(map[string]any)
	}

	if customAttrs != nil {
		existing := make(map[string]any)
		if strings.TrimSpace(contact.CustomAttributes) != "" {
			_ = json.Unmarshal([]byte(contact.CustomAttributes), &existing)
		}
		for k, v := range customAttrs {
			if v == nil {
				delete(existing, k)
			} else {
				existing[k] = v
			}
		}
		mergedBytes, err := json.Marshal(existing)
		if err != nil {
			return nil, err
		}
		updates["custom_attributes"] = string(mergedBytes)
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.Contact{}).
			Where("account_id = ? AND id = ?", accountID, contactID).
			Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	var updated domain.Contact
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, contactID).First(&updated).Error; err != nil {
		return nil, err
	}
	return &updated, nil
}

// ListContactConversations lists all conversations for a contact in an inbox with optional status filter
func (r *PublicRepository) ListContactConversations(ctx context.Context, accountID, inboxID, contactID uint, status string, page, pageSize int) ([]domain.Conversation, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var convs []domain.Conversation
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Conversation{}).
		Where("account_id = ? AND inbox_id = ? AND contact_id = ?", accountID, inboxID, contactID)

	trimmedStatus := strings.TrimSpace(status)
	if trimmedStatus != "" && trimmedStatus != "all" {
		query = query.Where("status = ?", trimmedStatus)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Messages", func(db *gorm.DB) *gorm.DB {
		return db.Where("private = false").Order("messages.id ASC")
	}).Preload("Assignee").Preload("Labels").
		Order("id DESC").
		Offset(offset).Limit(pageSize).
		Find(&convs).Error

	if err != nil {
		return nil, 0, err
	}
	return convs, total, nil
}

// GetContactConversation retrieves a single conversation detail strictly ensuring ownership
func (r *PublicRepository) GetContactConversation(ctx context.Context, accountID, inboxID, contactID, convID uint) (*domain.Conversation, error) {
	var conv domain.Conversation
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND inbox_id = ? AND contact_id = ? AND id = ?", accountID, inboxID, contactID, convID).
		Preload("Messages", func(db *gorm.DB) *gorm.DB {
			return db.Where("private = false").Order("messages.id ASC")
		}).Preload("Assignee").Preload("Labels").
		First(&conv).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &conv, nil
}

// ListConversationMessages retrieves message history for a public conversation filtering out private notes
func (r *PublicRepository) ListConversationMessages(ctx context.Context, accountID, convID uint, beforeID, afterID uint, page, pageSize int) ([]domain.Message, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var msgs []domain.Message
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Message{}).
		Where("account_id = ? AND conversation_id = ? AND private = false", accountID, convID)

	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	if afterID > 0 {
		query = query.Where("id > ?", afterID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Order("id ASC").Offset(offset).Limit(pageSize).Find(&msgs).Error
	if err != nil {
		return nil, 0, err
	}

	return msgs, total, nil
}

// UpdateConversationStatus updates conversation status and refreshes activity timestamps
func (r *PublicRepository) UpdateConversationStatus(ctx context.Context, accountID, convID uint, newStatus string) (*domain.Conversation, error) {
	var conv domain.Conversation
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, convID).First(&conv).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	updates := map[string]any{
		"status":           newStatus,
		"last_activity_at": now,
		"updated_at":       now,
	}

	if err := r.db.WithContext(ctx).Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, convID).
		Updates(updates).Error; err != nil {
		return nil, err
	}

	conv.Status = newStatus
	conv.LastActivityAt = now
	conv.UpdatedAt = now
	return &conv, nil
}

// MergeConversationCustomAttributes performs incremental shallow merge on conversation custom attributes
func (r *PublicRepository) MergeConversationCustomAttributes(ctx context.Context, accountID, convID uint, newAttrs map[string]any) (map[string]any, error) {
	var conv domain.Conversation
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, convID).First(&conv).Error; err != nil {
		return nil, err
	}

	existing := make(map[string]any)
	if strings.TrimSpace(conv.CustomAttributes) != "" {
		_ = json.Unmarshal([]byte(conv.CustomAttributes), &existing)
	}

	for k, v := range newAttrs {
		if v == nil {
			delete(existing, k)
		} else {
			existing[k] = v
		}
	}

	mergedBytes, err := json.Marshal(existing)
	if err != nil {
		return nil, err
	}

	if err := r.db.WithContext(ctx).Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, convID).
		Update("custom_attributes", string(mergedBytes)).Error; err != nil {
		return nil, err
	}

	return existing, nil
}
