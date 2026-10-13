package repository

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Keep expected bucket construction independent of the production helper:
// fixtures are civil UTC-shaped calendars shifted to absolute UTC seconds.
func reportCountOffsetBuckets(localFirst time.Time, grouping domain.ReportCountGrouping, count int, offset int64) []domain.ReportCountBucket {
	buckets := make([]domain.ReportCountBucket, count)
	for i := range buckets {
		localNext := reportCountCalendarNext(localFirst, grouping)
		first := localFirst.Add(-time.Duration(offset) * time.Second).UTC()
		next := localNext.Add(-time.Duration(offset) * time.Second).UTC()
		buckets[i] = domain.ReportCountBucket{Timestamp: first.Unix(), Since: first, Until: next, OffsetSeconds: offset}
		localFirst = localNext
	}
	return buckets
}

func TestReportCountOffsetSQLite(t *testing.T) {
	runReportCountOffset(t, reportCountTestSQLite)
}

func TestPostgresReportCountOffset(t *testing.T) {
	runReportCountOffset(t, reportCountTestPostgres(t))
}

func runReportCountOffset(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, owner := range reportCountTestOwners {
		for _, group := range reportCountCalendarGroupings {
			for _, offset := range []int64{19800, 20700, -12600} {
				t.Run(fmt.Sprintf("%s/%s/%d", owner.name, group.name, offset), func(t *testing.T) {
					t.Run("sparse_exact_type_tenant_and_fractional_half_open", func(t *testing.T) {
						db := open(t, owner.name)
						reportCountTestTable(t, db, owner)
						buckets := reportCountOffsetBuckets(group.first, group.grouping, 4, offset)
						buckets[0].Since = buckets[0].Since.Add(20*time.Minute + 123456001*time.Nanosecond)
						buckets[3].Until = buckets[3].Since.Add(20*time.Minute + 987654001*time.Nanosecond)
						// Both dialects store these microsecond-grid instants
						// exactly, while request bounds retain nanosecond precision.
						instants := []time.Time{
							buckets[3].Until.Truncate(time.Microsecond),
							buckets[1].Since,
							buckets[0].Since.Truncate(time.Microsecond).Add(time.Microsecond),
							buckets[1].Until.Add(-time.Microsecond),
							buckets[0].Since.Truncate(time.Microsecond),
							buckets[3].Until.Truncate(time.Microsecond).Add(time.Microsecond),
						}
						for i, at := range instants {
							reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
							reportCountTestInsert(t, db, owner, uint(i+101), 92, at, owner.messageType)
						}
						if owner.table == "messages" {
							for i, other := range []string{"activity", "template", "Incoming", "incoming ", "Outgoing", "outgoing ", "incoming' OR 1=1 --"} {
								reportCountTestInsert(t, db, owner, uint(i+201), 91, buckets[1].Since, other)
							}
							other := domain.MessageTypeIncoming
							if other == owner.messageType {
								other = domain.MessageTypeOutgoing
							}
							reportCountTestInsert(t, db, owner, 250, 91, buckets[1].Since, other)
						}
						got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, buckets)
						require.NoError(t, err)
						require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[0].Timestamp}, {Value: 2, Timestamp: buckets[1].Timestamp}, {Value: 1, Timestamp: buckets[3].Timestamp}}, got)
						got, err = owner.countCalendar(db, context.Background(), 999, group.grouping, buckets)
						require.NoError(t, err)
						require.Equal(t, []domain.ReportCountPoint{}, got)
					})
					t.Run("400_buckets_one_statement_bound_parameters_401_no_query", func(t *testing.T) {
						db := open(t, owner.name)
						reportCountTestTable(t, db, owner)
						buckets := reportCountOffsetBuckets(group.first, group.grouping, 401, offset)
						reportCountTestInsert(t, db, owner, 1, 91, buckets[200].Since, owner.messageType)
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						queries := 0
						require.NoError(t, db.Callback().Row().After("gorm:row").Register("offset_count_query", func(tx *gorm.DB) {
							queries++
							require.Equal(t, ctx, tx.Statement.Context)
							sql := tx.Statement.SQL.String()
							require.Contains(t, sql, "WITH report_count_buckets")
							require.Contains(t, sql, "COUNT(*)")
							require.Contains(t, sql, "GROUP BY")
							require.Contains(t, sql, "ORDER BY")
							for _, forbidden := range []string{"select *", "content", "metadata", "date(", "datetime(", "strftime(", "date_trunc(", "time_bucket(", "at time zone", "offset_seconds"} {
								require.NotContains(t, strings.ToLower(sql), forbidden)
							}
							wantVars := 801
							if owner.table == "messages" {
								wantVars++
								require.NotContains(t, sql, "conversations")
								require.Equal(t, owner.messageType, tx.Statement.Vars[801])
							} else {
								require.NotContains(t, sql, "messages")
							}
							require.Len(t, tx.Statement.Vars, wantVars)
							require.Less(t, len(tx.Statement.Vars), 999)
							require.Equal(t, uint(91), tx.Statement.Vars[800])
							for i, bucket := range buckets[:400] {
								require.Equal(t, ReportTimeBoundary(db, bucket.Since), tx.Statement.Vars[2*i])
								require.Equal(t, ReportTimeBoundary(db, bucket.Until), tx.Statement.Vars[2*i+1])
							}
							if db.Dialector.Name() == "postgres" {
								require.Equal(t, 800, strings.Count(sql, "AS timestamptz)"))
							}
						}))
						got, err := owner.countCalendar(db, ctx, 91, group.grouping, buckets[:400])
						require.NoError(t, err)
						require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[200].Timestamp}}, got)
						require.Equal(t, 1, queries)
						got, err = owner.countCalendar(db, ctx, 91, group.grouping, buckets)
						require.EqualError(t, err, "report counts are unavailable")
						require.Nil(t, got)
						require.Equal(t, 1, queries)
						cancel()
						got, err = owner.countCalendar(db, ctx, 91, group.grouping, buckets[:1])
						require.ErrorIs(t, err, context.Canceled)
						require.Nil(t, got)
						require.Equal(t, 1, queries)
					})
				})
			}
		}
	}
}

func TestReportCountOffsetSQLiteInvalidBuckets(t *testing.T) {
	runReportCountOffsetInvalidBuckets(t, reportCountTestSQLite)
}

func TestPostgresReportCountOffsetInvalidBuckets(t *testing.T) {
	runReportCountOffsetInvalidBuckets(t, reportCountTestPostgres(t))
}

func runReportCountOffsetInvalidBuckets(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	const maxOffset = int64(math.MaxInt64) / int64(time.Second)
	for _, owner := range reportCountTestOwners {
		for _, group := range reportCountCalendarGroupings {
			t.Run(owner.name+"/"+group.name, func(t *testing.T) {
				db := open(t, owner.name)
				queries := 0
				require.NoError(t, db.Callback().Row().After("gorm:row").Register("offset_invalid_no_query", func(*gorm.DB) { queries++ }))
				valid := reportCountOffsetBuckets(group.first, group.grouping, 3, 20700)
				change := func(edit func(*domain.ReportCountBucket)) []domain.ReportCountBucket {
					b := valid[0]
					edit(&b)
					return []domain.ReportCountBucket{b}
				}
				// Each bucket in mixed_hour_offsets is independently aligned
				// for hour/day/month/year; equality of offsets needs its own
				// validation rather than relying on calendar misalignment.
				mixed := append([]domain.ReportCountBucket(nil), valid[:2]...)
				mixed[1].OffsetSeconds += 3600
				mixed[1].Timestamp -= 3600
				mixed[1].Since = mixed[1].Since.Add(-time.Hour)
				mixed[1].Until = mixed[1].Until.Add(-time.Hour)
				mixed[0].Until = mixed[0].Until.Add(-2 * time.Hour)
				if group.grouping == domain.ReportCountHour {
					// Avoid overlap and preserve a nonempty first period.
					mixed = []domain.ReportCountBucket{valid[0], valid[2]}
					mixed[1].OffsetSeconds += 3600
				}
				cases := []struct {
					name string
					b    []domain.ReportCountBucket
				}{
					{"mixed_offsets", mixed},
					{"mixed_zero_offset", []domain.ReportCountBucket{valid[0], reportCountCalendarBuckets(reportCountCalendarNext(reportCountCalendarNext(group.first, group.grouping), group.grouping), group.grouping, 1)[0]}},
					{"unshifted_utc_label", change(func(b *domain.ReportCountBucket) { b.Timestamp = group.first.Unix() })},
					{"label_one_second", change(func(b *domain.ReportCountBucket) { b.Timestamp++ })},
					{"label_one_minute", change(func(b *domain.ReportCountBucket) { b.Timestamp += 60 })},
					{"since_before_local_period", change(func(b *domain.ReportCountBucket) { b.Since = b.Since.Add(-time.Nanosecond) })},
					{"until_after_local_period", change(func(b *domain.ReportCountBucket) { b.Until = b.Until.Add(time.Nanosecond) })},
					{"empty", change(func(b *domain.ReportCountBucket) { b.Until = b.Since })},
					{"reversed", change(func(b *domain.ReportCountBucket) { b.Since = b.Until.Add(time.Nanosecond) })},
					{"duplicate", []domain.ReportCountBucket{valid[0], valid[0]}},
					{"descending", []domain.ReportCountBucket{valid[1], valid[0]}},
					{"overlap", []domain.ReportCountBucket{valid[0], {Timestamp: valid[1].Timestamp, Since: valid[0].Until.Add(-time.Nanosecond), Until: valid[1].Until, OffsetSeconds: 20700}}},
					{"offset_too_positive", change(func(b *domain.ReportCountBucket) { b.OffsetSeconds = maxOffset + 1 })},
					{"offset_too_negative", change(func(b *domain.ReportCountBucket) { b.OffsetSeconds = -maxOffset - 1 })},
					{"offset_max_int64", change(func(b *domain.ReportCountBucket) { b.OffsetSeconds = math.MaxInt64 })},
					{"offset_min_int64", change(func(b *domain.ReportCountBucket) { b.OffsetSeconds = math.MinInt64 })},
					{"unrepresentable_positive_label", change(func(b *domain.ReportCountBucket) { b.Timestamp = math.MaxInt64 })},
					{"unrepresentable_negative_label", change(func(b *domain.ReportCountBucket) { b.Timestamp = math.MinInt64 })},
				}
				for _, tc := range cases {
					t.Run(tc.name, func(t *testing.T) {
						got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, tc.b)
						require.EqualError(t, err, "report counts are unavailable")
						require.Nil(t, got)
						require.Zero(t, queries)
					})
				}
			})
		}
	}
}

// This reference is used only with modest fixture dates. It deliberately uses
// no domain calendar helper and never asks SQL to interpret a timezone.
func reportCountOffsetInterval(since, until time.Time, grouping domain.ReportCountGrouping, offset int64) []domain.ReportCountBucket {
	local := since.UTC().Add(time.Duration(offset) * time.Second)
	first := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	switch grouping {
	case domain.ReportCountHour:
		first = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, time.UTC)
	case domain.ReportCountMonth:
		first = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
	case domain.ReportCountYear:
		first = time.Date(local.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	}
	var buckets []domain.ReportCountBucket
	for first.Add(-time.Duration(offset) * time.Second).Before(until) {
		b := reportCountOffsetBuckets(first, grouping, 1, offset)[0]
		if b.Since.Before(since) {
			b.Since = since.UTC()
		}
		if b.Until.After(until) {
			b.Until = until.UTC()
		}
		buckets = append(buckets, b)
		first = reportCountCalendarNext(first, grouping)
	}
	return buckets
}

func TestReportCountOffsetSQLiteSelectionConservation(t *testing.T) {
	runReportCountOffsetSelectionConservation(t, reportCountTestSQLite)
}

func TestPostgresReportCountOffsetSelectionConservation(t *testing.T) {
	runReportCountOffsetSelectionConservation(t, reportCountTestPostgres(t))
}

func runReportCountOffsetSelectionConservation(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	const maxOffset = int64(math.MaxInt64) / int64(time.Second)
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := open(t, owner.name)
			reportCountTestTable(t, db, owner)
			since := time.Date(2023, 12, 31, 10, 0, 0, 123456001, time.UTC)
			until := time.Date(2024, 1, 2, 6, 0, 0, 987654001, time.UTC)
			instants := []time.Time{
				since.Truncate(time.Microsecond),
				since.Truncate(time.Microsecond).Add(time.Microsecond),
				time.Date(2023, 12, 31, 18, 14, 59, 999999000, time.UTC),
				time.Date(2023, 12, 31, 18, 15, 0, 0, time.UTC),
				time.Date(2023, 12, 31, 18, 29, 59, 999999000, time.UTC),
				time.Date(2023, 12, 31, 18, 30, 0, 0, time.UTC),
				time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2024, 1, 1, 3, 29, 59, 999999000, time.UTC),
				time.Date(2024, 1, 1, 3, 30, 0, 0, time.UTC),
				until.Truncate(time.Microsecond),
				until.Truncate(time.Microsecond).Add(time.Microsecond),
			}
			for i, at := range instants {
				reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
				reportCountTestInsert(t, db, owner, uint(i+101), 92, at, owner.messageType)
			}
			if owner.table == "messages" {
				for i, other := range []string{"activity", "template", "Incoming", "Outgoing"} {
					reportCountTestInsert(t, db, owner, uint(i+201), 91, instants[5], other)
				}
			}
			for _, grouping := range []domain.ReportCountGrouping{domain.ReportCountHour, domain.ReportCountDay, domain.ReportCountMonth, domain.ReportCountYear} {
				for _, offset := range []int64{0, 19800, 20700, -12600, 9, -9, 90000, -90000, maxOffset, -maxOffset} {
					t.Run(fmt.Sprintf("%s/%d", grouping, offset), func(t *testing.T) {
						buckets := reportCountOffsetInterval(since, until, grouping, offset)
						var want []domain.ReportCountPoint
						for _, b := range buckets {
							var count int64
							for _, at := range instants {
								if !at.Before(b.Since) && at.Before(b.Until) {
									count++
								}
							}
							if count != 0 {
								want = append(want, domain.ReportCountPoint{Value: count, Timestamp: b.Timestamp})
							}
						}
						got, err := owner.countCalendar(db, context.Background(), 91, grouping, buckets)
						require.NoError(t, err)
						require.Equal(t, want, got)
						var total int64
						for _, p := range got {
							total += p.Value
						}
						require.Equal(t, int64(9), total, "only distribution and labels may change with offset")
					})
				}
			}
		})
	}
}

func TestReportCountOffsetSQLiteNanosecondEndpoints(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		for _, group := range reportCountCalendarGroupings {
			for _, offset := range []int64{19800, 20700, -12600, 9} {
				t.Run(fmt.Sprintf("%s/%s/%d", owner.name, group.name, offset), func(t *testing.T) {
					db := reportCountTestSQLite(t, owner.name)
					reportCountTestTable(t, db, owner)
					b := reportCountOffsetBuckets(group.first, group.grouping, 1, offset)[0]
					b.Since = b.Since.Add(15*time.Minute + 123456789*time.Nanosecond)
					b.Until = b.Since.Add(10 * time.Minute)
					for i, at := range []time.Time{b.Since.Add(-time.Nanosecond), b.Since, b.Until.Add(-time.Nanosecond), b.Until} {
						reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
					}
					got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, []domain.ReportCountBucket{b})
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{{Value: 2, Timestamp: b.Timestamp}}, got)
				})
			}
		}
	}
}

func TestReportCountOffsetSQLiteActualUTCRepresentation(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			for _, group := range reportCountCalendarGroupings {
				t.Run(group.name, func(t *testing.T) {
					db := reportCountTestSQLite(t, owner.name)
					queries := 0
					require.NoError(t, db.Callback().Row().After("gorm:row").Register("offset_query_representation", func(*gorm.DB) { queries++ }))
					b := reportCountOffsetBuckets(group.first, group.grouping, 1, 20700)[0]
					for _, endpoint := range []string{"since", "until"} {
						x := b
						if endpoint == "since" {
							x.Since = x.Since.In(time.FixedZone("local", 20700))
						} else {
							x.Until = x.Until.In(time.FixedZone("local", 20700))
						}
						got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, []domain.ReportCountBucket{x})
						require.ErrorIs(t, err, ErrReportCountQueryRepresentation)
						require.NotErrorIs(t, err, ErrReportCountStorageRepresentation)
						require.Nil(t, got)
						require.Zero(t, queries)
					}
				})
			}
			t.Run("nominal_label_in_year_zero_actual_bounds_in_year_one", func(t *testing.T) {
				db := reportCountTestSQLite(t, owner.name)
				reportCountTestTable(t, db, owner)
				since := time.Date(1, 1, 1, 0, 0, 0, 123456789, time.UTC)
				until := since.Add(time.Second)
				reportCountTestInsert(t, db, owner, 1, 91, since, owner.messageType)
				for _, grouping := range []domain.ReportCountGrouping{domain.ReportCountHour, domain.ReportCountDay, domain.ReportCountMonth, domain.ReportCountYear} {
					buckets := reportCountOffsetInterval(since, until, grouping, 20700)
					got, err := owner.countCalendar(db, context.Background(), 91, grouping, buckets)
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[0].Timestamp}}, got)
				}
			})
			t.Run("nominal_next_in_year_10000_actual_bounds_in_year_9999", func(t *testing.T) {
				db := reportCountTestSQLite(t, owner.name)
				reportCountTestTable(t, db, owner)
				since := time.Date(9999, 12, 31, 23, 50, 0, 123456789, time.UTC)
				until := since.Add(time.Minute)
				reportCountTestInsert(t, db, owner, 1, 91, since, owner.messageType)
				for _, offset := range []int64{20700, -12600} {
					for _, grouping := range []domain.ReportCountGrouping{domain.ReportCountHour, domain.ReportCountDay, domain.ReportCountMonth, domain.ReportCountYear} {
						buckets := reportCountOffsetInterval(since, until, grouping, offset)
						got, err := owner.countCalendar(db, context.Background(), 91, grouping, buckets)
						require.NoError(t, err)
						require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[0].Timestamp}}, got)
					}
				}
			})
			t.Run("storage_guard_still_checks_selected_account_type", func(t *testing.T) {
				db := reportCountTestSQLite(t, owner.name)
				reportCountTestTable(t, db, owner)
				first := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
				buckets := reportCountOffsetBuckets(first, domain.ReportCountYear, 1, 20700)
				reportCountTestInsert(t, db, owner, 1, 91, first, owner.messageType)
				reportCountTestRawTimestamp(t, db, owner, 2, 92, "private other tenant timestamp", owner.messageType)
				if owner.table == "messages" {
					reportCountTestRawTimestamp(t, db, owner, 3, 91, "private other type timestamp", "activity")
				}
				got, err := owner.countCalendar(db, context.Background(), 91, domain.ReportCountYear, buckets)
				require.NoError(t, err)
				require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[0].Timestamp}}, got)
				reportCountTestRawTimestamp(t, db, owner, 4, 91, "private selected timestamp", owner.messageType)
				got, err = owner.countCalendar(db, context.Background(), 91, domain.ReportCountYear, buckets)
				require.ErrorIs(t, err, ErrReportCountStorageRepresentation)
				require.NotContains(t, err.Error(), "private")
				require.Nil(t, got)
			})
		})
	}
}

func TestPostgresReportCountOffsetBroadYearMicroseconds(t *testing.T) {
	open := reportCountTestPostgres(t)
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := open(t, owner.name)
			reportCountTestTable(t, db, owner)
			first := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
			since := first.Add(123456001 * time.Nanosecond)
			until := first.Add(987654001 * time.Nanosecond)
			for i, at := range []time.Time{first.Add(123456 * time.Microsecond), first.Add(123457 * time.Microsecond), first.Add(987654 * time.Microsecond), first.Add(987655 * time.Microsecond)} {
				reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
			}
			for _, group := range reportCountCalendarGroupings {
				for _, offset := range []int64{19800, 20700, -12600, 9} {
					buckets := reportCountOffsetInterval(since, until, group.grouping, offset)
					got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, buckets)
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{{Value: 2, Timestamp: buckets[0].Timestamp}}, got)
				}
			}
		})
	}
}

func TestReportCountOffsetSQLiteRejectsActualOutOfRangeYears(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := reportCountTestSQLite(t, owner.name)
			queries := 0
			require.NoError(t, db.Callback().Row().After("gorm:row").Register("offset_actual_year_no_query", func(*gorm.DB) { queries++ }))
			for _, year := range []int{0, 10000} {
				for _, group := range reportCountCalendarGroupings {
					for _, offset := range []int64{20700, -12600} {
						since := time.Date(year, 1, 1, 12, 0, 0, 123456789, time.UTC)
						until := since.Add(time.Minute)
						buckets := reportCountOffsetInterval(since, until, group.grouping, offset)
						got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, buckets)
						require.ErrorIs(t, err, ErrReportCountQueryRepresentation)
						require.NotErrorIs(t, err, ErrReportCountStorageRepresentation)
						require.Nil(t, got)
						require.Zero(t, queries)
					}
				}
			}
		})
	}
}
