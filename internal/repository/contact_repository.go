package repository

import (
	"errors"
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
