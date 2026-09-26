package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type channelAuthBILRepository struct{ db *gorm.DB }

func (r *channelAuthBILRepository) ListBillings(accountID uint) ([]domain.AccountBilling, error) {
	var list []domain.AccountBilling
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&list).Error
	return list, err
}

func (r *channelAuthBILRepository) ListSubscriptionPlans() ([]domain.SubscriptionPlan, error) {
	var plans []domain.SubscriptionPlan
	err := r.db.Find(&plans).Error
	if err == nil && len(plans) == 0 {
		// Initialize default tiers
		plans = []domain.SubscriptionPlan{
			{Name: "Community Free", Slug: "free", PriceCents: 0, AgentLimit: 2, AITokenLimit: 10000, Features: `["basic_chat","inbox"]`},
			{Name: "Team Starter", Slug: "starter", PriceCents: 2900, AgentLimit: 10, AITokenLimit: 100000, Features: `["basic_chat","inbox","automation","sla"]`},
			{Name: "Business Pro", Slug: "pro", PriceCents: 9900, AgentLimit: 50, AITokenLimit: 500000, Features: `["basic_chat","inbox","automation","sla","copilot","reports_export"]`},
			{Name: "Enterprise Elite", Slug: "enterprise", PriceCents: 29900, AgentLimit: 500, AITokenLimit: 5000000, Features: `["basic_chat","inbox","automation","sla","copilot","reports_export","saml","custom_roles"]`},
		}
		for i := range plans {
			_ = r.db.Create(&plans[i]).Error
		}
	}
	return plans, err
}

func (r *channelAuthBILRepository) GetSubscriptionPlan(id uint) (*domain.SubscriptionPlan, error) {
	var plan domain.SubscriptionPlan
	err := r.db.First(&plan, id).Error
	return &plan, err
}

func (r *channelAuthBILRepository) GetSubscriptionPlanBySlug(slug string) (*domain.SubscriptionPlan, error) {
	var plan domain.SubscriptionPlan
	if err := r.db.Where("slug = ?", slug).First(&plan).Error; err != nil {
		return nil, err
	}
	return &plan, nil
}

func (r *channelAuthBILRepository) GetAccountSubscription(accountID uint) (*domain.AccountSubscription, error) {
	var sub domain.AccountSubscription
	err := r.db.Preload("Plan").Where("account_id = ?", accountID).First(&sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// default to free tier
			plans, _ := r.ListSubscriptionPlans()
			var freePlanID uint = 1
			for _, p := range plans {
				if p.Slug == "free" {
					freePlanID = p.ID
					break
				}
			}
			now := time.Now().UTC()
			defaultSub := domain.AccountSubscription{
				AccountID:          accountID,
				PlanID:             freePlanID,
				Status:             "active",
				CurrentPeriodStart: now,
				CurrentPeriodEnd:   now.AddDate(1, 0, 0),
			}
			_ = r.db.Create(&defaultSub).Error
			_ = r.db.Preload("Plan").First(&defaultSub, defaultSub.ID).Error
			return &defaultSub, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *channelAuthBILRepository) SaveAccountSubscription(sub *domain.AccountSubscription) error {
	var existing domain.AccountSubscription
	err := r.db.Where("account_id = ?", sub.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(sub).Error
		}
		return err
	}
	sub.ID = existing.ID
	return r.db.Save(sub).Error
}

func (r *channelAuthBILRepository) CreateBilling(billing *domain.AccountBilling) error {
	if billing.ID != 0 {
		var count int64
		if err := r.db.Model(&domain.AccountBilling{}).Where("account_id = ? AND id = ?", billing.AccountID, billing.ID).Count(&count).Error; err != nil {
			return err
		}
		if count == 1 {
			return nil
		}
	}
	return r.db.Create(billing).Error
}
