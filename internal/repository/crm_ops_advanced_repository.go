package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// CompanyRepository manages customer corporate organizations
type CompanyRepository struct {
	db *gorm.DB
}

func NewCompanyRepository(db *gorm.DB) *CompanyRepository {
	return &CompanyRepository{db: db}
}

func (r *CompanyRepository) Create(ctx context.Context, comp *domain.Company) error {
	return r.db.WithContext(ctx).Create(comp).Error
}

func (r *CompanyRepository) List(ctx context.Context, accountID uint) ([]domain.Company, error) {
	var companies []domain.Company
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Order("id DESC").Find(&companies).Error
	if err != nil {
		return nil, err
	}
	for i := range companies {
		var cnt int64
		_ = r.db.WithContext(ctx).Model(&domain.Contact{}).
			Where("account_id = ? AND company_id = ?", accountID, companies[i].ID).
			Count(&cnt).Error
		companies[i].ContactsCount = cnt
	}
	return companies, nil
}

func (r *CompanyRepository) ListPaginated(ctx context.Context, accountID uint, page, pageSize int, search string) ([]domain.Company, int64, error) {
	var companies []domain.Company
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Company{}).Where("account_id = ?", accountID)
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(domain) LIKE ? OR LOWER(industry) LIKE ?", s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Order("id DESC").Find(&companies).Error; err != nil {
		return nil, 0, err
	}

	for i := range companies {
		var cnt int64
		_ = r.db.WithContext(ctx).Model(&domain.Contact{}).
			Where("account_id = ? AND company_id = ?", accountID, companies[i].ID).
			Count(&cnt).Error
		companies[i].ContactsCount = cnt
	}

	return companies, total, nil
}

func (r *CompanyRepository) GetByID(ctx context.Context, accountID, id uint) (*domain.Company, error) {
	var comp domain.Company
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).First(&comp).Error
	if err != nil {
		return nil, err
	}
	var cnt int64
	_ = r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("account_id = ? AND company_id = ?", accountID, comp.ID).
		Count(&cnt).Error
	comp.ContactsCount = cnt
	return &comp, nil
}

func (r *CompanyRepository) Update(ctx context.Context, comp *domain.Company) error {
	return r.db.WithContext(ctx).Save(comp).Error
}

func (r *CompanyRepository) Delete(ctx context.Context, accountID, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Contact{}).
			Where("account_id = ? AND company_id = ?", accountID, id).
			Update("company_id", nil).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Company{}).Error
	})
}

func (r *CompanyRepository) ListCompanyContacts(ctx context.Context, accountID, companyID uint, page, pageSize int, search string) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("account_id = ? AND company_id = ?", accountID, companyID)

	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone_number LIKE ? OR identifier LIKE ?", s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").Offset(offset).Limit(pageSize).Order("id DESC").Find(&contacts).Error
	return contacts, total, err
}

func (r *CompanyRepository) AssociateContacts(ctx context.Context, accountID, companyID uint, contactIDs []uint) error {
	if len(contactIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("account_id = ? AND id IN ?", accountID, contactIDs).
		Update("company_id", companyID).Error
}

func (r *CompanyRepository) DisassociateContact(ctx context.Context, accountID, companyID, contactID uint) error {
	return r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("account_id = ? AND company_id = ? AND id = ?", accountID, companyID, contactID).
		Update("company_id", nil).Error
}

// Search finds companies by name, domain, industry or description matching the search term
func (r *CompanyRepository) Search(ctx context.Context, accountID uint, q string, page, pageSize int) ([]domain.Company, int64, error) {
	var companies []domain.Company
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Company{}).Where("account_id = ?", accountID)
	if q != "" {
		s := "%" + strings.ToLower(q) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(domain) LIKE ? OR LOWER(industry) LIKE ? OR LOWER(description) LIKE ?", s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Order("name ASC, id DESC").Find(&companies).Error; err != nil {
		return nil, 0, err
	}

	for i := range companies {
		var cnt int64
		_ = r.db.WithContext(ctx).Model(&domain.Contact{}).
			Where("account_id = ? AND company_id = ?", accountID, companies[i].ID).
			Count(&cnt).Error
		companies[i].ContactsCount = cnt
	}

	return companies, total, nil
}

// SearchContacts searches contacts for a company (defaulting to candidates not linked to this company, or scoped)
func (r *CompanyRepository) SearchContacts(ctx context.Context, accountID, companyID uint, q string, scope string, page, pageSize int) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Contact{}).Where("account_id = ?", accountID)
	if scope == "company" {
		query = query.Where("company_id = ?", companyID)
	} else if scope == "all" {
		// all contacts
	} else {
		// default: not linked to this company
		query = query.Where("company_id IS NULL OR company_id != ?", companyID)
	}

	if q != "" {
		s := "%" + strings.ToLower(q) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone_number LIKE ? OR identifier LIKE ?", s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Preload("Company").Preload("Labels").Offset(offset).Limit(pageSize).Order("name ASC, id ASC").Find(&contacts).Error; err != nil {
		return nil, 0, err
	}

	return contacts, total, nil
}

// DestroyCustomAttributes strips designated key(s) from a company's custom attributes JSON
func (r *CompanyRepository) DestroyCustomAttributes(ctx context.Context, accountID, companyID uint, keys []string) (*domain.Company, error) {
	comp, err := r.GetByID(ctx, accountID, companyID)
	if err != nil || comp == nil {
		return nil, err
	}

	attrs := make(map[string]any)
	if comp.CustomAttributes != "" {
		_ = json.Unmarshal([]byte(comp.CustomAttributes), &attrs)
	}

	for _, k := range keys {
		delete(attrs, k)
	}

	newBytes, _ := json.Marshal(attrs)
	comp.CustomAttributes = string(newBytes)

	if err := r.Update(ctx, comp); err != nil {
		return nil, err
	}

	return comp, nil
}

// DeleteAvatar removes the avatar URL for a company
func (r *CompanyRepository) DeleteAvatar(ctx context.Context, accountID, companyID uint) (*domain.Company, error) {
	comp, err := r.GetByID(ctx, accountID, companyID)
	if err != nil || comp == nil {
		return nil, err
	}

	comp.AvatarURL = ""
	if err := r.Update(ctx, comp); err != nil {
		return nil, err
	}

	return comp, nil
}

// SLARepository manages Service Level Agreements
type SLARepository struct {
	db *gorm.DB
}

func NewSLARepository(db *gorm.DB) *SLARepository {
	return &SLARepository{db: db}
}

func (r *SLARepository) Create(ctx context.Context, sla *domain.SLAPolicy) error {
	return r.db.WithContext(ctx).Create(sla).Error
}

func (r *SLARepository) List(ctx context.Context, accountID uint) ([]domain.SLAPolicy, error) {
	var list []domain.SLAPolicy
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Find(&list).Error
	return list, err
}

func (r *SLARepository) GetByID(ctx context.Context, accountID, id uint) (*domain.SLAPolicy, error) {
	var sla domain.SLAPolicy
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).First(&sla).Error
	if err != nil {
		return nil, err
	}
	return &sla, nil
}

func (r *SLARepository) Update(ctx context.Context, sla *domain.SLAPolicy) error {
	return r.db.WithContext(ctx).Save(sla).Error
}

func (r *SLARepository) Delete(ctx context.Context, accountID, id uint) error {
	return r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.SLAPolicy{}).Error
}

// AgentBotRepository manages automated webhook bot agents
type AgentBotRepository struct {
	db *gorm.DB
}

func NewAgentBotRepository(db *gorm.DB) *AgentBotRepository {
	return &AgentBotRepository{db: db}
}

func (r *AgentBotRepository) Create(ctx context.Context, bot *domain.AgentBot) error {
	return r.db.WithContext(ctx).Create(bot).Error
}

func (r *AgentBotRepository) List(ctx context.Context, accountID uint) ([]domain.AgentBot, error) {
	var list []domain.AgentBot
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Order("id ASC").Find(&list).Error
	return list, err
}

func (r *AgentBotRepository) GetByID(ctx context.Context, accountID, botID uint) (*domain.AgentBot, error) {
	var bot domain.AgentBot
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, botID).First(&bot).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &bot, nil
}

func (r *AgentBotRepository) Update(ctx context.Context, accountID, botID uint, updates map[string]any) (*domain.AgentBot, error) {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return nil, err
	}
	if bot == nil {
		return nil, gorm.ErrRecordNotFound
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.AgentBot{}).
			Where("account_id = ? AND id = ?", accountID, botID).
			Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	return r.GetByID(ctx, accountID, botID)
}

func (r *AgentBotRepository) Delete(ctx context.Context, accountID, botID uint) error {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return err
	}
	if bot == nil {
		return gorm.ErrRecordNotFound
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Clean up any inbox associations
		if err := tx.Where("account_id = ? AND agent_bot_id = ?", accountID, botID).
			Delete(&domain.AgentBotInbox{}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ? AND id = ?", accountID, botID).
			Delete(&domain.AgentBot{}).Error
	})
}

func (r *AgentBotRepository) DeleteAvatar(ctx context.Context, accountID, botID uint) error {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return err
	}
	if bot == nil {
		return gorm.ErrRecordNotFound
	}

	return r.db.WithContext(ctx).Model(&domain.AgentBot{}).
		Where("account_id = ? AND id = ?", accountID, botID).
		Update("avatar_url", "").Error
}

func (r *AgentBotRepository) GetInboxes(ctx context.Context, accountID, botID uint) ([]domain.Inbox, error) {
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return nil, err
	}
	if bot == nil {
		return nil, gorm.ErrRecordNotFound
	}

	var inboxes []domain.Inbox
	err = r.db.WithContext(ctx).
		Joins("JOIN agent_bot_inboxes ON agent_bot_inboxes.inbox_id = inboxes.id").
		Where("agent_bot_inboxes.account_id = ? AND agent_bot_inboxes.agent_bot_id = ?", accountID, botID).
		Find(&inboxes).Error
	return inboxes, err
}

func (r *AgentBotRepository) ConnectInbox(ctx context.Context, accountID, botID, inboxID uint) error {
	// Verify bot belongs to account
	bot, err := r.GetByID(ctx, accountID, botID)
	if err != nil {
		return err
	}
	if bot == nil {
		return gorm.ErrRecordNotFound
	}

	// Verify inbox belongs to account
	var inbox domain.Inbox
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// An inbox can only have one agent bot at a time
		if err := tx.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).
			Delete(&domain.AgentBotInbox{}).Error; err != nil {
			return err
		}
		binding := domain.AgentBotInbox{
			AccountID:  accountID,
			AgentBotID: botID,
			InboxID:    inboxID,
		}
		return tx.Create(&binding).Error
	})
}

func (r *AgentBotRepository) DisconnectInbox(ctx context.Context, accountID, botID, inboxID uint) error {
	res := r.db.WithContext(ctx).
		Where("account_id = ? AND agent_bot_id = ? AND inbox_id = ?", accountID, botID, inboxID).
		Delete(&domain.AgentBotInbox{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// AttachmentRepository manages message attachments
type AttachmentRepository struct {
	db *gorm.DB
}

func NewAttachmentRepository(db *gorm.DB) *AttachmentRepository {
	return &AttachmentRepository{db: db}
}

func (r *AttachmentRepository) Create(ctx context.Context, att *domain.Attachment) error {
	return r.db.WithContext(ctx).Create(att).Error
}

func (r *AttachmentRepository) ListByMessage(ctx context.Context, messageID uint) ([]domain.Attachment, error) {
	var list []domain.Attachment
	err := r.db.WithContext(ctx).Where("message_id = ?", messageID).Find(&list).Error
	return list, err
}
