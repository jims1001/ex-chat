package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
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

type systemConfigFixture func(*testing.T) (*gorm.DB, *gorm.DB)

func systemConfigSQLite(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "system-config.sqlite")
	first := openMFATestDB(t, path)
	require.NoError(t, first.AutoMigrate(&domain.SystemConfig{}))
	return first, openMFATestDB(t, path)
}

// Only the explicit PostgreSQL test opt-in permits connections. Every fixture
// gets two independent pools and a disposable schema containing only SYS data.
func isolatedSystemConfigPostgres(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	if os.Getenv("EX_CHAT_AUTH_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUTH_POSTGRES_TESTS=1 with a disposable loopback DB_DSN")
	}
	dsn := os.Getenv("DB_DSN")
	require.NotEmpty(t, dsn, "the PostgreSQL fixture needs an explicit DB_DSN")
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	loopback := func(host string) bool {
		ip := net.ParseIP(host)
		return host == "localhost" || (ip != nil && ip.IsLoopback())
	}
	require.True(t, loopback(cfg.Host), "test database must be loopback")
	for _, fallback := range cfg.Fallbacks {
		require.True(t, loopback(fallback.Host), "test database fallbacks must be loopback")
	}
	cfg.ConnectTimeout = 5 * time.Second
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, admin.PingContext(ctx))
	schema := "sys_config_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = make(map[string]string)
	}
	cfg.RuntimeParams["search_path"] = schema
	cfg.RuntimeParams["statement_timeout"] = "10000"
	cfg.RuntimeParams["lock_timeout"] = "10000"
	handles := make([]*gorm.DB, 2)
	for i := range handles {
		sqlDB := stdlib.OpenDB(*cfg)
		sqlDB.SetMaxOpenConns(8)
		t.Cleanup(func() { _ = sqlDB.Close() })
		handles[i], err = gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
	}
	require.NoError(t, handles[0].AutoMigrate(&domain.SystemConfig{}))
	return handles[0], handles[1]
}

func TestSystemConfigRepositorySQLite(t *testing.T) {
	testSystemConfigRepository(t, systemConfigSQLite)
}

func TestPostgresSystemConfigRepository(t *testing.T) {
	testSystemConfigRepository(t, isolatedSystemConfigPostgres)
}

func testSystemConfigRepository(t *testing.T, fixture systemConfigFixture) {
	t.Helper()
	t.Run("empty preserves without any database operation", func(t *testing.T) {
		first, second := fixture(t)
		before := seedSystemConfig(t, first, "SMTP_PASSWORD", "fixture-existing-secret")
		repo := NewSystemConfigRepository(first)
		// A read-before-write implementation is also wrong: preserving a value
		// must work even when the store cannot be queried or written.
		calls := 0
		reject := func(tx *gorm.DB) {
			calls++
			tx.AddError(errors.New("empty update must not access the database"))
		}
		registerSystemConfigRejectCallbacks(t, first, "reject_empty_access", reject)
		require.NoError(t, repo.Set(before.ConfigKey, ""))
		require.NoError(t, repo.Set("UNCONFIGURED_SECRET", ""))
		require.Zero(t, calls)
		requireSystemConfigUnchanged(t, second, before)
		var count int64
		require.NoError(t, second.Model(&domain.SystemConfig{}).Count(&count).Error)
		require.EqualValues(t, 1, count, "preserve must not insert an absent key")
	})

	t.Run("empty preserves in read-only storage", func(t *testing.T) {
		first, second := fixture(t)
		before := seedSystemConfig(t, first, "SMTP_PASSWORD", "fixture-existing-secret")
		readOnly := first
		if first.Dialector.Name() == "postgres" {
			readOnly = first.Begin(&sql.TxOptions{ReadOnly: true})
			require.NoError(t, readOnly.Error)
			t.Cleanup(func() { _ = readOnly.Rollback().Error })
		} else {
			require.NoError(t, first.Exec("PRAGMA query_only = ON").Error)
			t.Cleanup(func() { require.NoError(t, first.Exec("PRAGMA query_only = OFF").Error) })
		}
		repo := NewSystemConfigRepository(readOnly)
		require.NoError(t, repo.Set(before.ConfigKey, ""))
		require.NoError(t, repo.Set("UNCONFIGURED_SECRET", ""))
		require.Error(t, repo.Set(before.ConfigKey, "fixture-rejected-replacement"), "fixture must actually reject writes")
		requireSystemConfigUnchanged(t, second, before)
		var count int64
		require.NoError(t, second.Model(&domain.SystemConfig{}).Count(&count).Error)
		require.EqualValues(t, 1, count)
	})

	t.Run("nonempty bytes and raw owner reads are preserved", func(t *testing.T) {
		first, second := fixture(t)
		repo := NewSystemConfigRepository(first)
		const key = "SMTP_PASSWORD"
		const createdValue = "  fixture-secret\t\r\n雪  "
		require.NoError(t, repo.Set(key, createdValue))
		created := loadSystemConfig(t, second, key)
		require.NotZero(t, created.ID)
		require.Equal(t, createdValue, created.Value)
		require.False(t, created.UpdatedAt.IsZero())
		// Use an old persisted timestamp so its replacement is deterministic on
		// both stores, without sleeps or assumptions about timestamp precision.
		oldTime := time.Date(2001, 2, 3, 4, 5, 6, 123000000, time.UTC)
		require.NoError(t, first.Model(&domain.SystemConfig{}).Where("id = ?", created.ID).UpdateColumn("updated_at", oldTime).Error)
		for _, value := range []string{" \t\r\n ", "\n  fixture-replacement'\"\\雪\t  "} {
			require.NoError(t, repo.Set(key, value))
			got := loadSystemConfig(t, second, key)
			require.Equal(t, created.ID, got.ID)
			require.Equal(t, key, got.ConfigKey)
			require.Equal(t, value, got.Value, "whitespace is a replacement, not an empty preserve request")
			require.True(t, got.UpdatedAt.After(oldTime))
			raw, err := repo.Get(key)
			require.NoError(t, err)
			require.Equal(t, value, raw, "owner Get retains the raw stored value")
		}
		require.NoError(t, repo.Set("BRAND_NAME", "  Fixture Brand  "))
		rows, err := repo.List()
		require.NoError(t, err)
		require.Len(t, rows, 2)
		require.Equal(t, "BRAND_NAME", rows[0].ConfigKey)
		require.Equal(t, "  Fixture Brand  ", rows[0].Value)
		require.Equal(t, loadSystemConfig(t, second, key), rows[1], "owner List retains values, IDs and timestamps")
		raw, err := repo.Get("UNCONFIGURED_SECRET")
		require.NoError(t, err)
		require.Empty(t, raw)
	})

	t.Run("each replacement is one atomic upsert", func(t *testing.T) {
		first, _ := fixture(t)
		repo := NewSystemConfigRepository(first)
		var statements []string
		registerSystemConfigCallbacks(t, first, "record_config_statement", func(tx *gorm.DB) {
			statements = append(statements, tx.Statement.SQL.String())
		})
		for _, value := range []string{"fixture-created-secret", "fixture-replaced-secret"} {
			statements = nil
			require.NoError(t, repo.Set("SMTP_PASSWORD", value))
			require.Len(t, statements, 1, "no preflight SELECT or separate UPDATE may race another writer")
			// SQLite uses backticks and PostgreSQL uses double quotes for the
			// same identifiers; only normalize quoting in this parameterized SQL.
			statement := strings.ReplaceAll(statements[0], "`", `"`)
			require.Contains(t, statement, `INSERT INTO "system_configs"`)
			require.Regexp(t, `ON CONFLICT\s*\("config_key"\) DO UPDATE SET`, statement)
			parts := strings.SplitN(statement, "DO UPDATE SET", 2)
			require.Len(t, parts, 2)
			var columns []string
			for _, match := range regexp.MustCompile(`"([a-z_]+)"\s*=`).FindAllStringSubmatch(parts[1], -1) {
				columns = append(columns, match[1])
			}
			require.ElementsMatch(t, []string{"value", "updated_at"}, columns, "upserts must preserve row identity and key")
		}
	})

	t.Run("inherited verbose SQL logger stays silent", func(t *testing.T) {
		first, _ := fixture(t)
		seedSystemConfig(t, first, "SMTP_PASSWORD", "fixture-log-secret")
		var output bytes.Buffer
		verbose := first.Session(&gorm.Session{Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info})})
		repo := NewSystemConfigRepository(verbose)
		_, err := repo.Get("SMTP_PASSWORD")
		require.NoError(t, err)
		_, err = repo.Get("UNCONFIGURED_SECRET")
		require.NoError(t, err)
		_, err = repo.List()
		require.NoError(t, err)
		require.NoError(t, repo.Set("SMTP_PASSWORD", "fixture-log-replacement"))
		require.NoError(t, repo.Set("NEW_SECRET", "fixture-log-created"))
		require.NoError(t, repo.Set("SMTP_PASSWORD", ""))
		require.Empty(t, output.String(), "the owner repository must not trace configuration SQL")
		// The constructor must isolate its logger rather than silencing every
		// other repository that shares the caller's DB handle.
		var count int64
		require.NoError(t, verbose.Model(&domain.SystemConfig{}).Count(&count).Error)
		require.NotEmpty(t, output.String(), "the caller's verbose logger must remain enabled")
		output.Reset()
		require.NoError(t, first.Migrator().DropTable(&domain.SystemConfig{}))
		_, err = repo.Get("fixture-query-failure-secret")
		require.Error(t, err)
		_, err = repo.List()
		require.Error(t, err)
		require.Error(t, repo.Set("SMTP_PASSWORD", "fixture-insert-failure-secret"))
		require.NoError(t, repo.Set("SMTP_PASSWORD", ""), "preserve also works when the table is unavailable")
		require.Empty(t, output.String(), "query and write failures must not restore SQL tracing")
	})

	for _, operation := range []string{"INSERT", "UPDATE"} {
		t.Run(strings.ToLower(operation)+" failure rolls back", func(t *testing.T) {
			first, second := fixture(t)
			seedSystemConfig(t, first, "UNRELATED_SECRET", "fixture-unrelated-secret")
			if operation == "UPDATE" {
				seedSystemConfig(t, first, "SMTP_PASSWORD", "fixture-original-secret")
			}
			var before []domain.SystemConfig
			require.NoError(t, second.Order("config_key ASC").Find(&before).Error)
			removeTrigger := injectSystemConfigFailure(t, first, operation)
			var output bytes.Buffer
			verbose := first.Session(&gorm.Session{Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info})})
			repo := NewSystemConfigRepository(verbose)
			err := repo.Set("SMTP_PASSWORD", "fixture-rejected-secret")
			require.ErrorContains(t, err, "injected system config failure")
			require.Empty(t, output.String(), "SQL failures must not expose the attempted secret")
			var after []domain.SystemConfig
			require.NoError(t, second.Order("config_key ASC").Find(&after).Error)
			require.Equal(t, before, after, "an AFTER trigger failure must roll back every persisted field")
			removeTrigger()
			require.NoError(t, repo.Set("SMTP_PASSWORD", "fixture-accepted-secret"))
			require.Equal(t, "fixture-accepted-secret", loadSystemConfig(t, second, "SMTP_PASSWORD").Value)
			require.Empty(t, output.String())
		})
	}

	t.Run("concurrent preserve and replacement", func(t *testing.T) {
		first, second := fixture(t)
		before := seedSystemConfig(t, first, "SMTP_PASSWORD", "fixture-original-secret")
		results := concurrentSystemConfigSets(t, first, second, 24, func(index int) (string, string) {
			value := ""
			if index%3 == 0 {
				value = "fixture-concurrent-replacement"
			}
			return before.ConfigKey, value
		})
		for _, err := range results {
			require.NoError(t, err)
		}
		after := loadSystemConfig(t, second, before.ConfigKey)
		require.Equal(t, before.ID, after.ID)
		require.Equal(t, "fixture-concurrent-replacement", after.Value)
		require.True(t, after.UpdatedAt.After(before.UpdatedAt))
		require.NoError(t, NewSystemConfigRepository(first).Set(before.ConfigKey, ""))
		requireSystemConfigUnchanged(t, second, after)
		var count int64
		require.NoError(t, second.Model(&domain.SystemConfig{}).Count(&count).Error)
		require.EqualValues(t, 1, count)
	})

	t.Run("concurrent creates converge on one row", func(t *testing.T) {
		first, second := fixture(t)
		results := concurrentSystemConfigSets(t, first, second, 24, func(index int) (string, string) {
			return "NEW_SECRET", fmt.Sprintf("fixture-concurrent-secret-%02d", index)
		})
		for _, err := range results {
			require.NoError(t, err, "all concurrent writers must succeed without a read-create race")
		}
		var rows []domain.SystemConfig
		require.NoError(t, second.Find(&rows).Error)
		require.Len(t, rows, 1)
		require.Equal(t, "NEW_SECRET", rows[0].ConfigKey)
		require.NotZero(t, rows[0].ID)
		require.False(t, rows[0].UpdatedAt.IsZero())
		var writtenValues []string
		for index := range results {
			writtenValues = append(writtenValues, fmt.Sprintf("fixture-concurrent-secret-%02d", index))
		}
		require.Contains(t, writtenValues, rows[0].Value)
	})
}

func seedSystemConfig(t *testing.T, db *gorm.DB, key, value string) domain.SystemConfig {
	t.Helper()
	row := domain.SystemConfig{ConfigKey: key, Value: value, UpdatedAt: time.Date(2001, 2, 3, 4, 5, 6, 123000000, time.UTC)}
	require.NoError(t, db.Create(&row).Error)
	return loadSystemConfig(t, db, key)
}

func loadSystemConfig(t *testing.T, db *gorm.DB, key string) domain.SystemConfig {
	t.Helper()
	var row domain.SystemConfig
	require.NoError(t, db.Where("config_key = ?", key).First(&row).Error)
	return row
}

func requireSystemConfigUnchanged(t *testing.T, db *gorm.DB, before domain.SystemConfig) {
	t.Helper()
	after := loadSystemConfig(t, db, before.ConfigKey)
	require.Equal(t, before.ID, after.ID)
	require.Equal(t, before.ConfigKey, after.ConfigKey)
	require.Equal(t, before.Value, after.Value)
	require.True(t, before.UpdatedAt.Equal(after.UpdatedAt), "preserve must not change updated_at")
}

func registerSystemConfigRejectCallbacks(t *testing.T, db *gorm.DB, name string, fn func(*gorm.DB)) {
	t.Helper()
	require.NoError(t, db.Callback().Query().Before("*").Register(name, fn))
	require.NoError(t, db.Callback().Create().Before("*").Register(name, fn))
	require.NoError(t, db.Callback().Update().Before("*").Register(name, fn))
	require.NoError(t, db.Callback().Delete().Before("*").Register(name, fn))
	require.NoError(t, db.Callback().Row().Before("*").Register(name, fn))
	require.NoError(t, db.Callback().Raw().Before("*").Register(name, fn))
}

// Observing callbacks is independent of the SQL logger, which must stay silent.
// Separate fixture handles keep these callbacks out of assertion reads.
func registerSystemConfigCallbacks(t *testing.T, db *gorm.DB, name string, fn func(*gorm.DB)) {
	t.Helper()
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(name, fn))
	require.NoError(t, db.Callback().Create().After("gorm:create").Register(name, fn))
	require.NoError(t, db.Callback().Update().After("gorm:update").Register(name, fn))
	require.NoError(t, db.Callback().Delete().After("gorm:delete").Register(name, fn))
	require.NoError(t, db.Callback().Row().After("gorm:row").Register(name, fn))
	require.NoError(t, db.Callback().Raw().After("gorm:raw").Register(name, fn))
}

func injectSystemConfigFailure(t *testing.T, db *gorm.DB, operation string) func() {
	t.Helper()
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Exec(`CREATE FUNCTION fail_system_config() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected system config failure'; END; $$ LANGUAGE plpgsql`).Error)
		require.NoError(t, db.Exec("CREATE TRIGGER fail_system_config AFTER "+operation+" ON system_configs FOR EACH ROW WHEN (NEW.config_key = 'SMTP_PASSWORD') EXECUTE FUNCTION fail_system_config()").Error)
		return func() { require.NoError(t, db.Exec("DROP TRIGGER fail_system_config ON system_configs").Error) }
	}
	require.NoError(t, db.Exec("CREATE TRIGGER fail_system_config AFTER "+operation+" ON system_configs WHEN NEW.config_key = 'SMTP_PASSWORD' BEGIN SELECT RAISE(ABORT, 'injected system config failure'); END").Error)
	return func() { require.NoError(t, db.Exec("DROP TRIGGER fail_system_config").Error) }
}

func concurrentSystemConfigSets(t *testing.T, first, second *gorm.DB, attempts int, values func(int) (string, string)) []error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	repos := []*SystemConfigRepository{NewSystemConfigRepository(first.WithContext(ctx)), NewSystemConfigRepository(second.WithContext(ctx))}
	results := make([]error, attempts)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := range results {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			key, value := values(index)
			<-start
			results[index] = repos[index%len(repos)].Set(key, value)
		}(index)
	}
	close(start)
	workers.Wait()
	return results
}
