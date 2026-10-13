package test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This fixture exercises the actual Account REST route and the real global SMTP
// sender. No managed admission is allowed to contact even this loopback server.
type accountTranscriptSMTP struct {
	listener    net.Listener
	connections atomic.Int32
	mu          sync.Mutex
	messages    []string
	wg          sync.WaitGroup
}

func newAccountTranscriptSMTP(t *testing.T) *accountTranscriptSMTP {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &accountTranscriptSMTP{listener: l}
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
				_, _ = io.WriteString(c, "220 transcript-test ESMTP\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						_, _ = io.WriteString(c, "250 transcript-test\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"), strings.HasPrefix(line, "RSET"):
						_, _ = io.WriteString(c, "250 OK\r\n")
					case strings.HasPrefix(line, "DATA"):
						_, _ = io.WriteString(c, "354 send body\r\n")
						var body strings.Builder
						for {
							line, err = r.ReadString('\n')
							if err != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
							body.WriteString(line)
						}
						s.mu.Lock()
						s.messages = append(s.messages, body.String())
						s.mu.Unlock()
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

func (s *accountTranscriptSMTP) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

func (s *accountTranscriptSMTP) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.messages, "\n")
}

type accountTranscriptFixture struct {
	stopBackground    func()
	db                *gorm.DB
	r                 *gin.Engine
	cfg               *config.Config
	smtp              *accountTranscriptSMTP
	token             string
	accountID, userID uint
	inbox             domain.Inbox
	contact           domain.Contact
	conv              domain.Conversation
}

func newAccountTranscriptFixture(t *testing.T, nativePostgres ...bool) *accountTranscriptFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := newAccountTranscriptSMTP(t)
	host, port, _ := net.SplitHostPort(s.listener.Addr().String())
	for name, value := range map[string]string{
		"ACCOUNT_TRANSCRIPT_QUEUE_ENABLED": "true", "ORDINARY_EMAIL_WORKER_ENABLED": "true",
		"SMTP_ADDRESS": host, "SMTP_PORT": port, "SMTP_USERNAME": "", "SMTP_PASSWORD": "",
		"MAILER_SENDER_EMAIL": "support@transcript.example.test", "SMTP_FROM_EMAIL": "",
		"ENV": "test", "APP_ENV": "test", "ALLOW_MOCK_EMAIL": "false",
	} {
		t.Setenv(name, value)
	}
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "account-transcript.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", JWTSecret: "account-transcript-queue-test-jwt-secret", JWTExpirationHours: 24}
	var db *gorm.DB
	var err error
	if len(nativePostgres) != 0 && nativePostgres[0] {
		cfg.DBDriver, cfg.DBPath = "postgres", ""
		db = newAccountTranscriptPostgresDB(t)
	} else {
		db, err = database.InitDB(cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	f := &accountTranscriptFixture{db: db, cfg: cfg, smtp: s}
	t.Cleanup(func() {
		if f.stopBackground != nil {
			f.stopBackground()
		}
	})
	f.rebuild(router.Options{EmailSender: service.NewMockEmailSender()})
	f.token, f.accountID, f.userID = f.signup(t, "owner@transcript.example.test")
	f.inbox = domain.Inbox{AccountID: f.accountID, Name: "Transcript inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "account-transcript-test-widget"}
	f.contact = domain.Contact{AccountID: f.accountID, Name: "Frozen Customer", Email: "customer@transcript.example.test"}
	for _, row := range []any{&f.inbox, &f.contact} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.conv = domain.Conversation{AccountID: f.accountID, InboxID: f.inbox.ID, ContactID: f.contact.ID, DisplayID: 314, Status: domain.ConversationStatusOpen}
	if err := db.Create(&f.conv).Error; err != nil {
		t.Fatal(err)
	}
	rows := []domain.Message{
		{AccountID: f.accountID, ConversationID: f.conv.ID, SenderType: "Contact", SenderID: f.contact.ID, MessageType: domain.MessageTypeIncoming, Content: "TRANSCRIPT_PUBLIC_BODY_CANARY"},
		{AccountID: f.accountID, ConversationID: f.conv.ID, SenderType: "User", SenderID: f.userID, MessageType: domain.MessageTypeOutgoing, Private: true, Content: "TRANSCRIPT_PRIVATE_NOTE_CANARY"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	f.rebuild(router.Options{})
	return f
}

func (f *accountTranscriptFixture) rebuild(options router.Options) {
	if f.stopBackground != nil {
		f.stopBackground()
	}
	f.r, f.stopBackground = router.SetupRouterWithLifecycle(f.cfg, f.db, ws.NewHub(), options)
}

func (f *accountTranscriptFixture) request(method, path, token, key string, payload any) *httptest.ResponseRecorder {
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func (f *accountTranscriptFixture) signup(t *testing.T, email string) (string, uint, uint) {
	t.Helper()
	w := f.request(http.MethodPost, "/auth/sign_up", "", "", map[string]string{"account_name": "Transcript queue account", "name": "Transcript owner", "email": email, "password": "Password123!"})
	if w.Code != http.StatusCreated {
		t.Fatalf("signup failed: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Data struct {
			Token    string
			User     domain.User
			Accounts []domain.Account
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Data.Accounts) != 1 {
		t.Fatalf("invalid signup: %s", w.Body.String())
	}
	return result.Data.Token, result.Data.Accounts[0].ID, result.Data.User.ID
}

func (f *accountTranscriptFixture) path() string {
	return fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/transcript", f.accountID, f.conv.ID)
}

type accountTranscriptResult struct {
	Message       string `json:"message"`
	Email         string `json:"email"`
	MessagesCount int    `json:"messages_count"`
	EmailLogID    uint   `json:"email_log_id"`
	Status        string `json:"status"`
	Queued        bool   `json:"queued"`
	StatusURL     string `json:"status_url"`
}

func requireAccountTranscriptQueued(t *testing.T, f *accountTranscriptFixture, w *httptest.ResponseRecorder) accountTranscriptResult {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("expected queued admission HTTP 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct{ Data accountTranscriptResult }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	r := response.Data
	if r.Message != "Transcript queued" || !r.Queued || r.Status != "pending" || r.EmailLogID == 0 || r.StatusURL != fmt.Sprintf("/api/v1/accounts/%d/email_logs/%d", f.accountID, r.EmailLogID) {
		t.Fatalf("expected durable pending admission, got %+v", r)
	}
	return r
}

func requireAccountTranscriptCounts(t *testing.T, f *accountTranscriptFixture, logs, intents, attempts int64) {
	t.Helper()
	for _, item := range []struct {
		model any
		want  int64
	}{{&domain.EmailLog{}, logs}, {&domain.OrdinaryEmailIntent{}, intents}, {&domain.OrdinaryEmailAttempt{}, attempts}} {
		var got int64
		if err := f.db.Model(item.model).Count(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != item.want {
			t.Fatalf("%T count=%d, want %d", item.model, got, item.want)
		}
	}
}

func TestAccountTranscriptQueueHTTPAdmissionAndWorker(t *testing.T) {
	testAccountTranscriptAdmissionAndWorker(t, false)
}

func testAccountTranscriptAdmissionAndWorker(t *testing.T, nativePostgres bool) {
	f := newAccountTranscriptFixture(t, nativePostgres)
	w := f.request(http.MethodPost, f.path(), f.token, "admit-once", map[string]string{"email": "supervisor@transcript.example.test"})
	if f.smtp.connections.Load() != 0 || f.smtp.count() != 0 {
		t.Fatalf("REST contacted SMTP before durable admission: connections=%d accepted=%d", f.smtp.connections.Load(), f.smtp.count())
	}
	r := requireAccountTranscriptQueued(t, f, w)
	if r.MessagesCount != 2 || r.Email != "supervisor@transcript.example.test" {
		t.Fatalf("wrong frozen transcript response: %+v", r)
	}
	requireAccountTranscriptCounts(t, f, 1, 1, 0)
	var log domain.EmailLog
	if err := f.db.First(&log, r.EmailLogID).Error; err != nil {
		t.Fatal(err)
	}
	if log.Status != "pending" || log.ManagedQueue != "ordinary_smtp_v1" || log.ContentHTML != "" || log.ContentText != "" || log.MaxRetries != 0 {
		t.Fatalf("invalid managed public log: id=%d state=%s marker=%s", log.ID, log.Status, log.ManagedQueue)
	}
	status := f.request(http.MethodGet, r.StatusURL, f.token, "", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"queue_state":"pending"`) {
		t.Fatalf("managed status missing: %d %s", status.Code, status.Body.String())
	}
	for _, secret := range []string{"TRANSCRIPT_PUBLIC_BODY_CANARY", "TRANSCRIPT_PRIVATE_NOTE_CANARY", `"payload"`, `"payload_hash"`, `"transport_key"`, `"lease_token"`} {
		if strings.Contains(status.Body.String(), secret) {
			t.Fatalf("managed status exposed private data %q", secret)
		}
	}
	requireAccountTranscriptStatusListAgreement(t, f, r.EmailLogID, "pending")
	cfg, err := modruntime.LoadOrdinaryEmailConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	runtime := modruntime.Runtime{DB: f.db, Config: modruntime.Config{ModuleID: "MSG", ContractVersion: "v1", OrdinaryEmail: cfg}, OrdinaryEmailTransport: service.NewSMTPEmailSender(cfg.Host, cfg.Port, cfg.Username, cfg.Password)}
	processed, err := runtime.RunOrdinaryEmailOnce(context.Background())
	if err != nil || !processed || f.smtp.count() != 1 {
		t.Fatalf("worker did not submit exactly once: processed=%v accepted=%d err=%v", processed, f.smtp.count(), err)
	}
	if !strings.Contains(f.smtp.body(), "TRANSCRIPT_PUBLIC_BODY_CANARY") || !strings.Contains(f.smtp.body(), "TRANSCRIPT_PRIVATE_NOTE_CANARY") {
		t.Fatal("worker lost existing Account transcript rendering/private-note semantics")
	}
	status = f.request(http.MethodGet, r.StatusURL, f.token, "", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"queue_state":"sent"`) {
		t.Fatalf("managed status did not settle: %d %s", status.Code, status.Body.String())
	}
	requireAccountTranscriptStatusListAgreement(t, f, r.EmailLogID, "sent")
	if processed, err := runtime.RunOrdinaryEmailOnce(context.Background()); err != nil || processed {
		t.Fatalf("settled queue repeated work: %v %v", processed, err)
	}
	requireAccountTranscriptCounts(t, f, 1, 1, 1)
}

func TestAccountTranscriptQueueHTTPReplaySurvivesChangingSources(t *testing.T) {
	testAccountTranscriptReplaySurvivesChangingSources(t, false)
}

func testAccountTranscriptReplaySurvivesChangingSources(t *testing.T, nativePostgres bool) {
	f := newAccountTranscriptFixture(t, nativePostgres)
	first := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "frozen-input", map[string]string{}))
	var original domain.OrdinaryEmailIntent
	if err := f.db.First(&original).Error; err != nil {
		t.Fatal(err)
	}
	// The HTML includes wall time; crossing a second proves a retry did not render again.
	time.Sleep(1100 * time.Millisecond)
	if err := f.db.Model(&domain.Message{}).Where("conversation_id = ?", f.conv.ID).Update("content", "CHANGED_MESSAGE_MUST_NOT_REPLACE_FROZEN_BODY").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&domain.Message{AccountID: f.accountID, ConversationID: f.conv.ID, SenderType: "Contact", SenderID: f.contact.ID, Content: "A later message"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.contact).Updates(map[string]any{"email": "changed@transcript.example.test", "name": "Changed Customer"}).Error; err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(f.smtp.listener.Addr().String())
	provider, _ := json.Marshal(map[string]any{"smtp_address": host, "smtp_port": port, "smtp_enabled": true})
	if err := f.db.Model(&f.inbox).Update("provider_config", string(provider)).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAILER_SENDER_EMAIL", "changed-sender@transcript.example.test")
	t.Setenv("ACCOUNT_TRANSCRIPT_QUEUE_ENABLED", "false")
	t.Setenv("ORDINARY_EMAIL_WORKER_ENABLED", "false")
	f.rebuild(router.Options{})
	replayed := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "frozen-input", map[string]string{}))
	if replayed != first {
		t.Fatalf("exact key changed its frozen result: first=%+v replay=%+v", first, replayed)
	}
	var current domain.OrdinaryEmailIntent
	if err := f.db.First(&current, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Payload != original.Payload || current.PayloadHash != original.PayloadHash || current.TransportKey != original.TransportKey {
		t.Fatal("exact-key retry changed immutable intent")
	}
	conflict := f.request(http.MethodPost, f.path(), f.token, "frozen-input", map[string]string{"email": "different@transcript.example.test"})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed explicit request must conflict, got %d %s", conflict.Code, conflict.Body.String())
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("replay/configuration change fell back to SMTP")
	}
	requireAccountTranscriptCounts(t, f, 1, 1, 0)
}

func TestAccountTranscriptQueueHTTPAuthorityAndInputDenial(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	// Signup uses the same real auth route but a mock sender to isolate transcript traffic.
	f.rebuild(router.Options{EmailSender: service.NewMockEmailSender()})
	otherToken, otherAccount, _ := f.signup(t, "outsider@transcript.example.test")
	f.rebuild(router.Options{})
	for _, tc := range []struct {
		name, path, token, key string
		body                   any
		status                 int
	}{
		{"unauthenticated", f.path(), "", "denied", map[string]string{"email": "x@example.test"}, 401},
		{"nonmember", f.path(), otherToken, "denied", map[string]string{"email": "x@example.test"}, 403},
		{"cross-account-conversation", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/transcript", otherAccount, f.conv.ID), otherToken, "denied", map[string]string{"email": "x@example.test"}, 404},
		{"invalid-recipient", f.path(), f.token, "invalid-email", map[string]string{"email": "not an email"}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request(http.MethodPost, tc.path, tc.token, tc.key, tc.body)
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d %s", tc.status, w.Code, w.Body.String())
			}
		})
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("denied managed request attempted SMTP")
	}
	requireAccountTranscriptCounts(t, f, 0, 0, 0)
}

func TestAccountTranscriptQueueHTTPSenderEligibility(t *testing.T) {
	for _, mode := range []string{"global", "injected-mock", "injected-smtp", "inbox-smtp", "producer-disabled", "fallback-from"} {
		t.Run(mode, func(t *testing.T) {
			f := newAccountTranscriptFixture(t)
			options := router.Options{}
			mock := service.NewMockEmailSender()
			host, port, _ := net.SplitHostPort(f.smtp.listener.Addr().String())
			switch mode {
			case "injected-mock":
				options.EmailSender = mock
			case "injected-smtp":
				options.EmailSender = service.NewSMTPEmailSender(host, port, "", "")
			case "inbox-smtp":
				provider, _ := json.Marshal(map[string]any{"smtp_address": host, "smtp_port": port, "smtp_enabled": true})
				if err := f.db.Model(&f.inbox).Update("provider_config", string(provider)).Error; err != nil {
					t.Fatal(err)
				}
			case "producer-disabled":
				t.Setenv("ACCOUNT_TRANSCRIPT_QUEUE_ENABLED", "false")
			case "fallback-from":
				t.Setenv("MAILER_SENDER_EMAIL", "")
				t.Setenv("SMTP_FROM_EMAIL", "")
				t.Setenv("ORDINARY_EMAIL_WORKER_ENABLED", "false")
			}
			f.rebuild(options)
			w := f.request(http.MethodPost, f.path(), f.token, "eligibility", map[string]string{"email": "recipient@transcript.example.test"})
			if mode == "global" {
				requireAccountTranscriptQueued(t, f, w)
				if f.smtp.connections.Load() != 0 {
					t.Fatal("eligible global SMTP executed synchronously")
				}
				requireAccountTranscriptCounts(t, f, 1, 1, 0)
				return
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Transcript sent successfully") {
				t.Fatalf("legacy %s flow changed: %d %s", mode, w.Code, w.Body.String())
			}
			requireAccountTranscriptCounts(t, f, 1, 0, 0)
			var log domain.EmailLog
			if err := f.db.First(&log).Error; err != nil {
				t.Fatal(err)
			}
			if log.ManagedQueue != "" || log.Status != "sent" {
				t.Fatalf("ineligible %s transcript was adopted", mode)
			}
			if mode == "injected-mock" {
				if f.smtp.connections.Load() != 0 || len(mock.GetSentEmails()) != 1 {
					t.Fatal("injected mock sender was bypassed")
				}
			} else if f.smtp.count() != 1 {
				t.Fatalf("legacy %s sender did not execute once: %d", mode, f.smtp.count())
			}
		})
	}
}

func TestAccountTranscriptQueueHTTPFailedAdmissionNeverFallsBack(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	if err := f.db.Callback().Create().Before("gorm:create").Register("test:fail-managed-intent", func(tx *gorm.DB) {
		if tx.Statement.Table == "ordinary_email_intents" {
			tx.AddError(fmt.Errorf("synthetic private intent checkpoint failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	w := f.request(http.MethodPost, f.path(), f.token, "store-failure", map[string]string{"email": "recipient@transcript.example.test"})
	if w.Code < 500 {
		t.Fatalf("failed managed admission unexpectedly succeeded: %d %s", w.Code, w.Body.String())
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("failed managed admission fell back to direct SMTP")
	}
	if strings.Contains(w.Body.String(), "synthetic private intent") {
		t.Fatal("private storage error leaked to REST")
	}
	requireAccountTranscriptCounts(t, f, 0, 0, 0)
}

func TestAccountTranscriptQueueHTTPIdempotencyScope(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	f.rebuild(router.Options{EmailSender: service.NewMockEmailSender()})
	secondToken, secondAccount, secondUser := f.signup(t, "second-owner@transcript.example.test")
	if err := f.db.Create(&domain.AccountUser{AccountID: f.accountID, UserID: secondUser, Role: domain.RoleAdministrator}).Error; err != nil {
		t.Fatal(err)
	}
	secondConversation := domain.Conversation{AccountID: f.accountID, InboxID: f.inbox.ID, ContactID: f.contact.ID, DisplayID: 315, Status: domain.ConversationStatusOpen}
	otherInbox := domain.Inbox{AccountID: secondAccount, Name: "Other account inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "other-account-transcript-widget"}
	otherContact := domain.Contact{AccountID: secondAccount, Name: "Other customer", Email: "other-customer@example.test"}
	for _, row := range []any{&secondConversation, &otherInbox, &otherContact} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	otherConversation := domain.Conversation{AccountID: secondAccount, InboxID: otherInbox.ID, ContactID: otherContact.ID, DisplayID: 1, Status: domain.ConversationStatusOpen}
	if err := f.db.Create(&otherConversation).Error; err != nil {
		t.Fatal(err)
	}
	f.rebuild(router.Options{})
	seen := map[uint]bool{}
	for _, tc := range []struct {
		accountID, conversationID uint
		token                     string
	}{
		{f.accountID, f.conv.ID, f.token},
		{f.accountID, secondConversation.ID, f.token},
		{f.accountID, f.conv.ID, secondToken},
		{secondAccount, otherConversation.ID, secondToken},
	} {
		path := fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/transcript", tc.accountID, tc.conversationID)
		w := f.request(http.MethodPost, path, tc.token, "same-browser-key", map[string]string{"email": "recipient@example.test"})
		view := *f
		view.accountID = tc.accountID
		result := requireAccountTranscriptQueued(t, &view, w)
		if seen[result.EmailLogID] {
			t.Fatalf("request scope collapsed account, requester or conversation: log=%d", result.EmailLogID)
		}
		seen[result.EmailLogID] = true
		replay := requireAccountTranscriptQueued(t, &view, f.request(http.MethodPost, path, tc.token, "same-browser-key", map[string]string{"email": "recipient@example.test"}))
		if replay != result {
			t.Fatalf("scoped replay changed result: original=%+v replay=%+v", result, replay)
		}
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("scoped admission or replay contacted SMTP")
	}
	requireAccountTranscriptCounts(t, f, 4, 4, 0)
}

func requireAccountTranscriptStatusListAgreement(t *testing.T, f *accountTranscriptFixture, logID uint, state string) {
	t.Helper()
	status := f.request(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/email_logs/%d", f.accountID, logID), f.token, "", nil)
	list := f.request(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/email_logs", f.accountID), f.token, "", nil)
	if status.Code != 200 || list.Code != 200 {
		t.Fatalf("status/list failed: status=%d list=%d", status.Code, list.Code)
	}
	var detail struct {
		Data struct {
			ID             uint
			Status         string
			DeliveryStatus string `json:"delivery_status"`
			QueueState     string `json:"queue_state"`
		}
	}
	var logs struct{ Data []domain.EmailLog }
	if err := json.Unmarshal(status.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(list.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if detail.Data.ID != logID || detail.Data.Status != state || detail.Data.DeliveryStatus != state || detail.Data.QueueState != state {
		t.Fatalf("status fields disagree: %+v", detail.Data)
	}
	for _, row := range logs.Data {
		if row.ID != logID {
			continue
		}
		if row.Status != state || row.DeliveryStatus != state || row.ContentHTML != "" || row.ContentText != "" {
			t.Fatalf("public list disagrees with managed status or includes body: id=%d state=%s delivery=%s", row.ID, row.Status, row.DeliveryStatus)
		}
		return
	}
	t.Fatalf("managed log %d missing from list", logID)
}

func TestAccountTranscriptQueueHTTPConcurrentSameKey(t *testing.T) {
	testAccountTranscriptConcurrentSameKey(t, false)
}

func testAccountTranscriptConcurrentSameKey(t *testing.T, nativePostgres bool) {
	f := newAccountTranscriptFixture(t, nativePostgres)
	const clients = 8
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			responses <- f.request(http.MethodPost, f.path(), f.token, "concurrent-browser-key", map[string]string{"email": "recipient@example.test"})
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	var first accountTranscriptResult
	for response := range responses {
		result := requireAccountTranscriptQueued(t, f, response)
		if first.EmailLogID == 0 {
			first = result
		}
		if result != first {
			t.Fatalf("concurrent exact-key request changed result: first=%+v got=%+v", first, result)
		}
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("concurrent REST requests sent direct SMTP")
	}
	requireAccountTranscriptCounts(t, f, 1, 1, 0)
	var receipts int64
	if err := f.db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND object_type = ?", "command_receipt", "msg.email.send.v1").Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("concurrent admission created %d command receipts, want 1", receipts)
	}
}

func TestAccountTranscriptQueueHTTPKeyValidationAndWorkerDisabled(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"missing", nil}, {"short", []string{"short"}}, {"whitespace", []string{"bad browser key"}},
		{"oversized", []string{strings.Repeat("a", 129)}}, {"duplicate", []string{"same-valid-key", "same-valid-key"}},
		{"comma-joined", []string{"one-valid-key,another-valid-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, f.path(), strings.NewReader(`{"email":"recipient@example.test"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.token)
			for _, key := range tc.keys {
				req.Header.Add("Idempotency-Key", key)
			}
			w := httptest.NewRecorder()
			f.r.ServeHTTP(w, req)
			if w.Code != 400 {
				t.Fatalf("invalid key must be rejected, got %d %s", w.Code, w.Body.String())
			}
		})
	}
	t.Setenv("ORDINARY_EMAIL_WORKER_ENABLED", "false")
	f.rebuild(router.Options{})
	w := f.request(http.MethodPost, f.path(), f.token, "worker-disabled-key", map[string]string{"email": "recipient@example.test"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled worker must fail new managed admission: %d %s", w.Code, w.Body.String())
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("invalid key/disabled worker fell back to direct SMTP")
	}
	requireAccountTranscriptCounts(t, f, 0, 0, 0)
}

func TestAccountTranscriptQueueHTTPReplayRequiresCurrentAuthority(t *testing.T) {
	testAccountTranscriptReplayRequiresCurrentAuthority(t, false)
}

func testAccountTranscriptReplayRequiresCurrentAuthority(t *testing.T, nativePostgres bool) {
	for _, revoke := range []string{"membership", "conversation-permission"} {
		t.Run(revoke, func(t *testing.T) {
			f := newAccountTranscriptFixture(t, nativePostgres)
			var role domain.CustomRole
			if revoke == "conversation-permission" {
				role = domain.CustomRole{AccountID: f.accountID, Name: "Transcript operator", Permissions: `["conversation_manage"]`}
				if err := f.db.Create(&role).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", f.accountID, f.userID).Updates(map[string]any{"role": domain.RoleAgent, "custom_role_id": role.ID}).Error; err != nil {
					t.Fatal(err)
				}
			}
			result := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "authority-replay-key", map[string]string{}))
			if revoke == "membership" {
				if err := f.db.Where("account_id = ? AND user_id = ?", f.accountID, f.userID).Delete(&domain.AccountUser{}).Error; err != nil {
					t.Fatal(err)
				}
			} else if err := f.db.Model(&role).Update("permissions", "[]").Error; err != nil {
				t.Fatal(err)
			}
			w := f.request(http.MethodPost, f.path(), f.token, "authority-replay-key", map[string]string{})
			if w.Code != http.StatusForbidden {
				t.Fatalf("cached receipt bypassed revoked %s: %d %s", revoke, w.Code, w.Body.String())
			}
			if revoke == "membership" {
				status := f.request(http.MethodGet, result.StatusURL, f.token, "", nil)
				if status.Code != http.StatusForbidden {
					t.Fatalf("status bypassed revoked membership: %d %s", status.Code, status.Body.String())
				}
			}
			if f.smtp.connections.Load() != 0 {
				t.Fatal("revoked replay contacted SMTP")
			}
			requireAccountTranscriptCounts(t, f, 1, 1, 0)
		})
	}
}

func TestAccountTranscriptQueueHTTPMessageCapAndBranding(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	brand := domain.BrandedEmailLayout{AccountID: f.accountID, HeaderColor: "#ab12cd", LogoURL: "https://brand.example.test/transcript-logo.png"}
	if err := f.db.Create(&brand).Error; err != nil {
		t.Fatal(err)
	}
	var messages []domain.Message
	for i := 0; i < 205; i++ {
		messages = append(messages, domain.Message{AccountID: f.accountID, ConversationID: f.conv.ID, SenderType: domain.SenderTypeContact, SenderID: f.contact.ID, MessageType: domain.MessageTypeIncoming, Content: fmt.Sprintf("CAP_MESSAGE_%03d", i)})
	}
	if err := f.db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	r := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "message-cap-branding", map[string]string{}))
	if r.MessagesCount != 200 {
		t.Fatalf("existing 200-message cap changed: got %d", r.MessagesCount)
	}
	var intent domain.OrdinaryEmailIntent
	if err := f.db.Where("email_log_id = ?", r.EmailLogID).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	var payload struct{ HTML, Text string }
	if err := json.Unmarshal([]byte(intent.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#ab12cd", "https://brand.example.test/transcript-logo.png", "Frozen Customer", "Transcript inbox", "CAP_MESSAGE_197", "TRANSCRIPT_PRIVATE_NOTE_CANARY"} {
		if !strings.Contains(payload.HTML, want) {
			t.Fatalf("existing rendering lost %q", want)
		}
	}
	if strings.Contains(payload.HTML, "CAP_MESSAGE_198") || strings.Contains(payload.Text, "CAP_MESSAGE_198") {
		t.Fatal("managed transcript exceeded existing 200-message cap")
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("rendering sent synchronous SMTP")
	}
	requireAccountTranscriptCounts(t, f, 1, 1, 0)
}

func TestAccountTranscriptQueueHTTPStatusAccountIsolation(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	result := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "status-isolation", map[string]string{}))
	f.rebuild(router.Options{EmailSender: service.NewMockEmailSender()})
	otherToken, otherAccount, _ := f.signup(t, "status-outsider@transcript.example.test")
	f.rebuild(router.Options{})
	for _, tc := range []struct {
		name, path, token string
		status            int
	}{
		{"unauthenticated", result.StatusURL, "", 401},
		{"nonmember", result.StatusURL, otherToken, 403},
		{"foreign-log", fmt.Sprintf("/api/v1/accounts/%d/email_logs/%d", otherAccount, result.EmailLogID), otherToken, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request(http.MethodGet, tc.path, tc.token, "", nil)
			if w.Code != tc.status {
				t.Fatalf("expected status denial %d, got %d %s", tc.status, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), result.Email) {
				t.Fatal("denied status leaked recipient")
			}
		})
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("read-only status contacted SMTP")
	}
	requireAccountTranscriptStatusListAgreement(t, f, result.EmailLogID, "pending")
}

func TestAccountTranscriptQueueHTTPMalformedBody(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	result := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "fallback-replay-body", map[string]string{}))
	for _, key := range []string{"", "new-malformed-body", "fallback-replay-body"} {
		for _, body := range []string{`{"email":`, `{"email":123}`, `{"email":["recipient@example.test"]}`, `{} {"email":"different@example.test"}`, `{} trailing-garbage`} {
			req := httptest.NewRequest(http.MethodPost, f.path(), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.token)
			if key != "" {
				req.Header.Set("Idempotency-Key", key)
			}
			w := httptest.NewRecorder()
			f.r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("malformed explicit input was accepted or replayed: key=%q body=%q status=%d response=%s", key, body, w.Code, w.Body.String())
			}
		}
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("malformed managed input reached SMTP")
	}
	requireAccountTranscriptCounts(t, f, 1, 1, 0)
	replay := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "fallback-replay-body", map[string]string{}))
	if replay != result {
		t.Fatal("rejected malformed retries changed the original admission")
	}
}

// A supplied native DSN is used only to create a uniquely named schema. All
// application tables, fixtures, transactions, and cleanup remain in that schema.
func newAccountTranscriptPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN is required for isolated native PostgreSQL REST checks")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("native PostgreSQL connection unavailable")
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("phase39_transcript_rest_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		_ = adminSQL.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`).Error; err != nil {
			t.Errorf("isolated native REST schema cleanup failed: %v", err)
		}
		_ = adminSQL.Close()
	})
	connection, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("native PostgreSQL DSN is invalid")
	}
	if connection.RuntimeParams == nil {
		connection.RuntimeParams = map[string]string{}
	}
	connection.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*connection)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("isolated native PostgreSQL connection unavailable")
	}
	var current string
	if err := db.Raw("SELECT current_schema()").Scan(&current).Error; err != nil || current != schema {
		t.Fatal("native PostgreSQL fixture did not select its isolated schema")
	}
	if err := database.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAccountTranscriptQueueNativePostgresREST(t *testing.T) {
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN is required for isolated native PostgreSQL REST checks")
	}
	t.Run("admission_worker_status", func(t *testing.T) { testAccountTranscriptAdmissionAndWorker(t, true) })
	t.Run("concurrent_same_key", func(t *testing.T) { testAccountTranscriptConcurrentSameKey(t, true) })
	t.Run("replay_changed_sources", func(t *testing.T) { testAccountTranscriptReplaySurvivesChangingSources(t, true) })
	t.Run("replay_current_authority", func(t *testing.T) { testAccountTranscriptReplayRequiresCurrentAuthority(t, true) })
}

func TestAccountTranscriptQueueHTTPRecipientNormalization(t *testing.T) {
	f := newAccountTranscriptFixture(t)
	input := map[string]string{"email": " recipient@transcript.example.test "}
	first := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "normalized-recipient", input))
	replay := requireAccountTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.token, "normalized-recipient", input))
	if first != replay || first.Email != "recipient@transcript.example.test" {
		t.Fatalf("receiver normalization changed admission replay: first=%+v replay=%+v", first, replay)
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("recipient normalization contacted SMTP")
	}
	requireAccountTranscriptCounts(t, f, 1, 1, 0)
}
