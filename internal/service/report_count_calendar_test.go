package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

type calendarConversationCountStub struct {
	call func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error)
}

func (s calendarConversationCountStub) CountReportBuckets(ctx context.Context, account uint, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	return s.call(ctx, account, grouping, buckets)
}

type calendarMessageCountStub struct {
	call func(context.Context, uint, string, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error)
}

func (s calendarMessageCountStub) CountReportBuckets(ctx context.Context, account uint, messageType string, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	return s.call(ctx, account, messageType, grouping, buckets)
}

var calendarCountMetrics = []string{"conversations_count", "incoming_messages_count", "outgoing_messages_count"}
var calendarCountGroupings = []domain.ReportCountGrouping{domain.ReportCountHour, domain.ReportCountDay, domain.ReportCountMonth, domain.ReportCountYear}

func calendarCountAt(year int, month time.Month, day, hour, minute, second, nanosecond int) time.Time {
	return time.Date(year, month, day, hour, minute, second, nanosecond, time.UTC)
}

func calendarCountSetOwners(t *testing.T, s *ReportService, metric string, check func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error)) {
	t.Helper()
	s.SetCountOwners(calendarConversationCountStub{call: func(ctx context.Context, account uint, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
		require.Equal(t, "conversations_count", metric)
		return check(ctx, account, grouping, buckets)
	}}, calendarMessageCountStub{call: func(ctx context.Context, account uint, messageType string, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
		require.Equal(t, messageType+"_messages_count", metric)
		return check(ctx, account, grouping, buckets)
	}})
}

func TestReportCountCalendarServiceUTCLabelsAndClipping(t *testing.T) {
	at := calendarCountAt
	cases := []struct {
		name     string
		grouping domain.ReportCountGrouping
		since    time.Time
		until    time.Time
		starts   []time.Time
	}{
		{"hour_leap_day", domain.ReportCountHour, at(2024, 2, 28, 23, 59, 59, 123456789), at(2024, 2, 29, 1, 30, 0, 987654321), []time.Time{at(2024, 2, 28, 23, 0, 0, 0), at(2024, 2, 29, 0, 0, 0, 0), at(2024, 2, 29, 1, 0, 0, 0), at(2024, 2, 29, 2, 0, 0, 0)}},
		{"hour_year_end", domain.ReportCountHour, at(2023, 12, 31, 23, 30, 0, 123456789), at(2024, 1, 1, 1, 0, 0, 0), []time.Time{at(2023, 12, 31, 23, 0, 0, 0), at(2024, 1, 1, 0, 0, 0, 0), at(2024, 1, 1, 1, 0, 0, 0)}},
		{"day_leap_month_end", domain.ReportCountDay, at(2024, 2, 28, 12, 0, 0, 123456789), at(2024, 3, 1, 4, 0, 0, 987654321), []time.Time{at(2024, 2, 28, 0, 0, 0, 0), at(2024, 2, 29, 0, 0, 0, 0), at(2024, 3, 1, 0, 0, 0, 0), at(2024, 3, 2, 0, 0, 0, 0)}},
		{"month_leap_february", domain.ReportCountMonth, at(2024, 1, 31, 12, 0, 0, 123456789), at(2024, 3, 1, 12, 0, 0, 987654321), []time.Time{at(2024, 1, 1, 0, 0, 0, 0), at(2024, 2, 1, 0, 0, 0, 0), at(2024, 3, 1, 0, 0, 0, 0), at(2024, 4, 1, 0, 0, 0, 0)}},
		{"month_year_end", domain.ReportCountMonth, at(2023, 12, 31, 23, 0, 0, 123456789), at(2024, 2, 1, 0, 0, 0, 0), []time.Time{at(2023, 12, 1, 0, 0, 0, 0), at(2024, 1, 1, 0, 0, 0, 0), at(2024, 2, 1, 0, 0, 0, 0)}},
		{"year_contains_leap_day", domain.ReportCountYear, at(2023, 12, 31, 23, 0, 0, 123456789), at(2025, 1, 1, 0, 0, 0, 987654321), []time.Time{at(2023, 1, 1, 0, 0, 0, 0), at(2024, 1, 1, 0, 0, 0, 0), at(2025, 1, 1, 0, 0, 0, 0), at(2026, 1, 1, 0, 0, 0, 0)}},
		{"year_9999_clipped", domain.ReportCountYear, at(9999, 12, 30, 0, 0, 0, 123456789), at(9999, 12, 31, 23, 59, 59, 987654321), []time.Time{at(9999, 1, 1, 0, 0, 0, 0), at(10000, 1, 1, 0, 0, 0, 0)}},
	}
	for _, metric := range calendarCountMetrics {
		t.Run(metric, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					want := make([]domain.ReportCountBucket, len(tc.starts)-1)
					for i := range want {
						want[i] = domain.ReportCountBucket{Timestamp: tc.starts[i].Unix(), Since: tc.starts[i], Until: tc.starts[i+1]}
					}
					want[0].Since = tc.since
					want[len(want)-1].Until = tc.until
					ctx := context.WithValue(context.Background(), struct{ calendar bool }{true}, "owner-context")
					calls := 0
					s := &ReportService{}
					returned := []domain.ReportCountPoint{{Value: 7, Timestamp: want[len(want)-1].Timestamp}}
					calendarCountSetOwners(t, s, metric, func(gotCtx context.Context, account uint, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
						calls++
						require.Equal(t, ctx, gotCtx)
						require.Equal(t, uint(71), account)
						require.Equal(t, tc.grouping, grouping)
						require.Equal(t, want, buckets)
						return returned, nil
					})
					// Input timezone never changes the UTC natural labels, and
					// business_hours never alters record count selection.
					since := tc.since.In(time.FixedZone("request", -6*3600))
					until := tc.until.In(time.FixedZone("request", 9*3600))
					for _, business := range []bool{false, true} {
						got, err := s.GetCountSeries(ctx, 71, metric, tc.grouping, ReportFilter{Since: &since, Until: &until, BusinessHours: business})
						require.NoError(t, err)
						require.Equal(t, returned, got)
					}
					require.Equal(t, 2, calls)
				})
			}
		})
	}
}

func TestReportCountCalendarServiceSelectedBucketBudget(t *testing.T) {
	for _, metric := range calendarCountMetrics {
		t.Run(metric, func(t *testing.T) {
			for _, grouping := range calendarCountGroupings {
				t.Run(string(grouping), func(t *testing.T) {
					first := calendarCountAt(2024, 1, 1, 0, 0, 0, 0)
					since := first.Add(time.Nanosecond)
					var until time.Time
					switch grouping {
					case domain.ReportCountHour:
						until = first.Add(400 * time.Hour)
					case domain.ReportCountDay:
						until = first.AddDate(0, 0, 400)
					case domain.ReportCountMonth:
						until = first.AddDate(0, 400, 0)
					case domain.ReportCountYear:
						until = first.AddDate(400, 0, 0)
					}
					calls := 0
					s := &ReportService{}
					calendarCountSetOwners(t, s, metric, func(_ context.Context, account uint, gotGrouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
						calls++
						require.Equal(t, uint(71), account)
						require.Equal(t, grouping, gotGrouping)
						require.Len(t, buckets, 400)
						require.Equal(t, first.Unix(), buckets[0].Timestamp)
						require.Equal(t, since, buckets[0].Since)
						require.Equal(t, until, buckets[399].Until)
						return nil, nil
					})
					got, err := s.GetCountSeries(context.Background(), 71, metric, grouping, ReportFilter{Since: &since, Until: &until})
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{}, got)
					until = until.Add(time.Nanosecond)
					got, err = s.GetCountSeries(context.Background(), 71, metric, grouping, ReportFilter{Since: &since, Until: &until})
					require.ErrorIs(t, err, ErrReportCountBudget)
					require.Nil(t, got)
					require.Equal(t, 1, calls)
					// Budget validation also precedes missing owner lookup.
					got, err = (&ReportService{}).GetCountSeries(context.Background(), 71, metric, grouping, ReportFilter{Since: &since, Until: &until})
					require.ErrorIs(t, err, ErrReportCountBudget)
					require.Nil(t, got)
				})
			}
		})
	}
}

func TestReportCountCalendarServiceValidationAndCancellation(t *testing.T) {
	since := calendarCountAt(2024, 2, 29, 12, 0, 0, 123456789)
	until := since.Add(time.Minute)
	filter := ReportFilter{Since: &since, Until: &until}
	for _, metric := range calendarCountMetrics {
		t.Run(metric, func(t *testing.T) {
			for _, grouping := range calendarCountGroupings {
				t.Run(string(grouping), func(t *testing.T) {
					t.Run("empty_reversed_and_missing_bounds_before_owner", func(t *testing.T) {
						s := &ReportService{}
						calendarCountSetOwners(t, s, metric, func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
							t.Fatal("invalid or empty interval reached an owner")
							return nil, nil
						})
						for _, end := range []time.Time{since, since.Add(-time.Nanosecond)} {
							got, err := s.GetCountSeries(context.Background(), 71, metric, grouping, ReportFilter{Since: &since, Until: &end})
							require.NoError(t, err)
							require.Equal(t, []domain.ReportCountPoint{}, got)
						}
						for _, f := range []ReportFilter{{}, {Since: &since}, {Until: &until}} {
							got, err := s.GetCountSeries(context.Background(), 71, metric, grouping, f)
							require.ErrorIs(t, err, ErrReportCountBounds)
							require.Nil(t, got)
						}
					})
					t.Run("cancel_before_owner", func(t *testing.T) {
						ctx, cancel := context.WithCancel(context.Background())
						cancel()
						s := &ReportService{}
						calendarCountSetOwners(t, s, metric, func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
							t.Fatal("canceled request reached an owner")
							return nil, nil
						})
						got, err := s.GetCountSeries(ctx, 71, metric, grouping, filter)
						require.ErrorIs(t, err, context.Canceled)
						require.Nil(t, got)
					})
					t.Run("cancel_after_owner_discards_partial", func(t *testing.T) {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						s := &ReportService{}
						calendarCountSetOwners(t, s, metric, func(gotCtx context.Context, _ uint, gotGrouping domain.ReportCountGrouping, _ []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
							require.Equal(t, ctx, gotCtx)
							require.Equal(t, grouping, gotGrouping)
							cancel()
							return []domain.ReportCountPoint{{Value: 7}}, nil
						})
						got, err := s.GetCountSeries(ctx, 71, metric, grouping, filter)
						require.ErrorIs(t, err, context.Canceled)
						require.Nil(t, got)
					})
					t.Run("owner_failure_discards_partial", func(t *testing.T) {
						for _, tc := range []struct{ cause, want error }{{errors.New("private SQL driver details"), ErrReportCountUnavailable}, {repository.ErrReportCountStorageRepresentation, ErrReportCountStorageRepresentation}, {repository.ErrReportCountQueryRepresentation, ErrReportCountQueryRepresentation}} {
							s := &ReportService{}
							calendarCountSetOwners(t, s, metric, func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
								return []domain.ReportCountPoint{{Value: 7}}, tc.cause
							})
							got, err := s.GetCountSeries(context.Background(), 71, metric, grouping, filter)
							require.ErrorIs(t, err, tc.want)
							require.NotContains(t, err.Error(), "private")
							require.Nil(t, got)
						}
					})
				})
			}
		})
	}
	t.Run("invalid_grouping_and_metric_never_reach_owner", func(t *testing.T) {
		s := &ReportService{}
		calendarCountSetOwners(t, s, "conversations_count", func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
			t.Fatal("invalid grouping or metric reached an owner")
			return nil, nil
		})
		for _, grouping := range []domain.ReportCountGrouping{"", "fortnight", "DAY", "day' OR 1=1 --"} {
			got, err := s.GetCountSeries(context.Background(), 71, "conversations_count", grouping, filter)
			require.ErrorIs(t, err, ErrReportCountGrouping)
			require.Nil(t, got)
		}
		for _, grouping := range calendarCountGroupings {
			got, err := s.GetCountSeries(context.Background(), 71, "invalid_count", grouping, filter)
			require.ErrorIs(t, err, ErrReportCountMetric)
			require.Nil(t, got)
		}
	})
}
