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

func TestPlatform_Public_And_MobilePush(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment: "test",
		DBDriver:    "sqlite",
		DBPath:      ":memory:",
		JWTSecret:          "test_secret_key_1234567890123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Setup Platform App token for Platform API (OPEN-07)
	platformToken := "plat_token_test_123456789"
	platformApp := domain.PlatformApp{
		Name:  "Test SaaS Master Console",
		Token: platformToken,
	}
	db.Create(&platformApp)

	// 2. Test Platform API - Create Account (OPEN-09)
	createAccPayload := map[string]string{
		"name":   "Tenant X Corp",
		"locale": "en",
		"domain": "tenant-x.com",
	}
	body, _ := json.Marshal(createAccPayload)
	req := httptest.NewRequest(http.MethodPost, "/platform/api/v1/accounts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", platformToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create account failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var createAccResp struct {
		Data domain.Account `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &createAccResp)
	tenantID := createAccResp.Data.ID
	if tenantID == 0 {
		t.Fatalf("expected tenantID > 0")
	}

	// 3. Test Platform API - Create User (OPEN-08)
	createUserPayload := map[string]string{
		"name":     "Platform Agent",
		"email":    "plat_agent@example.com",
		"password": "SecretPassword123!",
		"role":     "agent",
	}
	body, _ = json.Marshal(createUserPayload)
	req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", platformToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var createUserResp struct {
		Data domain.User `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &createUserResp)
	userID := createUserResp.Data.ID
	if userID == 0 {
		t.Fatalf("expected userID > 0")
	}

	// 4. Test Platform API - Add User to Account (OPEN-10)
	addUserPayload := map[string]any{
		"user_id": userID,
		"role":    "agent",
	}
	body, _ = json.Marshal(addUserPayload)
	req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/accounts/"+strconv.Itoa(int(tenantID))+"/account_users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", platformToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add user to account failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 5. Test Public API (OPEN-01 to OPEN-05 & CONS-05)
	inbox := domain.Inbox{
		AccountID:       tenantID,
		Name:            "Public Website Channel",
		ChannelType:     domain.ChannelWebWidget,
		WebsiteToken:    "pub_web_token_abc",
		GreetingMessage: "Welcome to support!",
	}
	db.Create(&inbox)

	// 5.1 OPEN-01: Public Inbox Fetch
	req = httptest.NewRequest(http.MethodGet, "/public/api/v1/inboxes/pub_web_token_abc", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get public inbox failed: code=%d", w.Code)
	}

	// 5.2 OPEN-02: Public Contact Creation
	contactPayload := map[string]string{
		"name":  "Public Visitor",
		"email": "visitor@customer.com",
	}
	body, _ = json.Marshal(contactPayload)
	req = httptest.NewRequest(http.MethodPost, "/public/api/v1/inboxes/pub_web_token_abc/contacts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create public contact failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var contactResp struct {
		Data domain.Contact `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &contactResp)
	contactID := contactResp.Data.ID
	if contactID == 0 {
		t.Fatalf("expected contactID > 0")
	}

	// 5.3 OPEN-04: Public Conversation Creation
	req = httptest.NewRequest(http.MethodPost, "/public/api/v1/inboxes/pub_web_token_abc/contacts/"+strconv.Itoa(int(contactID))+"/conversations", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create public conversation failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var convResp struct {
		Data domain.Conversation `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &convResp)
	convID := convResp.Data.ID
	if convID == 0 {
		t.Fatalf("expected convID > 0")
	}

	// 5.4 OPEN-05 & CONS-05: Public Message Creation with Echo ID Idempotency
	msgPayload := map[string]string{
		"content": "Hello, I need help!",
		"echo_id": "echo_client_msg_1001",
	}
	body, _ = json.Marshal(msgPayload)
	msgUrl := "/public/api/v1/inboxes/pub_web_token_abc/contacts/" + strconv.Itoa(int(contactID)) + "/conversations/" + strconv.Itoa(int(convID)) + "/messages"
	req = httptest.NewRequest(http.MethodPost, msgUrl, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create public message failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Re-sending with same echo_id should return 200 OK without creating a duplicate message
	req = httptest.NewRequest(http.MethodPost, msgUrl, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on duplicate message echo_id, got %d", w.Code)
	}

	var msgCount int64
	db.Model(&domain.Message{}).Where("conversation_id = ?", convID).Count(&msgCount)
	if msgCount != 1 {
		t.Fatalf("expected exactly 1 message due to idempotency, got %d", msgCount)
	}

	// 6. Test Mobile Push & Device Subscriptions (MOB-01 ~ MOB-05)
	signInPayload := map[string]string{
		"email":    "plat_agent@example.com",
		"password": "SecretPassword123!",
	}
	body, _ = json.Marshal(signInPayload)
	req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent sign in failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var signInResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &signInResp)
	userToken := signInResp.Data.Token

	// Register device subscription
	subPayload := map[string]string{
		"subscription_type": "fcm",
		"push_token":        "fcm_token_device_xyz_998877",
		"device_name":       "Pixel 8 Pro",
		"app_version":       "2.5.0",
	}
	body, _ = json.Marshal(subPayload)
	subUrl := "/api/v1/accounts/" + strconv.Itoa(int(tenantID)) + "/notification_subscriptions"
	req = httptest.NewRequest(http.MethodPost, subUrl, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("register subscription failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Delete device subscription
	delSubPayload := map[string]string{
		"push_token": "fcm_token_device_xyz_998877",
	}
	body, _ = json.Marshal(delSubPayload)
	req = httptest.NewRequest(http.MethodDelete, subUrl, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete subscription failed: code=%d body=%s", w.Code, w.Body.String())
	}
}
