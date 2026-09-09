package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type CustomAttributeRepository struct {
	db *gorm.DB
}

func NewCustomAttributeRepository(db *gorm.DB) *CustomAttributeRepository {
	return &CustomAttributeRepository{db: db}
}

func (r *CustomAttributeRepository) Create(def *domain.CustomAttributeDefinition) error {
	return r.db.Create(def).Error
}

func (r *CustomAttributeRepository) List(accountID uint, model string) ([]domain.CustomAttributeDefinition, error) {
	var list []domain.CustomAttributeDefinition
	query := r.db.Where("account_id = ?", accountID)
	if model != "" {
		query = query.Where("attribute_model = ?", model)
	}
	err := query.Order("id ASC").Find(&list).Error
	return list, err
}

func (r *CustomAttributeRepository) Get(accountID, id uint) (*domain.CustomAttributeDefinition, error) {
	var def domain.CustomAttributeDefinition
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&def).Error
	if err != nil {
		return nil, err
	}
	return &def, nil
}

func (r *CustomAttributeRepository) Update(def *domain.CustomAttributeDefinition) error {
	return r.db.Save(def).Error
}

func (r *CustomAttributeRepository) Delete(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).
		Delete(&domain.CustomAttributeDefinition{}).Error
}
