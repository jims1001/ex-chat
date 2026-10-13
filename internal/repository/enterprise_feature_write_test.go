package repository

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func enterpriseFeatureSQLite(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "enterprise-features.db")
	first := openMFATestDB(t, path)
	require.NoError(t, first.AutoMigrate(&domain.AccountFeature{}))
	return first, openMFATestDB(t, path)
}

func enterpriseFeaturePostgres(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL feature-write regression")
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
	schema := "ent_features_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
	cfg.RuntimeParams["search_path"] = schema
	cfg.RuntimeParams["statement_timeout"] = "10000"
	cfg.RuntimeParams["lock_timeout"] = "10000"
	handles := make([]*gorm.DB, 2)
	for i := range handles {
		conn := stdlib.OpenDB(*cfg)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		handles[i], err = gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
	}
	require.NoError(t, handles[0].AutoMigrate(&domain.AccountFeature{}))
	return handles[0], handles[1]
}

func enterpriseFeatureWriteCases(t *testing.T, fixture func(*testing.T) (*gorm.DB, *gorm.DB)) {
	t.Helper()
	t.Run("transitions_preserve_identity_and_account_scope", func(t *testing.T) {
		first, second := fixture(t)
		repo := NewEnterpriseRepository(first)
		require.NoError(t, repo.SetAccountFeature(92, "captain", false))
		require.NoError(t, repo.SetAccountFeature(91, "unknown_future", true))
		var id uint
		for _, enabled := range []bool{false, false, true, true, false, true, false} {
			require.NoError(t, repo.SetAccountFeature(91, "captain", enabled))
			flags, err := NewEnterpriseRepository(second).GetAccountFeatures(91)
			require.NoError(t, err)
			require.Equal(t, map[string]bool{"captain": enabled, "unknown_future": true}, flags)
			flags, err = NewEnterpriseRepository(second).PersistedFeatureFlags(91)
			require.NoError(t, err)
			require.Equal(t, map[string]bool{"captain": enabled, "unknown_future": true}, flags)
			var row domain.AccountFeature
			require.NoError(t, second.Where("account_id = ? AND feature_name = ?", 91, "captain").First(&row).Error)
			require.False(t, row.UpdatedAt.IsZero())
			if id == 0 {
				id = row.ID
			} else {
				require.Equal(t, id, row.ID)
			}
			other, err := NewEnterpriseRepository(second).GetAccountFeatures(92)
			require.NoError(t, err)
			require.Equal(t, map[string]bool{"captain": false}, other)
		}
	})
	t.Run("concurrent_creates_and_updates", func(t *testing.T) {
		first, second := fixture(t)
		repos := []*EnterpriseRepository{NewEnterpriseRepository(first), NewEnterpriseRepository(second)}
		var id uint
		for _, enabled := range []bool{false, true, false} {
			start := make(chan struct{})
			results := make(chan error, 16)
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) { defer wg.Done(); <-start; results <- repos[i%2].SetAccountFeature(91, "captain", enabled) }(i)
			}
			close(start)
			wg.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			var rows []domain.AccountFeature
			require.NoError(t, second.Find(&rows).Error)
			require.Len(t, rows, 1)
			require.Equal(t, enabled, rows[0].Enabled)
			if id == 0 {
				id = rows[0].ID
			} else {
				require.Equal(t, id, rows[0].ID)
			}
		}
	})
	t.Run("storage_failure_preserves_existing_value", func(t *testing.T) {
		first, second := fixture(t)
		repo := NewEnterpriseRepository(first)
		require.NoError(t, repo.SetAccountFeature(91, "captain", false))
		require.NoError(t, first.Callback().Create().Before("gorm:create").Register("feature_write_failure", func(tx *gorm.DB) { tx.AddError(errors.New("synthetic write failure")) }))
		require.Error(t, repo.SetAccountFeature(91, "captain", true))
		flags, err := NewEnterpriseRepository(second).GetAccountFeatures(91)
		require.NoError(t, err)
		require.Equal(t, map[string]bool{"captain": false}, flags)
	})
}

func TestEnterpriseFeatureWritesSQLite(t *testing.T) {
	enterpriseFeatureWriteCases(t, enterpriseFeatureSQLite)
}
func TestPostgresEnterpriseFeatureWrites(t *testing.T) {
	enterpriseFeatureWriteCases(t, enterpriseFeaturePostgres)
}
