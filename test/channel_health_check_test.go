package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestChannelHealthCheckDynamicEngine(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "channel_health_check_secret_32b!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user and create test account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Health Admin",
		"email":        "health_admin@example.com",
		"password":     "Password123!",
		"account_name": "Channel Health Org",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(signUpBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin sign up failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// Helper to fetch health response
	getHealth := func(inboxID uint) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/health", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var res map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return w.Code, res
	}

	// =========================================================================
	// Case 1: Healthy standard WebWidget channel
	// =========================================================================
	t.Run("Case1_HealthyWebWidgetChannel", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "Web Support",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "web_tok_123456789",
			WebhookURL:   "https://api.example.com/webhook",
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		code, res := getHealth(inbox.ID)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if res["status"] != "healthy" {
			t.Fatalf("expected status healthy, got %v", res["status"])
		}
		if res["quality_rating"] != "GREEN" {
			t.Fatalf("expected quality_rating GREEN, got %v", res["quality_rating"])
		}
		if res["messaging_limit"] != "TIER_10K" {
			t.Fatalf("expected messaging_limit TIER_10K, got %v", res["messaging_limit"])
		}
		if res["verified"] != true {
			t.Fatalf("expected verified true, got %v", res["verified"])
		}
		checks, ok := res["checks"].(map[string]any)
		if !ok {
			t.Fatalf("expected checks map, got %v", res["checks"])
		}
		tokenCheck := checks["token"].(map[string]any)
		if tokenCheck["status"] != "valid" {
			t.Fatalf("expected token valid, got %v", tokenCheck["status"])
		}
	})

	// =========================================================================
	// Case 2: HMAC Mandatory enabled but missing HMACToken -> degraded/unhealthy
	// =========================================================================
	t.Run("Case2_HMACMandatoryMissingToken", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:     accountID,
			Name:          "Strict HMAC Web",
			ChannelType:   "Channel::WebWidget",
			WebsiteToken:  "valid_token_xyz_999",
			HMACMandatory: true,
			HMACToken:     "", // Missing HMAC token!
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		code, res := getHealth(inbox.ID)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if res["status"] == "healthy" {
			t.Fatalf("expected status to not be healthy, got %v", res["status"])
		}
		if res["verified"] == true {
			t.Fatalf("expected verified to be false when HMAC token missing, got %v", res["verified"])
		}
		issues, ok := res["issues"].([]any)
		if !ok || len(issues) == 0 {
			t.Fatalf("expected issues to contain HMAC warning, got %v", res["issues"])
		}
	})

	// =========================================================================
	// Case 3: WhatsApp channel missing ProviderConfig credentials -> unhealthy & RED
	// =========================================================================
	t.Run("Case3_WhatsAppMissingProviderConfig", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:      accountID,
			Name:           "WhatsApp Support",
			ChannelType:    "Channel::Whatsapp",
			WebsiteToken:   "wa_token_987654",
			ProviderConfig: "", // Unconfigured
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		code, res := getHealth(inbox.ID)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if res["status"] != "unhealthy" {
			t.Fatalf("expected unhealthy status for unconfigured WhatsApp, got %v", res["status"])
		}
		if res["quality_rating"] != "RED" {
			t.Fatalf("expected quality_rating RED, got %v", res["quality_rating"])
		}
		if res["verified"] != false {
			t.Fatalf("expected verified false, got %v", res["verified"])
		}
	})

	// =========================================================================
	// Case 4: High failure rate in recent 24h messages -> degradation
	// =========================================================================
	t.Run("Case4_DeliveryFailureRateDegradation", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "High Traffic Channel",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "hightraffic_tok_111",
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		// Create a conversation in this inbox
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    domain.ConversationStatusOpen,
		}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatalf("failed to create conversation: %v", err)
		}

		// Insert 6 failed messages in the last 24h
		now := time.Now().UTC()
		for i := 0; i < 6; i++ {
			msg := domain.Message{
				AccountID:      accountID,
				ConversationID: conv.ID,
				MessageType:    domain.MessageTypeOutgoing,
				Content:        fmt.Sprintf("Failed delivery attempt %d", i),
				Status:         "failed",
				CreatedAt:      now.Add(-10 * time.Minute),
			}
			if err := db.Create(&msg).Error; err != nil {
				t.Fatalf("failed to create message: %v", err)
			}
		}

		code, res := getHealth(inbox.ID)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if res["status"] != "unhealthy" {
			t.Fatalf("expected status unhealthy due to failures, got %v", res["status"])
		}
		checks := res["checks"].(map[string]any)
		deliveryCheck := checks["delivery"].(map[string]any)
		st := deliveryCheck["status"].(string)
		if st != "high_failure_rate" && st != "consecutive_failures" {
			t.Fatalf("expected high_failure_rate or consecutive_failures delivery status, got %v", st)
		}
		if fmt.Sprintf("%v", deliveryCheck["failed_messages"]) != "6" {
			t.Fatalf("expected failed_messages 6, got %v", deliveryCheck["failed_messages"])
		}
	})

	// =========================================================================
	// Case 5: WhatsApp credentials configured properly -> returns healthy
	// =========================================================================
	t.Run("Case5_WhatsAppProperlyConfigured", func(t *testing.T) {
		providerCfg, _ := json.Marshal(map[string]any{
			"phone_number_id": "10023456789",
			"access_token":    "EAAG_WHATSAPP_TEST_TOKEN_12345",
			"business_id":     "biz_123",
		})
		inbox := domain.Inbox{
			AccountID:      accountID,
			Name:           "WhatsApp Configured",
			ChannelType:    "Channel::Whatsapp",
			WebsiteToken:   "wa_token_perfect_1",
			WebhookURL:     "https://api.example.com/whatsapp/webhook",
			ProviderConfig: string(providerCfg),
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		code, res := getHealth(inbox.ID)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if res["status"] != "healthy" {
			t.Fatalf("expected status healthy, got %v (issues: %v)", res["status"], res["issues"])
		}
		if res["quality_rating"] != "GREEN" {
			t.Fatalf("expected quality_rating GREEN, got %v", res["quality_rating"])
		}
		if res["verified"] != true {
			t.Fatalf("expected verified true, got %v", res["verified"])
		}
	})

	// =========================================================================
	// Case 6: Consecutive message failures -> status unhealthy, RED, verified false
	// =========================================================================
	t.Run("Case6_ConsecutiveMessageFailuresUnhealthy", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "Consecutive Failures Channel",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "consecutive_tok_888",
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    domain.ConversationStatusOpen,
		}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatalf("failed to create conversation: %v", err)
		}

		now := time.Now().UTC()
		// 1 success earlier, followed by 3 consecutive failures
		db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Old message succeeded",
			Status:         "sent",
			CreatedAt:      now.Add(-20 * time.Minute),
		})
		for i := 0; i < 3; i++ {
			db.Create(&domain.Message{
				AccountID:      accountID,
				ConversationID: conv.ID,
				MessageType:    domain.MessageTypeOutgoing,
				Content:        fmt.Sprintf("Consecutive failed attempt %d", i),
				Status:         "failed",
				CreatedAt:      now.Add(-5 * time.Minute),
			})
		}

		code, res := getHealth(inbox.ID)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if res["status"] != "unhealthy" {
			t.Fatalf("expected status unhealthy due to 3 consecutive failures, got %v", res["status"])
		}
		if res["quality_rating"] != "RED" {
			t.Fatalf("expected quality_rating RED, got %v", res["quality_rating"])
		}
		if res["verified"] != false {
			t.Fatalf("expected verified false, got %v", res["verified"])
		}
		checks := res["checks"].(map[string]any)
		deliveryCheck := checks["delivery"].(map[string]any)
		if deliveryCheck["status"] != "consecutive_failures" {
			t.Fatalf("expected consecutive_failures status, got %v", deliveryCheck["status"])
		}
	})

	// =========================================================================
	// Case 7: Missing or invalid token -> status unhealthy, RED, UNVERIFIED
	// =========================================================================
	t.Run("Case7_MissingWebsiteTokenUnhealthy", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "Invalid Token Channel",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "", // Missing token!
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		code, res := getHealth(inbox.ID)
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d", code)
		}
		if res["status"] != "unhealthy" {
			t.Fatalf("expected status unhealthy for missing website_token, got %v", res["status"])
		}
		if res["quality_rating"] != "RED" {
			t.Fatalf("expected quality_rating RED, got %v", res["quality_rating"])
		}
		if res["messaging_limit"] != "UNVERIFIED" {
			t.Fatalf("expected messaging_limit UNVERIFIED, got %v", res["messaging_limit"])
		}
		if res["verified"] != false {
			t.Fatalf("expected verified false, got %v", res["verified"])
		}
	})
}
