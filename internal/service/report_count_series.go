package service

import (
	"context"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
)

const ReportCountMaxBuckets = 400

var (
	ErrReportCountOffset                = errors.New("report count offset is not representable")
	ErrReportCountGrouping              = errors.New("unsupported report count grouping")
	ErrReportCountBounds                = errors.New("valid report count time bounds are required")
	ErrReportCountBudget                = errors.New("report count range exceeds 400 selected buckets")
	ErrReportCountMetric                = errors.New("unsupported report count metric")
	ErrReportCountUnavailable           = errors.New("report counts are unavailable")
	ErrReportCountStorageRepresentation = errors.New("report count storage representation is unsupported")
	ErrReportCountQueryRepresentation   = errors.New("report count query representation is unsupported")
)

// These local RPT capabilities expose only aggregate counts from each data owner.
// They are deliberately separate from the legacy object-loading report ports.
type ReportConversationCountPort interface {
	CountReportBuckets(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error)
}
type ReportMessageCountPort interface {
	CountReportBuckets(context.Context, uint, string, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error)
}

func (s *ReportService) SetCountOwners(conversations ReportConversationCountPort, messages ReportMessageCountPort) {
	s.conversationCounts, s.messageCounts = conversations, messages
}

// GetCountSeries reads one bounded owner snapshot. business_hours does not
// alter record counts. Message counts use messages.created_at independently of
// their conversation's creation date, delivery status, privacy or sender type.
func (s *ReportService) GetCountSeries(ctx context.Context, accountID uint, metric string, grouping domain.ReportCountGrouping, filter ReportFilter) ([]domain.ReportCountPoint, error) {
	return s.GetCountSeriesWithOffset(ctx, accountID, metric, grouping, filter, 0)
}

// GetCountSeriesWithOffset changes only calendar boundaries, never the requested
// absolute interval or owner selection. The numeric offset is exact whole seconds.
func (s *ReportService) GetCountSeriesWithOffset(ctx context.Context, accountID uint, metric string, grouping domain.ReportCountGrouping, filter ReportFilter, offsetSeconds int64) ([]domain.ReportCountPoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offsetSeconds < -domain.ReportCountMaxOffsetSeconds || offsetSeconds > domain.ReportCountMaxOffsetSeconds {
		return nil, ErrReportCountOffset
	}
	if filter.Since == nil || filter.Until == nil {
		return nil, ErrReportCountBounds
	}
	switch metric {
	case "conversations_count", "incoming_messages_count", "outgoing_messages_count":
	default:
		return nil, ErrReportCountMetric
	}
	switch grouping {
	case domain.ReportCountHour, domain.ReportCountDay, domain.ReportCountMonth, domain.ReportCountYear:
	default:
		return nil, ErrReportCountGrouping
	}
	start, end := filter.Since.UTC(), filter.Until.UTC()
	if !domain.ReportCountOffsetInstantValid(start, offsetSeconds) || !domain.ReportCountOffsetInstantValid(end, offsetSeconds) {
		return nil, ErrReportCountBounds
	}
	points := make([]domain.ReportCountPoint, 0)
	if !end.After(start) {
		return points, ctx.Err()
	}
	bucketStart, _, valid := domain.ReportCountCalendarBounds(start, grouping, offsetSeconds)
	if !valid {
		return nil, ErrReportCountBounds
	}
	buckets := make([]domain.ReportCountBucket, 0, 32)
	for bucketStart.Before(end) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(buckets) == ReportCountMaxBuckets {
			return nil, ErrReportCountBudget
		}
		_, next, valid := domain.ReportCountCalendarBounds(bucketStart, grouping, offsetSeconds)
		if !valid {
			return nil, ErrReportCountBounds
		}
		lo, hi := bucketStart, next
		if start.After(lo) {
			lo = start
		}
		if end.Before(hi) {
			hi = end
		}
		buckets = append(buckets, domain.ReportCountBucket{Timestamp: bucketStart.Unix(), OffsetSeconds: offsetSeconds, Since: lo, Until: hi})
		bucketStart = next
	}
	var err error
	switch metric {
	case "conversations_count":
		if s.conversationCounts == nil {
			return nil, ErrReportCountUnavailable
		}
		points, err = s.conversationCounts.CountReportBuckets(ctx, accountID, grouping, buckets)
	case "incoming_messages_count", "outgoing_messages_count":
		if s.messageCounts == nil {
			return nil, ErrReportCountUnavailable
		}
		messageType := domain.MessageTypeIncoming
		if metric == "outgoing_messages_count" {
			messageType = domain.MessageTypeOutgoing
		}
		points, err = s.messageCounts.CountReportBuckets(ctx, accountID, messageType, grouping, buckets)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, repository.ErrReportCountQueryRepresentation) {
		return nil, ErrReportCountQueryRepresentation
	}
	if errors.Is(err, repository.ErrReportCountStorageRepresentation) {
		return nil, ErrReportCountStorageRepresentation
	}
	if err != nil {
		return nil, ErrReportCountUnavailable
	}
	if points == nil {
		points = make([]domain.ReportCountPoint, 0)
	}
	return points, nil
}
