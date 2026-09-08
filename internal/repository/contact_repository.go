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
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&contact).Error
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
	var contacts []domain.Contact
	var total int64

	query := r.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone_number LIKE ? OR identifier LIKE ?", s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("id DESC").Find(&contacts).Error
	return contacts, total, err
}

func (r *ContactRepository) Update(contact *domain.Contact) error {
	return r.db.Save(contact).Error
}

func (r *ContactRepository) Delete(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Contact{}).Error
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
		// Update ContactInboxes
		if err := tx.Model(&domain.ContactInbox{}).
			Where("contact_id = ?", mergeeID).
			Update("contact_id", baseID).Error; err != nil {
			return err
		}

		// Update Conversations
		if err := tx.Model(&domain.Conversation{}).
			Where("account_id = ? AND contact_id = ?", accountID, mergeeID).
			Update("contact_id", baseID).Error; err != nil {
			return err
		}

		// Delete mergee Contact
		if err := tx.Where("account_id = ? AND id = ?", accountID, mergeeID).
			Delete(&domain.Contact{}).Error; err != nil {
			return err
		}

		return nil
	})
}
