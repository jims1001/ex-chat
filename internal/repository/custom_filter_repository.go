package repository

import (
	"context"
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// ErrCustomFilterVersioned prevents legacy writers from bypassing SRH CAS and
// its atomic journal/outbox/receipt. It is returned only for the current owner.
var ErrCustomFilterVersioned = errors.New("custom filter requires versioned SRH command")

// CustomFilterRepository owns SRH saved-filter persistence.
type CustomFilterRepository struct{ db *gorm.DB }

func NewCustomFilterRepository(db *gorm.DB) *CustomFilterRepository {
	// SQL traces and provider errors can contain private saved criteria. Keep
	// this repository's logger silent; the REST boundary logs safe operation IDs.
	return &CustomFilterRepository{db: db.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})}
}

func (r *CustomFilterRepository) Create(ctx context.Context, filter *domain.CustomFilter) error {
	if filter == nil || filter.AccountID == 0 || filter.UserID == 0 {
		return gorm.ErrRecordNotFound
	}
	return r.db.WithContext(ctx).Create(filter).Error
}

// CreateLegacy binds identity and version at the owner boundary, not from input.
func (r *CustomFilterRepository) CreateLegacy(ctx context.Context, accountID, userID uint, filter *domain.CustomFilter) error {
	if filter == nil || accountID == 0 || userID == 0 {
		return gorm.ErrRecordNotFound
	}
	filter.ID, filter.AccountID, filter.UserID, filter.ObjectVersion = 0, accountID, userID, 0
	now := time.Now().UTC()
	filter.CreatedAt, filter.UpdatedAt = now, now
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(filter).Error
	})
}

// List includes owned legacy rows and owned runtime rows, never unowned rows.
func (r *CustomFilterRepository) List(ctx context.Context, accountID, userID uint, filterType string) ([]domain.CustomFilter, error) {
	filters := []domain.CustomFilter{}
	if accountID == 0 || userID == 0 {
		return filters, gorm.ErrRecordNotFound
	}
	query := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ?", accountID, userID)
	if filterType != "" {
		query = query.Where("filter_type = ?", filterType)
	}
	err := query.Order("id DESC").Find(&filters).Error
	return filters, err
}

func (r *CustomFilterRepository) Find(ctx context.Context, accountID, userID, id uint) (*domain.CustomFilter, error) {
	if accountID == 0 || userID == 0 || id == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var filter domain.CustomFilter
	if err := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).First(&filter).Error; err != nil {
		return nil, err
	}
	return &filter, nil
}

// CustomFilterPatch deliberately has no ownership or version fields.
type CustomFilterPatch struct{ Name, FilterType, Query *string }

func (r *CustomFilterRepository) UpdateLegacy(ctx context.Context, accountID, userID, id uint, patch CustomFilterPatch) (*domain.CustomFilter, error) {
	if accountID == 0 || userID == 0 || id == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var filter domain.CustomFilter
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope := tx.Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id)
		if err := scope.Clauses(clause.Locking{Strength: "UPDATE"}).First(&filter).Error; err != nil {
			return err
		}
		if filter.ObjectVersion != 0 {
			return ErrCustomFilterVersioned
		}
		changes := map[string]any{}
		if patch.Name != nil {
			changes["name"] = *patch.Name
		}
		if patch.FilterType != nil {
			changes["filter_type"] = *patch.FilterType
		}
		if patch.Query != nil {
			changes["query"] = *patch.Query
		}
		if len(changes) == 0 {
			return nil
		}
		changes["updated_at"] = time.Now().UTC()
		result := tx.Model(&domain.CustomFilter{}).Where("account_id = ? AND user_id = ? AND id = ? AND object_version = 0", accountID, userID, id).Updates(changes)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		// Reading the response is part of the transaction; failure rolls back.
		return tx.Where("account_id = ? AND user_id = ? AND id = ? AND object_version = 0", accountID, userID, id).First(&filter).Error
	})
	if err != nil {
		return nil, err
	}
	return &filter, nil
}

func (r *CustomFilterRepository) DeleteLegacy(ctx context.Context, accountID, userID, id uint) (bool, error) {
	if accountID == 0 || userID == 0 || id == 0 {
		return false, gorm.ErrRecordNotFound
	}
	deleted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var filter domain.CustomFilter
		err := tx.Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).Clauses(clause.Locking{Strength: "UPDATE"}).First(&filter).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if filter.ObjectVersion != 0 {
			return ErrCustomFilterVersioned
		}
		result := tx.Where("account_id = ? AND user_id = ? AND id = ? AND object_version = 0", accountID, userID, id).Delete(&domain.CustomFilter{})
		deleted = result.RowsAffected == 1
		return result.Error
	})
	return deleted && err == nil, err
}

// Runtime methods never include legacy shared (user_id=0) or unversioned rows.
func (r *CustomFilterRepository) ListPrivate(ctx context.Context, accountID, userID uint, filterType string) ([]domain.CustomFilter, error) {
	if accountID == 0 || userID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	query := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ? AND object_version > 0", accountID, userID)
	if filterType != "" {
		query = query.Where("filter_type = ?", filterType)
	}
	filters := []domain.CustomFilter{}
	err := query.Order("id DESC").Find(&filters).Error
	return filters, err
}

func (r *CustomFilterRepository) FindPrivate(ctx context.Context, accountID, userID, id uint, lock bool) (*domain.CustomFilter, error) {
	if accountID == 0 || userID == 0 || id == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	query := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ? AND id = ? AND object_version > 0", accountID, userID, id)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var filter domain.CustomFilter
	if err := query.First(&filter).Error; err != nil {
		return nil, err
	}
	return &filter, nil
}

func (r *CustomFilterRepository) SavePrivateCAS(ctx context.Context, filter *domain.CustomFilter, expectedVersion int) (bool, error) {
	if filter == nil || filter.AccountID == 0 || filter.UserID == 0 || filter.ID == 0 || expectedVersion <= 0 {
		return false, gorm.ErrRecordNotFound
	}
	result := r.db.WithContext(ctx).Model(&domain.CustomFilter{}).
		Where("account_id = ? AND user_id = ? AND id = ? AND object_version = ?", filter.AccountID, filter.UserID, filter.ID, expectedVersion).
		Updates(map[string]any{"name": filter.Name, "filter_type": filter.FilterType, "query": filter.Query, "object_version": filter.ObjectVersion, "updated_at": filter.UpdatedAt})
	return result.RowsAffected == 1, result.Error
}

func (r *CustomFilterRepository) DeletePrivateCAS(ctx context.Context, accountID, userID, id uint, expectedVersion int) (bool, error) {
	if accountID == 0 || userID == 0 || id == 0 || expectedVersion <= 0 {
		return false, gorm.ErrRecordNotFound
	}
	result := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ? AND id = ? AND object_version = ?", accountID, userID, id, expectedVersion).Delete(&domain.CustomFilter{})
	return result.RowsAffected == 1, result.Error
}
