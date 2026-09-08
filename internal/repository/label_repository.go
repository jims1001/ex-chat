package repository

import (
	"errors"

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
