package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// SystemConfigRepository owns SYS configuration persistence.
type SystemConfigRepository struct{ db *gorm.DB }

func NewSystemConfigRepository(db *gorm.DB) *SystemConfigRepository {
	// Config keys, values and database errors may contain credentials. Silence
	// this repository's SQL tracing without changing the caller's logger.
	return &SystemConfigRepository{db: db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})}
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
	// Empty input preserves both existing and absent settings without reading or
	// writing. In particular, never read then write back a stale secret value.
	if value == "" {
		return nil
	}
	now := time.Now().UTC()
	cfg := domain.SystemConfig{ConfigKey: key, Value: value, UpdatedAt: now}
	// A single statement resolves concurrent creates and replaces only the two
	// mutable fields. Existing last-writer-wins behavior is retained.
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "config_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&cfg).Error
}

func (r *SystemConfigRepository) List() ([]domain.SystemConfig, error) {
	var list []domain.SystemConfig
	err := r.db.Order("config_key ASC").Find(&list).Error
	return list, err
}
