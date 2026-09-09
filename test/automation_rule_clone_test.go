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
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
)

func TestAutomationRuleClone(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "automation-rule-clone-test-jwt-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up Account A
	signUpPayload := map[string]string{
		"account_name": "Automation Rule Corp",
		"name":         "Rule Admin",
		"email":        "admin@rules-corp.com",
		"password":     "Password123!",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up account A failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var authRespA struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authRespA)
	tokenA := authRespA.Data.Token
	accountIDA := authRespA.Data.Accounts[0].ID

	// 2. Sign up Account B (for cross-tenant security test)
	signUpPayloadB := map[string]string{
		"account_name": "Other Tenant Corp",
		"name":         "Other Admin",
		"email":        "admin@other-tenant.com",
		"password":     "Password123!",
	}
	body, _ = json.Marshal(signUpPayloadB)
	req = httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up account B failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var authRespB struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authRespB)
	tokenB := authRespB.Data.Token
	accountIDB := authRespB.Data.Accounts[0].ID

	// 3. Create initial source automation rule in Account A
	createRulePayload := map[string]any{
		"name":        "Auto Assign VIP Conversations",
		"description": "Automatically assign VIP conversations to Tier 2 support team",
		"event_name":  domain.EventConversationCreated,
		"conditions": []map[string]any{
			{
				"attribute_key":   "priority",
				"filter_operator": "equal_to",
				"values":          []string{"urgent", "high"},
			},
		},
		"actions": []map[string]any{
			{
				"action_name":   domain.ActionAssignTeam,
				"action_params": []int{42},
			},
			{
				"action_name":   domain.ActionAddLabel,
				"action_params": []string{"vip-urgent"},
			},
		},
		"active": true,
	}
	body, _ = json.Marshal(createRulePayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules", accountIDA), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tokenA)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create automation rule failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var createRuleResp struct {
		Data domain.AutomationRule `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &createRuleResp)
	sourceRule := createRuleResp.Data
	sourceRuleID := sourceRule.ID

	// ----------------------------------------------------
	// Test Scenario 1: Default clone (empty body -> name appends " (Copy)", active defaults to false)
	// ----------------------------------------------------
	t.Run("Scenario 1: Default Clone Without Parameters", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules/%d/clone", accountIDA, sourceRuleID), bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for clone, got %d body=%s", w.Code, w.Body.String())
		}

		var cloneResp struct {
			Data domain.AutomationRule `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &cloneResp)
		cloned := cloneResp.Data

		if cloned.ID == 0 || cloned.ID == sourceRuleID {
			t.Fatalf("expected new unique rule ID, got %d (source: %d)", cloned.ID, sourceRuleID)
		}
		expectedName := fmt.Sprintf("%s (Copy)", sourceRule.Name)
		if cloned.Name != expectedName {
			t.Fatalf("expected cloned name '%s', got '%s'", expectedName, cloned.Name)
		}
		if cloned.Active != false {
			t.Fatalf("expected safe default active=false for cloned rule, got %v", cloned.Active)
		}
		if cloned.Description != sourceRule.Description {
			t.Fatalf("expected description to match source rule, got '%s'", cloned.Description)
		}
		if cloned.EventName != sourceRule.EventName {
			t.Fatalf("expected event_name to match source rule, got '%s'", cloned.EventName)
		}
		if cloned.Conditions != sourceRule.Conditions {
			t.Fatalf("expected conditions to match source rule, got '%s'", cloned.Conditions)
		}
		if cloned.Actions != sourceRule.Actions {
			t.Fatalf("expected actions to match source rule, got '%s'", cloned.Actions)
		}

		// Verify database persistence
		var dbCloned domain.AutomationRule
		if err := db.Where("account_id = ? AND id = ?", accountIDA, cloned.ID).First(&dbCloned).Error; err != nil {
			t.Fatalf("failed to query cloned rule from db: %v", err)
		}
		if dbCloned.Name != expectedName {
			t.Fatalf("db rule name mismatch: %s", dbCloned.Name)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 2: Clone with custom name and explicit active=true
	// ----------------------------------------------------
	t.Run("Scenario 2: Clone With Custom Name And Active Status", func(t *testing.T) {
		customPayload := map[string]any{
			"name":   "Tier 2 VIP Workflow - Production Fork",
			"active": true,
		}
		body, _ := json.Marshal(customPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules/%d/clone", accountIDA, sourceRuleID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for custom clone, got %d body=%s", w.Code, w.Body.String())
		}

		var cloneResp struct {
			Data domain.AutomationRule `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &cloneResp)
		cloned := cloneResp.Data

		if cloned.Name != "Tier 2 VIP Workflow - Production Fork" {
			t.Fatalf("expected custom name 'Tier 2 VIP Workflow - Production Fork', got '%s'", cloned.Name)
		}
		if cloned.Active != true {
			t.Fatalf("expected explicit active=true, got %v", cloned.Active)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 3: Alias route /duplicate
	// ----------------------------------------------------
	t.Run("Scenario 3: Duplicate Alias Route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules/%d/duplicate", accountIDA, sourceRuleID), bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for /duplicate route, got %d body=%s", w.Code, w.Body.String())
		}

		var cloneResp struct {
			Data domain.AutomationRule `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &cloneResp)
		if cloneResp.Data.ID == 0 || cloneResp.Data.ID == sourceRuleID {
			t.Fatalf("expected new rule created via /duplicate, got ID %d", cloneResp.Data.ID)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 4: Non-existent rule ID -> 404 Not Found
	// ----------------------------------------------------
	t.Run("Scenario 4: Non-existent Rule ID Returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules/999999/clone", accountIDA), bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found, got %d body=%s", w.Code, w.Body.String())
		}
	})

	// ----------------------------------------------------
	// Test Scenario 5: Cross-tenant isolation -> Account B cannot clone Account A's rule
	// ----------------------------------------------------
	t.Run("Scenario 5: Cross-tenant Isolation Prevents Unauthorized Clone", func(t *testing.T) {
		// Token B attempts to clone Account A's rule via Account B endpoint
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules/%d/clone", accountIDB, sourceRuleID), bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer "+tokenB)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found for cross-tenant rule clone, got %d body=%s", w.Code, w.Body.String())
		}
	})
}
