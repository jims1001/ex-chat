package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestLabels_DetailAndLifecycle(t *testing.T) {
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

	// 1. Sign up Account A
	signUpA := map[string]string{
		"name":         "Label Admin A",
		"email":        "labeladminA@example.com",
		"password":     "Secret123!",
		"account_name": "Label Corp A",
	}
	bodyA, _ := json.Marshal(signUpA)
	reqA := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyA))
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	r.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusCreated {
		t.Fatalf("sign up A failed: code=%d body=%s", wA.Code, wA.Body.String())
	}

	var authRespA struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wA.Body.Bytes(), &authRespA)
	tokenA := authRespA.Data.Token
	accIDA := authRespA.Data.Accounts[0].ID
	accIDStrA := strconv.Itoa(int(accIDA))

	// 2. Sign up Account B
	signUpB := map[string]string{
		"name":         "Label Admin B",
		"email":        "labeladminB@example.com",
		"password":     "Secret123!",
		"account_name": "Label Corp B",
	}
	bodyB, _ := json.Marshal(signUpB)
	reqB := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyB))
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	r.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusCreated {
		t.Fatalf("sign up B failed: code=%d body=%s", wB.Code, wB.Body.String())
	}

	var authRespB struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wB.Body.Bytes(), &authRespB)
	tokenB := authRespB.Data.Token
	accIDB := authRespB.Data.Accounts[0].ID
	accIDStrB := strconv.Itoa(int(accIDB))

	// 3. Create Label in Account A with show_on_sidebar: true
	showSidebarTrue := true
	createPayload := map[string]any{
		"title":           "billing-issue",
		"description":     "Inquiries related to customer billing and invoices",
		"color":           "#FF5722",
		"show_on_sidebar": showSidebarTrue,
	}
	createBody, _ := json.Marshal(createPayload)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/labels", accIDStrA), bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create label failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var createResp struct {
		Success bool         `json:"success"`
		Data    domain.Label `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to decode create label response: %v", err)
	}
	labelID := createResp.Data.ID
	if labelID == 0 {
		t.Fatalf("expected positive label ID, got 0")
	}
	if !createResp.Data.ShowOnSidebar {
		t.Errorf("expected ShowOnSidebar true, got false")
	}

	// 4. Test GET /labels/:id (Show Label Detail)
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/labels/%d", accIDStrA, labelID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get label detail failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var detailResp struct {
		Success       bool         `json:"success"`
		ID            uint         `json:"id"`
		Title         string       `json:"title"`
		Description   string       `json:"description"`
		Color         string       `json:"color"`
		ShowOnSidebar bool         `json:"show_on_sidebar"`
		Payload       domain.Label `json:"payload"`
		Data          domain.Label `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detailResp); err != nil {
		t.Fatalf("failed to parse detail response: %v", err)
	}

	if !detailResp.Success {
		t.Errorf("expected success true")
	}
	if detailResp.ID != labelID || detailResp.Data.ID != labelID || detailResp.Payload.ID != labelID {
		t.Errorf("expected label ID %d, got root=%d, data=%d, payload=%d", labelID, detailResp.ID, detailResp.Data.ID, detailResp.Payload.ID)
	}
	if detailResp.Title != "billing-issue" || detailResp.Data.Title != "billing-issue" {
		t.Errorf("expected title billing-issue, got root=%s, data=%s", detailResp.Title, detailResp.Data.Title)
	}
	if detailResp.Description != "Inquiries related to customer billing and invoices" {
		t.Errorf("expected description matching, got %s", detailResp.Description)
	}
	if detailResp.Color != "#FF5722" {
		t.Errorf("expected color #FF5722, got %s", detailResp.Color)
	}
	if !detailResp.ShowOnSidebar || !detailResp.Data.ShowOnSidebar {
		t.Errorf("expected ShowOnSidebar true")
	}

	// 5. Test 404 on non-existent label
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/labels/99999", accIDStrA), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent label, got %d body=%s", w.Code, w.Body.String())
	}

	// 6. Test Multi-tenant isolation: Account B accessing Account A's label
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/labels/%d", accIDStrB, labelID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-tenant label access, got %d body=%s", w.Code, w.Body.String())
	}

	// 7. Update Label (set show_on_sidebar to false)
	showSidebarFalse := false
	updPayload := map[string]any{
		"title":           "billing-inquiry",
		"show_on_sidebar": showSidebarFalse,
	}
	updBody, _ := json.Marshal(updPayload)
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%s/labels/%d", accIDStrA, labelID), bytes.NewReader(updBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update label failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 8. Re-fetch label detail to confirm updated fields
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/labels/%d", accIDStrA, labelID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("re-fetch label detail failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var reDetailResp struct {
		Title         string `json:"title"`
		ShowOnSidebar bool   `json:"show_on_sidebar"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &reDetailResp)
	if reDetailResp.Title != "billing-inquiry" {
		t.Errorf("expected title billing-inquiry, got %s", reDetailResp.Title)
	}
	if reDetailResp.ShowOnSidebar {
		t.Errorf("expected ShowOnSidebar false after update, got true")
	}
}
