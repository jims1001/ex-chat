package repository

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresReportCountDaily(t *testing.T) {
	runReportCountDaily(t, reportCountTestPostgres(t))
}

func reportCountTestPostgres(t *testing.T) func(*testing.T, string) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL report count regression")
	}
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	hosts := []string{cfg.Host}
	for _, fallback := range cfg.Fallbacks {
		hosts = append(hosts, fallback.Host)
	}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		require.True(t, host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	}
	cfg.ConnectTimeout = 5 * time.Second
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	return func(t *testing.T, owner string) *gorm.DB {
		schema := "report_count_" + strings.ToLower(owner) + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err := admin.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
		ownerCfg := cfg.Copy()
		ownerCfg.RuntimeParams["search_path"] = schema
		ownerCfg.RuntimeParams["TimeZone"] = "Pacific/Chatham"
		conn := stdlib.OpenDB(*ownerCfg)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		return db
	}
}

func TestPostgresReportCountReadDeadlines(t *testing.T) {
	open := reportCountTestPostgres(t)
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := open(t, owner.name)
			reportCountTestTable(t, db, owner)
			day := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
			reportCountTestInsert(t, db, owner, 1, 91, day, owner.messageType)
			blocker := db.Begin()
			require.NoError(t, blocker.Error)
			t.Cleanup(func() { _ = blocker.Rollback().Error })
			require.NoError(t, blocker.Exec("LOCK TABLE "+owner.table+" IN ACCESS EXCLUSIVE MODE").Error)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			points, err := owner.count(db, ctx, 91, []domain.ReportCountBucket{reportCountTestBucket(day, day.AddDate(0, 0, 1))})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, points)
			require.Less(t, time.Since(started), 2*time.Second)
			require.NoError(t, blocker.Rollback().Error)
			points, err = owner.count(db, context.Background(), 91, []domain.ReportCountBucket{reportCountTestBucket(day, day.AddDate(0, 0, 1))})
			require.NoError(t, err)
			require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: day.Unix()}}, points)
		})
	}
}
