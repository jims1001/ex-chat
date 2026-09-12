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

func TestPlatformAPIFullLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:platform_full_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_platform_jwt_secret_999",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Create Platform App token for Platform API
	platformToken := fmt.Sprintf("plat_master_token_%d", time.Now().UnixNano())
	platformApp := domain.PlatformApp{
		Name:  "Cloud Master Control Plane",
		Token: platformToken,
	}
	if err := db.Create(&platformApp).Error; err != nil {
		t.Fatalf("failed to create platform app: %v", err)
	}

	var account1ID, account2ID uint
	var user1ID, user2ID, user3ID uint
	var globalBotID, accountBotID uint

	// =========================================================================
	// Scenario 1: 账号生命周期全量管理 (Account Full Lifecycle)
	// =========================================================================
	t.Run("Scenario 1: Account Full Lifecycle Management", func(t *testing.T) {
		// 1. Create Account 1
		a1Payload := map[string]any{
			"name":                  "Alpha Enterprise",
			"locale":                "zh-CN",
			"domain":                "alpha.example.com",
			"support_email":         "support@alpha.example.com",
			"auto_resolve_duration": 1440,
		}
		b, _ := json.Marshal(a1Payload)
		req := httptest.NewRequest(http.MethodPost, "/platform/api/v1/accounts", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create account 1 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var a1Resp struct {
			Data domain.Account `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &a1Resp)
		account1ID = a1Resp.Data.ID
		if account1ID == 0 {
			t.Fatalf("expected account1ID > 0")
		}

		// 2. Create Account 2
		a2Payload := map[string]any{
			"name":   "Beta Startup",
			"domain": "beta.example.com",
		}
		b, _ = json.Marshal(a2Payload)
		req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/accounts", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create account 2 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var a2Resp struct {
			Data domain.Account `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &a2Resp)
		account2ID = a2Resp.Data.ID

		// 3. List Accounts with Search & Pagination
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts?search=alpha&page=1&page_size=10", nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list accounts failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var listResp struct {
			Data struct {
				Accounts []domain.Account `json:"accounts"`
				Total    int64            `json:"total"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &listResp)
		if listResp.Data.Total != 1 || len(listResp.Data.Accounts) != 1 {
			t.Fatalf("expected 1 search result for 'alpha', got total=%d len=%d", listResp.Data.Total, len(listResp.Data.Accounts))
		}

		// 4. Get Account Detail
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/accounts/%d", account1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get account detail failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 5. Update Account
		updateAccPayload := map[string]any{
			"name":                  "Alpha Global Holdings",
			"domain":                "global.alpha.com",
			"auto_resolve_duration": 2880,
		}
		b, _ = json.Marshal(updateAccPayload)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/platform/api/v1/accounts/%d", account1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update account failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var patchResp struct {
			Data domain.Account `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &patchResp)
		if patchResp.Data.Name != "Alpha Global Holdings" || patchResp.Data.Domain != "global.alpha.com" {
			t.Fatalf("unexpected updated account: %v", patchResp.Data)
		}

		// 6. Check Account Status
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/accounts/%d/status", account1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get account status failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 7. Delete Account 2
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/platform/api/v1/accounts/%d", account2ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete account failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify Account 2 no longer exists
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/accounts/%d", account2ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted account, got %d", w.Code)
		}
	})

	// =========================================================================
	// Scenario 2: 用户生命周期全量管理 (User Full Lifecycle)
	// =========================================================================
	t.Run("Scenario 2: User Full Lifecycle Management", func(t *testing.T) {
		// 1. Create User 1
		u1Payload := map[string]string{
			"name":     "David Agent",
			"email":    "david@alpha.com",
			"password": "Password123!",
			"role":     "agent",
		}
		b, _ := json.Marshal(u1Payload)
		req := httptest.NewRequest(http.MethodPost, "/platform/api/v1/users", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create user 1 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var u1Resp struct {
			Data domain.User `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &u1Resp)
		user1ID = u1Resp.Data.ID

		// 2. Create User 2 (for deletion test)
		u2Payload := map[string]string{
			"name":     "Temporary Operator",
			"email":    "temp_op@alpha.com",
			"password": "Password123!",
			"role":     "agent",
		}
		b, _ = json.Marshal(u2Payload)
		req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/users", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create user 2 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var u2Resp struct {
			Data domain.User `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &u2Resp)
		user2ID = u2Resp.Data.ID

		// 3. List Users with Search & Role Filter
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/users?search=david&role=agent", nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list users failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var userListResp struct {
			Data struct {
				Users []domain.User `json:"users"`
				Total int64         `json:"total"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &userListResp)
		if userListResp.Data.Total != 1 || len(userListResp.Data.Users) != 1 {
			t.Fatalf("expected 1 search result for 'david', got total=%d len=%d", userListResp.Data.Total, len(userListResp.Data.Users))
		}

		// 4. Update User 1 Profile and Role
		updateUserPayload := map[string]any{
			"name":         "David Senior Agent",
			"role":         "administrator",
			"availability": "busy",
		}
		b, _ = json.Marshal(updateUserPayload)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/platform/api/v1/users/%d", user1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update user failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var updatedU1 struct {
			Data domain.User `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &updatedU1)
		if updatedU1.Data.Name != "David Senior Agent" || updatedU1.Data.Role != "administrator" {
			t.Fatalf("unexpected user update: %v", updatedU1.Data)
		}

		// 5. Get User Details
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/users/%d", user1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get user failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 6. Generate Single-Sign-On Login Token
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/users/%d/login", user1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("generate sso token failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var loginResp struct {
			Data struct {
				Token    string `json:"token"`
				LoginURL string `json:"login_url"`
				URL      string `json:"url"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
		if loginResp.Data.Token == "" || loginResp.Data.LoginURL == "" || loginResp.Data.URL == "" {
			t.Fatalf("expected non-empty sso login token, url and login_url, got %v", loginResp.Data)
		}

		// 6.1 Get Platform User Token (POST /platform/api/v1/users/:id/token)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/platform/api/v1/users/%d/token", user1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("post user token failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var tokenResp struct {
			Data struct {
				AccessToken string `json:"access_token"`
				Expiry      any    `json:"expiry"`
				User        struct {
					ID          uint   `json:"id"`
					Name        string `json:"name"`
					DisplayName string `json:"display_name"`
					Email       string `json:"email"`
					PubsubToken string `json:"pubsub_token"`
				} `json:"user"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &tokenResp)
		if tokenResp.Data.AccessToken == "" {
			t.Fatalf("expected non-empty access_token from user token endpoint")
		}
		if tokenResp.Data.User.ID != user1ID || tokenResp.Data.User.PubsubToken == "" {
			t.Fatalf("expected user data with valid ID and pubsub_token, got %+v", tokenResp.Data.User)
		}

		// 6.2 Get Platform User Token via GET
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/users/%d/token", user1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get user token failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 6.3 Create User Without Password (Chatwoot compatible auto-generated password)
		pwdlessPayload := map[string]string{
			"name":  "Passwordless Specialist",
			"email": "pwdless@alpha.com",
			"role":  "agent",
		}
		b, _ = json.Marshal(pwdlessPayload)
		req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/users", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create user without password failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 6.4 Create User with Existing Email (Chatwoot find-or-create behavior)
		dupPayload := map[string]string{
			"name":  "David Senior Chief Agent",
			"email": "david@alpha.com",
			"role":  "administrator",
		}
		b, _ = json.Marshal(dupPayload)
		req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/users", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("find-or-create existing user failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 7. Delete User 2
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/platform/api/v1/users/%d", user2ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete user failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify User 2 no longer exists
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/users/%d", user2ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted user, got %d", w.Code)
		}
	})

	// =========================================================================
	// Scenario 3: 租户成员关系添加、查询与解绑 (AccountUser Membership)
	// =========================================================================
	t.Run("Scenario 3: Account User Membership Operations", func(t *testing.T) {
		// 1. Add User 1 to Account 1
		addU1Payload := map[string]any{
			"user_id": user1ID,
			"role":    "administrator",
		}
		b, _ := json.Marshal(addU1Payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users", account1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("add user 1 to account failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var au1Resp struct {
			Data domain.AccountUser `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &au1Resp)
		if au1Resp.Data.UserID != user1ID || au1Resp.Data.Role != "administrator" {
			t.Fatalf("expected AccountUser entity returned with administrator role, got %+v", au1Resp.Data)
		}

		// 1.1 Re-add User 1 to Account 1 with different role (should update role, not error)
		updateRolePayload := map[string]any{
			"user_id": user1ID,
			"role":    "agent",
		}
		b, _ = json.Marshal(updateRolePayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users", account1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("re-add user 1 to update role failed: code=%d body=%s", w.Code, w.Body.String())
		}
		_ = json.Unmarshal(w.Body.Bytes(), &au1Resp)
		if au1Resp.Data.Role != "agent" {
			t.Fatalf("expected updated role agent, got %s", au1Resp.Data.Role)
		}

		// 2. Create User 3 and add to Account 1
		u3Payload := map[string]string{
			"name":     "Emma Support",
			"email":    "emma@alpha.com",
			"password": "Password123!",
			"role":     "agent",
		}
		b, _ = json.Marshal(u3Payload)
		req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/users", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var u3Resp struct {
			Data domain.User `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &u3Resp)
		user3ID = u3Resp.Data.ID

		addU3Payload := map[string]any{
			"user_id": user3ID,
			"role":    "agent",
		}
		b, _ = json.Marshal(addU3Payload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users", account1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("add user 3 to account failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 3. List Account Users
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users", account1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list account users failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var membersResp struct {
			Data struct {
				AccountUsers []domain.AccountUser `json:"account_users"`
				Total        int64                `json:"total"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &membersResp)
		if membersResp.Data.Total != 2 || len(membersResp.Data.AccountUsers) != 2 {
			t.Fatalf("expected 2 account users, got total=%d len=%d", membersResp.Data.Total, len(membersResp.Data.AccountUsers))
		}
		if membersResp.Data.AccountUsers[0].User == nil {
			t.Fatalf("expected user details preloaded in account_user")
		}

		// 4. Remove User 3 via JSON Body
		delPayload := map[string]any{
			"user_id": user3ID,
		}
		b, _ = json.Marshal(delPayload)
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users", account1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("remove user from account via body failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify 1 member remaining
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users", account1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		_ = json.Unmarshal(w.Body.Bytes(), &membersResp)
		if membersResp.Data.Total != 1 {
			t.Fatalf("expected 1 member remaining, got %d", membersResp.Data.Total)
		}

		// 5. Add User 3 back, then remove via RESTful URL Param: /accounts/:id/account_users/:user_id
		b, _ = json.Marshal(addU3Payload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users", account1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)

		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users/%d", account1ID, user3ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("remove user via url param failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify non-member deletion returns 404
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/platform/api/v1/accounts/%d/account_users/%d", account1ID, user3ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when removing non-member, got %d", w.Code)
		}
	})

	// =========================================================================
	// Scenario 4: 全局平台级 Bot 管理 (Platform Global AgentBot)
	// =========================================================================
	t.Run("Scenario 4: Platform Global AgentBot Management", func(t *testing.T) {
		// 1. Create Global Platform Bot (account_id = 0)
		botPayload := map[string]any{
			"name":         "Global AI Webhook Bot",
			"description":  "Platform-wide intelligent routing and reply bot",
			"outgoing_url": "https://ai.example.com/platform-webhook",
			"bot_type":     "webhook",
			"avatar_url":   "https://ai.example.com/bot-avatar.png",
		}
		b, _ := json.Marshal(botPayload)
		req := httptest.NewRequest(http.MethodPost, "/platform/api/v1/agent_bots", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create global agent bot failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var botResp struct {
			Data domain.AgentBot `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &botResp)
		globalBotID = botResp.Data.ID
		if globalBotID == 0 {
			t.Fatalf("expected globalBotID > 0")
		}
		if botResp.Data.AccountID != 0 {
			t.Fatalf("expected global bot AccountID = 0, got %d", botResp.Data.AccountID)
		}
		if botResp.Data.AccessToken == "" {
			t.Fatalf("expected auto-generated AccessToken on AgentBot")
		}
		if botResp.Data.AvatarURL != "https://ai.example.com/bot-avatar.png" {
			t.Fatalf("expected AvatarURL to be set, got %s", botResp.Data.AvatarURL)
		}

		// 2. List Global Agent Bots
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/agent_bots", nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list global agent bots failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var botsListResp struct {
			Data struct {
				AgentBots []domain.AgentBot `json:"agent_bots"`
				Total     int64             `json:"total"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &botsListResp)
		if botsListResp.Data.Total < 1 {
			t.Fatalf("expected at least 1 agent bot, got %d", botsListResp.Data.Total)
		}

		// 3. Get Agent Bot Detail
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/agent_bots/%d", globalBotID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get agent bot failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 4. Update Agent Bot
		updateBotPayload := map[string]any{
			"outgoing_url": "https://ai.example.com/v2/webhook",
			"description":  "Updated platform-wide intelligent routing bot",
		}
		b, _ = json.Marshal(updateBotPayload)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/platform/api/v1/agent_bots/%d", globalBotID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update agent bot failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var updatedBotResp struct {
			Data domain.AgentBot `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &updatedBotResp)
		if updatedBotResp.Data.OutgoingURL != "https://ai.example.com/v2/webhook" {
			t.Fatalf("expected updated outgoing url, got %s", updatedBotResp.Data.OutgoingURL)
		}

		// 5. Delete Agent Bot Avatar (DELETE /platform/api/v1/agent_bots/:id/avatar)
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/platform/api/v1/agent_bots/%d/avatar", globalBotID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete agent bot avatar failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var avatarDelResp struct {
			Data domain.AgentBot `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &avatarDelResp)
		if avatarDelResp.Data.AvatarURL != "" {
			t.Fatalf("expected avatar_url to be empty after deletion, got %s", avatarDelResp.Data.AvatarURL)
		}
	})

	// =========================================================================
	// Scenario 5: 租户专有 Bot 关联与治理 (Account AgentBot)
	// =========================================================================
	t.Run("Scenario 5: Account AgentBot Management", func(t *testing.T) {
		// 1. Create Bot Scoped to Account 1
		accBotPayload := map[string]any{
			"name":         "Alpha Custom CRM Bot",
			"description":  "Custom CRM synchronization agent bot",
			"outgoing_url": "https://alpha.example.com/crm-webhook",
			"bot_type":     "webhook",
		}
		b, _ := json.Marshal(accBotPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/platform/api/v1/accounts/%d/agent_bots", account1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api_access_token", platformToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create account agent bot failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var accBotResp struct {
			Data domain.AgentBot `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &accBotResp)
		accountBotID = accBotResp.Data.ID
		if accountBotID == 0 || accBotResp.Data.AccountID != account1ID {
			t.Fatalf("expected account-scoped bot with accountID=%d, got %d", account1ID, accBotResp.Data.AccountID)
		}

		// 2. List Account 1 Agent Bots (Should return Account 1 Custom Bot AND Global Platform Bot)
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/accounts/%d/agent_bots", account1ID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list account agent bots failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var listResp struct {
			Data struct {
				AgentBots []domain.AgentBot `json:"agent_bots"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &listResp)
		if len(listResp.Data.AgentBots) < 2 {
			t.Fatalf("expected at least 2 bots for account (1 custom + 1 global), got %d", len(listResp.Data.AgentBots))
		}

		// 3. Delete Account Agent Bot
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/platform/api/v1/accounts/%d/agent_bots/%d", account1ID, accountBotID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete account agent bot failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify deletion
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/platform/api/v1/agent_bots/%d", accountBotID), nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted account bot, got %d", w.Code)
		}
	})

	// =========================================================================
	// Scenario 6: 平台凭证认证与越权隔离防御 (Platform Auth & Security)
	// =========================================================================
	t.Run("Scenario 6: Platform Authentication and Boundary Protection", func(t *testing.T) {
		// 1. Missing Token -> 401 Unauthorized
		req := httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for missing platform token, got %d", w.Code)
		}

		// 2. Invalid Token -> 401 Unauthorized
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts", nil)
		req.Header.Set("api_access_token", "invalid_fake_platform_token")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for invalid platform token, got %d", w.Code)
		}

		// 3. Alternative Header X-Platform-App-Token -> 200 OK
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts", nil)
		req.Header.Set("X-Platform-App-Token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 using X-Platform-App-Token header, got %d", w.Code)
		}

		// 3.1 Alternative Header HTTP_API_ACCESS_TOKEN -> 200 OK
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts", nil)
		req.Header.Set("HTTP_API_ACCESS_TOKEN", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 using HTTP_API_ACCESS_TOKEN header, got %d", w.Code)
		}

		// 3.2 Standard Authorization Bearer Header -> 200 OK
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts", nil)
		req.Header.Set("Authorization", "Bearer "+platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 using Authorization Bearer header, got %d", w.Code)
		}

		// 3.3 Query Parameter ?api_access_token -> 200 OK
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts?api_access_token="+platformToken, nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 using query api_access_token, got %d", w.Code)
		}

		// 4. Invalid Account ID -> 400 or 404
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/accounts/99999999", nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent account, got %d", w.Code)
		}

		// 5. Invalid User ID -> 400 or 404
		req = httptest.NewRequest(http.MethodGet, "/platform/api/v1/users/99999999", nil)
		req.Header.Set("api_access_token", platformToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent user, got %d", w.Code)
		}
	})
}
