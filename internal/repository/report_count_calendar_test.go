package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var reportCountCalendarGroupings = []struct {
	name     string
	grouping domain.ReportCountGrouping
	first    time.Time
}{
	{"hour", domain.ReportCountHour, time.Date(2024, 2, 29, 22, 0, 0, 0, time.UTC)},
	{"day", domain.ReportCountDay, time.Date(2024, 2, 28, 0, 0, 0, 0, time.UTC)},
	{"month", domain.ReportCountMonth, time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC)},
	{"year", domain.ReportCountYear, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)},
}

func reportCountCalendarNext(at time.Time, grouping domain.ReportCountGrouping) time.Time {
	switch grouping {
	case domain.ReportCountHour:
		return at.Add(time.Hour)
	case domain.ReportCountDay:
		return at.AddDate(0, 0, 1)
	case domain.ReportCountMonth:
		return at.AddDate(0, 1, 0)
	case domain.ReportCountYear:
		return at.AddDate(1, 0, 0)
	default:
		panic("invalid test grouping")
	}
}

func reportCountCalendarBuckets(first time.Time, grouping domain.ReportCountGrouping, count int) []domain.ReportCountBucket {
	buckets := make([]domain.ReportCountBucket, count)
	for i := range buckets {
		next := reportCountCalendarNext(first, grouping)
		buckets[i] = domain.ReportCountBucket{Timestamp: first.Unix(), Since: first, Until: next}
		first = next
	}
	return buckets
}

func (owner reportCountTestOwner) countCalendar(db *gorm.DB, ctx context.Context, account uint, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	if owner.table == "conversations" {
		return NewConversationRepository(db).CountReportBuckets(ctx, account, grouping, buckets)
	}
	return NewMessageRepositoryWithConversationPort(db, nil).CountReportBuckets(ctx, account, owner.messageType, grouping, buckets)
}

func TestReportCountCalendarSQLite(t *testing.T) {
	runReportCountCalendar(t, reportCountTestSQLite)
}

func TestPostgresReportCountCalendar(t *testing.T) {
	runReportCountCalendar(t, reportCountTestPostgres(t))
}

// Each aggregate is exercised against the real dialect and an owner-local
// minimal schema. No conversation association or message payload is available.
func runReportCountCalendar(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			for _, group := range reportCountCalendarGroupings {
				t.Run(group.name, func(t *testing.T) {
					t.Run("clipped_fractional_half_open_sparse_exact_type_and_tenant", func(t *testing.T) {
						db := open(t, owner.name)
						reportCountTestTable(t, db, owner)
						buckets := reportCountCalendarBuckets(group.first, group.grouping, 4)
						buckets[0].Since = group.first.Add(20*time.Minute + 123456001*time.Nanosecond)
						buckets[3].Until = buckets[3].Since.Add(20*time.Minute + 987654001*time.Nanosecond)
						// Stored microsecond-grid neighbors make the same exact
						// endpoint expectations valid on both databases.
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
							other := domain.MessageTypeOutgoing
							if other == owner.messageType {
								other = domain.MessageTypeIncoming
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
					t.Run("400_parameterized_buckets_one_query_401_and_cancel_no_query", func(t *testing.T) {
						db := open(t, owner.name)
						reportCountTestTable(t, db, owner)
						buckets := reportCountCalendarBuckets(group.first, group.grouping, 401)
						reportCountTestInsert(t, db, owner, 1, 91, buckets[200].Since, owner.messageType)
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						queries := 0
						require.NoError(t, db.Callback().Row().After("gorm:row").Register("calendar_count_query", func(tx *gorm.DB) {
							queries++
							require.Equal(t, ctx, tx.Statement.Context)
							sql := tx.Statement.SQL.String()
							require.Contains(t, sql, "WITH report_count_buckets")
							require.Contains(t, sql, "COUNT(*)")
							require.Contains(t, sql, "GROUP BY")
							require.Contains(t, sql, "ORDER BY")
							for _, forbidden := range []string{"select *", "content", "metadata", "date(", "datetime(", "strftime(", "date_trunc(", "time_bucket("} {
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
							require.NotContains(t, sql, "2024-02-29")
						}))
						got, err := owner.countCalendar(db, ctx, 91, group.grouping, buckets[:400])
						require.NoError(t, err)
						require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[200].Timestamp}}, got)
						require.Equal(t, 1, queries)
						got, err = owner.countCalendar(db, ctx, 91, group.grouping, buckets)
						require.Error(t, err)
						require.Nil(t, got)
						require.Equal(t, 1, queries)
						got, err = owner.countCalendar(db, ctx, 91, group.grouping, nil)
						require.NoError(t, err)
						require.Equal(t, []domain.ReportCountPoint{}, got)
						require.Equal(t, 1, queries)
						cancel()
						got, err = owner.countCalendar(db, ctx, 91, group.grouping, buckets[:1])
						require.ErrorIs(t, err, context.Canceled)
						require.Nil(t, got)
						require.Equal(t, 1, queries)
					})
					t.Run("invalid_bucket_shapes_never_query", func(t *testing.T) {
						db := open(t, owner.name)
						queries := 0
						require.NoError(t, db.Callback().Row().After("gorm:row").Register("calendar_invalid_no_query", func(*gorm.DB) { queries++ }))
						valid := reportCountCalendarBuckets(group.first, group.grouping, 2)
						change := func(edit func(*domain.ReportCountBucket)) []domain.ReportCountBucket {
							b := valid[0]
							edit(&b)
							return []domain.ReportCountBucket{b}
						}
						cases := []struct {
							name string
							b    []domain.ReportCountBucket
						}{
							{"label_second", change(func(b *domain.ReportCountBucket) { b.Timestamp++ })},
							{"label_minute", change(func(b *domain.ReportCountBucket) { b.Timestamp += 60 })},
							{"since_before_period", change(func(b *domain.ReportCountBucket) { b.Since = b.Since.Add(-time.Nanosecond) })},
							{"until_after_period", change(func(b *domain.ReportCountBucket) { b.Until = b.Until.Add(time.Nanosecond) })},
							{"empty", change(func(b *domain.ReportCountBucket) { b.Until = b.Since })},
							{"reversed", change(func(b *domain.ReportCountBucket) { b.Since = b.Until.Add(time.Nanosecond) })},
							{"duplicate_label", []domain.ReportCountBucket{valid[0], valid[0]}},
							{"descending", []domain.ReportCountBucket{valid[1], valid[0]}},
							{"overlap", []domain.ReportCountBucket{valid[0], {Timestamp: valid[1].Timestamp, Since: valid[0].Until.Add(-time.Nanosecond), Until: valid[1].Until}}},
						}
						if group.grouping != domain.ReportCountHour {
							cases = append(cases, struct {
								name string
								b    []domain.ReportCountBucket
							}{"label_hour", change(func(b *domain.ReportCountBucket) { b.Timestamp += 3600 })})
						}
						if group.grouping == domain.ReportCountMonth || group.grouping == domain.ReportCountYear {
							cases = append(cases, struct {
								name string
								b    []domain.ReportCountBucket
							}{"label_day", change(func(b *domain.ReportCountBucket) { b.Timestamp += 86400 })})
						}
						if group.grouping == domain.ReportCountYear {
							cases = append(cases, struct {
								name string
								b    []domain.ReportCountBucket
							}{"label_month", change(func(b *domain.ReportCountBucket) { b.Timestamp = group.first.AddDate(0, 1, 0).Unix() })})
						}
						for _, tc := range cases {
							t.Run(tc.name, func(t *testing.T) {
								got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, tc.b)
								require.Error(t, err)
								require.Nil(t, got)
								require.Zero(t, queries)
							})
						}
						for _, grouping := range []domain.ReportCountGrouping{"", "fortnight", "DAY", "day' OR 1=1 --"} {
							got, err := owner.countCalendar(db, context.Background(), 91, grouping, valid[:1])
							require.Error(t, err)
							require.Nil(t, got)
							require.Zero(t, queries)
						}
					})
				})
			}
		})
	}
}

func TestReportCountCalendarSQLiteNanosecondBounds(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			for _, group := range reportCountCalendarGroupings {
				t.Run(group.name, func(t *testing.T) {
					db := reportCountTestSQLite(t, owner.name)
					reportCountTestTable(t, db, owner)
					since := group.first.Add(15*time.Minute + 123456789*time.Nanosecond)
					until := since.Add(10 * time.Minute)
					for i, at := range []time.Time{since.Add(-time.Nanosecond), since, until.Add(-time.Nanosecond), until} {
						reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
					}
					bucket := domain.ReportCountBucket{Timestamp: group.first.Unix(), Since: since, Until: until}
					got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, []domain.ReportCountBucket{bucket})
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{{Value: 2, Timestamp: bucket.Timestamp}}, got)
				})
			}
		})
	}
}

func TestReportCountCalendarSQLiteQueryRepresentation(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			for _, group := range reportCountCalendarGroupings {
				t.Run(group.name, func(t *testing.T) {
					db := reportCountTestSQLite(t, owner.name)
					queries := 0
					require.NoError(t, db.Callback().Row().After("gorm:row").Register("calendar_query_representation", func(*gorm.DB) { queries++ }))
					b := reportCountCalendarBuckets(group.first, group.grouping, 1)[0]
					for _, endpoint := range []string{"since", "until"} {
						t.Run("non_utc_"+endpoint, func(t *testing.T) {
							x := b
							zone := time.FixedZone("offset", 9*3600)
							if endpoint == "since" {
								x.Since = x.Since.In(zone)
							} else {
								x.Until = x.Until.In(zone)
							}
							got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, []domain.ReportCountBucket{x})
							require.ErrorIs(t, err, ErrReportCountQueryRepresentation)
							require.NotErrorIs(t, err, ErrReportCountStorageRepresentation)
							require.Nil(t, got)
							require.Zero(t, queries)
						})
					}
					for _, year := range []int{-1, 0, 10000} {
						first := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
						got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, reportCountCalendarBuckets(first, group.grouping, 1))
						require.ErrorIs(t, err, ErrReportCountQueryRepresentation)
						require.NotErrorIs(t, err, ErrReportCountStorageRepresentation)
						require.Nil(t, got)
						require.Zero(t, queries)
					}
				})
			}
			t.Run("year_0001_actual_bounds", func(t *testing.T) {
				db := reportCountTestSQLite(t, owner.name)
				reportCountTestTable(t, db, owner)
				first := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
				reportCountTestInsert(t, db, owner, 1, 91, first.Add(time.Nanosecond), owner.messageType)
				for _, group := range reportCountCalendarGroupings {
					got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, reportCountCalendarBuckets(first, group.grouping, 1))
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: first.Unix()}}, got)
				}
			})
			t.Run("clipped_year_9999_only_checks_actual_bounds", func(t *testing.T) {
				db := reportCountTestSQLite(t, owner.name)
				reportCountTestTable(t, db, owner)
				first := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
				since := time.Date(9999, 12, 30, 0, 0, 0, 0, time.FixedZone("zero", 0))
				until := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
				reportCountTestInsert(t, db, owner, 1, 91, since.UTC(), owner.messageType)
				b := domain.ReportCountBucket{Timestamp: first.Unix(), Since: since, Until: until}
				got, err := owner.countCalendar(db, context.Background(), 91, domain.ReportCountYear, []domain.ReportCountBucket{b})
				require.NoError(t, err)
				require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: first.Unix()}}, got)
				b.Until = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
				got, err = owner.countCalendar(db, context.Background(), 91, domain.ReportCountYear, []domain.ReportCountBucket{b})
				require.ErrorIs(t, err, ErrReportCountQueryRepresentation)
				require.Nil(t, got)
			})
			t.Run("stored_representation_failure_remains_distinct", func(t *testing.T) {
				db := reportCountTestSQLite(t, owner.name)
				reportCountTestTable(t, db, owner)
				reportCountTestRawTimestamp(t, db, owner, 1, 91, "invalid private storage value", owner.messageType)
				first := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
				got, err := owner.countCalendar(db, context.Background(), 91, domain.ReportCountYear, reportCountCalendarBuckets(first, domain.ReportCountYear, 1))
				require.ErrorIs(t, err, ErrReportCountStorageRepresentation)
				require.NotErrorIs(t, err, ErrReportCountQueryRepresentation)
				require.Nil(t, got)
			})
		})
	}
}

func TestPostgresReportCountCalendarBroadYearMicroseconds(t *testing.T) {
	open := reportCountTestPostgres(t)
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			for _, group := range reportCountCalendarGroupings {
				t.Run(group.name, func(t *testing.T) {
					db := open(t, owner.name)
					reportCountTestTable(t, db, owner)
					first := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
					since := first.Add(123456001 * time.Nanosecond)
					until := first.Add(987654001 * time.Nanosecond)
					for i, at := range []time.Time{first.Add(123456 * time.Microsecond), first.Add(123457 * time.Microsecond), first.Add(987654 * time.Microsecond), first.Add(987655 * time.Microsecond)} {
						reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
					}
					b := domain.ReportCountBucket{Timestamp: first.Unix(), Since: since, Until: until}
					got, err := owner.countCalendar(db, context.Background(), 91, group.grouping, []domain.ReportCountBucket{b})
					require.NoError(t, err)
					require.Equal(t, []domain.ReportCountPoint{{Value: 2, Timestamp: first.Unix()}}, got)
				})
			}
		})
	}
}
