package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type CallRepository struct{ db *gorm.DB }

func NewCallRepository(db *gorm.DB) *CallRepository { return &CallRepository{db: db} }

func (r *CallRepository) ReassignContact(accountID, fromContactID, toContactID uint) error {
	return r.db.Model(&domain.Call{}).Where("account_id = ? AND contact_id = ?", accountID, fromContactID).Update("contact_id", toContactID).Error
}
