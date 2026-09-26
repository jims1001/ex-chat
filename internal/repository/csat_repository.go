package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// CSATRepository manages CSAT surveys and satisfaction metrics
type CSATRepository struct {
	db *gorm.DB
}

func NewCSATRepository(db *gorm.DB) *CSATRepository {
	return &CSATRepository{db: db}
}

func (r *CSATRepository) Create(ctx context.Context, survey *domain.CSATSurvey) error {
	return r.db.WithContext(ctx).Create(survey).Error
}

func (r *CSATRepository) GetMetrics(ctx context.Context, accountID uint) (map[string]any, error) {
	var count int64
	var avgRating float64

	r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).Where("account_id = ?", accountID).Count(&count)
	row := r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).Where("account_id = ?", accountID).Select("AVG(rating)").Row()
	_ = row.Scan(&avgRating)

	type RatingDist struct {
		Rating int   `json:"rating"`
		Count  int64 `json:"count"`
	}
	var dist []RatingDist
	r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).
		Select("rating, count(*) as count").
		Where("account_id = ?", accountID).
		Group("rating").
		Scan(&dist)

	return map[string]any{
		"total_responses": count,
		"average_rating":  avgRating,
		"distribution":    dist,
	}, nil
}
