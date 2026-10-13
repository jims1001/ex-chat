package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

// These fixtures describe civil times independently of the production bucket
// helper. Subtracting the fixed offset gives the expected absolute UTC instant.
func offsetCountUTC(local time.Time, offset int64) time.Time {
	return local.Add(-time.Duration(offset) * time.Second).UTC()
}

func TestReportCountOffsetServiceLabelsAndClipping(t *testing.T) {
	at := calendarCountAt
	cases := []struct {
		name     string
		grouping domain.ReportCountGrouping
		since    time.Time
		until    time.Time
		starts   []time.Time
	}{
		{"hour_leap_midnight", domain.ReportCountHour, at(2024, 2, 28, 23, 59, 59, 123456789), at(2024, 2, 29, 1, 35, 0, 987654321), []time.Time{at(2024, 2, 28, 23, 0, 0, 0), at(2024, 2, 29, 0, 0, 0, 0), at(2024, 2, 29, 1, 0, 0, 0), at(2024, 2, 29, 2, 0, 0, 0)}},
		{"day_leap_february", domain.ReportCountDay, at(2024, 2, 28, 12, 0, 0, 123456789), at(2024, 3, 1, 4, 0, 0, 987654321), []time.Time{at(2024, 2, 28, 0, 0, 0, 0), at(2024, 2, 29, 0, 0, 0, 0), at(2024, 3, 1, 0, 0, 0, 0), at(2024, 3, 2, 0, 0, 0, 0)}},
		{"month_leap_february", domain.ReportCountMonth, at(2024, 1, 31, 12, 0, 0, 123456789), at(2024, 3, 1, 12, 0, 0, 987654321), []time.Time{at(2024, 1, 1, 0, 0, 0, 0), at(2024, 2, 1, 0, 0, 0, 0), at(2024, 3, 1, 0, 0, 0, 0), at(2024, 4, 1, 0, 0, 0, 0)}},
		{"year_exact_exclusive_end", domain.ReportCountYear, at(2023, 12, 31, 23, 0, 0, 123456789), at(2025, 1, 1, 0, 0, 0, 0), []time.Time{at(2023, 1, 1, 0, 0, 0, 0), at(2024, 1, 1, 0, 0, 0, 0), at(2025, 1, 1, 0, 0, 0, 0)}},
		{"month_negative_unix", domain.ReportCountMonth, at(1969, 12, 31, 23, 59, 59, 123456789), at(1970, 2, 1, 0, 0, 0, 0), []time.Time{at(1969, 12, 1, 0, 0, 0, 0), at(1970, 1, 1, 0, 0, 0, 0), at(1970, 2, 1, 0, 0, 0, 0)}},
	}
	for _, offset := range []int64{19800, 20700, -12600} {
		for _, metric := range calendarCountMetrics {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%d/%s/%s", offset, metric, tc.name), func(t *testing.T) {
					want := make([]domain.ReportCountBucket, len(tc.starts)-1)
					for i := range want {
						first, next := offsetCountUTC(tc.starts[i], offset), offsetCountUTC(tc.starts[i+1], offset)
						want[i] = domain.ReportCountBucket{Timestamp: first.Unix(), Since: first, Until: next, OffsetSeconds: offset}
					}
					want[0].Since = offsetCountUTC(tc.since, offset)
					want[len(want)-1].Until = offsetCountUTC(tc.until, offset)
					since := want[0].Since.In(time.FixedZone("input-west", -7*3600))
					until := want[len(want)-1].Until.In(time.FixedZone("input-east", 9*3600))
					ctx := context.WithValue(context.Background(), struct{ offset bool }{true}, "offset-owner-context")
					calls := 0
					s := &ReportService{}
					// The owner returns only occupied buckets; service must not
					// invent zero points or relabel the sparse result.
					returned := []domain.ReportCountPoint{{Value: 7, Timestamp: want[len(want)-1].Timestamp}}
					calendarCountSetOwners(t, s, metric, func(gotCtx context.Context, account uint, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
						calls++
						require.Equal(t, ctx, gotCtx)
						require.Equal(t, uint(71), account)
						require.Equal(t, tc.grouping, grouping)
						require.Equal(t, want, buckets)
						return returned, nil
					})
					for _, business := range []bool{false, true} {
						got, err := s.GetCountSeriesWithOffset(ctx, 71, metric, tc.grouping, ReportFilter{Since: &since, Until: &until, BusinessHours: business}, offset)
						require.NoError(t, err)
						require.Equal(t, returned, got)
					}
					require.Equal(t, 2, calls)
				})
			}
		}
	}
}

func TestReportCountOffsetServiceZeroMatchesLegacy(t *testing.T) {
	for _, metric := range calendarCountMetrics {
		for _, grouping := range calendarCountGroupings {
			t.Run(metric+"/"+string(grouping), func(t *testing.T) {
				since := calendarCountAt(2024, 2, 28, 23, 59, 59, 123456789)
				until := calendarCountAt(2024, 3, 1, 4, 0, 0, 987654321)
				filter := ReportFilter{Since: &since, Until: &until}
				var captured [][]domain.ReportCountBucket
				s := &ReportService{}
				calendarCountSetOwners(t, s, metric, func(_ context.Context, _ uint, _ domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
					captured = append(captured, append([]domain.ReportCountBucket(nil), buckets...))
					for _, b := range buckets {
						require.Zero(t, b.OffsetSeconds)
					}
					return []domain.ReportCountPoint{{Value: 2, Timestamp: buckets[0].Timestamp}}, nil
				})
				legacy, err := s.GetCountSeries(context.Background(), 71, metric, grouping, filter)
				require.NoError(t, err)
				got, err := s.GetCountSeriesWithOffset(context.Background(), 71, metric, grouping, filter, 0)
				require.NoError(t, err)
				require.Equal(t, legacy, got)
				require.Len(t, captured, 2)
				require.Equal(t, captured[0], captured[1])
			})
		}
	}
}

func TestReportCountOffsetServiceWholeSecondsAndSafeMagnitude(t *testing.T) {
	const maxOffset = int64(math.MaxInt64) / int64(time.Second)
	// Numeric offsets are not constrained to real-world timezone ranges or
	// minute precision. The duration-safe endpoint itself remains valid.
	for _, offset := range []int64{1, -1, 19801, -12601, 90000, -90000, maxOffset, -maxOffset} {
		for _, grouping := range calendarCountGroupings {
			t.Run(fmt.Sprintf("%d/%s", offset, grouping), func(t *testing.T) {
				localFirst := calendarCountAt(2024, 1, 1, 0, 0, 0, 0)
				first := offsetCountUTC(localFirst, offset)
				since, until := first.Add(time.Nanosecond), first.Add(time.Second)
				s := &ReportService{}
				calls := 0
				calendarCountSetOwners(t, s, "conversations_count", func(_ context.Context, _ uint, gotGrouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
					calls++
					require.Equal(t, grouping, gotGrouping)
					require.Equal(t, []domain.ReportCountBucket{{Timestamp: first.Unix(), Since: since, Until: until, OffsetSeconds: offset}}, buckets)
					return nil, nil
				})
				got, err := s.GetCountSeriesWithOffset(context.Background(), 71, "conversations_count", grouping, ReportFilter{Since: &since, Until: &until}, offset)
				require.NoError(t, err)
				require.Equal(t, []domain.ReportCountPoint{}, got)
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestReportCountOffsetServiceSelectedBucketBudget(t *testing.T) {
	for _, offset := range []int64{19800, 20700, -12600} {
		for _, metric := range calendarCountMetrics {
			for _, grouping := range calendarCountGroupings {
				t.Run(fmt.Sprintf("%d/%s/%s", offset, metric, grouping), func(t *testing.T) {
					localFirst := calendarCountAt(2024, 1, 1, 0, 0, 0, 0)
					var localEnd time.Time
					switch grouping {
					case domain.ReportCountHour:
						localEnd = localFirst.Add(400 * time.Hour)
					case domain.ReportCountDay:
						localEnd = localFirst.AddDate(0, 0, 400)
					case domain.ReportCountMonth:
						localEnd = localFirst.AddDate(0, 400, 0)
					case domain.ReportCountYear:
						localEnd = localFirst.AddDate(400, 0, 0)
					}
					first := offsetCountUTC(localFirst, offset)
					since, until := first.Add(time.Nanosecond), offsetCountUTC(localEnd, offset)
					calls := 0
					s := &ReportService{}
					calendarCountSetOwners(t, s, metric, func(_ context.Context, _ uint, gotGrouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
						calls++
						require.Equal(t, grouping, gotGrouping)
						require.Len(t, buckets, 400)
						require.Equal(t, first.Unix(), buckets[0].Timestamp)
						require.Equal(t, since, buckets[0].Since)
						require.Equal(t, until, buckets[399].Until)
						for _, b := range buckets {
							require.Equal(t, offset, b.OffsetSeconds)
						}
						return nil, nil
					})
					got, err := s.GetCountSeriesWithOffset(context.Background(), 71, metric, grouping, ReportFilter{Since: &since, Until: &until}, offset)
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{}, got)
					until = until.Add(time.Nanosecond)
					for _, subject := range []*ReportService{s, {}} {
						got, err = subject.GetCountSeriesWithOffset(context.Background(), 71, metric, grouping, ReportFilter{Since: &since, Until: &until}, offset)
						require.ErrorIs(t, err, ErrReportCountBudget)
						require.Nil(t, got)
					}
					require.Equal(t, 1, calls)
				})
			}
		}
	}
}

func TestReportCountOffsetServiceInvalidArithmeticNeverReachesOwner(t *testing.T) {
	const maxOffset = int64(math.MaxInt64) / int64(time.Second)
	since := calendarCountAt(2024, 1, 1, 0, 0, 0, 0)
	until := since.Add(time.Second)
	for _, metric := range calendarCountMetrics {
		for _, grouping := range calendarCountGroupings {
			t.Run(metric+"/"+string(grouping), func(t *testing.T) {
				s := &ReportService{}
				calendarCountSetOwners(t, s, metric, func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
					t.Fatal("invalid arithmetic reached an owner")
					return nil, nil
				})
				for _, offset := range []int64{maxOffset + 1, -maxOffset - 1, math.MaxInt64, math.MinInt64} {
					got, err := s.GetCountSeriesWithOffset(context.Background(), 71, metric, grouping, ReportFilter{Since: &since, Until: &until}, offset)
					require.ErrorIs(t, err, ErrReportCountOffset, "offset %d", offset)
					require.Nil(t, got)
				}
				// time.Time can carry Unix seconds close to int64 limits,
				// but shifting/civil reconstruction there must not wrap into
				// a plausible bucket. Both shift directions are checked.
				for _, tc := range []struct {
					since, until time.Time
					offset       int64
				}{
					{time.Unix(math.MaxInt64-10, 0).UTC(), time.Unix(math.MaxInt64-9, 0).UTC(), 19800},
					{time.Unix(math.MinInt64+10, 0).UTC(), time.Unix(math.MinInt64+11, 0).UTC(), -12600},
				} {
					got, err := s.GetCountSeriesWithOffset(context.Background(), 71, metric, grouping, ReportFilter{Since: &tc.since, Until: &tc.until}, tc.offset)
					require.ErrorIs(t, err, ErrReportCountBounds)
					require.Nil(t, got)
				}
			})
		}
	}
}

func TestReportCountOffsetServiceCancellationAndFailurePrivacy(t *testing.T) {
	since := calendarCountAt(2024, 2, 29, 12, 0, 0, 123456789)
	until := since.Add(time.Minute)
	filter := ReportFilter{Since: &since, Until: &until}
	for _, metric := range calendarCountMetrics {
		t.Run(metric, func(t *testing.T) {
			t.Run("cancel_before_owner", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				s := &ReportService{}
				calendarCountSetOwners(t, s, metric, func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
					t.Fatal("canceled offset request reached owner")
					return nil, nil
				})
				got, err := s.GetCountSeriesWithOffset(ctx, 71, metric, domain.ReportCountDay, filter, 20700)
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, got)
			})
			t.Run("cancel_after_owner_discards_partial", func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s := &ReportService{}
				calendarCountSetOwners(t, s, metric, func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
					cancel()
					return []domain.ReportCountPoint{{Value: 7}}, nil
				})
				got, err := s.GetCountSeriesWithOffset(ctx, 71, metric, domain.ReportCountDay, filter, -12600)
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, got)
			})
			for _, tc := range []struct{ cause, want error }{{errors.New("private SQL driver details"), ErrReportCountUnavailable}, {repository.ErrReportCountStorageRepresentation, ErrReportCountStorageRepresentation}, {repository.ErrReportCountQueryRepresentation, ErrReportCountQueryRepresentation}} {
				s := &ReportService{}
				calendarCountSetOwners(t, s, metric, func(context.Context, uint, domain.ReportCountGrouping, []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
					return []domain.ReportCountPoint{{Value: 7}}, tc.cause
				})
				got, err := s.GetCountSeriesWithOffset(context.Background(), 71, metric, domain.ReportCountDay, filter, 19800)
				require.ErrorIs(t, err, tc.want)
				require.NotContains(t, err.Error(), "private")
				require.Nil(t, got)
			}
		})
	}
}

func TestReportCountOffsetServiceClipsExtremeCalendarYears(t *testing.T) {
	at := calendarCountAt
	for _, tc := range []struct {
		name                     string
		grouping                 domain.ReportCountGrouping
		offset                   int64
		localFirst, since, until time.Time
	}{
		{"year_one_day_label_in_year_zero", domain.ReportCountDay, 20700, at(1, 1, 1, 0, 0, 0, 0), at(1, 1, 1, 0, 0, 0, 123456789), at(1, 1, 1, 0, 1, 0, 987654321)},
		{"year_one_month_label_in_year_zero", domain.ReportCountMonth, 20700, at(1, 1, 1, 0, 0, 0, 0), at(1, 1, 1, 0, 0, 0, 123456789), at(1, 1, 1, 0, 1, 0, 987654321)},
		{"year_one_negative_offset_local_year_zero", domain.ReportCountYear, -12600, at(0, 1, 1, 0, 0, 0, 0), at(1, 1, 1, 0, 0, 0, 123456789), at(1, 1, 1, 0, 1, 0, 987654321)},
		{"year_9999_positive_offset_local_year_10000", domain.ReportCountYear, 20700, at(10000, 1, 1, 0, 0, 0, 0), at(9999, 12, 31, 23, 50, 0, 123456789), at(9999, 12, 31, 23, 51, 0, 987654321)},
		{"year_9999_nominal_next_in_year_10000", domain.ReportCountYear, -12600, at(9999, 1, 1, 0, 0, 0, 0), at(9999, 12, 31, 23, 50, 0, 123456789), at(9999, 12, 31, 23, 51, 0, 987654321)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ReportService{}
			calls := 0
			calendarCountSetOwners(t, s, "conversations_count", func(_ context.Context, _ uint, _ domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
				calls++
				require.Equal(t, []domain.ReportCountBucket{{Timestamp: offsetCountUTC(tc.localFirst, tc.offset).Unix(), Since: tc.since, Until: tc.until, OffsetSeconds: tc.offset}}, buckets)
				return nil, nil
			})
			got, err := s.GetCountSeriesWithOffset(context.Background(), 71, "conversations_count", tc.grouping, ReportFilter{Since: &tc.since, Until: &tc.until}, tc.offset)
			require.NoError(t, err)
			require.Equal(t, []domain.ReportCountPoint{}, got)
			require.Equal(t, 1, calls)
		})
	}
}

func TestReportCountOffsetServiceRejectsUnsafeEmptyOrWrappedEnd(t *testing.T) {
	s := &ReportService{}
	ordinary := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	invalid := time.Unix(math.MaxInt64, 0).UTC()
	for _, bounds := range [][2]time.Time{{ordinary, invalid}, {invalid, invalid}, {invalid, ordinary}} {
		since, until := bounds[0], bounds[1]
		points, err := s.GetCountSeriesWithOffset(context.Background(), 71, "conversations_count", domain.ReportCountDay, ReportFilter{Since: &since, Until: &until}, 0)
		require.ErrorIs(t, err, ErrReportCountBounds)
		require.Nil(t, points)
	}
}
