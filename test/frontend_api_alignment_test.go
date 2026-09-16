package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestFrontendAPIAlignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "frontend-alignment-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 0: Sign up admin & account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "测试管理员",
		"email":        "admin.alignment@test.example",
		"password":     "password123456",
		"account_name": "测试工作区",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)
	if wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: %d, body: %s", wSignUp.Code, wSignUp.Body.String())
	}

	var authResp struct {
		Data struct {
			Token string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	authHeader := "Bearer " + token

	// 1. Verify all ReportDetailView backend endpoints
	reportEndpoints := []string{
		fmt.Sprintf("/api/v1/accounts/%d/reports/live_conversation_metrics", accountID),
		fmt.Sprintf("/api/v2/accounts/%d/reports/live_conversation_metrics", accountID),
		fmt.Sprintf("/api/v1/accounts/%d/reports/summary", accountID),
		fmt.Sprintf("/api/v2/accounts/%d/reports/summary", accountID),
		fmt.Sprintf("/api/v1/accounts/%d/reports/agents", accountID),
		fmt.Sprintf("/api/v2/accounts/%d/reports/agents", accountID),
		fmt.Sprintf("/api/v1/accounts/%d/reports/channel_summary", accountID),
		fmt.Sprintf("/api/v2/accounts/%d/reports/channel_summary", accountID),
		fmt.Sprintf("/api/v1/accounts/%d/reports/bot_summary", accountID),
		fmt.Sprintf("/api/v2/accounts/%d/reports/bot_summary", accountID),
		fmt.Sprintf("/api/v1/accounts/%d/reports/applied_slas/metrics", accountID),
		fmt.Sprintf("/api/v2/accounts/%d/reports/applied_slas/metrics", accountID),
		fmt.Sprintf("/api/v1/accounts/%d/reports/csat", accountID),
		fmt.Sprintf("/api/v2/accounts/%d/reports/csat", accountID),
		fmt.Sprintf("/api/v1/accounts/%d/reports/year_in_review", accountID),
	}

	for _, ep := range reportEndpoints {
		t.Run("ReportEndpoint_"+ep, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", ep, nil)
			req.Header.Set("Authorization", authHeader)
			engine.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 for %s, got %d, body: %s", ep, w.Code, w.Body.String())
			}
		})
	}

	// 2. Verify ModuleWorkspace overview endpoints
	t.Run("Overview_Contacts", func(t *testing.T) {
		// GET contacts
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/contacts", accountID), nil)
		req.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for GET contacts, got %d", w.Code)
		}

		// POST contact
		body, _ := json.Marshal(map[string]any{"name": "张三", "email": "zhangsan@test.com"})
		wPost := httptest.NewRecorder()
		reqPost, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/contacts", accountID), bytes.NewBuffer(body))
		reqPost.Header.Set("Authorization", authHeader)
		reqPost.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPost, reqPost)
		if wPost.Code != http.StatusOK && wPost.Code != http.StatusCreated {
			t.Fatalf("expected 200/201 for POST contact, got %d", wPost.Code)
		}
	})

	t.Run("Overview_AuditLogs", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/audit_logs", accountID), nil)
		req.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for audit_logs, got %d", w.Code)
		}
	})

	t.Run("Overview_Assistants", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/captain/assistants", accountID), nil)
		req.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for captain assistants, got %d", w.Code)
		}
	})

	t.Run("No_Fake_Success_Verification", func(t *testing.T) {
		// Bulk action with invalid/empty action should return 400 bad request, not 200
		body, _ := json.Marshal(map[string]any{})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", authHeader)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Fatalf("expected error code for invalid bulk_actions, got 200 (fake success)")
		}
	})
}
