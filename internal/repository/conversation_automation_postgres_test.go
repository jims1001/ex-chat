package repository

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Explicit opt-in, loopback only. This never starts or changes a PG service;
// every test object belongs to its generated disposable schema.
func TestPostgresConversationAutomationTransactions(t *testing.T) {
	if os.Getenv("EX_CHAT_AUT_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUT_POSTGRES_TESTS=1 and loopback DB_DSN")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DB_DSN"))
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	schema := "con_aut_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	conversation := automationTestConversation(t, repos[0])
	parallel := func(n int, action func(int, *ConversationRepository) error) []error {
		errs := make([]error, n)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for i := range errs {
			workers.Add(1)
			go func(i int) {
				defer workers.Done()
				<-start
				errs[i] = action(i, repos[i%2])
			}(i)
		}
		close(start)
		workers.Wait()
		return errs
	}
	for _, err := range parallel(16, func(i int, repo *ConversationRepository) error {
		return repo.UpdatePriority(conversation.AccountID, conversation.ID, fmt.Sprintf("test-%d", i))
	}) {
		require.NoError(t, err)
	}
	var current domain.Conversation
	require.NoError(t, repos[0].db.First(&current, conversation.ID).Error)
	require.Equal(t, 17, current.ObjectVersion)
	facts, err := repos[1].PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 17)
	versions := make(map[uint64]bool)
	for _, fact := range facts {
		require.False(t, versions[fact.AggregateVersion], "each committed version has exactly one fact")
		versions[fact.AggregateVersion] = true
	}
	wins := 0
	for _, err := range parallel(8, func(i int, repo *ConversationRepository) error {
		candidate := current
		candidate.Priority = fmt.Sprintf("stale-%d", i)
		return repo.Update(&candidate)
	}) {
		if err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, ErrConversationVersionConflict)
		}
	}
	require.Equal(t, 1, wins)
	for _, err := range parallel(8, func(_ int, repo *ConversationRepository) error {
		return repo.MarkAutomationFactDelivered(context.Background(), facts[0].EventID)
	}) {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, repos[0].db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND event_id = ?", "automation_delivery", facts[0].EventID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	facts, err = repos[1].PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 17, "18 committed facts minus one delivered receipt")
}
