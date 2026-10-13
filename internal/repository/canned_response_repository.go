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
	if cr == nil || cr.AccountID == 0 {
		return gorm.ErrRecordNotFound
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := LockCannedResponseAccount(tx, cr.AccountID); err != nil {
			return err
		}
		if err := checkCannedShortcode(tx, cr.AccountID, 0, cr.ShortCode, true); err != nil {
			return err
		}
		cr.ObjectVersion = 0
		return tx.Create(cr).Error
	})
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

func (r *CannedResponseRepository) FindMatch(accountID uint, query string) (*domain.CannedResponse, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	var response domain.CannedResponse
	err := r.db.Where("account_id = ? AND (LOWER(short_code) = ? OR LOWER(content) LIKE ?)", accountID, query, "%"+query+"%").
		First(&response).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &response, nil
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
	if cr == nil || cr.AccountID == 0 || cr.ID == 0 {
		return gorm.ErrRecordNotFound
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := LockCannedResponseAccount(tx, cr.AccountID); err != nil {
			return err
		}
		var stored domain.CannedResponse
		if err := tx.Where("account_id = ? AND id = ?", cr.AccountID, cr.ID).First(&stored).Error; err != nil {
			return err
		}
		if stored.ObjectVersion != 0 {
			return ErrCannedResponseVersioned
		}
		if err := checkCannedShortcode(tx, cr.AccountID, cr.ID, cr.ShortCode, true); err != nil {
			return err
		}
		updatedAt := tx.NowFunc()
		result := tx.Model(&domain.CannedResponse{}).Where("account_id = ? AND id = ? AND object_version = 0", cr.AccountID, cr.ID).
			Updates(map[string]any{"short_code": cr.ShortCode, "content": cr.Content, "updated_at": updatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		cr.ObjectVersion, cr.UpdatedAt = 0, updatedAt
		return nil
	})
}

func (r *CannedResponseRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := LockCannedResponseAccount(tx, accountID); err != nil {
			return err
		}
		var stored domain.CannedResponse
		err := tx.Where("account_id = ? AND id = ?", accountID, id).First(&stored).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if stored.ObjectVersion != 0 {
			return ErrCannedResponseVersioned
		}
		return tx.Where("account_id = ? AND id = ? AND object_version = 0", accountID, id).Delete(&domain.CannedResponse{}).Error
	})
}
