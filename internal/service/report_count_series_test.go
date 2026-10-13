package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

type countConversationStub struct {
	call func(context.Context, uint, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error)
}

func (s countConversationStub) CountReportBuckets(ctx context.Context, a uint, _ domain.ReportCountGrouping, b []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	return s.call(ctx, a, b)
}

type countMessageStub struct {
	call func(context.Context, uint, string, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error)
}

func (s countMessageStub) CountReportBuckets(ctx context.Context, a uint, m string, _ domain.ReportCountGrouping, b []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	return s.call(ctx, a, m, b)
}

func TestReportCountSeriesBudgetAndOwnerSelection(t *testing.T) {
	start := time.Date(2024, 2, 29, 12, 0, 0, 123, time.UTC)
	end := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)
	for _, metric := range []string{"conversations_count", "incoming_messages_count", "outgoing_messages_count"} {
		t.Run(metric, func(t *testing.T) {
			calls := 0
			s := &ReportService{}
			check := func(ctx context.Context, a uint, b []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
				calls++
				require.Equal(t, uint(7), a)
				require.Len(t, b, 2)
				require.Equal(t, start, b[0].Since)
				require.Equal(t, time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), b[0].Until)
				require.Equal(t, end, b[1].Until)
				return []domain.ReportCountPoint{{Value: 2, Timestamp: b[0].Timestamp}}, nil
			}
			s.SetCountOwners(countConversationStub{call: func(c context.Context, a uint, b []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
				require.Equal(t, "conversations_count", metric)
				return check(c, a, b)
			}}, countMessageStub{call: func(c context.Context, a uint, m string, b []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
				require.Equal(t, m+"_messages_count", metric)
				return check(c, a, b)
			}})
			for _, business := range []bool{false, true} {
				points, err := s.GetCountSeries(context.Background(), 7, metric, domain.ReportCountDay, ReportFilter{Since: &start, Until: &end, BusinessHours: business})
				require.NoError(t, err)
				require.Len(t, points, 1)
			}
			require.Equal(t, 2, calls)
		})
	}
	t.Run("exact_400_intersecting_days_and_401_rejection", func(t *testing.T) {
		day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		finish := day.AddDate(0, 0, 400)
		calls := 0
		s := &ReportService{}
		s.SetCountOwners(countConversationStub{call: func(_ context.Context, _ uint, b []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
			calls++
			require.Len(t, b, 400)
			return nil, nil
		}}, nil)
		p, e := s.GetCountSeries(context.Background(), 1, "conversations_count", domain.ReportCountDay, ReportFilter{Since: &day, Until: &finish})
		require.NoError(t, e)
		require.NotNil(t, p)
		finish = finish.Add(time.Nanosecond)
		p, e = s.GetCountSeries(context.Background(), 1, "conversations_count", domain.ReportCountDay, ReportFilter{Since: &day, Until: &finish})
		require.ErrorIs(t, e, ErrReportCountBudget)
		require.Nil(t, p)
		require.Equal(t, 1, calls)
	})
	t.Run("empty_reversed_and_missing_bounds", func(t *testing.T) {
		s := &ReportService{}
		for _, finish := range []time.Time{start, start.Add(-time.Nanosecond)} {
			p, e := s.GetCountSeries(context.Background(), 1, "conversations_count", domain.ReportCountDay, ReportFilter{Since: &start, Until: &finish})
			require.NoError(t, e)
			require.Equal(t, []domain.ReportCountPoint{}, p)
		}
		p, e := s.GetCountSeries(context.Background(), 1, "conversations_count", domain.ReportCountDay, ReportFilter{})
		require.ErrorIs(t, e, ErrReportCountBounds)
		require.Nil(t, p)
	})
}

func TestReportCountSeriesCancellationAndFailures(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	filter := ReportFilter{Since: &start, Until: &end}
	t.Run("cancel_before_owner", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s := &ReportService{}
		p, e := s.GetCountSeries(ctx, 1, "conversations_count", domain.ReportCountDay, filter)
		require.ErrorIs(t, e, context.Canceled)
		require.Nil(t, p)
	})
	t.Run("cancel_after_owner_discards_partial", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &ReportService{}
		s.SetCountOwners(countConversationStub{call: func(c context.Context, _ uint, _ []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
			require.Equal(t, ctx, c)
			cancel()
			return []domain.ReportCountPoint{{Value: 7}}, nil
		}}, nil)
		p, e := s.GetCountSeries(ctx, 1, "conversations_count", domain.ReportCountDay, filter)
		require.ErrorIs(t, e, context.Canceled)
		require.Nil(t, p)
	})
	t.Run("owner_error_discards_partial_and_private_error", func(t *testing.T) {
		s := &ReportService{}
		s.SetCountOwners(countConversationStub{call: func(context.Context, uint, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
			return []domain.ReportCountPoint{{Value: 7}}, errors.New("private SQL driver")
		}}, nil)
		p, e := s.GetCountSeries(context.Background(), 1, "conversations_count", domain.ReportCountDay, filter)
		require.ErrorIs(t, e, ErrReportCountUnavailable)
		require.NotContains(t, e.Error(), "private")
		require.Nil(t, p)
	})
	t.Run("owner_observes_deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		s := &ReportService{}
		s.SetCountOwners(countConversationStub{call: func(c context.Context, _ uint, _ []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
			<-c.Done()
			return nil, c.Err()
		}}, nil)
		p, e := s.GetCountSeries(ctx, 1, "conversations_count", domain.ReportCountDay, filter)
		require.ErrorIs(t, e, context.DeadlineExceeded)
		require.Nil(t, p)
	})
	t.Run("missing_owner_is_unavailable", func(t *testing.T) {
		s := &ReportService{}
		p, e := s.GetCountSeries(context.Background(), 1, "outgoing_messages_count", domain.ReportCountDay, filter)
		require.ErrorIs(t, e, ErrReportCountUnavailable)
		require.Nil(t, p)
	})
}
