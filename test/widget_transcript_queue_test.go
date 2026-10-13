package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"gorm.io/gorm"
)

// Widget REST tests reuse the Account queue's isolated database and loopback
// SMTP server. Only the durable worker may contact SMTP in these managed cases.
type widgetTranscriptFixture struct {
	*accountTranscriptFixture
	sourceID string
	session  string
	mapping  domain.ContactInbox
}

func newWidgetTranscriptFixture(t *testing.T, nativePostgres ...bool) *widgetTranscriptFixture {
	t.Helper()
	t.Setenv("WIDGET_TRANSCRIPT_QUEUE_ENABLED", "true")
	f := &widgetTranscriptFixture{accountTranscriptFixture: newAccountTranscriptFixture(t, nativePostgres...), sourceID: "widget-transcript-owner-source"}
	f.mapping = domain.ContactInbox{InboxID: f.inbox.ID, ContactID: f.contact.ID, SourceID: f.sourceID}
	if err := f.db.Create(&f.mapping).Error; err != nil {
		t.Fatal(err)
	}
	f.session = signWidgetVisitorSession(t, f.inbox.ID, f.contact.ID, f.sourceID, f.cfg.JWTSecret)
	f.rebuild(router.Options{})
	return f
}

func (f *widgetTranscriptFixture) path() string {
	return "/api/v1/widget/conversations/transcript"
}

func (f *widgetTranscriptFixture) request(method, path, session, key string, payload any) *httptest.ResponseRecorder {
	return f.requestForWebsite(method, path, f.inbox.WebsiteToken, session, key, payload, nil)
}

func (f *widgetTranscriptFixture) requestForWebsite(method, path, website, session, key string, payload any, headers http.Header) *httptest.ResponseRecorder {
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	req := httptest.NewRequest(method, path+sep+"website_token="+url.QueryEscape(website), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.Header.Set("X-Visitor-Session", session)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	for name, values := range headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func (f *widgetTranscriptFixture) input(email string) map[string]any {
	input := map[string]any{"conversation_id": f.conv.ID}
	if email != "" {
		input["email"] = email
	}
	return input
}

func requireWidgetTranscriptQueued(t *testing.T, f *widgetTranscriptFixture, w *httptest.ResponseRecorder) accountTranscriptResult {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("expected queued Widget admission HTTP 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct{ Data accountTranscriptResult }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	r := response.Data
	if r.Message != "Transcript queued" || !r.Queued || r.Status != "pending" || r.EmailLogID == 0 || r.StatusURL != fmt.Sprintf("/api/v1/widget/conversations/%d/transcript/%d", f.conv.ID, r.EmailLogID) {
		t.Fatalf("expected durable pending Widget admission, got %+v", r)
	}
	return r
}

func requireWidgetTranscriptStatus(t *testing.T, f *widgetTranscriptFixture, r accountTranscriptResult, session, key, state string) {
	t.Helper()
	w := f.request(http.MethodGet, r.StatusURL, session, key, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("Widget status failed: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Data struct {
			ID             uint
			Status         string
			DeliveryStatus string `json:"delivery_status"`
			QueueState     string `json:"queue_state"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.ID != r.EmailLogID || result.Data.Status != state || result.Data.DeliveryStatus != state || result.Data.QueueState != state {
		t.Fatalf("Widget status fields disagree: %+v", result.Data)
	}
	for _, secret := range []string{"TRANSCRIPT_PUBLIC_BODY_CANARY", "TRANSCRIPT_PRIVATE_NOTE_CANARY", `"payload"`, `"payload_hash"`, `"transport_key"`, `"lease_token"`, `"request_scope_hash"`, `"request_input_hash"`, `"content_html"`, `"content_text"`, `"to_email"`, `"from_email"`, `"subject"`, f.sourceID, session, key} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("Widget status exposed body or credential %q", secret)
		}
	}
}

func TestWidgetTranscriptQueueHTTPAdmissionAndWorker(t *testing.T) {
	testWidgetTranscriptAdmissionAndWorker(t, false)
}

func testWidgetTranscriptAdmissionAndWorker(t *testing.T, nativePostgres bool) {
	f := newWidgetTranscriptFixture(t, nativePostgres)
	const key = "widget-admit-once"
	w := f.request(http.MethodPost, f.path(), f.session, key, f.input("recipient@transcript.example.test"))
	if f.smtp.connections.Load() != 0 || f.smtp.count() != 0 {
		t.Fatalf("Widget REST contacted SMTP before durable admission: connections=%d accepted=%d", f.smtp.connections.Load(), f.smtp.count())
	}
	r := requireWidgetTranscriptQueued(t, f, w)
	if r.MessagesCount != 1 || r.Email != "recipient@transcript.example.test" {
		t.Fatalf("Widget public transcript response changed: %+v", r)
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
	var log domain.EmailLog
	if err := f.db.First(&log, r.EmailLogID).Error; err != nil {
		t.Fatal(err)
	}
	if log.Status != "pending" || log.ManagedQueue != "ordinary_smtp_v1" || log.ContentHTML != "" || log.ContentText != "" || log.MaxRetries != 0 {
		t.Fatalf("invalid managed Widget public log: id=%d state=%s marker=%s", log.ID, log.Status, log.ManagedQueue)
	}
	requireWidgetTranscriptStatus(t, f, r, f.session, key, "pending")
	cfg, err := modruntime.LoadOrdinaryEmailConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	runtime := modruntime.Runtime{DB: f.db, Config: modruntime.Config{ModuleID: "MSG", ContractVersion: "v1", OrdinaryEmail: cfg}, OrdinaryEmailTransport: service.NewSMTPEmailSender(cfg.Host, cfg.Port, cfg.Username, cfg.Password)}
	processed, err := runtime.RunOrdinaryEmailOnce(context.Background())
	if err != nil || !processed || f.smtp.count() != 1 {
		t.Fatalf("Widget worker did not submit exactly once: processed=%v accepted=%d err=%v", processed, f.smtp.count(), err)
	}
	if !strings.Contains(f.smtp.body(), "TRANSCRIPT_PUBLIC_BODY_CANARY") || strings.Contains(f.smtp.body(), "TRANSCRIPT_PRIVATE_NOTE_CANARY") {
		t.Fatal("Widget worker changed public-only transcript semantics")
	}
	requireWidgetTranscriptStatus(t, f, r, f.session, key, "sent")
	replayed := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("recipient@transcript.example.test")))
	if replayed != r {
		t.Fatalf("settled Widget delivery changed its immutable admission: first=%+v replay=%+v", r, replayed)
	}
	if processed, err := runtime.RunOrdinaryEmailOnce(context.Background()); err != nil || processed {
		t.Fatalf("settled Widget queue repeated work: %v %v", processed, err)
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 1)
}

func TestWidgetTranscriptQueueHTTPAcceptedDeliverySurvivesMappingRemoval(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	const key = "widget-accepted-before-removal"
	r := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("recipient@example.test")))
	if err := f.db.Delete(&f.mapping).Error; err != nil {
		t.Fatal(err)
	}
	// Removing the current visitor mapping denies new requests and status reads,
	// but does not cancel a delivery that was already authorized and accepted.
	assertDenied := func() {
		t.Helper()
		for _, endpoint := range []struct{ method, path string }{{http.MethodPost, f.path()}, {http.MethodGet, r.StatusURL}} {
			var input any
			if endpoint.method == http.MethodPost {
				input = f.input("recipient@example.test")
			}
			w := f.request(endpoint.method, endpoint.path, f.session, key, input)
			if w.Code != http.StatusForbidden {
				t.Fatalf("removed mapping retained %s access: %d %s", endpoint.method, w.Code, w.Body.String())
			}
		}
	}
	assertDenied()
	if f.smtp.connections.Load() != 0 {
		t.Fatal("accepted Widget request or denied replay contacted SMTP before worker execution")
	}
	cfg, err := modruntime.LoadOrdinaryEmailConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	runtime := modruntime.Runtime{DB: f.db, Config: modruntime.Config{ModuleID: "MSG", ContractVersion: "v1", OrdinaryEmail: cfg}, OrdinaryEmailTransport: service.NewSMTPEmailSender(cfg.Host, cfg.Port, cfg.Username, cfg.Password)}
	processed, err := runtime.RunOrdinaryEmailOnce(context.Background())
	if err != nil || !processed || f.smtp.count() != 1 {
		t.Fatalf("mapping removal canceled an accepted Widget delivery: processed=%v accepted=%d err=%v", processed, f.smtp.count(), err)
	}
	if !strings.Contains(f.smtp.body(), "TRANSCRIPT_PUBLIC_BODY_CANARY") || strings.Contains(f.smtp.body(), "TRANSCRIPT_PRIVATE_NOTE_CANARY") {
		t.Fatal("accepted Widget delivery lost its frozen public-only body")
	}
	var intent domain.OrdinaryEmailIntent
	if err := f.db.Where("email_log_id = ?", r.EmailLogID).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	if intent.State != "sent" {
		t.Fatalf("accepted Widget delivery did not settle after mapping removal: %s", intent.State)
	}
	assertDenied()
	if processed, err := runtime.RunOrdinaryEmailOnce(context.Background()); err != nil || processed || f.smtp.count() != 1 {
		t.Fatalf("accepted Widget delivery repeated after mapping removal: processed=%v accepted=%d err=%v", processed, f.smtp.count(), err)
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 1)
}

func TestWidgetTranscriptQueueHTTPPublicFilterBeforeFirst100(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	var messages []domain.Message
	for i := 0; i < 120; i++ {
		messages = append(messages,
			domain.Message{AccountID: f.accountID, ConversationID: f.conv.ID, SenderType: domain.SenderTypeUser, SenderID: f.userID, MessageType: domain.MessageTypeOutgoing, Private: true, Content: fmt.Sprintf("WIDGET_PRIVATE_CAP_%03d", i)},
			domain.Message{AccountID: f.accountID, ConversationID: f.conv.ID, SenderType: domain.SenderTypeContact, SenderID: f.contact.ID, MessageType: domain.MessageTypeIncoming, Content: fmt.Sprintf("WIDGET_PUBLIC_CAP_%03d", i)},
		)
	}
	if err := f.db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	r := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, "widget-public-cap", f.input("")))
	if r.MessagesCount != 100 {
		t.Fatalf("Widget cap must select first 100 public messages: got %d", r.MessagesCount)
	}
	var intent domain.OrdinaryEmailIntent
	if err := f.db.Where("email_log_id = ?", r.EmailLogID).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	var payload struct{ HTML, Text string }
	if err := json.Unmarshal([]byte(intent.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{payload.HTML, payload.Text} {
		for _, want := range []string{"TRANSCRIPT_PUBLIC_BODY_CANARY", "WIDGET_PUBLIC_CAP_000", "WIDGET_PUBLIC_CAP_098"} {
			if !strings.Contains(body, want) {
				t.Fatalf("Widget first100 public rendering lost %q", want)
			}
		}
		for _, forbidden := range []string{"TRANSCRIPT_PRIVATE_NOTE_CANARY", "WIDGET_PRIVATE_CAP_", "WIDGET_PUBLIC_CAP_099"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("Widget filter/cap exposed %q", forbidden)
			}
		}
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("Widget rendering contacted SMTP before admission")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
}

func TestWidgetTranscriptQueueHTTPReplayFrozenAndRefreshedSession(t *testing.T) {
	testWidgetTranscriptReplayFrozenAndRefreshedSession(t, false)
}

func testWidgetTranscriptReplayFrozenAndRefreshedSession(t *testing.T, nativePostgres bool) {
	f := newWidgetTranscriptFixture(t, nativePostgres)
	const key = "widget-frozen-input"
	first := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("")))
	if first.Email != f.contact.Email {
		t.Fatalf("Widget omitted recipient did not use contact email: %+v", first)
	}
	var original domain.OrdinaryEmailIntent
	if err := f.db.Where("email_log_id = ?", first.EmailLogID).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&domain.Message{}).Where("conversation_id = ?", f.conv.ID).Update("content", "CHANGED_WIDGET_MESSAGE_MUST_NOT_REPLACE_FROZEN_BODY").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&domain.Message{AccountID: f.accountID, ConversationID: f.conv.ID, SenderType: domain.SenderTypeContact, SenderID: f.contact.ID, Content: "Later Widget message"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.contact).Updates(map[string]any{"email": "changed@transcript.example.test", "name": "Changed Widget Customer"}).Error; err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(f.smtp.listener.Addr().String())
	provider, _ := json.Marshal(map[string]any{"smtp_address": host, "smtp_port": port, "smtp_enabled": true})
	if err := f.db.Model(&f.inbox).Update("provider_config", string(provider)).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAILER_SENDER_EMAIL", "changed-sender@transcript.example.test")
	t.Setenv("WIDGET_TRANSCRIPT_QUEUE_ENABLED", "false")
	t.Setenv("ORDINARY_EMAIL_WORKER_ENABLED", "false")
	f.rebuild(router.Options{})
	refreshed := signWidgetVisitorSession(t, f.inbox.ID, f.contact.ID, f.sourceID, f.cfg.JWTSecret)
	if refreshed == f.session {
		t.Fatal("refresh fixture did not mint a distinct visitor token")
	}
	replay := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path()+"?visitor_session="+url.QueryEscape(refreshed), "", key, f.input("")))
	if replay != first {
		t.Fatalf("same SourceID with fresh JWT changed frozen result: first=%+v replay=%+v", first, replay)
	}
	var current domain.OrdinaryEmailIntent
	if err := f.db.First(&current, original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Payload != original.Payload || current.PayloadHash != original.PayloadHash || current.TransportKey != original.TransportKey {
		t.Fatal("Widget exact-key replay changed frozen render, recipient or transport")
	}
	conflict := f.request(http.MethodPost, f.path(), refreshed, key, f.input("different@transcript.example.test"))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed explicit Widget recipient must conflict: %d %s", conflict.Code, conflict.Body.String())
	}
	requireWidgetTranscriptStatus(t, f, first, refreshed, key, "pending")
	if f.smtp.connections.Load() != 0 {
		t.Fatal("Widget refresh/replay fell back to SMTP after configuration changed")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
}

func TestWidgetTranscriptQueueHTTPConcurrentSameKey(t *testing.T) {
	testWidgetTranscriptConcurrentSameKey(t, false)
}

func testWidgetTranscriptConcurrentSameKey(t *testing.T, nativePostgres bool) {
	f := newWidgetTranscriptFixture(t, nativePostgres)
	const clients = 8
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			responses <- f.request(http.MethodPost, f.path(), f.session, "widget-concurrent-key", f.input("recipient@example.test"))
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	var first accountTranscriptResult
	for response := range responses {
		result := requireWidgetTranscriptQueued(t, f, response)
		if first.EmailLogID == 0 {
			first = result
		}
		if result != first {
			t.Fatalf("concurrent Widget exact-key results differ: first=%+v got=%+v", first, result)
		}
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("concurrent Widget admissions sent direct SMTP")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
	var receipts int64
	if err := f.db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND object_type = ?", "command_receipt", "msg.email.send.v1").Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("concurrent Widget admissions created %d receipts", receipts)
	}
}

func TestWidgetTranscriptQueueHTTPFailedAdmissionNeverFallsBack(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	if err := f.db.Callback().Create().Before("gorm:create").Register("test:fail-widget-managed-intent", func(tx *gorm.DB) {
		if tx.Statement.Table == "ordinary_email_intents" {
			tx.AddError(fmt.Errorf("synthetic private Widget intent checkpoint failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	w := f.request(http.MethodPost, f.path(), f.session, "widget-store-failure", f.input("recipient@example.test"))
	if w.Code < 500 {
		t.Fatalf("failed Widget managed admission unexpectedly succeeded: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "synthetic private") {
		t.Fatal("private Widget storage failure leaked to HTTP")
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("failed Widget admission fell back to direct SMTP")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 0, 0, 0)
	var receipts int64
	if err := f.db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND object_type = ?", "command_receipt", "msg.email.send.v1").Count(&receipts).Error; err != nil || receipts != 0 {
		t.Fatalf("failed Widget transaction left a receipt: count=%d err=%v", receipts, err)
	}
}

func TestWidgetTranscriptQueueHTTPAuthorityAndStatusIsolation(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	const key = "widget-scope-isolation"
	r := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("")))
	otherAccount := domain.Account{Name: "Foreign Widget account"}
	if err := f.db.Create(&otherAccount).Error; err != nil {
		t.Fatal(err)
	}
	otherContact := domain.Contact{AccountID: f.accountID, Name: "Other Widget visitor", Email: "other@example.test"}
	otherInbox := domain.Inbox{AccountID: f.accountID, Name: "Other Widget inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "widget-other-inbox"}
	foreignContact := domain.Contact{AccountID: otherAccount.ID, Name: "Foreign Widget visitor", Email: "foreign@example.test"}
	foreignInbox := domain.Inbox{AccountID: otherAccount.ID, Name: "Foreign Widget inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "widget-foreign-inbox"}
	for _, row := range []any{&otherContact, &otherInbox, &foreignContact, &foreignInbox} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []domain.ContactInbox{
		{InboxID: f.inbox.ID, ContactID: otherContact.ID, SourceID: "widget-other-contact"},
		{InboxID: otherInbox.ID, ContactID: f.contact.ID, SourceID: f.sourceID},
		{InboxID: foreignInbox.ID, ContactID: foreignContact.ID, SourceID: f.sourceID},
	} {
		if err := f.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	otherSession := signWidgetVisitorSession(t, f.inbox.ID, otherContact.ID, "widget-other-contact", f.cfg.JWTSecret)
	otherInboxSession := signWidgetVisitorSession(t, otherInbox.ID, f.contact.ID, f.sourceID, f.cfg.JWTSecret)
	foreignSession := signWidgetVisitorSession(t, foreignInbox.ID, foreignContact.ID, f.sourceID, f.cfg.JWTSecret)
	for _, tc := range []struct {
		name, website, session, query string
		status                        int
	}{
		{"missing-session", f.inbox.WebsiteToken, "", "", 403},
		{"raw-source-only", f.inbox.WebsiteToken, "", "?source_id=" + f.sourceID, 403},
		{"invalid-session", f.inbox.WebsiteToken, "invalid", "?source_id=" + f.sourceID, 403},
		{"cross-contact", f.inbox.WebsiteToken, otherSession, "?source_id=" + f.sourceID, 404},
		{"session-wrong-inbox", f.inbox.WebsiteToken, otherInboxSession, "", 403},
		{"same-contact-other-inbox", otherInbox.WebsiteToken, otherInboxSession, "", 404},
		{"session-wrong-account", f.inbox.WebsiteToken, foreignSession, "", 403},
		{"foreign-account", foreignInbox.WebsiteToken, foreignSession, "", 404},
		{"explicit-empty-session", f.inbox.WebsiteToken, f.session, "?visitor_session=", 403},
		{"explicit-invalid-session", f.inbox.WebsiteToken, f.session, "?visitor_session=invalid", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, endpoint := range []struct{ method, path string }{{http.MethodPost, f.path()}, {http.MethodGet, r.StatusURL}} {
				var input any
				if endpoint.method == http.MethodPost {
					input = f.input("")
				}
				w := f.requestForWebsite(endpoint.method, endpoint.path+tc.query, tc.website, tc.session, key, input, nil)
				if w.Code != tc.status {
					t.Fatalf("%s expected authority denial %d, got %d %s", endpoint.method, tc.status, w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), r.Email) || strings.Contains(w.Body.String(), "TRANSCRIPT_") {
					t.Fatal("Widget denied admission/status exposed recipient or transcript")
				}
			}
		})
	}
	for _, badKey := range []string{"", "different-widget-key"} {
		w := f.request(http.MethodGet, r.StatusURL, f.session, badKey, nil)
		want := http.StatusNotFound
		if badKey == "" {
			want = http.StatusBadRequest
		}
		if w.Code != want {
			t.Fatalf("Widget status admitted missing/wrong original key: key=%q code=%d body=%s", badKey, w.Code, w.Body.String())
		}
	}
	accountResult := requireAccountTranscriptQueued(t, f.accountTranscriptFixture, f.accountTranscriptFixture.request(http.MethodPost, f.accountTranscriptFixture.path(), f.token, key, map[string]string{}))
	accountAsWidget := fmt.Sprintf("/api/v1/widget/conversations/%d/transcript/%d", f.conv.ID, accountResult.EmailLogID)
	w := f.request(http.MethodGet, accountAsWidget, f.session, key, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("Widget status exposed Account-origin log: %d %s", w.Code, w.Body.String())
	}
	if accountResult.EmailLogID == r.EmailLogID {
		t.Fatal("Widget and Account producer scopes collided")
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("Widget authority checks contacted SMTP")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 2, 2, 0)
}

func TestWidgetTranscriptQueueHTTPSourceIdentityScopesReplay(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	const key = "widget-source-scoped-key"
	first := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("")))
	newSource := "widget-replacement-source"
	if err := f.db.Model(&f.mapping).Update("source_id", newSource).Error; err != nil {
		t.Fatal(err)
	}
	newSession := signWidgetVisitorSession(t, f.inbox.ID, f.contact.ID, newSource, f.cfg.JWTSecret)
	for _, endpoint := range []struct{ method, path string }{{http.MethodPost, f.path()}, {http.MethodGet, first.StatusURL}} {
		var input any
		if endpoint.method == http.MethodPost {
			input = f.input("")
		}
		w := f.request(endpoint.method, endpoint.path, f.session, key, input)
		if w.Code != http.StatusForbidden {
			t.Fatalf("replaced SourceID retained %s authority: %d %s", endpoint.method, w.Code, w.Body.String())
		}
	}
	w := f.request(http.MethodGet, first.StatusURL, newSession, key, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("new SourceID inherited old transcript status: %d %s", w.Code, w.Body.String())
	}
	second := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), newSession, key, f.input("")))
	if second.EmailLogID == first.EmailLogID {
		t.Fatal("SourceID change reused another visitor scope's durable admission")
	}
	requireWidgetTranscriptStatus(t, f, second, newSession, key, "pending")
	if f.smtp.connections.Load() != 0 {
		t.Fatal("Widget source-scoped replay contacted SMTP")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 2, 2, 0)
}

func TestWidgetTranscriptQueueHTTPReplayRequiresCurrentMapping(t *testing.T) {
	testWidgetTranscriptReplayRequiresCurrentMapping(t, false)
}

func testWidgetTranscriptReplayRequiresCurrentMapping(t *testing.T, nativePostgres bool) {
	for _, change := range []string{"delete-mapping", "reassign-mapping", "delete-conversation", "reassign-conversation-contact", "reassign-conversation-inbox", "reassign-conversation-account"} {
		t.Run(change, func(t *testing.T) {
			f := newWidgetTranscriptFixture(t, nativePostgres)
			const key = "widget-authority-replay"
			r := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("")))
			want := http.StatusNotFound
			var err error
			switch change {
			case "delete-mapping":
				err = f.db.Delete(&f.mapping).Error
				want = http.StatusForbidden
			case "reassign-mapping", "reassign-conversation-contact":
				other := domain.Contact{AccountID: f.accountID, Name: "Reassigned Widget visitor"}
				if err := f.db.Create(&other).Error; err != nil {
					t.Fatal(err)
				}
				if change == "reassign-mapping" {
					err = f.db.Model(&f.mapping).Update("contact_id", other.ID).Error
					want = http.StatusForbidden
				} else {
					err = f.db.Model(&f.conv).Update("contact_id", other.ID).Error
				}
			case "delete-conversation":
				err = repository.NewConversationRepository(f.db).Delete(f.accountID, f.conv.ID)
			case "reassign-conversation-inbox":
				other := domain.Inbox{AccountID: f.accountID, Name: "Reassigned Widget inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "widget-reassigned-inbox"}
				if err := f.db.Create(&other).Error; err != nil {
					t.Fatal(err)
				}
				err = f.db.Model(&f.conv).Update("inbox_id", other.ID).Error
			case "reassign-conversation-account":
				other := domain.Account{Name: "Reassigned Widget account"}
				if err := f.db.Create(&other).Error; err != nil {
					t.Fatal(err)
				}
				err = f.db.Model(&f.conv).Update("account_id", other.ID).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []struct{ method, path string }{{http.MethodPost, f.path()}, {http.MethodGet, r.StatusURL}} {
				var input any
				if endpoint.method == http.MethodPost {
					input = f.input("")
				}
				w := f.request(endpoint.method, endpoint.path, f.session, key, input)
				if w.Code != want {
					t.Fatalf("Widget cached receipt bypassed %s in %s: %d %s", change, endpoint.method, w.Code, w.Body.String())
				}
			}
			if f.smtp.connections.Load() != 0 {
				t.Fatal("Widget revoked mapping replay contacted SMTP")
			}
			if change == "delete-conversation" {
				requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 0, 0, 0)
			} else {
				requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
			}
		})
	}
}

func TestWidgetTranscriptQueueHTTPSecondaryRevokedCredentialDenied(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	const key = "widget-revocation-key"
	r := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("")))
	valid := signWidgetVisitorSession(t, f.inbox.ID, f.contact.ID, f.sourceID, f.cfg.JWTSecret)
	if err := f.db.Create(&domain.RevokedToken{Token: f.session, UserID: f.userID, ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, session string
		headers              http.Header
	}{
		{"primary-revoked", "", f.session, nil},
		{"query-token", "?visitor_session=" + url.QueryEscape(valid) + "&visitor_token=" + url.QueryEscape(f.session), "", nil},
		{"query-session-duplicate", "?visitor_session=" + url.QueryEscape(valid) + "&visitor_session=" + url.QueryEscape(f.session), "", nil},
		{"header-session-secondary", "?visitor_session=" + url.QueryEscape(valid), f.session, nil},
		{"header-token", "", valid, http.Header{"X-Visitor-Token": {f.session}}},
		{"header-session-duplicate", "", valid, http.Header{"X-Visitor-Session": {f.session}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, endpoint := range []struct{ method, path string }{{http.MethodPost, f.path()}, {http.MethodGet, r.StatusURL}} {
				var input any
				if endpoint.method == http.MethodPost {
					input = f.input("")
				}
				w := f.requestForWebsite(endpoint.method, endpoint.path+tc.query, f.inbox.WebsiteToken, tc.session, key, input, tc.headers)
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("%s ignored revoked credential alias %s: %d %s", endpoint.method, tc.name, w.Code, w.Body.String())
				}
			}
		})
	}
	requireWidgetTranscriptStatus(t, f, r, valid, key, "pending")
	if f.smtp.connections.Load() != 0 {
		t.Fatal("Widget revoked credential replay contacted SMTP")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
}

func TestWidgetTranscriptQueueHTTPKeyInputValidationAndWorkerDisabled(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	const key = "widget-valid-original"
	r := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("")))
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"missing", nil}, {"short", []string{"short"}}, {"whitespace", []string{"bad widget key"}},
		{"oversized", []string{strings.Repeat("a", 129)}}, {"duplicate", []string{key, key}},
		{"comma-joined", []string{"one-valid-key,another-valid-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, endpoint := range []struct{ method, path string }{{http.MethodPost, f.path()}, {http.MethodGet, r.StatusURL}} {
				var input any
				if endpoint.method == http.MethodPost {
					input = f.input("")
				}
				w := f.requestForWebsite(endpoint.method, endpoint.path, f.inbox.WebsiteToken, f.session, "", input, http.Header{"Idempotency-Key": tc.keys})
				if w.Code != http.StatusBadRequest {
					t.Fatalf("%s accepted invalid Widget key: %d %s", endpoint.method, w.Code, w.Body.String())
				}
			}
		})
	}
	for _, body := range []string{
		fmt.Sprintf(`{"conversation_id":%d,"email":`, f.conv.ID),
		fmt.Sprintf(`{"conversation_id":%d,"email":123}`, f.conv.ID),
		fmt.Sprintf(`{"conversation_id":%d,"email":["recipient@example.test"]}`, f.conv.ID),
		fmt.Sprintf(`{"conversation_id":%d} {"email":"other@example.test"}`, f.conv.ID),
		fmt.Sprintf(`{"conversation_id":%d} trailing-garbage`, f.conv.ID),
	} {
		req := httptest.NewRequest(http.MethodPost, f.path()+"?website_token="+url.QueryEscape(f.inbox.WebsiteToken), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Visitor-Session", f.session)
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		f.r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("malformed Widget body replayed admission: body=%q code=%d response=%s", body, w.Code, w.Body.String())
		}
	}
	invalidEmail := f.request(http.MethodPost, f.path(), f.session, "widget-invalid-email", f.input("not an email"))
	if invalidEmail.Code != http.StatusBadRequest {
		t.Fatalf("invalid Widget recipient was admitted: %d %s", invalidEmail.Code, invalidEmail.Body.String())
	}
	t.Setenv("ORDINARY_EMAIL_WORKER_ENABLED", "false")
	f.rebuild(router.Options{})
	w := f.request(http.MethodPost, f.path(), f.session, "widget-worker-disabled", f.input("recipient@example.test"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled worker must fail new managed Widget admission: %d %s", w.Code, w.Body.String())
	}
	if replay := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input(""))); replay != r {
		t.Fatal("invalid input/disabled worker changed the accepted Widget receipt")
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("invalid Widget input/disabled worker fell back to SMTP")
	}
	requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
}

func TestWidgetTranscriptQueueHTTPSenderEligibility(t *testing.T) {
	for _, mode := range []string{"global", "injected-mock", "injected-smtp", "inbox-smtp", "producer-disabled", "fallback-from"} {
		t.Run(mode, func(t *testing.T) {
			f := newWidgetTranscriptFixture(t)
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
				t.Setenv("WIDGET_TRANSCRIPT_QUEUE_ENABLED", "false")
			case "fallback-from":
				t.Setenv("MAILER_SENDER_EMAIL", "")
				t.Setenv("SMTP_FROM_EMAIL", "")
				t.Setenv("ORDINARY_EMAIL_WORKER_ENABLED", "false")
			}
			f.rebuild(options)
			w := f.request(http.MethodPost, f.path(), f.session, "widget-eligibility", f.input("recipient@example.test"))
			if mode == "global" {
				requireWidgetTranscriptQueued(t, f, w)
				if f.smtp.connections.Load() != 0 {
					t.Fatal("eligible Widget global SMTP executed synchronously")
				}
				requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
				return
			}
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Transcript sent successfully") {
				t.Fatalf("legacy Widget %s flow changed: %d %s", mode, w.Code, w.Body.String())
			}
			requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 0, 0)
			var log domain.EmailLog
			if err := f.db.First(&log).Error; err != nil {
				t.Fatal(err)
			}
			if log.ManagedQueue != "" || log.Status != "sent" || strings.Contains(log.ContentHTML, "TRANSCRIPT_PRIVATE_NOTE_CANARY") || strings.Contains(log.ContentText, "TRANSCRIPT_PRIVATE_NOTE_CANARY") {
				t.Fatalf("legacy Widget %s transcript was adopted or exposed private content", mode)
			}
			if mode == "injected-mock" {
				if f.smtp.connections.Load() != 0 || len(mock.GetSentEmails()) != 1 {
					t.Fatal("Widget injected mock sender was bypassed")
				}
			} else if f.smtp.count() != 1 {
				t.Fatalf("legacy Widget %s sender did not execute once: %d", mode, f.smtp.count())
			}
		})
	}
}

func TestWidgetTranscriptQueueHTTPVisitorProvenance(t *testing.T) {
	f := newWidgetTranscriptFixture(t)
	const key = "widget-provenance-key"
	r := requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, key, f.input("")))
	var intent domain.OrdinaryEmailIntent
	if err := f.db.Where("email_log_id = ?", r.EmailLogID).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	var log domain.EmailLog
	if err := f.db.First(&log, r.EmailLogID).Error; err != nil {
		t.Fatal(err)
	}
	if log.UserID != nil {
		t.Fatalf("Widget admission fabricated an Account user actor: %d", *log.UserID)
	}
	if intent.RequestScopeHash == nil || intent.RequestInputHash == "" || intent.CommandID == "" || len(intent.CommandID) > 64 || len(intent.CorrelationID) > 64 {
		t.Fatal("Widget receipt lacks durable bounded request identity")
	}
	var journals []domain.LocalChangeJournal
	if err := f.db.Where("command_id = ?", intent.CommandID).Find(&journals).Error; err != nil {
		t.Fatal(err)
	}
	if len(journals) == 0 {
		t.Fatal("Widget admission has no command provenance")
	}
	for _, journal := range journals {
		if journal.AccountID != f.accountID || journal.ActorType != "Contact" || journal.ActorID != f.contact.ID {
			t.Fatalf("Widget journal changed visitor provenance: id=%d actor=%s/%d account=%d", journal.ID, journal.ActorType, journal.ActorID, journal.AccountID)
		}
		if len(journal.CommandID) > 64 || len(journal.CorrelationID) > 64 || len(journal.CausationID) > 64 {
			t.Fatal("Widget provenance exceeds bounded journal fields")
		}
		encoded, _ := json.Marshal(journal)
		for _, secret := range []string{key, f.sourceID, f.session, "TRANSCRIPT_PUBLIC_BODY_CANARY", "TRANSCRIPT_PRIVATE_NOTE_CANARY"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("Widget journal exposed raw request credential or body %q", secret)
			}
		}
	}
	if f.smtp.connections.Load() != 0 {
		t.Fatal("Widget provenance inspection contacted SMTP")
	}
}

func TestWidgetTranscriptQueueNativePostgresREST(t *testing.T) {
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN is required for isolated native PostgreSQL Widget REST checks")
	}
	t.Run("admission_worker_status", func(t *testing.T) { testWidgetTranscriptAdmissionAndWorker(t, true) })
	t.Run("concurrent_same_key", func(t *testing.T) { testWidgetTranscriptConcurrentSameKey(t, true) })
	t.Run("replay_frozen_refreshed_session", func(t *testing.T) { testWidgetTranscriptReplayFrozenAndRefreshedSession(t, true) })
	t.Run("replay_current_mapping", func(t *testing.T) { testWidgetTranscriptReplayRequiresCurrentMapping(t, true) })
}
