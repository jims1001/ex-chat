package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const outboundHTTPSecret = "HTTP-fixture-secret-do-not-echo"
const outboundHTTPBody = "HTTP-fixture-private-text-do-not-echo-in-errors"
const outboundHTTPProviderID = "SM11111111111111111111111111111111"

type outboundHTTPFixture struct {
	db             *gorm.DB
	h              *ConversationHandler
	r              *gin.Engine
	service        *service.ChannelOutboundService
	cfg            channel.Config
	connection     domain.ChannelConnection
	conv           domain.Conversation
	calls          atomic.Int64
	webhookQueries atomic.Int64
	events         chan []byte
	replyStatus    int
	replyBody      string
}

func newOutboundHTTPFixture(t *testing.T, provider string, enabled bool) *outboundHTTPFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "outbound-http.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, db.AutoMigrate(&domain.Conversation{}, &domain.Message{}, &domain.Inbox{}, &domain.Contact{}, &domain.ContactInbox{}, &domain.User{}, &domain.ConversationLabel{}, &domain.ConversationParticipant{}, &domain.LocalChangeJournal{}, &domain.ChannelConnection{}, &domain.OutboundMessageAttempt{}, &domain.AutomationRule{}, &domain.AutomationRuleExecution{}, &domain.Webhook{}, &domain.WebhookDelivery{}, &domain.SLAPolicy{}, &domain.AppliedSLA{}, &domain.SLABreachLog{}, &domain.SLAEvent{}))
	f := &outboundHTTPFixture{db: db, r: gin.New(), events: make(chan []byte, 32), replyStatus: http.StatusCreated}
	f.cfg = channel.Config{OutboundEnabled: enabled, PublicBaseURL: "https://channels.example.test", EncryptionKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), WhatsAppAppID: "1001", WhatsAppAppSecret: "whatsapp-app-secret-fixture", WhatsAppVerifyToken: "whatsapp-verify-token-fixture", WhatsAppAPIVersion: "v24.0"}
	kind, account, sender, recipient := "Channel::TwilioSms", "AC11111111111111111111111111111111", "+14155550100", "+14155550101"
	if provider == "whatsapp" {
		kind, account, sender, recipient = "Channel::Whatsapp", "1002", "1003", "14155550101"
	}
	inbox := domain.Inbox{ID: 21, AccountID: 91, Name: "Outbound HTTP inbox", ChannelType: kind}
	contact := domain.Contact{ID: 31, AccountID: 91, Name: "Bound recipient", PhoneNumber: "+19999999999"}
	require.NoError(t, db.Create(&inbox).Error)
	require.NoError(t, db.Create(&contact).Error)
	require.NoError(t, db.Create(&domain.User{ID: 7, Name: "HTTP agent", Email: "outbound-agent@example.test"}).Error)
	require.NoError(t, db.Create(&domain.ContactInbox{InboxID: inbox.ID, ContactID: contact.ID, SourceID: recipient}).Error)
	f.conv = domain.Conversation{ID: 41, AccountID: 91, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen, ObjectVersion: 1, LastActivityAt: time.Now().UTC().Add(-time.Hour)}
	require.NoError(t, db.Create(&f.conv).Error)
	f.connection = domain.ChannelConnection{AccountID: 91, InboxID: inbox.ID, Provider: provider, ProviderAccountID: account, RecipientID: sender, RecipientKey: channel.StableKey(provider, sender)}
	key, err := f.cfg.Key()
	require.NoError(t, err)
	f.connection.SecretCiphertext, err = channel.Seal(key, channel.StableKey("91", provider, account, sender), outboundHTTPSecret)
	require.NoError(t, err)
	require.NoError(t, db.Create(&f.connection).Error)
	if provider == "whatsapp" {
		f.replyBody = fmt.Sprintf(`{"messaging_product":"whatsapp","contacts":[{"input":%q,"wa_id":%q}],"messages":[{"id":"wamid.http-fixture"}]}`, recipient, recipient)
	} else {
		f.replyBody = fmt.Sprintf(`{"account_sid":%q,"sid":%q,"from":%q,"to":%q,"direction":"outbound-api","num_media":"0","status":"queued"}`, account, outboundHTTPProviderID, sender, recipient)
	}
	conversations := repository.NewConversationRepository(db)
	messages := repository.NewMessageRepositoryWithConversationPort(db, conversations)
	conversations.SetAutomationCapture(true)
	messages.SetAutomationCapture(true)
	inboxes := repository.NewInboxRepository(db)
	transport := channel.NewHTTPTextTransport(&http.Client{Transport: retryHTTPTransport(func(req *http.Request) (*http.Response, error) {
		f.calls.Add(1)
		require.Equal(t, http.MethodPost, req.Method)
		require.Empty(t, req.Header.Get("Idempotency-Key"))
		payload, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Contains(t, string(payload), outboundHTTPBody)
		if provider == "twilio" {
			require.Equal(t, "https://api.twilio.com/2010-04-01/Accounts/"+account+"/Messages.json", req.URL.String())
			user, password, ok := req.BasicAuth()
			require.True(t, ok)
			require.Equal(t, account, user)
			require.Equal(t, outboundHTTPSecret, password)
			fields, err := url.ParseQuery(string(payload))
			require.NoError(t, err)
			require.Equal(t, recipient, fields.Get("To"))
			require.Equal(t, sender, fields.Get("From"))
			require.True(t, strings.HasPrefix(fields.Get("StatusCallback"), f.cfg.PublicBaseURL+"/api/v1/accounts/91/twilio/status/"))
		} else {
			require.Equal(t, "https://graph.facebook.com/v24.0/"+sender+"/messages", req.URL.String())
			require.Equal(t, "Bearer "+outboundHTTPSecret, req.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: f.replyStatus, Body: io.NopCloser(strings.NewReader(f.replyBody)), Header: make(http.Header)}, nil
	})})
	f.service = service.NewChannelOutboundService(service.NewChannelTextOwner(f.cfg, transport, inboxes), conversations, messages)
	hub := ws.NewHub()
	hub.OnBroadcast = func(event *ws.Event) { raw, _ := json.Marshal(event); f.events <- raw }
	go hub.Run()
	f.h = NewConversationHandler(conversations, messages, inboxes, repository.NewContactRepository(db), nil, hub)
	f.h.SetChannelOutboundService(f.service)
	webhooks := service.NewWebhookService(db, repository.NewWebhookRepository(db))
	f.h.SetAutomationAndWebhook(nil, webhooks)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:outbound_http_webhook_queries", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "webhooks" {
			f.webhookQueries.Add(1)
		}
	}))
	f.r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextAccountID, uint(91))
		c.Set(middleware.ContextUserID, uint(7))
		c.Next()
	})
	f.r.POST("/conversations/:id/messages", f.h.CreateMessage)
	f.r.POST("/conversations/:id/messages/:message_id/retry", f.h.RetryMessage)
	driver := NewChannelDriverHandler(nil, inboxes, repository.NewContactRepository(db), conversations, messages)
	driver.SetChannelOutboundService(f.service)
	driver.SetChannelIngressService(service.NewChannelIngressService(f.cfg, nil, inboxes, repository.NewContactRepository(db), conversations, messages))
	f.r.POST("/api/v1/accounts/:account_id/whatsapp/callback", driver.HandleWhatsAppWebhook)
	f.r.POST("/api/v1/accounts/:account_id/twilio/status/:attempt_id", driver.HandleTwilioStatus)
	return f
}

func (f *outboundHTTPFixture) create(t *testing.T, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/conversations/41/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}
func outboundHTTPMessage(t *testing.T, w *httptest.ResponseRecorder) domain.Message {
	t.Helper()
	var payload struct {
		Data domain.Message `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.NotZero(t, payload.Data.ID)
	return payload.Data
}
func (f *outboundHTTPFixture) attempts(t *testing.T) []domain.OutboundMessageAttempt {
	t.Helper()
	var attempts []domain.OutboundMessageAttempt
	require.NoError(t, f.db.Order("sequence ASC").Find(&attempts).Error)
	return attempts
}
func (f *outboundHTTPFixture) retry(messageID uint) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/conversations/41/messages/%d/retry", messageID), nil))
	return w
}
func assertOutboundHTTPNoLeak(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, secret := range []string{outboundHTTPSecret, outboundHTTPBody, "secret_ciphertext", "config_fingerprint", "claim_token"} {
		require.NotContains(t, w.Body.String(), secret)
	}
}

func TestOutboundHTTPCreateQueuesOnlyUntilWorkerDispatches(t *testing.T) {
	for _, provider := range []string{"twilio", "whatsapp"} {
		t.Run(provider, func(t *testing.T) {
			f := newOutboundHTTPFixture(t, provider, true)
			w := f.create(t, fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), "")
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			message := outboundHTTPMessage(t, w)
			require.Equal(t, domain.MessageStatusQueued, message.Status)
			require.Zero(t, f.calls.Load())
			attempts := f.attempts(t)
			require.Len(t, attempts, 1)
			require.Equal(t, message.ID, attempts[0].MessageID)
			require.Equal(t, domain.OutboundStateQueued, attempts[0].State)
			require.Empty(t, attempts[0].ProviderMessageID)
			require.NotContains(t, w.Body.String(), attempts[0].ID)
			require.NotContains(t, w.Body.String(), outboundHTTPSecret)
			require.NotContains(t, w.Body.String(), f.connection.SecretCiphertext)
			worked, err := f.service.DispatchOne(context.Background())
			require.NoError(t, err)
			require.True(t, worked)
			require.EqualValues(t, 1, f.calls.Load())
			var stored domain.Message
			require.NoError(t, f.db.First(&stored, message.ID).Error)
			require.Equal(t, domain.MessageStatusProviderAccepted, stored.Status)
			attempts = f.attempts(t)
			require.NotEmpty(t, attempts[0].ProviderMessageID)
			require.False(t, attempts[0].RetrySafe)
			worked, err = f.service.DispatchOne(context.Background())
			require.NoError(t, err)
			require.False(t, worked)
			require.EqualValues(t, 1, f.calls.Load())
		})
	}
}

func TestOutboundHTTPCreateReplayIsExactAndHasNoRepeatedFanout(t *testing.T) {
	f := newOutboundHTTPFixture(t, "twilio", true)
	body := fmt.Sprintf(`{"content":%q}`, outboundHTTPBody)
	first := f.create(t, body, "browser-submit-1")
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	original := outboundHTTPMessage(t, first)
	var event retryHTTPPayload
	require.NoError(t, json.Unmarshal(retryReceive(t, f.events), &event))
	require.Equal(t, ws.EventMessageCreated, event.Event)
	queries := f.webhookQueries.Load()
	require.Positive(t, queries)
	var before domain.Conversation
	require.NoError(t, f.db.First(&before, f.conv.ID).Error)
	replay := f.create(t, body, "browser-submit-1")
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Equal(t, original, outboundHTTPMessage(t, replay))
	require.Equal(t, queries, f.webhookQueries.Load())
	f.h.hub.Broadcast(&ws.Event{Name: "outbound.replay.barrier"})
	require.NoError(t, json.Unmarshal(retryReceive(t, f.events), &event))
	require.Equal(t, "outbound.replay.barrier", event.Event)
	var after domain.Conversation
	require.NoError(t, f.db.First(&after, f.conv.ID).Error)
	require.Equal(t, before, after)
	require.Len(t, f.attempts(t), 1)
	var count int64
	require.NoError(t, f.db.Model(&domain.Message{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Zero(t, f.calls.Load())
	conflict := f.create(t, `{"content":"changed private message body"}`, "browser-submit-1")
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	assertOutboundHTTPNoLeak(t, conflict)
	require.NotContains(t, conflict.Body.String(), "changed private message body")
	require.Len(t, f.attempts(t), 1)
}

func TestOutboundHTTPPrivateUnsupportedAndDisabledBoundaries(t *testing.T) {
	cases := []struct {
		name, kind, body string
		enabled          bool
		want             int
		status           string
	}{
		{"private note", "", `{"content":"local note","private":true}`, false, http.StatusCreated, domain.MessageStatusSaved},
		{"unsupported media", "", fmt.Sprintf(`{"content":%q,"content_type":"image"}`, outboundHTTPBody), true, http.StatusUnprocessableEntity, ""},
		{"unsupported provider", "Channel::Telegram", fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), true, http.StatusUnprocessableEntity, ""},
		{"disabled transport", "", fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), false, http.StatusServiceUnavailable, ""},
		{"web widget unchanged", "Channel::WebWidget", `{"content":"local widget message"}`, false, http.StatusCreated, domain.MessageStatusSent},
		{"web widget alias unchanged", "web_widget", `{"content":"local widget alias message"}`, false, http.StatusCreated, domain.MessageStatusSent},
		{"api unchanged", "Channel::Api", `{"content":"local API message"}`, false, http.StatusCreated, domain.MessageStatusSent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOutboundHTTPFixture(t, "twilio", tc.enabled)
			if tc.kind != "" {
				require.NoError(t, f.db.Model(&domain.Inbox{}).Where("id = ?", f.conv.InboxID).Update("channel_type", tc.kind).Error)
			}
			w := f.create(t, tc.body, "")
			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.status != "" {
				require.Equal(t, tc.status, outboundHTTPMessage(t, w).Status)
			} else {
				assertOutboundHTTPNoLeak(t, w)
			}
			require.Empty(t, f.attempts(t))
			require.Zero(t, f.calls.Load())
			var count int64
			require.NoError(t, f.db.Model(&domain.Message{}).Count(&count).Error)
			want := int64(0)
			if tc.status != "" {
				want = 1
			}
			require.Equal(t, want, count)
		})
	}
}

func TestOutboundHTTPExplicitRetryRequiresTrackedSafeRejection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		httpStatus int
		reply      string
		wantStatus string
		retry      bool
	}{
		{"documented rejection", 400, `{"code":21211,"status":400,"message":"provider secret should not escape"}`, domain.MessageStatusFailed, true},
		{"ambiguous server failure", 503, `{"body":"provider secret should not escape"}`, domain.MessageStatusUnknown, false},
		{"accepted", 201, "", domain.MessageStatusProviderAccepted, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOutboundHTTPFixture(t, "twilio", true)
			if tc.reply != "" {
				f.replyBody = tc.reply
			}
			f.replyStatus = tc.httpStatus
			w := f.create(t, fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), "")
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			message := outboundHTTPMessage(t, w)
			worked, err := f.service.DispatchOne(context.Background())
			require.NoError(t, err)
			require.True(t, worked)
			require.EqualValues(t, 1, f.calls.Load())
			var before domain.Message
			require.NoError(t, f.db.First(&before, message.ID).Error)
			require.Equal(t, tc.wantStatus, before.Status)
			retry := f.retry(message.ID)
			if !tc.retry {
				require.Equal(t, http.StatusBadRequest, retry.Code, retry.Body.String())
				assertOutboundHTTPNoLeak(t, retry)
				require.Len(t, f.attempts(t), 1)
				worked, err = f.service.DispatchOne(context.Background())
				require.NoError(t, err)
				require.False(t, worked)
				require.EqualValues(t, 1, f.calls.Load())
				return
			}
			require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
			retried := outboundHTTPMessage(t, retry)
			require.Equal(t, message.ID, retried.ID)
			require.Equal(t, domain.MessageStatusQueued, retried.Status)
			require.EqualValues(t, 1, f.calls.Load())
			attempts := f.attempts(t)
			require.Len(t, attempts, 2)
			require.True(t, attempts[0].RetrySafe)
			require.NotEqual(t, attempts[0].ID, attempts[1].ID)
			require.Equal(t, 2, attempts[1].Sequence)
			replay := f.retry(message.ID)
			require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
			require.Equal(t, retried, outboundHTTPMessage(t, replay))
			require.Len(t, f.attempts(t), 2)
			worked, err = f.service.DispatchOne(context.Background())
			require.NoError(t, err)
			require.True(t, worked)
			require.EqualValues(t, 2, f.calls.Load())
			var count int64
			require.NoError(t, f.db.Model(&domain.Message{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestOutboundHTTPQueuedAndAcceptedCannotSatisfySLA(t *testing.T) {
	f := newOutboundHTTPFixture(t, "twilio", true)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	require.NoError(t, f.db.Model(&domain.Conversation{}).Where("id = ?", f.conv.ID).Updates(map[string]any{"first_response_due_at": deadline, "next_response_due_at": deadline}).Error)
	f.h.SetSLAService(service.NewSLAService(f.db, f.h.convRepo))
	var evaluations atomic.Int64
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("test:outbound_http_sla", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "sla_policies" {
			evaluations.Add(1)
		}
	}))
	w := f.create(t, fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), "sla-intent")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	message := outboundHTTPMessage(t, w)
	require.Zero(t, evaluations.Load())
	timeline, err := f.h.msgRepo.ListSLATimeline(91, f.conv.ID)
	require.NoError(t, err)
	require.Empty(t, timeline)
	worked, err := f.service.DispatchOne(context.Background())
	require.NoError(t, err)
	require.True(t, worked)
	replay := f.create(t, fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), "sla-intent")
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Equal(t, domain.MessageStatusProviderAccepted, outboundHTTPMessage(t, replay).Status)
	require.Zero(t, evaluations.Load())
	timeline, err = f.h.msgRepo.ListSLATimeline(91, f.conv.ID)
	require.NoError(t, err)
	require.Empty(t, timeline)
	var after domain.Conversation
	require.NoError(t, f.db.First(&after, f.conv.ID).Error)
	require.NotNil(t, after.FirstResponseDueAt)
	require.NotNil(t, after.NextResponseDueAt)
	require.True(t, deadline.Equal(*after.FirstResponseDueAt))
	require.True(t, deadline.Equal(*after.NextResponseDueAt))
	require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", message.ID).Update("status", domain.MessageStatusSent).Error)
	timeline, err = f.h.msgRepo.ListSLATimeline(91, f.conv.ID)
	require.NoError(t, err)
	require.Len(t, timeline, 1)
	require.Equal(t, message.ID, timeline[0].ID)
}

func outboundHTTPSign(publicURL, secret string, values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var input strings.Builder
	input.WriteString(publicURL)
	for _, key := range keys {
		input.WriteString(key)
		input.WriteString(values.Get(key))
	}
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(input.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestOutboundHTTPTwilioStatusAuthenticatesCanonicalURLAndExactBinding(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*http.Request, url.Values, string)
		want   int
	}{
		{"valid configured public URL", nil, http.StatusNoContent},
		{"query tampering", func(r *http.Request, _ url.Values, _ string) { r.URL.RawQuery = "extra=value" }, http.StatusForbidden},
		{"empty query delimiter tampering", func(r *http.Request, _ url.Values, _ string) { r.URL.ForceQuery = true }, http.StatusForbidden},
		{"leading-zero account path", func(r *http.Request, _ url.Values, _ string) {
			r.URL.Path = strings.Replace(r.URL.Path, "/91/", "/091/", 1)
		}, http.StatusForbidden},
		{"encoded account path", func(r *http.Request, _ url.Values, _ string) {
			r.URL.RawPath = strings.Replace(r.URL.Path, "/91/", "/%39%31/", 1)
		}, http.StatusForbidden},
		{"wrong tenant path", func(r *http.Request, _ url.Values, _ string) {
			r.URL.Path = strings.Replace(r.URL.Path, "/91/", "/92/", 1)
		}, http.StatusForbidden},
		{"wrong signature", func(r *http.Request, _ url.Values, _ string) { r.Header.Set("X-Twilio-Signature", "invalid-signature") }, http.StatusForbidden},
		{"duplicate signature", func(r *http.Request, _ url.Values, _ string) {
			r.Header.Add("X-Twilio-Signature", r.Header.Get("X-Twilio-Signature"))
		}, http.StatusForbidden},
		{"signed request host instead of configured URL", func(r *http.Request, v url.Values, _ string) {
			r.Header.Set("X-Twilio-Signature", outboundHTTPSign("https://attacker.example.test"+r.URL.Path, outboundHTTPSecret, v))
		}, http.StatusForbidden},
		{"wrong account binding", func(r *http.Request, v url.Values, publicURL string) {
			v.Set("AccountSid", "AC22222222222222222222222222222222")
			r.Body = io.NopCloser(strings.NewReader(v.Encode()))
			r.Header.Set("X-Twilio-Signature", outboundHTTPSign(publicURL, outboundHTTPSecret, v))
		}, http.StatusForbidden},
		{"wrong sender binding", func(r *http.Request, v url.Values, publicURL string) {
			v.Set("From", "+14155550222")
			r.Body = io.NopCloser(strings.NewReader(v.Encode()))
			r.Header.Set("X-Twilio-Signature", outboundHTTPSign(publicURL, outboundHTTPSecret, v))
		}, http.StatusForbidden},
		{"wrong recipient binding", func(r *http.Request, v url.Values, publicURL string) {
			v.Set("To", "+14155550333")
			r.Body = io.NopCloser(strings.NewReader(v.Encode()))
			r.Header.Set("X-Twilio-Signature", outboundHTTPSign(publicURL, outboundHTTPSecret, v))
		}, http.StatusForbidden},
		{"wrong message binding", func(r *http.Request, v url.Values, publicURL string) {
			v.Set("MessageSid", "SM22222222222222222222222222222222")
			r.Body = io.NopCloser(strings.NewReader(v.Encode()))
			r.Header.Set("X-Twilio-Signature", outboundHTTPSign(publicURL, outboundHTTPSecret, v))
		}, http.StatusConflict},
		{"duplicate parameter", func(r *http.Request, v url.Values, _ string) {
			v.Add("MessageSid", outboundHTTPProviderID)
			r.Body = io.NopCloser(strings.NewReader(v.Encode()))
		}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOutboundHTTPFixture(t, "twilio", true)
			w := f.create(t, fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), "")
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			message := outboundHTTPMessage(t, w)
			_, err := f.service.DispatchOne(context.Background())
			require.NoError(t, err)
			attempts := f.attempts(t)
			require.Len(t, attempts, 1)
			a := attempts[0]
			values := url.Values{"AccountSid": {a.ProviderAccountID}, "MessageSid": {a.ProviderMessageID}, "From": {a.SenderID}, "To": {a.RecipientID}, "MessageStatus": {"delivered"}, "ExtraSignedField": {"signed-field-content"}}
			path := fmt.Sprintf("/api/v1/accounts/91/twilio/status/%s", a.ID)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-Twilio-Signature", outboundHTTPSign(a.CallbackURL, outboundHTTPSecret, values))
			req.Header.Set("X-Forwarded-Host", "attacker.example.test")
			req.Header.Set("X-Forwarded-Proto", "http")
			if tc.mutate != nil {
				tc.mutate(req, values, a.CallbackURL)
			}
			response := httptest.NewRecorder()
			f.r.ServeHTTP(response, req)
			require.Equal(t, tc.want, response.Code, response.Body.String())
			assertOutboundHTTPNoLeak(t, response)
			require.EqualValues(t, 1, f.calls.Load())
			var after domain.Message
			require.NoError(t, f.db.First(&after, message.ID).Error)
			want := domain.MessageStatusProviderAccepted
			if tc.want == http.StatusNoContent {
				want = domain.MessageStatusDelivered
			}
			require.Equal(t, want, after.Status)
			require.Len(t, f.attempts(t), 1)
		})
	}
}

func TestOutboundHTTPInvalidIntentFailsBeforePersistenceAndDispatch(t *testing.T) {
	cases := []struct {
		name    string
		headers []string
		body    string
		mutate  func(*outboundHTTPFixture)
		want    int
	}{
		{name: "duplicate idempotency header", headers: []string{"same-key", "same-key"}, want: http.StatusBadRequest},
		{name: "padded idempotency key", headers: []string{" padded-key "}, want: http.StatusBadRequest},
		{name: "overlong idempotency key", headers: []string{strings.Repeat("k", 201)}, want: http.StatusBadRequest},
		{name: "empty text", body: `{"content":"   "}`, want: http.StatusBadRequest},
		{name: "overlong Twilio text", body: fmt.Sprintf(`{"content":%q}`, strings.Repeat("x", 1601)), want: http.StatusBadRequest},
		{name: "missing service", mutate: func(f *outboundHTTPFixture) { f.h.SetChannelOutboundService(nil) }, want: http.StatusServiceUnavailable},
		{name: "corrupt credential", mutate: func(f *outboundHTTPFixture) {
			require.NoError(t, f.db.Model(&domain.ChannelConnection{}).Where("id = ?", f.connection.ID).Update("secret_ciphertext", outboundHTTPSecret).Error)
		}, want: http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOutboundHTTPFixture(t, "twilio", true)
			if tc.mutate != nil {
				tc.mutate(f)
			}
			body := tc.body
			if body == "" {
				body = fmt.Sprintf(`{"content":%q}`, outboundHTTPBody)
			}
			req := httptest.NewRequest(http.MethodPost, "/conversations/41/messages", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			for _, key := range tc.headers {
				req.Header.Add("Idempotency-Key", key)
			}
			w := httptest.NewRecorder()
			f.r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			assertOutboundHTTPNoLeak(t, w)
			require.Empty(t, f.attempts(t))
			var count int64
			require.NoError(t, f.db.Model(&domain.Message{}).Count(&count).Error)
			require.Zero(t, count)
			require.Zero(t, f.calls.Load())
			require.Zero(t, f.webhookQueries.Load())
		})
	}
}

func TestOutboundHTTPFailureReceiptPreservesSentProgressAndExposesDeliveryFailure(t *testing.T) {
	f := newOutboundHTTPFixture(t, "twilio", true)
	body := fmt.Sprintf(`{"content":%q}`, outboundHTTPBody)
	created := f.create(t, body, "delivery-failure-visible")
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	message := outboundHTTPMessage(t, created)
	worked, err := f.service.DispatchOne(context.Background())
	require.NoError(t, err)
	require.True(t, worked)
	a := f.attempts(t)[0]
	for _, tc := range []struct {
		receipt, wantStatus string
		failed              bool
	}{
		{"sent", domain.MessageStatusSent, false},
		{"failed", domain.MessageStatusSent, true},
		{"delivered", domain.MessageStatusDelivered, false},
		{"failed", domain.MessageStatusDelivered, false},
	} {
		values := url.Values{"AccountSid": {a.ProviderAccountID}, "MessageSid": {a.ProviderMessageID}, "From": {a.SenderID}, "To": {a.RecipientID}, "MessageStatus": {tc.receipt}}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/91/twilio/status/%s", a.ID), strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Twilio-Signature", outboundHTTPSign(a.CallbackURL, outboundHTTPSecret, values))
		w := httptest.NewRecorder()
		f.r.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		replay := f.create(t, body, "delivery-failure-visible")
		require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
		current := outboundHTTPMessage(t, replay)
		require.Equal(t, message.ID, current.ID)
		require.Equal(t, tc.wantStatus, current.Status, tc.receipt)
		require.Equal(t, tc.failed, current.DeliveryFailed, tc.receipt)
		if tc.failed {
			require.Contains(t, replay.Body.String(), `"delivery_failed":true`)
		} else {
			require.NotContains(t, replay.Body.String(), `"delivery_failed":true`)
		}
		require.Equal(t, http.StatusBadRequest, f.retry(message.ID).Code, "provider receipt must never authorize another physical send")
		require.Len(t, f.attempts(t), 1)
		require.EqualValues(t, 1, f.calls.Load())
	}
}

func TestOutboundHTTPCallbackStoreOutageReturns503(t *testing.T) {
	f := newOutboundHTTPFixture(t, "twilio", true)
	created := f.create(t, fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), "")
	require.Equal(t, http.StatusCreated, created.Code)
	_, err := f.service.DispatchOne(context.Background())
	require.NoError(t, err)
	attempts := f.attempts(t)
	require.Len(t, attempts, 1)
	a := attempts[0]
	pool, err := f.db.DB()
	require.NoError(t, err)
	require.NoError(t, pool.Close())
	values := url.Values{"AccountSid": {a.ProviderAccountID}, "MessageSid": {a.ProviderMessageID}, "From": {a.SenderID}, "To": {a.RecipientID}, "MessageStatus": {"delivered"}}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/91/twilio/status/%s", a.ID), strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", outboundHTTPSign(a.CallbackURL, outboundHTTPSecret, values))
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), outboundHTTPBody)
	require.NotContains(t, w.Body.String(), outboundHTTPSecret)
	require.NotContains(t, w.Body.String(), "database is closed")
	require.Equal(t, int64(1), f.calls.Load())
}

func TestOutboundHTTPWhatsAppSignedConfirmedStatus(t *testing.T) {
	f := newOutboundHTTPFixture(t, "whatsapp", true)
	created := f.create(t, fmt.Sprintf(`{"content":%q}`, outboundHTTPBody), "")
	require.Equal(t, http.StatusCreated, created.Code)
	message := outboundHTTPMessage(t, created)
	_, err := f.service.DispatchOne(context.Background())
	require.NoError(t, err)
	a := f.attempts(t)[0]
	raw := []byte(fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":%q,"changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":%q},"statuses":[{"id":%q,"recipient_id":%q,"status":"delivered","timestamp":"1780000000"}]}}]}]}`, a.ProviderAccountID, a.SenderID, a.ProviderMessageID, a.RecipientID))
	mac := hmac.New(sha256.New, []byte(f.cfg.WhatsAppAppSecret))
	_, _ = mac.Write(raw)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/91/whatsapp/callback", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var current domain.Message
	require.NoError(t, f.db.First(&current, message.ID).Error)
	require.Equal(t, domain.MessageStatusDelivered, current.Status)
}
