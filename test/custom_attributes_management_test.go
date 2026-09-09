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

func TestCustomAttributesManagement(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:custom_attrs_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "custom-attrs-jwt-secret-2026",
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
		"account_name": "Attributes Corp A",
		"name":         "Attr Admin A",
		"email":        "admin@attr-a.com",
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
		"account_name": "Attributes Corp B",
		"name":         "Attr Admin B",
		"email":        "admin@attr-b.com",
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

	var createdAttrID uint

	// =========================================================================
	// Scenario 1: Create Custom Attribute Definitions
	// =========================================================================
	t.Run("Create_Custom_Attribute_Definition", func(t *testing.T) {
		// 1.1 Contact attribute with dropdown list
		payload1 := map[string]any{
			"attribute_display_name": "Customer Plan",
			"attribute_key":          "customer_plan",
			"attribute_model":        "contact_attribute",
			"attribute_display_type": "list",
			"attribute_description":  "Subscription tier of the customer",
			"attribute_values":       []string{"Free", "Starter", "Enterprise"},
			"default_value":          "Free",
		}
		b1, _ := json.Marshal(payload1)
		req1 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions", accountIDA), bytes.NewReader(b1))
		req1.Header.Set("Authorization", "Bearer "+tokenA)
		req1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		r.ServeHTTP(w1, req1)

		if w1.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for attribute 1, got %d body=%s", w1.Code, w1.Body.String())
		}

		var resp1 struct {
			Data domain.CustomAttributeDefinition `json:"data"`
		}
		_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
		createdAttrID = resp1.Data.ID

		if createdAttrID == 0 || resp1.Data.AttributeKey != "customer_plan" {
			t.Errorf("unexpected attribute data: %+v", resp1.Data)
		}

		// 1.2 Conversation attribute
		payload2 := map[string]any{
			"attribute_display_name": "Issue Severity",
			"attribute_key":          "issue_severity",
			"attribute_model":        "conversation_attribute",
			"attribute_display_type": "text",
			"default_value":          "Normal",
		}
		b2, _ := json.Marshal(payload2)
		req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions", accountIDA), bytes.NewReader(b2))
		req2.Header.Set("Authorization", "Bearer "+tokenA)
		req2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)

		if w2.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for attribute 2, got %d body=%s", w2.Code, w2.Body.String())
		}
	})

	// =========================================================================
	// Scenario 2: List Custom Attribute Definitions & Model Filter
	// =========================================================================
	t.Run("List_Custom_Attribute_Definitions", func(t *testing.T) {
		// List all
		reqAll := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions", accountIDA), nil)
		reqAll.Header.Set("Authorization", "Bearer "+tokenA)
		wAll := httptest.NewRecorder()
		r.ServeHTTP(wAll, reqAll)

		if wAll.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for list all, got %d", wAll.Code)
		}

		var respAll struct {
			Data []domain.CustomAttributeDefinition `json:"data"`
		}
		_ = json.Unmarshal(wAll.Body.Bytes(), &respAll)
		if len(respAll.Data) != 2 {
			t.Errorf("expected 2 attributes, got %d", len(respAll.Data))
		}

		// Filter by conversation_attribute
		reqConv := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions?attribute_model=conversation_attribute", accountIDA), nil)
		reqConv.Header.Set("Authorization", "Bearer "+tokenA)
		wConv := httptest.NewRecorder()
		r.ServeHTTP(wConv, reqConv)

		var respConv struct {
			Data []domain.CustomAttributeDefinition `json:"data"`
		}
		_ = json.Unmarshal(wConv.Body.Bytes(), &respConv)
		if len(respConv.Data) != 1 || respConv.Data[0].AttributeKey != "issue_severity" {
			t.Errorf("expected 1 conversation attribute, got %d", len(respConv.Data))
		}
	})

	// =========================================================================
	// Scenario 3: Get Single Custom Attribute Definition Details
	// =========================================================================
	t.Run("Get_Single_Custom_Attribute_Definition", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/%d", accountIDA, createdAttrID), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for get attribute details, got %d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.CustomAttributeDefinition `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)

		if resp.Data.ID != createdAttrID || resp.Data.AttributeDisplayName != "Customer Plan" {
			t.Errorf("unexpected attribute details: %+v", resp.Data)
		}
		if resp.Data.AttributeModel != "contact_attribute" {
			t.Errorf("expected contact_attribute, got %s", resp.Data.AttributeModel)
		}

		// Non-existent ID returns 404
		reqNotFound := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/99999", accountIDA), nil)
		reqNotFound.Header.Set("Authorization", "Bearer "+tokenA)
		wNotFound := httptest.NewRecorder()
		r.ServeHTTP(wNotFound, reqNotFound)
		if wNotFound.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", wNotFound.Code)
		}
	})

	// =========================================================================
	// Scenario 4: Edit / Update Custom Attribute Definition (PUT & PATCH)
	// =========================================================================
	t.Run("Edit_Custom_Attribute_Definition_PUT_And_PATCH", func(t *testing.T) {
		// 4.1 Full Update with PUT
		putPayload := map[string]any{
			"attribute_display_name": "Customer Membership Tier",
			"attribute_display_type": "list",
			"attribute_description":  "VIP/Membership level of customer account",
			"attribute_values":       []string{"Standard", "Silver", "Gold", "Platinum"},
			"default_value":          "Standard",
		}
		putBody, _ := json.Marshal(putPayload)
		putReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/%d", accountIDA, createdAttrID), bytes.NewReader(putBody))
		putReq.Header.Set("Authorization", "Bearer "+tokenA)
		putReq.Header.Set("Content-Type", "application/json")
		putW := httptest.NewRecorder()
		r.ServeHTTP(putW, putReq)

		if putW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for PUT attribute, got %d body=%s", putW.Code, putW.Body.String())
		}

		var putResp struct {
			Data domain.CustomAttributeDefinition `json:"data"`
		}
		_ = json.Unmarshal(putW.Body.Bytes(), &putResp)
		if putResp.Data.AttributeDisplayName != "Customer Membership Tier" {
			t.Errorf("expected updated display name, got '%s'", putResp.Data.AttributeDisplayName)
		}

		// Verify in DB
		var dbAttr domain.CustomAttributeDefinition
		db.First(&dbAttr, createdAttrID)
		if dbAttr.AttributeDisplayName != "Customer Membership Tier" {
			t.Errorf("DB display name not updated: %s", dbAttr.AttributeDisplayName)
		}

		// 4.2 Partial Update with PATCH (only display name)
		patchPayload := map[string]any{
			"attribute_display_name": "VIP Level",
		}
		patchBody, _ := json.Marshal(patchPayload)
		patchReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/%d", accountIDA, createdAttrID), bytes.NewReader(patchBody))
		patchReq.Header.Set("Authorization", "Bearer "+tokenA)
		patchReq.Header.Set("Content-Type", "application/json")
		patchW := httptest.NewRecorder()
		r.ServeHTTP(patchW, patchReq)

		if patchW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for PATCH attribute, got %d body=%s", patchW.Code, patchW.Body.String())
		}

		var patchResp struct {
			Data domain.CustomAttributeDefinition `json:"data"`
		}
		_ = json.Unmarshal(patchW.Body.Bytes(), &patchResp)
		if patchResp.Data.AttributeDisplayName != "VIP Level" {
			t.Errorf("expected 'VIP Level', got '%s'", patchResp.Data.AttributeDisplayName)
		}
		if patchResp.Data.AttributeModel != "contact_attribute" {
			t.Errorf("expected model 'contact_attribute' to remain unchanged, got '%s'", patchResp.Data.AttributeModel)
		}

		// Non-existent ID returns 404
		patchNonReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/99999", accountIDA), bytes.NewReader(patchBody))
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
		// Account B attempts to GET Account A's attribute definition
		hackGetReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/%d", accountIDB, createdAttrID), nil)
		hackGetReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackGetW := httptest.NewRecorder()
		r.ServeHTTP(hackGetW, hackGetReq)
		if hackGetW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B gets Account A attribute, got %d", hackGetW.Code)
		}

		// Account B attempts to PUT Account A's attribute definition
		hackPutBody, _ := json.Marshal(map[string]any{"attribute_display_name": "Hacked Name"})
		hackPutReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/%d", accountIDB, createdAttrID), bytes.NewReader(hackPutBody))
		hackPutReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackPutReq.Header.Set("Content-Type", "application/json")
		hackPutW := httptest.NewRecorder()
		r.ServeHTTP(hackPutW, hackPutReq)
		if hackPutW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B updates Account A attribute, got %d", hackPutW.Code)
		}

		// Account B attempts to DELETE Account A's attribute definition
		hackDelReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/%d", accountIDB, createdAttrID), nil)
		hackDelReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackDelW := httptest.NewRecorder()
		r.ServeHTTP(hackDelW, hackDelReq)
		// Account A attribute in DB must still exist
		var checkAttr domain.CustomAttributeDefinition
		if err := db.Where("id = ? AND account_id = ?", createdAttrID, accountIDA).First(&checkAttr).Error; err != nil {
			t.Fatalf("Account A attribute should NOT be deleted by Account B: %v", err)
		}
	})

	// =========================================================================
	// Scenario 6: Delete Custom Attribute Definition
	// =========================================================================
	t.Run("Delete_Custom_Attribute_Definition", func(t *testing.T) {
		delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/custom_attribute_definitions/%d", accountIDA, createdAttrID), nil)
		delReq.Header.Set("Authorization", "Bearer "+tokenA)
		delW := httptest.NewRecorder()
		r.ServeHTTP(delW, delReq)

		if delW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for delete, got %d", delW.Code)
		}

		// Verify attribute definition is deleted from DB
		var count int64
		db.Model(&domain.CustomAttributeDefinition{}).Where("id = ?", createdAttrID).Count(&count)
		if count != 0 {
			t.Errorf("expected attribute definition to be deleted, count=%d", count)
		}
	})
}
