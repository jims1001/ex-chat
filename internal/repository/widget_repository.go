package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// WidgetRepository owns API widget events and composes visitor operations through domain owners.
type WidgetRepository struct {
	db            *gorm.DB
	inboxes       *InboxRepository
	contacts      *ContactRepository
	conversations *ConversationRepository
	campaigns     *CampaignRepository
	labels        *LabelRepository
	messages      *MessageRepository
}

func NewWidgetRepository(db *gorm.DB) *WidgetRepository {
	// Use local owner adapters without their wider orchestration graphs. This
	// constructor serves the combined runtime; extracted runtimes inject ports.
	contacts := &ContactRepository{db: db}
	conversations := &ConversationRepository{db: db}
	messages := &MessageRepository{db: db}
	return NewWidgetRepositoryWithOwnerPorts(db, NewInboxRepository(db), contacts, conversations, NewCampaignRepository(db), NewLabelRepositoryWithConversationPort(db, conversations), messages)
}

func NewWidgetRepositoryWithOwnerPorts(db *gorm.DB, inboxes *InboxRepository, contacts *ContactRepository, conversations *ConversationRepository, campaigns *CampaignRepository, labels *LabelRepository, messages *MessageRepository) *WidgetRepository {
	return &WidgetRepository{db: db, inboxes: inboxes, contacts: contacts, conversations: conversations, campaigns: campaigns, labels: labels, messages: messages}
}

func (r *WidgetRepository) ReassignContact(ctx context.Context, accountID, fromContactID, toContactID uint) error {
	return r.db.WithContext(ctx).Model(&domain.WidgetEvent{}).Where("account_id = ? AND contact_id = ?", accountID, fromContactID).Update("contact_id", toContactID).Error
}

func (r *WidgetRepository) GetInboxByWebsiteToken(ctx context.Context, token string) (*domain.Inbox, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("empty website token")
	}
	if r.inboxes == nil {
		return nil, gorm.ErrInvalidData
	}
	inbox, err := r.inboxes.FindByWebsiteToken(strings.TrimSpace(token))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return inbox, err
}

func (r *WidgetRepository) FindContactBySourceID(ctx context.Context, inboxID uint, sourceID string) (*domain.Contact, error) {
	if strings.TrimSpace(sourceID) == "" {
		return nil, nil
	}
	if r.contacts == nil {
		return nil, gorm.ErrInvalidData
	}
	return r.contacts.FindContactBySourceID(inboxID, strings.TrimSpace(sourceID))
}

func (r *WidgetRepository) ListContactConversations(ctx context.Context, accountID, inboxID, contactID uint, page, pageSize int) ([]domain.Conversation, int64, error) {
	if r.conversations == nil || r.messages == nil {
		return nil, 0, gorm.ErrInvalidData
	}
	conversations, total, err := r.conversations.ListForWidget(accountID, inboxID, contactID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	for i := range conversations {
		messages, _, err := r.messages.ListPublicByConversation(accountID, conversations[i].ID, 0, 0, 1, 100)
		if err != nil {
			return nil, 0, err
		}
		conversations[i].Messages = messages
		if r.labels != nil {
			labels, err := r.labels.GetConversationLabels(accountID, conversations[i].ID)
			if err != nil {
				return nil, 0, err
			}
			conversations[i].Labels = labels
		}
	}
	return conversations, total, nil
}

func (r *WidgetRepository) GetConversationDetail(ctx context.Context, accountID, inboxID, contactID, conversationID uint) (*domain.Conversation, error) {
	if r.conversations == nil || r.messages == nil {
		return nil, gorm.ErrInvalidData
	}
	conversation, err := r.conversations.FindForInbox(accountID, inboxID, conversationID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil || conversation.ContactID != contactID {
		return nil, err
	}
	messages, _, err := r.messages.ListPublicByConversation(accountID, conversationID, 0, 0, 1, 100)
	if err != nil {
		return nil, err
	}
	conversation.Messages = messages
	if r.labels != nil {
		conversation.Labels, err = r.labels.GetConversationLabels(accountID, conversationID)
		if err != nil {
			return nil, err
		}
	}
	return conversation, nil
}

func (r *WidgetRepository) ListActiveCampaigns(ctx context.Context, accountID, inboxID uint) ([]domain.Campaign, error) {
	if r.campaigns == nil {
		return nil, gorm.ErrInvalidData
	}
	return r.campaigns.ListActiveOngoing(ctx, accountID, inboxID)
}

func (r *WidgetRepository) RecordWidgetEvent(ctx context.Context, event *domain.WidgetEvent) error {
	if event == nil {
		return errors.New("nil widget event")
	}
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *WidgetRepository) ListWidgetEvents(ctx context.Context, accountID, inboxID uint, sourceID string, contactID, conversationID *uint, page, pageSize int) ([]domain.WidgetEvent, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}
	query := r.db.WithContext(ctx).Model(&domain.WidgetEvent{}).Where("account_id = ? AND inbox_id = ?", accountID, inboxID)
	if strings.TrimSpace(sourceID) != "" {
		query = query.Where("source_id = ?", strings.TrimSpace(sourceID))
	}
	if contactID != nil && *contactID > 0 {
		query = query.Where("contact_id = ?", *contactID)
	}
	if conversationID != nil && *conversationID > 0 {
		query = query.Where("conversation_id = ?", *conversationID)
	}
	var events []domain.WidgetEvent
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&events).Error
	return events, total, err
}

func (r *WidgetRepository) AttachLabels(ctx context.Context, accountID, contactID uint, conversationID *uint, labelNames []string) ([]domain.Label, error) {
	if r.labels == nil {
		return nil, gorm.ErrInvalidData
	}
	result := make([]domain.Label, 0, len(labelNames))
	for _, name := range labelNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		label, err := r.labels.FindOrCreateByTitle(accountID, name)
		if err != nil {
			return nil, err
		}
		if err := r.labels.AttachToContact(contactID, label.ID); err != nil {
			return nil, err
		}
		if conversationID != nil && *conversationID > 0 {
			if err := r.labels.AttachToConversation(accountID, *conversationID, label.ID); err != nil {
				return nil, err
			}
		}
		result = append(result, *label)
	}
	return result, nil
}

func (r *WidgetRepository) RemoveLabels(ctx context.Context, accountID, contactID uint, conversationID *uint, labelNames []string) error {
	if r.labels == nil {
		return gorm.ErrInvalidData
	}
	for _, name := range labelNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		label, err := r.labels.FindByTitle(accountID, name)
		if err != nil {
			return err
		}
		if label == nil {
			continue
		}
		if err := r.labels.DetachFromContact(contactID, label.ID); err != nil {
			return err
		}
		if conversationID != nil && *conversationID > 0 {
			if err := r.labels.DetachFromConversation(accountID, *conversationID, label.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *WidgetRepository) GetVisitorLabels(ctx context.Context, accountID, contactID uint, conversationID *uint) ([]domain.Label, error) {
	if r.labels == nil {
		return nil, gorm.ErrInvalidData
	}
	if conversationID != nil && *conversationID > 0 {
		return r.labels.GetConversationLabels(accountID, *conversationID)
	}
	return r.labels.GetContactLabels(contactID)
}

func (r *WidgetRepository) MergeContactCustomAttributes(ctx context.Context, accountID, contactID uint, attrs map[string]any) (map[string]any, error) {
	if r.contacts == nil {
		return nil, gorm.ErrInvalidData
	}
	contact, err := r.contacts.UpdatePublic(accountID, contactID, nil, attrs)
	if err != nil {
		return nil, err
	}
	return decodeAttributes(contact.CustomAttributes), nil
}

func (r *WidgetRepository) MergeConversationCustomAttributes(ctx context.Context, accountID, conversationID uint, attrs map[string]any) (map[string]any, error) {
	if r.conversations == nil {
		return nil, gorm.ErrInvalidData
	}
	return r.conversations.UpdateCustomAttributes(accountID, conversationID, attrs)
}

func (r *WidgetRepository) UpdateContactInfo(ctx context.Context, accountID, contactID uint, name, email, phone, identifier string, attrs map[string]any) (*domain.Contact, error) {
	if r.contacts == nil {
		return nil, gorm.ErrInvalidData
	}
	updates := map[string]any{}
	if strings.TrimSpace(name) != "" {
		updates["name"] = strings.TrimSpace(name)
	}
	if strings.TrimSpace(email) != "" {
		updates["email"] = strings.ToLower(strings.TrimSpace(email))
	}
	if strings.TrimSpace(phone) != "" {
		updates["phone_number"] = strings.TrimSpace(phone)
	}
	if strings.TrimSpace(identifier) != "" {
		updates["identifier"] = strings.TrimSpace(identifier)
	}
	return r.contacts.UpdatePublic(accountID, contactID, updates, attrs)
}

func (r *WidgetRepository) DestroyContactCustomAttributes(ctx context.Context, accountID, contactID uint, keys []string) (map[string]any, error) {
	if r.contacts == nil {
		return nil, gorm.ErrInvalidData
	}
	contact, err := r.contacts.DestroyCustomAttributes(accountID, contactID, keys)
	if err != nil {
		return nil, err
	}
	return decodeAttributes(contact.CustomAttributes), nil
}

func (r *WidgetRepository) SetUser(ctx context.Context, inbox *domain.Inbox, identifier, name, email, phone, avatarURL string, attrs map[string]any, sourceID string) (*domain.Contact, error) {
	if inbox == nil || r.contacts == nil {
		return nil, gorm.ErrInvalidData
	}
	return r.contacts.SetWidgetUser(inbox.AccountID, inbox.ID, identifier, name, email, phone, avatarURL, attrs, sourceID)
}

func (r *WidgetRepository) UpdateMessageSubmittedValues(ctx context.Context, messageID, conversationID uint, values map[string]any, content string) (*domain.Message, error) {
	if r.messages == nil {
		return nil, gorm.ErrInvalidData
	}
	return r.messages.UpdateWidgetSubmittedValues(messageID, conversationID, values, content)
}

func decodeAttributes(raw string) map[string]any {
	result := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = unmarshalCustomAttributeObject(raw, &result)
	}
	return result
}
