package repository

import (
	"bytes"
	"context"
	"log"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type reportCountTestOwner struct {
	name        string
	table       string
	messageType string
}

var reportCountTestOwners = []reportCountTestOwner{
	{name: "CON", table: "conversations"},
	{name: "MSG_incoming", table: "messages", messageType: domain.MessageTypeIncoming},
	{name: "MSG_outgoing", table: "messages", messageType: domain.MessageTypeOutgoing},
}

func (owner reportCountTestOwner) count(db *gorm.DB, ctx context.Context, account uint, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	if owner.table == "conversations" {
		return NewConversationRepository(db).CountReportBuckets(ctx, account, domain.ReportCountDay, buckets)
	}
	return NewMessageRepositoryWithConversationPort(db, nil).CountReportBuckets(ctx, account, owner.messageType, domain.ReportCountDay, buckets)
}

func reportCountTestBucket(since, until time.Time) domain.ReportCountBucket {
	utc := since.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	return domain.ReportCountBucket{Timestamp: day.Unix(), Since: since, Until: until}
}

func reportCountTestSQLite(t *testing.T, owner string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return db
}

func reportCountTestTable(t *testing.T, db *gorm.DB, owner reportCountTestOwner) {
	t.Helper()
	stampType := "datetime"
	if db.Dialector.Name() == "postgres" {
		stampType = "timestamptz"
	}
	fields := "id bigint PRIMARY KEY, account_id bigint, created_at " + stampType
	if owner.table == "messages" {
		fields += ", message_type text"
	}
	// These minimal owner-local tables deliberately have no body, association,
	// status, private, reporting-event or opposite-owner columns/tables.
	require.NoError(t, db.Exec("CREATE TABLE "+owner.table+" ("+fields+")").Error)
}

func reportCountTestInsert(t *testing.T, db *gorm.DB, owner reportCountTestOwner, id, account uint, at time.Time, messageType string) {
	t.Helper()
	row := map[string]any{"id": id, "account_id": account, "created_at": at}
	if owner.table == "messages" {
		row["message_type"] = messageType
	}
	require.NoError(t, db.Table(owner.table).Create(row).Error)
}

func TestReportCountDailySQLite(t *testing.T) {
	runReportCountDaily(t, reportCountTestSQLite)
}

// Shared native coverage uses actual SQLite and opt-in PostgreSQL drivers.
func runReportCountDaily(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			t.Run("owner_local_sparse_sorted_exact_type", func(t *testing.T) {
				db := open(t, owner.name)
				reportCountTestTable(t, db, owner)
				first := time.Date(2024, 2, 28, 0, 0, 0, 0, time.UTC)
				buckets := make([]domain.ReportCountBucket, 4)
				for i := range buckets {
					day := first.AddDate(0, 0, i)
					buckets[i] = reportCountTestBucket(day, day.AddDate(0, 0, 1))
				}
				// Insert out of time order. Empty March 1 must remain absent.
				for i, offset := range []int{3, 1, 0, 1} {
					at := first.AddDate(0, 0, offset).Add(12 * time.Hour)
					reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
					reportCountTestInsert(t, db, owner, uint(i+101), 92, at, owner.messageType)
				}
				if owner.table == "messages" {
					for i, direction := range []string{"activity", "template", "Incoming", "incoming ", "Outgoing", "outgoing ", "incoming' OR 1=1 --"} {
						reportCountTestInsert(t, db, owner, uint(i+201), 91, first, direction)
					}
					other := domain.MessageTypeOutgoing
					if owner.messageType == other {
						other = domain.MessageTypeIncoming
					}
					reportCountTestInsert(t, db, owner, 250, 91, first, other)
				}
				got, err := owner.count(db, context.Background(), 91, buckets)
				require.NoError(t, err)
				require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[0].Timestamp}, {Value: 2, Timestamp: buckets[1].Timestamp}, {Value: 1, Timestamp: buckets[3].Timestamp}}, got)
				got, err = owner.count(db, context.Background(), 999, buckets)
				require.NoError(t, err)
				require.NotNil(t, got)
				require.Empty(t, got)
			})
			t.Run("one_statement_400_buckets_parameters_and_context", func(t *testing.T) {
				db := open(t, owner.name)
				reportCountTestTable(t, db, owner)
				first := time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC)
				buckets := make([]domain.ReportCountBucket, 400)
				for i := range buckets {
					day := first.AddDate(0, 0, i)
					buckets[i] = reportCountTestBucket(day, day.AddDate(0, 0, 1))
				}
				reportCountTestInsert(t, db, owner, 1, 91, buckets[200].Since, owner.messageType)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				queries := 0
				require.NoError(t, db.Callback().Row().After("gorm:row").Register("count_capture", func(tx *gorm.DB) {
					queries++
					require.Equal(t, ctx, tx.Statement.Context)
					statement := tx.Statement.SQL.String()
					require.Contains(t, statement, "WITH report_count_buckets")
					if db.Dialector.Name() == "sqlite" {
						require.Contains(t, statement, "FROM "+owner.table+" WHERE account_id = ?")
						require.Contains(t, statement, "JOIN report_count_owner_rows AS owned")
						require.Contains(t, statement, "report_count_validity")
						require.Contains(t, statement, "UNION ALL SELECT -1, 0")
						require.Contains(t, statement, "ORDER BY bucket_index ASC")
					} else {
						require.Contains(t, statement, "JOIN "+owner.table+" AS owned")
						require.Contains(t, statement, "ORDER BY b.bucket_index ASC")
					}
					require.Contains(t, statement, "COUNT(*)")
					require.Contains(t, statement, "GROUP BY b.bucket_index")
					for _, forbidden := range []string{"content", "metadata", "SELECT *", "date(", "datetime(", "strftime(", "date_trunc(", "time_bucket("} {
						require.NotContains(t, statement, forbidden)
					}
					wantParams := 801
					if owner.table == "messages" {
						wantParams++
						require.NotContains(t, statement, "conversations")
						require.Equal(t, owner.messageType, tx.Statement.Vars[801])
					} else {
						require.NotContains(t, statement, "messages")
					}
					require.Len(t, tx.Statement.Vars, wantParams)
					require.Equal(t, uint(91), tx.Statement.Vars[800])
					for i, bucket := range buckets {
						require.Equal(t, ReportTimeBoundary(db, bucket.Since), tx.Statement.Vars[2*i])
						require.Equal(t, ReportTimeBoundary(db, bucket.Until), tx.Statement.Vars[2*i+1])
					}
					if db.Dialector.Name() == "postgres" {
						require.Equal(t, 800, strings.Count(statement, "AS timestamptz)"))
					}
				}))
				got, err := owner.count(db, ctx, 91, buckets)
				require.NoError(t, err)
				require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: buckets[200].Timestamp}}, got)
				require.Equal(t, 1, queries)
			})
			t.Run("exact_clipped_half_open_precision", func(t *testing.T) {
				for _, base := range []time.Time{
					time.Date(2024, 2, 29, 12, 30, 0, 250000000, time.UTC),
					time.Date(2024, 2, 29, 23, 59, 59, 999999000, time.UTC),
					time.Date(1969, 12, 31, 23, 59, 59, 999999000, time.UTC),
				} {
					t.Run(base.Format(time.RFC3339Nano), func(t *testing.T) {
						db := open(t, owner.name)
						reportCountTestTable(t, db, owner)
						for i, at := range []time.Time{base.Add(-time.Microsecond), base, base.Add(time.Microsecond)} {
							reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
						}
						for _, fraction := range []time.Duration{0, time.Nanosecond, 999 * time.Nanosecond} {
							boundary := base.Add(fraction)
							left := reportCountTestBucket(base.Add(-2*time.Microsecond), boundary)
							right := reportCountTestBucket(boundary, base.Add(time.Microsecond))
							wantLeft, wantRight := int64(1), int64(1)
							if fraction != 0 {
								wantLeft, wantRight = 2, 0
							}
							for i, bucket := range []domain.ReportCountBucket{left, right} {
								want := []int64{wantLeft, wantRight}[i]
								got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{bucket})
								require.NoError(t, err)
								if want == 0 {
									require.Empty(t, got)
								} else {
									require.Equal(t, []domain.ReportCountPoint{{Value: want, Timestamp: bucket.Timestamp}}, got)
								}
							}
						}
					})
				}
			})
			t.Run("cancel_and_storage_errors_are_sanitized", func(t *testing.T) {
				for _, phase := range []string{"nil_context", "before_query", "after_query", "missing_table"} {
					t.Run(phase, func(t *testing.T) {
						db := open(t, owner.name)
						day := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
						if phase != "missing_table" {
							reportCountTestTable(t, db, owner)
							reportCountTestInsert(t, db, owner, 1, 91, day, owner.messageType)
						}
						var logged bytes.Buffer
						db.Logger = logger.New(log.New(&logged, "", 0), logger.Config{LogLevel: logger.Info})
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						queries := 0
						require.NoError(t, db.Callback().Row().After("gorm:row").Register("count_cancel", func(tx *gorm.DB) {
							queries++
							if phase == "after_query" {
								cancel()
							}
						}))
						if phase == "before_query" {
							cancel()
						}
						if phase == "nil_context" {
							ctx = nil
						}
						got, err := owner.count(db, ctx, 91, []domain.ReportCountBucket{reportCountTestBucket(day, day.AddDate(0, 0, 1))})
						if phase == "before_query" || phase == "after_query" {
							require.ErrorIs(t, err, context.Canceled)
						} else {
							require.EqualError(t, err, "report counts are unavailable")
						}
						require.Nil(t, got)
						require.Empty(t, logged.String(), "neither raw SQL nor a driver error may reach the configured logger")
						if phase == "nil_context" || phase == "before_query" {
							require.Zero(t, queries)
						} else {
							require.Equal(t, 1, queries)
						}
					})
				}
			})
		})
	}
}

func TestReportCountDailyRejectsInvalidInputWithoutQuery(t *testing.T) {
	day := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	valid := reportCountTestBucket(day, day.AddDate(0, 0, 1))
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := reportCountTestSQLite(t, owner.name)
			queries := 0
			require.NoError(t, db.Callback().Row().After("gorm:row").Register("count_no_query", func(tx *gorm.DB) { queries++ }))
			for _, tc := range []struct {
				name    string
				account uint
				buckets []domain.ReportCountBucket
			}{
				{"zero_account", 0, []domain.ReportCountBucket{valid}},
				{"overflow_account", uint(math.MaxUint64), []domain.ReportCountBucket{valid}},
				{"over_400", 91, make([]domain.ReportCountBucket, 401)},
				{"equal_bounds", 91, []domain.ReportCountBucket{reportCountTestBucket(day, day)}},
				{"reversed_bounds", 91, []domain.ReportCountBucket{reportCountTestBucket(day.Add(time.Hour), day)}},
				{"non_midnight_label", 91, []domain.ReportCountBucket{{Timestamp: valid.Timestamp + 1, Since: valid.Since, Until: valid.Until}}},
				{"start_before_day", 91, []domain.ReportCountBucket{{Timestamp: valid.Timestamp, Since: day.Add(-time.Nanosecond), Until: valid.Until}}},
				{"end_after_day", 91, []domain.ReportCountBucket{reportCountTestBucket(day, valid.Until.Add(time.Nanosecond))}},
				{"duplicate_day", 91, []domain.ReportCountBucket{valid, valid}},
				{"unsorted_days", 91, []domain.ReportCountBucket{reportCountTestBucket(day.AddDate(0, 0, 1), day.AddDate(0, 0, 2)), valid}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := owner.count(db, context.Background(), tc.account, tc.buckets)
					require.EqualError(t, err, "report counts are unavailable")
					require.Nil(t, got)
				})
			}
			got, err := owner.count(db, context.Background(), 91, nil)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Empty(t, got)
			require.Zero(t, queries)
		})
	}
	for _, direction := range []string{"", "Incoming", "Outgoing", "incoming ", "activity", "incoming' OR 1=1 --"} {
		got, err := NewMessageRepositoryWithConversationPort(reportCountTestSQLite(t, "MSG"), nil).CountReportBuckets(context.Background(), 91, direction, domain.ReportCountDay, []domain.ReportCountBucket{valid})
		require.EqualError(t, err, "report counts are unavailable")
		require.Nil(t, got)
	}
	var conversations *ConversationRepository
	var messages *MessageRepository
	got, err := conversations.CountReportBuckets(context.Background(), 91, domain.ReportCountDay, []domain.ReportCountBucket{valid})
	require.EqualError(t, err, "report counts are unavailable")
	require.Nil(t, got)
	got, err = messages.CountReportBuckets(context.Background(), 91, domain.MessageTypeIncoming, domain.ReportCountDay, []domain.ReportCountBucket{valid})
	require.EqualError(t, err, "report counts are unavailable")
	require.Nil(t, got)
	for _, db := range []*gorm.DB{nil, {}, {Config: &gorm.Config{}}} {
		for _, owner := range reportCountTestOwners {
			got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{valid})
			require.EqualError(t, err, "report counts are unavailable")
			require.Nil(t, got)
		}
	}
}

func TestReportCountDailySQLiteCanonicalUTCNanoseconds(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := reportCountTestSQLite(t, owner.name)
			reportCountTestTable(t, db, owner)
			since := time.Date(2024, 2, 29, 12, 0, 0, 123456789, time.UTC)
			until := since.Add(time.Hour)
			for i, at := range []time.Time{since.Add(-time.Nanosecond), since, until.Add(-time.Nanosecond), until} {
				reportCountTestInsert(t, db, owner, uint(i+1), 91, at, owner.messageType)
			}
			bucket := reportCountTestBucket(since, until)
			got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{bucket})
			require.NoError(t, err)
			require.Equal(t, []domain.ReportCountPoint{{Value: 2, Timestamp: bucket.Timestamp}}, got)
		})
	}
}
