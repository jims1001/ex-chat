package repository

import (
	"errors"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type LabelRepository struct {
	db *gorm.DB
}

func NewLabelRepository(db *gorm.DB) *LabelRepository {
	return &LabelRepository{db: db}
}

func (r *LabelRepository) Create(label *domain.Label) error {
	return r.db.Create(label).Error
}

func (r *LabelRepository) FindByID(accountID, id uint) (*domain.Label, error) {
	var label domain.Label
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&label).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &label, nil
}

func (r *LabelRepository) List(accountID uint) ([]domain.Label, error) {
	var labels []domain.Label
	err := r.db.Where("account_id = ?", accountID).Find(&labels).Error
	return labels, err
}

func (r *LabelRepository) Update(label *domain.Label) error {
	return r.db.Save(label).Error
}

func (r *LabelRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		_ = tx.Where("label_id = ?", id).Delete(&domain.ConversationLabel{}).Error
		_ = tx.Where("label_id = ?", id).Delete(&domain.ContactLabel{}).Error
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Label{}).Error
	})
}

func (r *LabelRepository) AttachToConversation(convID, labelID uint) error {
	cl := domain.ConversationLabel{
		ConversationID: convID,
		LabelID:        labelID,
	}
	return r.db.Where(cl).FirstOrCreate(&cl).Error
}

func (r *LabelRepository) DetachFromConversation(convID, labelID uint) error {
	return r.db.Where("conversation_id = ? AND label_id = ?", convID, labelID).
		Delete(&domain.ConversationLabel{}).Error
}

func (r *LabelRepository) GetConversationLabels(convID uint) ([]domain.Label, error) {
	var labels []domain.Label
	err := r.db.Joins("JOIN conversation_labels ON conversation_labels.label_id = labels.id").
		Where("conversation_labels.conversation_id = ?", convID).
		Find(&labels).Error
	return labels, err
}

func (r *LabelRepository) AttachToContact(contactID, labelID uint) error {
	cl := domain.ContactLabel{
		ContactID: contactID,
		LabelID:   labelID,
	}
	return r.db.Where(cl).FirstOrCreate(&cl).Error
}

func (r *LabelRepository) DetachFromContact(contactID, labelID uint) error {
	return r.db.Where("contact_id = ? AND label_id = ?", contactID, labelID).
		Delete(&domain.ContactLabel{}).Error
}

func (r *LabelRepository) GetContactLabels(contactID uint) ([]domain.Label, error) {
	var labels []domain.Label
	err := r.db.Joins("JOIN contact_labels ON contact_labels.label_id = labels.id").
		Where("contact_labels.contact_id = ?", contactID).
		Find(&labels).Error
	return labels, err
}

func (r *LabelRepository) SetContactLabels(accountID, contactID uint, labelNamesOrIDs []string) ([]domain.Label, error) {
	var finalLabels []domain.Label

	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 1. Resolve each label name/ID
		for _, raw := range labelNamesOrIDs {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				continue
			}

			var label domain.Label
			var findErr error

			if id, parseErr := strconv.ParseUint(trimmed, 10, 64); parseErr == nil && id > 0 {
				findErr = tx.Where("account_id = ? AND id = ?", accountID, uint(id)).First(&label).Error
			}

			if label.ID == 0 {
				findErr = tx.Where("account_id = ? AND LOWER(title) = ?", accountID, strings.ToLower(trimmed)).First(&label).Error
			}

			if errors.Is(findErr, gorm.ErrRecordNotFound) || label.ID == 0 {
				label = domain.Label{
					AccountID: accountID,
					Title:     trimmed,
					Color:     "#1f93ff",
				}
				if err := tx.Create(&label).Error; err != nil {
					return err
				}
			}

			finalLabels = append(finalLabels, label)
		}

		// 2. Clear old contact labels
		if err := tx.Where("contact_id = ?", contactID).Delete(&domain.ContactLabel{}).Error; err != nil {
			return err
		}

		// 3. Insert new contact labels (deduplicated)
		seen := make(map[uint]bool)
		for _, l := range finalLabels {
			if seen[l.ID] {
				continue
			}
			seen[l.ID] = true
			cl := domain.ContactLabel{
				ContactID: contactID,
				LabelID:   l.ID,
			}
			if err := tx.Create(&cl).Error; err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return finalLabels, nil
}
