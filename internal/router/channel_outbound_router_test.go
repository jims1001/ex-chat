package router_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const outboundRouterSecret = "router-fixture-token-must-remain-private"
const outboundRouterProviderID = "SM33333333333333333333333333333333"

type outboundRouterTransport struct {
	sends, lookups atomic.Int64
	lookupStatus   string
}

func (p *outboundRouterTransport) SendText(context.Context, channel.TextRequest) channel.Result {
	p.sends.Add(1)
	return channel.Result{ProviderID: outboundRouterProviderID, Status: domain.MessageStatusProviderAccepted, Disposition: channel.DispositionAccepted}
}
func (p *outboundRouterTransport) LookupText(_ context.Context, _ channel.TextRequest, providerID string) channel.Result {
	p.lookups.Add(1)
	return channel.Result{ProviderID: providerID, Status: p.lookupStatus, Disposition: channel.DispositionAccepted}
}

func outboundRouterSignature(publicURL string, params url.Values) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var value strings.Builder
	value.WriteString(publicURL)
	for _, key := range keys {
		value.WriteString(key)
		value.WriteString(params.Get(key))
	}
	mac := hmac.New(sha1.New, []byte(outboundRouterSecret))
	_, _ = mac.Write([]byte(value.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestOutboundRouterSeparatesSignedProviderCallbacksFromScopedAgentActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "outbound-router.db"), JWTSecret: "outbound-router-test-key", JWTExpirationHours: 24, AutomationExecutionMode: "durable"}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	accounts := repository.NewAccountRepository(db)
	users := repository.NewUserRepository(db)
	account := domain.Account{Name: "Outbound tenant"}
	otherAccount := domain.Account{Name: "Other outbound tenant"}
	require.NoError(t, accounts.Create(&account))
	require.NoError(t, accounts.Create(&otherAccount))
	agent := domain.User{Name: "Outbound agent", Email: "outbound-agent@example.test", Role: domain.RoleAgent}
	outsider := domain.User{Name: "Other tenant agent", Email: "other-outbound-agent@example.test", Role: domain.RoleAgent}
	require.NoError(t, users.Create(&agent))
	require.NoError(t, users.Create(&outsider))
	require.NoError(t, accounts.AddMember(account.ID, agent.ID, domain.RoleAgent))
	require.NoError(t, accounts.AddMember(otherAccount.ID, outsider.ID, domain.RoleAgent))
	token, err := auth.GenerateToken(&agent, cfg.JWTSecret, 24)
	require.NoError(t, err)
	outsiderToken, err := auth.GenerateToken(&outsider, cfg.JWTSecret, 24)
	require.NoError(t, err)
	inbox := domain.Inbox{AccountID: account.ID, Name: "Outbound router inbox", ChannelType: "Channel::TwilioSms"}
	require.NoError(t, db.Create(&inbox).Error)
	contact := domain.Contact{AccountID: account.ID, Name: "Bound recipient"}
	require.NoError(t, db.Create(&contact).Error)
	require.NoError(t, db.Create(&domain.ContactInbox{InboxID: inbox.ID, ContactID: contact.ID, SourceID: "+14155550101"}).Error)
	conv := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
	otherConv := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
	require.NoError(t, db.Create(&conv).Error)
	require.NoError(t, db.Create(&otherConv).Error)
	cc := channel.Config{OutboundEnabled: true, PublicBaseURL: "https://canonical.example.test", EncryptionKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32))}
	connection := domain.ChannelConnection{AccountID: account.ID, InboxID: inbox.ID, Provider: "twilio", ProviderAccountID: "AC33333333333333333333333333333333", RecipientID: "+14155550100", RecipientKey: channel.StableKey("twilio", "+14155550100")}
	key, err := cc.Key()
	require.NoError(t, err)
	connection.SecretCiphertext, err = channel.Seal(key, channel.StableKey(strconv.FormatUint(uint64(account.ID), 10), connection.Provider, connection.ProviderAccountID, connection.RecipientID), outboundRouterSecret)
	require.NoError(t, err)
	require.NoError(t, db.Create(&connection).Error)
	transport := &outboundRouterTransport{lookupStatus: domain.MessageStatusDelivered}
	r := router.SetupRouterWithOptions(cfg, db, ws.NewHub(), router.Options{ChannelConfig: &cc, ChannelTextTransport: transport})
	request := func(path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"content":"router text that should only reach its provider"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	createPath := fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", account.ID, conv.ID)
	for _, suffix := range []string{"", "/123/retry", "/123/reconcile"} {
		w := request(createPath+suffix, "")
		require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		w = request(createPath+suffix, outsiderToken)
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	}
	var count int64
	require.NoError(t, db.Model(&domain.Message{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&domain.OutboundMessageAttempt{}).Count(&count).Error)
	require.Zero(t, count)
	require.Zero(t, transport.sends.Load())
	require.Zero(t, transport.lookups.Load())
	created := request(createPath, token)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		Data domain.Message `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	message := response.Data
	require.NotZero(t, message.ID)
	require.Equal(t, domain.MessageStatusQueued, message.Status)
	require.Zero(t, transport.sends.Load())
	require.NotContains(t, created.Body.String(), outboundRouterSecret)
	require.NotContains(t, created.Body.String(), connection.SecretCiphertext)
	queuedReconcile := request(fmt.Sprintf("%s/%d/reconcile", createPath, message.ID), token)
	require.Equal(t, http.StatusUnprocessableEntity, queuedReconcile.Code, queuedReconcile.Body.String())
	require.Zero(t, transport.lookups.Load(), "a queued intent has no provider receipt to look up")
	require.Zero(t, transport.sends.Load(), "reconcile cannot dispatch the queued intent")
	conversations := repository.NewConversationRepository(db)
	conversations.SetAutomationCapture(true)
	messages := repository.NewMessageRepositoryWithConversationPort(db, conversations)
	messages.SetAutomationCapture(true)
	outbound := service.NewChannelOutboundService(service.NewChannelTextOwner(cc, transport, repository.NewInboxRepository(db)), conversations, messages)
	worked, err := outbound.DispatchOne(context.Background())
	require.NoError(t, err)
	require.True(t, worked)
	require.EqualValues(t, 1, transport.sends.Load())
	var attempt domain.OutboundMessageAttempt
	require.NoError(t, db.Where("message_id = ?", message.ID).First(&attempt).Error)
	require.NotContains(t, created.Body.String(), attempt.ID)
	reconcilePath := fmt.Sprintf("%s/%d/reconcile", createPath, message.ID)
	for _, suffix := range []string{"retry", "reconcile"} {
		path := fmt.Sprintf("%s/%d/%s", createPath, message.ID, suffix)
		require.Equal(t, http.StatusUnauthorized, request(path, "").Code)
		require.Equal(t, http.StatusForbidden, request(path, outsiderToken).Code)
	}
	// A valid member of another tenant cannot reach this row by changing route IDs.
	crossTenant := fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/reconcile", otherAccount.ID, conv.ID, message.ID)
	w := request(crossTenant, outsiderToken)
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), outboundRouterSecret)
	wrongConversation := fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/reconcile", account.ID, otherConv.ID, message.ID)
	w = request(wrongConversation, token)
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	require.Zero(t, transport.lookups.Load())
	require.EqualValues(t, 1, transport.sends.Load())
	reconciled := request(reconcilePath, token)
	require.Equal(t, http.StatusOK, reconciled.Code, reconciled.Body.String())
	require.NoError(t, json.Unmarshal(reconciled.Body.Bytes(), &response))
	require.Equal(t, message.ID, response.Data.ID)
	require.Equal(t, domain.MessageStatusDelivered, response.Data.Status)
	require.EqualValues(t, 1, transport.lookups.Load())
	require.EqualValues(t, 1, transport.sends.Load())
	require.NotContains(t, reconciled.Body.String(), outboundRouterSecret)
	require.NotContains(t, reconciled.Body.String(), attempt.ID)
	// Provider callback authentication is the Twilio signature, not a session JWT.
	values := url.Values{"AccountSid": {attempt.ProviderAccountID}, "MessageSid": {attempt.ProviderMessageID}, "From": {attempt.SenderID}, "To": {attempt.RecipientID}, "MessageStatus": {"delivered"}}
	callbackPath := fmt.Sprintf("/api/v1/accounts/%d/twilio/status/%s", account.ID, attempt.ID)
	for _, authorization := range []string{"", "Bearer deliberately-invalid-session"} {
		req := httptest.NewRequest(http.MethodPost, callbackPath, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Twilio-Signature", outboundRouterSignature(attempt.CallbackURL, values))
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	}
	// An authenticated agent session cannot replace the required provider proof.
	req := httptest.NewRequest(http.MethodPost, callbackPath, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), outboundRouterSecret)
	require.NoError(t, db.Model(&domain.OutboundMessageAttempt{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Model(&domain.Message{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.EqualValues(t, 1, transport.sends.Load())
	require.EqualValues(t, 1, transport.lookups.Load())
}
