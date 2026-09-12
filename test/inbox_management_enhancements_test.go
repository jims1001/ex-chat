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

func TestInboxManagementEnhancements(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "inbox_mgmt_enhancements_secret_32b!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user and create test account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Inbox Admin",
		"email":        "inbox_admin@example.com",
		"password":     "Password123!",
		"account_name": "Inbox Management Org",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(signUpBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin sign up failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	adminUserID := authResp.Data.User.ID

	// Create 2 additional agents
	var agent1, agent2 domain.User
	agent1 = domain.User{
		Name:         "Agent One",
		Email:        "agent1@example.com",
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	agent2 = domain.User{
		Name:         "Agent Two",
		Email:        "agent2@example.com",
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	db.Create(&agent1)
	db.Create(&agent2)
	db.Create(&domain.AccountUser{AccountID: accountID, UserID: agent1.ID, Role: domain.RoleAgent})
	db.Create(&domain.AccountUser{AccountID: accountID, UserID: agent2.ID, Role: domain.RoleAgent})

	// Create an Inbox
	createInboxBody, _ := json.Marshal(map[string]any{
		"name": "Support Web Inbox",
		"channel": map[string]any{
			"type":        "web_widget",
			"website_url": "https://example.com",
		},
	})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes", accountID), bytes.NewReader(createInboxBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create inbox failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var inboxResp struct {
		Data domain.Inbox `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inboxResp)
	inboxID := inboxResp.Data.ID
	if inboxID == 0 {
		t.Fatalf("invalid inbox id returned: %d", inboxID)
	}

	// ==========================================
	// Test 1: Inbox 成员批量更新和批量移除 (Bulk Sync & Bulk Remove)
	// ==========================================
	t.Run("BulkSyncAndRemoveMembers", func(t *testing.T) {
		// Test PATCH /inboxes/:id/members
		syncBody, _ := json.Marshal(map[string]any{
			"user_ids": []uint{adminUserID, agent1.ID, agent2.ID},
		})
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/members", accountID, inboxID), bytes.NewReader(syncBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("patch inboxes/:id/members failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var syncResp struct {
			Payload []domain.User `json:"payload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &syncResp)
		if len(syncResp.Payload) != 3 {
			t.Fatalf("expected 3 members after sync, got %d", len(syncResp.Payload))
		}

		// Test DELETE /inbox_members (remove agent2)
		delBody, _ := json.Marshal(map[string]any{
			"inbox_id": inboxID,
			"user_ids": []uint{agent2.ID},
		})
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/inbox_members", accountID), bytes.NewReader(delBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete inbox_members failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// Test DELETE /inboxes/:id/members (remove agent1)
		delBody2, _ := json.Marshal(map[string]any{
			"user_ids": []uint{agent1.ID},
		})
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/members", accountID, inboxID), bytes.NewReader(delBody2))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete inboxes/:id/members failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// Sync back admin and agent1 for subsequent tests
		syncBody2, _ := json.Marshal(map[string]any{
			"inbox_id": inboxID,
			"user_ids": []uint{adminUserID, agent1.ID},
		})
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/inbox_members", accountID), bytes.NewReader(syncBody2))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("patch inbox_members failed: code=%d, body=%s", w.Code, w.Body.String())
		}
	})

	// ==========================================
	// Test 2: 可分配坐席查询 (GET /inboxes/:id/assignable_agents)
	// ==========================================
	t.Run("GetAssignableAgents", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignable_agents", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get assignable agents failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Payload []domain.User `json:"payload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Payload) != 2 {
			t.Fatalf("expected 2 assignable agents, got %d", len(resp.Payload))
		}
	})

	// ==========================================
	// Test 3: Inbox 活动列表 (GET /inboxes/:id/campaigns)
	// ==========================================
	t.Run("ListInboxCampaigns", func(t *testing.T) {
		// Create a campaign for this inbox
		campaign := domain.Campaign{
			AccountID:    accountID,
			InboxID:      inboxID,
			Title:        "Welcome Campaign",
			Description:  "A campaign for web visitors",
			Message:      "Welcome to our store!",
			CampaignType: "ongoing",
			Status:       "active",
		}
		db.Create(&campaign)

		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/campaigns", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list inbox campaigns failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Payload []domain.Campaign `json:"payload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Payload) == 0 {
			t.Fatalf("expected at least 1 campaign, got 0")
		}
		if resp.Payload[0].Title != "Welcome Campaign" {
			t.Errorf("expected title 'Welcome Campaign', got '%s'", resp.Payload[0].Title)
		}
	})

	// ==========================================
	// Test 4: Agent Bot 查询、绑定、解绑
	// ==========================================
	t.Run("AgentBotLifecycle", func(t *testing.T) {
		bot := domain.AgentBot{
			AccountID:   accountID,
			Name:        "Test CS Bot",
			Description: "Automated CS bot",
			BotType:     "webhook",
			OutgoingURL: "https://bot.example.com/webhook",
		}
		db.Create(&bot)

		// 1. Set agent bot
		setBody, _ := json.Marshal(map[string]any{
			"agent_bot": bot.ID,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/set_agent_bot", accountID, inboxID), bytes.NewReader(setBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("set agent bot failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// 2. Get agent bot
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/agent_bot", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get agent bot failed: code=%d, body=%s", w.Code, w.Body.String())
		}
		var botResp struct {
			Data *domain.AgentBot `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &botResp)
		if botResp.Data == nil || botResp.Data.ID != bot.ID {
			t.Fatalf("expected bot id %d, got %+v", bot.ID, botResp.Data)
		}

		// 3. Unset agent bot via DELETE /inboxes/:id/agent_bot
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/agent_bot", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("unset agent bot failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// Verify bot is unset
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/agent_bot", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get agent bot after unset failed: code=%d", w.Code)
		}
		var botRespAfter struct {
			Data *domain.AgentBot `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &botRespAfter)
		if botRespAfter.Data != nil {
			t.Fatalf("expected nil agent bot after unset, got %+v", botRespAfter.Data)
		}

		// Set again, and test unsetting via POST set_agent_bot with 0 / null
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/set_agent_bot", accountID, inboxID), bytes.NewReader(setBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("re-set agent bot failed: code=%d", w.Code)
		}

		unsetBody, _ := json.Marshal(map[string]any{
			"agent_bot": 0,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/set_agent_bot", accountID, inboxID), bytes.NewReader(unsetBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("unset agent bot via post failed: code=%d", w.Code)
		}
	})

	// ==========================================
	// Test 5: Inbox 头像删除 (DELETE /inboxes/:id/avatar)
	// ==========================================
	t.Run("DeleteInboxAvatar", func(t *testing.T) {
		// Set avatar first
		db.Model(&domain.Inbox{}).Where("id = ?", inboxID).Update("avatar_url", "https://cdn.example.com/avatar.png")

		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/avatar", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete avatar failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var updatedInbox domain.Inbox
		db.First(&updatedInbox, inboxID)
		if updatedInbox.AvatarURL != "" {
			t.Fatalf("expected avatar_url to be empty, got %s", updatedInbox.AvatarURL)
		}
	})

	// ==========================================
	// Test 6: 消息模板同步与列表 (GET/POST /message_templates, /sync_templates)
	// ==========================================
	t.Run("MessageTemplates", func(t *testing.T) {
		// GET templates
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/message_templates", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get message templates failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var listResp struct {
			Templates []map[string]any `json:"templates"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &listResp)
		if len(listResp.Templates) == 0 {
			t.Fatalf("expected non-empty templates, got 0")
		}

		// POST sync_templates
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/sync_templates", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("sync message templates failed: code=%d, body=%s", w.Code, w.Body.String())
		}
		var syncResp struct {
			Status    string           `json:"status"`
			Templates []map[string]any `json:"templates"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &syncResp)
		if syncResp.Status != "synced" || len(syncResp.Templates) == 0 {
			t.Fatalf("unexpected sync response: %+v", syncResp)
		}
	})

	// ==========================================
	// Test 7: 渠道健康状态 (GET /inboxes/:id/health)
	// ==========================================
	t.Run("ChannelHealth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/health", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get channel health failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var healthResp struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &healthResp)
		if healthResp.Status != "healthy" {
			t.Fatalf("expected healthy status, got %s", healthResp.Status)
		}
	})

	// ==========================================
	// Test 8: Webhook 注册 (POST /inboxes/:id/register_webhook)
	// ==========================================
	t.Run("RegisterWebhook", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"webhook_url": "https://callback.mycrm.com/chatwoot_inbox",
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/register_webhook", accountID, inboxID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("register webhook failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var updated domain.Inbox
		db.First(&updated, inboxID)
		if updated.WebhookURL != "https://callback.mycrm.com/chatwoot_inbox" {
			t.Fatalf("expected webhook url updated, got %s", updated.WebhookURL)
		}
	})

	// ==========================================
	// Test 9: 密钥与 HMAC Token 重置
	// ==========================================
	t.Run("ResetSecretAndRotateHMAC", func(t *testing.T) {
		// Reset secret
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/reset_secret", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("reset secret failed: code=%d, body=%s", w.Code, w.Body.String())
		}
		var secretResp struct {
			SecretKey string `json:"secret_key"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &secretResp)
		if secretResp.SecretKey == "" {
			t.Fatalf("expected non-empty secret key")
		}

		// Rotate HMAC token
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/rotate_hmac_token", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("rotate hmac token failed: code=%d, body=%s", w.Code, w.Body.String())
		}
		var hmacResp struct {
			HMACToken string `json:"hmac_token"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &hmacResp)
		if hmacResp.HMACToken == "" {
			t.Fatalf("expected non-empty hmac token")
		}
	})

	// ==========================================
	// Test 10: WhatsApp Calling 开关与来电设置
	// ==========================================
	t.Run("WhatsAppCallingAndSettings", func(t *testing.T) {
		// Enable calling
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/enable_whatsapp_calling", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("enable whatsapp calling failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// Set inbound calls
		inboundBody, _ := json.Marshal(map[string]any{
			"inbound_calls_enabled": true,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/set_inbound_calls", accountID, inboxID), bytes.NewReader(inboundBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("set inbound calls failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// Set call recording
		recordingBody, _ := json.Marshal(map[string]any{
			"call_recording_enabled": true,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/set_call_recording", accountID, inboxID), bytes.NewReader(recordingBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("set call recording failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// Disable calling
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/disable_whatsapp_calling", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("disable whatsapp calling failed: code=%d, body=%s", w.Code, w.Body.String())
		}
	})

	// ==========================================
	// Test 11: CSAT 模板读取、保存与分析
	// ==========================================
	t.Run("CSATTemplateOperations", func(t *testing.T) {
		// Save CSAT template
		saveBody, _ := json.Marshal(map[string]any{
			"csat_template": map[string]any{
				"question": "How satisfied were you with your customer support experience?",
				"ratings":  []int{1, 2, 3, 4, 5},
			},
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/csat_template", accountID, inboxID), bytes.NewReader(saveBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("save csat template failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		// Get CSAT template
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/csat_template", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get csat template failed: code=%d, body=%s", w.Code, w.Body.String())
		}
		var templateResp struct {
			Template map[string]any `json:"template"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &templateResp)
		if templateResp.Template["question"] != "How satisfied were you with your customer support experience?" {
			t.Fatalf("unexpected template question: %v", templateResp.Template)
		}

		// Analyze CSAT template
		analyzeBody, _ := json.Marshal(map[string]any{
			"response_text": "Customer said service was fast, courteous and totally solved the issue.",
			"rating":        5,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/csat_template/analyze", accountID, inboxID), bytes.NewReader(analyzeBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("analyze csat template failed: code=%d, body=%s", w.Code, w.Body.String())
		}
		var analysisResp struct {
			Sentiment string `json:"sentiment"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &analysisResp)
		if analysisResp.Sentiment != "positive" {
			t.Fatalf("expected positive sentiment, got %s", analysisResp.Sentiment)
		}
	})

	// ==========================================
	// Test 12: 权限与角色管控 (Role and Permission Enforcement)
	// ==========================================
	t.Run("RoleAndPermissionEnforcement", func(t *testing.T) {
		// Generate token for agent1 (who does NOT have inbox_manage permission)
		agentToken, err := auth.GenerateToken(&agent1, cfg.JWTSecret, 24)
		if err != nil {
			t.Fatalf("failed to generate agent token: %v", err)
		}

		// Protected endpoint: DELETE /avatar
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/avatar", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+agentToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent without inbox_manage, got %d", w.Code)
		}

		// Protected endpoint: POST /set_agent_bot
		botReqBody, _ := json.Marshal(map[string]any{"agent_bot": 1})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/set_agent_bot", accountID, inboxID), bytes.NewReader(botReqBody))
		req.Header.Set("Authorization", "Bearer "+agentToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent without inbox_manage, got %d", w.Code)
		}

		// Protected endpoint: POST /rotate_hmac_token
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/rotate_hmac_token", accountID, inboxID), nil)
		req.Header.Set("Authorization", "Bearer "+agentToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent without inbox_manage, got %d", w.Code)
		}
	})
}
