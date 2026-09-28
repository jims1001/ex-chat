package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// IntegrationRepository owns EXT installation state mutations.
type IntegrationRepository struct {
	db *gorm.DB
}

func NewIntegrationRepository(db *gorm.DB) *IntegrationRepository {
	return &IntegrationRepository{db: db}
}

func (r *IntegrationRepository) ListByAccount(accountID uint) ([]domain.IntegrationInstallation, error) {
	installations := make([]domain.IntegrationInstallation, 0)
	err := r.db.Where("account_id = ?", accountID).Order("id ASC").Find(&installations).Error
	return installations, err
}

func (r *IntegrationRepository) FindInstalled(accountID uint, appID string) (*domain.IntegrationInstallation, error) {
	var installation domain.IntegrationInstallation
	err := r.db.Where("account_id = ? AND app_id = ? AND status = ?", accountID, appID, "installed").First(&installation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &installation, err
}

func (r *IntegrationRepository) Install(accountID uint, appID, settings string) (*domain.IntegrationInstallation, error) {
	var installation domain.IntegrationInstallation
	err := r.db.Where("account_id = ? AND app_id = ?", accountID, appID).
		FirstOrCreate(&installation, domain.IntegrationInstallation{AccountID: accountID, AppID: appID}).Error
	if err != nil {
		return nil, err
	}
	installation.Status = "installed"
	installation.Settings = settings
	if err := r.db.Save(&installation).Error; err != nil {
		return nil, err
	}
	return &installation, nil
}

func (r *IntegrationRepository) Disable(accountID uint, appID string) error {
	return r.db.Model(&domain.IntegrationInstallation{}).
		Where("account_id = ? AND app_id = ?", accountID, appID).
		Update("status", "disabled").Error
}
