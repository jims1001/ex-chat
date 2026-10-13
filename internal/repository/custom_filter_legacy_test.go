package repository

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func legacyFilterTestRepository(t *testing.T) *CustomFilterRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "custom-filters.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, db.AutoMigrate(&domain.CustomFilter{}, &domain.LocalChangeJournal{}))
	return NewCustomFilterRepository(db)
}

func legacyFilterSeed(t *testing.T, db *gorm.DB, accountID, userID uint, version int) domain.CustomFilter {
	t.Helper()
	row := domain.CustomFilter{AccountID: accountID, UserID: userID, Name: "original name", FilterType: "conversation", Query: `{"status":"open"}`, ObjectVersion: version}
	// Direct insertion also permits historical unowned rows, which API writers
	// must never be able to adopt or erase.
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, db.First(&row, row.ID).Error)
	return row
}

func legacyFilterSnapshot(t *testing.T, db *gorm.DB) ([]domain.CustomFilter, []domain.LocalChangeJournal) {
	t.Helper()
	var rows []domain.CustomFilter
	var journal []domain.LocalChangeJournal
	require.NoError(t, db.Order("id").Find(&rows).Error)
	require.NoError(t, db.Order("id").Find(&journal).Error)
	return rows, journal
}

func TestCustomFilterLegacyOwnerScopeAndZeroIdentities(t *testing.T) {
	r := legacyFilterTestRepository(t)
	ctx := context.Background()
	owned := legacyFilterSeed(t, r.db, 91, 7, 0)
	runtime := legacyFilterSeed(t, r.db, 91, 7, 3)
	peer := legacyFilterSeed(t, r.db, 91, 8, 0)
	foreign := legacyFilterSeed(t, r.db, 92, 7, 0)
	unowned := legacyFilterSeed(t, r.db, 91, 0, 0)
	unscoped := legacyFilterSeed(t, r.db, 0, 7, 0)
	require.NoError(t, r.db.Model(&runtime).Update("filter_type", "contact").Error)

	rows, err := r.List(ctx, 91, 7, "")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, []uint{runtime.ID, owned.ID}, []uint{rows[0].ID, rows[1].ID})
	rows, err = r.List(ctx, 91, 7, "conversation")
	require.NoError(t, err)
	require.Equal(t, []domain.CustomFilter{owned}, rows)
	got, err := r.Find(ctx, 91, 7, runtime.ID)
	require.NoError(t, err)
	require.Equal(t, runtime.ID, got.ID, "owned runtime rows remain readable")
	before, journalBefore := legacyFilterSnapshot(t, r.db)
	name := "unauthorized change"
	for _, row := range []domain.CustomFilter{peer, foreign, unowned, unscoped} {
		t.Run(fmt.Sprintf("foreign_row_%d", row.ID), func(t *testing.T) {
			got, err := r.Find(ctx, 91, 7, row.ID)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.Nil(t, got)
			got, err = r.UpdateLegacy(ctx, 91, 7, row.ID, CustomFilterPatch{Name: &name})
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.Nil(t, got)
			deleted, err := r.DeleteLegacy(ctx, 91, 7, row.ID)
			require.NoError(t, err)
			require.False(t, deleted)
		})
	}
	for _, identity := range []struct {
		name              string
		accountID, userID uint
		id                uint
	}{{"zero account", 0, 7, unscoped.ID}, {"zero user", 91, 0, unowned.ID}, {"zero id", 91, 7, 0}} {
		t.Run(identity.name, func(t *testing.T) {
			got, err := r.Find(ctx, identity.accountID, identity.userID, identity.id)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.Nil(t, got)
			got, err = r.UpdateLegacy(ctx, identity.accountID, identity.userID, identity.id, CustomFilterPatch{Name: &name})
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.Nil(t, got)
			deleted, err := r.DeleteLegacy(ctx, identity.accountID, identity.userID, identity.id)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.False(t, deleted)
			if identity.accountID == 0 || identity.userID == 0 {
				rows, err := r.List(ctx, identity.accountID, identity.userID, "")
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				require.Empty(t, rows)
				candidate := domain.CustomFilter{AccountID: identity.accountID, UserID: identity.userID, Name: "invalid", FilterType: "contact", Query: "{}"}
				require.ErrorIs(t, r.Create(ctx, &candidate), gorm.ErrRecordNotFound)
				require.ErrorIs(t, r.CreateLegacy(ctx, identity.accountID, identity.userID, &candidate), gorm.ErrRecordNotFound)
			}
		})
	}
	require.ErrorIs(t, r.Create(ctx, nil), gorm.ErrRecordNotFound)
	require.ErrorIs(t, r.CreateLegacy(ctx, 91, 7, nil), gorm.ErrRecordNotFound)
	after, journalAfter := legacyFilterSnapshot(t, r.db)
	require.Equal(t, before, after, "rejected operations must preserve even historical unowned rows")
	require.Equal(t, journalBefore, journalAfter)
}

func TestCustomFilterLegacyCreateBindsOwnerAndPartialUpdate(t *testing.T) {
	r := legacyFilterTestRepository(t)
	ctx := context.Background()
	existing := legacyFilterSeed(t, r.db, 91, 0, 0)
	row := domain.CustomFilter{ID: existing.ID, AccountID: 92, UserID: 8, ObjectVersion: 99, Name: "new filter", FilterType: "contact", Query: `{"tag":"vip"}`}
	require.NoError(t, r.CreateLegacy(ctx, 91, 7, &row))
	require.NotEqual(t, existing.ID, row.ID)
	require.EqualValues(t, 91, row.AccountID)
	require.EqualValues(t, 7, row.UserID)
	require.Zero(t, row.ObjectVersion)
	created, err := r.Find(ctx, 91, 7, row.ID)
	require.NoError(t, err)
	name := "renamed"
	updated, err := r.UpdateLegacy(ctx, 91, 7, row.ID, CustomFilterPatch{Name: &name})
	require.NoError(t, err)
	require.Equal(t, name, updated.Name)
	require.Equal(t, created.Query, updated.Query)
	require.Equal(t, created.FilterType, updated.FilterType)
	require.Equal(t, created.CreatedAt, updated.CreatedAt)
	require.Zero(t, updated.ObjectVersion)
	unchanged, err := r.UpdateLegacy(ctx, 91, 7, row.ID, CustomFilterPatch{})
	require.NoError(t, err)
	require.Equal(t, updated, unchanged, "an empty patch must not touch the row")
	deleted, err := r.DeleteLegacy(ctx, 91, 7, row.ID)
	require.NoError(t, err)
	require.True(t, deleted)
	deleted, err = r.DeleteLegacy(ctx, 91, 7, row.ID)
	require.NoError(t, err)
	require.False(t, deleted, "deleting an absent owned row is idempotent")
	var preserved domain.CustomFilter
	require.NoError(t, r.db.First(&preserved, existing.ID).Error)
	require.Equal(t, existing, preserved)
}

func TestCustomFilterLegacyRejectsVersionedWritesWithoutChanges(t *testing.T) {
	r := legacyFilterTestRepository(t)
	row := legacyFilterSeed(t, r.db, 91, 7, 4)
	for _, action := range []string{"update", "event_outbox", "command_receipt"} {
		require.NoError(t, r.db.Create(&domain.LocalChangeJournal{ChangeID: uuid.NewString(), SourceModule: "SRH", AccountID: 91, ObjectType: "CustomFilter", ObjectID: row.ID, Action: action, ActorType: "user", ActorID: 7, AfterState: "existing durable evidence"}).Error)
	}
	before, journalBefore := legacyFilterSnapshot(t, r.db)
	name, query := "attempted rename", `{"status":"closed"}`
	for _, patch := range []CustomFilterPatch{{Name: &name, Query: &query}, {}} {
		got, err := r.UpdateLegacy(context.Background(), 91, 7, row.ID, patch)
		require.ErrorIs(t, err, ErrCustomFilterVersioned)
		require.Nil(t, got)
	}
	deleted, err := r.DeleteLegacy(context.Background(), 91, 7, row.ID)
	require.ErrorIs(t, err, ErrCustomFilterVersioned)
	require.False(t, deleted)
	for _, identity := range [][2]uint{{91, 8}, {92, 7}} {
		got, err := r.UpdateLegacy(context.Background(), identity[0], identity[1], row.ID, CustomFilterPatch{Name: &name})
		require.ErrorIs(t, err, gorm.ErrRecordNotFound, "version rejection must not disclose another owner's row")
		require.Nil(t, got)
		deleted, err := r.DeleteLegacy(context.Background(), identity[0], identity[1], row.ID)
		require.NoError(t, err)
		require.False(t, deleted)
	}
	after, journalAfter := legacyFilterSnapshot(t, r.db)
	require.Equal(t, before, after)
	require.Equal(t, journalBefore, journalAfter, "legacy rejection must not alter SRH changes, outbox, or receipts")
}

func TestCustomFilterLegacyRollbackAfterSQLAndCallbackFailures(t *testing.T) {
	for _, failure := range []string{"update SQL", "delete SQL", "create SQL", "update readback", "create callback", "delete callback"} {
		t.Run(failure, func(t *testing.T) {
			r := legacyFilterTestRepository(t)
			row := legacyFilterSeed(t, r.db, 91, 7, 0)
			before, journalBefore := legacyFilterSnapshot(t, r.db)
			injected := errors.New("injected custom filter failure")
			callbackRan := false
			switch failure {
			case "update SQL", "delete SQL", "create SQL":
				operation := map[string]string{"update SQL": "UPDATE", "delete SQL": "DELETE", "create SQL": "INSERT"}[failure]
				require.NoError(t, r.db.Exec("CREATE TRIGGER deny_filter_write BEFORE "+operation+" ON custom_filters BEGIN SELECT RAISE(ABORT, 'injected custom filter failure'); END").Error)
			case "update readback":
				queries := 0
				require.NoError(t, r.db.Callback().Query().After("gorm:query").Register("fail_filter_readback", func(tx *gorm.DB) {
					if tx.Statement.Table == "custom_filters" {
						queries++
						if queries == 2 {
							readback, ok := tx.Statement.Dest.(*domain.CustomFilter)
							callbackRan = ok && readback.Name == "must roll back"
							_ = tx.AddError(injected)
						}
					}
				}))
			case "create callback":
				require.NoError(t, r.db.Callback().Create().After("gorm:create").Register("fail_filter_create", func(tx *gorm.DB) {
					if tx.Statement.Table == "custom_filters" && tx.RowsAffected == 1 {
						callbackRan = true
						_ = tx.AddError(injected)
					}
				}))
			case "delete callback":
				require.NoError(t, r.db.Callback().Delete().After("gorm:delete").Register("fail_filter_delete", func(tx *gorm.DB) {
					if tx.Statement.Table == "custom_filters" && tx.RowsAffected == 1 {
						callbackRan = true
						_ = tx.AddError(injected)
					}
				}))
			}
			var err error
			switch {
			case strings.HasPrefix(failure, "create"):
				err = r.CreateLegacy(context.Background(), 91, 7, &domain.CustomFilter{Name: "new", FilterType: "contact", Query: "{}"})
			case strings.HasPrefix(failure, "delete"):
				var deleted bool
				deleted, err = r.DeleteLegacy(context.Background(), 91, 7, row.ID)
				require.False(t, deleted, "a rolled-back delete cannot report success")
			default:
				name := "must roll back"
				var updated *domain.CustomFilter
				updated, err = r.UpdateLegacy(context.Background(), 91, 7, row.ID, CustomFilterPatch{Name: &name})
				require.Nil(t, updated, "a failed readback cannot return an uncommitted row")
			}
			require.ErrorContains(t, err, injected.Error())
			if !strings.HasSuffix(failure, "SQL") {
				require.ErrorIs(t, err, injected)
				require.True(t, callbackRan, "failure must occur after the SQL was executed")
			}
			after, journalAfter := legacyFilterSnapshot(t, r.db)
			require.Equal(t, before, after)
			require.Equal(t, journalBefore, journalAfter)
		})
	}
}

func postgresLegacyFilterRepositories(t *testing.T) ([2]*CustomFilterRepository, string) {
	t.Helper()
	if os.Getenv("EX_CHAT_AUT_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUT_POSTGRES_TESTS=1 and a disposable loopback keyword DB_DSN")
	}
	dsn := os.Getenv("DB_DSN")
	require.NotEmpty(t, dsn)
	require.False(t, strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://"), "use a libpq keyword DB_DSN")
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	schema := "filter_legacy_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		_ = admin.Close()
	})
	var repos [2]*CustomFilterRepository
	for i := range repos {
		workerConfig := cfg.Copy()
		workerConfig.RuntimeParams["search_path"] = schema
		workerConfig.RuntimeParams["application_name"] = fmt.Sprintf("%s_%d", schema, i)
		workerConfig.RuntimeParams["statement_timeout"] = "10000"
		pool := stdlib.OpenDB(*workerConfig)
		pool.SetMaxOpenConns(4)
		t.Cleanup(func() { _ = pool.Close() })
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		repos[i] = NewCustomFilterRepository(db)
	}
	require.NoError(t, repos[0].db.AutoMigrate(&domain.CustomFilter{}, &domain.LocalChangeJournal{}))
	return repos, schema + "_1"
}

// Hold the first operation after its SELECT FOR UPDATE has acquired the row.
// The other operation uses an independent PostgreSQL pool, and pg_stat_activity
// proves that it is waiting on a real database lock before the first proceeds.
func holdLegacyFilterRead(t *testing.T, db *gorm.DB) (<-chan struct{}, func()) {
	t.Helper()
	locked, proceed := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	var once sync.Once
	release := func() { once.Do(func() { close(proceed) }) }
	t.Cleanup(release)
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("hold_legacy_filter_read", func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "custom_filters" || !held.CompareAndSwap(false, true) {
			return
		}
		close(locked)
		select {
		case <-proceed:
		case <-tx.Statement.Context.Done():
			_ = tx.AddError(tx.Statement.Context.Err())
		}
	}))
	return locked, release
}

func awaitLegacyFilterLock(t *testing.T, ctx context.Context, locked <-chan struct{}) {
	t.Helper()
	select {
	case <-locked:
	case <-ctx.Done():
		t.Fatal("first custom filter operation did not acquire its row lock")
	}
}

func requireLegacyFilterWaiter(t *testing.T, db *gorm.DB, applicationName string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var count int64
		err := db.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND application_name = ? AND state = 'active' AND wait_event_type = 'Lock'", applicationName).Scan(&count).Error
		return err == nil && count == 1
	}, 5*time.Second, 10*time.Millisecond, "second pool must wait for the first operation's PostgreSQL row lock")
}

func TestPostgresCustomFilterLegacyConcurrentPartialUpdates(t *testing.T) {
	repos, secondApplication := postgresLegacyFilterRepositories(t)
	row := legacyFilterSeed(t, repos[0].db, 91, 7, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	locked, release := holdLegacyFilterRead(t, repos[0].db)
	name, query := "concurrent rename", `{"priority":"urgent"}`
	type outcome struct {
		row *domain.CustomFilter
		err error
	}
	first, second := make(chan outcome, 1), make(chan outcome, 1)
	go func() {
		updated, err := repos[0].UpdateLegacy(ctx, 91, 7, row.ID, CustomFilterPatch{Name: &name})
		first <- outcome{updated, err}
	}()
	awaitLegacyFilterLock(t, ctx, locked)
	go func() {
		updated, err := repos[1].UpdateLegacy(ctx, 91, 7, row.ID, CustomFilterPatch{Query: &query})
		second <- outcome{updated, err}
	}()
	requireLegacyFilterWaiter(t, repos[0].db, secondApplication)
	release()
	a, b := <-first, <-second
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	require.Equal(t, name, a.row.Name)
	require.Equal(t, row.Query, a.row.Query)
	require.Equal(t, name, b.row.Name)
	require.Equal(t, query, b.row.Query)
	stored, err := repos[0].Find(ctx, 91, 7, row.ID)
	require.NoError(t, err)
	require.Equal(t, b.row, stored)
	require.Equal(t, row.FilterType, stored.FilterType)
	require.Zero(t, stored.ObjectVersion)
	_, journal := legacyFilterSnapshot(t, repos[0].db)
	require.Empty(t, journal)
}

func TestPostgresCustomFilterLegacyUpdateDeleteNeverResurrects(t *testing.T) {
	for _, deleteFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete_first_%t", deleteFirst), func(t *testing.T) {
			repos, secondApplication := postgresLegacyFilterRepositories(t)
			row := legacyFilterSeed(t, repos[0].db, 91, 7, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			locked, release := holdLegacyFilterRead(t, repos[0].db)
			type outcome struct {
				row     *domain.CustomFilter
				deleted bool
				err     error
			}
			updateResult, deleteResult := make(chan outcome, 1), make(chan outcome, 1)
			update := func(r *CustomFilterRepository) {
				name := "pending rename"
				updated, err := r.UpdateLegacy(ctx, 91, 7, row.ID, CustomFilterPatch{Name: &name})
				updateResult <- outcome{row: updated, err: err}
			}
			remove := func(r *CustomFilterRepository) {
				deleted, err := r.DeleteLegacy(ctx, 91, 7, row.ID)
				deleteResult <- outcome{deleted: deleted, err: err}
			}
			if deleteFirst {
				go remove(repos[0])
			} else {
				go update(repos[0])
			}
			awaitLegacyFilterLock(t, ctx, locked)
			if deleteFirst {
				go update(repos[1])
			} else {
				go remove(repos[1])
			}
			requireLegacyFilterWaiter(t, repos[0].db, secondApplication)
			release()
			updated, deleted := <-updateResult, <-deleteResult
			require.NoError(t, deleted.err)
			require.True(t, deleted.deleted)
			if deleteFirst {
				require.ErrorIs(t, updated.err, gorm.ErrRecordNotFound)
				require.Nil(t, updated.row)
			} else {
				require.NoError(t, updated.err)
				require.Equal(t, "pending rename", updated.row.Name)
			}
			rows, journal := legacyFilterSnapshot(t, repos[0].db)
			require.Empty(t, rows, "a waiting partial update must never recreate a deleted filter")
			require.Empty(t, journal)
		})
	}
}
