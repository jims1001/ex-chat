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

func TestCompanyManagementEnhancements(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_company_mgmt_enhancements_987",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	r := router.SetupRouter(cfg, db, hub)

	doReq := func(method, path string, payload any, token string) *httptest.ResponseRecorder {
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		r.ServeHTTP(w, req)
		return w
	}

	// 1. Sign up Administrator for Account 1 (Acme Global)
	w1 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Acme Global Enterprise",
		"name":         "Admin User",
		"email":        "admin@acmeglobal.test",
		"password":     "Password123!",
	}, "")
	if w1.Code != http.StatusCreated {
		t.Fatalf("failed to sign up account 1: %s", w1.Body.String())
	}
	var authResp1 struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &authResp1)
	tokenAdmin := authResp1.Data.Token
	accID := authResp1.Data.Accounts[0].ID

	// 2. Sign up Account 2 for tenant isolation test
	w2 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Rival Technologies",
		"name":         "Rival Admin",
		"email":        "admin@rival.test",
		"password":     "Password123!",
	}, "")
	if w2.Code != http.StatusCreated {
		t.Fatalf("failed to sign up account 2: %s", w2.Body.String())
	}
	var authResp2 struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	tokenRival := authResp2.Data.Token
	rivalAccID := authResp2.Data.Accounts[0].ID

	// 3. Create regular agent in Account 1 without contact_manage permission
	agentUser := domain.User{
		Name:         "Support Agent",
		Email:        "agent_limited@acmeglobal.test",
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	db.Create(&agentUser)
	db.Create(&domain.AccountUser{AccountID: accID, UserID: agentUser.ID, Role: domain.RoleAgent})

	tokenAgent, err := auth.GenerateToken(&agentUser, cfg.JWTSecret, 24)
	if err != nil {
		t.Fatalf("failed to generate agent token: %v", err)
	}

	// 4. Create sample companies in Account 1
	wComp1 := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies", accID), map[string]any{
		"name":        "Acme Robotics",
		"domain":      "acmerobotics.com",
		"industry":    "AI & Robotics",
		"description": "Leading industrial robotics provider",
		"avatar_url":  "https://example.com/acme.png",
		"custom_attributes": map[string]any{
			"tier":   "enterprise",
			"region": "APAC",
		},
	}, tokenAdmin)
	if wComp1.Code != http.StatusCreated && wComp1.Code != http.StatusOK {
		t.Fatalf("failed to create company 1: %s", wComp1.Body.String())
	}
	var comp1Resp struct {
		Payload struct {
			ID uint `json:"id"`
		} `json:"payload"`
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wComp1.Body.Bytes(), &comp1Resp)
	company1ID := comp1Resp.Payload.ID
	if company1ID == 0 {
		company1ID = comp1Resp.Data.ID
	}

	wComp2 := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies", accID), map[string]any{
		"company": map[string]any{
			"name":        "Stark Industries",
			"domain":      "starkindustries.com",
			"industry":    "Aerospace & Defense",
			"description": "Advanced energy and propulsion tech",
			"avatar_url":  "https://example.com/stark.png",
			"custom_attributes": map[string]any{
				"tier": "vip",
			},
		},
	}, tokenAdmin)
	if wComp2.Code != http.StatusCreated && wComp2.Code != http.StatusOK {
		t.Fatalf("failed to create company 2: %s", wComp2.Body.String())
	}
	var comp2Resp struct {
		Payload struct {
			ID uint `json:"id"`
		} `json:"payload"`
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wComp2.Body.Bytes(), &comp2Resp)
	company2ID := comp2Resp.Payload.ID
	if company2ID == 0 {
		company2ID = comp2Resp.Data.ID
	}

	// Create company in Account 2 (Rival) to ensure tenant boundary
	_ = doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies", rivalAccID), map[string]any{
		"name":   "Acme Fake Rival",
		"domain": "acmerobotics.com",
	}, tokenRival)

	// Create sample contacts in Account 1
	wCt1 := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts", accID), map[string]any{
		"name":         "Tony Stark",
		"email":        "tony@starkindustries.com",
		"phone_number": "+12345678901",
		"identifier":   "STARK-001",
	}, tokenAdmin)
	var ct1Resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
		Payload struct {
			Contact struct {
				ID uint `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(wCt1.Body.Bytes(), &ct1Resp)
	contact1ID := ct1Resp.Data.ID
	if contact1ID == 0 {
		contact1ID = ct1Resp.Payload.Contact.ID
	}

	wCt2 := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts", accID), map[string]any{
		"name":         "Pepper Potts",
		"email":        "pepper@starkindustries.com",
		"phone_number": "+12345678902",
		"identifier":   "STARK-002",
	}, tokenAdmin)
	var ct2Resp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wCt2.Body.Bytes(), &ct2Resp)
	contact2ID := ct2Resp.Data.ID

	// Link contact 1 (Tony Stark) to Company 2 (Stark Industries)
	_ = doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/contacts", accID, company2ID), map[string]any{
		"contact_ids": []uint{contact1ID},
	}, tokenAdmin)

	// ==========================================
	// Test 1: Company Search (GET /companies/search)
	// ==========================================
	t.Run("1_SearchCompanies", func(t *testing.T) {
		// A. Missing q param -> returns 422 Unprocessable Entity
		wMissing := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/search", accID), nil, tokenAdmin)
		if wMissing.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for missing q, got %d: %s", wMissing.Code, wMissing.Body.String())
		}
		var errResp map[string]any
		_ = json.Unmarshal(wMissing.Body.Bytes(), &errResp)
		if errResp["error"] != "Specify search string with parameter q" {
			t.Errorf("unexpected error message: %v", errResp["error"])
		}

		// B. Search by name (case-insensitive)
		wName := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/search?q=ROBOTICS", accID), nil, tokenAdmin)
		if wName.Code != http.StatusOK {
			t.Fatalf("expected 200 for company search, got %d: %s", wName.Code, wName.Body.String())
		}
		var searchResp struct {
			Meta struct {
				TotalCount int `json:"total_count"`
				Page       int `json:"page"`
			} `json:"meta"`
			Payload []map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(wName.Body.Bytes(), &searchResp)
		if searchResp.Meta.TotalCount != 1 || len(searchResp.Payload) != 1 {
			t.Fatalf("expected 1 company matching ROBOTICS, got %d (meta: %d)", len(searchResp.Payload), searchResp.Meta.TotalCount)
		}
		if searchResp.Payload[0]["name"] != "Acme Robotics" {
			t.Errorf("expected Acme Robotics, got %v", searchResp.Payload[0]["name"])
		}

		// C. Search by domain
		wDomain := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/search?q=starkindustries.com", accID), nil, tokenAdmin)
		if wDomain.Code != http.StatusOK {
			t.Fatalf("expected 200 for domain search, got %d", wDomain.Code)
		}
		var domainResp struct {
			Payload []map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(wDomain.Body.Bytes(), &domainResp)
		if len(domainResp.Payload) != 1 || domainResp.Payload[0]["domain"] != "starkindustries.com" {
			t.Fatalf("expected Stark Industries by domain, got %v", domainResp.Payload)
		}

		// D. Tenant Isolation: Search Acme in Account 1 must not return Rival
		wAcme := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/search?q=Acme", accID), nil, tokenAdmin)
		var acmeResp struct {
			Payload []map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(wAcme.Body.Bytes(), &acmeResp)
		for _, comp := range acmeResp.Payload {
			if comp["name"] == "Acme Fake Rival" {
				t.Errorf("tenant isolation violation: returned rival company in account 1 search")
			}
		}
	})

	// ==========================================
	// Test 2: Search Company Contacts (GET /companies/:id/contacts/search)
	// ==========================================
	t.Run("2_SearchCompanyContacts", func(t *testing.T) {
		// A. Missing q param -> returns 422
		wMissing := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/contacts/search", accID, company2ID), nil, tokenAdmin)
		if wMissing.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for missing q in contact search, got %d: %s", wMissing.Code, wMissing.Body.String())
		}

		// B. Non-existent company -> returns 404
		wNotFound := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/99999/contacts/search?q=Pepper", accID), nil, tokenAdmin)
		if wNotFound.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent company, got %d", wNotFound.Code)
		}

		// C. Candidate search (default Chatwoot behavior: contacts not yet linked to company)
		// Contact 1 (Tony Stark) is linked to Company 2.
		// Contact 2 (Pepper Potts) is NOT linked to Company 2.
		wCand := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/contacts/search?q=starkindustries.com", accID, company2ID), nil, tokenAdmin)
		if wCand.Code != http.StatusOK {
			t.Fatalf("expected 200 for candidate contacts search, got %d: %s", wCand.Code, wCand.Body.String())
		}
		var candResp struct {
			Meta struct {
				TotalCount int `json:"total_count"`
			} `json:"meta"`
			Payload []struct {
				ID                     uint   `json:"id"`
				Name                   string `json:"name"`
				LinkedToCurrentCompany bool   `json:"linked_to_current_company"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(wCand.Body.Bytes(), &candResp)
		if candResp.Meta.TotalCount != 1 || len(candResp.Payload) != 1 {
			t.Fatalf("expected exactly 1 candidate contact (Pepper), got count=%d, items=%d", candResp.Meta.TotalCount, len(candResp.Payload))
		}
		if candResp.Payload[0].ID != contact2ID {
			t.Errorf("expected Pepper Potts (%d) as candidate, got %d", contact2ID, candResp.Payload[0].ID)
		}
		if candResp.Payload[0].LinkedToCurrentCompany != false {
			t.Errorf("expected linked_to_current_company to be false for candidate")
		}

		// D. Search with include_current=true (or scope=all) -> returns both
		wAll := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/contacts/search?q=starkindustries.com&include_current=true", accID, company2ID), nil, tokenAdmin)
		if wAll.Code != http.StatusOK {
			t.Fatalf("expected 200 with include_current=true, got %d: %s", wAll.Code, wAll.Body.String())
		}
		var allResp struct {
			Payload []struct {
				ID                     uint   `json:"id"`
				Name                   string `json:"name"`
				LinkedToCurrentCompany bool   `json:"linked_to_current_company"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(wAll.Body.Bytes(), &allResp)
		if len(allResp.Payload) != 2 {
			t.Fatalf("expected 2 contacts with include_current=true, got %d", len(allResp.Payload))
		}
		for _, ct := range allResp.Payload {
			if ct.ID == contact1ID && !ct.LinkedToCurrentCompany {
				t.Errorf("expected Tony Stark to have linked_to_current_company=true")
			}
			if ct.ID == contact2ID && ct.LinkedToCurrentCompany {
				t.Errorf("expected Pepper Potts to have linked_to_current_company=false")
			}
		}
	})

	// ==========================================
	// Test 3: Destroy Company Custom Attributes
	// ==========================================
	t.Run("3_DestroyCompanyCustomAttributes", func(t *testing.T) {
		// A. Empty keys -> 422
		wEmpty := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/destroy_custom_attributes", accID, company1ID), map[string]any{
			"custom_attributes": []string{},
		}, tokenAdmin)
		if wEmpty.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for empty custom_attributes array, got %d: %s", wEmpty.Code, wEmpty.Body.String())
		}

		// B. Remove "tier" attribute, leave "region" intact
		wDestroy := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/destroy_custom_attributes", accID, company1ID), map[string]any{
			"custom_attributes": []string{"tier"},
		}, tokenAdmin)
		if wDestroy.Code != http.StatusOK {
			t.Fatalf("expected 200 for destroying custom attributes, got %d: %s", wDestroy.Code, wDestroy.Body.String())
		}

		// Verify database state
		var updatedComp domain.Company
		_ = db.First(&updatedComp, company1ID).Error
		var attrs map[string]any
		_ = json.Unmarshal([]byte(updatedComp.CustomAttributes), &attrs)
		if _, exists := attrs["tier"]; exists {
			t.Errorf("expected tier attribute to be removed, but still present")
		}
		if attrs["region"] != "APAC" {
			t.Errorf("expected region attribute APAC to remain, got %v", attrs["region"])
		}

		// C. Alternative route without :id in URL (providing company_id in body)
		wAlt := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies/destroy_custom_attributes", accID), map[string]any{
			"company_id":        company1ID,
			"custom_attributes": []string{"region"},
		}, tokenAdmin)
		if wAlt.Code != http.StatusOK {
			t.Fatalf("expected 200 for alternative destroy endpoint, got %d: %s", wAlt.Code, wAlt.Body.String())
		}
		_ = db.First(&updatedComp, company1ID).Error
		attrs = make(map[string]any)
		_ = json.Unmarshal([]byte(updatedComp.CustomAttributes), &attrs)
		if _, exists := attrs["region"]; exists {
			t.Errorf("expected region attribute to be removed")
		}
	})

	// ==========================================
	// Test 4: Delete Company Avatar
	// ==========================================
	t.Run("4_DeleteCompanyAvatar", func(t *testing.T) {
		// Ensure company 2 has avatar_url
		var c2 domain.Company
		_ = db.First(&c2, company2ID).Error
		if c2.AvatarURL == "" {
			t.Fatalf("company 2 should have initial avatar_url")
		}

		wDel := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/avatar", accID, company2ID), nil, tokenAdmin)
		if wDel.Code != http.StatusOK {
			t.Fatalf("expected 200 for delete avatar, got %d: %s", wDel.Code, wDel.Body.String())
		}

		var delResp struct {
			AvatarURL string `json:"avatar_url"`
			Success   bool   `json:"success"`
		}
		_ = json.Unmarshal(wDel.Body.Bytes(), &delResp)
		if !delResp.Success || delResp.AvatarURL != "" {
			t.Errorf("expected success with empty avatar_url in response, got %+v", delResp)
		}

		// Verify database
		_ = db.First(&c2, company2ID).Error
		if c2.AvatarURL != "" {
			t.Errorf("expected avatar_url to be empty in db, got %q", c2.AvatarURL)
		}
	})

	// ==========================================
	// Test 5: Role and Permission Enforcement
	// ==========================================
	t.Run("5_RoleAndPermissionEnforcement", func(t *testing.T) {
		// Agent without contact_manage permission tries to destroy custom attributes -> 403
		wDestroy := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/destroy_custom_attributes", accID, company1ID), map[string]any{
			"custom_attributes": []string{"test"},
		}, tokenAgent)
		if wDestroy.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent without contact_manage on destroy_custom_attributes, got %d", wDestroy.Code)
		}

		// Agent without contact_manage permission tries to delete avatar -> 403
		wAvatar := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/avatar", accID, company1ID), nil, tokenAgent)
		if wAvatar.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent without contact_manage on delete avatar, got %d", wAvatar.Code)
		}

		// Read operations (Search companies, Search company contacts) should succeed for agent
		wSearchComp := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/search?q=Stark", accID), nil, tokenAgent)
		if wSearchComp.Code != http.StatusOK {
			t.Fatalf("expected 200 for agent reading company search, got %d", wSearchComp.Code)
		}

		wSearchCt := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/companies/%d/contacts/search?q=Pepper", accID, company2ID), nil, tokenAgent)
		if wSearchCt.Code != http.StatusOK {
			t.Fatalf("expected 200 for agent reading company contacts search, got %d", wSearchCt.Code)
		}
	})
}
