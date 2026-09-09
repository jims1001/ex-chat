package repository

import (
	"errors"
	"fmt"
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
	db *gorm.DB
}

// NewContactExtensionRepository creates a new repository instance
func NewContactExtensionRepository(db *gorm.DB) *ContactExtensionRepository {
	return &ContactExtensionRepository{db: db}
}

// ListContactAttachments retrieves all attachments associated with a contact's conversations
func (r *ContactExtensionRepository) ListContactAttachments(accountID, contactID uint, filter ContactAttachmentFilter) ([]ContactAttachmentItem, int64, error) {
	var items []ContactAttachmentItem
	var total int64

	baseQuery := r.db.Table("attachments").
		Select("attachments.id, attachments.account_id, attachments.message_id, messages.conversation_id, attachments.file_type, attachments.data_url, attachments.file_size, messages.sender_type, messages.sender_id, messages.content AS message_content, attachments.created_at").
		Joins("JOIN messages ON messages.id = attachments.message_id").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("conversations.account_id = ? AND conversations.contact_id = ?", accountID, contactID)

	if filter.FileType != "" {
		ft := "%" + strings.ToLower(filter.FileType) + "%"
		baseQuery = baseQuery.Where("LOWER(attachments.file_type) LIKE ?", ft)
	}

	if filter.ConversationID != nil && *filter.ConversationID > 0 {
		baseQuery = baseQuery.Where("messages.conversation_id = ?", *filter.ConversationID)
	}

	if filter.SenderType != "" {
		baseQuery = baseQuery.Where("LOWER(messages.sender_type) = ?", strings.ToLower(filter.SenderType))
	}

	if filter.Search != "" {
		s := "%" + strings.ToLower(filter.Search) + "%"
		baseQuery = baseQuery.Where("(LOWER(messages.content) LIKE ? OR LOWER(attachments.data_url) LIKE ?)", s, s)
	}

	// Count total matching records
	countQuery := r.db.Table("attachments").
		Joins("JOIN messages ON messages.id = attachments.message_id").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("conversations.account_id = ? AND conversations.contact_id = ?", accountID, contactID)

	if filter.FileType != "" {
		ft := "%" + strings.ToLower(filter.FileType) + "%"
		countQuery = countQuery.Where("LOWER(attachments.file_type) LIKE ?", ft)
	}
	if filter.ConversationID != nil && *filter.ConversationID > 0 {
		countQuery = countQuery.Where("messages.conversation_id = ?", *filter.ConversationID)
	}
	if filter.SenderType != "" {
		countQuery = countQuery.Where("LOWER(messages.sender_type) = ?", strings.ToLower(filter.SenderType))
	}
	if filter.Search != "" {
		s := "%" + strings.ToLower(filter.Search) + "%"
		countQuery = countQuery.Where("(LOWER(messages.content) LIKE ? OR LOWER(attachments.data_url) LIKE ?)", s, s)
	}

	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize

	err := baseQuery.Order("attachments.id DESC").Offset(offset).Limit(pageSize).Scan(&items).Error
	if err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// GetContactableInboxes returns inboxes that can contact this customer
func (r *ContactExtensionRepository) GetContactableInboxes(accountID, contactID uint) ([]ContactableInboxItem, error) {
	var contact domain.Contact
	if err := r.db.Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var allInboxes []domain.Inbox
	if err := r.db.Where("account_id = ?", accountID).Order("id ASC").Find(&allInboxes).Error; err != nil {
		return nil, err
	}

	var existingContactInboxes []domain.ContactInbox
	if err := r.db.Preload("Inbox").Where("contact_id = ?", contactID).Find(&existingContactInboxes).Error; err != nil {
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
	var list []domain.ContactInbox
	err := r.db.Preload("Inbox").
		Joins("JOIN inboxes ON inboxes.id = contact_inboxes.inbox_id").
		Where("inboxes.account_id = ? AND contact_inboxes.contact_id = ?", accountID, contactID).
		Order("contact_inboxes.id ASC").
		Find(&list).Error
	return list, err
}

// CreateContactInbox links an inbox identity to a contact
func (r *ContactExtensionRepository) CreateContactInbox(accountID, contactID, inboxID uint, sourceID string) (*domain.ContactInbox, error) {
	var contact domain.Contact
	if err := r.db.Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		return nil, fmt.Errorf("contact not found: %w", err)
	}

	var inbox domain.Inbox
	if err := r.db.Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, fmt.Errorf("inbox not found: %w", err)
	}

	var existing domain.ContactInbox
	err := r.db.Where("contact_id = ? AND inbox_id = ?", contactID, inboxID).First(&existing).Error
	if err == nil {
		if sourceID != "" && existing.SourceID != sourceID {
			existing.SourceID = sourceID
			if err := r.db.Save(&existing).Error; err != nil {
				return nil, err
			}
		}
		_ = r.db.Preload("Inbox").First(&existing, existing.ID)
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

	if err := r.db.Create(&ci).Error; err != nil {
		return nil, err
	}

	_ = r.db.Preload("Inbox").First(&ci, ci.ID)
	return &ci, nil
}

// DeleteContactInbox removes a specific ContactInbox record
func (r *ContactExtensionRepository) DeleteContactInbox(accountID, contactID, contactInboxID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var ci domain.ContactInbox
		err := tx.Joins("JOIN inboxes ON inboxes.id = contact_inboxes.inbox_id").
			Where("inboxes.account_id = ? AND contact_inboxes.contact_id = ? AND contact_inboxes.id = ?", accountID, contactID, contactInboxID).
			First(&ci).Error
		if err != nil {
			return err
		}
		return tx.Delete(&ci).Error
	})
}

// DeleteContactInboxByInboxID removes a ContactInbox record by inbox_id
func (r *ContactExtensionRepository) DeleteContactInboxByInboxID(accountID, contactID, inboxID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var ci domain.ContactInbox
		err := tx.Joins("JOIN inboxes ON inboxes.id = contact_inboxes.inbox_id").
			Where("inboxes.account_id = ? AND contact_inboxes.contact_id = ? AND contact_inboxes.inbox_id = ?", accountID, contactID, inboxID).
			First(&ci).Error
		if err != nil {
			return err
		}
		return tx.Delete(&ci).Error
	})
}

// ListContactConversations lists all conversations belonging to a contact
func (r *ContactExtensionRepository) ListContactConversations(accountID, contactID uint, status string, inboxID *uint, page, pageSize int) ([]domain.Conversation, int64, error) {
	var conversations []domain.Conversation
	var total int64

	query := r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND contact_id = ?", accountID, contactID)

	if status != "" {
		query = query.Where("status = ?", status)
	}

	if inboxID != nil && *inboxID > 0 {
		query = query.Where("inbox_id = ?", *inboxID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize

	err := query.Preload("Inbox").
		Preload("Assignee").
		Preload("Labels").
		Preload("Team").
		Offset(offset).
		Limit(pageSize).
		Order("last_activity_at DESC, id DESC").
		Find(&conversations).Error

	return conversations, total, err
}

// GetContactStats gathers comprehensive interaction statistics for a contact
func (r *ContactExtensionRepository) GetContactStats(accountID, contactID uint) (*ContactStats, error) {
	var contact domain.Contact
	if err := r.db.Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	stats := &ContactStats{
		ContactID: contactID,
	}

	// 1. Conversations count & status breakdown
	_ = r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND contact_id = ?", accountID, contactID).
		Count(&stats.ConversationsCount).Error

	_ = r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND contact_id = ? AND status = ?", accountID, contactID, domain.ConversationStatusOpen).
		Count(&stats.OpenConversationsCount).Error

	_ = r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND contact_id = ? AND status = ?", accountID, contactID, domain.ConversationStatusResolved).
		Count(&stats.ResolvedConversationsCount).Error

	// 2. Messages count
	_ = r.db.Table("messages").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("conversations.account_id = ? AND conversations.contact_id = ?", accountID, contactID).
		Count(&stats.MessagesCount).Error

	// 3. Attachments count
	_ = r.db.Table("attachments").
		Joins("JOIN messages ON messages.id = attachments.message_id").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("conversations.account_id = ? AND conversations.contact_id = ?", accountID, contactID).
		Count(&stats.AttachmentsCount).Error

	// 4. Contact inboxes count
	_ = r.db.Table("contact_inboxes").
		Joins("JOIN inboxes ON inboxes.id = contact_inboxes.inbox_id").
		Where("inboxes.account_id = ? AND contact_inboxes.contact_id = ?", accountID, contactID).
		Count(&stats.ContactInboxesCount).Error

	// 5. First seen & last activity timestamps
	var earliestConv domain.Conversation
	if err := r.db.Where("account_id = ? AND contact_id = ?", accountID, contactID).
		Order("created_at ASC").First(&earliestConv).Error; err == nil {
		t := earliestConv.CreatedAt
		stats.FirstSeenAt = &t
	} else {
		t := contact.CreatedAt
		stats.FirstSeenAt = &t
	}

	var latestConv domain.Conversation
	if err := r.db.Where("account_id = ? AND contact_id = ?", accountID, contactID).
		Order("last_activity_at DESC").First(&latestConv).Error; err == nil && !latestConv.LastActivityAt.IsZero() {
		t := latestConv.LastActivityAt
		stats.LastActivityAt = &t
	} else if stats.FirstSeenAt != nil {
		stats.LastActivityAt = stats.FirstSeenAt
	}

	return stats, nil
}
