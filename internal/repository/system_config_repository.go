package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// SystemConfigRepository owns SYS configuration persistence.
type SystemConfigRepository struct{ db *gorm.DB }

func NewSystemConfigRepository(db *gorm.DB) *SystemConfigRepository {
	return &SystemConfigRepository{db: db}
}

func (r *SystemConfigRepository) Get(key string) (string, error) {
	var cfg domain.SystemConfig
	err := r.db.Where("config_key = ?", key).First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return cfg.Value, nil
}

func (r *SystemConfigRepository) Set(key, value string) error {
	now := time.Now().UTC()
	cfg := domain.SystemConfig{ConfigKey: key, Value: value, UpdatedAt: now}
	return r.db.Where(domain.SystemConfig{ConfigKey: key}).
		Assign(domain.SystemConfig{Value: value, UpdatedAt: now}).FirstOrCreate(&cfg).Error
}

func (r *SystemConfigRepository) List() ([]domain.SystemConfig, error) {
	var list []domain.SystemConfig
	err := r.db.Order("config_key ASC").Find(&list).Error
	return list, err
}
