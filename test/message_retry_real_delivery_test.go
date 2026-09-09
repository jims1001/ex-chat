package test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

type mockWebhookTransport struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockWebhookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestMessageRetryActualDelivery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_message_retry_actual_delivery",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	var webhookHitCount int
	var lastWebhookEvent string
	mockClient := &http.Client{
		Transport: &mockWebhookTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				webhookHitCount++
				lastWebhookEvent = req.Header.Get("X-Chatwoot-Event")
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"status":"ack","code":200}`)),
					Header:     make(http.Header),
				}, nil
			},
		},
	}
	router.SetWebhookHTTPClient(mockClient)

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Seed Account & User
	account := domain.Account{Name: "Retry Actual Delivery Account"}
	db.Create(&account)

	user := domain.User{
		Email:        "retry_agent@example.com",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuvwxyz123456",
		Name:         "Retry Agent",
		Role:         domain.RoleAgent,
	}
	db.Create(&user)

	db.Create(&domain.AccountUser{
		AccountID:    account.ID,
		UserID:       user.ID,
		Role:         domain.RoleAgent,
		Availability: "online",
	})

	inbox := domain.Inbox{
		AccountID:    account.ID,
		Name:         "Retry Test Inbox",
		ChannelType:  domain.ChannelWebWidget,
		WebsiteToken: "tok_retry_actual_delivery",
	}
	db.Create(&inbox)

	contact := domain.Contact{
		AccountID: account.ID,
		Name:      "Retry Visitor",
		Email:     "visitor@example.com",
	}
	db.Create(&contact)

	pastTime := time.Now().UTC().Add(-2 * time.Hour)
	conv := domain.Conversation{
		AccountID:      account.ID,
		InboxID:        inbox.ID,
		ContactID:      contact.ID,
		Status:         domain.ConversationStatusOpen,
		AssigneeID:     &user.ID,
		LastActivityAt: pastTime,
	}
	db.Create(&conv)

	webhook := domain.Webhook{
		AccountID:     account.ID,
		URL:           "https://api.external-crm.com/webhooks/listener",
		Subscriptions: `["message_created","message_updated"]`,
	}
	db.Create(&webhook)

	token, _ := auth.GenerateToken(&user, cfg.JWTSecret, 24)

	t.Run("Message Retry Actually Bumps Activity and Executes Real Delivery", func(t *testing.T) {
		initialHits := webhookHitCount

		// Create failed message
		failedMsg := domain.Message{
			AccountID:      account.ID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       user.ID,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        "Actual retransmission message",
			Status:         domain.MessageStatusFailed,
		}
		db.Create(&failedMsg)

		retryURL := fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/retry", account.ID, conv.ID, failedMsg.ID)
		req := httptest.NewRequest(http.MethodPost, retryURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on retry, got %d body=%s", w.Code, w.Body.String())
		}

		// 1. Verify Message Status in DB is updated to 'sent'
		var refreshedMsg domain.Message
		db.First(&refreshedMsg, failedMsg.ID)
		if refreshedMsg.Status != domain.MessageStatusSent {
			t.Fatalf("expected status 'sent', got '%s'", refreshedMsg.Status)
		}

		// 2. Verify Conversation Activity was bumped (not just status updated)
		var refreshedConv domain.Conversation
		db.First(&refreshedConv, conv.ID)
		if !refreshedConv.LastActivityAt.After(pastTime) {
			t.Fatalf("expected conversation LastActivityAt to be bumped after %v, got %v", pastTime, refreshedConv.LastActivityAt)
		}

		// 3. Verify Webhook Delivery record was dispatched and executed via HTTP client
		time.Sleep(150 * time.Millisecond)
		var del domain.WebhookDelivery
		err := db.Where("account_id = ? AND event = ?", account.ID, "message_created").Order("id DESC").First(&del).Error
		if err != nil {
			t.Fatalf("expected webhook delivery record to be created: %v", err)
		}
		if del.URL != webhook.URL {
			t.Fatalf("expected delivery url to be %s, got %s", webhook.URL, del.URL)
		}
		if webhookHitCount <= initialHits {
			t.Fatalf("expected webhook HTTP dispatch to execute, initial=%d now=%d", initialHits, webhookHitCount)
		}
		if lastWebhookEvent != "message_created" {
			t.Fatalf("expected webhook event header 'message_created', got '%s'", lastWebhookEvent)
		}
	})

	t.Run("Message Retry Re-opens Resolved Conversation for Inbound Message", func(t *testing.T) {
		db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Update("status", domain.ConversationStatusResolved)

		inboundFailedMsg := domain.Message{
			AccountID:      account.ID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			SenderID:       contact.ID,
			MessageType:    domain.MessageTypeIncoming,
			ContentType:    domain.ContentTypeText,
			Content:        "Visitor retry message",
			Status:         domain.MessageStatusFailed,
		}
		db.Create(&inboundFailedMsg)

		retryURL := fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/retry", account.ID, conv.ID, inboundFailedMsg.ID)
		req := httptest.NewRequest(http.MethodPost, retryURL, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on retry inbound, got %d body=%s", w.Code, w.Body.String())
		}

		var reopenedConv domain.Conversation
		db.First(&reopenedConv, conv.ID)
		if reopenedConv.Status != domain.ConversationStatusOpen {
			t.Fatalf("expected conversation to re-open to 'open', got '%s'", reopenedConv.Status)
		}
	})

	t.Run("Webhook Delivery Real HTTP Retransmission", func(t *testing.T) {
		initialHits := webhookHitCount

		failedDelivery := domain.WebhookDelivery{
			AccountID:    account.ID,
			WebhookID:    webhook.ID,
			Event:        "message_created",
			URL:          "https://api.external-crm.com/webhooks/listener",
			Payload:      `{"event":"message_created","id":999}`,
			Attempts:     1,
			Status:       "failed",
			ResponseBody: "Connection refused previously",
		}
		db.Create(&failedDelivery)

		// Call webhook delivery retry endpoint
		retryReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/webhooks/deliveries/%d/retry", account.ID, failedDelivery.ID), nil)
		retryReq.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, retryReq)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on webhook delivery retry, got %d body=%s", w.Code, w.Body.String())
		}

		if webhookHitCount != initialHits+1 {
			t.Fatalf("expected HTTP client to execute retransmission request, before=%d, after=%d", initialHits, webhookHitCount)
		}

		var refreshedDelivery domain.WebhookDelivery
		db.First(&refreshedDelivery, failedDelivery.ID)
		if refreshedDelivery.Status != "delivered" {
			t.Fatalf("expected status to be 'delivered', got '%s'", refreshedDelivery.Status)
		}
		if refreshedDelivery.Attempts != 2 {
			t.Fatalf("expected attempts to be 2, got %d", refreshedDelivery.Attempts)
		}
		if refreshedDelivery.ResponseCode != 200 {
			t.Fatalf("expected response code 200, got %d", refreshedDelivery.ResponseCode)
		}
	})
}
