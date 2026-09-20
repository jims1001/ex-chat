package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type EmailLogRepository struct{ db *gorm.DB }

func NewEmailLogRepository(db *gorm.DB) *EmailLogRepository { return &EmailLogRepository{db: db} }

func (r *EmailLogRepository) List(accountID uint, status, emailType string) ([]domain.EmailLog, error) {
	var logs []domain.EmailLog
	query := r.db.Where("account_id = ?", accountID)
	if status != "" {
		query = query.Where("status = ? OR delivery_status = ?", status, status)
	}
	if emailType != "" {
		query = query.Where("email_type = ?", emailType)
	}
	err := query.Order("id DESC").Limit(100).Find(&logs).Error
	return logs, err
}

func (r *EmailLogRepository) Find(accountID, logID uint) (*domain.EmailLog, error) {
	var emailLog domain.EmailLog
	if err := r.db.Where("account_id = ? AND id = ?", accountID, logID).First(&emailLog).Error; err != nil {
		return nil, err
	}
	return &emailLog, nil
}
