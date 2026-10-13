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

func TestPostgresCSATMetricsDependencyHTTP(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL CSAT metrics dependency HTTP regression")
	}
	c, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	hosts := []string{c.Host}
	for _, fallback := range c.Fallbacks {
		hosts = append(hosts, fallback.Host)
	}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		require.True(t, host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	}
	c.ConnectTimeout = 5 * time.Second
	admin := stdlib.OpenDB(*c)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := "csat_metrics_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
	if c.RuntimeParams == nil {
		c.RuntimeParams = make(map[string]string)
	}
	c.RuntimeParams["search_path"] = schema
	conn := stdlib.OpenDB(*c)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	cfg := csatMetricsDependencyHTTPConfig()
	cfg.DBDriver = "postgres"
	runCSATMetricsDependencyHTTP(t, cfg, db)
}
