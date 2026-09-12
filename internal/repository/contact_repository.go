package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ContactRepository struct {
	db *gorm.DB
}

func NewContactRepository(db *gorm.DB) *ContactRepository {
	return &ContactRepository{db: db}
}

func (r *ContactRepository) Create(contact *domain.Contact) error {
	return r.db.Create(contact).Error
}

func (r *ContactRepository) FindByID(accountID, id uint) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Preload("Company").Preload("Labels").Where("account_id = ? AND id = ?", accountID, id).First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

func (r *ContactRepository) FindByEmail(accountID uint, email string) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Where("account_id = ? AND email = ?", accountID, email).First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

func (r *ContactRepository) FindByIdentifier(accountID uint, identifier string) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Where("account_id = ? AND identifier = ?", accountID, identifier).First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

func (r *ContactRepository) List(accountID uint, page, pageSize int, search string) ([]domain.Contact, int64, error) {
	return r.ListFiltered(accountID, page, pageSize, search, "", nil)
}

func (r *ContactRepository) ListFiltered(accountID uint, page, pageSize int, search, label string, companyID *uint) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.Model(&domain.Contact{}).Where("contacts.account_id = ?", accountID)
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(contacts.name) LIKE ? OR LOWER(contacts.email) LIKE ? OR contacts.phone_number LIKE ? OR contacts.identifier LIKE ?", s, s, s, s)
	}

	if companyID != nil && *companyID > 0 {
		query = query.Where("contacts.company_id = ?", *companyID)
	}

	if strings.TrimSpace(label) != "" {
		lbl := strings.TrimSpace(label)
		query = query.Joins("JOIN contact_labels ON contact_labels.contact_id = contacts.id").
			Joins("JOIN labels ON labels.id = contact_labels.label_id").
			Where("labels.account_id = ? AND (LOWER(labels.title) = ? OR labels.id = ?)", accountID, strings.ToLower(lbl), lbl).
			Distinct()
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").Offset(offset).Limit(pageSize).Order("contacts.id DESC").Find(&contacts).Error
	return contacts, total, err
}

func (r *ContactRepository) Update(contact *domain.Contact) error {
	return r.db.Save(contact).Error
}

func (r *ContactRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		_ = tx.Where("contact_id = ?", id).Delete(&domain.ContactLabel{}).Error
		_ = tx.Where("account_id = ? AND contact_id = ?", accountID, id).Delete(&domain.ContactNote{}).Error
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Contact{}).Error
	})
}

func (r *ContactRepository) CreateContactInbox(ci *domain.ContactInbox) error {
	return r.db.Create(ci).Error
}

func (r *ContactRepository) FindOrCreateContactInbox(contactID, inboxID uint, sourceID string) (*domain.ContactInbox, error) {
	var ci domain.ContactInbox
	err := r.db.Where("inbox_id = ? AND source_id = ?", inboxID, sourceID).First(&ci).Error
	if err == nil {
		return &ci, nil
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		ci = domain.ContactInbox{
			ContactID: contactID,
			InboxID:   inboxID,
			SourceID:  sourceID,
		}
		if err := r.db.Create(&ci).Error; err != nil {
			return nil, err
		}
		return &ci, nil
	}

	return nil, err
}

func (r *ContactRepository) FindContactBySourceID(inboxID uint, sourceID string) (*domain.Contact, error) {
	var ci domain.ContactInbox
	err := r.db.Preload("Contact").Where("inbox_id = ? AND source_id = ?", inboxID, sourceID).First(&ci).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return ci.Contact, nil
}

func (r *ContactRepository) MergeContacts(accountID, baseID, mergeeID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. Update ContactInboxes
		if err := tx.Model(&domain.ContactInbox{}).
			Where("contact_id = ?", mergeeID).
			Update("contact_id", baseID).Error; err != nil {
			return err
		}

		// 2. Update Conversations
		if err := tx.Model(&domain.Conversation{}).
			Where("account_id = ? AND contact_id = ?", accountID, mergeeID).
			Update("contact_id", baseID).Error; err != nil {
			return err
		}

		// 3. Migrate Contact Notes from mergee to base contact
		if err := tx.Model(&domain.ContactNote{}).
			Where("account_id = ? AND contact_id = ?", accountID, mergeeID).
			Update("contact_id", baseID).Error; err != nil {
			return err
		}

		// 4. Migrate Message sender ownership for incoming messages from mergee contact
		if err := tx.Model(&domain.Message{}).
			Where("account_id = ? AND LOWER(sender_type) = ? AND sender_id = ?", accountID, "contact", mergeeID).
			Update("sender_id", baseID).Error; err != nil {
			return err
		}

		// 5. Migrate ContactLabels from mergee to base contact
		var mergeeLabels []domain.ContactLabel
		if err := tx.Where("contact_id = ?", mergeeID).Find(&mergeeLabels).Error; err == nil {
			for _, ml := range mergeeLabels {
				var existing domain.ContactLabel
				if err := tx.Where("contact_id = ? AND label_id = ?", baseID, ml.LabelID).First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
					_ = tx.Create(&domain.ContactLabel{ContactID: baseID, LabelID: ml.LabelID}).Error
				}
			}
			_ = tx.Where("contact_id = ?", mergeeID).Delete(&domain.ContactLabel{}).Error
		}

		// 6. Delete mergee Contact
		if err := tx.Where("account_id = ? AND id = ?", accountID, mergeeID).
			Delete(&domain.Contact{}).Error; err != nil {
			return err
		}

		return nil
	})
}

func (r *ContactRepository) GetDB() *gorm.DB {
	return r.db
}

// ListActive retrieves contacts with ongoing/active (non-resolved) conversations
func (r *ContactRepository) ListActive(accountID uint, page, pageSize int, search string) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	subQuery := r.db.Model(&domain.Conversation{}).
		Select("DISTINCT contact_id").
		Where("account_id = ? AND status != ?", accountID, "resolved")

	query := r.db.Model(&domain.Contact{}).
		Where("account_id = ? AND id IN (?)", accountID, subQuery)

	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone_number LIKE ? OR identifier LIKE ?", s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").
		Offset(offset).Limit(pageSize).
		Order("id DESC").
		Find(&contacts).Error

	return contacts, total, err
}

// Search retrieves contacts matching query across name, email, phone, identifier, and custom_attributes
func (r *ContactRepository) Search(accountID uint, search string, page, pageSize int) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone_number LIKE ? OR identifier LIKE ? OR LOWER(custom_attributes) LIKE ?", s, s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").
		Offset(offset).Limit(pageSize).
		Order("id DESC").
		Find(&contacts).Error

	return contacts, total, err
}

// Filter applies structured rules to filter contacts dynamically
func (r *ContactRepository) Filter(accountID uint, filters []domain.FilterRule, page, pageSize int) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)

	for _, rule := range filters {
		op := strings.ToLower(rule.FilterOperator)
		key := strings.ToLower(rule.AttributeKey)

		switch key {
		case "name":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("LOWER(name) = ?", strings.ToLower(valStr))
				} else if op == "not_equal_to" {
					query = query.Where("LOWER(name) != ?", strings.ToLower(valStr))
				} else if op == "contains" {
					query = query.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(valStr)+"%")
				} else if op == "does_not_contain" {
					query = query.Where("LOWER(name) NOT LIKE ?", "%"+strings.ToLower(valStr)+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("name IS NOT NULL AND name != ''")
			} else if op == "is_not_present" {
				query = query.Where("name IS NULL OR name = ''")
			}

		case "email":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("LOWER(email) = ?", strings.ToLower(valStr))
				} else if op == "not_equal_to" {
					query = query.Where("LOWER(email) != ?", strings.ToLower(valStr))
				} else if op == "contains" {
					query = query.Where("LOWER(email) LIKE ?", "%"+strings.ToLower(valStr)+"%")
				} else if op == "does_not_contain" {
					query = query.Where("LOWER(email) NOT LIKE ?", "%"+strings.ToLower(valStr)+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("email IS NOT NULL AND email != ''")
			} else if op == "is_not_present" {
				query = query.Where("email IS NULL OR email = ''")
			}

		case "phone_number", "phone":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("phone_number = ?", valStr)
				} else if op == "not_equal_to" {
					query = query.Where("phone_number != ?", valStr)
				} else if op == "contains" {
					query = query.Where("phone_number LIKE ?", "%"+valStr+"%")
				} else if op == "does_not_contain" {
					query = query.Where("phone_number NOT LIKE ?", "%"+valStr+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("phone_number IS NOT NULL AND phone_number != ''")
			} else if op == "is_not_present" {
				query = query.Where("phone_number IS NULL OR phone_number = ''")
			}

		case "identifier":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("identifier = ?", valStr)
				} else if op == "not_equal_to" {
					query = query.Where("identifier != ?", valStr)
				} else if op == "contains" {
					query = query.Where("identifier LIKE ?", "%"+valStr+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("identifier IS NOT NULL AND identifier != ''")
			} else if op == "is_not_present" {
				query = query.Where("identifier IS NULL OR identifier = ''")
			}

		case "company_id":
			if op == "equal_to" {
				query = query.Where("company_id IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("company_id NOT IN (?)", rule.Values)
			} else if op == "is_present" {
				query = query.Where("company_id IS NOT NULL")
			} else if op == "is_not_present" {
				query = query.Where("company_id IS NULL")
			}

		case "labels":
			if op == "equal_to" || op == "contains" {
				query = query.Where("id IN (SELECT contact_id FROM contact_labels JOIN labels ON contact_labels.label_id = labels.id WHERE labels.title IN (?))", rule.Values)
			}

		default:
			// custom attribute or generic field
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if strings.HasPrefix(key, "custom_attributes.") {
					attrKey := strings.TrimPrefix(key, "custom_attributes.")
					query = query.Where("custom_attributes LIKE ? AND custom_attributes LIKE ?", "%"+attrKey+"%", "%"+valStr+"%")
				} else {
					query = query.Where("custom_attributes LIKE ?", "%"+valStr+"%")
				}
			}
		}
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").
		Offset(offset).Limit(pageSize).
		Order("id DESC").
		Find(&contacts).Error

	return contacts, total, err
}

// ImportContacts handles batch contact import with smart deduplication/upsert
func (r *ContactRepository) ImportContacts(accountID uint, contacts []domain.Contact) (int, int, error) {
	importedCount := 0
	updatedCount := 0

	err := r.db.Transaction(func(tx *gorm.DB) error {
		for i := range contacts {
			c := contacts[i]
			c.AccountID = accountID

			var existing domain.Contact
			found := false

			if c.Email != "" {
				if err := tx.Where("account_id = ? AND LOWER(email) = ?", accountID, strings.ToLower(c.Email)).First(&existing).Error; err == nil {
					found = true
				}
			}
			if !found && c.Identifier != "" {
				if err := tx.Where("account_id = ? AND identifier = ?", accountID, c.Identifier).First(&existing).Error; err == nil {
					found = true
				}
			}
			if !found && c.PhoneNumber != "" {
				if err := tx.Where("account_id = ? AND phone_number = ?", accountID, c.PhoneNumber).First(&existing).Error; err == nil {
					found = true
				}
			}

			if found {
				if c.Name != "" {
					existing.Name = c.Name
				}
				if c.PhoneNumber != "" {
					existing.PhoneNumber = c.PhoneNumber
				}
				if c.Identifier != "" {
					existing.Identifier = c.Identifier
				}
				if c.CustomAttributes != "" {
					existing.CustomAttributes = c.CustomAttributes
				}
				if c.CompanyID != nil {
					existing.CompanyID = c.CompanyID
				}
				if err := tx.Save(&existing).Error; err != nil {
					return err
				}
				updatedCount++
			} else {
				if err := tx.Create(&c).Error; err != nil {
					return err
				}
				importedCount++
			}
		}
		return nil
	})

	return importedCount, updatedCount, err
}

// DeleteAvatar removes the avatar URL for a contact
func (r *ContactRepository) DeleteAvatar(accountID, contactID uint) error {
	return r.db.Model(&domain.Contact{}).
		Where("account_id = ? AND id = ?", accountID, contactID).
		Update("avatar_url", "").Error
}

// DestroyCustomAttributes strips designated key(s) from a contact's custom attributes JSON
func (r *ContactRepository) DestroyCustomAttributes(accountID, contactID uint, keys []string) (*domain.Contact, error) {
	contact, err := r.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		return nil, err
	}

	attrs := make(map[string]any)
	if contact.CustomAttributes != "" {
		_ = json.Unmarshal([]byte(contact.CustomAttributes), &attrs)
	}

	for _, k := range keys {
		delete(attrs, k)
	}

	newBytes, _ := json.Marshal(attrs)
	contact.CustomAttributes = string(newBytes)
	contact.ObjectVersion++

	if err := r.db.Save(contact).Error; err != nil {
		return nil, err
	}

	return contact, nil
}
