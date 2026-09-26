package repository

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ContactAttachmentItem represents an attachment linked to a contact via messages and conversations
type ContactAttachmentItem struct {
	ID             uint      `json:"id"`
	AccountID      uint      `json:"account_id"`
	MessageID      uint      `json:"message_id"`
	ConversationID uint      `json:"conversation_id"`
	FileType       string    `json:"file_type"`
	DataURL        string    `json:"data_url"`
	FileSize       int64     `json:"file_size"`
	SenderType     string    `json:"sender_type"`
	SenderID       uint      `json:"sender_id"`
	MessageContent string    `json:"message_content"`
	CreatedAt      time.Time `json:"created_at"`
}

// ContactAttachmentFilter holds filtering options for contact attachments
type ContactAttachmentFilter struct {
	FileType       string
	ConversationID *uint
	SenderType     string
	Search         string
	Page           int
	PageSize       int
}

// ContactableInboxItem represents an inbox through which a contact can be reached
type ContactableInboxItem struct {
	Inbox        domain.Inbox         `json:"inbox"`
	ContactInbox *domain.ContactInbox `json:"contact_inbox,omitempty"`
	SourceID     string               `json:"source_id"`
}

// ContactStats summarizes a contact's interactions across the system
type ContactStats struct {
	ContactID                  uint       `json:"contact_id"`
	ConversationsCount         int64      `json:"conversations_count"`
	OpenConversationsCount     int64      `json:"open_conversations_count"`
	ResolvedConversationsCount int64      `json:"resolved_conversations_count"`
	MessagesCount              int64      `json:"messages_count"`
	AttachmentsCount           int64      `json:"attachments_count"`
	ContactInboxesCount        int64      `json:"contact_inboxes_count"`
	FirstSeenAt                *time.Time `json:"first_seen_at,omitempty"`
	LastActivityAt             *time.Time `json:"last_activity_at,omitempty"`
}

// ContactExtensionRepository handles advanced CRM queries for contacts
type ContactExtensionRepository struct {
	db            *gorm.DB
	inboxes       *InboxRepository
	conversations *ConversationRepository
	messages      *MessageRepository
}

// NewContactExtensionRepository creates a new repository instance
func NewContactExtensionRepository(db *gorm.DB) *ContactExtensionRepository {
	return NewContactExtensionRepositoryWithOwnerPorts(db, NewInboxRepository(db), NewConversationRepository(db), NewMessageRepository(db))
}

func NewContactExtensionRepositoryWithOwnerPorts(db *gorm.DB, inboxes *InboxRepository, conversations *ConversationRepository, messages *MessageRepository) *ContactExtensionRepository {
	return &ContactExtensionRepository{db: db, inboxes: inboxes, conversations: conversations, messages: messages}
}

func (r *ContactExtensionRepository) findContact(accountID, contactID uint) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &contact, err
}

func (r *ContactExtensionRepository) hydrateContactInboxes(accountID uint, list []domain.ContactInbox) ([]domain.ContactInbox, error) {
	if len(list) == 0 {
		return []domain.ContactInbox{}, nil
	}
	if r.inboxes == nil {
		return nil, gorm.ErrInvalidData
	}
	ids := make([]uint, 0, len(list))
	for _, item := range list {
		ids = append(ids, item.InboxID)
	}
	inboxes, err := r.inboxes.FindByIDs(accountID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]*domain.Inbox, len(inboxes))
	for i := range inboxes {
		byID[inboxes[i].ID] = &inboxes[i]
	}
	result := make([]domain.ContactInbox, 0, len(list))
	for i := range list {
		if inbox := byID[list[i].InboxID]; inbox != nil {
			list[i].Inbox = inbox
			result = append(result, list[i])
		}
	}
	return result, nil
}

// ListContactAttachments retrieves all attachments associated with a contact's conversations
func (r *ContactExtensionRepository) ListContactAttachments(accountID, contactID uint, filter ContactAttachmentFilter) ([]ContactAttachmentItem, int64, error) {
	contact, err := r.findContact(accountID, contactID)
	if err != nil {
		return nil, 0, err
	}
	if contact == nil {
		return []ContactAttachmentItem{}, 0, nil
	}
	if r.conversations == nil || r.messages == nil {
		return nil, 0, gorm.ErrInvalidData
	}
	conversationIDs, err := r.conversations.FindIDsByContactIDs(accountID, []uint{contactID})
	if err != nil {
		return nil, 0, err
	}
	return r.messages.ListAttachmentsByConversationIDs(accountID, conversationIDs, CompanyAttachmentFilter{
		FileType: filter.FileType, ConversationID: filter.ConversationID, SenderType: filter.SenderType,
		Search: filter.Search, Page: filter.Page, PageSize: filter.PageSize,
	})
}

// GetContactableInboxes returns inboxes that can contact this customer
func (r *ContactExtensionRepository) GetContactableInboxes(accountID, contactID uint) ([]ContactableInboxItem, error) {
	contact, err := r.findContact(accountID, contactID)
	if err != nil || contact == nil {
		return nil, err
	}
	if r.inboxes == nil {
		return nil, gorm.ErrInvalidData
	}
	allInboxes, err := r.inboxes.ListByAccount(accountID)
	if err != nil {
		return nil, err
	}
	sort.Slice(allInboxes, func(i, j int) bool { return allInboxes[i].ID < allInboxes[j].ID })

	var existingContactInboxes []domain.ContactInbox
	if err := r.db.Where("contact_id = ?", contactID).Find(&existingContactInboxes).Error; err != nil {
		return nil, err
	}

	ciMap := make(map[uint]domain.ContactInbox)
	for _, ci := range existingContactInboxes {
		ciMap[ci.InboxID] = ci
	}

	var result []ContactableInboxItem
	for _, inbox := range allInboxes {
		if ci, exists := ciMap[inbox.ID]; exists {
			copyCi := ci
			result = append(result, ContactableInboxItem{
				Inbox:        inbox,
				ContactInbox: &copyCi,
				SourceID:     ci.SourceID,
			})
			continue
		}

		chType := strings.ToLower(inbox.ChannelType)
		var sourceID string
		canContact := false

		switch {
		case strings.Contains(chType, "email"):
			if contact.Email != "" {
				sourceID = contact.Email
				canContact = true
			}
		case strings.Contains(chType, "twilio") || strings.Contains(chType, "whatsapp") || strings.Contains(chType, "sms"):
			if contact.PhoneNumber != "" {
				sourceID = contact.PhoneNumber
				canContact = true
			}
		case strings.Contains(chType, "api"):
			if contact.Identifier != "" {
				sourceID = contact.Identifier
				canContact = true
			} else if contact.Email != "" {
				sourceID = contact.Email
				canContact = true
			} else if contact.PhoneNumber != "" {
				sourceID = contact.PhoneNumber
				canContact = true
			}
		}

		if canContact {
			result = append(result, ContactableInboxItem{
				Inbox:        inbox,
				ContactInbox: nil,
				SourceID:     sourceID,
			})
		}
	}

	return result, nil
}

// ListContactInboxes returns all linked ContactInboxes for a contact
func (r *ContactExtensionRepository) ListContactInboxes(accountID, contactID uint) ([]domain.ContactInbox, error) {
	contact, err := r.findContact(accountID, contactID)
	if err != nil || contact == nil {
		return nil, err
	}
	var list []domain.ContactInbox
	err = r.db.Where("contact_id = ?", contactID).
		Order("contact_inboxes.id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return r.hydrateContactInboxes(accountID, list)
}

// CreateContactInbox links an inbox identity to a contact
func (r *ContactExtensionRepository) CreateContactInbox(accountID, contactID, inboxID uint, sourceID string) (*domain.ContactInbox, error) {
	var contact domain.Contact
	if err := r.db.Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		return nil, fmt.Errorf("contact not found: %w", err)
	}

	if r.inboxes == nil {
		return nil, gorm.ErrInvalidData
	}
	inbox, err := r.inboxes.FindByID(accountID, inboxID)
	if err != nil {
		return nil, fmt.Errorf("inbox not found: %w", err)
	}
	if inbox == nil {
		return nil, fmt.Errorf("inbox not found: %w", gorm.ErrRecordNotFound)
	}

	var existing domain.ContactInbox
	err = r.db.Where("contact_id = ? AND inbox_id = ?", contactID, inboxID).First(&existing).Error
	if err == nil {
		if sourceID != "" && existing.SourceID != sourceID {
			existing.SourceID = sourceID
			if err := r.db.Save(&existing).Error; err != nil {
				return nil, err
			}
		}
		existing.Inbox = inbox
		return &existing, nil
	}

	if sourceID == "" {
		chType := strings.ToLower(inbox.ChannelType)
		switch {
		case strings.Contains(chType, "email") && contact.Email != "":
			sourceID = contact.Email
		case (strings.Contains(chType, "sms") || strings.Contains(chType, "whatsapp")) && contact.PhoneNumber != "":
			sourceID = contact.PhoneNumber
		case contact.Identifier != "":
			sourceID = contact.Identifier
		case contact.Email != "":
			sourceID = contact.Email
		case contact.PhoneNumber != "":
			sourceID = contact.PhoneNumber
		default:
			sourceID = uuid.New().String()
		}
	}

	ci := domain.ContactInbox{
		ContactID: contactID,
		InboxID:   inboxID,
		SourceID:  sourceID,
	}

	if err := r.db.Omit("Inbox", "Contact").Create(&ci).Error; err != nil {
		return nil, err
	}

	ci.Inbox = inbox
	return &ci, nil
}

// DeleteContactInbox removes a specific ContactInbox record
func (r *ContactExtensionRepository) DeleteContactInbox(accountID, contactID, contactInboxID uint) error {
	contact, err := r.findContact(accountID, contactID)
	if err != nil {
		return err
	}
	if contact == nil {
		return gorm.ErrRecordNotFound
	}
	var ci domain.ContactInbox
	if err := r.db.Where("contact_id = ? AND id = ?", contactID, contactInboxID).First(&ci).Error; err != nil {
		return err
	}
	if r.inboxes == nil {
		return gorm.ErrInvalidData
	}
	exists, err := r.inboxes.Exists(accountID, ci.InboxID)
	if err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	return r.db.Delete(&ci).Error
}

// DeleteContactInboxByInboxID removes a ContactInbox record by inbox_id
func (r *ContactExtensionRepository) DeleteContactInboxByInboxID(accountID, contactID, inboxID uint) error {
	contact, err := r.findContact(accountID, contactID)
	if err != nil {
		return err
	}
	if contact == nil {
		return gorm.ErrRecordNotFound
	}
	if r.inboxes == nil {
		return gorm.ErrInvalidData
	}
	exists, err := r.inboxes.Exists(accountID, inboxID)
	if err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	result := r.db.Where("contact_id = ? AND inbox_id = ?", contactID, inboxID).Delete(&domain.ContactInbox{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ListContactConversations lists all conversations belonging to a contact
func (r *ContactExtensionRepository) ListContactConversations(accountID, contactID uint, status string, inboxID *uint, page, pageSize int) ([]domain.Conversation, int64, error) {
	contact, err := r.findContact(accountID, contactID)
	if err != nil {
		return nil, 0, err
	}
	if contact == nil {
		return []domain.Conversation{}, 0, nil
	}
	if r.conversations == nil {
		return nil, 0, gorm.ErrInvalidData
	}
	return r.conversations.ListByContactIDs(accountID, []uint{contactID}, status, inboxID, &contactID, page, pageSize)
}

// GetContactStats gathers comprehensive interaction statistics for a contact
func (r *ContactExtensionRepository) GetContactStats(accountID, contactID uint) (*ContactStats, error) {
	contact, err := r.findContact(accountID, contactID)
	if err != nil || contact == nil {
		return nil, err
	}

	stats := &ContactStats{
		ContactID: contactID,
	}

	if r.conversations == nil || r.messages == nil {
		return nil, gorm.ErrInvalidData
	}
	convStats, err := r.conversations.GetStatsByContactIDs(accountID, []uint{contactID})
	if err != nil {
		return nil, err
	}
	stats.ConversationsCount, stats.OpenConversationsCount, stats.ResolvedConversationsCount = convStats.Total, convStats.Open, convStats.Resolved
	stats.MessagesCount, stats.AttachmentsCount, err = r.messages.CountActivityByConversationIDs(accountID, convStats.ConversationIDs)
	if err != nil {
		return nil, err
	}

	// 4. Contact inboxes count
	contactInboxes, err := r.ListContactInboxes(accountID, contactID)
	if err != nil {
		return nil, err
	}
	stats.ContactInboxesCount = int64(len(contactInboxes))

	// 5. First seen & last activity timestamps
	if convStats.FirstInteractionAt != nil {
		stats.FirstSeenAt = convStats.FirstInteractionAt
	} else {
		t := contact.CreatedAt
		stats.FirstSeenAt = &t
	}
	if convStats.LastActivityAt != nil && !convStats.LastActivityAt.IsZero() {
		stats.LastActivityAt = convStats.LastActivityAt
	} else if stats.FirstSeenAt != nil {
		stats.LastActivityAt = stats.FirstSeenAt
	}

	return stats, nil
}
