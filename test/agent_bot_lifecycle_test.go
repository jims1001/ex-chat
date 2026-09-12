package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAgentBot_FullLifecycleAndInboxBinding(t *testing.T) {
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

	// 1. Sign up Account A Admin
	signUpA := map[string]string{
		"name":         "Bot Admin A",
		"email":        "botadminA@example.com",
		"password":     "Secret123!",
		"account_name": "Bot Corp A",
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

	// 2. Sign up Account B Admin
	signUpB := map[string]string{
		"name":         "Bot Admin B",
		"email":        "botadminB@example.com",
		"password":     "Secret123!",
		"account_name": "Bot Corp B",
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

	// 3. Add a plain Agent user in Account A
	agentUser := domain.User{
		Name:         "Plain Agent",
		Email:        "plainagent@example.com",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuvwxyzABCDEF",
		Role:         domain.RoleAgent,
	}
	db.Create(&agentUser)
	db.Create(&domain.AccountUser{
		AccountID: accIDA,
		UserID:    agentUser.ID,
		Role:      domain.RoleAgent,
	})

	// Login as plain agent
	loginReqBody, _ := json.Marshal(map[string]string{
		"email":    "plainagent@example.com",
		"password": "Password123!",
	})
	reqLogin := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginReqBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	wLogin := httptest.NewRecorder()
	r.ServeHTTP(wLogin, reqLogin)
	agentToken, err := auth.GenerateToken(&agentUser, cfg.JWTSecret, 24)
	if err != nil {
		t.Fatalf("failed to generate agent token: %v", err)
	}

	// 4. Create an Inbox in Account A for binding test
	inbox := domain.Inbox{
		AccountID:   accIDA,
		Name:        "Customer Support Web",
		ChannelType: "Channel::WebWidget",
	}
	db.Create(&inbox)

	// 5. Test Create Agent Bot (POST /agent_bots)
	createPayload := map[string]any{
		"name":         "FAQ Auto Responder",
		"description":  "Answers standard customer queries via webhook",
		"outgoing_url": "https://faq-service.internal/events",
		"bot_type":     "webhook",
		"avatar_url":   "https://assets.internal/bots/faq.png",
		"bot_config":   `{"confidence_threshold": 0.85}`,
	}
	cBody, _ := json.Marshal(createPayload)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/agent_bots", accIDStrA), bytes.NewReader(cBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create agent bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var createResp struct {
		Success     bool   `json:"success"`
		ID          uint   `json:"id"`
		Name        string `json:"name"`
		OutgoingURL string `json:"outgoing_url"`
		AvatarURL   string `json:"avatar_url"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("failed to decode create response: %v", err)
	}
	botID := createResp.ID
	if botID == 0 {
		t.Fatalf("expected positive bot ID, got 0")
	}
	if createResp.AccessToken == "" {
		t.Errorf("expected access_token to be generated")
	}
	if createResp.AvatarURL != "https://assets.internal/bots/faq.png" {
		t.Errorf("expected avatar_url to be saved, got %s", createResp.AvatarURL)
	}

	// 6. Test Get Agent Bot Details (GET /agent_bots/:id)
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get agent bot detail failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var detailResp struct {
		Success     bool           `json:"success"`
		ID          uint           `json:"id"`
		Name        string         `json:"name"`
		OutgoingURL string         `json:"outgoing_url"`
		AvatarURL   string         `json:"avatar_url"`
		Inboxes     []domain.Inbox `json:"inboxes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detailResp); err != nil {
		t.Fatalf("failed to parse detail response: %v", err)
	}
	if detailResp.Name != "FAQ Auto Responder" || detailResp.ID != botID {
		t.Errorf("unexpected detail name=%s id=%d", detailResp.Name, detailResp.ID)
	}
	if len(detailResp.Inboxes) != 0 {
		t.Errorf("expected 0 attached inboxes initially, got %d", len(detailResp.Inboxes))
	}

	// 7. Test Update Agent Bot (PUT & PATCH /agent_bots/:id)
	updPayload := map[string]any{
		"name":        "FAQ & Routing Assistant",
		"description": "Updated description for routing",
	}
	uBody, _ := json.Marshal(updPayload)
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrA, botID), bytes.NewReader(uBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update agent bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var updResp struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updResp)
	if updResp.Name != "FAQ & Routing Assistant" {
		t.Errorf("expected updated name, got %s", updResp.Name)
	}

	// 8. Test Delete Avatar (DELETE /agent_bots/:id/avatar)
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d/avatar", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete avatar failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Verify avatar is cleared
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var verifyAvatarResp struct {
		AvatarURL string `json:"avatar_url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &verifyAvatarResp)
	if verifyAvatarResp.AvatarURL != "" {
		t.Errorf("expected avatar_url to be empty after delete, got %s", verifyAvatarResp.AvatarURL)
	}

	// 9. Multi-tenant isolation: Account B accessing Account A's Bot
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrB, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-tenant get, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrB, botID), bytes.NewReader(uBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenB)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-tenant put, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrB, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenB)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for cross-tenant delete, got %d", w.Code)
	}

	// 10. Test Agent Bot and Inbox Binding from Bot endpoint
	// POST /agent_bots/:id/inbox
	bindBody, _ := json.Marshal(map[string]any{"inbox_id": inbox.ID})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d/inbox", accIDStrA, botID), bytes.NewReader(bindBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bind inbox to agent bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// GET /agent_bots/:id/inboxes
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d/inboxes", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get bot inboxes failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var inboxesResp struct {
		Data []domain.Inbox `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inboxesResp)
	if len(inboxesResp.Data) != 1 || inboxesResp.Data[0].ID != inbox.ID {
		t.Fatalf("expected 1 attached inbox with id %d, got %v", inbox.ID, inboxesResp.Data)
	}

	// Check from Inbox side: GET /inboxes/:id/agent_bot
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/inboxes/%d/agent_bot", accIDStrA, inbox.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get inbox agent bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Disconnect from Bot side: DELETE /agent_bots/:id/inbox/:inbox_id
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d/inbox/%d", accIDStrA, botID, inbox.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disconnect inbox from agent bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Verify disconnected: GET /agent_bots/:id/inboxes
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d/inboxes", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	_ = json.Unmarshal(w.Body.Bytes(), &inboxesResp)
	if len(inboxesResp.Data) != 0 {
		t.Errorf("expected 0 inboxes after disconnect, got %d", len(inboxesResp.Data))
	}

	// 11. Test Chatwoot standard RESTful binding from Inbox side: POST /inboxes/:id/agent_bot
	inboxSetBody, _ := json.Marshal(map[string]any{"agent_bot": botID})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/inboxes/%d/agent_bot", accIDStrA, inbox.ID), bytes.NewReader(inboxSetBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set agent bot via POST /inboxes/:id/agent_bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Verify binding exists again
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/inboxes/%d/agent_bot", accIDStrA, inbox.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get inbox agent bot failed after set: code=%d", w.Code)
	}

	// 12. Test RBAC permissions: Plain Agent should be rejected from modifying or deleting bots
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/agent_bots", accIDStrA), bytes.NewReader(cBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agentToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for agent creating bot, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrA, botID), bytes.NewReader(uBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agentToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for agent updating bot, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+agentToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for agent deleting bot, got %d", w.Code)
	}

	// 13. Cascade deletion: Delete Agent Bot and verify bindings are cleanly removed
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete agent bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Verify bot is 404
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/agent_bots/%d", accIDStrA, botID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for deleted bot, got %d", w.Code)
	}

	// Verify inbox has no agent bot and no DB corruption
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/inboxes/%d/agent_bot", accIDStrA, inbox.ID), nil)
	req.Header.Set("Authorization", "Bearer "+tokenA)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get inbox agent bot failed after bot deletion: code=%d body=%s", w.Code, w.Body.String())
	}
	var emptyBotResp struct {
		Data any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &emptyBotResp)
	if emptyBotResp.Data != nil {
		t.Errorf("expected nil data for inbox agent bot after bot deletion, got %v", emptyBotResp.Data)
	}
}
