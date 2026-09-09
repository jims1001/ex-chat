package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAICustomToolLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:ai_tool_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_ai_tool_123",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user to obtain token & account 1
	signUpPayload := map[string]string{
		"name":         "Tool Admin",
		"email":        fmt.Sprintf("tool_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "AI Tool Test Corp",
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
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// 2. Sign up Account 2 (for tenant isolation check)
	signUpPayload2 := map[string]string{
		"name":         "Tenant2 Admin",
		"email":        fmt.Sprintf("tool_tenant2_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Tenant 2 Tool Corp",
	}
	body2, _ := json.Marshal(signUpPayload2)
	req2 := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	var authResp2 struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	token2 := authResp2.Data.Token
	accountID2 := authResp2.Data.Accounts[0].ID

	var queryOrderToolID uint
	var updateAddressToolID uint

	// -------------------------------------------------------------
	// Scenario 1: Tool Definition & Schema Validation
	// -------------------------------------------------------------
	t.Run("Scenario1_Define_and_Validate_Tools", func(t *testing.T) {
		// 1A. Define Readonly Tool: query_order
		tool1 := map[string]any{
			"name":             "query_order",
			"title":            "查询订单详情",
			"description":      "根据客户提供的订单ID查询该订单的当前状态、金额、物流单号及商品列表",
			"category":         "订单插件",
			"tool_type":        "internal_function",
			"permission_level": "readonly",
			"timeout_seconds":  8,
			"input_schema": `{
				"type": "object",
				"properties": {
					"order_id": {"type": "string", "description": "系统唯一订单编号"}
				},
				"required": ["order_id"]
			}`,
		}
		b1, _ := json.Marshal(tool1)
		reqCreate1 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), bytes.NewReader(b1))
		reqCreate1.Header.Set("Authorization", "Bearer "+token)
		reqCreate1.Header.Set("Content-Type", "application/json")
		wCreate1 := httptest.NewRecorder()
		r.ServeHTTP(wCreate1, reqCreate1)

		if wCreate1.Code != http.StatusOK {
			t.Fatalf("POST /captain/tools failed: code=%d body=%s", wCreate1.Code, wCreate1.Body.String())
		}

		var resp1 struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wCreate1.Body.Bytes(), &resp1)
		queryOrderToolID = resp1.Data.ID

		if queryOrderToolID == 0 {
			t.Fatalf("expected valid tool ID, got 0")
		}
		if resp1.Data.Name != "query_order" || resp1.Data.PermissionLevel != "readonly" {
			t.Errorf("unexpected tool payload: %+v", resp1.Data)
		}

		// 1B. Define Write Confirmation Tool: update_shipping_address
		tool2 := map[string]any{
			"name":                  "update_shipping_address",
			"title":                 "修改收货地址",
			"description":           "为未发货或拦截中的订单变更配送地址，需坐席向客户确认后执行",
			"category":              "订单插件",
			"tool_type":             "internal_function",
			"permission_level":      "write_confirm",
			"requires_confirmation": true,
			"timeout_seconds":       12,
			"input_schema": `{
				"type": "object",
				"properties": {
					"order_id": {"type": "string", "description": "订单编号"},
					"new_address": {"type": "string", "description": "新配送地址"}
				},
				"required": ["order_id", "new_address"]
			}`,
		}
		b2, _ := json.Marshal(tool2)
		reqCreate2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), bytes.NewReader(b2))
		reqCreate2.Header.Set("Authorization", "Bearer "+token)
		reqCreate2.Header.Set("Content-Type", "application/json")
		wCreate2 := httptest.NewRecorder()
		r.ServeHTTP(wCreate2, reqCreate2)

		if wCreate2.Code != http.StatusOK {
			t.Fatalf("POST /captain/tools failed for write tool: code=%d body=%s", wCreate2.Code, wCreate2.Body.String())
		}
		var resp2 struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wCreate2.Body.Bytes(), &resp2)
		updateAddressToolID = resp2.Data.ID

		// 1C. Attempt invalid JSON schema -> should be rejected with 400
		invalidTool := map[string]any{
			"name":         "invalid_tool",
			"title":        "非法工具",
			"description":  "测试非法Schema",
			"input_schema": "not a valid json schema {",
		}
		bInv, _ := json.Marshal(invalidTool)
		reqInv := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), bytes.NewReader(bInv))
		reqInv.Header.Set("Authorization", "Bearer "+token)
		reqInv.Header.Set("Content-Type", "application/json")
		wInv := httptest.NewRecorder()
		r.ServeHTTP(wInv, reqInv)

		if wInv.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid json schema, got %d", wInv.Code)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Tool Online Editing & Status Update
	// -------------------------------------------------------------
	t.Run("Scenario2_Tool_Online_Editing", func(t *testing.T) {
		newDesc := "全渠道订单实时状态查询，包含快件轨迹与商品列表"
		newTimeout := 15
		newStatus := domain.AIToolStatusRestricted

		updatePayload := map[string]any{
			"description":     newDesc,
			"timeout_seconds": newTimeout,
			"status":          newStatus,
		}
		uBody, _ := json.Marshal(updatePayload)
		reqUpdate := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d", accountID, queryOrderToolID), bytes.NewReader(uBody))
		reqUpdate.Header.Set("Authorization", "Bearer "+token)
		reqUpdate.Header.Set("Content-Type", "application/json")
		wUpdate := httptest.NewRecorder()
		r.ServeHTTP(wUpdate, reqUpdate)

		if wUpdate.Code != http.StatusOK {
			t.Fatalf("PUT /captain/tools/:id failed: code=%d body=%s", wUpdate.Code, wUpdate.Body.String())
		}

		// Get Tool and verify
		reqGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d", accountID, queryOrderToolID), nil)
		reqGet.Header.Set("Authorization", "Bearer "+token)
		wGet := httptest.NewRecorder()
		r.ServeHTTP(wGet, reqGet)

		var getResp struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wGet.Body.Bytes(), &getResp)

		if getResp.Data.Description != newDesc {
			t.Errorf("expected updated description, got '%s'", getResp.Data.Description)
		}
		if getResp.Data.TimeoutSeconds != newTimeout {
			t.Errorf("expected timeout %d, got %d", newTimeout, getResp.Data.TimeoutSeconds)
		}
		if getResp.Data.Status != newStatus {
			t.Errorf("expected status '%s', got '%s'", newStatus, getResp.Data.Status)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Tool Testing & Simulation Engine (Test Tool)
	// -------------------------------------------------------------
	t.Run("Scenario3_Tool_Testing_Simulation", func(t *testing.T) {
		// 3A. Test with valid parameter
		testPayloadValid := map[string]any{
			"params": map[string]any{
				"order_id": "ORD-2026-9988",
			},
		}
		tvBody, _ := json.Marshal(testPayloadValid)
		reqTestValid := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, queryOrderToolID), bytes.NewReader(tvBody))
		reqTestValid.Header.Set("Authorization", "Bearer "+token)
		reqTestValid.Header.Set("Content-Type", "application/json")
		wTestValid := httptest.NewRecorder()
		r.ServeHTTP(wTestValid, reqTestValid)

		if wTestValid.Code != http.StatusOK {
			t.Fatalf("POST /captain/tools/:id/test valid failed: code=%d body=%s", wTestValid.Code, wTestValid.Body.String())
		}

		var testResp struct {
			Data struct {
				Status          string `json:"status"`
				ToolName        string `json:"tool_name"`
				Result          gin.H  `json:"result"`
				LatencyMs       int64  `json:"latency_ms"`
				SchemaValidated bool   `json:"schema_validated"`
				LogID           uint   `json:"log_id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wTestValid.Body.Bytes(), &testResp)

		if testResp.Data.Status != "success" {
			t.Errorf("expected status 'success', got '%s'", testResp.Data.Status)
		}
		if !testResp.Data.SchemaValidated {
			t.Errorf("expected SchemaValidated to be true")
		}
		if testResp.Data.LogID == 0 {
			t.Errorf("expected generated execution log ID")
		}
		if testResp.Data.Result["order_id"] != "ORD-2026-9988" {
			t.Errorf("expected result order_id ORD-2026-9988, got %v", testResp.Data.Result["order_id"])
		}

		// 3B. Test with invalid parameter (missing required order_id)
		testPayloadInvalid := map[string]any{
			"params": map[string]any{},
		}
		tiBody, _ := json.Marshal(testPayloadInvalid)
		reqTestInv := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, queryOrderToolID), bytes.NewReader(tiBody))
		reqTestInv.Header.Set("Authorization", "Bearer "+token)
		reqTestInv.Header.Set("Content-Type", "application/json")
		wTestInv := httptest.NewRecorder()
		r.ServeHTTP(wTestInv, reqTestInv)

		if wTestInv.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for missing required parameter in test, got %d", wTestInv.Code)
		}
		if !strings.Contains(wTestInv.Body.String(), "order_id") {
			t.Errorf("expected error message to mention order_id, got %s", wTestInv.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: Write Confirmation Tool & Execution Logs
	// -------------------------------------------------------------
	t.Run("Scenario4_Write_Confirmation_and_Logs", func(t *testing.T) {
		// 4A. Run write tool without confirm_execute -> should prompt for confirmation
		testWriteNoConfirm := map[string]any{
			"params": map[string]any{
				"order_id":    "ORD-8848",
				"new_address": "北京市海淀区中关村南大街1号",
			},
			"confirm_execute": false,
		}
		bNoConf, _ := json.Marshal(testWriteNoConfirm)
		reqNoConf := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, updateAddressToolID), bytes.NewReader(bNoConf))
		reqNoConf.Header.Set("Authorization", "Bearer "+token)
		reqNoConf.Header.Set("Content-Type", "application/json")
		wNoConf := httptest.NewRecorder()
		r.ServeHTTP(wNoConf, reqNoConf)

		if wNoConf.Code != http.StatusOK {
			t.Fatalf("POST /captain/tools/:id/test without confirmation failed: code=%d", wNoConf.Code)
		}

		var promptResp struct {
			Status               string `json:"status"`
			RequiresConfirmation bool   `json:"requires_confirmation"`
			Message              string `json:"message"`
		}
		_ = json.Unmarshal(wNoConf.Body.Bytes(), &promptResp)

		if promptResp.Status != "requires_confirmation" || !promptResp.RequiresConfirmation {
			t.Errorf("expected status 'requires_confirmation', got %s", promptResp.Status)
		}

		// 4B. Run with confirm_execute = true -> succeeds
		testWriteConfirmed := map[string]any{
			"params": map[string]any{
				"order_id":    "ORD-8848",
				"new_address": "上海市浦东新区张江高科技园区88号",
			},
			"confirm_execute": true,
		}
		bConf, _ := json.Marshal(testWriteConfirmed)
		reqConf := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, updateAddressToolID), bytes.NewReader(bConf))
		reqConf.Header.Set("Authorization", "Bearer "+token)
		reqConf.Header.Set("Content-Type", "application/json")
		wConf := httptest.NewRecorder()
		r.ServeHTTP(wConf, reqConf)

		if wConf.Code != http.StatusOK {
			t.Fatalf("POST /captain/tools/:id/test with confirmation failed: code=%d body=%s", wConf.Code, wConf.Body.String())
		}

		// 4C. Check execution logs endpoint
		reqLogs := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/logs", accountID, updateAddressToolID), nil)
		reqLogs.Header.Set("Authorization", "Bearer "+token)
		wLogs := httptest.NewRecorder()
		r.ServeHTTP(wLogs, reqLogs)

		if wLogs.Code != http.StatusOK {
			t.Fatalf("GET /captain/tools/:id/logs failed: code=%d body=%s", wLogs.Code, wLogs.Body.String())
		}

		var logsResp struct {
			Data struct {
				Logs []domain.AIToolExecutionLog `json:"logs"`
				Meta struct {
					Total int `json:"total"`
				} `json:"meta"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wLogs.Body.Bytes(), &logsResp)

		if len(logsResp.Data.Logs) < 2 {
			t.Errorf("expected at least 2 logs (rejected and confirmed), got %d", len(logsResp.Data.Logs))
		}
	})

	// -------------------------------------------------------------
	// Scenario 5: Filtering & Metrics Dashboard
	// -------------------------------------------------------------
	t.Run("Scenario5_Filtering_and_Metrics", func(t *testing.T) {
		// 5A. Filter by permission_level=readonly
		reqFilter := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/captain/tools?permission_level=readonly", accountID), nil)
		reqFilter.Header.Set("Authorization", "Bearer "+token)
		wFilter := httptest.NewRecorder()
		r.ServeHTTP(wFilter, reqFilter)

		if wFilter.Code != http.StatusOK {
			t.Fatalf("GET /captain/tools filtered failed: code=%d", wFilter.Code)
		}

		var filterResp struct {
			Data struct {
				Tools []domain.AICustomTool `json:"tools"`
				Meta  struct {
					Total int `json:"total"`
				} `json:"meta"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wFilter.Body.Bytes(), &filterResp)

		if filterResp.Data.Meta.Total < 1 {
			t.Errorf("expected at least 1 readonly tool")
		}

		// 5B. Metrics API
		reqMetrics := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/metrics", accountID), nil)
		reqMetrics.Header.Set("Authorization", "Bearer "+token)
		wMetrics := httptest.NewRecorder()
		r.ServeHTTP(wMetrics, reqMetrics)

		if wMetrics.Code != http.StatusOK {
			t.Fatalf("GET /captain/tools/metrics failed: code=%d body=%s", wMetrics.Code, wMetrics.Body.String())
		}

		var metricsResp struct {
			Data repository.AIToolMetrics `json:"data"`
		}
		_ = json.Unmarshal(wMetrics.Body.Bytes(), &metricsResp)

		if metricsResp.Data.TotalTools < 2 {
			t.Errorf("expected at least 2 total tools, got %d", metricsResp.Data.TotalTools)
		}
		if metricsResp.Data.ReadonlyTools < 1 {
			t.Errorf("expected at least 1 readonly tool, got %d", metricsResp.Data.ReadonlyTools)
		}
		if metricsResp.Data.WriteTools < 1 {
			t.Errorf("expected at least 1 write tool, got %d", metricsResp.Data.WriteTools)
		}
		if metricsResp.Data.TodayCalls < 2 {
			t.Errorf("expected at least 2 calls today, got %d", metricsResp.Data.TodayCalls)
		}
		if metricsResp.Data.SuccessRate == "" {
			t.Errorf("expected non-empty success rate")
		}
	})

	// -------------------------------------------------------------
	// Scenario 6: Multi-tenant Security Isolation & Deletion
	// -------------------------------------------------------------
	t.Run("Scenario6_Tenant_Isolation_and_Deletion", func(t *testing.T) {
		// 6A. Tenant 2 tries to access Tenant 1's tool -> 404
		reqCrossGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d", accountID2, queryOrderToolID), nil)
		reqCrossGet.Header.Set("Authorization", "Bearer "+token2)
		wCrossGet := httptest.NewRecorder()
		r.ServeHTTP(wCrossGet, reqCrossGet)

		if wCrossGet.Code != http.StatusNotFound {
			t.Errorf("expected 404 for cross-tenant tool access, got %d", wCrossGet.Code)
		}

		// Tenant 2 tries to test Tenant 1's tool -> 404
		reqCrossTest := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID2, queryOrderToolID), bytes.NewReader([]byte(`{"params":{"order_id":"test"}}`)))
		reqCrossTest.Header.Set("Authorization", "Bearer "+token2)
		reqCrossTest.Header.Set("Content-Type", "application/json")
		wCrossTest := httptest.NewRecorder()
		r.ServeHTTP(wCrossTest, reqCrossTest)

		if wCrossTest.Code != http.StatusNotFound {
			t.Errorf("expected 404 for cross-tenant tool test, got %d", wCrossTest.Code)
		}

		// 6B. Delete tool by owner Account 1
		reqDel := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d", accountID, queryOrderToolID), nil)
		reqDel.Header.Set("Authorization", "Bearer "+token)
		wDel := httptest.NewRecorder()
		r.ServeHTTP(wDel, reqDel)

		if wDel.Code != http.StatusOK {
			t.Fatalf("DELETE /captain/tools/:id failed: code=%d body=%s", wDel.Code, wDel.Body.String())
		}

		// Verify tool is gone
		reqAfter := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d", accountID, queryOrderToolID), nil)
		reqAfter.Header.Set("Authorization", "Bearer "+token)
		wAfter := httptest.NewRecorder()
		r.ServeHTTP(wAfter, reqAfter)

		if wAfter.Code != http.StatusNotFound {
			t.Errorf("expected 404 after tool deletion, got %d", wAfter.Code)
		}
	})
}
