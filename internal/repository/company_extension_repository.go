package repository

import (
	"errors"
	"fmt"
	"sort"
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
	db            *gorm.DB
	users         *UserRepository
	conversations *ConversationRepository
	messages      *MessageRepository
}

// NewCompanyExtensionRepository creates a new repository instance
func NewCompanyExtensionRepository(db *gorm.DB) *CompanyExtensionRepository {
	return NewCompanyExtensionRepositoryWithOwnerPorts(db, NewUserRepository(db), NewConversationRepository(db), NewMessageRepository(db))
}

func NewCompanyExtensionRepositoryWithOwnerPorts(db *gorm.DB, users *UserRepository, conversations *ConversationRepository, messages *MessageRepository) *CompanyExtensionRepository {
	return &CompanyExtensionRepository{db: db, users: users, conversations: conversations, messages: messages}
}

func (r *CompanyExtensionRepository) companyContactIDs(accountID, companyID uint) ([]uint, error) {
	var ids []uint
	err := r.db.Model(&domain.Contact{}).Where("account_id = ? AND company_id = ?", accountID, companyID).Pluck("id", &ids).Error
	return ids, err
}

func (r *CompanyExtensionRepository) hydrateNoteUsers(items []UnifiedCompanyNoteItem) error {
	if len(items) == 0 {
		return nil
	}
	if r.users == nil {
		return gorm.ErrInvalidData
	}
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.UserID)
	}
	users, err := r.users.FindByIDs(ids)
	if err != nil {
		return err
	}
	byID := make(map[uint]*domain.User, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
	}
	for i := range items {
		items[i].User = byID[items[i].UserID]
	}
	return nil
}

func (r *CompanyExtensionRepository) hydrateCompanyNote(note *domain.CompanyNote) error {
	if note == nil {
		return nil
	}
	if r.users == nil {
		return gorm.ErrInvalidData
	}
	user, err := r.users.FindByID(note.UserID)
	if err != nil {
		return err
	}
	note.User = user
	return nil
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

	if r.conversations == nil {
		return nil, 0, gorm.ErrInvalidData
	}
	contactIDs, err := r.companyContactIDs(accountID, companyID)
	if err != nil {
		return nil, 0, err
	}
	return r.conversations.ListByContactIDs(accountID, contactIDs, status, inboxID, contactID, page, pageSize)
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
		if err := r.db.Where("account_id = ? AND company_id = ?", accountID, companyID).
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

		for _, row := range rows {
			cid := row.ContactID
			cName := row.ContactName
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
			})
		}
	}
	if err := r.hydrateNoteUsers(items); err != nil {
		return nil, 0, err
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

	if err := r.db.Omit("User", "Company").Create(note).Error; err != nil {
		return err
	}

	return r.hydrateCompanyNote(note)
}

// GetCompanyNote retrieves a single direct company note
func (r *CompanyExtensionRepository) GetCompanyNote(accountID, companyID, noteID uint) (*domain.CompanyNote, error) {
	var note domain.CompanyNote
	err := r.db.Where("account_id = ? AND company_id = ? AND id = ?", accountID, companyID, noteID).
		First(&note).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := r.hydrateCompanyNote(&note); err != nil {
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

	if err := r.db.Omit("User", "Company").Save(note).Error; err != nil {
		return nil, err
	}

	if err := r.hydrateCompanyNote(note); err != nil {
		return nil, err
	}
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

	if r.conversations == nil || r.messages == nil {
		return nil, gorm.ErrInvalidData
	}
	contactIDs, err := r.companyContactIDs(accountID, companyID)
	if err != nil {
		return nil, err
	}
	convStats, err := r.conversations.GetStatsByContactIDs(accountID, contactIDs)
	if err != nil {
		return nil, err
	}
	stats.ConversationsCount, stats.OpenConversationsCount, stats.ResolvedConversationsCount = convStats.Total, convStats.Open, convStats.Resolved
	stats.FirstInteractionAt, stats.LastActivityAt = convStats.FirstInteractionAt, convStats.LastActivityAt
	stats.MessagesCount, stats.AttachmentsCount, err = r.messages.CountActivityByConversationIDs(accountID, convStats.ConversationIDs)
	if err != nil {
		return nil, err
	}

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

	if stats.FirstInteractionAt == nil {
		t := company.CreatedAt
		stats.FirstInteractionAt = &t
	}
	if stats.LastActivityAt == nil && stats.FirstInteractionAt != nil {
		stats.LastActivityAt = stats.FirstInteractionAt
	}

	return stats, nil
}

// ListCompanyAttachments retrieves all attachments belonging to any contact in the company
func (r *CompanyExtensionRepository) ListCompanyAttachments(accountID, companyID uint, filter CompanyAttachmentFilter) ([]ContactAttachmentItem, int64, error) {
	if r.conversations == nil || r.messages == nil {
		return nil, 0, gorm.ErrInvalidData
	}
	contactIDs, err := r.companyContactIDs(accountID, companyID)
	if err != nil {
		return nil, 0, err
	}
	if filter.ContactID != nil {
		belongs := false
		for _, id := range contactIDs {
			if id == *filter.ContactID {
				belongs = true
				break
			}
		}
		if !belongs {
			return []ContactAttachmentItem{}, 0, nil
		}
		contactIDs = []uint{*filter.ContactID}
	}
	conversationIDs, err := r.conversations.FindIDsByContactIDs(accountID, contactIDs)
	if err != nil {
		return nil, 0, err
	}
	return r.messages.ListAttachmentsByConversationIDs(accountID, conversationIDs, filter)
}
