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
	"github.com/gin-gonic/gin"
)

func TestCaptainToolRealBusiness(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_jwt_secret_captain_real_biz_32b!",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up user & account
	signUpBody := map[string]string{
		"email":        "captain_tester@example.com",
		"password":     "Password123!",
		"name":         "Captain Tester",
		"account_name": "Captain Business Lab",
	}
	bUp, _ := json.Marshal(signUpBody)
	wUp := httptest.NewRecorder()
	r.ServeHTTP(wUp, httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bUp)))
	if wUp.Code != http.StatusCreated && wUp.Code != http.StatusOK {
		t.Fatalf("sign up failed: %d %s", wUp.Code, wUp.Body.String())
	}

	var signUpResp struct {
		Data struct {
			Token string `json:"token"`
			User  domain.User
		} `json:"data"`
	}
	_ = json.Unmarshal(wUp.Body.Bytes(), &signUpResp)
	token := signUpResp.Data.Token
	accountID := uint(1)

	authReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var bodyReader *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(b)
		} else {
			bodyReader = bytes.NewReader([]byte{})
		}
		req := httptest.NewRequest(method, path, bodyReader)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Create a test conversation to test linkage
	conv := domain.Conversation{
		AccountID: accountID,
		DisplayID: 101,
		InboxID:   1,
		ContactID: 1,
		Status:    "open",
	}
	_ = db.Create(&conv)

	// ----------------------------------------------------------------------
	// Test 1: Query Order - creates/retrieves real persistent domain.Order
	// ----------------------------------------------------------------------
	t.Run("1. Query Order initializes and returns real database order", func(t *testing.T) {
		toolDef := map[string]any{
			"name":             "query_order",
			"title":            "查询订单",
			"description":      "查询客户真实订单",
			"tool_type":        "internal_function",
			"permission_level": "readonly",
			"input_schema": `{
				"type": "object",
				"properties": {
					"order_id": {"type": "string"}
				},
				"required": ["order_id"]
			}`,
		}
		wTool := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), toolDef)
		if wTool.Code != http.StatusOK {
			t.Fatalf("create query_order tool failed: %d %s", wTool.Code, wTool.Body.String())
		}
		var createdTool struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool.Body.Bytes(), &createdTool)

		// Seed a real database order for ORD-REAL-9999
		initialOrder := domain.Order{
			AccountID:       accountID,
			OrderID:         "ORD-REAL-9999",
			CustomerName:    "真实客户李四",
			CustomerEmail:   "lisi@example.com",
			CustomerPhone:   "+86 13900139000",
			AmountYuan:      399.00,
			OrderStatus:     "shipped",
			ShippingAddress: "北京市海淀区中关村南大街1号",
			Carrier:         "顺丰速运",
			TrackingNumber:  "SF1982736421",
			WarehouseSynced: false,
			ConversationID:  &conv.ID,
		}
		if err := db.Create(&initialOrder).Error; err != nil {
			t.Fatalf("failed to seed test order: %v", err)
		}

		// Test executing query_order
		testReq := map[string]any{
			"params": map[string]any{
				"order_id": "ORD-REAL-9999",
			},
			"conversation_id": conv.ID,
		}
		wExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdTool.Data.ID), testReq)
		if wExec.Code != http.StatusOK {
			t.Fatalf("test query_order failed: %d %s", wExec.Code, wExec.Body.String())
		}

		var execResp struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wExec.Body.Bytes(), &execResp)
		if execResp.Data.Status != "success" {
			t.Fatalf("expected status success, got %s", execResp.Data.Status)
		}
		if execResp.Data.Result["order_id"] != "ORD-REAL-9999" {
			t.Fatalf("expected order_id ORD-REAL-9999, got %v", execResp.Data.Result["order_id"])
		}

		// Verify database row exists!
		var dbOrder domain.Order
		if err := db.Where("account_id = ? AND order_id = ?", accountID, "ORD-REAL-9999").First(&dbOrder).Error; err != nil {
			t.Fatalf("expected order ORD-REAL-9999 to be persisted in database, but got err: %v", err)
		}
		if dbOrder.AmountYuan != 399.00 {
			t.Errorf("expected amount 399, got %f", dbOrder.AmountYuan)
		}
		if dbOrder.WarehouseSynced {
			t.Errorf("expected warehouse_synced to be false initially")
		}
	})

	// ----------------------------------------------------------------------
	// Test 2: Modify Address - truly modifies database order & syncs warehouse
	// ----------------------------------------------------------------------
	t.Run("2. Modify Address actually updates database order and sets warehouse_synced", func(t *testing.T) {
		toolDef := map[string]any{
			"name":                  "update_shipping_address",
			"title":                 "修改收货地址",
			"description":           "变更配送收货地址",
			"tool_type":             "internal_function",
			"permission_level":      "write_confirm",
			"requires_confirmation": true,
			"input_schema": `{
				"type": "object",
				"properties": {
					"order_id": {"type": "string"},
					"new_address": {"type": "string"}
				},
				"required": ["order_id", "new_address"]
			}`,
		}
		wTool := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), toolDef)
		if wTool.Code != http.StatusOK {
			t.Fatalf("create update_shipping_address tool failed: %d %s", wTool.Code, wTool.Body.String())
		}
		var createdTool struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool.Body.Bytes(), &createdTool)

		newAddress := "广东省深圳市南山区粤海街道高新南一道88号T3栋12层"
		testReq := map[string]any{
			"params": map[string]any{
				"order_id":    "ORD-REAL-9999",
				"new_address": newAddress,
			},
			"confirm_execute": true,
			"conversation_id": conv.ID,
		}
		wExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdTool.Data.ID), testReq)
		if wExec.Code != http.StatusOK {
			t.Fatalf("test update_shipping_address failed: %d %s", wExec.Code, wExec.Body.String())
		}

		var execResp struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wExec.Body.Bytes(), &execResp)
		if execResp.Data.Status != "success" {
			t.Fatalf("expected status success, got %s", execResp.Data.Status)
		}
		if execResp.Data.Result["sync_status"] != "synced_to_warehouse" {
			t.Errorf("expected sync_status synced_to_warehouse, got %v", execResp.Data.Result["sync_status"])
		}

		// Verify database order was ACTUALLY MODIFIED
		var updatedOrder domain.Order
		if err := db.Where("account_id = ? AND order_id = ?", accountID, "ORD-REAL-9999").First(&updatedOrder).Error; err != nil {
			t.Fatalf("failed to query updated order from db: %v", err)
		}
		if updatedOrder.ShippingAddress != newAddress {
			t.Fatalf("expected db order address to be '%s', got '%s'", newAddress, updatedOrder.ShippingAddress)
		}
		if !updatedOrder.WarehouseSynced {
			t.Fatalf("expected db order warehouse_synced to be true")
		}
		if updatedOrder.SyncedAt == nil {
			t.Fatalf("expected synced_at timestamp to be set")
		}

		// Verify activity note message was recorded on conversation
		var msgCount int64
		db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", accountID, conv.ID).Count(&msgCount)
		if msgCount == 0 {
			t.Errorf("expected conversation activity message to be created for address update")
		}

		// Now query again via query_order tool and check that it reflects the new address
		qReq := map[string]any{
			"params": map[string]any{
				"order_id": "ORD-REAL-9999",
			},
		}
		wQ := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/execute", accountID), map[string]any{
			"tool_name": "order_lookup",
			"params":    qReq["params"],
		})
		if wQ.Code != http.StatusOK {
			t.Fatalf("order_lookup failed: %d %s", wQ.Code, wQ.Body.String())
		}
		var qResp struct {
			Data struct {
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wQ.Body.Bytes(), &qResp)
		if qResp.Data.Result["shipping_address"] != newAddress {
			t.Fatalf("expected query to return updated address '%s', got '%v'", newAddress, qResp.Data.Result["shipping_address"])
		}
		if qResp.Data.Result["warehouse_synced"] != true {
			t.Fatalf("expected warehouse_synced true from order_lookup")
		}
	})

	// ----------------------------------------------------------------------
	// Test 3: Create Ticket - creates real domain.Ticket in database
	// ----------------------------------------------------------------------
	t.Run("3. Create Ticket persists real Ticket record in database", func(t *testing.T) {
		toolDef := map[string]any{
			"name":             "create_ticket",
			"title":            "创建工单",
			"description":      "创建售后服务工单",
			"tool_type":        "internal_function",
			"permission_level": "readonly",
			"input_schema": `{
				"type": "object",
				"properties": {
					"title": {"type": "string"},
					"description": {"type": "string"}
				},
				"required": ["title"]
			}`,
		}
		wTool := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), toolDef)
		if wTool.Code != http.StatusOK {
			t.Fatalf("create ticket tool failed: %d %s", wTool.Code, wTool.Body.String())
		}
		var createdTool struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool.Body.Bytes(), &createdTool)

		testReq := map[string]any{
			"params": map[string]any{
				"title":       "包裹破损漏液索赔申请",
				"description": "客户反馈收到的耳机外包装严重挤压变形且有液体浸湿，申请重新补发。",
				"priority":    "urgent",
			},
			"conversation_id": conv.ID,
		}
		wExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdTool.Data.ID), testReq)
		if wExec.Code != http.StatusOK {
			t.Fatalf("test create_ticket failed: %d %s", wExec.Code, wExec.Body.String())
		}

		var execResp struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wExec.Body.Bytes(), &execResp)
		if execResp.Data.Status != "success" {
			t.Fatalf("expected status success, got %s", execResp.Data.Status)
		}
		ticketID, ok := execResp.Data.Result["ticket_id"].(string)
		if !ok || ticketID == "" {
			t.Fatalf("expected ticket_id in result, got %v", execResp.Data.Result["ticket_id"])
		}

		// Verify database Ticket record
		var dbTicket domain.Ticket
		if err := db.Where("account_id = ? AND ticket_number = ?", accountID, ticketID).First(&dbTicket).Error; err != nil {
			t.Fatalf("expected ticket %s to be saved in database, got err: %v", ticketID, err)
		}
		if dbTicket.Title != "包裹破损漏液索赔申请" {
			t.Errorf("expected title '包裹破损漏液索赔申请', got '%s'", dbTicket.Title)
		}
		if dbTicket.Priority != "urgent" {
			t.Errorf("expected priority 'urgent', got '%s'", dbTicket.Priority)
		}
		if dbTicket.ConversationID == nil || *dbTicket.ConversationID != conv.ID {
			t.Errorf("expected linked conversation_id %d", conv.ID)
		}
	})

	// ----------------------------------------------------------------------
	// Test 4: Tools without Endpoint - MUST FAIL instead of succeeding blindly
	// ----------------------------------------------------------------------
	t.Run("4. Tools without Endpoint fail with clear error and do not falsely succeed", func(t *testing.T) {
		// 4A. Webhook tool with empty endpoint
		webhookTool := map[string]any{
			"name":             "external_crm_sync",
			"title":            "同步外部CRM",
			"description":      "Webhook同步外部系统",
			"tool_type":        "webhook",
			"endpoint_url":     "", // missing endpoint
			"permission_level": "readonly",
			"input_schema": `{
				"type": "object",
				"properties": {
					"user_id": {"type": "string"}
				},
				"required": ["user_id"]
			}`,
		}
		wTool1 := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), webhookTool)
		if wTool1.Code != http.StatusOK {
			t.Fatalf("create webhook tool failed: %d %s", wTool1.Code, wTool1.Body.String())
		}
		var createdWTool struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool1.Body.Bytes(), &createdWTool)

		// Executing webhook tool with no endpoint must FAIL
		wTest1 := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdWTool.Data.ID), map[string]any{
			"params": map[string]any{"user_id": "12345"},
		})
		if wTest1.Code != http.StatusOK {
			t.Fatalf("endpoint test returned unexpected HTTP error: %d", wTest1.Code)
		}
		var testResp1 struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wTest1.Body.Bytes(), &testResp1)

		if testResp1.Data.Status != domain.AIToolExecutionFailed {
			t.Fatalf("expected status '%s' for tool without endpoint, got '%s'", domain.AIToolExecutionFailed, testResp1.Data.Status)
		}
		if testResp1.Data.Result["success"] == true {
			t.Fatalf("expected result.success to be false for tool without endpoint")
		}

		// 4B. Unrecognized internal tool name without endpoint
		unrecTool := map[string]any{
			"name":             "nonexistent_plugin_xyz",
			"title":            "未知插件",
			"description":      "未知内部工具",
			"tool_type":        "internal_function",
			"endpoint_url":     "",
			"permission_level": "readonly",
			"input_schema":     `{"type": "object"}`,
		}
		wTool2 := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), unrecTool)
		if wTool2.Code != http.StatusOK {
			t.Fatalf("create unrec tool failed: %d", wTool2.Code)
		}
		var createdUnrec struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool2.Body.Bytes(), &createdUnrec)

		wTest2 := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdUnrec.Data.ID), map[string]any{
			"params": map[string]any{},
		})
		var testResp2 struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wTest2.Body.Bytes(), &testResp2)
		if testResp2.Data.Status != domain.AIToolExecutionFailed {
			t.Fatalf("expected status '%s' for unknown tool without endpoint, got '%s'", domain.AIToolExecutionFailed, testResp2.Data.Status)
		}

		// 4C. Check execution logs endpoint (/captain/tools/execution_logs)
		wLogs := authReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/execution_logs", accountID), nil)
		if wLogs.Code != http.StatusOK {
			t.Fatalf("expected 200 for /captain/tools/execution_logs, got %d %s", wLogs.Code, wLogs.Body.String())
		}
	})

	// ----------------------------------------------------------------------
	// Test 5: /captain/tools/execute rejection on unconfigured tool
	// ----------------------------------------------------------------------
	t.Run("5. /captain/tools/execute rejects unconfigured tool with 400", func(t *testing.T) {
		wExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/execute", accountID), map[string]any{
			"tool_name": "unsupported_missing_tool",
			"params":    map[string]any{"foo": "bar"},
		})
		if wExec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for unconfigured tool execute, got %d %s", wExec.Code, wExec.Body.String())
		}
	})
}
