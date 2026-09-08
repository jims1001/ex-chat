package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type EnterpriseRepository struct {
	db *gorm.DB
}

func NewEnterpriseRepository(db *gorm.DB) *EnterpriseRepository {
	return &EnterpriseRepository{db: db}
}

// ----------------- System Configuration -----------------

func (r *EnterpriseRepository) GetSystemConfig(key string) (string, error) {
	var cfg domain.SystemConfig
	err := r.db.Where("config_key = ?", key).First(&cfg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return cfg.Value, nil
}

func (r *EnterpriseRepository) SetSystemConfig(key, value string) error {
	cfg := domain.SystemConfig{
		ConfigKey: key,
		Value:     value,
		UpdatedAt: time.Now().UTC(),
	}
	return r.db.Where(domain.SystemConfig{ConfigKey: key}).
		Assign(domain.SystemConfig{Value: value, UpdatedAt: time.Now().UTC()}).
		FirstOrCreate(&cfg).Error
}

func (r *EnterpriseRepository) ListSystemConfigs() ([]domain.SystemConfig, error) {
	var list []domain.SystemConfig
	err := r.db.Order("config_key ASC").Find(&list).Error
	return list, err
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
	af := domain.AccountFeature{
		AccountID:   accountID,
		FeatureName: featureName,
		Enabled:     enabled,
		UpdatedAt:   time.Now().UTC(),
	}
	return r.db.Where(domain.AccountFeature{AccountID: accountID, FeatureName: featureName}).
		Assign(domain.AccountFeature{Enabled: enabled, UpdatedAt: time.Now().UTC()}).
		FirstOrCreate(&af).Error
}
