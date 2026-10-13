package repository

import (
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type EnterpriseRepository struct {
	db *gorm.DB
}

func NewEnterpriseRepository(db *gorm.DB) *EnterpriseRepository {
	return &EnterpriseRepository{db: db}
}

// ----------------- Account Features -----------------

func (r *EnterpriseRepository) GetAccountFeatures(accountID uint) (map[string]bool, error) {
	var features []domain.AccountFeature
	err := r.db.Where("account_id = ?", accountID).Find(&features).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool)
	for _, f := range features {
		result[f.FeatureName] = f.Enabled
	}
	return result, nil
}

func (r *EnterpriseRepository) SetAccountFeature(accountID uint, featureName string, enabled bool) error {
	// Explicit map values bypass the model's default:true on creates. The one
	// statement upsert also preserves false updates and resolves concurrent
	// creates without changing row identity or account/key scope.
	return r.db.Model(&domain.AccountFeature{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "account_id"}, {Name: "feature_name"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"}),
	}).Create(map[string]any{
		"account_id": accountID, "feature_name": featureName,
		"enabled": enabled, "updated_at": time.Now().UTC(),
	}).Error
}
