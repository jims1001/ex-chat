package repository

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestReportMessageSummary(t *testing.T) { runReportMessageSummary(t, reportCountTestSQLite) }
func TestPostgresReportMessageSummary(t *testing.T) {
	runReportMessageSummary(t, reportCountTestPostgres(t))
}

func runReportMessageSummary(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	owner := reportCountTestOwner{name: "MSG", table: "messages"}
	t.Run("one_parameterized_owner_snapshot_and_exact_types", func(t *testing.T) {
		db := open(t, "MSG")
		reportCountTestTable(t, db, owner)
		at := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
		kinds := []string{"incoming", "incoming", "outgoing", "outgoing", "activity", "template", "Incoming", "Outgoing", "incoming ", "outgoing' OR 1=1 --"}
		for i, kind := range kinds {
			reportCountTestInsert(t, db, owner, uint(i+1), 91, at, kind)
			reportCountTestInsert(t, db, owner, uint(i+101), 92, at, kind)
		}
		statements := []string{}
		var params []any
		writes := 0
		require.NoError(t, db.Callback().Row().After("gorm:row").Register("summary_select_capture", func(tx *gorm.DB) {
			statements = append(statements, tx.Statement.SQL.String())
			params = append([]any(nil), tx.Statement.Vars...)
		}))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("summary_no_create", func(*gorm.DB) { writes++ }))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("summary_no_update", func(*gorm.DB) { writes++ }))
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("summary_no_delete", func(*gorm.DB) { writes++ }))
		until := at.Add(time.Hour)
		got, err := NewMessageRepositoryWithConversationPort(db, nil).CountReportSummary(91, &at, &until)
		require.NoError(t, err)
		require.Equal(t, domain.ReportMessageSummary{Total: 10, Incoming: 2, Outgoing: 2}, got)
		require.Len(t, statements, 1)
		sql := strings.ToLower(statements[0])
		require.True(t, strings.HasPrefix(sql, "select "))
		for _, s := range []string{" join ", "content", "conversation_id", "conversations", "status", "private", "insert ", "update ", "delete "} {
			require.NotContains(t, sql, s)
		}
		require.Len(t, params, 5)
		require.Equal(t, "incoming", params[0])
		require.Equal(t, "outgoing", params[1])
		require.Equal(t, uint(91), params[2])
		require.Zero(t, writes)
		got, err = NewMessageRepository(db).CountReportSummary(999, nil, nil)
		require.NoError(t, err)
		require.Equal(t, domain.ReportMessageSummary{}, got)
	})
	t.Run("optional_half_open_and_submicrosecond_bounds", func(t *testing.T) {
		db := open(t, "MSG")
		reportCountTestTable(t, db, owner)
		start := time.Date(2024, 2, 29, 23, 59, 59, 999999000, time.UTC)
		end := start.Add(2 * time.Microsecond)
		for i, at := range []time.Time{start.Add(-time.Microsecond), start, start.Add(time.Microsecond), end, end.Add(time.Microsecond)} {
			reportCountTestInsert(t, db, owner, uint(i+1), 91, at, "incoming")
		}
		fractional := start.Add(time.Nanosecond)
		for _, tc := range []struct {
			name         string
			since, until *time.Time
			count        int64
		}{
			{"unbounded", nil, nil, 5}, {"since_only", &start, nil, 4}, {"until_only", nil, &end, 3}, {"exact", &start, &end, 2}, {"empty", &start, &start, 0}, {"reverse", &end, &start, 0}, {"fractional_since", &fractional, nil, 3}, {"fractional_until", nil, &fractional, 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := NewMessageRepository(db).CountReportSummary(91, tc.since, tc.until)
				require.NoError(t, err)
				require.Equal(t, domain.ReportMessageSummary{Total: tc.count, Incoming: tc.count}, got)
			})
		}
		if db.Dialector.Name() == "postgres" {
			t.Run("equivalent_offsets", func(t *testing.T) {
				lo, hi := start.In(time.FixedZone("east", 20700)), end.In(time.FixedZone("west", -12600))
				got, err := NewMessageRepository(db).CountReportSummary(91, &lo, &hi)
				require.NoError(t, err)
				require.Equal(t, int64(2), got.Total)
			})
		}
	})
	t.Run("database_failure_is_not_zero_and_is_secret_safe", func(t *testing.T) {
		db := open(t, "MSG")
		var logs bytes.Buffer
		db = db.Session(&gorm.Session{Logger: logger.New(log.New(&logs, "", 0), logger.Config{LogLevel: logger.Info})})
		got, err := NewMessageRepository(db).CountReportSummary(91, nil, nil)
		require.EqualError(t, err, "report message summary is unavailable")
		require.Equal(t, domain.ReportMessageSummary{}, got)
		require.Empty(t, logs.String())
	})
}

func TestReportMessageSummarySQLiteCompatibility(t *testing.T) {
	db := reportCountTestSQLite(t, "MSG")
	owner := reportCountTestOwner{name: "MSG", table: "messages"}
	reportCountTestTable(t, db, owner)
	at := time.Date(2024, 2, 29, 12, 0, 0, 123456789, time.FixedZone("legacy", 20700))
	end := at.Add(time.Nanosecond)
	reportCountTestInsert(t, db, owner, 1, 91, at, "incoming")
	// Matching-offset legacy text and exact nanoseconds remain accepted. This is
	// deliberately not the UTC-only storage guard from the phase44 series endpoint.
	got, err := NewMessageRepository(db).CountReportSummary(91, &at, &end)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Incoming)
	got, err = NewMessageRepository(db).CountReportSummary(91, &end, nil)
	require.NoError(t, err)
	require.Zero(t, got.Total)
}
