package repository

import (
	"context"
	"encoding/json"
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

func (r *CompanyRepository) ListContactsByIDs(ctx context.Context, accountID uint, contactIDs []uint) ([]domain.Contact, error) {
	if len(contactIDs) == 0 {
		return []domain.Contact{}, nil
	}
	var contacts []domain.Contact
	err := r.db.WithContext(ctx).Preload("Company").Preload("Labels").
		Where("account_id = ? AND id IN ?", accountID, contactIDs).
		Find(&contacts).Error
	return contacts, err
}

func (r *CompanyRepository) ContactBelongsToCompany(ctx context.Context, accountID, companyID, contactID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.Contact{}).
		Where("account_id = ? AND company_id = ? AND id = ?", accountID, companyID, contactID).
		Count(&count).Error
	return count > 0, err
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
