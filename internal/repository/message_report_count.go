package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// CountReportBuckets reads only MSG-owned counts of the exact incoming/outgoing type.
// Every matching message counts, regardless of private flag, status, or content.
func (r *MessageRepository) CountReportBuckets(ctx context.Context, accountID uint, messageType string, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	if r == nil || (messageType != domain.MessageTypeIncoming && messageType != domain.MessageTypeOutgoing) {
		return nil, errReportCountUnavailable
	}
	return countReportBuckets(ctx, r.db, accountID, "messages", messageType, grouping, buckets)
}
