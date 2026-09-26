package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// SLARepository manages Service Level Agreements
type SLARepository struct {
	db *gorm.DB
}

func NewSLARepository(db *gorm.DB) *SLARepository {
	return &SLARepository{db: db}
}

func (r *SLARepository) Create(ctx context.Context, sla *domain.SLAPolicy) error {
	return r.db.WithContext(ctx).Create(sla).Error
}

func (r *SLARepository) List(ctx context.Context, accountID uint) ([]domain.SLAPolicy, error) {
	var list []domain.SLAPolicy
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Find(&list).Error
	return list, err
}

func (r *SLARepository) GetByID(ctx context.Context, accountID, id uint) (*domain.SLAPolicy, error) {
	var sla domain.SLAPolicy
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).First(&sla).Error
	if err != nil {
		return nil, err
	}
	return &sla, nil
}

func (r *SLARepository) Update(ctx context.Context, sla *domain.SLAPolicy) error {
	return r.db.WithContext(ctx).Save(sla).Error
}

func (r *SLARepository) Delete(ctx context.Context, accountID, id uint) error {
	return r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.SLAPolicy{}).Error
}
