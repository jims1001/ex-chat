package repository

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth/samltest"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Explicit opt-in only: these tests create disposable schemas on a local test
// database. No inherited application DB_DSN or production database is used.
func isolatedSAMLPostgres(t *testing.T) (*SAMLRequestRepository, *SAMLRequestRepository) {
	t.Helper()
	dsn := os.Getenv("SAML_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SAML_TEST_POSTGRES_DSN to an explicitly authorized loopback test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	loopback := func(host string) bool {
		ip := net.ParseIP(host)
		return host == "localhost" || (ip != nil && ip.IsLoopback())
	}
	require.True(t, loopback(cfg.Host), "SAML test database must be loopback")
	for _, fallback := range cfg.Fallbacks {
		require.True(t, loopback(fallback.Host), "SAML test database fallbacks must be loopback")
	}
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	schema := "saml_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		_ = admin.Close()
	})
	cfg.RuntimeParams["search_path"] = schema
	repositories := make([]*SAMLRequestRepository, 2)
	for i := range repositories {
		sqlDB := stdlib.OpenDB(*cfg)
		sqlDB.SetMaxOpenConns(8)
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		repositories[i] = NewSAMLRequestRepository(db)
	}
	require.NoError(t, repositories[0].db.AutoMigrate(&domain.SAMLAuthnRequest{}, &domain.SAMLReplay{}))
	return repositories[0], repositories[1]
}

func TestPostgresSAMLRequestAtomicConsume(t *testing.T) {
	first, second := isolatedSAMLPostgres(t)
	fixture := samltest.NewFixture(t)
	verified, config := samlVerified(t, fixture, "_postgres_one_use")
	request := samlPrepare(t, first, "_postgres_one_use", config)
	results := samlConcurrent(t, first, second, 24, func(repo *SAMLRequestRepository, _ int) error {
		return repo.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified)
	})
	requireSAMLWinner(t, results)
	requireSAMLRowCounts(t, first, 0, 1, 1)
	restarted := NewSAMLRequestRepository(second.db)
	require.ErrorIs(t, restarted.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified), ErrSAMLRequestInvalid)
}

func TestPostgresSAMLIndependentReplayIDRaces(t *testing.T) {
	first, second := isolatedSAMLPostgres(t)
	testSAMLConcurrentReplayIDs(t, first, second)
}

func TestPostgresSAMLStorageFailureRollsBack(t *testing.T) {
	first, second := isolatedSAMLPostgres(t)
	fixture := samltest.NewFixture(t)
	verified, config := samlVerified(t, fixture, "_postgres_rollback")
	request := samlPrepare(t, first, "_postgres_rollback", config)
	require.NoError(t, first.db.Exec(`CREATE FUNCTION saml_fail_replay() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected replay failure'; END; $$ LANGUAGE plpgsql`).Error)
	require.NoError(t, first.db.Exec(`CREATE TRIGGER saml_fail BEFORE INSERT ON saml_replays FOR EACH ROW EXECUTE FUNCTION saml_fail_replay()`).Error)
	err := first.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrSAMLRequestInvalid)
	requireSAMLRowCounts(t, second, 1, 0, 0)
	require.NoError(t, first.db.Exec(`DROP TRIGGER saml_fail ON saml_replays`).Error)
	require.NoError(t, second.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified))
	requireSAMLRowCounts(t, first, 0, 1, 1)
}
