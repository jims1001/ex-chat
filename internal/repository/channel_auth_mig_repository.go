package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type channelAuthMIGRepository struct{ db *gorm.DB }

func (r *channelAuthMIGRepository) CreateDataImport(imp *domain.DataImport) error {
	return r.db.Create(imp).Error
}

func (r *channelAuthMIGRepository) ListDataImports(accountID uint) ([]domain.DataImport, error) {
	var imports []domain.DataImport
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&imports).Error
	return imports, err
}

func (r *channelAuthMIGRepository) GetDataImport(accountID, id uint) (*domain.DataImport, error) {
	var imp domain.DataImport
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&imp).Error
	if err != nil {
		return nil, err
	}
	return &imp, nil
}

func (r *channelAuthMIGRepository) UpdateDataImport(imp *domain.DataImport) error {
	return r.db.Save(imp).Error
}

func (r *channelAuthMIGRepository) DeleteDataImport(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.DataImport{}).Error
}

func (r *channelAuthMIGRepository) CreateMigrationJob(job *domain.MigrationJob) error {
	return r.db.Create(job).Error
}

func (r *channelAuthMIGRepository) ListMigrationJobs(accountID uint) ([]domain.MigrationJob, error) {
	var jobs []domain.MigrationJob
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&jobs).Error
	return jobs, err
}

func (r *channelAuthMIGRepository) CreateEmailMigration(mig *domain.EmailChannelMigration) error {
	return r.db.Create(mig).Error
}

func (r *channelAuthMIGRepository) ListEmailMigrations(accountID uint) ([]domain.EmailChannelMigration, error) {
	var migrations []domain.EmailChannelMigration
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&migrations).Error
	return migrations, err
}
