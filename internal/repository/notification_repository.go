package repository

import (
	"context"
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// NotificationRepository manages agent notifications
type NotificationRepository struct {
	db *gorm.DB
}

func NewNotificationRepository(db *gorm.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

func (r *NotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	return r.db.WithContext(ctx).Create(n).Error
}

func (r *NotificationRepository) FindByID(ctx context.Context, accountID, userID, id uint) (*domain.Notification, error) {
	var n domain.Notification
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		First(&n).Error
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r *NotificationRepository) Update(ctx context.Context, n *domain.Notification) error {
	return r.db.WithContext(ctx).Save(n).Error
}

func (r *NotificationRepository) List(ctx context.Context, accountID, userID uint) ([]domain.Notification, error) {
	return r.ListWithFilter(ctx, accountID, userID, "", false)
}

func (r *NotificationRepository) ListWithFilter(ctx context.Context, accountID, userID uint, status string, includeSnoozed bool) ([]domain.Notification, error) {
	var list []domain.Notification
	query := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ?", accountID, userID)
	now := time.Now().UTC()

	switch status {
	case "unread":
		query = query.Where("read_at IS NULL")
		if !includeSnoozed {
			query = query.Where("snoozed_until IS NULL OR snoozed_until <= ?", now)
		}
	case "read":
		query = query.Where("read_at IS NOT NULL")
		if !includeSnoozed {
			query = query.Where("snoozed_until IS NULL OR snoozed_until <= ?", now)
		}
	case "snoozed":
		query = query.Where("snoozed_until IS NOT NULL AND snoozed_until > ?", now)
	default:
		if !includeSnoozed {
			query = query.Where("snoozed_until IS NULL OR snoozed_until <= ?", now)
		}
	}

	err := query.Order("created_at desc").Find(&list).Error
	return list, err
}

func (r *NotificationRepository) MarkAllRead(ctx context.Context, accountID, userID uint) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND read_at IS NULL", accountID, userID).
		Update("read_at", now).Error
}

func (r *NotificationRepository) MarkConversationNotificationsRead(ctx context.Context, accountID, userID, conversationID uint) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND primary_actor_type = ? AND primary_actor_id = ? AND read_at IS NULL",
			accountID, userID, "Conversation", conversationID).
		Update("read_at", now).Error
}

func (r *NotificationRepository) MarkRead(ctx context.Context, accountID, userID, id uint) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("read_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) MarkUnread(ctx context.Context, accountID, userID, id uint) error {
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("read_at", nil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) Snooze(ctx context.Context, accountID, userID, id uint, snoozedUntil *time.Time) error {
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("snoozed_until", snoozedUntil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) Unsnooze(ctx context.Context, accountID, userID, id uint) error {
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("snoozed_until", nil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) Delete(ctx context.Context, accountID, userID, id uint) error {
	res := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Delete(&domain.Notification{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) BatchDelete(ctx context.Context, accountID, userID uint, ids []uint, onlyRead bool) (int64, error) {
	query := r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ?", accountID, userID)

	if len(ids) > 0 {
		query = query.Where("id IN (?)", ids)
	}
	if onlyRead {
		query = query.Where("read_at IS NOT NULL")
	}

	res := query.Delete(&domain.Notification{})
	return res.RowsAffected, res.Error
}

func (r *NotificationRepository) GetUnreadCount(ctx context.Context, accountID, userID uint) (unreadCount, totalCount, snoozedCount int64, err error) {
	now := time.Now().UTC()
	err = r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND read_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= ?)", accountID, userID, now).
		Count(&unreadCount).Error
	if err != nil {
		return 0, 0, 0, err
	}

	err = r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND snoozed_until IS NOT NULL AND snoozed_until > ?", accountID, userID, now).
		Count(&snoozedCount).Error
	if err != nil {
		return 0, 0, 0, err
	}

	err = r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ?", accountID, userID).
		Count(&totalCount).Error
	return unreadCount, totalCount, snoozedCount, err
}

func (r *NotificationRepository) GetNotificationSetting(ctx context.Context, accountID, userID uint) (*domain.NotificationSetting, error) {
	var setting domain.NotificationSetting
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ?", accountID, userID).
		First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Return default settings
			return &domain.NotificationSetting{
				AccountID:          accountID,
				UserID:             userID,
				SelectedEmailFlags: `["conversation_assignment","conversation_mention"]`,
				SelectedPushFlags:  `["conversation_assignment","conversation_mention","sla_breach"]`,
				SelectedInAppFlags: `["conversation_assignment","conversation_mention","conversation_creation","sla_breach","system_alert"]`,
				Muted:              false,
				QuietHoursEnabled:  false,
				QuietHoursStart:    "22:00",
				QuietHoursEnd:      "08:00",
			}, nil
		}
		return nil, err
	}
	return &setting, nil
}

func (r *NotificationRepository) UpsertNotificationSetting(ctx context.Context, setting *domain.NotificationSetting) error {
	var existing domain.NotificationSetting
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ?", setting.AccountID, setting.UserID).
		First(&existing).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.WithContext(ctx).Create(setting).Error
		}
		return err
	}

	existing.SelectedEmailFlags = setting.SelectedEmailFlags
	existing.SelectedPushFlags = setting.SelectedPushFlags
	existing.SelectedInAppFlags = setting.SelectedInAppFlags
	existing.Muted = setting.Muted
	existing.QuietHoursEnabled = setting.QuietHoursEnabled
	existing.QuietHoursStart = setting.QuietHoursStart
	existing.QuietHoursEnd = setting.QuietHoursEnd
	existing.UpdatedAt = time.Now().UTC()

	return r.db.WithContext(ctx).Save(&existing).Error
}
