package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// OrderRepository owns BIL order state mutations.
type OrderRepository struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

func (r *OrderRepository) Update(accountID uint, order *domain.Order) error {
	if accountID == 0 || order.ID == 0 || order.AccountID != accountID {
		return gorm.ErrRecordNotFound
	}
	if order.ConversationID != nil && *order.ConversationID > 0 {
		var count int64
		if err := r.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND id = ?", accountID, *order.ConversationID).
			Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return gorm.ErrRecordNotFound
		}
	}
	var existingCount int64
	if err := r.db.Model(&domain.Order{}).Where("account_id = ? AND id = ?", accountID, order.ID).Count(&existingCount).Error; err != nil {
		return err
	}
	if existingCount != 1 {
		return gorm.ErrRecordNotFound
	}
	result := r.db.Model(&domain.Order{}).Where("account_id = ? AND id = ?", accountID, order.ID).Select("*").Omit("id").Updates(order)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
