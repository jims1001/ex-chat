package modruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type ordinaryTestTransport func(context.Context, func() error) domain.OrdinarySMTPResult

func (f ordinaryTestTransport) SendOrdinary(ctx context.Context, _, _, _, _, _ string, hook func() error) domain.OrdinarySMTPResult {
	return f(ctx, hook)
}
func ordinaryAccepted(_ context.Context, hook func() error) domain.OrdinarySMTPResult {
	if hook() != nil {
		return domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPCancelledBeforeSubmission}
	}
	return domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPAccepted}
}
func ordinaryDB(t *testing.T, pg bool) *gorm.DB {
	t.Helper()
	if pg {
		return postgresModuleDB(t, "MSG")
	}
	return nil
}
func ordinaryRuntime(t *testing.T, db *gorm.DB) Runtime {
	t.Helper()
	if db == nil {
		db = newModuleTestDB(t, "MSG")
	}
	cfg := businessConfig("MSG")
	cfg.OrdinaryEmail = OrdinaryEmailConfig{Enabled: true, From: "sender@example.test", Host: "127.0.0.1", Port: "2525"}
	r := Runtime{DB: db, Config: cfg, Contracts: NewContractRegistry("MSG"), OrdinaryEmailTransport: ordinaryTestTransport(ordinaryAccepted)}
	require.NoError(t, r.RegisterBusinessContracts())
	return r
}
func ordinaryCommand(key string) foundation.CommandEnvelope {
	c := confirmationQueueCommand("notification", key)
	c.Input["subject"] = "ordinary-private-subject"
	c.Input["content_html"] = "<p>ordinary-private-body</p>"
	c.Input["content_text"] = "ordinary-private-body"
	return c
}
func ordinaryCall(r Runtime, c foundation.CommandEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(r.Handler(), "/contracts/commands/"+c.CommandType, c, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+r.Config.AuthToken)
		req.Header.Set(headerCaller, "API")
		req.Header.Set(headerCorrelationID, c.CorrelationID)
	})
}
func ordinaryEnqueue(t *testing.T, r Runtime, key string) domain.OrdinaryEmailIntent {
	t.Helper()
	response := ordinaryCall(r, ordinaryCommand(key))
	require.Equal(t, 200, response.Code, response.Body.String())
	var item domain.OrdinaryEmailIntent
	require.NoError(t, r.DB.Where("command_id = ?", key).First(&item).Error)
	return item
}
func ordinaryRead(t *testing.T, r Runtime, id uint) domain.OrdinaryEmailIntent {
	t.Helper()
	var item domain.OrdinaryEmailIntent
	require.NoError(t, r.DB.First(&item, id).Error)
	return item
}
func ordinaryExpire(t *testing.T, r Runtime, id uint) {
	t.Helper()
	past := time.Now().UTC().Add(-time.Minute)
	require.NoError(t, r.DB.Model(&domain.OrdinaryEmailIntent{}).Where("id = ?", id).Update("lease_until", past).Error)
}
func ordinaryRun(t *testing.T, r Runtime) {
	t.Helper()
	ran, err := r.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ran)
}

// A real socket fixture checks the committed marker from a separate DB read
// before accepting DATA, then optionally loses the final acknowledgement.
func ordinarySMTPServer(t *testing.T, r *Runtime, loseAck bool) (*atomic.Int32, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	bodies := make(chan string, 8)
	count := &atomic.Int32{}
	done := make(chan struct{})
	var connections sync.WaitGroup
	go func() {
		defer close(done)
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().UTC().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				fmt.Fprint(conn, "220 fixture\r\n")
				for {
					line, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						fmt.Fprint(conn, "250 fixture\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						fmt.Fprint(conn, "250 ok\r\n")
					case strings.HasPrefix(line, "DATA"):
						var marked int64
						e := r.DB.Model(&domain.OrdinaryEmailAttempt{}).Where("state = ? AND submitting_at IS NOT NULL", "submitting").Count(&marked).Error
						if e != nil || marked != 1 {
							bodies <- "missing durable submitting marker"
							return
						}
						fmt.Fprint(conn, "354 go\r\n")
						var body strings.Builder
						for {
							part, e := reader.ReadString('\n')
							if e != nil {
								return
							}
							if part == ".\r\n" {
								break
							}
							body.WriteString(part)
						}
						count.Add(1)
						bodies <- body.String()
						if loseAck {
							return
						}
						fmt.Fprint(conn, "250 accepted\r\n")
					case strings.HasPrefix(line, "QUIT"):
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done; connections.Wait() })
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	r.Config.OrdinaryEmail.Host = host
	r.Config.OrdinaryEmail.Port = port
	r.OrdinaryEmailTransport = service.NewSMTPEmailSender(host, port, "", "")
	r.Contracts = NewContractRegistry("MSG")
	require.NoError(t, r.RegisterBusinessContracts())
	return count, bodies
}
func TestOrdinaryEmailQueueHasDurableIntentBeforeSMTP(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	count, bodies := ordinarySMTPServer(t, &r, false)
	item := ordinaryEnqueue(t, r, "real-enqueue")
	require.True(t, r.DB.Migrator().HasTable("ordinary_email_intents"))
	require.Zero(t, count.Load())
	require.Equal(t, "pending", item.State)
	response := ordinaryCall(r, ordinaryCommand("real-enqueue"))
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"max_retries":0`)
	listed, listErr := repository.NewEmailLogRepository(r.DB).List(91, "", "")
	require.NoError(t, listErr)
	require.Len(t, listed, 1)
	require.Zero(t, listed[0].MaxRetries)
	conflict := ordinaryCommand("real-enqueue")
	conflict.Input["subject"] = "changed"
	require.Equal(t, 409, ordinaryCall(r, conflict).Code)
	ordinaryRun(t, r)
	require.Equal(t, int32(1), count.Load())
	select {
	case body := <-bodies:
		require.Contains(t, body, "ordinary-private-body")
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP did not accept body")
	}
	require.Equal(t, "sent", ordinaryRead(t, r, item.ID).State)
	_, err := r.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(1), count.Load())
	q := foundation.QueryEnvelope{QueryType: "msg.email_delivery.get.v1", AccountID: 91, Filter: map[string]any{"id": item.EmailLogID}}
	out, err := r.Contracts.queries[q.QueryType](context.Background(), q)
	require.NoError(t, err)
	raw, _ := json.Marshal(out)
	require.Contains(t, string(raw), `"queue_state":"sent"`)
	require.Contains(t, string(raw), `"max_retries":0`)
	require.Contains(t, string(raw), `"max_attempts":4`)
	require.NotContains(t, string(raw), "ordinary-private")
	require.NotContains(t, string(raw), "recipient@")
	require.NotContains(t, string(raw), "lease_token")
	q.AccountID = 92
	_, err = r.Contracts.queries[q.QueryType](context.Background(), q)
	require.Error(t, err)
	var events []domain.LocalChangeJournal
	require.NoError(t, r.DB.Where("action = ?", "event_outbox").Find(&events).Error)
	require.Len(t, events, 4)
	for _, e := range events {
		require.NotContains(t, e.AfterState, "ordinary-private")
		require.NotContains(t, e.AfterState, "recipient@")
	}
	var total int64
	require.NoError(t, r.DB.Model(&domain.OrdinaryEmailIntent{}).Count(&total).Error)
	require.EqualValues(t, 1, total)
	// Isolated MSG remains free of SYS, inbox and credential-owner tables.
	for _, model := range []any{&domain.SystemConfig{}, &domain.Inbox{}, &domain.PasswordResetToken{}, &domain.AgentInvitation{}} {
		require.False(t, r.DB.Migrator().HasTable(model))
	}
}
func TestOrdinaryEmailAdmissionRequiresExplicitEnabledIdentity(t *testing.T) {
	for _, name := range []string{"disabled", "missing_sender", "different_sender", "header_control"} {
		t.Run(name, func(t *testing.T) {
			r := ordinaryRuntime(t, nil)
			cmd := ordinaryCommand("admission")
			switch name {
			case "disabled":
				r.Config.OrdinaryEmail.Enabled = false
			case "missing_sender":
				r.Config.OrdinaryEmail.From = ""
			case "different_sender":
				cmd.Input["from_email"] = "other@example.test"
			case "header_control":
				cmd.Input["subject"] = "subject\r\nBcc: other@example.test"
			}
			r.Contracts = NewContractRegistry("MSG")
			require.NoError(t, r.RegisterBusinessContracts())
			res := ordinaryCall(r, cmd)
			require.NotEqual(t, 200, res.Code)
			for _, model := range []any{&domain.EmailLog{}, &domain.OrdinaryEmailIntent{}, &domain.OrdinaryEmailAttempt{}, &domain.LocalChangeJournal{}} {
				var count int64
				require.NoError(t, r.DB.Model(model).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
	cfg, err := LoadOrdinaryEmailConfig(func(string) string { return "" })
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
}
func TestOrdinaryEmailManagedQueueNeverAdoptsLegacyOrSensitiveMail(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	for _, kind := range []string{"transcript", "notification", "reset_password", "confirmation", "agent_invitation"} {
		require.NoError(t, r.DB.Create(&domain.EmailLog{AccountID: 91, EmailType: kind, FromEmail: "sender@example.test", ToEmail: "recipient@example.test", Subject: "legacy", ContentText: "legacy", Status: "pending", DeliveryStatus: "pending"}).Error)
	}
	ran, err := r.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	cmd := ordinaryCommand("reset-excluded")
	cmd.Input["email_type"] = "reset_password"
	require.Equal(t, 200, ordinaryCall(r, cmd).Code)
	for _, kind := range []string{"confirmation", "agent_invitation"} {
		cmd := ordinaryCommand("sensitive-" + kind)
		cmd.Input["email_type"] = kind
		require.NotEqual(t, 200, ordinaryCall(r, cmd).Code)
	}
	var n int64
	require.NoError(t, r.DB.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
	require.Zero(t, n)
}
func TestOrdinaryEmailLostAcknowledgementNeverResends(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	count, bodies := ordinarySMTPServer(t, &r, true)
	item := ordinaryEnqueue(t, r, "lost-ack")
	ordinaryRun(t, r)
	select {
	case body := <-bodies:
		require.Contains(t, body, "ordinary-private-body")
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP did not accept body")
	}
	require.Equal(t, "unknown", ordinaryRead(t, r, item.ID).State)
	for i := 0; i < 3; i++ {
		ran, err := r.RunOrdinaryEmailOnce(context.Background())
		require.NoError(t, err)
		require.False(t, ran)
	}
	require.Equal(t, int32(1), count.Load())
}
func TestOrdinaryEmailRetryIsBoundedAndClassified(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome domain.OrdinarySMTPOutcome
		state   string
		count   int
	}{{"transient", domain.OrdinarySMTPPreSubmissionTransient, "failed", 4}, {"auth", domain.OrdinarySMTPAuthenticationFailure, "failed", 1}, {"permanent", domain.OrdinarySMTPPermanentFailure, "failed", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			r := ordinaryRuntime(t, nil)
			calls := 0
			r.OrdinaryEmailTransport = ordinaryTestTransport(func(context.Context, func() error) domain.OrdinarySMTPResult {
				calls++
				return domain.OrdinarySMTPResult{Outcome: tc.outcome}
			})
			item := ordinaryEnqueue(t, r, "retry")
			for i := 0; i < tc.count; i++ {
				before := time.Now()
				ordinaryRun(t, r)
				got := ordinaryRead(t, r, item.ID)
				if i < tc.count-1 {
					require.Equal(t, "pending", got.State)
					require.True(t, got.NextAttemptAt.After(before.Add(4*time.Minute)))
					ran, err := r.RunOrdinaryEmailOnce(context.Background())
					require.NoError(t, err)
					require.False(t, ran)
					require.NoError(t, r.DB.Model(&got).Update("next_attempt_at", time.Now().UTC().Add(-time.Second)).Error)
				}
			}
			require.Equal(t, tc.state, ordinaryRead(t, r, item.ID).State)
			require.Equal(t, tc.count, calls)
			ran, err := r.RunOrdinaryEmailOnce(context.Background())
			require.NoError(t, err)
			require.False(t, ran)
		})
	}
}
func TestOrdinaryEmailCrashRecoveryAndStaleLeaseFencing(t *testing.T) { ordinaryCrashTest(t, false) }
func ordinaryCrashTest(t *testing.T, pg bool) {
	t.Run("before_submission", func(t *testing.T) {
		r := ordinaryRuntime(t, ordinaryDB(t, pg))
		item := ordinaryEnqueue(t, r, "pre-crash")
		old, ok, err := r.claimOrdinaryEmail(context.Background())
		require.NoError(t, err)
		require.True(t, ok)
		ordinaryExpire(t, r, item.ID)
		ordinaryRun(t, r)
		require.Equal(t, "sent", ordinaryRead(t, r, item.ID).State)
		require.Error(t, r.startOrdinarySubmission(context.Background(), old))
		require.Error(t, r.completeOrdinaryEmail(context.Background(), old, "sent", "", false))
		var attempts []domain.OrdinaryEmailAttempt
		require.NoError(t, r.DB.Order("sequence").Find(&attempts).Error)
		require.Len(t, attempts, 2)
		require.Equal(t, "abandoned", attempts[0].State)
	})
	t.Run("after_submission", func(t *testing.T) {
		r := ordinaryRuntime(t, ordinaryDB(t, pg))
		item := ordinaryEnqueue(t, r, "post-crash")
		old, _, err := r.claimOrdinaryEmail(context.Background())
		require.NoError(t, err)
		require.NoError(t, r.startOrdinarySubmission(context.Background(), old))
		ordinaryExpire(t, r, item.ID)
		r.OrdinaryEmailTransport = ordinaryTestTransport(func(context.Context, func() error) domain.OrdinarySMTPResult {
			t.Fatal("recovered uncertain SMTP was retried")
			return domain.OrdinarySMTPResult{}
		})
		ordinaryRun(t, r)
		require.Equal(t, "unknown", ordinaryRead(t, r, item.ID).State)
		require.Error(t, r.completeOrdinaryEmail(context.Background(), old, "sent", "", false))
	})
}
func TestOrdinaryEmailOutcomeAndEventRollbackRecoverUnknown(t *testing.T) {
	ordinaryOutcomeRollbackTest(t, false)
}
func ordinaryOutcomeRollbackTest(t *testing.T, pg bool) {
	r := ordinaryRuntime(t, ordinaryDB(t, pg))
	item := ordinaryEnqueue(t, r, "rollback")
	var sent atomic.Int32
	r.OrdinaryEmailTransport = ordinaryTestTransport(func(ctx context.Context, hook func() error) domain.OrdinarySMTPResult {
		out := ordinaryAccepted(ctx, hook)
		if out.Outcome == domain.OrdinarySMTPAccepted {
			sent.Add(1)
		}
		return out
	})
	require.NoError(t, r.DB.Callback().Create().Before("gorm:create").Register("fail_ordinary_outcome_event", func(tx *gorm.DB) {
		if j, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && j.Action == "event_outbox" && strings.Contains(j.AfterState, `"delivery_status":"sent"`) {
			tx.AddError(errors.New("synthetic private store failure"))
		}
	}))
	_, err := r.RunOrdinaryEmailOnce(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private")
	require.Equal(t, "submitting", ordinaryRead(t, r, item.ID).State)
	require.NoError(t, r.DB.Callback().Create().Remove("fail_ordinary_outcome_event"))
	ordinaryExpire(t, r, item.ID)
	ordinaryRun(t, r)
	require.Equal(t, "unknown", ordinaryRead(t, r, item.ID).State)
	require.Equal(t, int32(1), sent.Load())
	var log domain.EmailLog
	require.NoError(t, r.DB.First(&log, item.EmailLogID).Error)
	require.Equal(t, "unknown", log.DeliveryStatus)
	var events []domain.LocalChangeJournal
	require.NoError(t, r.DB.Where("action = ?", "event_outbox").Find(&events).Error)
	for _, e := range events {
		require.NotContains(t, e.AfterState, `"delivery_status":"sent"`)
	}
}
func TestOrdinaryEmailShutdownPersistsUncertainty(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	item := ordinaryEnqueue(t, r, "shutdown")
	started := make(chan struct{})
	r.OrdinaryEmailTransport = ordinaryTestTransport(func(ctx context.Context, hook func() error) domain.OrdinarySMTPResult {
		require.NoError(t, hook())
		close(started)
		<-ctx.Done()
		return domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPUnknown}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.RunOrdinaryEmail(ctx, nil) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not stop")
	}
	require.Equal(t, "unknown", ordinaryRead(t, r, item.ID).State)
}
func ordinaryAnonymize(t *testing.T, r Runtime, key string) *httptest.ResponseRecorder {
	t.Helper()
	cmd := ordinaryCommand(key)
	cmd.CommandType = "msg.account_messages.anonymize.v1"
	cmd.Input = map[string]any{}
	return ordinaryCall(r, cmd)
}
func TestOrdinaryEmailAnonymizationFencesAndClearsPrivateIntent(t *testing.T) {
	ordinaryAnonymizeTest(t, false)
}
func ordinaryAnonymizeTest(t *testing.T, pg bool) {
	r := ordinaryRuntime(t, ordinaryDB(t, pg))
	item := ordinaryEnqueue(t, r, "delete-pending")
	old, _, err := r.claimOrdinaryEmail(context.Background())
	require.NoError(t, err)
	require.Equal(t, 200, ordinaryAnonymize(t, r, "anonymize-leased").Code)
	got := ordinaryRead(t, r, item.ID)
	require.Equal(t, "cancelled", got.State)
	require.Empty(t, got.Payload)
	require.True(t, got.Anonymized)
	require.Error(t, r.startOrdinarySubmission(context.Background(), old))
	item = ordinaryEnqueue(t, r, "delete-submitting")
	active, _, err := r.claimOrdinaryEmail(context.Background())
	require.NoError(t, err)
	require.NoError(t, r.startOrdinarySubmission(context.Background(), active))
	require.Equal(t, 409, ordinaryAnonymize(t, r, "anonymize-active").Code)
	require.NotEmpty(t, ordinaryRead(t, r, item.ID).Payload)
	require.NoError(t, r.completeOrdinaryEmail(context.Background(), active, "sent", "", false))
	require.Equal(t, 200, ordinaryAnonymize(t, r, "anonymize-finished").Code)
	require.Empty(t, ordinaryRead(t, r, item.ID).Payload)
	out, err := r.Contracts.queries["msg.account_deletion_status.get.v1"](context.Background(), foundation.QueryEnvelope{AccountID: 91})
	require.NoError(t, err)
	require.EqualValues(t, 0, out.(map[string]int64)["ordinary_email_private_content_count"])
}
func TestOrdinaryEmailSnapshotNeverReactivatesRestoredBacklog(t *testing.T) {
	ordinarySnapshotTest(t, false)
}
func ordinarySnapshotTest(t *testing.T, pg bool) {
	var db *gorm.DB
	if pg {
		db = postgresModuleDB(t, "MSG")
	}
	r := ordinaryRuntime(t, db)
	pending := ordinaryEnqueue(t, r, "backup-pending")
	active := ordinaryEnqueue(t, r, "backup-submitting")
	require.NoError(t, r.DB.Model(&domain.OrdinaryEmailIntent{}).Where("id = ?", pending.ID).Update("next_attempt_at", time.Now().UTC().Add(time.Hour)).Error)
	claim, _, err := r.claimOrdinaryEmail(context.Background())
	require.NoError(t, err)
	require.Equal(t, active.ID, claim.ID)
	require.NoError(t, r.startOrdinarySubmission(context.Background(), claim))
	path := filepath.Join(t.TempDir(), "msg.json")
	require.NoError(t, CreateSnapshot(r.DB, "MSG", "mail-generation", path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "ordinary-private-body")
	target := newModuleTestDB(t, "MSG")
	if pg {
		target = postgresModuleDB(t, "MSG")
	}
	require.NoError(t, RestoreSnapshot(context.Background(), target, "MSG", "mail-generation", path, nil))
	restored := ordinaryRuntime(t, target)
	for _, id := range []uint{pending.ID, active.ID} {
		var audit domain.EmailLog
		require.NoError(t, target.First(&audit, id).Error)
		require.Zero(t, audit.MaxRetries, "restore re-enabled legacy retry default")
		require.Equal(t, "unknown", ordinaryRead(t, restored, id).State)
	}
	ran, err := restored.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	confirmationSnapshotRewrite(t, raw, path, func(s *ModuleSnapshot) {
		var table map[string]any
		require.NoError(t, json.Unmarshal(s.Tables["ordinary_email_intents"], &table))
		rows := table["rows"].([]any)
		delete(rows[0].(map[string]any), "private")
		s.Tables["ordinary_email_intents"], _ = json.Marshal(table)
	})
	require.Error(t, RestoreSnapshot(context.Background(), target, "MSG", "mail-generation", path, nil))
	require.Equal(t, "unknown", ordinaryRead(t, restored, pending.ID).State)
	// Restore corruption cannot fabricate SMTP acceptance from a leased attempt.
	confirmationSnapshotRewrite(t, raw, path, func(s *ModuleSnapshot) {
		var table map[string]any
		require.NoError(t, json.Unmarshal(s.Tables["ordinary_email_intents"], &table))
		row := table["rows"].([]any)[1].(map[string]any)
		row["state"] = "sent"
		private := row["private"].(map[string]any)
		private["lease_token"] = ""
		private["lease_until"] = nil
		s.Tables["ordinary_email_intents"], _ = json.Marshal(table)
		var logs []domain.EmailLog
		require.NoError(t, json.Unmarshal(s.Tables["email_logs"], &logs))
		for i := range logs {
			if logs[i].ID == active.EmailLogID {
				logs[i].Status = "sent"
				logs[i].DeliveryStatus = "sent"
			}
		}
		s.Tables["email_logs"], _ = json.Marshal(logs)
	})
	require.Error(t, RestoreSnapshot(context.Background(), target, "MSG", "mail-generation", path, nil))
	// Failure after transactional sequence repair and quarantine rolls everything
	// back, including the recovery event and the sequence restart.
	require.NoError(t, os.WriteFile(path, raw, 0600))
	var beforeEvents int64
	require.NoError(t, target.Model(&domain.LocalChangeJournal{}).Count(&beforeEvents).Error)
	require.Error(t, RestoreSnapshot(context.Background(), target, "MSG", "mail-generation", path, func(context.Context, *gorm.DB) error { return errors.New("synthetic rebuild failure") }))
	var afterEvents int64
	require.NoError(t, target.Model(&domain.LocalChangeJournal{}).Count(&afterEvents).Error)
	require.Equal(t, beforeEvents, afterEvents)
	require.Equal(t, "unknown", ordinaryRead(t, restored, pending.ID).State)
	next := ordinaryEnqueue(t, restored, "post-restore-rollback")
	require.Greater(t, next.ID, active.ID)
	ordinaryRun(t, restored)
	require.Equal(t, "sent", ordinaryRead(t, restored, next.ID).State)

	var public domain.OrdinaryEmailIntent
	require.NoError(t, target.First(&public, pending.ID).Error)
	encoded, _ := json.Marshal(public)
	require.NotContains(t, string(encoded), "ordinary-private")
	require.NotContains(t, string(encoded), "sender@example")
}
func TestOrdinaryEmailConfigChangeStopsInsteadOfRerouting(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	item := ordinaryEnqueue(t, r, "transport-change")
	r.Config.OrdinaryEmail.Host = "smtp.other.test"
	r.OrdinaryEmailTransport = ordinaryTestTransport(func(context.Context, func() error) domain.OrdinarySMTPResult {
		t.Fatal("changed transport sent old intent")
		return domain.OrdinarySMTPResult{}
	})
	ordinaryRun(t, r)
	require.Equal(t, "failed", ordinaryRead(t, r, item.ID).State)
}
func TestOrdinaryEmailMigrationDoesNotAdoptHistoricalLogs(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	old := domain.EmailLog{AccountID: 91, EmailType: "notification", Status: "failed", DeliveryStatus: "failed", ContentText: "historical-private"}
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, database.AutoMigrateModule(db, "MSG"))
	require.NoError(t, db.First(&old, old.ID).Error)
	require.Empty(t, old.ManagedQueue)
	require.Equal(t, "historical-private", old.ContentText)
	var n int64
	require.NoError(t, db.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
	require.Zero(t, n)
}
func TestPostgresOrdinaryEmailWorkersAndManualRetry(t *testing.T) {
	for _, scenario := range []string{"concurrent_submission", "unknown_excluded", "snapshot_recovery", "crash_recovery", "outcome_event_rollback", "anonymization", "concurrent_anonymization", "conversation_cleanup", "conversation_admission"} {
		t.Run(scenario, func(t *testing.T) {
			switch scenario {
			case "conversation_cleanup":
				ordinaryConversationCleanupTest(t, true)
				return
			case "conversation_admission":
				ordinaryConversationAdmissionTest(t, true)
				return
			case "concurrent_anonymization":
				ordinaryConcurrentAnonymizeTest(t)
				return
			case "crash_recovery":
				ordinaryCrashTest(t, true)
				return
			case "outcome_event_rollback":
				ordinaryOutcomeRollbackTest(t, true)
				return
			case "anonymization":
				ordinaryAnonymizeTest(t, true)
				return
			}
			if scenario == "snapshot_recovery" {
				ordinarySnapshotTest(t, true)
				return
			}
			r := ordinaryRuntime(t, postgresModuleDB(t, "MSG"))
			count, bodies := ordinarySMTPServer(t, &r, scenario == "unknown_excluded")
			item := ordinaryEnqueue(t, r, "pg-"+scenario)
			legacy := service.NewEmailService(r.DB)
			mock := service.NewMockEmailSender()
			legacy.SetSender(mock)
			start := make(chan struct{})
			errs := make(chan error, 4)
			for i := 0; i < 2; i++ {
				go func() { <-start; _, e := r.RunOrdinaryEmailOnce(context.Background()); errs <- e }()
			}
			for i := 0; i < 2; i++ {
				go func() {
					<-start
					_, _, e := legacy.RetryFailedEmail(context.Background(), 91, item.EmailLogID)
					if e == nil {
						e = errors.New("managed email escaped into legacy retry")
					} else {
						e = nil
					}
					errs <- e
				}()
			}
			close(start)
			for i := 0; i < 4; i++ {
				require.NoError(t, <-errs)
			}
			select {
			case body := <-bodies:
				require.Contains(t, body, "ordinary-private-body")
			case <-time.After(2 * time.Second):
				t.Fatal("SMTP did not accept body")
			}
			require.Equal(t, int32(1), count.Load())
			require.Empty(t, mock.GetSentEmails())
			expected := "sent"
			if scenario == "unknown_excluded" {
				expected = "unknown"
			}
			require.Equal(t, expected, ordinaryRead(t, r, item.ID).State)
			var audit domain.EmailLog
			require.NoError(t, r.DB.First(&audit, item.EmailLogID).Error)
			require.Zero(t, audit.MaxRetries)
			var oldSelector int64
			require.NoError(t, r.DB.Model(&domain.EmailLog{}).Where("id = ? AND retry_count < max_retries", item.EmailLogID).Count(&oldSelector).Error)
			require.Zero(t, oldSelector, "even a pre-upgrade legacy retry must reject new managed rows")

			// Even the old failed-only selection cannot take a managed intent.
			require.NoError(t, r.DB.Model(&domain.EmailLog{}).Where("id = ?", item.EmailLogID).Updates(map[string]any{"status": "failed", "delivery_status": "failed", "max_retries": 3}).Error)
			n, e := legacy.RetryFailedEmails(context.Background(), 10)
			require.NoError(t, e)
			require.Zero(t, n)
			_, _, e = legacy.RetryFailedEmail(context.Background(), 91, item.EmailLogID)
			require.Error(t, e)
			require.Empty(t, mock.GetSentEmails())
		})
	}
}

func ordinaryConcurrentAnonymizeTest(t *testing.T) {
	r := ordinaryRuntime(t, postgresModuleDB(t, "MSG"))
	ordinaryEnqueue(t, r, "old-before-anonymize")
	selected, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	require.NoError(t, r.DB.Callback().Query().After("gorm:query").Register("pause_ordinary_anonymize", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]domain.OrdinaryEmailIntent); ok && once.CompareAndSwap(false, true) {
			close(selected)
			<-release
		}
	}))
	anonDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { anonDone <- ordinaryAnonymize(t, r, "concurrent-anonymize") }()
	<-selected
	sendDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { sendDone <- ordinaryCall(r, ordinaryCommand("after-anonymize")) }()
	var early *httptest.ResponseRecorder
	select {
	case early = <-sendDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.Equal(t, 200, (<-anonDone).Code)
	require.NoError(t, r.DB.Callback().Query().Remove("pause_ordinary_anonymize"))
	if early != nil {
		t.Fatalf("enqueue crossed account anonymization fence: %d", early.Code)
	}
	response := <-sendDone
	require.Equal(t, 200, response.Code, response.Body.String())
	var items []domain.OrdinaryEmailIntent
	require.NoError(t, r.DB.Order("id ASC").Find(&items).Error)
	require.Len(t, items, 2)
	require.True(t, items[0].Anonymized)
	require.Empty(t, items[0].Payload)
	require.Equal(t, "pending", items[1].State)
	var log domain.EmailLog
	require.NoError(t, r.DB.First(&log, items[1].EmailLogID).Error)
	require.NotEmpty(t, log.ToEmail, "a post-fence intent must not be paired with a pre-fence anonymized log")
}

func TestOrdinaryEmailLostMarkerAcknowledgementKeepsUnknownCheckpoint(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	item := ordinaryEnqueue(t, r, "lost-marker-ack")
	claim, _, err := r.claimOrdinaryEmail(context.Background())
	require.NoError(t, err)
	require.NoError(t, r.startOrdinarySubmission(context.Background(), claim))
	// Model the commit succeeding but its acknowledgement being lost: transport
	// received a hook error and never sent DATA, while the DB retains submitting.
	require.NoError(t, r.completeOrdinaryEmail(context.Background(), claim, "pending", "pre_submission_cancelled", true))
	require.Equal(t, "unknown", ordinaryRead(t, r, item.ID).State)
	path := filepath.Join(t.TempDir(), "msg.json")
	require.NoError(t, CreateSnapshot(r.DB, "MSG", "lost-marker", path))
	target := newModuleTestDB(t, "MSG")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "MSG", "lost-marker", path, nil))
	require.Equal(t, "unknown", ordinaryRead(t, ordinaryRuntime(t, target), item.ID).State)
}
func TestOrdinaryEmailStatusDoesNotMixVersions(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	item := ordinaryEnqueue(t, r, "query-version")
	var old domain.EmailLog
	require.NoError(t, r.DB.First(&old, item.EmailLogID).Error)
	ordinaryRun(t, r)
	view, err := r.ordinaryEmailStatus(old)
	require.NoError(t, err)
	require.Equal(t, "sent", view["status"])
	require.Equal(t, view["status"], view["queue_state"])
	require.Equal(t, 1, view["attempt_count"])
}

func ordinaryConversationCleanup(t *testing.T, r Runtime) *repository.ConversationCleanup {
	t.Helper()
	stores := map[string]*gorm.DB{"MSG": r.DB}
	for _, owner := range []string{"RPT", "QLT", "RTM", "TKT", "API", "AIC", "OPS", "BIL"} {
		stores[owner] = newModuleTestDB(t, owner)
	}
	cleanup, err := repository.NewConversationCleanup(stores)
	require.NoError(t, err)
	return cleanup
}
func ordinaryConversationContract(t *testing.T, r *Runtime, beforeGrant func()) {
	t.Helper()
	registry := NewContractRegistry("CON")
	require.NoError(t, registry.RegisterQuery("con.conversation.get.v1", func(_ context.Context, q foundation.QueryEnvelope) (any, error) {
		if beforeGrant != nil {
			beforeGrant()
		}
		return conversationView{ID: 11, AccountID: q.AccountID}, nil
	}))
	cfg := businessConfig("CON")
	cfg.AllowedCallers["MSG"] = true
	r.Config.Dependencies = map[string]DependencyConfig{"CON": {URL: "http://local-con", Token: cfg.AuthToken, LocalHandler: Runtime{Config: cfg, Contracts: registry}.Handler()}}
	r.Contracts = NewContractRegistry("MSG")
	require.NoError(t, r.RegisterBusinessContracts())
}
func ordinaryTranscriptCommand(key string) foundation.CommandEnvelope {
	cmd := ordinaryCommand(key)
	cmd.Input["conversation_id"] = 11
	cmd.Input["email_type"] = "transcript"
	return cmd
}
func TestOrdinaryEmailConversationCleanupRemovesPrivateQueue(t *testing.T) {
	ordinaryConversationCleanupTest(t, false)
}
func ordinaryConversationCleanupTest(t *testing.T, pg bool) {
	for _, state := range []string{"pending", "leased", "submitting"} {
		t.Run(state, func(t *testing.T) {
			r := ordinaryRuntime(t, ordinaryDB(t, pg))
			ordinaryConversationContract(t, &r, nil)
			res := ordinaryCall(r, ordinaryTranscriptCommand("conversation-email"))
			require.Equal(t, 200, res.Code, res.Body.String())
			var item domain.OrdinaryEmailIntent
			require.NoError(t, r.DB.First(&item).Error)
			var claim domain.OrdinaryEmailIntent
			if state != "pending" {
				var err error
				claim, _, err = r.claimOrdinaryEmail(context.Background())
				require.NoError(t, err)
				if state == "submitting" {
					require.NoError(t, r.startOrdinarySubmission(context.Background(), claim))
				}
			}
			cleanup := ordinaryConversationCleanup(t, r)
			if state == "submitting" {
				require.Error(t, cleanup.CleanupConversation(91, 11))
				require.NotEmpty(t, ordinaryRead(t, r, item.ID).Payload)
				require.NoError(t, r.completeOrdinaryEmail(context.Background(), claim, "unknown", "submission_outcome_unknown", false))
			}
			require.NoError(t, cleanup.CleanupConversation(91, 11))
			for _, model := range []any{&domain.EmailLog{}, &domain.OrdinaryEmailIntent{}, &domain.OrdinaryEmailAttempt{}} {
				var n int64
				require.NoError(t, r.DB.Model(model).Count(&n).Error)
				require.Zero(t, n, "conversation cleanup left ordinary-mail private state")
			}
			if state != "pending" {
				require.Error(t, r.startOrdinarySubmission(context.Background(), claim))
				require.Error(t, r.completeOrdinaryEmail(context.Background(), claim, "sent", "", false))
			}
			require.NotEqual(t, 200, ordinaryCall(r, ordinaryTranscriptCommand("stale-con-grant")).Code)
			next := ordinaryEnqueue(t, r, "unrelated-account-notification")
			ordinaryRun(t, r)
			require.Equal(t, "sent", ordinaryRead(t, r, next.ID).State)
		})
	}
}
func TestOrdinaryEmailConversationDeletionFencesEarlierValidation(t *testing.T) {
	ordinaryConversationAdmissionTest(t, false)
}
func ordinaryConversationAdmissionTest(t *testing.T, pg bool) {
	r := ordinaryRuntime(t, ordinaryDB(t, pg))
	cleanup := ordinaryConversationCleanup(t, r)
	// Deletion completes after the CON preflight began but before MSG admission.
	if pg {
		ordinaryConversationContract(t, &r, func() { require.NoError(t, cleanup.CleanupConversation(91, 11)) })
	} else {
		require.NoError(t, cleanup.CleanupConversation(91, 11))
		ordinaryConversationContract(t, &r, nil)
	}
	response := ordinaryCall(r, ordinaryTranscriptCommand("validated-before-delete"))
	require.NotEqual(t, 200, response.Code)
	var n int64
	require.NoError(t, r.DB.Model(&domain.OrdinaryEmailIntent{}).Count(&n).Error)
	require.Zero(t, n)
}

func TestOrdinaryEmailAdmissionEventFailureRollsBackAllState(t *testing.T) {
	r := ordinaryRuntime(t, nil)
	require.NoError(t, r.DB.Callback().Create().Before("gorm:create").Register("fail_ordinary_pending_event", func(tx *gorm.DB) {
		if j, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && j.Action == "event_outbox" && j.ObjectType == "EmailLog" {
			tx.AddError(errors.New("synthetic enqueue event failure"))
		}
	}))
	res := ordinaryCall(r, ordinaryCommand("enqueue-rollback"))
	require.Equal(t, 503, res.Code)
	for _, model := range []any{&domain.EmailLog{}, &domain.OrdinaryEmailIntent{}, &domain.OrdinaryEmailAttempt{}, &domain.LocalChangeJournal{}} {
		var n int64
		require.NoError(t, r.DB.Model(model).Count(&n).Error)
		require.Zero(t, n)
	}
}

func TestOrdinaryEmailPrivateSQLNeverEntersCombinedLogger(t *testing.T) {
	var captured bytes.Buffer
	r := ordinaryRuntime(t, nil)
	r.DB = r.DB.Session(&gorm.Session{Logger: logger.New(log.New(&captured, "", 0), logger.Config{LogLevel: logger.Info, SlowThreshold: time.Nanosecond})})
	r.Contracts = NewContractRegistry("MSG")
	require.NoError(t, r.RegisterBusinessContracts())
	require.NoError(t, r.DB.Callback().Create().Before("gorm:create").Register("private_intent_insert_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "ordinary_email_intents" {
			tx.AddError(errors.New("ordinary-private-body recipient@example.test"))
		}
	}))
	res := ordinaryCall(r, ordinaryCommand("private-failed-enqueue"))
	require.NotEqual(t, 200, res.Code)
	require.NotContains(t, res.Body.String(), "ordinary-private-body")
	require.NotContains(t, captured.String(), "ordinary-private-body")
	require.NotContains(t, captured.String(), "recipient@example.test")
	require.NoError(t, r.DB.Callback().Create().Remove("private_intent_insert_failure"))
	captured.Reset()
	ordinaryEnqueue(t, r, "private-success-enqueue")
	ordinaryRun(t, r)
	require.NotContains(t, captured.String(), "ordinary-private-body")
	require.NotContains(t, captured.String(), "recipient@example.test")
	path := filepath.Join(t.TempDir(), "private.json")
	require.NoError(t, CreateSnapshot(r.DB, "MSG", "private-logs", path))
	target := newModuleTestDB(t, "MSG").Session(&gorm.Session{Logger: logger.New(log.New(&captured, "", 0), logger.Config{LogLevel: logger.Info, SlowThreshold: time.Nanosecond})})
	require.NoError(t, target.Callback().Create().Before("gorm:create").Register("private_restore_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "ordinary_email_intents" {
			tx.AddError(errors.New("ordinary-private-body recipient@example.test"))
		}
	}))
	captured.Reset()
	err := RestoreSnapshot(context.Background(), target, "MSG", "private-logs", path, nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "ordinary-private-body")
	require.NotContains(t, captured.String(), "ordinary-private-body")
	require.NotContains(t, captured.String(), "recipient@example.test")
	require.NoError(t, target.Callback().Create().Remove("private_restore_failure"))
	captured.Reset()
	require.NoError(t, RestoreSnapshot(context.Background(), target, "MSG", "private-logs", path, nil))
	require.NotContains(t, captured.String(), "ordinary-private-body")
	require.NotContains(t, captured.String(), "recipient@example.test")
}
