package repository

import (
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type CannedResponseRepository struct {
	db *gorm.DB
}

func NewCannedResponseRepository(db *gorm.DB) *CannedResponseRepository {
	return &CannedResponseRepository{db: db}
}

func (r *CannedResponseRepository) Create(cr *domain.CannedResponse) error {
	return r.db.Create(cr).Error
}

func (r *CannedResponseRepository) FindByID(accountID, id uint) (*domain.CannedResponse, error) {
	var cr domain.CannedResponse
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&cr).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cr, nil
}

func (r *CannedResponseRepository) List(accountID uint, search string) ([]domain.CannedResponse, error) {
	var list []domain.CannedResponse
	query := r.db.Where("account_id = ?", accountID)
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(short_code) LIKE ? OR LOWER(content) LIKE ?", s, s)
	}
	err := query.Order("id DESC").Find(&list).Error
	return list, err
}

func (r *CannedResponseRepository) Update(cr *domain.CannedResponse) error {
	return r.db.Save(cr).Error
}

func (r *CannedResponseRepository) Delete(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.CannedResponse{}).Error
}
