package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// OrderRepository owns BIL order state mutations.
type OrderRepository struct {
	db            *gorm.DB
	conversations orderConversationPort
}

type orderConversationPort interface {
	MessageTargetExists(accountID, conversationID uint) (bool, error)
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return NewOrderRepositoryWithConversationPort(db, NewConversationRepository(db))
}

func NewOrderRepositoryWithConversationPort(db *gorm.DB, conversations orderConversationPort) *OrderRepository {
	return &OrderRepository{db: db, conversations: conversations}
}

func (r *OrderRepository) FindForTool(accountID uint, orderID, trackingNumber string) (*domain.Order, error) {
	var order domain.Order
	query := r.db.Where("account_id = ?", accountID)
	switch {
	case orderID != "" && trackingNumber != "":
		query = query.Where("order_id = ? OR tracking_number = ?", orderID, trackingNumber)
	case orderID != "":
		query = query.Where("order_id = ?", orderID)
	case trackingNumber != "":
		query = query.Where("tracking_number = ?", trackingNumber)
	default:
		return nil, gorm.ErrInvalidData
	}
	if err := query.First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &order, nil
}

func (r *OrderRepository) ReassignContact(accountID, fromContactID, toContactID uint) error {
	return r.db.Model(&domain.Order{}).Where("account_id = ? AND contact_id = ?", accountID, fromContactID).Update("contact_id", toContactID).Error
}

func (r *OrderRepository) Update(accountID uint, order *domain.Order) error {
	if accountID == 0 || order.ID == 0 || order.AccountID != accountID {
		return gorm.ErrRecordNotFound
	}
	if order.ConversationID != nil && *order.ConversationID > 0 {
		if r.conversations == nil {
			return gorm.ErrInvalidData
		}
		exists, err := r.conversations.MessageTargetExists(accountID, *order.ConversationID)
		if err != nil {
			return err
		}
		if !exists {
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
