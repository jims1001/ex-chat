package repository

import (
	"context"
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

// CampaignRepository manages outbound campaigns
type CampaignRepository struct {
	db *gorm.DB
}

func NewCampaignRepository(db *gorm.DB) *CampaignRepository {
	return &CampaignRepository{db: db}
}

func (r *CampaignRepository) Create(ctx context.Context, camp *domain.Campaign) error {
	return r.db.WithContext(ctx).Create(camp).Error
}

func (r *CampaignRepository) List(ctx context.Context, accountID uint) ([]domain.Campaign, error) {
	var list []domain.Campaign
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Find(&list).Error
	return list, err
}

func (r *CampaignRepository) GetByID(ctx context.Context, accountID, id uint) (*domain.Campaign, error) {
	var camp domain.Campaign
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).First(&camp).Error
	if err != nil {
		return nil, err
	}
	return &camp, nil
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
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Find(&list).Error
	return list, err
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
