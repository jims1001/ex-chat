package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type channelAuthENTRepository struct{ db *gorm.DB }

func (r *channelAuthENTRepository) GetAccountLimit(accountID uint) (*domain.AccountLimit, error) {
	var limit domain.AccountLimit
	err := r.db.Where("account_id = ?", accountID).First(&limit).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &domain.AccountLimit{
				AccountID:         accountID,
				ConversationLimit: 10000,
				AgentLimit:        50,
				InboxLimit:        20,
				Credits:           1000.0,
				Currency:          "USD",
			}, nil
		}
		return nil, err
	}
	return &limit, nil
}

func (r *channelAuthENTRepository) SaveAccountLimit(limit *domain.AccountLimit) error {
	var existing domain.AccountLimit
	err := r.db.Where("account_id = ?", limit.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(limit).Error
		}
		return err
	}
	limit.ID = existing.ID
	return r.db.Save(limit).Error
}

func (r *channelAuthENTRepository) GetBrandedEmailLayout(accountID uint) (*domain.BrandedEmailLayout, error) {
	var layout domain.BrandedEmailLayout
	err := r.db.Where("account_id = ?", accountID).First(&layout).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &layout, nil
}

func (r *channelAuthENTRepository) SaveBrandedEmailLayout(layout *domain.BrandedEmailLayout) error {
	var existing domain.BrandedEmailLayout
	err := r.db.Where("account_id = ?", layout.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(layout).Error
		}
		return err
	}
	layout.ID = existing.ID
	return r.db.Save(layout).Error
}

func (r *channelAuthENTRepository) ApplyBilling(billing *domain.AccountBilling) error {
	changeID := fmt.Sprintf("billing-credit-%d", billing.ID)
	return r.db.Transaction(func(tx *gorm.DB) error {
		var applied int64
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", changeID).Count(&applied).Error; err != nil {
			return err
		}
		if applied != 0 {
			return nil
		}
		var limit domain.AccountLimit
		if err := tx.Where("account_id = ?", billing.AccountID).First(&limit).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			limit = domain.AccountLimit{AccountID: billing.AccountID, Credits: 1000, Currency: billing.Currency}
			if err := tx.Create(&limit).Error; err != nil {
				return err
			}
		}
		if billing.ActionType == "deposit" {
			limit.Credits += billing.Amount
		} else if billing.ActionType == "deduction" {
			limit.Credits -= billing.Amount
		}
		if err := tx.Save(&limit).Error; err != nil {
			return err
		}
		change := domain.LocalChangeJournal{ChangeID: changeID, SourceModule: "ENT", AccountID: billing.AccountID, ObjectType: "AccountLimit", ObjectID: limit.ID, Action: "update", ChangedFields: "credits", ActorType: "system", SourceType: "bil.account_billing.created.v1", SourceID: fmt.Sprint(billing.ID), OccurredAt: time.Now().UTC()}
		return NewJournalRepository(tx).RecordLocalChange(tx, &change)
	})
}
