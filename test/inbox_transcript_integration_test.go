package test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
)

// These sockets accept only disposable loopback test identities. The fixture
// records AUTH separately so restart tests can prove live credential retrieval.
type inboxTranscriptSMTPFixture struct {
	listener          net.Listener
	connections       atomic.Int32
	transient         atomic.Bool
	loseACK           bool
	mu                sync.Mutex
	passwords, bodies []string
	wg                sync.WaitGroup
}

func newInboxTranscriptSMTP(t *testing.T, transient, loseACK bool) *inboxTranscriptSMTPFixture {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &inboxTranscriptSMTPFixture{listener: l, loseACK: loseACK}
	s.transient.Store(transient)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.connections.Add(1)
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(10 * time.Second))
				r := bufio.NewReader(c)
				_, _ = io.WriteString(c, "220 inbox fixture\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						_, _ = io.WriteString(c, "250-loopback\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(line, "AUTH PLAIN "):
						raw, e := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(line, "AUTH PLAIN ")))
						fields := strings.Split(string(raw), "\x00")
						if e != nil || len(fields) != 3 {
							return
						}
						s.mu.Lock()
						s.passwords = append(s.passwords, fields[2])
						s.mu.Unlock()
						_, _ = io.WriteString(c, "235 authenticated\r\n")
					case strings.HasPrefix(line, "MAIL"):
						_, _ = io.WriteString(c, "250 sender\r\n")
					case strings.HasPrefix(line, "RCPT"):
						if s.transient.CompareAndSwap(true, false) {
							_, _ = io.WriteString(c, "421 INBOX_PROVIDER_ECHO_CANARY retry later\r\n")
							return
						}
						_, _ = io.WriteString(c, "250 recipient\r\n")
					case strings.HasPrefix(line, "DATA"):
						_, _ = io.WriteString(c, "354 body\r\n")
						var body strings.Builder
						for {
							part, e := r.ReadString('\n')
							if e != nil {
								return
							}
							if part == ".\r\n" {
								break
							}
							body.WriteString(part)
						}
						s.mu.Lock()
						s.bodies = append(s.bodies, body.String())
						s.mu.Unlock()
						if s.loseACK {
							return
						}
						_, _ = io.WriteString(c, "250 accepted\r\n")
					case strings.HasPrefix(line, "QUIT"):
						_, _ = io.WriteString(c, "221 bye\r\n")
						return
					default:
						_, _ = io.WriteString(c, "500 unsupported\r\n")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = l.Close(); s.wg.Wait() })
	return s
}

func (s *inboxTranscriptSMTPFixture) observed() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.passwords...), append([]string(nil), s.bodies...)
}

func inboxTranscriptProvider(t *testing.T, addr net.Addr, user, password string) map[string]any {
	t.Helper()
	host, port, err := net.SplitHostPort(addr.String())
	require.NoError(t, err)
	return map[string]any{"smtp_address": host, "smtp_port": port, "smtp_enabled": true, "smtp_username": user, "smtp_password": password}
}

func saveInboxTranscriptProvider(t *testing.T, f *accountTranscriptFixture, provider map[string]any) {
	t.Helper()
	raw, err := json.Marshal(provider)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&f.inbox).Update("provider_config", string(raw)).Error)
	f.inbox.ProviderConfig = string(raw)
}

func inboxTranscriptWorker(t *testing.T, f *accountTranscriptFixture) modruntime.Runtime {
	t.Helper()
	cfg, err := modruntime.LoadOrdinaryEmailConfig(os.Getenv)
	require.NoError(t, err)
	return modruntime.Runtime{DB: f.db, Config: modruntime.Config{ModuleID: "MSG", ContractVersion: "v1", OrdinaryEmail: cfg},
		OrdinaryEmailTransport: service.NewSMTPEmailSender(cfg.Host, cfg.Port, cfg.Username, cfg.Password),
		InboxTranscriptSMTP:    service.NewInboxTranscriptSMTP(repository.NewInboxRepository(f.db), cfg.From)}
}

func inboxTranscriptIntent(t *testing.T, f *accountTranscriptFixture, id uint) domain.OrdinaryEmailIntent {
	t.Helper()
	var item domain.OrdinaryEmailIntent
	require.NoError(t, f.db.Where("email_log_id = ?", id).First(&item).Error)
	return item
}

func TestInboxTranscriptQueueHTTPTwoInboxesEqualFrom(t *testing.T) {
	inboxTranscriptTwoInboxes(t, false)
}

func inboxTranscriptTwoInboxes(t *testing.T, pg bool) {
	t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "true")
	f := newWidgetTranscriptFixture(t, pg)
	one, two := newAccountTranscriptSMTP(t), newAccountTranscriptSMTP(t)
	saveInboxTranscriptProvider(t, f.accountTranscriptFixture, inboxTranscriptProvider(t, one.listener.Addr(), "", ""))
	otherBase := *f.accountTranscriptFixture
	otherBase.inbox = domain.Inbox{AccountID: f.accountID, Name: "second inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "inbox-transcript-second"}
	require.NoError(t, f.db.Create(&otherBase.inbox).Error)
	otherBase.conv = domain.Conversation{AccountID: f.accountID, InboxID: otherBase.inbox.ID, ContactID: f.contact.ID, DisplayID: 315, Status: "open"}
	require.NoError(t, f.db.Create(&otherBase.conv).Error)
	require.NoError(t, f.db.Create(&domain.Message{AccountID: f.accountID, ConversationID: otherBase.conv.ID, Content: "SECOND_INBOX_PUBLIC_CANARY", MessageType: domain.MessageTypeIncoming}).Error)
	require.NoError(t, f.db.Create(&domain.Message{AccountID: f.accountID, ConversationID: otherBase.conv.ID, Content: "SECOND_INBOX_PRIVATE_CANARY", Private: true, MessageType: domain.MessageTypeOutgoing}).Error)
	other := &widgetTranscriptFixture{accountTranscriptFixture: &otherBase, sourceID: "second-inbox-source"}
	other.mapping = domain.ContactInbox{InboxID: other.inbox.ID, ContactID: f.contact.ID, SourceID: other.sourceID}
	require.NoError(t, f.db.Create(&other.mapping).Error)
	other.session = signWidgetVisitorSession(t, other.inbox.ID, f.contact.ID, other.sourceID, f.cfg.JWTSecret)
	saveInboxTranscriptProvider(t, &otherBase, inboxTranscriptProvider(t, two.listener.Addr(), "", ""))
	f.rebuild(router.Options{})
	other.rebuild(router.Options{})
	a := f.accountTranscriptFixture
	accountResult := requireAccountTranscriptQueued(t, a, a.request(http.MethodPost, a.path(), a.token, "equal-from-account", map[string]string{}))
	widgetResult := requireWidgetTranscriptQueued(t, other, other.request(http.MethodPost, other.path(), other.session, "equal-from-widget", other.input("")))
	require.Zero(t, one.connections.Load())
	require.Zero(t, two.connections.Load())
	require.Zero(t, f.smtp.connections.Load())
	for _, tc := range []struct {
		result accountTranscriptResult
		inbox  uint
		prefix string
	}{{accountResult, f.inbox.ID, "acct-inbox-v1:"}, {widgetResult, other.inbox.ID, "wdg-inbox-v1:"}} {
		item := inboxTranscriptIntent(t, a, tc.result.EmailLogID)
		require.Equal(t, "inbox_smtp_v1", item.RouteKind)
		require.Equal(t, strconv.FormatUint(uint64(tc.inbox), 10), item.RouteInboxID)
		require.Empty(t, item.TransportKey)
		require.True(t, strings.HasPrefix(item.CommandID, tc.prefix))
	}
	r := inboxTranscriptWorker(t, a)
	for i := 0; i < 2; i++ {
		ran, err := r.RunOrdinaryEmailOnce(context.Background())
		require.NoError(t, err)
		require.True(t, ran)
	}
	require.Equal(t, 1, one.count())
	require.Equal(t, 1, two.count())
	require.Zero(t, f.smtp.connections.Load())
	require.Contains(t, one.body(), "TRANSCRIPT_PRIVATE_NOTE_CANARY")
	require.NotContains(t, one.body(), "SECOND_INBOX_PUBLIC_CANARY")
	require.Contains(t, two.body(), "SECOND_INBOX_PUBLIC_CANARY")
	require.NotContains(t, two.body(), "SECOND_INBOX_PRIVATE_CANARY")
	require.NotContains(t, two.body(), "TRANSCRIPT_PUBLIC_BODY_CANARY")
	require.Contains(t, one.body(), "From: <support@transcript.example.test>")
	require.Contains(t, two.body(), "From: <support@transcript.example.test>")
	requireWidgetTranscriptStatus(t, other, widgetResult, other.session, "equal-from-widget", "sent")
}

func TestInboxTranscriptQueueHTTPRestartRotatesPasswordKeepsPayload(t *testing.T) {
	for _, producer := range []string{"account", "widget"} {
		t.Run(producer, func(t *testing.T) {
			t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "true")
			f := newWidgetTranscriptFixture(t)
			a := f.accountTranscriptFixture
			s := newInboxTranscriptSMTP(t, true, false)
			provider := inboxTranscriptProvider(t, s.listener.Addr(), "inbox-sender@example.test", "INBOX_OLD_PASSWORD_CANARY")
			saveInboxTranscriptProvider(t, a, provider)
			f.rebuild(router.Options{})
			submit := func(key, email string) accountTranscriptResult {
				if producer == "account" {
					return requireAccountTranscriptQueued(t, a, a.request(http.MethodPost, a.path(), a.token, key, map[string]string{"email": email}))
				}
				return requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input(email)))
			}
			const key = "inbox-restart-password"
			first := submit(key, "")
			frozen := inboxTranscriptIntent(t, a, first.EmailLogID)
			r := inboxTranscriptWorker(t, a)
			ran, err := r.RunOrdinaryEmailOnce(context.Background())
			require.NoError(t, err)
			require.True(t, ran)
			pending := inboxTranscriptIntent(t, a, first.EmailLogID)
			require.Equal(t, "pending", pending.State)
			require.Equal(t, 1, pending.AttemptCount)
			passwords, bodies := s.observed()
			require.Equal(t, []string{"INBOX_OLD_PASSWORD_CANARY"}, passwords)
			require.Empty(t, bodies)
			var attempts []domain.OrdinaryEmailAttempt
			require.NoError(t, f.db.Find(&attempts).Error)
			require.Len(t, attempts, 1)
			require.Nil(t, attempts[0].SubmittingAt)
			provider["smtp_password"] = "INBOX_NEW_PASSWORD_CANARY"
			saveInboxTranscriptProvider(t, a, provider)
			require.NoError(t, f.db.Model(&domain.Message{}).Where("conversation_id = ?", f.conv.ID).Update("content", "LATER_BODY_MUST_NOT_REPLACE_FROZEN").Error)
			require.NoError(t, f.db.Model(&f.contact).Update("email", "later@example.test").Error)
			t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "false")
			t.Setenv("ACCOUNT_TRANSCRIPT_QUEUE_ENABLED", "false")
			t.Setenv("WIDGET_TRANSCRIPT_QUEUE_ENABLED", "false")
			f.rebuild(router.Options{})
			require.Equal(t, first, submit(key, ""))
			require.Equal(t, frozen.Payload, inboxTranscriptIntent(t, a, first.EmailLogID).Payload)
			require.NoError(t, f.db.Model(&pending).Update("next_attempt_at", time.Now().UTC().Add(-time.Second)).Error)
			restarted := inboxTranscriptWorker(t, a)
			ran, err = restarted.RunOrdinaryEmailOnce(context.Background())
			require.NoError(t, err)
			require.True(t, ran)
			passwords, bodies = s.observed()
			require.Equal(t, []string{"INBOX_OLD_PASSWORD_CANARY", "INBOX_NEW_PASSWORD_CANARY"}, passwords)
			require.Len(t, bodies, 1)
			require.Contains(t, bodies[0], "TRANSCRIPT_PUBLIC_BODY_CANARY")
			require.NotContains(t, bodies[0], "LATER_BODY_MUST_NOT_REPLACE_FROZEN")
			if producer == "account" {
				require.Contains(t, bodies[0], "TRANSCRIPT_PRIVATE_NOTE_CANARY")
			} else {
				require.NotContains(t, bodies[0], "TRANSCRIPT_PRIVATE_NOTE_CANARY")
			}
			require.Equal(t, "sent", inboxTranscriptIntent(t, a, first.EmailLogID).State)
			require.Zero(t, f.smtp.connections.Load())
			var public []domain.LocalChangeJournal
			require.NoError(t, f.db.Find(&public).Error)
			raw, err := json.Marshal(public)
			require.NoError(t, err)
			for _, secret := range []string{"INBOX_OLD_PASSWORD_CANARY", "INBOX_NEW_PASSWORD_CANARY", "INBOX_PROVIDER_ECHO_CANARY", "TRANSCRIPT_PUBLIC_BODY_CANARY", "TRANSCRIPT_PRIVATE_NOTE_CANARY"} {
				require.NotContains(t, string(raw), secret)
				require.NotContains(t, pending.ErrorCode, secret)
			}
			var changedCode int
			if producer == "account" {
				changedCode = a.request(http.MethodPost, a.path(), a.token, key, map[string]string{"email": "changed@example.test"}).Code
			} else {
				changedCode = f.request(http.MethodPost, f.path(), f.session, key, f.input("changed@example.test")).Code
			}
			require.Equal(t, http.StatusConflict, changedCode)
		})
	}
}

func TestInboxTranscriptQueueHTTPRouteInvalidationNoFallback(t *testing.T) {
	for _, mutation := range []string{"host", "port", "username", "from", "deleted", "recreated", "disabled", "tenant", "credential_loss", "unsupported_auth"} {
		t.Run(mutation, func(t *testing.T) {
			t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "true")
			f := newWidgetTranscriptFixture(t)
			a := f.accountTranscriptFixture
			s, alternate := newInboxTranscriptSMTP(t, false, false), newInboxTranscriptSMTP(t, false, false)
			provider := inboxTranscriptProvider(t, s.listener.Addr(), "", "")
			if mutation == "credential_loss" {
				provider["smtp_username"] = "test-user"
				provider["smtp_password"] = "DISPOSABLE_PASSWORD"
			}
			saveInboxTranscriptProvider(t, a, provider)
			f.rebuild(router.Options{})
			first := requireAccountTranscriptQueued(t, a, a.request(http.MethodPost, a.path(), a.token, "invalidate-inbox-route", map[string]string{}))
			r := inboxTranscriptWorker(t, a)
			switch mutation {
			case "host":
				provider["smtp_address"] = "localhost"
			case "port":
				_, port, _ := net.SplitHostPort(alternate.listener.Addr().String())
				provider["smtp_port"] = port
			case "username":
				provider["smtp_username"] = "different-user"
				provider["smtp_password"] = "DISPOSABLE_PASSWORD"
			case "from":
				r.InboxTranscriptSMTP = service.NewInboxTranscriptSMTP(repository.NewInboxRepository(f.db), "changed-from@example.test")
			case "deleted":
				require.NoError(t, f.db.Delete(&a.inbox).Error)
			case "recreated":
				old := a.inbox
				require.NoError(t, f.db.Delete(&a.inbox).Error)
				old.CreatedAt = old.CreatedAt.Add(time.Second)
				require.NoError(t, f.db.Create(&old).Error)
			case "disabled":
				provider["smtp_enabled"] = false
			case "tenant":
				require.NoError(t, f.db.Model(&a.inbox).Update("account_id", f.accountID+1000).Error)
			case "credential_loss":
				provider["smtp_password"] = ""
			case "unsupported_auth":
				provider["smtp_authentication"] = "oauth2"
			}
			if mutation != "deleted" && mutation != "recreated" && mutation != "tenant" && mutation != "from" {
				saveInboxTranscriptProvider(t, a, provider)
			}
			ran, err := r.RunOrdinaryEmailOnce(context.Background())
			require.NoError(t, err)
			require.True(t, ran)
			require.Equal(t, "failed", inboxTranscriptIntent(t, a, first.EmailLogID).State)
			_, bodies := s.observed()
			_, otherBodies := alternate.observed()
			require.Empty(t, bodies)
			require.Empty(t, otherBodies)
			require.Zero(t, f.smtp.connections.Load())
			var attempts []domain.OrdinaryEmailAttempt
			require.NoError(t, f.db.Find(&attempts).Error)
			require.Len(t, attempts, 1)
			require.Nil(t, attempts[0].SubmittingAt)
		})
	}
}

func TestInboxTranscriptQueueHTTPGlobalIntentDoesNotAdoptInbox(t *testing.T) {
	t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "true")
	f := newWidgetTranscriptFixture(t)
	a := f.accountTranscriptFixture
	first := requireAccountTranscriptQueued(t, a, a.request(http.MethodPost, a.path(), a.token, "global-before-inbox", map[string]string{}))
	s := newAccountTranscriptSMTP(t)
	saveInboxTranscriptProvider(t, a, inboxTranscriptProvider(t, s.listener.Addr(), "", ""))
	f.rebuild(router.Options{})
	item := inboxTranscriptIntent(t, a, first.EmailLogID)
	require.Empty(t, item.RouteKind)
	require.Empty(t, item.RouteFingerprint)
	require.NotEmpty(t, item.TransportKey)
	r := inboxTranscriptWorker(t, a)
	ran, err := r.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 1, f.smtp.count())
	require.Zero(t, s.connections.Load())
	require.Equal(t, first, requireAccountTranscriptQueued(t, a, a.request(http.MethodPost, a.path(), a.token, "global-before-inbox", map[string]string{})))
}

func TestInboxTranscriptQueueHTTPLostDataReplyStaysUnknown(t *testing.T) {
	t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "true")
	f := newWidgetTranscriptFixture(t)
	a := f.accountTranscriptFixture
	s := newInboxTranscriptSMTP(t, false, true)
	saveInboxTranscriptProvider(t, a, inboxTranscriptProvider(t, s.listener.Addr(), "", ""))
	f.rebuild(router.Options{})
	first := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, "inbox-lost-data-reply", f.input("")))
	r := inboxTranscriptWorker(t, a)
	ran, err := r.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, "unknown", inboxTranscriptIntent(t, a, first.EmailLogID).State)
	requireWidgetTranscriptStatus(t, f, first, f.session, "inbox-lost-data-reply", "unknown")
	restarted := inboxTranscriptWorker(t, a)
	ran, err = restarted.RunOrdinaryEmailOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	legacy := service.NewEmailService(f.db)
	mock := service.NewMockEmailSender()
	legacy.SetSender(mock)
	_, _, err = legacy.RetryFailedEmail(context.Background(), f.accountID, first.EmailLogID)
	require.Error(t, err)
	require.Empty(t, mock.GetSentEmails())
	require.Equal(t, first, requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, "inbox-lost-data-reply", f.input(""))))
	_, bodies := s.observed()
	require.Len(t, bodies, 1)
	require.Zero(t, f.smtp.connections.Load())
}

func TestInboxTranscriptQueueNativePostgresREST(t *testing.T) {
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN is required for isolated native PostgreSQL Inbox REST checks")
	}
	t.Run("two_inboxes_equal_from", func(t *testing.T) { inboxTranscriptTwoInboxes(t, true) })
}

func TestInboxTranscriptQueueHTTPExplicitPrerequisites(t *testing.T) {
	for _, producer := range []string{"account", "widget"} {
		for _, mode := range []string{"default_off", "inbox_off", "producer_off", "worker_off", "selected_invalid"} {
			t.Run(producer+"/"+mode, func(t *testing.T) {
				t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "true")
				f := newWidgetTranscriptFixture(t)
				a := f.accountTranscriptFixture
				s := newAccountTranscriptSMTP(t)
				provider := inboxTranscriptProvider(t, s.listener.Addr(), "", "")
				switch mode {
				case "default_off":
					t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "")
				case "inbox_off":
					t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "false")
				case "producer_off":
					if producer == "account" {
						t.Setenv("ACCOUNT_TRANSCRIPT_QUEUE_ENABLED", "false")
					} else {
						t.Setenv("WIDGET_TRANSCRIPT_QUEUE_ENABLED", "false")
					}
				case "worker_off":
					t.Setenv("ORDINARY_EMAIL_WORKER_ENABLED", "false")
				case "selected_invalid":
					provider["smtp_username"] = "missing-credential"
					provider["smtp_password"] = ""
				}
				saveInboxTranscriptProvider(t, a, provider)
				f.rebuild(router.Options{})
				var code int
				var body string
				if producer == "account" {
					w := a.request(http.MethodPost, a.path(), a.token, "inbox-prerequisite-key", map[string]string{})
					code, body = w.Code, w.Body.String()
				} else {
					w := f.request(http.MethodPost, f.path(), f.session, "inbox-prerequisite-key", f.input(""))
					code, body = w.Code, w.Body.String()
				}
				if mode == "worker_off" || mode == "selected_invalid" {
					require.Equal(t, http.StatusServiceUnavailable, code, body)
					require.Zero(t, s.connections.Load())
					requireAccountTranscriptCounts(t, a, 0, 0, 0)
				} else {
					require.Equal(t, http.StatusOK, code, body)
					require.Contains(t, body, "Transcript sent successfully")
					require.Equal(t, 1, s.count())
					requireAccountTranscriptCounts(t, a, 1, 0, 0)
				}
				require.Zero(t, f.smtp.connections.Load())
			})
		}
	}
}
