package repository

import (
	"errors"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type LabelRepository struct {
	db            *gorm.DB
	conversations *ConversationRepository
}

func NewLabelRepository(db *gorm.DB) *LabelRepository {
	return NewLabelRepositoryWithConversationPort(db, &ConversationRepository{db: db})
}

func NewLabelRepositoryWithConversationPort(db *gorm.DB, conversations *ConversationRepository) *LabelRepository {
	return &LabelRepository{db: db, conversations: conversations}
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

func (r *LabelRepository) FindIDsByTitles(accountID uint, titles []any) ([]uint, error) {
	var ids []uint
	err := r.db.Model(&domain.Label{}).Where("account_id = ? AND title IN ?", accountID, titles).Pluck("id", &ids).Error
	return ids, err
}

func (r *LabelRepository) Exists(accountID, id uint) (bool, error) {
	var count int64
	err := r.db.Model(&domain.Label{}).Where("account_id = ? AND id = ?", accountID, id).Count(&count).Error
	return count == 1, err
}

func (r *LabelRepository) FindByTitle(accountID uint, title string) (*domain.Label, error) {
	var label domain.Label
	err := r.db.Where("account_id = ? AND title = ?", accountID, title).First(&label).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &label, err
}

func (r *LabelRepository) FindOrCreateByTitle(accountID uint, title string) (*domain.Label, error) {
	label := domain.Label{AccountID: accountID, Title: title}
	if err := r.db.Where("account_id = ? AND title = ?", accountID, title).FirstOrCreate(&label).Error; err != nil {
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
	label, err := r.FindByID(accountID, id)
	if err != nil {
		return err
	}
	if label == nil {
		return gorm.ErrRecordNotFound
	}
	if r.conversations == nil {
		return gorm.ErrInvalidData
	}
	if err := r.conversations.DeleteLabelReferences(accountID, id); err != nil {
		return err
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("label_id = ?", id).Delete(&domain.ContactLabel{}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Label{}).Error
	})
}

func (r *LabelRepository) AttachToConversation(accountID, convID, labelID uint) error {
	exists, err := r.Exists(accountID, labelID)
	if err != nil {
		return err
	}
	if !exists {
		return gorm.ErrRecordNotFound
	}
	if r.conversations == nil {
		return gorm.ErrInvalidData
	}
	return r.conversations.AttachLabelReference(accountID, convID, labelID)
}

func (r *LabelRepository) DetachFromConversation(accountID, convID, labelID uint) error {
	if r.conversations == nil {
		return gorm.ErrInvalidData
	}
	return r.conversations.DetachLabel(accountID, convID, labelID)
}

func (r *LabelRepository) GetConversationLabels(accountID, convID uint) ([]domain.Label, error) {
	if r.conversations == nil {
		return nil, gorm.ErrInvalidData
	}
	labelIDs, err := r.conversations.FindLabelIDs(accountID, convID)
	if err != nil || len(labelIDs) == 0 {
		return nil, err
	}
	var labels []domain.Label
	err = r.db.Where("account_id = ? AND id IN ?", accountID, labelIDs).Find(&labels).Error
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
