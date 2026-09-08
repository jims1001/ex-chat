package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// DeviceRepository manages push notification subscriptions (FCM, APNs, Browser)
type DeviceRepository struct {
	db *gorm.DB
}

func NewDeviceRepository(db *gorm.DB) *DeviceRepository {
	return &DeviceRepository{db: db}
}

func (r *DeviceRepository) UpsertSubscription(ctx context.Context, sub *domain.NotificationSubscription) error {
	var existing domain.NotificationSubscription
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND account_id = ? AND push_token = ?", sub.UserID, sub.AccountID, sub.PushToken).
		First(&existing).Error

	if err == nil {
		// Update existing
		existing.SubscriptionType = sub.SubscriptionType
		existing.DeviceName = sub.DeviceName
		existing.AppVersion = sub.AppVersion
		return r.db.WithContext(ctx).Save(&existing).Error
	}

	return r.db.WithContext(ctx).Create(sub).Error
}

func (r *DeviceRepository) DeleteSubscription(ctx context.Context, userID, accountID uint, pushToken string) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND account_id = ? AND push_token = ?", userID, accountID, pushToken).
		Delete(&domain.NotificationSubscription{}).Error
}

func (r *DeviceRepository) ListSubscriptions(ctx context.Context, userID, accountID uint) ([]domain.NotificationSubscription, error) {
	var subs []domain.NotificationSubscription
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND account_id = ?", userID, accountID).
		Find(&subs).Error
	return subs, err
}
