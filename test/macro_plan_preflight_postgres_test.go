package test

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func macroPlanPostgres(t *testing.T) func(*testing.T, string) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL macro plan preflight regression")
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
		t.Helper()
		schema := "macro_plan_" + strings.ToLower(owner) + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err := admin.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
		local := cfg.Copy()
		if local.RuntimeParams == nil {
			local.RuntimeParams = map[string]string{}
		}
		local.RuntimeParams["search_path"] = schema
		conn := stdlib.OpenDB(*local)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		return db
	}
}
func TestPostgresMacroPlanPreflightOwners(t *testing.T) {
	runMacroPlanPreflightOwners(t, macroPlanPostgres(t))
}
func TestPostgresMacroPlanPreflightHTTP(t *testing.T) {
	db := macroPlanPostgres(t)(t, "HTTP")
	require.NoError(t, database.AutoMigrate(db))
	cfg := macroPlanHTTPConfig()
	cfg.DBDriver = "postgres"
	runMacroPlanPreflightHTTP(t, cfg, db)
}
