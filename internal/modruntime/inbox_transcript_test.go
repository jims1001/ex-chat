package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type inboxTranscriptTestTransport func(context.Context, domain.InboxSMTPBinding, string, string, string, string, string, func() error) domain.OrdinarySMTPResult

func (f inboxTranscriptTestTransport) SendInboxTranscript(ctx context.Context, b domain.InboxSMTPBinding, from, to, subject, html, text string, hook func() error) domain.OrdinarySMTPResult {
	return f(ctx, b, from, to, subject, html, text, hook)
}

type inboxBridgeFixture struct {
	*accountBridgeFixture
	binding      domain.InboxSMTPBinding
	prepareInbox func(context.Context) (domain.AccountTranscriptPrepared, error)
}

func inboxBridgeSetup(t *testing.T, pg bool) *inboxBridgeFixture {
	t.Helper()
	f := &inboxBridgeFixture{accountBridgeFixture: accountBridgeSetup(t, pg)}
	var err error
	f.b, err = NewAccountTranscriptBridge(f.db, func(key string) string {
		if key == "INBOX_TRANSCRIPT_QUEUE_ENABLED" {
			return "true"
		}
		return accountBridgeEnv(key)
	})
	require.NoError(t, err)
	var inbox domain.Inbox
	require.NoError(t, f.db.First(&inbox, 5).Error)
	f.binding = domain.InboxSMTPBinding{Kind: "inbox_smtp_v1", AccountID: "91", InboxID: "5", CreatedAt: inbox.CreatedAt.UTC().Format(time.RFC3339Nano), Fingerprint: domain.InboxSMTPFingerprintPrefix + strings.Repeat("a", 64)}
	f.prepareInbox = func(ctx context.Context) (domain.AccountTranscriptPrepared, error) {
		p, e := f.prepare(ctx)
		p.InboxSelected = true
		p.InboxRoute = &f.binding
		return p, e
	}
	f.b.runtime.OrdinaryEmailTransport = ordinaryTestTransport(func(context.Context, func() error) domain.OrdinarySMTPResult {
		t.Error("Inbox work reached global transport")
		return domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPPermanentFailure}
	})
	f.b.runtime.InboxTranscriptSMTP = inboxTranscriptTestTransport(func(ctx context.Context, b domain.InboxSMTPBinding, from, to, subject, html, text string, hook func() error) domain.OrdinarySMTPResult {
		return ordinaryAccepted(ctx, hook)
	})
	return f
}

func (f *inboxBridgeFixture) admit(t *testing.T) *domain.AccountTranscriptAdmission {
	t.Helper()
	a, handled, err := f.b.Submit(context.Background(), f.req, f.prepareInbox)
	require.NoError(t, err)
	require.True(t, handled)
	require.NotNil(t, a)
	return a
}

func TestInboxTranscriptBridgeAdmissionReplayAndLostAcknowledgement(t *testing.T) {
	inboxBridgeReplay(t, false)
}
func inboxBridgeReplay(t *testing.T, pg bool) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost_ack_%v", lost), func(t *testing.T) {
			f := inboxBridgeSetup(t, pg)
			if lost {
				once := &atomic.Bool{}
				wrapped := f.db.Session(&gorm.Session{NewDB: true})
				wrapped.Statement.ConnPool = &lostAdmissionPool{ConnPool: f.db.Statement.ConnPool, once: once}
				f.b.db = wrapped
			}
			type admission struct {
				a *domain.AccountTranscriptAdmission
				h bool
				e error
			}
			out := make(chan admission, 6)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					a, h, e := f.b.Submit(context.Background(), f.req, f.prepareInbox)
					out <- admission{a, h, e}
				}()
			}
			close(start)
			wg.Wait()
			close(out)
			var first *domain.AccountTranscriptAdmission
			for got := range out {
				require.NoError(t, got.e)
				require.True(t, got.h)
				require.NotNil(t, got.a)
				if first == nil {
					first = got.a
				}
				require.Equal(t, first, got.a)
			}
			require.EqualValues(t, 1, f.renders.Load())
			item := f.item(t)
			scope, input := accountTranscriptIdentity(f.req)
			require.Equal(t, scope, *item.RequestScopeHash)
			require.Equal(t, input, item.RequestInputHash)
			require.True(t, strings.HasPrefix(item.CommandID, "acct-inbox-v1:"))
			require.Empty(t, item.TransportKey)
			f.b.enabled = false
			f.b.inboxEnabled = false
			f.b.runtime.Config.OrdinaryEmail.Enabled = false
			a, h, e := f.b.Submit(context.Background(), f.req, func(context.Context) (domain.AccountTranscriptPrepared, error) {
				t.Error("known Inbox key was prepared again")
				return domain.AccountTranscriptPrepared{}, nil
			})
			require.NoError(t, e)
			require.True(t, h)
			require.Equal(t, first, a)
			changed := f.req
			changed.Email = "changed@example.test"
			_, h, e = f.b.Submit(context.Background(), changed, f.prepareInbox)
			require.True(t, h)
			require.ErrorIs(t, e, domain.ErrAccountTranscriptConflict)
			var count int64
			require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestInboxTranscriptBridgeRejectsInvalidSelectedBinding(t *testing.T) {
	for _, mutation := range []string{"nil", "kind", "account", "inbox", "incarnation", "fingerprint", "global_with_binding"} {
		t.Run(mutation, func(t *testing.T) {
			f := inboxBridgeSetup(t, false)
			prep := f.prepareInbox
			_, handled, err := f.b.Submit(context.Background(), f.req, func(ctx context.Context) (domain.AccountTranscriptPrepared, error) {
				p, e := prep(ctx)
				switch mutation {
				case "nil":
					p.InboxRoute = nil
				case "kind":
					p.InboxRoute.Kind = ""
				case "account":
					p.InboxRoute.AccountID = "92"
				case "inbox":
					p.InboxRoute.InboxID = "6"
				case "incarnation":
					p.InboxRoute.CreatedAt = ""
				case "fingerprint":
					p.InboxRoute.Fingerprint = ""
				case "global_with_binding":
					p.InboxSelected = false
				}
				return p, e
			})
			require.True(t, handled)
			require.Error(t, err)
			for _, model := range []any{&domain.OrdinaryEmailIntent{}, &domain.EmailLog{}, &domain.LocalChangeJournal{}} {
				var n int64
				require.NoError(t, f.db.Model(model).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestInboxTranscriptPublicCommandCannotSpoofRoute(t *testing.T) {
	for _, key := range []string{"route_kind", "route_account_id", "route_inbox_id", "route_created_at", "route_fingerprint", "inbox_route", "_inbox_route", "route", "smtp_password", "_inbox_transcript_route", "InboxRoute"} {
		t.Run(key, func(t *testing.T) {
			r := ordinaryRuntime(t, nil)
			cmd := ordinaryCommand("spoof-inbox-route")
			cmd.Input[key] = map[string]any{"kind": "inbox_smtp_v1", "account_id": "91", "inbox_id": "5"}
			response := ordinaryCall(r, cmd)
			require.NotEqual(t, 200, response.Code, response.Body.String())
			var n int64
			require.NoError(t, r.DB.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
			require.Zero(t, n)
		})
	}
	// A generated-looking name alone remains a legal global command. It does
	// not grant the caller route or REST requester ownership.
	for _, prefix := range []string{"acct-inbox-v1:", "wdg-inbox-v1:"} {
		r := ordinaryRuntime(t, nil)
		cmd := ordinaryCommand(prefix + strings.Repeat("a", 43))
		response := ordinaryCall(r, cmd)
		require.Equal(t, 200, response.Code, response.Body.String())
		var item domain.OrdinaryEmailIntent
		require.NoError(t, r.DB.First(&item).Error)
		require.Nil(t, item.RequestScopeHash)
		require.False(t, ordinarySnapshotHasRoute(item))
		require.Equal(t, r.Config.OrdinaryEmail.key(), item.TransportKey)
	}
}

func TestInboxTranscriptUnsupportedWorkerLeavesRowsUntouched(t *testing.T) {
	f := inboxBridgeSetup(t, false)
	var ids []uint
	for _, state := range []string{"leased", "submitting", "unknown", "failed", "sent", "pending"} {
		f.req.Key = "inbox-unsupported-" + state
		f.admit(t)
		item := f.itemByKey(t)
		if state == "leased" || state == "submitting" {
			claim, ok, err := f.b.runtime.claimOrdinaryEmail(context.Background())
			require.NoError(t, err)
			require.True(t, ok)
			if state == "submitting" {
				require.NoError(t, f.b.runtime.startOrdinarySubmission(context.Background(), claim))
			}
		} else if state != "pending" {
			require.NoError(t, f.db.Model(&item).Update("state", state).Error)
		}
		ids = append(ids, item.ID)
	}
	require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Where("state IN ?", []string{"leased", "submitting"}).Update("lease_until", time.Now().UTC().Add(-time.Minute)).Error)
	var before []domain.OrdinaryEmailIntent
	require.NoError(t, f.db.Where("id IN ?", ids).Order("id").Find(&before).Error)
	var attemptsBefore []domain.OrdinaryEmailAttempt
	require.NoError(t, f.db.Order("id").Find(&attemptsBefore).Error)
	r := *f.b.runtime
	r.InboxTranscriptSMTP = nil
	ran, err := r.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	var after []domain.OrdinaryEmailIntent
	require.NoError(t, f.db.Where("id IN ?", ids).Order("id").Find(&after).Error)
	require.Equal(t, before, after)
	var attemptsAfter []domain.OrdinaryEmailAttempt
	require.NoError(t, f.db.Order("id").Find(&attemptsAfter).Error)
	require.Equal(t, attemptsBefore, attemptsAfter)
	// Earlier Inbox work cannot starve a later global intent on a global worker.
	f.req.Key = "global-after-inbox-unsupported"
	_, h, e := f.b.Submit(context.Background(), f.req, f.prepare)
	require.NoError(t, e)
	require.True(t, h)
	r.OrdinaryEmailTransport = ordinaryTestTransport(ordinaryAccepted)
	ordinaryRun(t, r)
	require.Equal(t, "sent", f.itemByKey(t).State)
	require.NoError(t, f.db.Where("id IN ?", ids).Order("id").Find(&after).Error)
	require.Equal(t, before, after)
}

func (f *inboxBridgeFixture) itemByKey(t *testing.T) domain.OrdinaryEmailIntent {
	t.Helper()
	scope, _ := accountTranscriptIdentity(f.req)
	var item domain.OrdinaryEmailIntent
	require.NoError(t, f.db.Where("request_scope_hash = ?", scope).First(&item).Error)
	return item
}

func TestInboxTranscriptWorkerCrashAndOutcomeFences(t *testing.T) { inboxWorkerFences(t, false) }
func inboxWorkerFences(t *testing.T, pg bool) {
	for _, scenario := range []string{"pre_marker_restart", "post_marker_restart", "marker_lost_ack", "outcome_rollback", "scrub", "delete"} {
		t.Run(scenario, func(t *testing.T) {
			f := inboxBridgeSetup(t, pg)
			first := f.admit(t)
			item := f.item(t)
			r := *f.b.runtime
			var sent atomic.Int32
			r.InboxTranscriptSMTP = inboxTranscriptTestTransport(func(ctx context.Context, b domain.InboxSMTPBinding, from, to, subject, html, text string, hook func() error) domain.OrdinarySMTPResult {
				result := ordinaryAccepted(ctx, hook)
				if result.Outcome == domain.OrdinarySMTPAccepted {
					sent.Add(1)
				}
				return result
			})
			switch scenario {
			case "pre_marker_restart", "post_marker_restart", "scrub", "delete":
				stale, ok, err := r.claimOrdinaryEmail(context.Background())
				require.NoError(t, err)
				require.True(t, ok)
				if scenario == "post_marker_restart" {
					require.NoError(t, r.startOrdinarySubmission(context.Background(), stale))
				}
				if scenario == "scrub" {
					require.Equal(t, 200, ordinaryAnonymize(t, r, "scrub-inbox-intent").Code)
				} else if scenario == "delete" {
					require.NoError(t, repository.NewCombinedConversationCleanup(f.db).CleanupConversation(91, 11))
				} else {
					ordinaryExpire(t, r, item.ID)
					ordinaryRun(t, r)
				}
				require.Error(t, r.startOrdinarySubmission(context.Background(), stale))
				require.Error(t, r.completeOrdinaryEmail(context.Background(), stale, "sent", "", false))
				if scenario == "pre_marker_restart" {
					require.EqualValues(t, 1, sent.Load())
					require.Equal(t, "sent", f.item(t).State)
				} else {
					require.Zero(t, sent.Load())
				}
				if scenario == "post_marker_restart" {
					require.Equal(t, "unknown", f.item(t).State)
				}
				if scenario == "scrub" {
					scrubbed := f.item(t)
					require.Empty(t, scrubbed.Payload)
					require.Empty(t, scrubbed.RouteKind)
					require.Empty(t, scrubbed.RouteFingerprint)
					require.NotNil(t, scrubbed.RequestScopeHash)
					_, handled, err := f.b.Submit(context.Background(), f.req, f.prepareInbox)
					require.True(t, handled)
					require.ErrorIs(t, err, domain.ErrAccountTranscriptConflict)
				}
				if scenario == "delete" {
					var n int64
					require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
					require.Zero(t, n)
					_, handled, err := f.b.Submit(context.Background(), f.req, f.prepareInbox)
					require.True(t, handled)
					require.ErrorIs(t, err, domain.ErrAccountTranscriptConflict)
				}
			case "marker_lost_ack":
				r.InboxTranscriptSMTP = inboxTranscriptTestTransport(func(ctx context.Context, b domain.InboxSMTPBinding, from, to, subject, html, text string, hook func() error) domain.OrdinarySMTPResult {
					require.NoError(t, hook())
					return domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPCancelledBeforeSubmission}
				})
				ordinaryRun(t, r)
				require.Equal(t, "unknown", f.item(t).State)
			case "outcome_rollback":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("inbox_fail_outcome", func(tx *gorm.DB) {
					if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.Action == "event_outbox" && strings.Contains(row.AfterState, `"delivery_status":"sent"`) {
						tx.AddError(errors.New("INBOX_PROVIDER_PRIVATE_ECHO"))
					}
				}))
				_, err := r.RunOrdinaryEmailOnce(context.Background())
				require.Error(t, err)
				require.NotContains(t, err.Error(), "INBOX_PROVIDER_PRIVATE_ECHO")
				require.Equal(t, "submitting", f.item(t).State)
				require.NoError(t, f.db.Callback().Create().Remove("inbox_fail_outcome"))
				ordinaryExpire(t, r, item.ID)
				ordinaryRun(t, r)
				require.Equal(t, "unknown", f.item(t).State)
				require.EqualValues(t, 1, sent.Load())
			}
			ran, err := r.RunOrdinaryEmailOnce(context.Background())
			require.NoError(t, err)
			require.False(t, ran)
			if scenario != "scrub" && scenario != "delete" {
				require.Equal(t, first, f.admit(t))
			}
		})
	}
}

func TestPostgresInboxTranscriptQueueLifecycle(t *testing.T) {
	t.Run("admission_replay_lost_ack", func(t *testing.T) { inboxBridgeReplay(t, true) })
	t.Run("crash_scrub_delete", func(t *testing.T) { inboxWorkerFences(t, true) })
	t.Run("competing_workers", func(t *testing.T) {
		f := inboxBridgeSetup(t, true)
		f.admit(t)
		r := *f.b.runtime
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		var once sync.Once
		r.InboxTranscriptSMTP = inboxTranscriptTestTransport(func(ctx context.Context, b domain.InboxSMTPBinding, from, to, subject, html, text string, hook func() error) domain.OrdinarySMTPResult {
			calls.Add(1)
			once.Do(func() { close(entered) })
			<-release
			return ordinaryAccepted(ctx, hook)
		})
		type result struct {
			ran bool
			err error
		}
		first := make(chan result, 1)
		go func() { ran, err := r.RunOrdinaryEmailOnce(context.Background()); first <- result{ran, err} }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("first worker did not reach deterministic transport barrier")
		}
		second := make(chan result, 1)
		go func() { ran, err := r.RunOrdinaryEmailOnce(context.Background()); second <- result{ran, err} }()
		select {
		case got := <-second:
			require.NoError(t, got.err)
			require.False(t, got.ran)
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("second worker failed to skip owned Inbox intent")
		}
		close(release)
		got := <-first
		require.NoError(t, got.err)
		require.True(t, got.ran)
		require.EqualValues(t, 1, calls.Load())
		require.Equal(t, "sent", f.item(t).State)
		var attempts []domain.OrdinaryEmailAttempt
		require.NoError(t, f.db.Find(&attempts).Error)
		require.Len(t, attempts, 1)
		require.NotNil(t, attempts[0].SubmittingAt)
	})
}

func TestInboxTranscriptPrivateRouteNeverAppearsInPublicJSON(t *testing.T) {
	f := inboxBridgeSetup(t, false)
	a := f.admit(t)
	item := f.item(t)
	raw, err := json.Marshal(item)
	require.NoError(t, err)
	view, err := f.b.Status(context.Background(), 91, 7, a.EmailLogID)
	require.NoError(t, err)
	status, err := json.Marshal(view)
	require.NoError(t, err)
	for _, secret := range []string{item.RouteFingerprint, "route_kind", "route_account_id", "route_inbox_id", "route_created_at", "route_fingerprint", "ACCOUNT_TRANSCRIPT_PRIVATE_BODY", "recipient@example.test"} {
		require.NotContains(t, string(raw), secret)
		require.NotContains(t, string(status), secret)
	}
}

func TestInboxTranscriptReceiptBindsRouteAndRejectsPublicReplay(t *testing.T) {
	f := inboxBridgeSetup(t, false)
	a := f.admit(t)
	item := f.item(t)
	var body ordinaryEmailPayload
	require.NoError(t, json.Unmarshal([]byte(item.Payload), &body))
	cmd := ordinaryCommand(item.CommandID)
	cmd.Actor = foundation.ContractActor{Type: "User", ID: "7", AuthSource: "jwt"}
	cmd.Target = foundation.ContractTarget{Type: "conversation", ID: "11"}
	cmd.IdempotencyKey = "account-transcript-v1:" + *item.RequestScopeHash
	cmd.Input = map[string]any{"conversation_id": uint(11), "user_id": uint(7), "email_type": "transcript", "to_email": body.To, "from_email": body.From, "subject": body.Subject, "content_html": body.HTML, "content_text": body.Text, inboxTranscriptReceiptRoute: f.binding}
	r := *f.b.runtime
	r.Contracts.UseCommandReceipts(f.db)
	output, err := r.executeTranscriptAdmission(context.Background(), cmd, &f.binding)
	require.NoError(t, err)
	require.NotNil(t, output)
	response := ordinaryCall(r, cmd)
	require.NotEqual(t, 200, response.Code, response.Body.String())
	changed := f.binding
	changed.Fingerprint = domain.InboxSMTPFingerprintPrefix + strings.Repeat("b", 64)
	cmd.Input[inboxTranscriptReceiptRoute] = changed
	_, err = r.executeTranscriptAdmission(context.Background(), cmd, &changed)
	require.Error(t, err)
	delete(cmd.Input, inboxTranscriptReceiptRoute)
	response = ordinaryCall(r, cmd)
	require.NotEqual(t, 200, response.Code, response.Body.String())
	var count int64
	require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Equal(t, a.EmailLogID, f.item(t).EmailLogID)
}

func TestInboxTranscriptBridgeRequiresGlobalConfiguration(t *testing.T) {
	for _, field := range []string{"host", "port", "from", "worker"} {
		t.Run(field, func(t *testing.T) {
			f := inboxBridgeSetup(t, false)
			switch field {
			case "host":
				f.b.runtime.Config.OrdinaryEmail.Host = ""
			case "port":
				f.b.runtime.Config.OrdinaryEmail.Port = "0"
			case "from":
				f.b.runtime.Config.OrdinaryEmail.From = ""
			case "worker":
				f.b.runtime.Config.OrdinaryEmail.Enabled = false
			}
			_, handled, err := f.b.Submit(context.Background(), f.req, f.prepareInbox)
			require.True(t, handled)
			require.ErrorIs(t, err, domain.ErrAccountTranscriptUnavailable)
			var n int64
			require.NoError(t, f.db.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
