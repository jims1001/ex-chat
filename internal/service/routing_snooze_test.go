package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	applog "github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func routingSnoozeFixture(t *testing.T, capture bool) (*RoutingService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "snooze.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.Conversation{}, &domain.LocalChangeJournal{}))
	con := repository.NewConversationRepositoryWithReadPorts(db, nil, nil, nil, nil, nil, nil, nil)
	con.SetAutomationCapture(capture)
	return NewRoutingService(db, con, nil), db
}

func routingSnoozeRow(t *testing.T, db *gorm.DB, deadline time.Time) domain.Conversation {
	t.Helper()
	c := domain.Conversation{AccountID: 91, Status: "snoozed", SnoozedUntil: &deadline, ObjectVersion: 1}
	require.NoError(t, db.Create(&c).Error)
	return c
}

func TestRoutingSnoozePublishesPartialCommittedWinners(t *testing.T) {
	s, db := routingSnoozeFixture(t, true)
	s.hub = ws.NewHub()
	first := routingSnoozeRow(t, db, time.Now().UTC().Add(-time.Minute))
	second := routingSnoozeRow(t, db, time.Now().UTC().Add(-time.Minute))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject_second_expiry", func(tx *gorm.DB) {
		if change, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && change.Action == "automation_trigger" && change.ObjectID == second.ID {
			_ = tx.AddError(errors.New("injected capture failure"))
		}
	}))
	winners, err := s.ResumeSnoozedConversations(context.Background())
	require.ErrorContains(t, err, "injected capture failure")
	require.Len(t, winners, 1)
	require.Equal(t, first.ID, winners[0].ID)
	// Observe only queue length: no endless Hub.Run goroutine is needed.
	require.Equal(t, 1, reflect.ValueOf(s.hub).Elem().FieldByName("broadcast").Len())
	require.NoError(t, db.Callback().Create().Remove("reject_second_expiry"))
	winners, err = s.ResumeSnoozedConversations(context.Background())
	require.NoError(t, err)
	require.Len(t, winners, 1)
	require.Equal(t, second.ID, winners[0].ID)
	require.Equal(t, 2, reflect.ValueOf(s.hub).Elem().FieldByName("broadcast").Len())
}

func TestRoutingSnoozeSchedulerStartupTickCancelAndRestart(t *testing.T) {
	s, db := routingSnoozeFixture(t, true)
	first := routingSnoozeRow(t, db, time.Now().UTC().Add(-time.Minute))
	second := routingSnoozeRow(t, db, time.Now().UTC().Add(60*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.runSnoozeScheduler(ctx, 20*time.Millisecond) }()
	t.Cleanup(func() { cancel(); <-done })
	for _, expected := range []domain.Conversation{first, second} {
		require.Eventually(t, func() bool {
			var current domain.Conversation
			return db.First(&current, expected.ID).Error == nil && current.Status == "open" && current.SnoozedUntil == nil && current.ObjectVersion == 2
		}, time.Second, 5*time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	third := routingSnoozeRow(t, db, time.Now().UTC().Add(-time.Minute))
	time.Sleep(50 * time.Millisecond)
	var current domain.Conversation
	require.NoError(t, db.First(&current, third.ID).Error)
	require.Equal(t, "snoozed", current.Status)
	// A new worker recovers overdue state on its first pass, not after a tick.
	restarted := NewRoutingService(db, s.convRepo, nil)
	next, stop := context.WithCancel(context.Background())
	defer stop()
	restarted.StartSnoozeScheduler(next, time.Hour)
	require.Eventually(t, func() bool {
		current = domain.Conversation{}
		return db.First(&current, third.ID).Error == nil && current.ObjectVersion == 2 && current.Status == "open"
	}, time.Second, 5*time.Millisecond)
}

func TestRoutingSnoozeSchedulerRequiresLiveContextAndSafeInterval(t *testing.T) {
	s, db := routingSnoozeFixture(t, false)
	first := routingSnoozeRow(t, db, time.Now().UTC().Add(-time.Minute))
	s.StartSnoozeScheduler(nil, time.Nanosecond)
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	s.StartSnoozeScheduler(cancelled, time.Nanosecond)
	time.Sleep(30 * time.Millisecond)
	var current domain.Conversation
	require.NoError(t, db.First(&current, first.ID).Error)
	require.Equal(t, 1, current.ObjectVersion)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.runSnoozeScheduler(ctx, 0) }()
	require.Eventually(t, func() bool {
		current = domain.Conversation{}
		return db.First(&current, first.ID).Error == nil && current.Status == "open"
	}, time.Second, 5*time.Millisecond)
	second := routingSnoozeRow(t, db, time.Now().UTC().Add(-time.Minute))
	time.Sleep(30 * time.Millisecond)
	current = domain.Conversation{}
	require.NoError(t, db.First(&current, second.ID).Error)
	require.Equal(t, 1, current.ObjectVersion, "invalid interval must not create a busy loop")
	cancel()
	<-done
}

type snoozeLogBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *snoozeLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *snoozeLogBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

func TestRoutingSnoozeSchedulerReportsFailureAndRecovers(t *testing.T) {
	s, db := routingSnoozeFixture(t, true)
	first := routingSnoozeRow(t, db, time.Now().UTC().Add(-time.Minute))
	var output snoozeLogBuffer
	applog.SetOutput(&output, slog.LevelInfo, "json")
	defer applog.SetOutput(os.Stdout, slog.LevelInfo, "json")
	var failureMu sync.Mutex
	fail := true
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("transient_snooze_failure", func(tx *gorm.DB) {
		failureMu.Lock()
		defer failureMu.Unlock()
		if change, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && fail && change.Action == "automation_trigger" {
			_ = tx.AddError(errors.New("private error details must not appear in scheduler log"))
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.runSnoozeScheduler(ctx, 20*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	require.Eventually(t, func() bool { return strings.Contains(output.String(), "expired snooze recovery deferred") }, time.Second, 5*time.Millisecond)
	require.NotContains(t, output.String(), "private error details")
	failureMu.Lock()
	fail = false
	failureMu.Unlock()
	require.Eventually(t, func() bool {
		var current domain.Conversation
		return db.First(&current, first.ID).Error == nil && current.ObjectVersion == 2 && current.Status == "open"
	}, time.Second, 5*time.Millisecond)
}
