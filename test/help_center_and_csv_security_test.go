package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/security"
	"github.com/gin-gonic/gin"
)

func TestCSVSanitizationUnit(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"=cmd|' /C calc'!A0", "'=cmd|' /C calc'!A0"},
		{"+1+1", "'+1+1"},
		{"-2+3", "'-2+3"},
		{"@SUM(A1:A10)", "'@SUM(A1:A10)"},
		{"\tTAB", "'\tTAB"},
		{"\rCR", "'\rCR"},
		{"Normal Name", "Normal Name"},
		{"", ""},
		{"12345", "12345"},
	}

	for _, c := range cases {
		actual := security.SanitizeCSVCell(c.input)
		if actual != c.expected {
			t.Errorf("SanitizeCSVCell(%q) = %q, expected %q", c.input, actual, c.expected)
		}
	}
}

func TestCSVExportAndHelpCenterSecurity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "csv-hc-test-jwt-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// 1. Sign up Account 1 (Owner 1)
	signUp1, _ := json.Marshal(map[string]string{
		"name":         "Owner One",
		"email":        "owner1@example.com",
		"password":     "password123456",
		"account_name": "Tenant One",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUp1))
	req1.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("sign up 1 failed: %d, body: %s", w1.Code, w1.Body.String())
	}
	var res1 struct {
		Data struct {
			Token    string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &res1)
	token1 := res1.Data.Token
	account1ID := res1.Data.Accounts[0].ID

	// 2. Sign up Account 2 (Owner 2)
	signUp2, _ := json.Marshal(map[string]string{
		"name":         "Owner Two",
		"email":        "owner2@example.com",
		"password":     "password123456",
		"account_name": "Tenant Two",
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUp2))
	req2.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("sign up 2 failed: %d, body: %s", w2.Code, w2.Body.String())
	}
	var res2 struct {
		Data struct {
			Token    string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &res2)
	token2 := res2.Data.Token
	account2ID := res2.Data.Accounts[0].ID

	// ==========================================
	// PART 1: CSV Formula Injection Sanitization
	// ==========================================
	t.Run("Contact CSV Export Sanitization", func(t *testing.T) {
		// Create a contact with formula injection values in Account 1
		contactBody, _ := json.Marshal(map[string]interface{}{
			"name":         "=cmd|' /C calc'!A0",
			"email":        "victim@example.com",
			"phone_number": "+1800123456",
			"identifier":   "@SUM(1,2)",
			"custom_attributes": map[string]interface{}{
				"note": "-2+5",
			},
		})
		wCreate := httptest.NewRecorder()
		reqCreate, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/contacts", account1ID), bytes.NewBuffer(contactBody))
		reqCreate.Header.Set("Content-Type", "application/json")
		reqCreate.Header.Set("Authorization", "Bearer "+token1)
		engine.ServeHTTP(wCreate, reqCreate)
		if wCreate.Code != http.StatusCreated && wCreate.Code != http.StatusOK {
			t.Fatalf("failed to create contact: %d, body: %s", wCreate.Code, wCreate.Body.String())
		}

		// Export contacts as CSV
		wExport := httptest.NewRecorder()
		reqExport, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/contacts/export", account1ID), nil)
		reqExport.Header.Set("Authorization", "Bearer "+token1)
		engine.ServeHTTP(wExport, reqExport)
		if wExport.Code != http.StatusOK {
			t.Fatalf("export contacts failed: %d, body: %s", wExport.Code, wExport.Body.String())
		}

		csvContent := wExport.Body.String()
		// Must not contain raw executable formulas
		if strings.Contains(csvContent, ",=cmd|") {
			t.Fatalf("raw formula injection detected in CSV: %s", csvContent)
		}
		// Must be escaped with leading single quote
		if !strings.Contains(csvContent, "'=cmd|' /C calc'!A0") {
			t.Fatalf("expected escaped formula in CSV, got: %s", csvContent)
		}
		if !strings.Contains(csvContent, "'+1800123456") {
			t.Fatalf("expected escaped phone with leading + in CSV, got: %s", csvContent)
		}
		if !strings.Contains(csvContent, "'@SUM(1,2)") {
			t.Fatalf("expected escaped identifier with leading @ in CSV, got: %s", csvContent)
		}
	})

	// ==========================================
	// PART 2: Help Center Multi-tenant IDOR Protection
	// ==========================================
	t.Run("Help Center Cross-Tenant IDOR and Hierarchy Isolation", func(t *testing.T) {
		// 1. Account 1 creates Portal 1
		p1Body, _ := json.Marshal(map[string]string{
			"name": "Help Center 1",
			"slug": "hc-tenant-1",
		})
		wP1 := httptest.NewRecorder()
		reqP1, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals", account1ID), bytes.NewBuffer(p1Body))
		reqP1.Header.Set("Content-Type", "application/json")
		reqP1.Header.Set("Authorization", "Bearer "+token1)
		engine.ServeHTTP(wP1, reqP1)
		if wP1.Code != http.StatusCreated {
			t.Fatalf("create portal 1 failed: %d, body: %s", wP1.Code, wP1.Body.String())
		}
		var p1Resp struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wP1.Body.Bytes(), &p1Resp)
		portal1ID := p1Resp.Data.ID

		// 2. Account 1 creates Category 1 under Portal 1
		c1Body, _ := json.Marshal(map[string]string{
			"name": "Billing FAQ",
			"slug": "billing-faq",
		})
		wC1 := httptest.NewRecorder()
		reqC1, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/categories", account1ID, portal1ID), bytes.NewBuffer(c1Body))
		reqC1.Header.Set("Content-Type", "application/json")
		reqC1.Header.Set("Authorization", "Bearer "+token1)
		engine.ServeHTTP(wC1, reqC1)
		if wC1.Code != http.StatusCreated {
			t.Fatalf("create category 1 failed: %d, body: %s", wC1.Code, wC1.Body.String())
		}
		var c1Resp struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wC1.Body.Bytes(), &c1Resp)
		category1ID := c1Resp.Data.ID

		// 3. Account 1 creates Article 1 under Portal 1 & Category 1
		a1Body, _ := json.Marshal(map[string]interface{}{
			"category_id": category1ID,
			"title":       "How to pay",
			"slug":        "how-to-pay",
			"content":     "You can pay via card.",
			"status":      "published",
		})
		wA1 := httptest.NewRecorder()
		reqA1, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/articles", account1ID, portal1ID), bytes.NewBuffer(a1Body))
		reqA1.Header.Set("Content-Type", "application/json")
		reqA1.Header.Set("Authorization", "Bearer "+token1)
		engine.ServeHTTP(wA1, reqA1)
		if wA1.Code != http.StatusCreated {
			t.Fatalf("create article 1 failed: %d, body: %s", wA1.Code, wA1.Body.String())
		}
		var a1Resp struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wA1.Body.Bytes(), &a1Resp)
		article1ID := a1Resp.Data.ID

		// 4. Account 2 creates Portal 2
		p2Body, _ := json.Marshal(map[string]string{
			"name": "Help Center 2",
			"slug": "hc-tenant-2",
		})
		wP2 := httptest.NewRecorder()
		reqP2, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals", account2ID), bytes.NewBuffer(p2Body))
		reqP2.Header.Set("Content-Type", "application/json")
		reqP2.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wP2, reqP2)
		if wP2.Code != http.StatusCreated {
			t.Fatalf("create portal 2 failed: %d, body: %s", wP2.Code, wP2.Body.String())
		}
		var p2Resp struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wP2.Body.Bytes(), &p2Resp)
		portal2ID := p2Resp.Data.ID

		// 5. Cross-tenant IDOR attacks from Account 2 targeting Portal 1
		// 5.1 Account 2 attempts to GET Portal 1
		wGetP := httptest.NewRecorder()
		reqGetP, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/portals/%d", account2ID, portal1ID), nil)
		reqGetP.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wGetP, reqGetP)
		if wGetP.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant GET portal, got: %d", wGetP.Code)
		}

		// 5.2 Account 2 attempts to UPDATE Portal 1
		wPutP := httptest.NewRecorder()
		upBody, _ := json.Marshal(map[string]string{"name": "Hacked Portal"})
		reqPutP, _ := http.NewRequest("PUT", fmt.Sprintf("/api/v1/accounts/%d/portals/%d", account2ID, portal1ID), bytes.NewBuffer(upBody))
		reqPutP.Header.Set("Content-Type", "application/json")
		reqPutP.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wPutP, reqPutP)
		if wPutP.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant PUT portal, got: %d", wPutP.Code)
		}

		// 5.3 Account 2 attempts to DELETE Portal 1
		wDelP := httptest.NewRecorder()
		reqDelP, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/portals/%d", account2ID, portal1ID), nil)
		reqDelP.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wDelP, reqDelP)
		if wDelP.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant DELETE portal, got: %d", wDelP.Code)
		}

		// 5.4 Account 2 attempts to list categories under Portal 1
		wListCats := httptest.NewRecorder()
		reqListCats, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/categories", account2ID, portal1ID), nil)
		reqListCats.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wListCats, reqListCats)
		if wListCats.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant GET categories, got: %d", wListCats.Code)
		}

		// 5.5 Account 2 attempts to create category under Portal 1
		wCreateCat := httptest.NewRecorder()
		injectedCatBody, _ := json.Marshal(map[string]string{"name": "Injected Cat", "slug": "injected-cat"})
		reqCreateCat, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/categories", account2ID, portal1ID), bytes.NewBuffer(injectedCatBody))
		reqCreateCat.Header.Set("Content-Type", "application/json")
		reqCreateCat.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wCreateCat, reqCreateCat)
		if wCreateCat.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant POST category, got: %d", wCreateCat.Code)
		}

		// 5.6 Account 2 attempts to GET Category 1
		wGetCat := httptest.NewRecorder()
		reqGetCat, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/categories/%d", account2ID, portal1ID, category1ID), nil)
		reqGetCat.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wGetCat, reqGetCat)
		if wGetCat.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant GET category, got: %d", wGetCat.Code)
		}

		// 5.7 Account 2 attempts to UPDATE Category 1
		wPutCat := httptest.NewRecorder()
		reqPutCat, _ := http.NewRequest("PUT", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/categories/%d", account2ID, portal1ID, category1ID), bytes.NewBuffer(injectedCatBody))
		reqPutCat.Header.Set("Content-Type", "application/json")
		reqPutCat.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wPutCat, reqPutCat)
		if wPutCat.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant PUT category, got: %d", wPutCat.Code)
		}

		// 5.8 Account 2 attempts to DELETE Category 1
		wDelCat := httptest.NewRecorder()
		reqDelCat, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/categories/%d", account2ID, portal1ID, category1ID), nil)
		reqDelCat.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wDelCat, reqDelCat)
		if wDelCat.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant DELETE category, got: %d", wDelCat.Code)
		}

		// 5.9 Account 2 attempts to create an article in Portal 2 referencing Category 1 (from Portal 1)
		wCreateArt := httptest.NewRecorder()
		crossArtBody, _ := json.Marshal(map[string]interface{}{
			"category_id": category1ID,
			"title":       "Cross Tenant Category",
			"slug":        "cross-cat",
			"content":     "test",
		})
		reqCreateArt, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/articles", account2ID, portal2ID), bytes.NewBuffer(crossArtBody))
		reqCreateArt.Header.Set("Content-Type", "application/json")
		reqCreateArt.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wCreateArt, reqCreateArt)
		if wCreateArt.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request on cross-portal category attachment, got: %d", wCreateArt.Code)
		}

		// 5.10 Account 2 attempts to GET Article 1
		wGetArt := httptest.NewRecorder()
		reqGetArt, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/articles/%d", account2ID, portal1ID, article1ID), nil)
		reqGetArt.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wGetArt, reqGetArt)
		if wGetArt.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant GET article, got: %d", wGetArt.Code)
		}

		// 5.11 Account 2 attempts to DELETE Article 1
		wDelArt := httptest.NewRecorder()
		reqDelArt, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/articles/%d", account2ID, portal1ID, article1ID), nil)
		reqDelArt.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wDelArt, reqDelArt)
		if wDelArt.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant DELETE article, got: %d", wDelArt.Code)
		}

		// 5.12 Account 2 attempts bulk action on Portal 1
		wBulk := httptest.NewRecorder()
		bulkBody, _ := json.Marshal(map[string]interface{}{
			"ids":    []uint{article1ID},
			"status": "archived",
		})
		reqBulk, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals/%d/articles/bulk_actions", account2ID, portal1ID), bytes.NewBuffer(bulkBody))
		reqBulk.Header.Set("Content-Type", "application/json")
		reqBulk.Header.Set("Authorization", "Bearer "+token2)
		engine.ServeHTTP(wBulk, reqBulk)
		if wBulk.Code != http.StatusNotFound {
			t.Fatalf("expected 404 on cross-tenant bulk article action, got: %d", wBulk.Code)
		}

		// 6. Verify Public Endpoints remain accessible and intact
		wPubGet := httptest.NewRecorder()
		reqPubGet, _ := http.NewRequest("GET", "/public/api/v1/portals/hc-tenant-1", nil)
		engine.ServeHTTP(wPubGet, reqPubGet)
		if wPubGet.Code != http.StatusOK {
			t.Fatalf("expected public portal to return 200, got: %d", wPubGet.Code)
		}

		wPubHtml := httptest.NewRecorder()
		reqPubHtml, _ := http.NewRequest("GET", "/hc/hc-tenant-1", nil)
		engine.ServeHTTP(wPubHtml, reqPubHtml)
		if wPubHtml.Code != http.StatusOK {
			t.Fatalf("expected public portal HTML to return 200, got: %d", wPubHtml.Code)
		}
		if !strings.Contains(wPubHtml.Body.String(), "Help Center 1") {
			t.Fatalf("expected Help Center 1 in HTML body, got: %s", wPubHtml.Body.String())
		}
	})

	// ==========================================
	// PART 3: Custom Role RBAC on Help Center
	// ==========================================
	t.Run("Help Center Custom Role RBAC", func(t *testing.T) {
		// Create custom role without knowledge base permissions in Account 1
		roleBody, _ := json.Marshal(map[string]interface{}{
			"name":        "Conversation Only Agent",
			"description": "Only allowed to handle conversations",
			"permissions": []string{domain.PermissionConversationManage},
		})
		wRole := httptest.NewRecorder()
		reqRole, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/custom_roles", account1ID), bytes.NewBuffer(roleBody))
		reqRole.Header.Set("Content-Type", "application/json")
		reqRole.Header.Set("Authorization", "Bearer "+token1)
		engine.ServeHTTP(wRole, reqRole)
		if wRole.Code != http.StatusCreated {
			t.Fatalf("create custom role failed: %d, body: %s", wRole.Code, wRole.Body.String())
		}
		var roleResp struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wRole.Body.Bytes(), &roleResp)
		roleID := roleResp.Data.ID

		// Add a new agent with this custom role
		agentBody, _ := json.Marshal(map[string]interface{}{
			"name":           "Restricted Agent",
			"email":          "restricted.agent@example.com",
			"role":           "agent",
			"custom_role_id": roleID,
		})
		wAgent := httptest.NewRecorder()
		reqAgent, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", account1ID), bytes.NewBuffer(agentBody))
		reqAgent.Header.Set("Content-Type", "application/json")
		reqAgent.Header.Set("Authorization", "Bearer "+token1)
		engine.ServeHTTP(wAgent, reqAgent)
		if wAgent.Code != http.StatusOK && wAgent.Code != http.StatusCreated {
			t.Fatalf("add agent failed: %d, body: %s", wAgent.Code, wAgent.Body.String())
		}

		// Login as the restricted agent
		loginBody, _ := json.Marshal(map[string]string{
			"email":    "restricted.agent@example.com",
			"password": "Password123!",
		})
		wLogin := httptest.NewRecorder()
		reqLogin, _ := http.NewRequest("POST", "/auth/login", bytes.NewBuffer(loginBody))
		reqLogin.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wLogin, reqLogin)
		if wLogin.Code != http.StatusOK {
			t.Fatalf("restricted agent login failed: %d, body: %s", wLogin.Code, wLogin.Body.String())
		}
		var agentLoginResp struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wLogin.Body.Bytes(), &agentLoginResp)
		agentToken := agentLoginResp.Data.Token

		// Attempt to create a portal with restricted agent -> Must be 403 Forbidden
		portalBody, _ := json.Marshal(map[string]string{
			"name": "Forbidden Portal",
			"slug": "forbidden-portal",
		})
		wForbid := httptest.NewRecorder()
		reqForbid, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/portals", account1ID), bytes.NewBuffer(portalBody))
		reqForbid.Header.Set("Content-Type", "application/json")
		reqForbid.Header.Set("Authorization", "Bearer "+agentToken)
		engine.ServeHTTP(wForbid, reqForbid)
		if wForbid.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for restricted agent creating portal, got: %d", wForbid.Code)
		}
	})
}
