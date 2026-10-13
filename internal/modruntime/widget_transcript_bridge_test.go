package modruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type widgetBridgeFixture struct {
	b       *WidgetTranscriptBridge
	db      *gorm.DB
	req     domain.WidgetTranscriptRequest
	renders atomic.Int32
}

func widgetBridgeDB(t *testing.T, pg bool) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	if pg {
		db = postgresModuleDB(t, "MSG")
	} else {
		db = newModuleTestDB(t, "MSG")
	}
	require.NoError(t, database.AutoMigrate(db))
	return db
}
func widgetBridgeSetup(t *testing.T, pg bool) *widgetBridgeFixture {
	t.Helper()
	db := widgetBridgeDB(t, pg)
	for _, row := range []any{&domain.Account{ID: 91, Name: "Account transcript test"}, &domain.User{ID: 7, Name: "Agent", Email: "agent@example.test"}, &domain.AccountUser{AccountID: 91, UserID: 7, Role: "administrator"}, &domain.Inbox{ID: 5, AccountID: 91, Name: "Inbox", ChannelType: "Channel::WebWidget"}, &domain.Contact{ID: 6, AccountID: 91, Name: "Contact", Email: "recipient@example.test"}, &domain.Conversation{ID: 11, AccountID: 91, InboxID: 5, ContactID: 6, Status: "open"}, &domain.Message{ID: 12, AccountID: 91, ConversationID: 11, Content: "WIDGET_TRANSCRIPT_PUBLIC_BODY", Private: false, MessageType: domain.MessageTypeOutgoing}} {
		require.NoError(t, db.Create(row).Error)
	}
	b, err := NewWidgetTranscriptBridge(db, widgetBridgeEnv)
	require.NoError(t, err)
	return &widgetBridgeFixture{b: b, db: db, req: domain.WidgetTranscriptRequest{AccountID: 91, InboxID: 5, ContactID: 6, SourceID: "signed-widget-source", ConversationID: 11, Key: "widget-request-key", Email: "recipient@example.test"}}
}
func widgetBridgeEnv(key string) string {
	return map[string]string{"WIDGET_TRANSCRIPT_QUEUE_ENABLED": "true", "ORDINARY_EMAIL_WORKER_ENABLED": "true", "SMTP_ADDRESS": "127.0.0.1", "SMTP_PORT": "2525", "MAILER_SENDER_EMAIL": "sender@example.test"}[key]
}
func (f *widgetBridgeFixture) authorize(context.Context) error { return nil }
func (f *widgetBridgeFixture) prepare(context.Context) (domain.WidgetTranscriptPrepared, error) {
	return domain.WidgetTranscriptPrepared{Eligible: true, From: "sender@example.test", To: f.req.Email, Subject: "Account transcript", Render: func(messages []domain.Message) (string, string) {
		f.renders.Add(1)
		var body strings.Builder
		for _, m := range messages {
			body.WriteString(m.Content)
		}
		return "<p>" + body.String() + "</p>", body.String()
	}}, nil
}
func (f *widgetBridgeFixture) submit(t *testing.T) *domain.WidgetTranscriptAdmission {
	t.Helper()
	result, handled, err := f.b.Submit(context.Background(), f.req, f.authorize, f.prepare)
	require.NoError(t, err)
	require.True(t, handled)
	require.NotNil(t, result)
	return result
}
func (f *widgetBridgeFixture) item(t *testing.T) domain.OrdinaryEmailIntent {
	t.Helper()
	var item domain.OrdinaryEmailIntent
	require.NoError(t, f.db.First(&item).Error)
	return item
}

func TestWidgetTranscriptBridgeAtomicReplayAndConcurrency(t *testing.T) {
	widgetBridgeConcurrency(t, false)
}
func widgetBridgeConcurrency(t *testing.T, pg bool) {
	f := widgetBridgeSetup(t, pg)
	var wg sync.WaitGroup
	type result struct {
		a       *domain.WidgetTranscriptAdmission
		handled bool
		err     error
	}
	out := make(chan result, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, h, e := f.b.Submit(context.Background(), f.req, f.authorize, f.prepare)
			out <- result{a, h, e}
		}()
	}
	wg.Wait()
	close(out)
	var id uint
	for r := range out {
		require.NoError(t, r.err)
		require.True(t, r.handled)
		require.NotNil(t, r.a)
		if id == 0 {
			id = r.a.EmailLogID
		}
		require.Equal(t, id, r.a.EmailLogID)
	}
	require.Equal(t, int32(1), f.renders.Load())
	item := f.item(t)
	require.NotNil(t, item.RequestScopeHash)
	require.NotEmpty(t, item.RequestInputHash)
	require.LessOrEqual(t, len(item.CommandID), 64)
	require.LessOrEqual(t, len(item.CorrelationID), 64)
	var journals []domain.LocalChangeJournal
	require.NoError(t, f.db.Find(&journals).Error)
	for _, j := range journals {
		require.LessOrEqual(t, len(j.CommandID), 64)
		require.LessOrEqual(t, len(j.CorrelationID), 64)
		require.LessOrEqual(t, len(j.CausationID), 64)
	}
	var receipts int64
	require.NoError(t, f.db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", 12).Update("content", "LATER_PRIVATE_CONTENT").Error)
	f.b.enabled = false
	f.b.runtime.Config.OrdinaryEmail.Enabled = false
	replay, handled, err := f.b.Submit(context.Background(), f.req, f.authorize, func(context.Context) (domain.WidgetTranscriptPrepared, error) {
		t.Error("known request was rendered/prepared again")
		return domain.WidgetTranscriptPrepared{}, nil
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, id, replay.EmailLogID)
	require.Equal(t, item.Payload, f.item(t).Payload)
	changed := f.req
	changed.Email = "changed@example.test"
	_, handled, err = f.b.Submit(context.Background(), changed, f.authorize, f.prepare)
	require.True(t, handled)
	require.ErrorIs(t, err, domain.ErrAccountTranscriptConflict)
}

func TestWidgetTranscriptBridgeLostAdmissionAcknowledgement(t *testing.T) {
	widgetBridgeLostAck(t, false)
}
func widgetBridgeLostAck(t *testing.T, pg bool) {
	f := widgetBridgeSetup(t, pg)
	once := &atomic.Bool{}
	wrapped := f.db.Session(&gorm.Session{NewDB: true})
	wrapped.Statement.ConnPool = &lostAdmissionPool{ConnPool: f.db.Statement.ConnPool, once: once}
	f.b.db = wrapped
	first := f.submit(t)
	require.True(t, once.Load())
	require.Equal(t, first, f.submit(t))
	require.Equal(t, int32(1), f.renders.Load())
	var n int64
	require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
}

func TestWidgetTranscriptBridgeAnonymizationAndDeletion(t *testing.T) {
	widgetBridgeLifecycle(t, false)
}
func widgetBridgeLifecycle(t *testing.T, pg bool) {
	t.Run("anonymized_key", func(t *testing.T) {
		f := widgetBridgeSetup(t, pg)
		a := f.submit(t)
		r := *f.b.runtime
		require.Equal(t, 200, ordinaryAnonymize(t, r, "scrub-account-transcript").Code)
		item := f.item(t)
		require.True(t, item.Anonymized)
		require.Empty(t, item.Payload)
		require.Empty(t, item.RequestInputHash)
		require.Zero(t, item.TranscriptMessages)
		require.NotNil(t, item.RequestScopeHash)
		f.b.enabled = false
		_, handled, err := f.b.Submit(context.Background(), f.req, f.authorize, f.prepare)
		require.True(t, handled)
		require.ErrorIs(t, err, domain.ErrAccountTranscriptConflict)
		require.Equal(t, int32(1), f.renders.Load())
		view, err := f.b.Status(context.Background(), f.req, a.EmailLogID, f.authorize)
		require.ErrorIs(t, err, domain.ErrAccountTranscriptNotFound)
		require.Nil(t, view)
	})
	t.Run("deleted_conversation", func(t *testing.T) {
		f := widgetBridgeSetup(t, pg)
		a := f.submit(t)
		require.NoError(t, repository.NewCombinedConversationCleanup(f.db).CleanupConversation(91, 11))
		var n int64
		require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
		require.Zero(t, n)
		_, handled, err := f.b.Submit(context.Background(), f.req, f.authorize, f.prepare)
		require.True(t, handled)
		require.ErrorIs(t, err, domain.ErrAccountTranscriptConflict)
		_, err = f.b.Status(context.Background(), f.req, a.EmailLogID, f.authorize)
		require.ErrorIs(t, err, domain.ErrAccountTranscriptNotFound)
	})
}

func TestWidgetTranscriptBridgeSnapshotAndVersionCompatibility(t *testing.T) {
	widgetBridgeSnapshot(t, false)
}
func widgetBridgeSnapshot(t *testing.T, pg bool) {
	f := widgetBridgeSetup(t, pg)
	a := f.submit(t)
	source := f.item(t)
	path := filepath.Join(t.TempDir(), "transcript.json")
	require.NoError(t, CreateSnapshot(f.db, "MSG", "transcript-request", path))
	target := ordinaryDB(t, pg)
	if target == nil {
		target = newModuleTestDB(t, "MSG")
	}
	require.NoError(t, RestoreSnapshot(context.Background(), target, "MSG", "transcript-request", path, nil))
	var restored domain.OrdinaryEmailIntent
	require.NoError(t, target.First(&restored).Error)
	require.Equal(t, "unknown", restored.State)
	require.Equal(t, source.RequestScopeHash, restored.RequestScopeHash)
	b, err := NewWidgetTranscriptBridge(target, widgetBridgeEnv)
	require.NoError(t, err)
	replay, handled, err := b.Submit(context.Background(), f.req, f.authorize, func(context.Context) (domain.WidgetTranscriptPrepared, error) {
		t.Fatal("restored request rendered again")
		return domain.WidgetTranscriptPrepared{}, nil
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, a, replay)
	worker := Runtime{DB: target, Config: Config{ModuleID: "MSG", OrdinaryEmail: b.runtime.Config.OrdinaryEmail}, OrdinaryEmailTransport: ordinaryTestTransport(func(context.Context, func() error) domain.OrdinarySMTPResult {
		t.Error("restored request was sent")
		return domain.OrdinarySMTPResult{}
	})}
	ran, err := worker.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	// Real phase38 rows had no request identity. All original private fields
	// remain required, and a v2 requester cannot be disguised as such a v1 row.
	generic := ordinaryRuntime(t, ordinaryDB(t, pg))
	old := ordinaryEnqueue(t, generic, "phase38-ordinary")
	raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{old})
	require.NoError(t, err)
	var table map[string]any
	require.NoError(t, json.Unmarshal(raw, &table))
	table["codec"] = "msg.ordinary_email_intent.v1"
	row := table["rows"].([]any)[0].(map[string]any)
	private := row["private"].(map[string]any)
	delete(private, "request")
	legacy, _ := json.Marshal(table)
	var rows []domain.OrdinaryEmailIntent
	require.NoError(t, unmarshalOrdinaryIntents(legacy, &rows))
	require.Nil(t, rows[0].RequestScopeHash)
	oldPath := filepath.Join(t.TempDir(), "phase38-v1.json")
	require.NoError(t, CreateSnapshot(generic.DB, "MSG", "phase38-v1", oldPath))
	fileRaw, e := os.ReadFile(oldPath)
	require.NoError(t, e)
	var file snapshotFile
	require.NoError(t, json.Unmarshal(fileRaw, &file))
	file.Snapshot.Tables["ordinary_email_intents"] = legacy
	payload, e := json.Marshal(file.Snapshot)
	require.NoError(t, e)
	sum := sha256.Sum256(payload)
	file.Checksum = hex.EncodeToString(sum[:])
	fileRaw, e = json.Marshal(file)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(oldPath, fileRaw, 0600))
	oldTarget := ordinaryDB(t, pg)
	if oldTarget == nil {
		oldTarget = newModuleTestDB(t, "MSG")
	}
	require.NoError(t, RestoreSnapshot(context.Background(), oldTarget, "MSG", "phase38-v1", oldPath, nil))
	var oldRestored domain.OrdinaryEmailIntent
	require.NoError(t, oldTarget.First(&oldRestored).Error)
	require.Equal(t, "unknown", oldRestored.State)
	require.Nil(t, oldRestored.RequestScopeHash)

	delete(private, "payload_hash")
	missing, _ := json.Marshal(table)
	require.Error(t, unmarshalOrdinaryIntents(missing, &rows))
	current, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{source})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(current, &table))
	table["codec"] = "msg.ordinary_email_intent.v2"
	row = table["rows"].([]any)[0].(map[string]any)
	delete(row["private"].(map[string]any), "request")
	downgrade, _ := json.Marshal(table)
	require.Error(t, unmarshalOrdinaryIntents(downgrade, &rows))
	require.NoError(t, json.Unmarshal(current, &table))
	row = table["rows"].([]any)[0].(map[string]any)
	delete(row["private"].(map[string]any), "request")
	missing, _ = json.Marshal(table)
	require.Error(t, unmarshalOrdinaryIntents(missing, &rows))
}

func TestWidgetTranscriptBridgePrivateSQLAndRollback(t *testing.T) {
	f := widgetBridgeSetup(t, false)
	var captured bytes.Buffer
	db := f.db.Session(&gorm.Session{Logger: logger.New(log.New(&captured, "", 0), logger.Config{LogLevel: logger.Info, SlowThreshold: time.Nanosecond})})
	var err error
	f.b, err = NewWidgetTranscriptBridge(db, widgetBridgeEnv)
	require.NoError(t, err)
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("fail_request_mapping", func(tx *gorm.DB) {
		if tx.Statement.Table == "ordinary_email_intents" {
			tx.AddError(errors.New("WIDGET_TRANSCRIPT_PUBLIC_BODY recipient@example.test"))
		}
	}))
	_, handled, err := f.b.Submit(context.Background(), f.req, f.authorize, f.prepare)
	require.True(t, handled)
	require.ErrorIs(t, err, domain.ErrAccountTranscriptUnavailable)
	require.NotContains(t, err.Error(), "WIDGET_TRANSCRIPT_PUBLIC_BODY")
	require.NotContains(t, captured.String(), "WIDGET_TRANSCRIPT_PUBLIC_BODY")
	require.NotContains(t, captured.String(), "recipient@example.test")
	for _, model := range []any{&domain.EmailLog{}, &domain.OrdinaryEmailIntent{}, &domain.LocalChangeJournal{}} {
		var count int64
		require.NoError(t, f.db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestPostgresWidgetTranscriptBridgeLifecycle(t *testing.T) {
	t.Run("concurrent_keys", func(t *testing.T) { widgetBridgeConcurrency(t, true) })
	t.Run("lost_admission_ack", func(t *testing.T) { widgetBridgeLostAck(t, true) })
	t.Run("lifecycle", func(t *testing.T) { widgetBridgeLifecycle(t, true) })
	t.Run("snapshot", func(t *testing.T) { widgetBridgeSnapshot(t, true) })
	t.Run("worker_restart_unknown", func(t *testing.T) { widgetBridgeUnknown(t, true) })
	t.Run("deadline_fence", func(t *testing.T) { widgetBridgeDeadline(t, true) })
	t.Run("deletion_after_capture", func(t *testing.T) { widgetBridgeDeletionRace(t) })
	t.Run("anonymize_before_capture", func(t *testing.T) { widgetBridgeAnonymizeRace(t, true) })
	t.Run("anonymize_after_capture", func(t *testing.T) { widgetBridgeAnonymizeRace(t, false) })
}
func widgetBridgeAnonymizeRace(t *testing.T, anonFirst bool) {
	f := widgetBridgeSetup(t, true)
	locked, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	if anonFirst {
		require.NoError(t, f.db.Callback().Query().After("gorm:query").Register("pause_transcript_anonymize", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*[]domain.OrdinaryEmailIntent); ok && once.CompareAndSwap(false, true) {
				close(locked)
				<-release
			}
		}))
		done := make(chan int, 1)
		go func() { done <- ordinaryAnonymize(t, *f.b.runtime, "scrub-before-capture").Code }()
		<-locked
		sent := make(chan error, 1)
		go func() { _, _, err := f.b.Submit(context.Background(), f.req, f.authorize, f.prepare); sent <- err }()
		select {
		case err := <-sent:
			t.Fatalf("capture crossed scrub fence: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		close(release)
		require.Equal(t, 200, <-done)
		require.NoError(t, <-sent)
		require.NotContains(t, f.item(t).Payload, "WIDGET_TRANSCRIPT_PUBLIC_BODY")
	} else {
		prepared := func(ctx context.Context) (domain.WidgetTranscriptPrepared, error) {
			p, e := f.prepare(ctx)
			render := p.Render
			p.Render = func(m []domain.Message) (string, string) { close(locked); <-release; return render(m) }
			return p, e
		}
		sent := make(chan error, 1)
		go func() { _, _, err := f.b.Submit(context.Background(), f.req, f.authorize, prepared); sent <- err }()
		<-locked
		done := make(chan int, 1)
		go func() { done <- ordinaryAnonymize(t, *f.b.runtime, "scrub-after-capture").Code }()
		select {
		case code := <-done:
			t.Fatalf("scrub crossed capture fence: %d", code)
		case <-time.After(50 * time.Millisecond):
		}
		close(release)
		require.NoError(t, <-sent)
		require.Equal(t, 200, <-done)
		require.Empty(t, f.item(t).Payload)
		require.True(t, f.item(t).Anonymized)
	}
}

func TestWidgetTranscriptBridgeWorkerRestartKeepsUnknown(t *testing.T) {
	widgetBridgeUnknown(t, false)
}
func widgetBridgeUnknown(t *testing.T, pg bool) {
	f := widgetBridgeSetup(t, pg)
	count, bodies := ordinarySMTPServer(t, f.b.runtime, true)
	a := f.submit(t)
	require.Zero(t, count.Load())
	ordinaryRun(t, *f.b.runtime)
	require.Contains(t, <-bodies, "WIDGET_TRANSCRIPT_PUBLIC_BODY")
	require.Equal(t, "unknown", f.item(t).State)
	restarted := *f.b.runtime
	ran, err := restarted.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	require.Equal(t, a, f.submit(t))
	require.EqualValues(t, 1, count.Load())
	view, err := f.b.Status(context.Background(), f.req, a.EmailLogID, f.authorize)
	require.NoError(t, err)
	require.Equal(t, "unknown", view["status"])
	require.Equal(t, false, view["manual_retry_allowed"])
}
func widgetBridgeDeletionRace(t *testing.T) {
	f := widgetBridgeSetup(t, true)
	locked, release := make(chan struct{}), make(chan struct{})
	prepared := func(ctx context.Context) (domain.WidgetTranscriptPrepared, error) {
		p, e := f.prepare(ctx)
		render := p.Render
		p.Render = func(m []domain.Message) (string, string) { close(locked); <-release; return render(m) }
		return p, e
	}
	sent := make(chan error, 1)
	go func() { _, _, err := f.b.Submit(context.Background(), f.req, f.authorize, prepared); sent <- err }()
	<-locked
	done := make(chan error, 1)
	go func() { done <- repository.NewCombinedConversationCleanup(f.db).CleanupConversation(91, 11) }()
	select {
	case err := <-done:
		t.Fatalf("deletion crossed capture fence: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-sent)
	require.NoError(t, <-done)
	for _, model := range []any{&domain.EmailLog{}, &domain.OrdinaryEmailIntent{}, &domain.OrdinaryEmailAttempt{}} {
		var count int64
		require.NoError(t, f.db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
	_, handled, err := f.b.Submit(context.Background(), f.req, f.authorize, f.prepare)
	require.True(t, handled)
	require.ErrorIs(t, err, domain.ErrAccountTranscriptConflict)
}

func TestWidgetTranscriptBridgeEnvelopeBoundsAndDeadline(t *testing.T) {
	t.Run("size", func(t *testing.T) {
		f := widgetBridgeSetup(t, false)
		prepared := func(ctx context.Context) (domain.WidgetTranscriptPrepared, error) {
			p, e := f.prepare(ctx)
			p.Render = func([]domain.Message) (string, string) {
				return strings.Repeat("x", int(f.b.runtime.Config.MaxRequestBytes)), ""
			}
			return p, e
		}
		_, handled, err := f.b.Submit(context.Background(), f.req, f.authorize, prepared)
		require.True(t, handled)
		require.ErrorIs(t, err, domain.ErrAccountTranscriptInvalid)
		for _, model := range []any{&domain.EmailLog{}, &domain.OrdinaryEmailIntent{}, &domain.LocalChangeJournal{}} {
			var count int64
			require.NoError(t, f.db.Model(model).Count(&count).Error)
			require.Zero(t, count)
		}
	})
	t.Run("deadline", func(t *testing.T) { widgetBridgeDeadline(t, false) })
}
func widgetBridgeDeadline(t *testing.T, pg bool) {
	f := widgetBridgeSetup(t, pg)
	f.b.runtime.Config.MaxTimeout = 30 * time.Millisecond
	expired, release := make(chan struct{}), make(chan struct{})
	prepare := func(ctx context.Context) (domain.WidgetTranscriptPrepared, error) {
		<-ctx.Done()
		close(expired)
		<-release
		return domain.WidgetTranscriptPrepared{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, _, err := f.b.Submit(context.Background(), f.req, f.authorize, prepare); done <- err }()
	<-expired
	// Even before the synchronous caller returns, the bounded SQL transaction
	// has rolled back and cannot retain its account lock beyond the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return repository.LockOrdinaryEmailAccount(tx, 91) })
	close(release)
	require.NoError(t, err)
	require.ErrorIs(t, <-done, domain.ErrAccountTranscriptUnavailable)
	for _, model := range []any{&domain.EmailLog{}, &domain.OrdinaryEmailIntent{}, &domain.LocalChangeJournal{}} {
		var count int64
		require.NoError(t, f.db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestWidgetTranscriptBridgeCurrentAuthorityAndNamespace(t *testing.T) {
	f := widgetBridgeSetup(t, false)
	a := f.submit(t)
	denied := func(context.Context) error { return domain.ErrWidgetTranscriptForbidden }
	_, handled, err := f.b.Submit(context.Background(), f.req, denied, f.prepare)
	require.True(t, handled)
	require.ErrorIs(t, err, domain.ErrWidgetTranscriptForbidden)
	_, err = f.b.Status(context.Background(), f.req, a.EmailLogID, denied)
	require.ErrorIs(t, err, domain.ErrWidgetTranscriptForbidden)
	_, _, err = f.b.Submit(context.Background(), f.req, nil, f.prepare)
	require.ErrorIs(t, err, domain.ErrAccountTranscriptInvalid)
	scope, _ := widgetTranscriptIdentity(f.req)
	require.NotEqual(t, accountTranscriptCommandID(scope), widgetTranscriptCommandID(scope))
	for _, mutate := range []func(*domain.WidgetTranscriptRequest){
		func(r *domain.WidgetTranscriptRequest) { r.AccountID++ },
		func(r *domain.WidgetTranscriptRequest) { r.InboxID++ },
		func(r *domain.WidgetTranscriptRequest) { r.ContactID++ },
		func(r *domain.WidgetTranscriptRequest) { r.ConversationID++ },
		func(r *domain.WidgetTranscriptRequest) { r.SourceID += "-different" },
		func(r *domain.WidgetTranscriptRequest) { r.Key += "-different" },
	} {
		changed := f.req
		mutate(&changed)
		_, err = f.b.Status(context.Background(), changed, a.EmailLogID, f.authorize)
		require.ErrorIs(t, err, domain.ErrAccountTranscriptNotFound)
	}
	account, err := NewAccountTranscriptBridge(f.db, accountBridgeEnv)
	require.NoError(t, err)
	req := domain.AccountTranscriptRequest{AccountID: 91, UserID: 7, ConversationID: 11, Key: f.req.Key, Email: f.req.Email}
	admitted, handled, err := account.Submit(context.Background(), req, func(context.Context) (domain.AccountTranscriptPrepared, error) {
		return domain.AccountTranscriptPrepared{Eligible: true, From: "sender@example.test", To: req.Email, Subject: "Account", Render: func([]domain.Message) (string, string) { return "Account private", "Account private" }}, nil
	})
	require.NoError(t, err)
	require.True(t, handled)
	require.NotEqual(t, a.EmailLogID, admitted.EmailLogID)
	_, err = f.b.Status(context.Background(), f.req, admitted.EmailLogID, f.authorize)
	require.ErrorIs(t, err, domain.ErrAccountTranscriptNotFound)
}

func TestWidgetTranscriptBridgeLostAckReauthorizes(t *testing.T) {
	f := widgetBridgeSetup(t, false)
	committed := &atomic.Bool{}
	wrapped := f.db.Session(&gorm.Session{NewDB: true})
	wrapped.Statement.ConnPool = &lostAdmissionPool{ConnPool: f.db.Statement.ConnPool, once: committed}
	f.b.db = wrapped
	authorize := func(context.Context) error {
		if committed.Load() {
			return domain.ErrWidgetTranscriptForbidden
		}
		return nil
	}
	a, handled, err := f.b.Submit(context.Background(), f.req, authorize, f.prepare)
	require.True(t, committed.Load())
	require.True(t, handled)
	require.Nil(t, a)
	require.ErrorIs(t, err, domain.ErrWidgetTranscriptForbidden)
	var n int64
	require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.Equal(t, int32(1), f.renders.Load())
}

func TestWidgetTranscriptBridgeSnapshotRejectsOwnershipDowngrade(t *testing.T) {
	f := widgetBridgeSetup(t, false)
	f.submit(t)
	original := f.item(t)
	raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{original})
	require.NoError(t, err)
	var table map[string]any
	require.NoError(t, json.Unmarshal(raw, &table))
	require.Equal(t, "msg.ordinary_email_intent.v3", table["codec"])
	for _, version := range []string{"v1", "v2"} {
		for _, strip := range []bool{false, true} {
			require.NoError(t, json.Unmarshal(raw, &table))
			table["codec"] = "msg.ordinary_email_intent." + version
			if strip {
				delete(table["rows"].([]any)[0].(map[string]any)["private"].(map[string]any), "request")
			}
			damaged, _ := json.Marshal(table)
			var rows []domain.OrdinaryEmailIntent
			require.Error(t, unmarshalOrdinaryIntents(damaged, &rows))
		}
	}
	tampered := original
	tampered.TranscriptMessages = 101
	require.False(t, validOrdinaryIntent(tampered))
	tampered = original
	scope := strings.Repeat("0", 64)
	tampered.RequestScopeHash = &scope
	require.False(t, validOrdinaryIntent(tampered))
	fakeUser := uint(7)
	require.NoError(t, f.db.Model(&domain.EmailLog{}).Where("id = ?", original.EmailLogID).Update("user_id", fakeUser).Error)
	require.Error(t, f.db.Transaction(restoreOrdinaryEmailSafety))
}
