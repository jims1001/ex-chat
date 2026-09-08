package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestHelpCenter_And_Webhooks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_key_1234567890123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]string{
		"name":         "Portal Admin",
		"email":        "portaladmin@example.com",
		"password":     "Secret123!",
		"account_name": "Help Center Org",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Success bool `json:"success"`
		Data    struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &authResp); err != nil {
		t.Fatalf("failed to parse sign up response: %v", err)
	}
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	accIDStr := strconv.Itoa(int(accountID))

	// 2. Create Help Center Portal (HELP-01 ~ 03)
	portalPayload := map[string]string{
		"name": "Docs & Guides",
		"slug": "docs-guides",
	}
	body, _ = json.Marshal(portalPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/portals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create portal failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var portalResp struct {
		Data domain.Portal `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &portalResp)
	portalID := portalResp.Data.ID

	// 3. Create Category (HELP-04)
	catPayload := map[string]string{
		"name": "Getting Started",
		"slug": "getting-started",
	}
	body, _ = json.Marshal(catPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/portals/"+strconv.Itoa(int(portalID))+"/categories", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create category failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var catResp struct {
		Data domain.Category `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &catResp)
	catID := catResp.Data.ID

	// 4. Create Article (HELP-05 ~ 08)
	artPayload := map[string]any{
		"category_id": catID,
		"title":       "Welcome to ex-chat Customer Support",
		"slug":        "welcome-guide",
		"content":     "Full guide to getting started with ex-chat channels and conversations.",
		"status":      "published",
	}
	body, _ = json.Marshal(artPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/portals/"+strconv.Itoa(int(portalID))+"/articles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create article failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 5. Test Public Portal access without authentication (HELP-10)
	req = httptest.NewRequest(http.MethodGet, "/public/api/v1/portals/docs-guides/articles", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get public portal articles failed: code=%d", w.Code)
	}

	// 6. Test Webhooks (EXT-01 ~ 05)
	webhookPayload := map[string]any{
		"url":           "https://external-service.internal/webhook",
		"subscriptions": []string{"conversation_created", "message_created"},
	}
	body, _ = json.Marshal(webhookPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/webhooks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create webhook failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var whResp struct {
		Data domain.Webhook `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &whResp)
	assertID := whResp.Data.ID
	if assertID == 0 {
		t.Fatalf("expected webhook ID > 0")
	}

	// 7. Test HMAC Signing in Webhook Service
	webhookRepo := repository.NewWebhookRepository(db)
	webhookService := service.NewWebhookService(db, webhookRepo)
	sig := webhookService.SignPayload([]byte(`{"event":"conversation_created"}`), "test_secret_key")
	if sig == "" {
		t.Fatalf("expected valid HMAC-SHA256 signature string")
	}
}
