package repository

import (
	"context"
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

// Opt-in disposable loopback fixture only. Independent connections/stores prove
// owner row locks and CAS; no process-local retry mutex serializes the workers.
func TestPostgresExplicitRetryTransactions(t *testing.T) {
	if os.Getenv("EX_CHAT_AUT_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUT_POSTGRES_TESTS=1 and loopback DB_DSN")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DB_DSN"))
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	t.Cleanup(func() { _ = admin.Close() })
	prefix := "retry_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	open := func(owner string) *gorm.DB {
		schema := prefix + "_" + owner
		_, err := admin.Exec("CREATE SCHEMA IF NOT EXISTS " + schema)
		require.NoError(t, err)
		clone := cfg.Copy()
		clone.RuntimeParams["search_path"] = schema
		pool := stdlib.OpenDB(*clone)
		pool.SetMaxOpenConns(8)
		t.Cleanup(func() { _ = pool.Close() })
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
		require.NoError(t, err)
		return db
	}
	t.Cleanup(func() {
		for _, owner := range []string{"msg", "con"} {
			_, err := admin.Exec("DROP SCHEMA " + prefix + "_" + owner + " CASCADE")
			require.NoError(t, err)
		}
	})
	con1, msg1 := open("con"), open("msg")
	require.NoError(t, con1.AutoMigrate(&domain.Conversation{}, &domain.LocalChangeJournal{}))
	require.NoError(t, msg1.AutoMigrate(&domain.Message{}, &domain.LocalChangeJournal{}))
	con2, msg2 := open("con"), open("msg")
	r1 := NewMessageRepositoryWithConversationPort(msg1, NewConversationRepository(con1))
	r1.SetAutomationCapture(true)
	r2 := NewMessageRepositoryWithConversationPort(msg2, NewConversationRepository(con2))
	r2.SetAutomationCapture(true)
	c := domain.Conversation{AccountID: 1, ContactID: 1, InboxID: 1, DisplayID: 1, ObjectVersion: 7, Status: "resolved", UnreadCount: 3, Muted: true}
	require.NoError(t, con1.Create(&c).Error)
	m := retryFailedMessage(t, msg1, c, "incoming", false)
	const n = 16
	errs := make([]error, n)
	accepted := make([]bool, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for j := 0; j < n; j++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			<-start
			r := r1
			if j%2 != 0 {
				r = r2
			}
			_, accepted[j], errs[j] = r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
		}(j)
	}
	close(start)
	wg.Wait()
	wins := 0
	for j, err := range errs {
		require.NoError(t, err)
		if accepted[j] {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	require.EqualValues(t, 1, retryCount(t, msg1, "retry_attempt"))
	require.EqualValues(t, 1, retryCount(t, con1, "retry_activity"))
	var after domain.Conversation
	require.NoError(t, con1.First(&after, c.ID).Error)
	require.Equal(t, 8, after.ObjectVersion)
	require.Equal(t, 3, after.UnreadCount)
	require.Equal(t, "open", after.Status)
	facts, err := r2.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, 8, facts[0].Payload["object_version"])
	require.NoError(t, r1.MarkAutomationFactDelivered(context.Background(), facts[0].EventID))
	require.NoError(t, msg1.Model(&domain.Message{}).Where("id = ?", m.ID).Update("status", "failed").Error)
	_, win, err := r2.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
	require.NoError(t, err)
	require.True(t, win)
	require.EqualValues(t, 2, retryCount(t, msg1, "retry_attempt"))
	require.EqualValues(t, 2, retryCount(t, con1, "retry_activity"))
	require.False(t, msg1.Migrator().HasTable(&domain.Conversation{}))
	require.False(t, con1.Migrator().HasTable(&domain.Message{}))
	// Interleave an independent manual owner command after the committed MSG
	// acceptance. The old expected version is retained, never rebased.
	pending := retryFailedMessage(t, msg1, c, "outgoing", false)
	port := &retryTestPort{ConversationRepository: NewConversationRepository(con2), fail: true}
	delayed := NewMessageRepositoryWithConversationPort(msg2, port)
	delayed.SetAutomationCapture(true)
	_, _, err = delayed.RetryMessageAccepted(context.Background(), 1, c.ID, pending.ID)
	require.NoError(t, err)
	require.NoError(t, NewConversationRepository(con1).UpdateStatus(1, c.ID, "resolved", nil))
	port.fail = false
	pendingFacts, err := delayed.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	found := false
	for _, f := range pendingFacts {
		if f.Payload["retry_activity_result"] == "stale" {
			found = true
			require.Equal(t, 9, f.Payload["object_version"])
			require.NotContains(t, f.Payload, "status_before")
		}
	}
	require.True(t, found)
	// Commit the receipt but lose its response, then concurrently recover through
	// fresh MSG/CON connections. Every reader observes one immutable owner effect.
	lost := retryFailedMessage(t, msg1, c, "incoming", true)
	port.loseResponse = true
	_, win, err = delayed.RetryMessageAccepted(context.Background(), 1, c.ID, lost.ID)
	require.NoError(t, err)
	require.True(t, win)
	require.EqualValues(t, 4, retryCount(t, con1, "retry_activity"))
	port.loseResponse = false
	scanErrors := make([]error, 8)
	start = make(chan struct{})
	for j := range scanErrors {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			<-start
			r := r1
			if j%2 != 0 {
				r = r2
			}
			_, scanErrors[j] = r.PendingAutomationFacts(context.Background(), 100)
		}(j)
	}
	close(start)
	wg.Wait()
	for _, err := range scanErrors {
		require.NoError(t, err)
	}
	require.EqualValues(t, 4, retryCount(t, con1, "retry_activity"))
	var received domain.LocalChangeJournal
	require.NoError(t, msg1.Where("action = ? AND object_id = ?", "retry_attempt", lost.ID).First(&received).Error)
	require.Equal(t, "delivered", received.Status)

}
