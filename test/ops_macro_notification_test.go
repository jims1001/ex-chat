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

func TestOps_MacroNotificationAndCSAT(t *testing.T) {
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
		"name":         "Ops Admin",
		"email":        "opsadmin@example.com",
		"password":     "Secret123!",
		"account_name": "Macro Test Corp",
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
	if len(authResp.Data.Accounts) == 0 {
		t.Fatalf("expected accounts in auth response")
	}
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	accIDStr := strconv.Itoa(int(accountID))

	// 2. Create conversation directly for testing
	conv := domain.Conversation{
		AccountID: accountID,
		DisplayID: 1,
		Status:    domain.ConversationStatusOpen,
		Priority:  domain.PriorityMedium,
	}
	db.Create(&conv)

	// 3. Create a Macro (OPS-01)
	macroPayload := map[string]any{
		"name":       "Resolve and Label Macro",
		"visibility": "global",
		"actions":    `[{"action_name":"resolve_conversation"},{"action_name":"add_label","action_params":["vip"]}]`,
	}
	body, _ = json.Marshal(macroPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/macros", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create macro failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var macroResp struct {
		Data domain.Macro `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &macroResp)
	macroID := macroResp.Data.ID
	if macroID == 0 {
		t.Fatalf("expected macro ID > 0, got %d", macroID)
	}

	// 4. List Macros (OPS-02)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/macros", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list macros failed: code=%d", w.Code)
	}

	// 5. Execute Macro (OPS-03 & OPS-04)
	execPayload := map[string]any{
		"conversation_ids": []uint{conv.ID},
	}
	body, _ = json.Marshal(execPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/macros/"+strconv.Itoa(int(macroID))+"/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("execute macro failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Verify conversation is now resolved and object_version incremented (CONS-10)
	var updatedConv domain.Conversation
	db.First(&updatedConv, conv.ID)
	if updatedConv.Status != domain.ConversationStatusResolved {
		t.Errorf("expected status resolved, got %s", updatedConv.Status)
	}
	if updatedConv.ObjectVersion != 2 {
		t.Errorf("expected object_version 2, got %d", updatedConv.ObjectVersion)
	}

	// 6. Test Notifications (OPS-14 ~ OPS-16)
	notif := domain.Notification{
		AccountID:        accountID,
		UserID:           authResp.Data.Accounts[0].ID,
		NotificationType: "conversation_assignment",
	}
	db.Create(&notif)

	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list notifications failed: code=%d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/notifications/read_all", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mark notifications read failed: code=%d", w.Code)
	}

	// 7. Test CSAT Survey Submission & CSAT Reporting (OPEN-06 & RPT)
	csatPayload := map[string]any{
		"account_id":    accountID,
		"rating":        5,
		"feedback_text": "Excellent support experience!",
	}
	body, _ = json.Marshal(csatPayload)
	req = httptest.NewRequest(http.MethodPost, "/public/api/v1/csat_survey/"+strconv.Itoa(int(conv.ID)), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit csat failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Check CSAT report
	req = httptest.NewRequest(http.MethodGet, "/api/v2/accounts/"+accIDStr+"/reports/csat", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get csat report failed: code=%d body=%s", w.Code, w.Body.String())
	}
}
