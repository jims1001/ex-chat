package repository

import (
	"encoding/json"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type WebhookRepository struct {
	db *gorm.DB
}

func NewWebhookRepository(db *gorm.DB) *WebhookRepository {
	return &WebhookRepository{db: db}
}

func (r *WebhookRepository) Create(w *domain.Webhook) error {
	return r.db.Create(w).Error
}

func (r *WebhookRepository) Update(w *domain.Webhook) error {
	return r.db.Save(w).Error
}

func (r *WebhookRepository) List(accountID uint) ([]domain.Webhook, error) {
	var list []domain.Webhook
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&list).Error
	return list, err
}

func (r *WebhookRepository) FindByID(accountID, id uint) (*domain.Webhook, error) {
	var w domain.Webhook
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&w).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

func (r *WebhookRepository) Delete(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Webhook{}).Error
}

func (r *WebhookRepository) ListSubscribed(accountID uint, eventName string) ([]domain.Webhook, error) {
	all, err := r.List(accountID)
	if err != nil {
		return nil, err
	}

	var matched []domain.Webhook
	for _, w := range all {
		var subs []string
		if err := json.Unmarshal([]byte(w.Subscriptions), &subs); err == nil {
			for _, sub := range subs {
				if sub == eventName || sub == "*" {
					matched = append(matched, w)
					break
				}
			}
		}
	}
	return matched, nil
}
