package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// CountReportBuckets reads only CON-owned aggregate counts, never conversation rows.
func (r *ConversationRepository) CountReportBuckets(ctx context.Context, accountID uint, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	if r == nil {
		return nil, errReportCountUnavailable
	}
	return countReportBuckets(ctx, r.db, accountID, "conversations", "", grouping, buckets)
}
