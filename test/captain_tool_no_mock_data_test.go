package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestCaptainToolNoMockDataAndAIUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_jwt_secret_no_mock_data_32b!",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Register user
	signUpBody := map[string]string{
		"email":        "nomock_tester@example.com",
		"password":     "Password123!",
		"name":         "NoMock Tester",
		"account_name": "No Mock Business Workspace",
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

	// -------------------------------------------------------------------------
	// 1. query_order on non-existent order MUST FAIL and NOT auto-create "张三"
	// -------------------------------------------------------------------------
	t.Run("1. query_order on non-existent order fails and does not create mock order", func(t *testing.T) {
		toolDef := map[string]any{
			"name":             "query_order",
			"title":            "查询订单",
			"description":      "查询真实订单",
			"tool_type":        "internal_function",
			"permission_level": "readonly",
			"input_schema":     `{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`,
		}
		wTool := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), toolDef)
		if wTool.Code != http.StatusOK {
			t.Fatalf("create tool failed: %d %s", wTool.Code, wTool.Body.String())
		}
		var createdTool struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool.Body.Bytes(), &createdTool)

		// Test executing with a non-existent order
		nonExistentID := "ORD-GHOST-404"
		testReq := map[string]any{
			"params": map[string]any{"order_id": nonExistentID},
		}
		wExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdTool.Data.ID), testReq)
		if wExec.Code != http.StatusOK {
			t.Fatalf("unexpected HTTP code: %d", wExec.Code)
		}

		var execResp struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wExec.Body.Bytes(), &execResp)
		if execResp.Data.Status != "failed" {
			t.Fatalf("expected status 'failed' for non-existent order, got '%s'", execResp.Data.Status)
		}
		if execResp.Data.Result["success"] == true {
			t.Fatalf("expected result.success false for non-existent order")
		}

		// Verify database does NOT have this order
		var count int64
		db.Model(&domain.Order{}).Where("account_id = ? AND order_id = ?", accountID, nonExistentID).Count(&count)
		if count != 0 {
			t.Fatalf("expected 0 orders in database for non-existent order, got %d", count)
		}
	})

	// -------------------------------------------------------------------------
	// 2. update_shipping_address on non-existent order MUST FAIL
	// -------------------------------------------------------------------------
	t.Run("2. update_shipping_address on non-existent order fails and does not forge order", func(t *testing.T) {
		toolDef := map[string]any{
			"name":                  "update_shipping_address",
			"title":                 "修改地址",
			"description":           "修改地址",
			"tool_type":             "internal_function",
			"permission_level":      "write_confirm",
			"requires_confirmation": true,
			"input_schema":          `{"type":"object","properties":{"order_id":{"type":"string"},"new_address":{"type":"string"}},"required":["order_id","new_address"]}`,
		}
		wTool := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), toolDef)
		var createdTool struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool.Body.Bytes(), &createdTool)

		testReq := map[string]any{
			"params": map[string]any{
				"order_id":    "ORD-NONEXISTENT-ADDR",
				"new_address": "广州市天河区珠江新城金穗路1号",
			},
			"confirm_execute": true,
		}
		wExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdTool.Data.ID), testReq)
		var execResp struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wExec.Body.Bytes(), &execResp)
		if execResp.Data.Status != "failed" {
			t.Fatalf("expected status 'failed', got '%s'", execResp.Data.Status)
		}
		if execResp.Data.Result["success"] == true {
			t.Fatalf("expected result.success false")
		}

		// Also test /captain/tools/execute directly
		wDirect := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/execute", accountID), map[string]any{
			"tool_name": "update_shipping_address",
			"params": map[string]any{
				"order_id":    "ORD-NONEXISTENT-DIRECT",
				"new_address": "广州市天河区",
			},
		})
		if wDirect.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for updating non-existent order, got %d", wDirect.Code)
		}
	})

	// -------------------------------------------------------------------------
	// 3. lookup_logistics on non-existent order MUST FAIL and NOT return fake 顺丰/李师傅
	// -------------------------------------------------------------------------
	t.Run("3. lookup_logistics on non-existent tracking fails without fake checkpoints", func(t *testing.T) {
		toolDef := map[string]any{
			"name":             "lookup_logistics",
			"title":            "查询物流",
			"description":      "查询物流详情",
			"tool_type":        "internal_function",
			"permission_level": "readonly",
			"input_schema":     `{"type":"object"}`,
		}
		wTool := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accountID), toolDef)
		var createdTool struct {
			Data domain.AICustomTool `json:"data"`
		}
		_ = json.Unmarshal(wTool.Body.Bytes(), &createdTool)

		testReq := map[string]any{
			"params": map[string]any{
				"tracking_number": "SF-NONEXISTENT-TRACKING",
			},
		}
		wExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/%d/test", accountID, createdTool.Data.ID), testReq)
		var execResp struct {
			Data struct {
				Status string         `json:"status"`
				Result map[string]any `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wExec.Body.Bytes(), &execResp)
		if execResp.Data.Status != "failed" {
			t.Fatalf("expected status 'failed', got '%s'", execResp.Data.Status)
		}

		// Also test /captain/tools/execute directly
		wDirect := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/captain/tools/execute", accountID), map[string]any{
			"tool_name": "lookup_logistics",
			"params":    map[string]any{"tracking_number": "SF-NONEXISTENT-TRACKING"},
		})
		if wDirect.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for looking up non-existent tracking, got %d", wDirect.Code)
		}
	})

	// -------------------------------------------------------------------------
	// 4. AI Provider unconfigured returns ErrModelNotConfigured and HTTP 422
	// -------------------------------------------------------------------------
	t.Run("4. AI unconfigured returns ErrModelNotConfigured and HTTP 422", func(t *testing.T) {
		// Calling GetAIProvider without configuration
		prov := service.GetAIProvider("", "", "")
		resp, err := prov.GenerateCompletion(context.Background(), service.AICompletionRequest{
			Model: "unknown-model",
			Messages: []service.AIMessage{
				{Role: "user", Content: "hello"},
			},
		})
		if err == nil {
			t.Fatalf("expected error when AI provider is unconfigured, got nil (content=%v)", resp)
		}
		if !errors.Is(err, service.ErrModelNotConfigured) {
			t.Fatalf("expected ErrModelNotConfigured, got: %v", err)
		}

		// HTTP POST /copilot/completions without configured model returns 422
		wComp := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/copilot/completions", accountID), map[string]any{
			"provider": "unconfigured_provider",
			"model":    "custom_model",
			"prompt":   "写一首诗",
		})
		if wComp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 UnprocessableEntity for unconfigured AI provider, got %d %s", wComp.Code, wComp.Body.String())
		}
	})
}
