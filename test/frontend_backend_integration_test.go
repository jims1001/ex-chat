package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestFrontendBackend7GapsIntegration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "frontend-gaps-secret-token-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	var receivedWebhookReq *http.Request
	router.SetWebhookHTTPClient(&http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			receivedWebhookReq = req
			res := httptest.NewRecorder()
			res.WriteHeader(http.StatusOK)
			_, _ = res.Write([]byte(`{"received":true,"status":"delivered"}`))
			return res.Result(), nil
		}),
	})

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 0: Create initial admin & account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "陈宁",
		"email":        "chen.ning@chengchuan.example",
		"password":     "password123456",
		"account_name": "澄川服务",
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
			User  struct {
				ID           uint   `json:"id"`
				Name         string `json:"name"`
				Email        string `json:"email"`
				Availability string `json:"availability"`
			} `json:"user"`
			Accounts []struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
			} `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wSignUp.Body.Bytes(), &authResp); err != nil {
		t.Fatalf("failed to unmarshal sign up response: %v, body: %s", err, wSignUp.Body.String())
	}
	if len(authResp.Data.Accounts) == 0 {
		t.Fatalf("no accounts returned in sign up response: %s", wSignUp.Body.String())
	}
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	authHeader := "Bearer " + token

	// -------------------------------------------------------------
	// Gap 4: Profile, Availability & Workspace Sync
	// -------------------------------------------------------------
	t.Run("Gap4_Profile_Availability_Workspace_Sync", func(t *testing.T) {
		// 1. GET /api/v1/profile
		wProf := httptest.NewRecorder()
		reqProf, _ := http.NewRequest("GET", "/api/v1/profile", nil)
		reqProf.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wProf, reqProf)
		if wProf.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/profile failed: %d", wProf.Code)
		}

		// 2. PUT /api/v1/profile (Update Name)
		putBody, _ := json.Marshal(map[string]string{
			"name": "陈宁 (主管)",
		})
		wPutProf := httptest.NewRecorder()
		reqPutProf, _ := http.NewRequest("PUT", "/api/v1/profile", bytes.NewBuffer(putBody))
		reqPutProf.Header.Set("Authorization", authHeader)
		reqPutProf.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPutProf, reqPutProf)
		if wPutProf.Code != http.StatusOK {
			t.Fatalf("PUT /api/v1/profile failed: %d", wPutProf.Code)
		}

		// Verify name was updated
		var updatedUser domain.User
		db.First(&updatedUser, authResp.Data.User.ID)
		if updatedUser.Name != "陈宁 (主管)" {
			t.Fatalf("expected updated user name '陈宁 (主管)', got '%s'", updatedUser.Name)
		}

		// 3. POST /api/v1/profile/availability (online -> busy)
		availBody, _ := json.Marshal(map[string]string{
			"availability": "busy",
		})
		wAvail := httptest.NewRecorder()
		reqAvail, _ := http.NewRequest("POST", "/api/v1/profile/availability", bytes.NewBuffer(availBody))
		reqAvail.Header.Set("Authorization", authHeader)
		reqAvail.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAvail, reqAvail)
		if wAvail.Code != http.StatusOK {
			t.Fatalf("POST /api/v1/profile/availability failed: %d", wAvail.Code)
		}

		db.First(&updatedUser, authResp.Data.User.ID)
		if updatedUser.Availability != "busy" {
			t.Fatalf("expected availability 'busy', got '%s'", updatedUser.Availability)
		}
	})

	// -------------------------------------------------------------
	// Gap 1 & 2: Feature Persistence & Real Backend Retry
	// -------------------------------------------------------------
	t.Run("Gap1_and_2_Feature_Persistence_And_Backend_Retry", func(t *testing.T) {
		// 1. Create Webhook via API
		hookBody, _ := json.Marshal(map[string]any{
			"url":           "https://test-partner.example.com/webhook",
			"subscriptions": []string{"message_created", "conversation_created"},
		})
		wHook := httptest.NewRecorder()
		reqHook, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/webhooks", accountID), bytes.NewBuffer(hookBody))
		reqHook.Header.Set("Authorization", authHeader)
		reqHook.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wHook, reqHook)
		if wHook.Code != http.StatusCreated {
			t.Fatalf("POST webhooks failed: %d, body: %s", wHook.Code, wHook.Body.String())
		}

		var hookResp struct {
			Data domain.Webhook `json:"data"`
		}
		_ = json.Unmarshal(wHook.Body.Bytes(), &hookResp)
		if hookResp.Data.ID == 0 {
			t.Fatalf("expected created webhook with ID, got %+v", hookResp)
		}

		// 2. Real Webhook Delivery Retry API (POST /webhooks/deliveries/:id/retry)
		wRetry := httptest.NewRecorder()
		reqRetry, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/webhooks/deliveries/1/retry", accountID), nil)
		reqRetry.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wRetry, reqRetry)
		if wRetry.Code != http.StatusOK {
			t.Fatalf("POST webhook delivery retry failed: %d, body: %s", wRetry.Code, wRetry.Body.String())
		}

		var retryResp struct {
			Data struct {
				Success bool   `json:"success"`
				Status  string `json:"status"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wRetry.Body.Bytes(), &retryResp)
		if !retryResp.Data.Success || retryResp.Data.Status != "delivered" {
			t.Fatalf("expected success and delivered status, got %+v", retryResp)
		}

		// Verify real HTTP request was dispatched to httpClient transport
		if receivedWebhookReq == nil {
			t.Fatalf("expected real webhook HTTP request to be dispatched, but receivedWebhookReq was nil")
		}
		if receivedWebhookReq.Header.Get("X-Chatwoot-Event") != "manual.retry" {
			t.Fatalf("expected X-Chatwoot-Event 'manual.retry', got '%s'", receivedWebhookReq.Header.Get("X-Chatwoot-Event"))
		}

		// 3. Edit Webhook via PUT /webhooks/:id
		putHookBody, _ := json.Marshal(map[string]any{
			"url":           "https://test-partner.example.com/webhook-updated",
			"subscriptions": []string{"message_created", "conversation_created", "webwidget_triggered"},
		})
		wPutHook := httptest.NewRecorder()
		reqPutHook, _ := http.NewRequest("PUT", fmt.Sprintf("/api/v1/accounts/%d/webhooks/%d", accountID, hookResp.Data.ID), bytes.NewBuffer(putHookBody))
		reqPutHook.Header.Set("Authorization", authHeader)
		reqPutHook.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPutHook, reqPutHook)
		if wPutHook.Code != http.StatusOK {
			t.Fatalf("PUT webhooks failed: %d, body: %s", wPutHook.Code, wPutHook.Body.String())
		}

		var updatedHook domain.Webhook
		db.Where("account_id = ? AND id = ?", accountID, hookResp.Data.ID).First(&updatedHook)
		if updatedHook.URL != "https://test-partner.example.com/webhook-updated" {
			t.Fatalf("expected updated webhook URL 'https://test-partner.example.com/webhook-updated', got '%s'", updatedHook.URL)
		}

		// 4. Real SLA process API
		wSLA := httptest.NewRecorder()
		reqSLA, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/sla_policies/process", accountID), nil)
		reqSLA.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wSLA, reqSLA)
		if wSLA.Code != http.StatusOK {
			t.Fatalf("POST sla_policies/process failed: %d", wSLA.Code)
		}
	})

	// -------------------------------------------------------------
	// Gap 3: Custom Roles & Permissions Management
	// -------------------------------------------------------------
	t.Run("Gap3_Custom_Roles_Management", func(t *testing.T) {
		// 1. Create Custom Role
		roleBody, _ := json.Marshal(map[string]any{
			"name":        "技术客服 (一线)",
			"description": "处理技术支持业务并调用受控技术能力",
			"permissions": []string{"conversation_manage", "contact_manage"},
		})
		wRole := httptest.NewRecorder()
		reqRole, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/custom_roles", accountID), bytes.NewBuffer(roleBody))
		reqRole.Header.Set("Authorization", authHeader)
		reqRole.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wRole, reqRole)
		if wRole.Code != http.StatusCreated {
			t.Fatalf("POST custom_roles failed: %d, body: %s", wRole.Code, wRole.Body.String())
		}

		var roleResp struct {
			Data domain.CustomRole `json:"data"`
		}
		_ = json.Unmarshal(wRole.Body.Bytes(), &roleResp)
		if roleResp.Data.ID == 0 {
			t.Fatalf("expected custom role with ID, got %+v", roleResp)
		}

		// 2. Edit Custom Role via PUT /custom_roles/:id
		putRoleBody, _ := json.Marshal(map[string]any{
			"name":        "技术客服 (资深)",
			"description": "高级技术支持与权限管理",
			"permissions": []string{"conversation_manage", "contact_manage", "administrator"},
		})
		wPutRole := httptest.NewRecorder()
		reqPutRole, _ := http.NewRequest("PUT", fmt.Sprintf("/api/v1/accounts/%d/custom_roles/%d", accountID, roleResp.Data.ID), bytes.NewBuffer(putRoleBody))
		reqPutRole.Header.Set("Authorization", authHeader)
		reqPutRole.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPutRole, reqPutRole)
		if wPutRole.Code != http.StatusOK {
			t.Fatalf("PUT custom_roles failed: %d, body: %s", wPutRole.Code, wPutRole.Body.String())
		}

		// 3. List Custom Roles
		wListRole := httptest.NewRecorder()
		reqListRole, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/custom_roles", accountID), nil)
		reqListRole.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wListRole, reqListRole)
		if wListRole.Code != http.StatusOK {
			t.Fatalf("GET custom_roles failed: %d", wListRole.Code)
		}

		var listResp struct {
			Data []domain.CustomRole `json:"data"`
		}
		_ = json.Unmarshal(wListRole.Body.Bytes(), &listResp)
		if len(listResp.Data) == 0 || listResp.Data[0].Name != "技术客服 (资深)" {
			t.Fatalf("expected updated custom role, got %+v", listResp.Data)
		}
	})

	// -------------------------------------------------------------
	// New Issues: Invalid JSON Import, Empty Migration & SLA Edit
	// -------------------------------------------------------------
	t.Run("NewIssues_DataImport_Migration_And_Edit_Verification", func(t *testing.T) {
		// 1. Invalid JSON Import -> Expect 400 Bad Request, DB has status=failed, processed_records=0
		invImportBody, _ := json.Marshal(map[string]any{
			"source_provider": "json",
			"import_type":     "contacts",
			"raw_data":        `[{"name": "Bad JSON", "email": "bad@test"`, // Malformed JSON
		})
		wImp := httptest.NewRecorder()
		reqImp, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/data_imports", accountID), bytes.NewBuffer(invImportBody))
		reqImp.Header.Set("Authorization", authHeader)
		reqImp.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wImp, reqImp)
		if wImp.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for invalid JSON import, got %d, body: %s", wImp.Code, wImp.Body.String())
		}

		var lastImp domain.DataImport
		if err := db.Where("account_id = ?", accountID).Order("id DESC").First(&lastImp).Error; err != nil {
			t.Fatalf("failed to query last data import from db: %v", err)
		}
		if lastImp.Status != "failed" || lastImp.ProcessedRecords != 0 {
			t.Fatalf("expected data import status 'failed' with 0 processed records, got status='%s', processed=%d", lastImp.Status, lastImp.ProcessedRecords)
		}

		// 2. Empty Data Migration -> Expect 201 Created with synced_records=0
		emptyMigBody, _ := json.Marshal(map[string]any{
			"source_system": "legacy_chatwoot",
			"job_type":      "contacts",
			"data":          "[]",
		})
		wMig := httptest.NewRecorder()
		reqMig, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/migration_jobs", accountID), bytes.NewBuffer(emptyMigBody))
		reqMig.Header.Set("Authorization", authHeader)
		reqMig.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wMig, reqMig)
		if wMig.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for empty migration job, got %d, body: %s", wMig.Code, wMig.Body.String())
		}

		var migResp struct {
			Data struct {
				ID            uint   `json:"id"`
				SyncedRecords int    `json:"synced_records"`
				Logs          string `json:"logs"`
			} `json:"data"`
		}
		if err := json.Unmarshal(wMig.Body.Bytes(), &migResp); err != nil {
			t.Fatalf("failed to unmarshal migration response: %v, body: %s", err, wMig.Body.String())
		}
		if migResp.Data.SyncedRecords != 0 {
			t.Fatalf("expected synced_records to be 0 for empty migration, got %d", migResp.Data.SyncedRecords)
		}
		if !strings.Contains(migResp.Data.Logs, "No records migrated") {
			t.Fatalf("expected logs to mention 'No records migrated', got '%s'", migResp.Data.Logs)
		}

		// 3. SLA Policy Edit (PUT /sla_policies/:id)
		slaCreateBody, _ := json.Marshal(map[string]any{
			"name":                          "基础服务 SLA",
			"description":                   "默认客诉响应标准",
			"first_response_time_threshold": 3600,
			"resolution_time_threshold":    86400,
		})
		wCreateSLA := httptest.NewRecorder()
		reqCreateSLA, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/sla_policies", accountID), bytes.NewBuffer(slaCreateBody))
		reqCreateSLA.Header.Set("Authorization", authHeader)
		reqCreateSLA.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wCreateSLA, reqCreateSLA)
		if wCreateSLA.Code != http.StatusCreated && wCreateSLA.Code != http.StatusOK {
			t.Fatalf("POST sla_policies failed: %d, body: %s", wCreateSLA.Code, wCreateSLA.Body.String())
		}

		var createdSLAResp struct {
			Data domain.SLAPolicy `json:"data"`
		}
		_ = json.Unmarshal(wCreateSLA.Body.Bytes(), &createdSLAResp)
		if createdSLAResp.Data.ID == 0 {
			t.Fatalf("expected created SLA policy ID, got %+v", createdSLAResp)
		}

		slaPutBody, _ := json.Marshal(map[string]any{
			"name":                          "VIP 加急保障 SLA",
			"description":                   "VIP 客户极速响应通道",
			"first_response_time_threshold": 300,
			"resolution_time_threshold":    1800,
		})
		wPutSLA := httptest.NewRecorder()
		reqPutSLA, _ := http.NewRequest("PUT", fmt.Sprintf("/api/v1/accounts/%d/sla_policies/%d", accountID, createdSLAResp.Data.ID), bytes.NewBuffer(slaPutBody))
		reqPutSLA.Header.Set("Authorization", authHeader)
		reqPutSLA.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPutSLA, reqPutSLA)
		if wPutSLA.Code != http.StatusOK {
			t.Fatalf("PUT sla_policies failed: %d, body: %s", wPutSLA.Code, wPutSLA.Body.String())
		}

		var updatedSLA domain.SLAPolicy
		db.Where("account_id = ? AND id = ?", accountID, createdSLAResp.Data.ID).First(&updatedSLA)
		if updatedSLA.Name != "VIP 加急保障 SLA" || updatedSLA.FirstResponseTimeThreshold != 300 {
			t.Fatalf("expected updated SLA policy name and threshold, got %+v", updatedSLA)
		}
	})

	// -------------------------------------------------------------
	// Gap 5: Live Metrics (Reports Summary)
	// -------------------------------------------------------------
	t.Run("Gap5_Live_Report_Metrics", func(t *testing.T) {
		wRep := httptest.NewRecorder()
		reqRep, _ := http.NewRequest("GET", fmt.Sprintf("/api/v2/accounts/%d/reports/summary", accountID), nil)
		reqRep.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wRep, reqRep)
		if wRep.Code != http.StatusOK {
			t.Fatalf("GET /reports/summary failed: %d, body: %s", wRep.Code, wRep.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Gap 6: Dynamic Conversation & Message Linkage
	// -------------------------------------------------------------
	t.Run("Gap6_Dynamic_Conversation_Messages", func(t *testing.T) {
		// Setup inbox and contact
		inbox := domain.Inbox{
			AccountID:   accountID,
			Name:        "WhatsApp VIP",
			ChannelType: "Channel::Whatsapp",
		}
		db.Create(&inbox)

		contact := domain.Contact{
			AccountID: accountID,
			Name:      "林晓雨",
			Email:     "xiaoyu.lin@example.com",
		}
		db.Create(&contact)

		conv := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			LastActivityAt: time.Now(),
		}
		db.Create(&conv)

		// 1. List Conversations
		wConv := httptest.NewRecorder()
		reqConv, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), nil)
		reqConv.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wConv, reqConv)
		if wConv.Code != http.StatusOK {
			t.Fatalf("GET conversations failed: %d", wConv.Code)
		}

		// 2. Post Message
		msgBody, _ := json.Marshal(map[string]any{
			"content": "您好，已为您核实订单状态，包裹已在配送中。",
			"private": false,
		})
		wMsg := httptest.NewRecorder()
		reqMsg, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, conv.ID), bytes.NewBuffer(msgBody))
		reqMsg.Header.Set("Authorization", authHeader)
		reqMsg.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wMsg, reqMsg)
		if wMsg.Code != http.StatusCreated && wMsg.Code != http.StatusOK {
			t.Fatalf("POST messages failed: %d, body: %s", wMsg.Code, wMsg.Body.String())
		}

		// 3. Get Messages
		wGetMsgs := httptest.NewRecorder()
		reqGetMsgs, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, conv.ID), nil)
		reqGetMsgs.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wGetMsgs, reqGetMsgs)
		if wGetMsgs.Code != http.StatusOK {
			t.Fatalf("GET messages failed: %d", wGetMsgs.Code)
		}
	})

	// -------------------------------------------------------------
	// Gap 7: ActionCable / WebSocket Real-time Integration
	// -------------------------------------------------------------
	t.Run("Gap7_WebSocket_Realtime_Connection", func(t *testing.T) {
		ts := httptest.NewServer(engine)
		defer ts.Close()

		u, _ := url.Parse(ts.URL)
		u.Scheme = "ws"
		u.Path = "/cable"
		q := u.Query()
		q.Set("token", token)
		q.Set("account_id", fmt.Sprintf("%d", accountID))
		u.RawQuery = q.Encode()

		// Connect to WebSocket
		wsConn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			if strings.Contains(err.Error(), "operation not permitted") || strings.Contains(err.Error(), "network is unreachable") {
				t.Skipf("skipping live websocket dial due to sandbox network restrictions: %v", err)
				return
			}
			t.Fatalf("WebSocket connection failed: %v", err)
		}
		defer wsConn.Close()
		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("expected status 101 Switching Protocols, got %d", resp.StatusCode)
		}

		// Broadcast an event to Hub
		hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      accountID,
			ConversationID: 10842,
			Data: map[string]any{
				"content": "包裹已在配送中，预计今晚送达",
			},
		})

		// Read event from WebSocket
		_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msgBytes, err := wsConn.ReadMessage()
		if err != nil {
			t.Fatalf("failed to receive WebSocket broadcast message: %v", err)
		}

		if !strings.Contains(string(msgBytes), "message.created") || !strings.Contains(string(msgBytes), "包裹已在配送中") {
			t.Fatalf("unexpected message payload from ws: %s", string(msgBytes))
		}
	})

	// -------------------------------------------------------------
	// Frontend CRUD & Persistence Verification
	// -------------------------------------------------------------
	t.Run("Frontend_CRUD_Persistence_Verification", func(t *testing.T) {
		// 1. Company CRUD
		compBody, _ := json.Marshal(map[string]any{
			"name":        "清屿生活科技有限公司",
			"domain":      "qingyu.example.com",
			"description": "新零售重点客户",
		})
		wCreateComp := httptest.NewRecorder()
		reqCreateComp, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/companies", accountID), bytes.NewBuffer(compBody))
		reqCreateComp.Header.Set("Authorization", authHeader)
		reqCreateComp.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wCreateComp, reqCreateComp)
		if wCreateComp.Code != http.StatusCreated && wCreateComp.Code != http.StatusOK {
			t.Fatalf("POST companies failed: %d, body: %s", wCreateComp.Code, wCreateComp.Body.String())
		}

		var compResp struct {
			Data struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wCreateComp.Body.Bytes(), &compResp)
		if compResp.Data.ID == 0 {
			t.Fatalf("expected non-zero company ID, got: %s", wCreateComp.Body.String())
		}

		// Company List Check
		wListComp := httptest.NewRecorder()
		reqListComp, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/companies", accountID), nil)
		reqListComp.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wListComp, reqListComp)
		if wListComp.Code != http.StatusOK {
			t.Fatalf("GET companies failed: %d", wListComp.Code)
		}
		if !strings.Contains(wListComp.Body.String(), "清屿生活科技有限公司") {
			t.Fatalf("expected list to contain company name, got: %s", wListComp.Body.String())
		}

		// Company Edit via PUT
		updateCompBody, _ := json.Marshal(map[string]any{
			"name":        "清屿生活集团",
			"domain":      "qingyu-group.example.com",
			"description": "战略升级客户",
		})
		wPutComp := httptest.NewRecorder()
		reqPutComp, _ := http.NewRequest("PUT", fmt.Sprintf("/api/v1/accounts/%d/companies/%d", accountID, compResp.Data.ID), bytes.NewBuffer(updateCompBody))
		reqPutComp.Header.Set("Authorization", authHeader)
		reqPutComp.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPutComp, reqPutComp)
		if wPutComp.Code != http.StatusOK {
			t.Fatalf("PUT companies failed: %d, body: %s", wPutComp.Code, wPutComp.Body.String())
		}

		var dbComp domain.Company
		if err := db.Where("account_id = ? AND id = ?", accountID, compResp.Data.ID).First(&dbComp).Error; err != nil {
			t.Fatalf("failed to query updated company from db: %v", err)
		}
		if dbComp.Name != "清屿生活集团" {
			t.Fatalf("expected company name '清屿生活集团', got '%s'", dbComp.Name)
		}

		// 2. Canned Response CRUD
		cannedBody, _ := json.Marshal(map[string]any{
			"short_code": "/delay_info",
			"content":    "您的包裹目前处于派送中，请耐心等待。",
		})
		wCanned := httptest.NewRecorder()
		reqCanned, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/canned_responses", accountID), bytes.NewBuffer(cannedBody))
		reqCanned.Header.Set("Authorization", authHeader)
		reqCanned.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wCanned, reqCanned)
		if wCanned.Code != http.StatusCreated && wCanned.Code != http.StatusOK {
			t.Fatalf("POST canned_responses failed: %d, body: %s", wCanned.Code, wCanned.Body.String())
		}

		// 3. Custom Attribute Definition CRUD
		attrBody, _ := json.Marshal(map[string]any{
			"attribute_display_name": "会员积分等级",
			"attribute_key":          "vip_points_tier",
			"attribute_model":        "contact_attribute",
			"attribute_display_type": "text",
		})
		wAttr := httptest.NewRecorder()
		reqAttr, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions", accountID), bytes.NewBuffer(attrBody))
		reqAttr.Header.Set("Authorization", authHeader)
		reqAttr.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAttr, reqAttr)
		if wAttr.Code != http.StatusCreated && wAttr.Code != http.StatusOK {
			t.Fatalf("POST custom_attribute_definitions failed: %d, body: %s", wAttr.Code, wAttr.Body.String())
		}

		// Verify in DB
		var dbAttr domain.CustomAttributeDefinition
		if err := db.Where("account_id = ? AND attribute_key = ?", accountID, "vip_points_tier").First(&dbAttr).Error; err != nil {
			t.Fatalf("custom attribute not found in DB: %v", err)
		}
	})
}

