package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// WidgetRepository handles visitor-facing operations for conversations, campaigns, events, labels and partial attributes
type WidgetRepository struct {
	db *gorm.DB
}

// NewWidgetRepository creates a new WidgetRepository
func NewWidgetRepository(db *gorm.DB) *WidgetRepository {
	return &WidgetRepository{db: db}
}

// GetInboxByWebsiteToken retrieves the inbox associated with a website token
func (r *WidgetRepository) GetInboxByWebsiteToken(ctx context.Context, token string) (*domain.Inbox, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("empty website token")
	}
	var inbox domain.Inbox
	err := r.db.WithContext(ctx).Where("website_token = ?", strings.TrimSpace(token)).First(&inbox).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inbox, nil
}

// FindContactBySourceID locates a visitor contact by inbox ID and source ID
func (r *WidgetRepository) FindContactBySourceID(ctx context.Context, inboxID uint, sourceID string) (*domain.Contact, error) {
	if strings.TrimSpace(sourceID) == "" {
		return nil, nil
	}
	var ci domain.ContactInbox
	err := r.db.WithContext(ctx).Preload("Contact").
		Where("inbox_id = ? AND source_id = ?", inboxID, strings.TrimSpace(sourceID)).
		First(&ci).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return ci.Contact, nil
}

// ListContactConversations lists all conversations for a visitor contact in an inbox
func (r *WidgetRepository) ListContactConversations(ctx context.Context, accountID, inboxID, contactID uint, page, pageSize int) ([]domain.Conversation, int64, error) {
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

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Messages", func(db *gorm.DB) *gorm.DB {
		return db.Order("messages.id ASC")
	}).Preload("Assignee").Preload("Labels").
		Order("id DESC").
		Offset(offset).Limit(pageSize).
		Find(&convs).Error

	if err != nil {
		return nil, 0, err
	}
	return convs, total, nil
}

// GetConversationDetail retrieves a single conversation detail with messages for a visitor
func (r *WidgetRepository) GetConversationDetail(ctx context.Context, accountID, inboxID, contactID, conversationID uint) (*domain.Conversation, error) {
	var conv domain.Conversation
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND inbox_id = ? AND contact_id = ? AND id = ?", accountID, inboxID, contactID, conversationID).
		Preload("Messages", func(db *gorm.DB) *gorm.DB {
			return db.Order("messages.id ASC")
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

// ListActiveCampaigns retrieves all ongoing active campaigns for the widget inbox
func (r *WidgetRepository) ListActiveCampaigns(ctx context.Context, accountID, inboxID uint) ([]domain.Campaign, error) {
	var campaigns []domain.Campaign
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND inbox_id = ? AND status = ? AND campaign_type = ?",
			accountID, inboxID, "active", "ongoing").
		Order("id DESC").
		Find(&campaigns).Error
	if err != nil {
		return nil, err
	}
	return campaigns, nil
}

// RecordWidgetEvent persists a visitor interaction or telemetry event
func (r *WidgetRepository) RecordWidgetEvent(ctx context.Context, event *domain.WidgetEvent) error {
	if event == nil {
		return errors.New("nil widget event")
	}
	return r.db.WithContext(ctx).Create(event).Error
}

// ListWidgetEvents queries visitor events with optional source/contact/conversation filters
func (r *WidgetRepository) ListWidgetEvents(ctx context.Context, accountID, inboxID uint, sourceID string, contactID *uint, conversationID *uint, page, pageSize int) ([]domain.WidgetEvent, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var events []domain.WidgetEvent
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.WidgetEvent{}).
		Where("account_id = ? AND inbox_id = ?", accountID, inboxID)

	if strings.TrimSpace(sourceID) != "" {
		query = query.Where("source_id = ?", strings.TrimSpace(sourceID))
	}
	if contactID != nil && *contactID > 0 {
		query = query.Where("contact_id = ?", *contactID)
	}
	if conversationID != nil && *conversationID > 0 {
		query = query.Where("conversation_id = ?", *conversationID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Order("id DESC").Offset(offset).Limit(pageSize).Find(&events).Error
	if err != nil {
		return nil, 0, err
	}

	return events, total, nil
}

// AttachLabels attaches label names to a visitor contact and optionally to a conversation
func (r *WidgetRepository) AttachLabels(ctx context.Context, accountID, contactID uint, conversationID *uint, labelNames []string) ([]domain.Label, error) {
	var result []domain.Label
	if len(labelNames) == 0 {
		return result, nil
	}

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, name := range labelNames {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}

			var label domain.Label
			err := tx.Where("account_id = ? AND LOWER(title) = ?", accountID, strings.ToLower(trimmed)).First(&label).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				label = domain.Label{
					AccountID: accountID,
					Title:     trimmed,
					Color:     "#1f93ff",
				}
				if err := tx.Create(&label).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			}

			result = append(result, label)

			// 1. Link to contact_labels
			cl := domain.ContactLabel{
				ContactID: contactID,
				LabelID:   label.ID,
			}
			if err := tx.Where("contact_id = ? AND label_id = ?", contactID, label.ID).FirstOrCreate(&cl).Error; err != nil {
				return err
			}

			// 2. Link to conversation_labels if conversation specified
			if conversationID != nil && *conversationID > 0 {
				convLabel := domain.ConversationLabel{
					ConversationID: *conversationID,
					LabelID:        label.ID,
				}
				if err := tx.Where("conversation_id = ? AND label_id = ?", *conversationID, label.ID).FirstOrCreate(&convLabel).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}

// RemoveLabels removes specified labels from a visitor contact and optionally conversation
func (r *WidgetRepository) RemoveLabels(ctx context.Context, accountID, contactID uint, conversationID *uint, labelNames []string) error {
	if len(labelNames) == 0 {
		return nil
	}

	var labelIDs []uint
	for _, name := range labelNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		var label domain.Label
		if err := r.db.WithContext(ctx).Where("account_id = ? AND LOWER(title) = ?", accountID, strings.ToLower(trimmed)).First(&label).Error; err == nil {
			labelIDs = append(labelIDs, label.ID)
		}
	}

	if len(labelIDs) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("contact_id = ? AND label_id IN ?", contactID, labelIDs).Delete(&domain.ContactLabel{}).Error; err != nil {
			return err
		}
		if conversationID != nil && *conversationID > 0 {
			if err := tx.Where("conversation_id = ? AND label_id IN ?", *conversationID, labelIDs).Delete(&domain.ConversationLabel{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetVisitorLabels retrieves labels for a contact or conversation
func (r *WidgetRepository) GetVisitorLabels(ctx context.Context, accountID, contactID uint, conversationID *uint) ([]domain.Label, error) {
	var labels []domain.Label
	if conversationID != nil && *conversationID > 0 {
		err := r.db.WithContext(ctx).
			Joins("JOIN conversation_labels ON conversation_labels.label_id = labels.id").
			Where("labels.account_id = ? AND conversation_labels.conversation_id = ?", accountID, *conversationID).
			Find(&labels).Error
		return labels, err
	}

	err := r.db.WithContext(ctx).
		Joins("JOIN contact_labels ON contact_labels.label_id = labels.id").
		Where("labels.account_id = ? AND contact_labels.contact_id = ?", accountID, contactID).
		Find(&labels).Error
	return labels, err
}

// MergeContactCustomAttributes performs an incremental shallow merge on contact custom attributes
func (r *WidgetRepository) MergeContactCustomAttributes(ctx context.Context, accountID, contactID uint, newAttrs map[string]any) (map[string]any, error) {
	var contact domain.Contact
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		return nil, err
	}

	existing := make(map[string]any)
	if strings.TrimSpace(contact.CustomAttributes) != "" {
		_ = json.Unmarshal([]byte(contact.CustomAttributes), &existing)
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

	contact.CustomAttributes = string(mergedBytes)
	if err := r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("account_id = ? AND id = ?", accountID, contactID).
		Update("custom_attributes", contact.CustomAttributes).Error; err != nil {
		return nil, err
	}

	return existing, nil
}

// MergeConversationCustomAttributes performs an incremental shallow merge on conversation custom attributes
func (r *WidgetRepository) MergeConversationCustomAttributes(ctx context.Context, accountID, conversationID uint, newAttrs map[string]any) (map[string]any, error) {
	var conv domain.Conversation
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, conversationID).First(&conv).Error; err != nil {
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

	conv.CustomAttributes = string(mergedBytes)
	if err := r.db.WithContext(ctx).Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, conversationID).
		Update("custom_attributes", conv.CustomAttributes).Error; err != nil {
		return nil, err
	}

	return existing, nil
}

// UpdateContactInfo partially updates contact fields and optionally merges custom attributes
func (r *WidgetRepository) UpdateContactInfo(ctx context.Context, accountID, contactID uint, name, email, phone, identifier string, customAttrs map[string]any) (*domain.Contact, error) {
	var contact domain.Contact
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		return nil, err
	}

	updates := make(map[string]any)
	if strings.TrimSpace(name) != "" {
		updates["name"] = strings.TrimSpace(name)
		contact.Name = strings.TrimSpace(name)
	}
	if strings.TrimSpace(email) != "" {
		updates["email"] = strings.ToLower(strings.TrimSpace(email))
		contact.Email = updates["email"].(string)
	}
	if strings.TrimSpace(phone) != "" {
		updates["phone_number"] = strings.TrimSpace(phone)
		contact.PhoneNumber = strings.TrimSpace(phone)
	}
	if strings.TrimSpace(identifier) != "" {
		updates["identifier"] = strings.TrimSpace(identifier)
		contact.Identifier = strings.TrimSpace(identifier)
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
		mergedBytes, _ := json.Marshal(existing)
		updates["custom_attributes"] = string(mergedBytes)
		contact.CustomAttributes = string(mergedBytes)
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.Contact{}).
			Where("account_id = ? AND id = ?", accountID, contactID).
			Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	return &contact, nil
}
