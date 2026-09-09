package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// CampaignRepository manages outbound one-off and ongoing campaigns
type CampaignRepository struct {
	db *gorm.DB
}

// NewCampaignRepository creates a new campaign repository instance
func NewCampaignRepository(db *gorm.DB) *CampaignRepository {
	return &CampaignRepository{db: db}
}

// Create stores a new campaign
func (r *CampaignRepository) Create(ctx context.Context, camp *domain.Campaign) error {
	return r.db.WithContext(ctx).Create(camp).Error
}

// List lists all campaigns for an account
func (r *CampaignRepository) List(ctx context.Context, accountID uint) ([]domain.Campaign, error) {
	var list []domain.Campaign
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Order("id desc").Find(&list).Error
	return list, err
}

// GetByID finds a campaign by its ID and account ID
func (r *CampaignRepository) GetByID(ctx context.Context, accountID, id uint) (*domain.Campaign, error) {
	var camp domain.Campaign
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).First(&camp).Error
	if err != nil {
		return nil, err
	}
	return &camp, nil
}

// Update updates an existing campaign record
func (r *CampaignRepository) Update(ctx context.Context, camp *domain.Campaign) error {
	return r.db.WithContext(ctx).Save(camp).Error
}

// Delete removes a campaign by ID and account ID
func (r *CampaignRepository) Delete(ctx context.Context, accountID, id uint) error {
	return r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Campaign{}).Error
}

// UpdateStatus updates the lifecycle status of a campaign
func (r *CampaignRepository) UpdateStatus(ctx context.Context, accountID, id uint, status string) error {
	return r.db.WithContext(ctx).Model(&domain.Campaign{}).
		Where("account_id = ? AND id = ?", accountID, id).
		Update("status", status).Error
}

// GetDeliveries returns paginated deliveries for a campaign
func (r *CampaignRepository) GetDeliveries(ctx context.Context, accountID, campaignID uint, page, pageSize int) ([]domain.CampaignDelivery, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var list []domain.CampaignDelivery
	var total int64

	base := r.db.WithContext(ctx).Model(&domain.CampaignDelivery{}).
		Where("account_id = ? AND campaign_id = ?", accountID, campaignID)

	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := base.Order("id desc").Offset(offset).Limit(pageSize).Find(&list).Error
	return list, total, err
}

// GetDeliveryStats aggregates delivery statistics for a campaign
func (r *CampaignRepository) GetDeliveryStats(ctx context.Context, accountID, campaignID uint) (map[string]int64, error) {
	stats := map[string]int64{
		"total_deliveries": 0,
		"sent":             0,
		"delivered":        0,
		"failed":           0,
	}

	type statusCount struct {
		Status string
		Count  int64
	}
	var results []statusCount
	err := r.db.WithContext(ctx).Model(&domain.CampaignDelivery{}).
		Select("status, count(*) as count").
		Where("account_id = ? AND campaign_id = ?", accountID, campaignID).
		Group("status").
		Scan(&results).Error
	if err != nil {
		return stats, err
	}

	for _, sc := range results {
		stats[sc.Status] = sc.Count
		stats["total_deliveries"] += sc.Count
	}

	return stats, nil
}

// HasDeliveredToContact checks if a campaign has already been delivered to a contact
func (r *CampaignRepository) HasDeliveredToContact(ctx context.Context, accountID, campaignID, contactID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.CampaignDelivery{}).
		Where("account_id = ? AND campaign_id = ? AND contact_id = ?", accountID, campaignID, contactID).
		Count(&count).Error
	return count > 0, err
}

// ListActiveOngoing retrieves active ongoing campaigns for a given account and optional inbox
func (r *CampaignRepository) ListActiveOngoing(ctx context.Context, accountID uint, inboxID uint) ([]domain.Campaign, error) {
	var list []domain.Campaign
	q := r.db.WithContext(ctx).Where("account_id = ? AND status = ? AND campaign_type = ?", accountID, "active", "ongoing")
	if inboxID > 0 {
		q = q.Where("inbox_id = ?", inboxID)
	}
	err := q.Find(&list).Error
	return list, err
}
