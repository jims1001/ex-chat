package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

func TestContactManagementEnhancements(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "contact_mgmt_enhancements_secret_32b!",
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
		"name":         "Contact Admin",
		"email":        "contact_admin@example.com",
		"password":     "Password123!",
		"account_name": "Contact Management Org",
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
	adminUser := authResp.Data.User

	// Create an agent user for RBAC test
	agentUser := domain.User{
		Name:         "Plain Agent",
		Email:        "plain_agent_crm@example.com",
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	db.Create(&agentUser)
	db.Create(&domain.AccountUser{AccountID: accountID, UserID: agentUser.ID, Role: domain.RoleAgent})

	agentToken, err := auth.GenerateToken(&agentUser, cfg.JWTSecret, 24)
	if err != nil {
		t.Fatalf("failed to generate agent token: %v", err)
	}

	// Create test inbox and contacts
	inbox := domain.Inbox{
		AccountID:   accountID,
		Name:        "Main CRM Inbox",
		ChannelType: "Channel::WebWidget",
	}
	db.Create(&inbox)

	contact1 := domain.Contact{
		AccountID:        accountID,
		Name:             "Alice Cooper",
		Email:            "alice.cooper@example.com",
		PhoneNumber:      "+15550101",
		Identifier:       "CRM_ALICE_001",
		AvatarURL:        "https://cdn.example.com/alice.png",
		CustomAttributes: `{"customer_tier":"vip","city":"New York","industry":"Finance"}`,
	}
	contact2 := domain.Contact{
		AccountID:        accountID,
		Name:             "Bob Builder",
		Email:            "bob.builder@example.com",
		PhoneNumber:      "+15550102",
		Identifier:       "CRM_BOB_002",
		CustomAttributes: `{"customer_tier":"standard","city":"Austin"}`,
	}
	contact3 := domain.Contact{
		AccountID:        accountID,
		Name:             "Charlie Brown",
		Email:            "charlie.brown@example.com",
		PhoneNumber:      "+15550103",
		Identifier:       "CRM_CHARLIE_003",
		CustomAttributes: `{"city":"Chicago"}`,
	}
	db.Create(&contact1)
	db.Create(&contact2)
	db.Create(&contact3)

	// Attach a label to contact1
	labelVIP := domain.Label{AccountID: accountID, Title: "KeyClient", Color: "#FF0000"}
	db.Create(&labelVIP)
	db.Create(&domain.ContactLabel{ContactID: contact1.ID, LabelID: labelVIP.ID})

	// Create an ongoing conversation for Alice (open), and a resolved conversation for Bob (resolved)
	convAlice := domain.Conversation{
		AccountID: accountID,
		InboxID:   inbox.ID,
		ContactID: contact1.ID,
		Status:    "open",
	}
	convBob := domain.Conversation{
		AccountID: accountID,
		InboxID:   inbox.ID,
		ContactID: contact2.ID,
		Status:    "resolved",
	}
	db.Create(&convAlice)
	db.Create(&convBob)

	// ==========================================
	// Test 1: 活跃联系人列表 (GET /contacts/active)
	// ==========================================
	t.Run("ListActiveContacts", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/active", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get active contacts failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Payload []domain.Contact `json:"payload"`
			Meta    struct {
				Count int64 `json:"count"`
			} `json:"meta"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)

		// Alice has an open conversation; Bob is resolved; Charlie has no conversation.
		if resp.Meta.Count != 1 || len(resp.Payload) != 1 {
			t.Fatalf("expected 1 active contact (Alice), got count=%d, payload_len=%d", resp.Meta.Count, len(resp.Payload))
		}
		if resp.Payload[0].ID != contact1.ID {
			t.Errorf("expected contact id %d (Alice), got %d", contact1.ID, resp.Payload[0].ID)
		}
	})

	// ==========================================
	// Test 2: 联系人专用搜索 (GET /contacts/search)
	// ==========================================
	t.Run("SearchContacts", func(t *testing.T) {
		// Search by query "Alice"
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/search?q=Alice", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("search contacts failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Payload []domain.Contact `json:"payload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Payload) != 1 || resp.Payload[0].Email != "alice.cooper@example.com" {
			t.Fatalf("expected 1 result with Alice, got: %+v", resp.Payload)
		}

		// Search by phone number
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/search?q=5550102", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("search by phone failed: code=%d", w.Code)
		}
		var phoneResp struct {
			Payload []domain.Contact `json:"payload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &phoneResp)
		if len(phoneResp.Payload) != 1 || phoneResp.Payload[0].ID != contact2.ID {
			t.Fatalf("expected Bob by phone search, got: %+v", phoneResp.Payload)
		}
	})

	// ==========================================
	// Test 3: 联系人高级筛选 (POST /contacts/filter)
	// ==========================================
	t.Run("FilterContacts", func(t *testing.T) {
		// Filter rule: city contains "New York"
		filterBody, _ := json.Marshal(map[string]any{
			"payload": []map[string]any{
				{
					"attribute_key":   "custom_attributes.city",
					"filter_operator": "contains",
					"values":          []string{"New York"},
				},
			},
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/filter", accountID), bytes.NewReader(filterBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("filter contacts failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var filterResp struct {
			Payload []domain.Contact `json:"payload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &filterResp)
		if len(filterResp.Payload) != 1 || filterResp.Payload[0].ID != contact1.ID {
			t.Fatalf("expected 1 contact matching New York (Alice), got: %+v", filterResp.Payload)
		}

		// Filter rule: labels equal_to KeyClient
		labelFilterBody, _ := json.Marshal(map[string]any{
			"payload": []map[string]any{
				{
					"attribute_key":   "labels",
					"filter_operator": "equal_to",
					"values":          []string{"KeyClient"},
				},
			},
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/filter", accountID), bytes.NewReader(labelFilterBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("filter by label failed: code=%d", w.Code)
		}
		var labelResp struct {
			Payload []domain.Contact `json:"payload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &labelResp)
		if len(labelResp.Payload) != 1 || labelResp.Payload[0].ID != contact1.ID {
			t.Fatalf("expected 1 contact matching KeyClient, got: %+v", labelResp.Payload)
		}
	})

	// ==========================================
	// Test 4: 联系人直接导入入口 (POST /contacts/import)
	// ==========================================
	t.Run("ImportContacts", func(t *testing.T) {
		// 1. Direct JSON import (1 new, 1 update)
		jsonImportBody, _ := json.Marshal(map[string]any{
			"contacts": []map[string]any{
				{
					"name":        "Alice Cooper Updated",
					"email":       "alice.cooper@example.com", // existing email -> upsert
					"phone_number": "+15559999",
				},
				{
					"name":        "David Miller",
					"email":       "david.miller@example.com", // new contact
					"phone_number": "+15550104",
					"identifier":  "CRM_DAVID_004",
				},
			},
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/import", accountID), bytes.NewReader(jsonImportBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("import contacts json failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var importResp struct {
			Success       bool `json:"success"`
			ImportedCount int  `json:"imported_count"`
			UpdatedCount  int  `json:"updated_count"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &importResp)
		if importResp.ImportedCount != 1 || importResp.UpdatedCount != 1 {
			t.Fatalf("expected 1 imported and 1 updated, got imported=%d, updated=%d", importResp.ImportedCount, importResp.UpdatedCount)
		}

		// Verify Alice was updated
		var updatedAlice domain.Contact
		db.First(&updatedAlice, contact1.ID)
		if updatedAlice.Name != "Alice Cooper Updated" || updatedAlice.PhoneNumber != "+15559999" {
			t.Errorf("Alice update failed: %+v", updatedAlice)
		}

		// 2. Multipart CSV file import
		var b bytes.Buffer
		writer := multipart.NewWriter(&b)
		part, _ := writer.CreateFormFile("import_file", "contacts.csv")
		csvContent := "name,email,phone_number,identifier\nEmma Watson,emma@example.com,+15550105,CRM_EMMA_005\n"
		_, _ = part.Write([]byte(csvContent))
		writer.Close()

		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/import", accountID), &b)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("import contacts multipart CSV failed: code=%d, body=%s", w.Code, w.Body.String())
		}
		var csvResp struct {
			ImportedCount int `json:"imported_count"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &csvResp)
		if csvResp.ImportedCount != 1 {
			t.Fatalf("expected 1 imported from CSV, got %d", csvResp.ImportedCount)
		}
	})

	// ==========================================
	// Test 5: 删除单个自定义属性 (POST /contacts/:id/destroy_custom_attributes)
	// ==========================================
	t.Run("DestroyCustomAttributes", func(t *testing.T) {
		// contact1 has {"customer_tier":"vip","city":"New York","industry":"Finance"}
		// Remove "industry"
		delAttrBody, _ := json.Marshal(map[string]any{
			"custom_attributes": []string{"industry"},
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/%d/destroy_custom_attributes", accountID, contact1.ID), bytes.NewReader(delAttrBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("destroy custom attributes failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var freshContact domain.Contact
		db.First(&freshContact, contact1.ID)
		var attrs map[string]any
		_ = json.Unmarshal([]byte(freshContact.CustomAttributes), &attrs)
		if _, exists := attrs["industry"]; exists {
			t.Fatalf("expected 'industry' to be deleted, but still exists: %v", attrs)
		}
		if attrs["city"] != "New York" || attrs["customer_tier"] != "vip" {
			t.Fatalf("expected other attributes to remain intact, got: %v", attrs)
		}
	})

	// ==========================================
	// Test 6: 删除联系人头像 (DELETE /contacts/:id/avatar)
	// ==========================================
	t.Run("DeleteContactAvatar", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/contacts/%d/avatar", accountID, contact1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete contact avatar failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var freshContact domain.Contact
		db.First(&freshContact, contact1.ID)
		if freshContact.AvatarURL != "" {
			t.Fatalf("expected avatar_url to be empty, got: %s", freshContact.AvatarURL)
		}
	})

	// ==========================================
	// Test 7: 联系人备注详情读取 (GET /contacts/:id/notes/:note_id)
	// ==========================================
	t.Run("GetContactNoteDetails", func(t *testing.T) {
		// 1. Create a note for contact1
		note := domain.ContactNote{
			AccountID: accountID,
			ContactID: contact1.ID,
			UserID:    adminUser.ID,
			Content:   "Customer expressed interest in enterprise volume pricing.",
		}
		db.Create(&note)

		// 2. Read note details
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/%d/notes/%d", accountID, contact1.ID, note.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get contact note failed: code=%d, body=%s", w.Code, w.Body.String())
		}

		var noteResp struct {
			Data domain.ContactNote `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &noteResp)
		if noteResp.Data.ID != note.ID || noteResp.Data.Content != "Customer expressed interest in enterprise volume pricing." {
			t.Fatalf("unexpected note details: %+v", noteResp.Data)
		}
		if noteResp.Data.User == nil || noteResp.Data.User.ID != adminUser.ID {
			t.Fatalf("expected preloaded user in note, got: %+v", noteResp.Data.User)
		}

		// 3. Request non-existent note
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/%d/notes/99999", accountID, contact1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent note, got %d", w.Code)
		}
	})

	// ==========================================
	// Test 8: 权限控制拦截 (RBAC Permission Enforcement)
	// ==========================================
	t.Run("RoleAndPermissionEnforcement", func(t *testing.T) {
		// Plain agent without contact_manage tries to import contacts -> 403 Forbidden
		importBody, _ := json.Marshal(map[string]any{
			"contacts": []map[string]any{{"name": "Unauthorized Contact"}},
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/import", accountID), bytes.NewReader(importBody))
		req.Header.Set("Authorization", "Bearer "+agentToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent import, got %d", w.Code)
		}

		// Plain agent tries to delete avatar -> 403 Forbidden
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/contacts/%d/avatar", accountID, contact1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+agentToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent avatar delete, got %d", w.Code)
		}

		// Plain agent tries to destroy custom attributes -> 403 Forbidden
		delBody, _ := json.Marshal(map[string]any{"custom_attributes": []string{"city"}})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/%d/destroy_custom_attributes", accountID, contact1.ID), bytes.NewReader(delBody))
		req.Header.Set("Authorization", "Bearer "+agentToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for agent destroy custom attributes, got %d", w.Code)
		}
	})
}
