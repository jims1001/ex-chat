package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// CustomFilterRepository owns SRH saved-filter persistence.
type CustomFilterRepository struct {
	db *gorm.DB
}

func NewCustomFilterRepository(db *gorm.DB) *CustomFilterRepository {
	return &CustomFilterRepository{db: db}
}

func (r *CustomFilterRepository) Create(ctx context.Context, filter *domain.CustomFilter) error {
	return r.db.WithContext(ctx).Create(filter).Error
}

func (r *CustomFilterRepository) List(ctx context.Context, accountID, userID uint, filterType string) ([]domain.CustomFilter, error) {
	query := r.db.WithContext(ctx).Where("account_id = ?", accountID)
	if userID > 0 {
		query = query.Where("user_id = ? OR user_id = 0", userID)
	}
	if filterType != "" {
		query = query.Where("filter_type = ?", filterType)
	}
	var filters []domain.CustomFilter
	err := query.Order("id DESC").Find(&filters).Error
	return filters, err
}

func (r *CustomFilterRepository) Find(ctx context.Context, accountID, id uint) (*domain.CustomFilter, error) {
	var filter domain.CustomFilter
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).First(&filter).Error; err != nil {
		return nil, err
	}
	return &filter, nil
}

func (r *CustomFilterRepository) Save(ctx context.Context, accountID uint, filter *domain.CustomFilter) error {
	result := r.db.WithContext(ctx).Model(&domain.CustomFilter{}).
		Where("account_id = ? AND id = ?", accountID, filter.ID).
		Updates(map[string]any{
			"name":        filter.Name,
			"filter_type": filter.FilterType,
			"query":       filter.Query,
			"updated_at":  filter.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *CustomFilterRepository) Delete(ctx context.Context, accountID, id uint) (bool, error) {
	result := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.CustomFilter{})
	return result.RowsAffected > 0, result.Error
}
