package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestDashboardApps(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_dashboard_apps_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	r := router.SetupRouter(cfg, db, hub)

	doReq := func(method, path string, payload any, token string) *httptest.ResponseRecorder {
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		r.ServeHTTP(w, req)
		return w
	}

	// 1. Sign up Administrator for Account 1 (Acme Org)
	w1 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Acme Apps Org",
		"name":         "App Admin",
		"email":        "admin@acmeapps.test",
		"password":     "Password123!",
	}, "")
	if w1.Code != http.StatusCreated {
		t.Fatalf("failed to sign up account 1: %s", w1.Body.String())
	}
	var authResp1 struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &authResp1)
	tokenAdmin := authResp1.Data.Token
	accID := authResp1.Data.Accounts[0].ID

	// 2. Sign up Account 2 for tenant isolation test
	w2 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Beta Apps Org",
		"name":         "Beta Admin",
		"email":        "admin@betaapps.test",
		"password":     "Password123!",
	}, "")
	if w2.Code != http.StatusCreated {
		t.Fatalf("failed to sign up account 2: %s", w2.Body.String())
	}
	var authResp2 struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	tokenBeta := authResp2.Data.Token
	betaAccID := authResp2.Data.Accounts[0].ID

	// 3. Create regular agent in Account 1 without settings_manage permission
	agentUser := domain.User{
		Name:         "Support Agent",
		Email:        "agent@acmeapps.test",
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	db.Create(&agentUser)
	db.Create(&domain.AccountUser{AccountID: accID, UserID: agentUser.ID, Role: domain.RoleAgent})

	tokenAgent, err := auth.GenerateToken(&agentUser, cfg.JWTSecret, 24)
	if err != nil {
		t.Fatalf("failed to generate agent token: %v", err)
	}

	var app1ID uint

	// ==========================================
	// Test 1: Create Dashboard App (POST /dashboard_apps)
	// ==========================================
	t.Run("1_CreateDashboardApp", func(t *testing.T) {
		// A. Create with flat payload
		wCreate := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"title": "Stripe Billing Dashboard",
			"content": []map[string]any{
				{
					"type": "frame",
					"url":  "https://billing.stripe.com/p/login",
				},
			},
		}, tokenAdmin)

		if wCreate.Code != http.StatusOK && wCreate.Code != http.StatusCreated {
			t.Fatalf("failed to create dashboard app: status=%d, body=%s", wCreate.Code, wCreate.Body.String())
		}

		var createdApp struct {
			ID      uint   `json:"id"`
			Title   string `json:"title"`
			Content []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
				Link string `json:"link"`
			} `json:"content"`
		}
		_ = json.Unmarshal(wCreate.Body.Bytes(), &createdApp)
		if createdApp.ID == 0 {
			t.Fatalf("expected created app ID > 0, got 0")
		}
		if createdApp.Title != "Stripe Billing Dashboard" {
			t.Errorf("expected title 'Stripe Billing Dashboard', got %q", createdApp.Title)
		}
		if len(createdApp.Content) != 1 || createdApp.Content[0].URL != "https://billing.stripe.com/p/login" {
			t.Errorf("unexpected content: %+v", createdApp.Content)
		}
		if createdApp.Content[0].Link != "https://billing.stripe.com/p/login" {
			t.Errorf("expected link to match url, got %q", createdApp.Content[0].Link)
		}
		app1ID = createdApp.ID

		// B. Create with nested payload { "dashboard_app": { ... } } and HTTP url
		wNested := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"dashboard_app": map[string]any{
				"title": "Grafana Internal Metrics",
				"content": []map[string]any{
					{
						"type": "frame",
						"url":  "http://grafana.internal.net:3000/d/chatwoot",
					},
				},
			},
		}, tokenAdmin)

		if wNested.Code != http.StatusOK && wNested.Code != http.StatusCreated {
			t.Fatalf("failed to create nested dashboard app: status=%d, body=%s", wNested.Code, wNested.Body.String())
		}
	})

	// ==========================================
	// Test 2: List Dashboard Apps (GET /dashboard_apps)
	// ==========================================
	t.Run("2_ListDashboardApps", func(t *testing.T) {
		wList := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), nil, tokenAdmin)
		if wList.Code != http.StatusOK {
			t.Fatalf("expected 200 for list dashboard apps, got %d: %s", wList.Code, wList.Body.String())
		}

		var list []struct {
			ID      uint   `json:"id"`
			Title   string `json:"title"`
			Content []struct {
				Type string `json:"type"`
				URL  string `json:"url"`
			} `json:"content"`
		}
		_ = json.Unmarshal(wList.Body.Bytes(), &list)
		if len(list) != 2 {
			t.Fatalf("expected 2 dashboard apps, got %d", len(list))
		}
		if list[0].Title != "Stripe Billing Dashboard" {
			t.Errorf("unexpected first app title: %s", list[0].Title)
		}
	})

	// ==========================================
	// Test 3: Get Dashboard App by ID (GET /dashboard_apps/:id)
	// ==========================================
	t.Run("3_GetDashboardApp", func(t *testing.T) {
		wGet := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps/%d", accID, app1ID), nil, tokenAdmin)
		if wGet.Code != http.StatusOK {
			t.Fatalf("expected 200 for get dashboard app, got %d: %s", wGet.Code, wGet.Body.String())
		}
		var app struct {
			ID    uint   `json:"id"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal(wGet.Body.Bytes(), &app)
		if app.ID != app1ID || app.Title != "Stripe Billing Dashboard" {
			t.Errorf("unexpected get response: %+v", app)
		}

		// Non-existent ID returns 404
		wNotFound := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps/99999", accID), nil, tokenAdmin)
		if wNotFound.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent app, got %d", wNotFound.Code)
		}
	})

	// ==========================================
	// Test 4: Update Dashboard App (PUT/PATCH /dashboard_apps/:id)
	// ==========================================
	t.Run("4_UpdateDashboardApp", func(t *testing.T) {
		wUpdate := doReq(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps/%d", accID, app1ID), map[string]any{
			"title": "Stripe Customer Portal V2",
			"content": []map[string]any{
				{
					"type": "frame",
					"url":  "https://billing.stripe.com/v2/portal",
				},
			},
		}, tokenAdmin)

		if wUpdate.Code != http.StatusOK {
			t.Fatalf("expected 200 for update dashboard app, got %d: %s", wUpdate.Code, wUpdate.Body.String())
		}

		var updated struct {
			Title   string `json:"title"`
			Content []struct {
				URL string `json:"url"`
			} `json:"content"`
		}
		_ = json.Unmarshal(wUpdate.Body.Bytes(), &updated)
		if updated.Title != "Stripe Customer Portal V2" {
			t.Errorf("expected updated title, got %q", updated.Title)
		}
		if len(updated.Content) != 1 || updated.Content[0].URL != "https://billing.stripe.com/v2/portal" {
			t.Errorf("expected updated url, got %+v", updated.Content)
		}
	})

	// ==========================================
	// Test 5: Validation Enforcement (HTTP 422)
	// ==========================================
	t.Run("5_ContentValidation", func(t *testing.T) {
		// A. Missing title -> 422
		wNoTitle := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"content": []map[string]any{
				{"type": "frame", "url": "https://link.com"},
			},
		}, tokenAdmin)
		if wNoTitle.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for missing title, got %d", wNoTitle.Code)
		}

		// B. Empty content -> 422
		wNoContent := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"title":   "Invalid App",
			"content": []map[string]any{},
		}, tokenAdmin)
		if wNoContent.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for empty content, got %d", wNoContent.Code)
		}

		// C. Invalid URL (not http/https) -> 422
		wFtp := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"title": "FTP App",
			"content": []map[string]any{
				{"type": "frame", "url": "ftp://wontwork.chatwoot.com"},
			},
		}, tokenAdmin)
		if wFtp.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for ftp URL, got %d", wFtp.Code)
		}

		// D. Invalid URL syntax ("com") -> 422
		wBadUrl := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"title": "Bad URL App",
			"content": []map[string]any{
				{"type": "frame", "url": "com"},
			},
		}, tokenAdmin)
		if wBadUrl.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for malformed URL, got %d", wBadUrl.Code)
		}

		// E. Invalid type (not frame) -> 422
		wBadType := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"title": "Bad Type App",
			"content": []map[string]any{
				{"type": "dda", "url": "https://link.com"},
			},
		}, tokenAdmin)
		if wBadType.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for invalid type, got %d", wBadType.Code)
		}
	})

	// ==========================================
	// Test 6: Delete Dashboard App (DELETE /dashboard_apps/:id)
	// ==========================================
	t.Run("6_DeleteDashboardApp", func(t *testing.T) {
		wDelete := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps/%d", accID, app1ID), nil, tokenAdmin)
		if wDelete.Code != http.StatusNoContent && wDelete.Code != http.StatusOK {
			t.Fatalf("expected 204/200 for delete dashboard app, got %d: %s", wDelete.Code, wDelete.Body.String())
		}

		// Verify database
		var count int64
		db.Model(&domain.DashboardApp{}).Where("id = ?", app1ID).Count(&count)
		if count != 0 {
			t.Errorf("expected dashboard app to be deleted from DB, but still exists")
		}

		// Get after delete returns 404
		wGet := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps/%d", accID, app1ID), nil, tokenAdmin)
		if wGet.Code != http.StatusNotFound {
			t.Errorf("expected 404 after deletion, got %d", wGet.Code)
		}
	})

	// ==========================================
	// Test 7: Multi-Tenant Isolation
	// ==========================================
	t.Run("7_TenantIsolation", func(t *testing.T) {
		// Create app in Beta account
		wBeta := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", betaAccID), map[string]any{
			"title": "Beta Secret App",
			"content": []map[string]any{
				{"type": "frame", "url": "https://beta.internal.com"},
			},
		}, tokenBeta)
		if wBeta.Code != http.StatusOK && wBeta.Code != http.StatusCreated {
			t.Fatalf("failed to create app in beta account: %s", wBeta.Body.String())
		}

		var betaApp struct {
			ID uint `json:"id"`
		}
		_ = json.Unmarshal(wBeta.Body.Bytes(), &betaApp)

		// Account 1 cannot get Beta's app -> 404
		wCross := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps/%d", accID, betaApp.ID), nil, tokenAdmin)
		if wCross.Code != http.StatusNotFound {
			t.Errorf("tenant leak: account 1 accessed beta app, got %d", wCross.Code)
		}

		// Account 1 cannot delete Beta's app -> 404
		wCrossDel := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps/%d", accID, betaApp.ID), nil, tokenAdmin)
		if wCrossDel.Code != http.StatusNotFound {
			t.Errorf("tenant leak: account 1 deleted beta app, got %d", wCrossDel.Code)
		}
	})

	// ==========================================
	// Test 8: Role and Permission Enforcement
	// ==========================================
	t.Run("8_RoleAndPermissionEnforcement", func(t *testing.T) {
		// Regular agent cannot create -> 403
		wCreate := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), map[string]any{
			"title": "Unauthorized App",
			"content": []map[string]any{
				{"type": "frame", "url": "https://link.com"},
			},
		}, tokenAgent)
		if wCreate.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent create, got %d: %s", wCreate.Code, wCreate.Body.String())
		}

		// Regular agent can read list -> 200
		wList := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/dashboard_apps", accID), nil, tokenAgent)
		if wList.Code != http.StatusOK {
			t.Fatalf("expected 200 for agent list dashboard apps, got %d", wList.Code)
		}
	})
}
