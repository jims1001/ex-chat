package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// DestroyContactCustomAttributes removes specified custom attribute keys from a contact
func (r *WidgetRepository) DestroyContactCustomAttributes(ctx context.Context, accountID, contactID uint, keys []string) (map[string]any, error) {
	var contact domain.Contact
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		return nil, err
	}

	attrs := make(map[string]any)
	if strings.TrimSpace(contact.CustomAttributes) != "" {
		_ = json.Unmarshal([]byte(contact.CustomAttributes), &attrs)
	}

	for _, k := range keys {
		delete(attrs, strings.TrimSpace(k))
	}

	rawJSON, _ := json.Marshal(attrs)
	if err := r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("account_id = ? AND id = ?", accountID, contactID).
		Update("custom_attributes", string(rawJSON)).Error; err != nil {
		return nil, err
	}

	return attrs, nil
}

// SetUser resolves or creates a contact for window.$chatwoot.setUser, binds ContactInbox, and merges attributes
func (r *WidgetRepository) SetUser(ctx context.Context, inbox *domain.Inbox, identifier, name, email, phone, avatarURL string, customAttrs map[string]any, sourceID string) (*domain.Contact, error) {
	var contact *domain.Contact

	identifier = strings.TrimSpace(identifier)
	email = strings.ToLower(strings.TrimSpace(email))
	phone = strings.TrimSpace(phone)
	name = strings.TrimSpace(name)
	avatarURL = strings.TrimSpace(avatarURL)
	sourceID = strings.TrimSpace(sourceID)

	// 1. Try finding by identifier in this account
	if identifier != "" {
		var c domain.Contact
		if err := r.db.WithContext(ctx).Where("account_id = ? AND identifier = ?", inbox.AccountID, identifier).First(&c).Error; err == nil && c.ID > 0 {
			contact = &c
		}
	}

	// 2. Try finding by email in this account
	if contact == nil && email != "" {
		var c domain.Contact
		if err := r.db.WithContext(ctx).Where("account_id = ? AND email = ?", inbox.AccountID, email).First(&c).Error; err == nil && c.ID > 0 {
			contact = &c
		}
	}

	// 3. Try finding by sourceID in this inbox
	if contact == nil && sourceID != "" {
		var ci domain.ContactInbox
		if err := r.db.WithContext(ctx).Preload("Contact").
			Where("inbox_id = ? AND source_id = ?", inbox.ID, sourceID).First(&ci).Error; err == nil && ci.Contact != nil {
			contact = ci.Contact
		}
	}

	// 4. Create new contact if not found
	if contact == nil {
		displayName := name
		if displayName == "" {
			if email != "" {
				displayName = strings.Split(email, "@")[0]
			} else if identifier != "" {
				displayName = "User " + identifier
			} else {
				displayName = "Visitor"
			}
		}

		newContact := domain.Contact{
			AccountID:   inbox.AccountID,
			Name:        displayName,
			Email:       email,
			PhoneNumber: phone,
			Identifier:  identifier,
			AvatarURL:   avatarURL,
		}
		if customAttrs != nil {
			b, _ := json.Marshal(customAttrs)
			newContact.CustomAttributes = string(b)
		}
		if err := r.db.WithContext(ctx).Create(&newContact).Error; err != nil {
			return nil, err
		}
		contact = &newContact
	} else {
		// Update existing contact
		updates := make(map[string]any)
		if name != "" && contact.Name != name {
			updates["name"] = name
			contact.Name = name
		}
		if email != "" && contact.Email != email {
			updates["email"] = email
			contact.Email = email
		}
		if phone != "" && contact.PhoneNumber != phone {
			updates["phone_number"] = phone
			contact.PhoneNumber = phone
		}
		if identifier != "" && contact.Identifier != identifier {
			updates["identifier"] = identifier
			contact.Identifier = identifier
		}
		if avatarURL != "" && contact.AvatarURL != avatarURL {
			updates["avatar_url"] = avatarURL
			contact.AvatarURL = avatarURL
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
			_ = r.db.WithContext(ctx).Model(&domain.Contact{}).
				Where("account_id = ? AND id = ?", inbox.AccountID, contact.ID).
				Updates(updates).Error
		}
	}

	// 5. Ensure ContactInbox mapping exists
	effectiveSourceID := sourceID
	if effectiveSourceID == "" {
		if identifier != "" {
			effectiveSourceID = identifier
		} else {
			effectiveSourceID = fmt.Sprintf("src_%d_%d", inbox.ID, contact.ID)
		}
	}

	var ci domain.ContactInbox
	err := r.db.WithContext(ctx).Where("inbox_id = ? AND contact_id = ?", inbox.ID, contact.ID).First(&ci).Error
	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		ci = domain.ContactInbox{
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			SourceID:  effectiveSourceID,
		}
		_ = r.db.WithContext(ctx).Create(&ci).Error
	} else if ci.SourceID != "" && effectiveSourceID != "" && ci.SourceID != effectiveSourceID {
		_ = r.db.WithContext(ctx).Model(&ci).Update("source_id", effectiveSourceID).Error
	}

	return contact, nil
}

// UpdateMessageSubmittedValues updates message content, submitted_values or interactive properties
func (r *WidgetRepository) UpdateMessageSubmittedValues(ctx context.Context, messageID, conversationID uint, submittedValues map[string]any, content string) (*domain.Message, error) {
	var msg domain.Message
	if err := r.db.WithContext(ctx).Where("id = ? AND conversation_id = ?", messageID, conversationID).First(&msg).Error; err != nil {
		return nil, err
	}

	updates := make(map[string]any)
	if strings.TrimSpace(content) != "" {
		updates["content"] = content
		msg.Content = content
	}

	if submittedValues != nil {
		existing := make(map[string]any)
		if strings.TrimSpace(msg.ContentAttributes) != "" {
			_ = json.Unmarshal([]byte(msg.ContentAttributes), &existing)
		}
		existing["submitted_values"] = submittedValues
		raw, _ := json.Marshal(existing)
		updates["content_attributes"] = string(raw)
		msg.ContentAttributes = string(raw)
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.Message{}).Where("id = ?", messageID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	return &msg, nil
}
