package repository

import (
	"context"
	"errors"
	"net"
	"os"
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

func postgresSnoozeRepositories(t *testing.T) []*ConversationRepository {
	t.Helper()
	if os.Getenv("EX_CHAT_AUT_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUT_POSTGRES_TESTS=1 and loopback DB_DSN")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DB_DSN"))
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	schema := "con_snooze_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		_ = admin.Close()
	})
	cfg.RuntimeParams["search_path"] = schema
	repos := make([]*ConversationRepository, 2)
	for i := range repos {
		pool := stdlib.OpenDB(*cfg)
		pool.SetMaxOpenConns(8)
		t.Cleanup(func() { _ = pool.Close() })
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
		require.NoError(t, err)
		repos[i] = &ConversationRepository{db: db}
		repos[i].SetAutomationCapture(true)
	}
	require.NoError(t, repos[0].db.AutoMigrate(&domain.Conversation{}, &domain.LocalChangeJournal{}))
	return repos
}

func TestPostgresConversationSnoozeConcurrentWorkers(t *testing.T) {
	repos := postgresSnoozeRepositories(t)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 12; i++ {
		snoozeCandidate(t, repos[0], uint(91+i%2), cutoff)
	}
	const n = 8
	var workers sync.WaitGroup
	results := make([][]domain.Conversation, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			results[i], errs[i] = repos[i%2].ResumeExpiredSnoozed(context.Background(), cutoff)
		}(i)
	}
	close(start)
	workers.Wait()
	ids := map[uint]bool{}
	for i, err := range errs {
		require.NoError(t, err)
		for _, winner := range results[i] {
			require.False(t, ids[winner.ID], "independent workers must never publish the same transition")
			ids[winner.ID] = true
			require.Equal(t, 8, winner.ObjectVersion)
			require.Nil(t, winner.SnoozedUntil)
		}
	}
	require.Len(t, ids, 12)
	facts, err := repos[0].PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 12)
	for _, fact := range facts {
		require.Equal(t, "snoozed", fact.Payload["status_before"])
		require.Equal(t, "open", fact.Payload["status_after"])
		require.EqualValues(t, 8, fact.AggregateVersion)
	}
}

func TestPostgresConversationSnoozeManualInterleaves(t *testing.T) {
	for _, change := range []string{"open", "resolved", "deleting", "delete", "extend", "resnooze"} {
		t.Run(change, func(t *testing.T) {
			repos := postgresSnoozeRepositories(t)
			cutoff := time.Now().UTC().Truncate(time.Microsecond)
			candidate := snoozeCandidate(t, repos[0], 91, cutoff)
			scanned, release := make(chan struct{}), make(chan struct{})
			require.NoError(t, repos[0].db.Callback().Query().After("gorm:query").Register("hold_expiry_scan", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]domain.Conversation); ok && tx.Statement.Table == "conversations" {
					close(scanned)
					<-release
				}
			}))
			var winners []domain.Conversation
			var expiryErr error
			done := make(chan struct{})
			go func() {
				defer close(done)
				winners, expiryErr = repos[0].ResumeExpiredSnoozed(context.Background(), cutoff)
			}()
			select {
			case <-scanned:
			case <-time.After(5 * time.Second):
				t.Fatal("expiry did not scan")
			}
			switch change {
			case "delete":
				require.NoError(t, repos[1].db.Delete(&candidate).Error)
			case "extend":
				require.NoError(t, repos[1].db.Model(&candidate).Update("snoozed_until", cutoff.Add(time.Hour)).Error)
			case "resnooze":
				past := cutoff.Add(-time.Second)
				require.NoError(t, repos[1].UpdateStatus(91, candidate.ID, "snoozed", &past))
			default:
				require.NoError(t, repos[1].UpdateStatus(91, candidate.ID, change, nil))
			}
			var before domain.Conversation
			errBefore := repos[1].db.First(&before, candidate.ID).Error
			var countBefore int64
			require.NoError(t, repos[1].db.Model(&domain.LocalChangeJournal{}).Count(&countBefore).Error)
			close(release)
			<-done
			require.NoError(t, expiryErr)
			require.Empty(t, winners)
			var after domain.Conversation
			require.Equal(t, errBefore, repos[1].db.First(&after, candidate.ID).Error)
			require.Equal(t, before, after)
			var countAfter int64
			require.NoError(t, repos[1].db.Model(&domain.LocalChangeJournal{}).Count(&countAfter).Error)
			require.Equal(t, countBefore, countAfter)
		})
	}
}

func TestPostgresConversationSnoozeRollbackReturnsCommittedWinners(t *testing.T) {
	repos := postgresSnoozeRepositories(t)
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	first := snoozeCandidate(t, repos[0], 91, cutoff)
	second := snoozeCandidate(t, repos[0], 91, cutoff)
	require.NoError(t, repos[0].db.Callback().Create().Before("gorm:create").Register("reject_expiry_fact", func(tx *gorm.DB) {
		if change, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && change.Action == "automation_trigger" && change.ObjectID == second.ID {
			_ = tx.AddError(errors.New("expiry journal unavailable"))
		}
	}))
	winners, err := repos[0].ResumeExpiredSnoozed(context.Background(), cutoff)
	require.ErrorContains(t, err, "expiry journal unavailable")
	require.Len(t, winners, 1)
	require.Equal(t, first.ID, winners[0].ID)
	var unchanged domain.Conversation
	require.NoError(t, repos[1].db.First(&unchanged, second.ID).Error)
	require.Equal(t, second.Status, unchanged.Status)
	require.Equal(t, second.ObjectVersion, unchanged.ObjectVersion)
	require.True(t, second.SnoozedUntil.Equal(*unchanged.SnoozedUntil))
	require.True(t, second.LastActivityAt.Equal(unchanged.LastActivityAt))
	// Another process can recover the pending row without rerunning the winner.
	winners, err = repos[1].ResumeExpiredSnoozed(context.Background(), cutoff)
	require.NoError(t, err)
	require.Len(t, winners, 1)
	require.Equal(t, second.ID, winners[0].ID)
	facts, err := repos[1].PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 2)
}
