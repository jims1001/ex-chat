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
	"github.com/gin-gonic/gin"
)

func TestCompanyExtensions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_company_ext_123456",
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

	getData := func(body []byte) any {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		if d, ok := m["data"]; ok {
			return d
		}
		return m
	}

	getMap := func(body []byte) map[string]any {
		d := getData(body)
		if m, ok := d.(map[string]any); ok {
			return m
		}
		return nil
	}

	// 1. Sign up Account 1 (Acme Global)
	w1 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Acme Global B2B",
		"name":         "Enterprise Admin",
		"email":        "admin@acmeb2b.com",
		"password":     "Password123!",
	}, "")
	if w1.Code != http.StatusCreated {
		t.Fatalf("failed to sign up account 1: %s", w1.Body.String())
	}
	var authResp1 struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &authResp1)
	token1 := authResp1.Data.Token

	// 2. Sign up Account 2 (Rival Corp)
	w2 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Rival Holdings",
		"name":         "Rival Admin",
		"email":        "admin@rivalholdings.com",
		"password":     "Password123!",
	}, "")
	var authResp2 struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	token2 := authResp2.Data.Token

	// 3. Create Company under Account 1
	wComp := doReq(http.MethodPost, "/api/v1/accounts/1/companies", map[string]any{
		"name":        "MegaCorp Enterprises",
		"domain":      "megacorp.com",
		"industry":    "Financial Services",
		"description": "Global enterprise client with high-tier SLA",
	}, token1)
	if wComp.Code != http.StatusOK && wComp.Code != http.StatusCreated {
		t.Fatalf("failed to create company: %s", wComp.Body.String())
	}
	compMap := getMap(wComp.Body.Bytes())
	companyID := uint(compMap["id"].(float64))

	// Create Inbox under Account 1
	inbox := domain.Inbox{
		AccountID:    1,
		Name:         "Enterprise Support Desk",
		ChannelType:  "Channel::Email",
		WebsiteToken: "enterprise_desk_token_123",
	}
	db.Create(&inbox)

	// Create 2 Contacts affiliated with MegaCorp
	compIDPtr := &companyID
	contact1 := domain.Contact{
		AccountID: 1,
		CompanyID: compIDPtr,
		Name:      "Alice VP",
		Email:     "alice@megacorp.com",
	}
	contact2 := domain.Contact{
		AccountID: 1,
		CompanyID: compIDPtr,
		Name:      "Bob Director",
		Email:     "bob@megacorp.com",
	}
	// Create 1 unaffiliated Contact
	contact3 := domain.Contact{
		AccountID: 1,
		Name:      "Charlie Freelancer",
		Email:     "charlie@freelance.org",
	}
	db.Create(&contact1)
	db.Create(&contact2)
	db.Create(&contact3)

	// Create Conversations
	conv1 := domain.Conversation{
		AccountID:      1,
		InboxID:        inbox.ID,
		ContactID:      contact1.ID,
		Status:         domain.ConversationStatusOpen,
		Priority:       "urgent",
		LastActivityAt: time.Now().UTC().Add(-1 * time.Hour),
		CreatedAt:      time.Now().UTC().Add(-3 * time.Hour),
	}
	conv2 := domain.Conversation{
		AccountID:      1,
		InboxID:        inbox.ID,
		ContactID:      contact1.ID,
		Status:         domain.ConversationStatusResolved,
		Priority:       "low",
		LastActivityAt: time.Now().UTC().Add(-2 * time.Hour),
		CreatedAt:      time.Now().UTC().Add(-4 * time.Hour),
	}
	conv3 := domain.Conversation{
		AccountID:      1,
		InboxID:        inbox.ID,
		ContactID:      contact2.ID,
		Status:         domain.ConversationStatusOpen,
		Priority:       "medium",
		LastActivityAt: time.Now().UTC().Add(-30 * time.Minute),
		CreatedAt:      time.Now().UTC().Add(-1 * time.Hour),
	}
	convOutside := domain.Conversation{
		AccountID:      1,
		InboxID:        inbox.ID,
		ContactID:      contact3.ID,
		Status:         domain.ConversationStatusOpen,
		Priority:       "high",
		LastActivityAt: time.Now().UTC(),
	}
	db.Create(&conv1)
	db.Create(&conv2)
	db.Create(&conv3)
	db.Create(&convOutside)

	// Create Messages & Attachments
	msg1 := domain.Message{
		AccountID:      1,
		ConversationID: conv1.ID,
		SenderType:     "contact",
		SenderID:       contact1.ID,
		Content:        "Quarterly budget statement",
	}
	db.Create(&msg1)
	att1 := domain.Attachment{
		AccountID: 1,
		MessageID: msg1.ID,
		FileType:  "application/pdf",
		DataURL:   "https://storage.acme.com/megacorp_q3_budget.pdf",
		FileSize:  524288,
	}
	db.Create(&att1)

	msg3 := domain.Message{
		AccountID:      1,
		ConversationID: conv3.ID,
		SenderType:     "contact",
		SenderID:       contact2.ID,
		Content:        "Architecture diagram photo",
	}
	db.Create(&msg3)
	att2 := domain.Attachment{
		AccountID: 1,
		MessageID: msg3.ID,
		FileType:  "image/png",
		DataURL:   "https://storage.acme.com/architecture.png",
		FileSize:  102400,
	}
	db.Create(&att2)

	// =========================================================================
	// Scenario 1: Company Conversations Query (Full Company Penetration)
	// =========================================================================
	t.Run("Scenario1_CompanyConversations_Query", func(t *testing.T) {
		// 1.1 All conversations across MegaCorp's contacts
		w := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/conversations", companyID), nil, token1)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		data := getMap(w.Body.Bytes())
		total := int(data["total"].(float64))
		if total != 3 {
			t.Fatalf("expected 3 conversations for MegaCorp, got %d", total)
		}
		items := data["items"].([]any)
		if len(items) != 3 {
			t.Fatalf("expected 3 items returned, got %d", len(items))
		}

		// 1.2 Filter by status = open
		wOpen := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/conversations?status=open", companyID), nil, token1)
		dataOpen := getMap(wOpen.Body.Bytes())
		if int(dataOpen["total"].(float64)) != 2 {
			t.Fatalf("expected 2 open conversations, got %v", dataOpen["total"])
		}

		// 1.3 Filter by contact_id = contact1.ID
		wContact := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/conversations?contact_id=%d", companyID, contact1.ID), nil, token1)
		dataContact := getMap(wContact.Body.Bytes())
		if int(dataContact["total"].(float64)) != 2 {
			t.Fatalf("expected 2 conversations for contact1, got %v", dataContact["total"])
		}
	})

	// =========================================================================
	// Scenario 2: Direct Company Notes Management (CRUD)
	// =========================================================================
	var directNoteID uint
	t.Run("Scenario2_CompanyNotes_CRUD", func(t *testing.T) {
		// 2.1 Create direct company note
		wCreate := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes", companyID), map[string]any{
			"content": "Key Account Tier 1: annual enterprise agreement renewal due Nov 15th",
		}, token1)
		if wCreate.Code != http.StatusOK {
			t.Fatalf("expected 200 for creating company note, got %d: %s", wCreate.Code, wCreate.Body.String())
		}
		noteData := getMap(wCreate.Body.Bytes())
		directNoteID = uint(noteData["id"].(float64))
		if noteData["content"] != "Key Account Tier 1: annual enterprise agreement renewal due Nov 15th" {
			t.Fatalf("unexpected note content: %v", noteData["content"])
		}

		// 2.2 Get single direct company note
		wGet := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes/%d", companyID, directNoteID), nil, token1)
		if wGet.Code != http.StatusOK {
			t.Fatalf("expected 200 on get company note, got %d", wGet.Code)
		}

		// 2.3 Update direct company note
		wUpdate := doReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes/%d", companyID, directNoteID), map[string]any{
			"content": "Key Account Tier 1: renewal moved forward to Oct 30th",
		}, token1)
		if wUpdate.Code != http.StatusOK {
			t.Fatalf("expected 200 on update company note, got %d: %s", wUpdate.Code, wUpdate.Body.String())
		}
		updatedData := getMap(wUpdate.Body.Bytes())
		if updatedData["content"] != "Key Account Tier 1: renewal moved forward to Oct 30th" {
			t.Fatalf("content was not updated: %v", updatedData["content"])
		}
	})

	// =========================================================================
	// Scenario 3: Unified Company & Contact Notes Timeline (Scopes: all, company, contacts)
	// =========================================================================
	t.Run("Scenario3_UnifiedNotes_Aggregation_And_Scopes", func(t *testing.T) {
		// Add 2 contact notes: 1 on Alice and 1 on Bob
		wNoteAlice := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes", contact1.ID), map[string]any{
			"content": "Alice prefers communicating via Slack or video calls",
		}, token1)
		if wNoteAlice.Code != http.StatusOK && wNoteAlice.Code != http.StatusCreated {
			t.Fatalf("failed to create contact note on Alice: %s", wNoteAlice.Body.String())
		}

		wNoteBob := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/notes", contact2.ID), map[string]any{
			"content": "Bob is the technical evaluation lead",
		}, token1)
		if wNoteBob.Code != http.StatusOK && wNoteBob.Code != http.StatusCreated {
			t.Fatalf("failed to create contact note on Bob: %s", wNoteBob.Body.String())
		}

		// 3.1 Scope = all (default)
		wAll := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes", companyID), nil, token1)
		if wAll.Code != http.StatusOK {
			t.Fatalf("expected 200 on unified notes, got %d", wAll.Code)
		}
		dataAll := getMap(wAll.Body.Bytes())
		if int(dataAll["total"].(float64)) != 3 {
			t.Fatalf("expected 3 unified notes (1 company + 2 contact notes), got %v", dataAll["total"])
		}
		items := dataAll["items"].([]any)
		// Check that contact note has contact_name and note_type = "contact"
		hasContactNote := false
		hasCompanyNote := false
		for _, item := range items {
			m := item.(map[string]any)
			if m["note_type"] == "contact" {
				hasContactNote = true
				if m["contact_name"] == "" {
					t.Fatalf("expected contact_name on contact note item, got empty")
				}
			}
			if m["note_type"] == "company" {
				hasCompanyNote = true
			}
		}
		if !hasContactNote || !hasCompanyNote {
			t.Fatalf("expected both company and contact note types in timeline")
		}

		// 3.2 Scope = company
		wCompanyOnly := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes?scope=company", companyID), nil, token1)
		dataCompOnly := getMap(wCompanyOnly.Body.Bytes())
		if int(dataCompOnly["total"].(float64)) != 1 {
			t.Fatalf("expected 1 company-only note, got %v", dataCompOnly["total"])
		}

		// 3.3 Scope = contacts
		wContactsOnly := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes?scope=contacts", companyID), nil, token1)
		dataContactsOnly := getMap(wContactsOnly.Body.Bytes())
		if int(dataContactsOnly["total"].(float64)) != 2 {
			t.Fatalf("expected 2 contact notes, got %v", dataContactsOnly["total"])
		}
	})

	// =========================================================================
	// Scenario 4: Company Notes Summary
	// =========================================================================
	t.Run("Scenario4_CompanyNotes_Summary", func(t *testing.T) {
		wSummary := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes/summary", companyID), nil, token1)
		if wSummary.Code != http.StatusOK {
			t.Fatalf("expected 200 on notes summary, got %d", wSummary.Code)
		}
		data := getMap(wSummary.Body.Bytes())
		if int(data["total_notes"].(float64)) != 3 {
			t.Fatalf("expected total_notes 3, got %v", data["total_notes"])
		}
		if int(data["company_notes_count"].(float64)) != 1 {
			t.Fatalf("expected company_notes_count 1, got %v", data["company_notes_count"])
		}
		if int(data["contact_notes_count"].(float64)) != 2 {
			t.Fatalf("expected contact_notes_count 2, got %v", data["contact_notes_count"])
		}
		if int(data["contacts_with_notes"].(float64)) != 2 {
			t.Fatalf("expected contacts_with_notes 2, got %v", data["contacts_with_notes"])
		}
		if data["last_note_at"] == nil {
			t.Fatalf("expected last_note_at timestamp, got nil")
		}
	})

	// =========================================================================
	// Scenario 5: Company Stats & Company-wide Attachments Query
	// =========================================================================
	t.Run("Scenario5_CompanyStats_And_Attachments", func(t *testing.T) {
		// 5.1 Company stats
		wStats := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/stats", companyID), nil, token1)
		if wStats.Code != http.StatusOK {
			t.Fatalf("expected 200 on company stats, got %d", wStats.Code)
		}
		stats := getMap(wStats.Body.Bytes())
		if int(stats["contacts_count"].(float64)) != 2 {
			t.Fatalf("expected contacts_count 2, got %v", stats["contacts_count"])
		}
		if int(stats["conversations_count"].(float64)) != 3 {
			t.Fatalf("expected conversations_count 3, got %v", stats["conversations_count"])
		}
		if int(stats["open_conversations_count"].(float64)) != 2 {
			t.Fatalf("expected open_conversations_count 2, got %v", stats["open_conversations_count"])
		}
		if int(stats["resolved_conversations_count"].(float64)) != 1 {
			t.Fatalf("expected resolved_conversations_count 1, got %v", stats["resolved_conversations_count"])
		}
		if int(stats["attachments_count"].(float64)) != 2 {
			t.Fatalf("expected attachments_count 2, got %v", stats["attachments_count"])
		}
		if int(stats["notes_count"].(float64)) != 3 {
			t.Fatalf("expected notes_count 3, got %v", stats["notes_count"])
		}

		// 5.2 Company-wide attachments
		wAtt := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/attachments", companyID), nil, token1)
		if wAtt.Code != http.StatusOK {
			t.Fatalf("expected 200 on company attachments, got %d", wAtt.Code)
		}
		attData := getMap(wAtt.Body.Bytes())
		if int(attData["total"].(float64)) != 2 {
			t.Fatalf("expected 2 attachments for company, got %v", attData["total"])
		}

		// Filter by file_type = image
		wAttImg := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/companies/%d/attachments?file_type=image", companyID), nil, token1)
		attImgData := getMap(wAttImg.Body.Bytes())
		if int(attImgData["total"].(float64)) != 1 {
			t.Fatalf("expected 1 image attachment, got %v", attImgData["total"])
		}
	})

	// =========================================================================
	// Scenario 6: Multi-Tenant Security Isolation (Account 2 access denied)
	// =========================================================================
	t.Run("Scenario6_MultiTenant_Security_Isolation", func(t *testing.T) {
		// 6.1 Conversations
		wConv := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/companies/%d/conversations", companyID), nil, token2)
		if wConv.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant conversations query, got %d", wConv.Code)
		}

		// 6.2 Notes
		wNotes := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/companies/%d/notes", companyID), nil, token2)
		if wNotes.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant notes query, got %d", wNotes.Code)
		}

		// 6.3 Notes Summary
		wSummary := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/companies/%d/notes/summary", companyID), nil, token2)
		if wSummary.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant notes summary, got %d", wSummary.Code)
		}

		// 6.4 Create Note
		wCreate := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/2/companies/%d/notes", companyID), map[string]any{
			"content": "Malicious intrusion attempt",
		}, token2)
		if wCreate.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant create note, got %d", wCreate.Code)
		}

		// 6.5 Stats
		wStats := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/companies/%d/stats", companyID), nil, token2)
		if wStats.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant stats query, got %d", wStats.Code)
		}

		// 6.6 Attachments
		wAtt := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/companies/%d/attachments", companyID), nil, token2)
		if wAtt.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant attachments query, got %d", wAtt.Code)
		}

		// 6.7 Delete direct note
		wDel := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/1/companies/%d/notes/%d", companyID, directNoteID), nil, token1)
		if wDel.Code != http.StatusOK {
			t.Fatalf("expected 200 on note delete, got %d", wDel.Code)
		}
	})
}
