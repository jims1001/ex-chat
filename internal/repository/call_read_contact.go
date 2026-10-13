package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// FindCallReadCaller supplies only the existing caller fields after Call GET
// authorization. Do not preload unrelated contact aggregates or log raw errors.
func (r *ContactRepository) FindCallReadCaller(accountID, contactID uint) (*domain.Contact, error) {
	if r == nil || r.db == nil || r.db.Config == nil || r.db.Dialector == nil {
		return nil, errors.New("call caller unavailable")
	}
	var contact domain.Contact
	err := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Select("id", "account_id", "name", "phone_number", "avatar_url").
		Where("account_id = ? AND id = ?", accountID, contactID).First(&contact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &contact, err
}
