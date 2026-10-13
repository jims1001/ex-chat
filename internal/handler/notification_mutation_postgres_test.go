package handler

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestPostgresNotificationMutationOwners(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL notification mutation regression")
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
	runNotificationMutationOwners(t, func(t *testing.T) *gorm.DB {
		schema := "ntf_mutation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err := admin.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
			require.NoError(t, err)
		})
		fixtureConfig := cfg.Copy()
		if fixtureConfig.RuntimeParams == nil {
			fixtureConfig.RuntimeParams = map[string]string{}
		}
		fixtureConfig.RuntimeParams["search_path"] = schema
		conn := stdlib.OpenDB(*fixtureConfig)
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		return db
	})
}
