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
)

func TestCustomFiltersManagement(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:custom_filters_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "custom-filters-jwt-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up Account A
	signUpPayloadA := map[string]string{
		"account_name": "Filter Corp A",
		"name":         "Filter Admin A",
		"email":        "admin@filter-a.com",
		"password":     "Password123!",
	}
	bodyA, _ := json.Marshal(signUpPayloadA)
	reqA := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyA))
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	r.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusCreated {
		t.Fatalf("sign up account A failed: code=%d body=%s", wA.Code, wA.Body.String())
	}

	var authRespA struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wA.Body.Bytes(), &authRespA)
	tokenA := authRespA.Data.Token
	accountIDA := authRespA.Data.Accounts[0].ID

	// 2. Sign up Account B for cross-tenant isolation testing
	signUpPayloadB := map[string]string{
		"account_name": "Filter Corp B",
		"name":         "Filter Admin B",
		"email":        "admin@filter-b.com",
		"password":     "Password123!",
	}
	bodyB, _ := json.Marshal(signUpPayloadB)
	reqB := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyB))
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	r.ServeHTTP(wB, reqB)
	var authRespB struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wB.Body.Bytes(), &authRespB)
	tokenB := authRespB.Data.Token
	accountIDB := authRespB.Data.Accounts[0].ID

	var createdFilterID uint

	// =========================================================================
	// Scenario 1: Create Custom Filters (string query & object query)
	// =========================================================================
	t.Run("Create_Custom_Filter", func(t *testing.T) {
		// 1.1 Create with string query
		payload1 := map[string]any{
			"name":        "Urgent Open Tickets",
			"filter_type": "conversation",
			"query":       `{"status":"open","priority":"urgent"}`,
		}
		b1, _ := json.Marshal(payload1)
		req1 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/custom_filters", accountIDA), bytes.NewReader(b1))
		req1.Header.Set("Authorization", "Bearer "+tokenA)
		req1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		r.ServeHTTP(w1, req1)

		if w1.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for filter 1, got %d body=%s", w1.Code, w1.Body.String())
		}

		var resp1 struct {
			Data domain.CustomFilter `json:"data"`
		}
		_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
		createdFilterID = resp1.Data.ID

		if createdFilterID == 0 || resp1.Data.Name != "Urgent Open Tickets" {
			t.Errorf("unexpected filter 1 data: %+v", resp1.Data)
		}

		// 1.2 Create with structured JSON object query
		payload2 := map[string]any{
			"name":        "VIP Customers",
			"filter_type": "contact",
			"query": map[string]any{
				"tag":     "vip",
				"country": "US",
			},
		}
		b2, _ := json.Marshal(payload2)
		req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/custom_filters", accountIDA), bytes.NewReader(b2))
		req2.Header.Set("Authorization", "Bearer "+tokenA)
		req2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)

		if w2.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for filter 2, got %d body=%s", w2.Code, w2.Body.String())
		}
	})

	// =========================================================================
	// Scenario 2: List Custom Filters & Query Param Filtering
	// =========================================================================
	t.Run("List_Custom_Filters", func(t *testing.T) {
		// List all
		reqAll := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_filters", accountIDA), nil)
		reqAll.Header.Set("Authorization", "Bearer "+tokenA)
		wAll := httptest.NewRecorder()
		r.ServeHTTP(wAll, reqAll)

		if wAll.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for list all, got %d", wAll.Code)
		}

		var respAll struct {
			Data []domain.CustomFilter `json:"data"`
		}
		_ = json.Unmarshal(wAll.Body.Bytes(), &respAll)
		if len(respAll.Data) != 2 {
			t.Errorf("expected 2 filters, got %d", len(respAll.Data))
		}

		// Filter by type=conversation
		reqConv := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_filters?filter_type=conversation", accountIDA), nil)
		reqConv.Header.Set("Authorization", "Bearer "+tokenA)
		wConv := httptest.NewRecorder()
		r.ServeHTTP(wConv, reqConv)

		var respConv struct {
			Data []domain.CustomFilter `json:"data"`
		}
		_ = json.Unmarshal(wConv.Body.Bytes(), &respConv)
		if len(respConv.Data) != 1 || respConv.Data[0].Name != "Urgent Open Tickets" {
			t.Errorf("expected 1 conversation filter, got %d", len(respConv.Data))
		}
	})

	// =========================================================================
	// Scenario 3: Get Single Custom Filter Details
	// =========================================================================
	t.Run("Get_Single_Custom_Filter_Details", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/%d", accountIDA, createdFilterID), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for get filter details, got %d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.CustomFilter `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)

		if resp.Data.ID != createdFilterID || resp.Data.Name != "Urgent Open Tickets" {
			t.Errorf("unexpected filter details: %+v", resp.Data)
		}
		if resp.Data.FilterType != "conversation" {
			t.Errorf("expected filter_type 'conversation', got '%s'", resp.Data.FilterType)
		}

		// Non-existent filter returns 404
		reqNotFound := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/99999", accountIDA), nil)
		reqNotFound.Header.Set("Authorization", "Bearer "+tokenA)
		wNotFound := httptest.NewRecorder()
		r.ServeHTTP(wNotFound, reqNotFound)
		if wNotFound.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found for non-existent filter, got %d", wNotFound.Code)
		}
	})

	// =========================================================================
	// Scenario 4: Edit / Update Custom Filter (PUT & PATCH)
	// =========================================================================
	t.Run("Edit_Custom_Filter_PUT_And_PATCH", func(t *testing.T) {
		// 4.1 Full Update with PUT
		putPayload := map[string]any{
			"name":        "Escalated Critical Tickets",
			"filter_type": "conversation",
			"query":       `{"status":"open","priority":"critical","escalated":true}`,
		}
		putBody, _ := json.Marshal(putPayload)
		putReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/%d", accountIDA, createdFilterID), bytes.NewReader(putBody))
		putReq.Header.Set("Authorization", "Bearer "+tokenA)
		putReq.Header.Set("Content-Type", "application/json")
		putW := httptest.NewRecorder()
		r.ServeHTTP(putW, putReq)

		if putW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for PUT custom filter, got %d body=%s", putW.Code, putW.Body.String())
		}

		var putResp struct {
			Data domain.CustomFilter `json:"data"`
		}
		_ = json.Unmarshal(putW.Body.Bytes(), &putResp)
		if putResp.Data.Name != "Escalated Critical Tickets" {
			t.Errorf("expected updated name, got '%s'", putResp.Data.Name)
		}

		// Verify in DB
		var dbFilter domain.CustomFilter
		db.First(&dbFilter, createdFilterID)
		if dbFilter.Name != "Escalated Critical Tickets" {
			t.Errorf("DB filter name not updated: %s", dbFilter.Name)
		}

		// 4.2 Partial Update with PATCH (only name and structured query)
		patchPayload := map[string]any{
			"name": "Renamed Tickets View",
			"query": map[string]any{
				"status": "pending",
			},
		}
		patchBody, _ := json.Marshal(patchPayload)
		patchReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/%d", accountIDA, createdFilterID), bytes.NewReader(patchBody))
		patchReq.Header.Set("Authorization", "Bearer "+tokenA)
		patchReq.Header.Set("Content-Type", "application/json")
		patchW := httptest.NewRecorder()
		r.ServeHTTP(patchW, patchReq)

		if patchW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for PATCH custom filter, got %d body=%s", patchW.Code, patchW.Body.String())
		}

		var patchResp struct {
			Data domain.CustomFilter `json:"data"`
		}
		_ = json.Unmarshal(patchW.Body.Bytes(), &patchResp)
		if patchResp.Data.Name != "Renamed Tickets View" {
			t.Errorf("expected renamed filter, got '%s'", patchResp.Data.Name)
		}
		if patchResp.Data.FilterType != "conversation" {
			t.Errorf("filter_type should remain 'conversation', got '%s'", patchResp.Data.FilterType)
		}

		// Update non-existent filter returns 404
		patchNonReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/99999", accountIDA), bytes.NewReader(patchBody))
		patchNonReq.Header.Set("Authorization", "Bearer "+tokenA)
		patchNonReq.Header.Set("Content-Type", "application/json")
		patchNonW := httptest.NewRecorder()
		r.ServeHTTP(patchNonW, patchNonReq)
		if patchNonW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", patchNonW.Code)
		}
	})

	// =========================================================================
	// Scenario 5: Multi-Tenant Security Isolation
	// =========================================================================
	t.Run("Multi_Tenant_Security_Isolation", func(t *testing.T) {
		// Account B attempts to GET Account A's filter
		hackGetReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/%d", accountIDB, createdFilterID), nil)
		hackGetReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackGetW := httptest.NewRecorder()
		r.ServeHTTP(hackGetW, hackGetReq)
		if hackGetW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B gets Account A filter, got %d", hackGetW.Code)
		}

		// Account B attempts to PUT Account A's filter
		hackPutBody, _ := json.Marshal(map[string]any{"name": "Hacked Name"})
		hackPutReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/%d", accountIDB, createdFilterID), bytes.NewReader(hackPutBody))
		hackPutReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackPutReq.Header.Set("Content-Type", "application/json")
		hackPutW := httptest.NewRecorder()
		r.ServeHTTP(hackPutW, hackPutReq)
		if hackPutW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B updates Account A filter, got %d", hackPutW.Code)
		}

		// Account B attempts to DELETE Account A's filter
		hackDelReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/%d", accountIDB, createdFilterID), nil)
		hackDelReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackDelW := httptest.NewRecorder()
		r.ServeHTTP(hackDelW, hackDelReq)
		// Since where clause includes account_id, delete affects 0 rows and returns deleted:true or 200,
		// but the filter in DB must still belong to Account A!
		var checkFilter domain.CustomFilter
		if err := db.Where("id = ? AND account_id = ?", createdFilterID, accountIDA).First(&checkFilter).Error; err != nil {
			t.Fatalf("Account A filter should NOT be deleted by Account B: %v", err)
		}
	})

	// =========================================================================
	// Scenario 6: Delete Custom Filter
	// =========================================================================
	t.Run("Delete_Custom_Filter", func(t *testing.T) {
		delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/custom_filters/%d", accountIDA, createdFilterID), nil)
		delReq.Header.Set("Authorization", "Bearer "+tokenA)
		delW := httptest.NewRecorder()
		r.ServeHTTP(delW, delReq)

		if delW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for delete, got %d", delW.Code)
		}

		// Verify filter is deleted
		var count int64
		db.Model(&domain.CustomFilter{}).Where("id = ?", createdFilterID).Count(&count)
		if count != 0 {
			t.Errorf("expected filter to be deleted from DB, count=%d", count)
		}
	})
}
