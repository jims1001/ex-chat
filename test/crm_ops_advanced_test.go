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
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAdvancedCapabilities_CRM_OPS_EXT(t *testing.T) {
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

	// 1. Sign up admin
	signUpPayload := map[string]string{
		"name":         "Advanced Admin",
		"email":        "advancedadmin@example.com",
		"password":     "Secret123!",
		"account_name": "Enterprise Suite Corp",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d", w.Code)
	}

	var authResp struct {
		Success bool `json:"success"`
		Data    struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	accIDStr := strconv.Itoa(int(accountID))

	// 2. Test Company (CRM-10 ~ 12)
	companyPayload := map[string]string{
		"name":        "Acme Global Holdings",
		"domain":      "acme.com",
		"industry":    "FinTech",
		"description": "Enterprise customer",
	}
	body, _ = json.Marshal(companyPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/companies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create company failed: code=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/companies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list companies failed: code=%d", w.Code)
	}

	// 3. Test Campaign (OPS-07 ~ 13)
	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "Campaign Web Channel",
		ChannelType:  domain.ChannelWebWidget,
		WebsiteToken: "camp_web_tok_123",
	}
	db.Create(&inbox)

	campPayload := map[string]any{
		"inbox_id":      inbox.ID,
		"title":         "Spring Announcement",
		"message":       "Check out our new release!",
		"campaign_type": "one_off",
		"audience":      `{"tags":["vip"]}`,
	}
	body, _ = json.Marshal(campPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/campaigns", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create campaign failed: code=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/campaigns", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list campaigns failed: code=%d", w.Code)
	}

	// 4. Test SLA Policy (RPT / SLA)
	slaPayload := map[string]any{
		"name":                          "VIP Priority SLA",
		"description":                   "1 hour first response, 24 hour resolution",
		"first_response_time_threshold": 3600,
		"resolution_time_threshold":    86400,
	}
	body, _ = json.Marshal(slaPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/sla_policies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create sla policy failed: code=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/sla_policies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list sla policies failed: code=%d", w.Code)
	}

	// 5. Test AgentBot (EXT / Bots)
	botPayload := map[string]string{
		"name":         "Custom Auto Responder",
		"description":  "Webhook responder",
		"outgoing_url": "https://bot-service.internal/webhook",
		"bot_type":     "webhook",
	}
	body, _ = json.Marshal(botPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/agent_bots", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create agent bot failed: code=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/agent_bots", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list agent bots failed: code=%d", w.Code)
	}

	// 6. Test Attachment Upload (CONV / Attachments)
	msg := domain.Message{
		AccountID:      accountID,
		ConversationID: 1,
		SenderType:     domain.SenderTypeUser,
		SenderID:       1,
		Content:        "Here is the screenshot",
	}
	db.Create(&msg)

	attPayload := map[string]any{
		"message_id": msg.ID,
		"file_type":  "image/png",
		"data_url":   "https://storage.internal/uploads/screenshot.png",
		"file_size":  204800,
	}
	body, _ = json.Marshal(attPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations/1/attachments", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload attachment failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 7. Test Custom Filter (OPS / Filters)
	filterPayload := map[string]string{
		"name":        "My Open Conversations",
		"filter_type": "conversation",
		"query":       `{"status":"open"}`,
	}
	body, _ = json.Marshal(filterPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/custom_filters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create custom filter failed: code=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/custom_filters", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list custom filters failed: code=%d", w.Code)
	}

	// 8. Test Draft Message (CONV / Drafts)
	draftPayload := map[string]string{
		"message": "Draft reply to customer...",
	}
	body, _ = json.Marshal(draftPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations/1/draft_messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save draft failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 9. Test Conversation Participants (CONV / Participants)
	partPayload := map[string]any{
		"user_ids": []uint{authResp.Data.Accounts[0].ID},
	}
	body, _ = json.Marshal(partPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations/1/participants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add participant failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 10. Test Contact Notes (CRM / Notes)
	notePayload := map[string]string{
		"content": "VIP customer requesting priority escalation",
	}
	body, _ = json.Marshal(notePayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/contacts/1/notes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create contact note failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 11. Test Capacity Policy (ACC / Capacity)
	capPayload := map[string]any{
		"user_id":            authResp.Data.Accounts[0].ID,
		"conversation_limit": 15,
	}
	body, _ = json.Marshal(capPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/agent_capacity_policies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set capacity policy failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 12. Test Bulk Actions (OPS / Bulk)
	bulkPayload := map[string]any{
		"type": "update_status",
		"ids":  []uint{1},
		"fields": map[string]any{
			"status": "resolved",
		},
	}
	body, _ = json.Marshal(bulkPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/bulk_actions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk actions failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 13. Test Integrations Directory (EXT / Apps)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/integrations/apps", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list integration apps failed: code=%d body=%s", w.Code, w.Body.String())
	}
}
