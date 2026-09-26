package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type channelAuthTENRepository struct{ db *gorm.DB }

func (r *channelAuthTENRepository) GetOnboarding(accountID uint) (*domain.Onboarding, error) {
	var onb domain.Onboarding
	err := r.db.Where("account_id = ?", accountID).First(&onb).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &domain.Onboarding{
				AccountID: accountID,
				Step:      "welcome",
				Completed: false,
			}, nil
		}
		return nil, err
	}
	return &onb, nil
}

func (r *channelAuthTENRepository) SaveOnboarding(onboarding *domain.Onboarding) error {
	var existing domain.Onboarding
	err := r.db.Where("account_id = ?", onboarding.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(onboarding).Error
		}
		return err
	}
	onboarding.ID = existing.ID
	return r.db.Save(onboarding).Error
}
