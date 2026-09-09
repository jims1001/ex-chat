package repository

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// UnifiedCompanyNoteItem represents a company-level or subordinate contact-level internal note
type UnifiedCompanyNoteItem struct {
	ID          uint         `json:"id"`
	NoteType    string       `json:"note_type"` // "company" or "contact"
	CompanyID   uint         `json:"company_id"`
	ContactID   *uint        `json:"contact_id,omitempty"`
	ContactName string       `json:"contact_name,omitempty"`
	UserID      uint         `json:"user_id"`
	Content     string       `json:"content"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	User        *domain.User `json:"user,omitempty"`
}

// CompanyNotesSummary aggregates metrics about company and employee notes
type CompanyNotesSummary struct {
	CompanyID         uint       `json:"company_id"`
	TotalNotes        int64      `json:"total_notes"`
	CompanyNotesCount int64      `json:"company_notes_count"`
	ContactNotesCount int64      `json:"contact_notes_count"`
	ContactsWithNotes int64      `json:"contacts_with_notes"`
	LastNoteAt        *time.Time `json:"last_note_at,omitempty"`
}

// CompanyStats represents full-spectrum operational metrics for a company
type CompanyStats struct {
	CompanyID                  uint       `json:"company_id"`
	ContactsCount              int64      `json:"contacts_count"`
	ConversationsCount         int64      `json:"conversations_count"`
	OpenConversationsCount     int64      `json:"open_conversations_count"`
	ResolvedConversationsCount int64      `json:"resolved_conversations_count"`
	MessagesCount              int64      `json:"messages_count"`
	AttachmentsCount           int64      `json:"attachments_count"`
	NotesCount                 int64      `json:"notes_count"`
	FirstInteractionAt         *time.Time `json:"first_interaction_at,omitempty"`
	LastActivityAt             *time.Time `json:"last_activity_at,omitempty"`
}

// CompanyAttachmentFilter defines query criteria for company-wide attachments
type CompanyAttachmentFilter struct {
	FileType       string
	ContactID      *uint
	ConversationID *uint
	SenderType     string
	Search         string
	Page           int
	PageSize       int
}

// CompanyExtensionRepository handles extended CRM operations for companies
type CompanyExtensionRepository struct {
	db *gorm.DB
}

// NewCompanyExtensionRepository creates a new repository instance
func NewCompanyExtensionRepository(db *gorm.DB) *CompanyExtensionRepository {
	return &CompanyExtensionRepository{db: db}
}

// ListCompanyConversations retrieves all conversations across contacts associated with a company
func (r *CompanyExtensionRepository) ListCompanyConversations(accountID, companyID uint, status string, inboxID, contactID *uint, page, pageSize int) ([]domain.Conversation, int64, error) {
	var company domain.Company
	if err := r.db.Where("account_id = ? AND id = ?", accountID, companyID).First(&company).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	var conversations []domain.Conversation
	var total int64

	baseQuery := r.db.Model(&domain.Conversation{}).
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID)

	if status != "" {
		baseQuery = baseQuery.Where("conversations.status = ?", status)
	}
	if inboxID != nil && *inboxID > 0 {
		baseQuery = baseQuery.Where("conversations.inbox_id = ?", *inboxID)
	}
	if contactID != nil && *contactID > 0 {
		baseQuery = baseQuery.Where("conversations.contact_id = ?", *contactID)
	}

	if err := baseQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize

	err := baseQuery.Preload("Contact").
		Preload("Inbox").
		Preload("Assignee").
		Preload("Labels").
		Preload("Team").
		Offset(offset).
		Limit(pageSize).
		Order("conversations.last_activity_at DESC, conversations.id DESC").
		Find(&conversations).Error

	return conversations, total, err
}

// ListUnifiedNotes gathers both direct company notes and subordinated contact notes into a single timeline
func (r *CompanyExtensionRepository) ListUnifiedNotes(accountID, companyID uint, scope string, page, pageSize int) ([]UnifiedCompanyNoteItem, int64, error) {
	var company domain.Company
	if err := r.db.Where("account_id = ? AND id = ?", accountID, companyID).First(&company).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	var items []UnifiedCompanyNoteItem

	fetchCompanyNotes := scope != "contacts"
	fetchContactNotes := scope != "company"

	if fetchCompanyNotes {
		var cNotes []domain.CompanyNote
		if err := r.db.Preload("User").
			Where("account_id = ? AND company_id = ?", accountID, companyID).
			Find(&cNotes).Error; err != nil {
			return nil, 0, err
		}
		for _, cn := range cNotes {
			items = append(items, UnifiedCompanyNoteItem{
				ID:        cn.ID,
				NoteType:  "company",
				CompanyID: companyID,
				UserID:    cn.UserID,
				Content:   cn.Content,
				CreatedAt: cn.CreatedAt,
				UpdatedAt: cn.UpdatedAt,
				User:      cn.User,
			})
		}
	}

	if fetchContactNotes {
		type contactNoteRow struct {
			domain.ContactNote
			ContactName string `gorm:"column:contact_name"`
		}
		var rows []contactNoteRow
		err := r.db.Table("contact_notes").
			Select("contact_notes.*, contacts.name AS contact_name").
			Joins("JOIN contacts ON contacts.id = contact_notes.contact_id").
			Where("contact_notes.account_id = ? AND contacts.company_id = ?", accountID, companyID).
			Find(&rows).Error
		if err != nil {
			return nil, 0, err
		}

		userIDs := make([]uint, 0, len(rows))
		for _, row := range rows {
			userIDs = append(userIDs, row.UserID)
		}
		userMap := make(map[uint]domain.User)
		if len(userIDs) > 0 {
			var users []domain.User
			if err := r.db.Where("id IN ?", userIDs).Find(&users).Error; err == nil {
				for _, u := range users {
					userMap[u.ID] = u
				}
			}
		}

		for _, row := range rows {
			cid := row.ContactID
			cName := row.ContactName
			var userPtr *domain.User
			if u, ok := userMap[row.UserID]; ok {
				userCopy := u
				userPtr = &userCopy
			}
			items = append(items, UnifiedCompanyNoteItem{
				ID:          row.ID,
				NoteType:    "contact",
				CompanyID:   companyID,
				ContactID:   &cid,
				ContactName: cName,
				UserID:      row.UserID,
				Content:     row.Content,
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
				User:        userPtr,
			})
		}
	}

	// Sort chronologically descending
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	total := int64(len(items))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize
	if offset >= len(items) {
		return []UnifiedCompanyNoteItem{}, total, nil
	}

	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}

	return items[offset:end], total, nil
}

// GetCompanyNotesSummary calculates notes distribution and timestamps
func (r *CompanyExtensionRepository) GetCompanyNotesSummary(accountID, companyID uint) (*CompanyNotesSummary, error) {
	var company domain.Company
	if err := r.db.Where("account_id = ? AND id = ?", accountID, companyID).First(&company).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	summary := &CompanyNotesSummary{
		CompanyID: companyID,
	}

	_ = r.db.Model(&domain.CompanyNote{}).
		Where("account_id = ? AND company_id = ?", accountID, companyID).
		Count(&summary.CompanyNotesCount).Error

	_ = r.db.Table("contact_notes").
		Joins("JOIN contacts ON contacts.id = contact_notes.contact_id").
		Where("contact_notes.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Count(&summary.ContactNotesCount).Error

	summary.TotalNotes = summary.CompanyNotesCount + summary.ContactNotesCount

	_ = r.db.Table("contact_notes").
		Joins("JOIN contacts ON contacts.id = contact_notes.contact_id").
		Where("contact_notes.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Distinct("contact_notes.contact_id").
		Count(&summary.ContactsWithNotes).Error

	var latestCN domain.CompanyNote
	var latestContactNote domain.ContactNote

	_ = r.db.Where("account_id = ? AND company_id = ?", accountID, companyID).
		Order("created_at DESC").First(&latestCN).Error

	_ = r.db.Table("contact_notes").
		Joins("JOIN contacts ON contacts.id = contact_notes.contact_id").
		Where("contact_notes.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Order("contact_notes.created_at DESC").First(&latestContactNote).Error

	switch {
	case !latestCN.CreatedAt.IsZero() && !latestContactNote.CreatedAt.IsZero():
		if latestCN.CreatedAt.After(latestContactNote.CreatedAt) {
			t := latestCN.CreatedAt
			summary.LastNoteAt = &t
		} else {
			t := latestContactNote.CreatedAt
			summary.LastNoteAt = &t
		}
	case !latestCN.CreatedAt.IsZero():
		t := latestCN.CreatedAt
		summary.LastNoteAt = &t
	case !latestContactNote.CreatedAt.IsZero():
		t := latestContactNote.CreatedAt
		summary.LastNoteAt = &t
	}

	return summary, nil
}

// CreateCompanyNote writes a direct company note
func (r *CompanyExtensionRepository) CreateCompanyNote(note *domain.CompanyNote) error {
	var company domain.Company
	if err := r.db.Where("account_id = ? AND id = ?", note.AccountID, note.CompanyID).First(&company).Error; err != nil {
		return fmt.Errorf("company not found: %w", err)
	}

	if err := r.db.Create(note).Error; err != nil {
		return err
	}

	_ = r.db.Preload("User").First(note, note.ID)
	return nil
}

// GetCompanyNote retrieves a single direct company note
func (r *CompanyExtensionRepository) GetCompanyNote(accountID, companyID, noteID uint) (*domain.CompanyNote, error) {
	var note domain.CompanyNote
	err := r.db.Preload("User").
		Where("account_id = ? AND company_id = ? AND id = ?", accountID, companyID, noteID).
		First(&note).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &note, nil
}

// UpdateCompanyNote modifies a company note's content
func (r *CompanyExtensionRepository) UpdateCompanyNote(accountID, companyID, noteID uint, content string) (*domain.CompanyNote, error) {
	note, err := r.GetCompanyNote(accountID, companyID, noteID)
	if err != nil || note == nil {
		return nil, err
	}

	note.Content = content
	note.UpdatedAt = time.Now().UTC()

	if err := r.db.Save(note).Error; err != nil {
		return nil, err
	}

	_ = r.db.Preload("User").First(note, note.ID)
	return note, nil
}

// DeleteCompanyNote removes a direct company note
func (r *CompanyExtensionRepository) DeleteCompanyNote(accountID, companyID, noteID uint) error {
	res := r.db.Where("account_id = ? AND company_id = ? AND id = ?", accountID, companyID, noteID).
		Delete(&domain.CompanyNote{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// GetCompanyStats aggregates all organizational telemetry
func (r *CompanyExtensionRepository) GetCompanyStats(accountID, companyID uint) (*CompanyStats, error) {
	var company domain.Company
	if err := r.db.Where("account_id = ? AND id = ?", accountID, companyID).First(&company).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	stats := &CompanyStats{
		CompanyID: companyID,
	}

	// 1. Contacts count
	_ = r.db.Model(&domain.Contact{}).
		Where("account_id = ? AND company_id = ?", accountID, companyID).
		Count(&stats.ContactsCount).Error

	// 2. Conversations count & breakdown
	_ = r.db.Model(&domain.Conversation{}).
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Count(&stats.ConversationsCount).Error

	_ = r.db.Model(&domain.Conversation{}).
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ? AND conversations.status = ?", accountID, companyID, domain.ConversationStatusOpen).
		Count(&stats.OpenConversationsCount).Error

	_ = r.db.Model(&domain.Conversation{}).
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ? AND conversations.status = ?", accountID, companyID, domain.ConversationStatusResolved).
		Count(&stats.ResolvedConversationsCount).Error

	// 3. Messages count
	_ = r.db.Table("messages").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Count(&stats.MessagesCount).Error

	// 4. Attachments count
	_ = r.db.Table("attachments").
		Joins("JOIN messages ON messages.id = attachments.message_id").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Count(&stats.AttachmentsCount).Error

	// 5. Notes count (Company + Contact notes)
	var cnCount, ctnCount int64
	_ = r.db.Model(&domain.CompanyNote{}).
		Where("account_id = ? AND company_id = ?", accountID, companyID).
		Count(&cnCount).Error
	_ = r.db.Table("contact_notes").
		Joins("JOIN contacts ON contacts.id = contact_notes.contact_id").
		Where("contact_notes.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Count(&ctnCount).Error
	stats.NotesCount = cnCount + ctnCount

	// 6. First interaction & last activity
	var earliestConv domain.Conversation
	if err := r.db.Model(&domain.Conversation{}).
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Order("conversations.created_at ASC").First(&earliestConv).Error; err == nil {
		t := earliestConv.CreatedAt
		stats.FirstInteractionAt = &t
	} else {
		t := company.CreatedAt
		stats.FirstInteractionAt = &t
	}

	var latestConv domain.Conversation
	if err := r.db.Model(&domain.Conversation{}).
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID).
		Order("conversations.last_activity_at DESC").First(&latestConv).Error; err == nil && !latestConv.LastActivityAt.IsZero() {
		t := latestConv.LastActivityAt
		stats.LastActivityAt = &t
	} else if stats.FirstInteractionAt != nil {
		stats.LastActivityAt = stats.FirstInteractionAt
	}

	return stats, nil
}

// ListCompanyAttachments retrieves all attachments belonging to any contact in the company
func (r *CompanyExtensionRepository) ListCompanyAttachments(accountID, companyID uint, filter CompanyAttachmentFilter) ([]ContactAttachmentItem, int64, error) {
	var items []ContactAttachmentItem
	var total int64

	baseQuery := r.db.Table("attachments").
		Select("attachments.id, attachments.account_id, attachments.message_id, messages.conversation_id, attachments.file_type, attachments.data_url, attachments.file_size, messages.sender_type, messages.sender_id, messages.content AS message_content, attachments.created_at").
		Joins("JOIN messages ON messages.id = attachments.message_id").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID)

	if filter.FileType != "" {
		ft := "%" + strings.ToLower(filter.FileType) + "%"
		baseQuery = baseQuery.Where("LOWER(attachments.file_type) LIKE ?", ft)
	}
	if filter.ContactID != nil && *filter.ContactID > 0 {
		baseQuery = baseQuery.Where("contacts.id = ?", *filter.ContactID)
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

	countQuery := r.db.Table("attachments").
		Joins("JOIN messages ON messages.id = attachments.message_id").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Joins("JOIN contacts ON contacts.id = conversations.contact_id").
		Where("conversations.account_id = ? AND contacts.company_id = ?", accountID, companyID)

	if filter.FileType != "" {
		ft := "%" + strings.ToLower(filter.FileType) + "%"
		countQuery = countQuery.Where("LOWER(attachments.file_type) LIKE ?", ft)
	}
	if filter.ContactID != nil && *filter.ContactID > 0 {
		countQuery = countQuery.Where("contacts.id = ?", *filter.ContactID)
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
