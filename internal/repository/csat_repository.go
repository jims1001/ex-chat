package repository

import (
	"context"
	"database/sql"

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
	var avgRating sql.NullFloat64

	if err := r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).Where("account_id = ?", accountID).Count(&count).Error; err != nil {
		return nil, err
	}
	avgQuery := r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).Where("account_id = ?", accountID).Select("AVG(rating)")
	row := avgQuery.Row()
	if avgQuery.Error != nil {
		return nil, avgQuery.Error
	}
	if err := row.Scan(&avgRating); err != nil {
		return nil, err
	}

	type RatingDist struct {
		Rating int   `json:"rating"`
		Count  int64 `json:"count"`
	}
	var dist []RatingDist
	if err := r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).
		Select("rating, count(*) as count").
		Where("account_id = ?", accountID).
		Group("rating").
		Scan(&dist).Error; err != nil {
		return nil, err
	}

	return map[string]any{
		"total_responses": count,
		"average_rating":  avgRating.Float64,
		"distribution":    dist,
	}, nil
}
