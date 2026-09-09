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

func TestContactExtensions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_contact_ext_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	r := router.SetupRouter(cfg, db, hub)

	// Helper for HTTP requests
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

	getSlice := func(body []byte) []any {
		d := getData(body)
		if s, ok := d.([]any); ok {
			return s
		}
		return nil
	}

	// 1. Register Account 1 & User 1
	signUpPayload := map[string]any{
		"account_name": "Acme Global Contact CRM",
		"name":         "CRM Lead",
		"email":        "crm.lead@acme.com",
		"password":     "Password123!",
	}
	w1 := doReq(http.MethodPost, "/auth/sign_up", signUpPayload, "")
	if w1.Code != http.StatusCreated {
		t.Fatalf("sign_up expected 201, got %d: %s", w1.Code, w1.Body.String())
	}
	var authResp1 struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &authResp1)
	token1 := authResp1.Data.Token

	// 2. Register Account 2 & User 2 (For multi-tenant isolation tests)
	signUpPayload2 := map[string]any{
		"account_name": "Rival Corp",
		"name":         "Rival Director",
		"email":        "director@rival.com",
		"password":     "Password123!",
	}
	w2 := doReq(http.MethodPost, "/auth/sign_up", signUpPayload2, "")
	var authResp2 struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	token2 := authResp2.Data.Token

	// 3. Create Contact 1 under Account 1
	wContact1 := doReq(http.MethodPost, "/api/v1/accounts/1/contacts", map[string]any{
		"name":         "Alice Morgan",
		"email":        "alice.morgan@example.com",
		"phone_number": "+15551234567",
		"identifier":   "alice_vip_99",
	}, token1)
	if wContact1.Code != http.StatusOK && wContact1.Code != http.StatusCreated {
		t.Fatalf("failed to create contact 1: %s", wContact1.Body.String())
	}
	c1Map := getMap(wContact1.Body.Bytes())
	contact1ID := uint(c1Map["id"].(float64))

	// Create Inboxes under Account 1 directly in DB
	inboxWeb := domain.Inbox{
		AccountID:    1,
		Name:         "Live Web Chat",
		ChannelType:  "Channel::WebWidget",
		WebsiteToken: "web_widget_token_abc_1",
	}
	inboxEmail := domain.Inbox{
		AccountID:    1,
		Name:         "Support Email Helpdesk",
		ChannelType:  "Channel::Email",
		WebsiteToken: "email_token_abc_2",
	}
	inboxWhatsApp := domain.Inbox{
		AccountID:    1,
		Name:         "Official WhatsApp",
		ChannelType:  "Channel::Whatsapp",
		WebsiteToken: "whatsapp_token_abc_3",
	}
	db.Create(&inboxWeb)
	db.Create(&inboxEmail)
	db.Create(&inboxWhatsApp)

	// Create Conversations and Messages with Attachments for Contact 1
	conv1 := domain.Conversation{
		AccountID:      1,
		InboxID:        inboxWeb.ID,
		ContactID:      contact1ID,
		Status:         domain.ConversationStatusOpen,
		Priority:       "high",
		LastActivityAt: time.Now().UTC().Add(-2 * time.Hour),
		CreatedAt:      time.Now().UTC().Add(-2 * time.Hour),
	}
	db.Create(&conv1)

	msg1 := domain.Message{
		AccountID:      1,
		ConversationID: conv1.ID,
		SenderType:     "contact",
		SenderID:       contact1ID,
		Content:        "Here is the screenshot of my payment error",
		CreatedAt:      time.Now().UTC().Add(-110 * time.Minute),
	}
	db.Create(&msg1)

	att1 := domain.Attachment{
		AccountID: 1,
		MessageID: msg1.ID,
		FileType:  "image/png",
		DataURL:   "https://storage.acme.com/receipt.png",
		FileSize:  51200,
		CreatedAt: time.Now().UTC().Add(-110 * time.Minute),
	}
	att2 := domain.Attachment{
		AccountID: 1,
		MessageID: msg1.ID,
		FileType:  "application/pdf",
		DataURL:   "https://storage.acme.com/invoice.pdf",
		FileSize:  102400,
		CreatedAt: time.Now().UTC().Add(-110 * time.Minute),
	}
	db.Create(&att1)
	db.Create(&att2)

	msg2 := domain.Message{
		AccountID:      1,
		ConversationID: conv1.ID,
		SenderType:     "user",
		SenderID:       1,
		Content:        "Thank you, here is the refund receipt",
		CreatedAt:      time.Now().UTC().Add(-60 * time.Minute),
	}
	db.Create(&msg2)

	att3 := domain.Attachment{
		AccountID: 1,
		MessageID: msg2.ID,
		FileType:  "image/jpeg",
		DataURL:   "https://storage.acme.com/refund_confirmation.jpg",
		FileSize:  20480,
		CreatedAt: time.Now().UTC().Add(-60 * time.Minute),
	}
	db.Create(&att3)

	// Conversation 2 for Contact 1
	conv2 := domain.Conversation{
		AccountID:      1,
		InboxID:        inboxEmail.ID,
		ContactID:      contact1ID,
		Status:         domain.ConversationStatusResolved,
		Priority:       "low",
		LastActivityAt: time.Now().UTC().Add(-10 * time.Minute),
		CreatedAt:      time.Now().UTC().Add(-15 * time.Minute),
	}
	db.Create(&conv2)

	msg3 := domain.Message{
		AccountID:      1,
		ConversationID: conv2.ID,
		SenderType:     "contact",
		SenderID:       contact1ID,
		Content:        "Voice memo audio message",
		CreatedAt:      time.Now().UTC().Add(-12 * time.Minute),
	}
	db.Create(&msg3)

	att4 := domain.Attachment{
		AccountID: 1,
		MessageID: msg3.ID,
		FileType:  "audio/mpeg",
		DataURL:   "https://storage.acme.com/voicemail.mp3",
		FileSize:  40960,
		CreatedAt: time.Now().UTC().Add(-12 * time.Minute),
	}
	db.Create(&att4)

	// Create Contact 2 under Account 1 with separate attachment
	contact2 := domain.Contact{
		AccountID: 1,
		Name:      "Bob Brown",
		Email:     "bob@example.com",
	}
	db.Create(&contact2)
	convBob := domain.Conversation{
		AccountID: 1,
		InboxID:   inboxWeb.ID,
		ContactID: contact2.ID,
		Status:    domain.ConversationStatusOpen,
	}
	db.Create(&convBob)
	msgBob := domain.Message{
		AccountID:      1,
		ConversationID: convBob.ID,
		SenderType:     "contact",
		SenderID:       contact2.ID,
		Content:        "Bob's secret document",
	}
	db.Create(&msgBob)
	attBob := domain.Attachment{
		AccountID: 1,
		MessageID: msgBob.ID,
		FileType:  "image/png",
		DataURL:   "https://storage.acme.com/bob_photo.png",
		FileSize:  15000,
	}
	db.Create(&attBob)

	// =========================================================================
	// Scenario 1: Contact Attachments Query (Filter, Search, Pagination)
	// =========================================================================
	t.Run("Scenario1_ContactAttachments_List_And_Filter", func(t *testing.T) {
		// 1.1 All attachments for Contact 1
		w := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/attachments", contact1ID), nil, token1)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		data := getMap(w.Body.Bytes())
		total := int(data["total"].(float64))
		if total != 4 {
			t.Fatalf("expected total 4 attachments for Contact 1, got %d", total)
		}
		items := data["items"].([]any)
		if len(items) != 4 {
			t.Fatalf("expected 4 items in list, got %d", len(items))
		}
		// Verify metadata enrichment
		first := items[0].(map[string]any)
		if first["conversation_id"] == nil || first["message_content"] == nil || first["sender_type"] == nil {
			t.Fatalf("expected enriched message metadata, got %+v", first)
		}

		// 1.2 Filter by file_type = image
		wImg := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/attachments?file_type=image", contact1ID), nil, token1)
		dataImg := getMap(wImg.Body.Bytes())
		if int(dataImg["total"].(float64)) != 2 {
			t.Fatalf("expected 2 image attachments, got %v", dataImg["total"])
		}

		// 1.3 Filter by conversation_id = conv1.ID
		wConv := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/attachments?conversation_id=%d", contact1ID, conv1.ID), nil, token1)
		dataConv := getMap(wConv.Body.Bytes())
		if int(dataConv["total"].(float64)) != 3 {
			t.Fatalf("expected 3 attachments in conv1, got %v", dataConv["total"])
		}

		// 1.4 Filter by sender_type = contact
		wSender := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/attachments?sender_type=contact", contact1ID), nil, token1)
		dataSender := getMap(wSender.Body.Bytes())
		if int(dataSender["total"].(float64)) != 3 {
			t.Fatalf("expected 3 contact attachments, got %v", dataSender["total"])
		}

		// 1.5 Search by content keyword
		wSearch := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/attachments?q=refund", contact1ID), nil, token1)
		dataSearch := getMap(wSearch.Body.Bytes())
		if int(dataSearch["total"].(float64)) != 1 {
			t.Fatalf("expected 1 attachment matching 'refund', got %v", dataSearch["total"])
		}

		// 1.6 Pagination
		wPage := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/attachments?page=1&page_size=2", contact1ID), nil, token1)
		dataPage := getMap(wPage.Body.Bytes())
		pageItems := dataPage["items"].([]any)
		if len(pageItems) != 2 || int(dataPage["total_pages"].(float64)) != 2 {
			t.Fatalf("expected 2 items per page and 2 total pages, got %d items, %v total pages", len(pageItems), dataPage["total_pages"])
		}
	})

	// =========================================================================
	// Scenario 2: Contactable Inboxes Query & Channel Identification
	// =========================================================================
	t.Run("Scenario2_ContactableInboxes_Query", func(t *testing.T) {
		// Before linking any ContactInbox:
		// Contact 1 has email ("alice.morgan@example.com") and phone ("+15551234567")
		// Should be contactable on:
		// - inboxEmail (Channel::Email) with source_id = alice.morgan@example.com
		// - inboxWhatsApp (Channel::Whatsapp) with source_id = +15551234567
		// inboxWeb (Channel::WebWidget) is NOT contactable yet because web widget requires existing session token
		w := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/contactable_inboxes", contact1ID), nil, token1)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		list := getSlice(w.Body.Bytes())
		if len(list) != 2 {
			t.Fatalf("expected 2 contactable inboxes, got %d", len(list))
		}

		// Check alias route /inboxes
		wAlias := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/inboxes", contact1ID), nil, token1)
		if wAlias.Code != http.StatusOK {
			t.Fatalf("expected 200 from alias, got %d", wAlias.Code)
		}

		// Now link Contact 1 with WebWidget Inbox via ContactInbox
		wLink := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/contact_inboxes", contact1ID), map[string]any{
			"inbox_id":  inboxWeb.ID,
			"source_id": "widget_session_user_token_999",
		}, token1)
		if wLink.Code != http.StatusOK {
			t.Fatalf("expected 200 for linking contact inbox, got %d: %s", wLink.Code, wLink.Body.String())
		}

		// Re-query contactable inboxes: now all 3 inboxes should be contactable!
		wAfter := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/contactable_inboxes", contact1ID), nil, token1)
		listAfter := getSlice(wAfter.Body.Bytes())
		if len(listAfter) != 3 {
			t.Fatalf("expected 3 contactable inboxes after link, got %d", len(listAfter))
		}
	})

	// =========================================================================
	// Scenario 3: Contact Inboxes Management (CRUD & Idempotency)
	// =========================================================================
	t.Run("Scenario3_ContactInboxes_CRUD_And_Idempotency", func(t *testing.T) {
		// 3.1 List contact inboxes
		wList := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/contact_inboxes", contact1ID), nil, token1)
		list := getSlice(wList.Body.Bytes())
		if len(list) < 1 {
			t.Fatalf("expected at least 1 contact inbox, got %d", len(list))
		}
		ciMap := list[0].(map[string]any)
		ciID := uint(ciMap["id"].(float64))

		// 3.2 Idempotent update on existing inbox link
		wUpdate := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/contact_inboxes", contact1ID), map[string]any{
			"inbox_id":  inboxWeb.ID,
			"source_id": "widget_session_user_token_updated",
		}, token1)
		if wUpdate.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", wUpdate.Code, wUpdate.Body.String())
		}
		updatedMap := getMap(wUpdate.Body.Bytes())
		if updatedMap["source_id"] != "widget_session_user_token_updated" {
			t.Fatalf("expected source_id to be updated, got %v", updatedMap["source_id"])
		}

		// 3.3 Link Email inbox with auto-inferred source_id
		wAuto := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/contact_inboxes", contact1ID), map[string]any{
			"inbox_id": inboxEmail.ID,
		}, token1)
		if wAuto.Code != http.StatusOK {
			t.Fatalf("expected 200 for auto link, got %d: %s", wAuto.Code, wAuto.Body.String())
		}
		autoMap := getMap(wAuto.Body.Bytes())
		if autoMap["source_id"] != "alice.morgan@example.com" {
			t.Fatalf("expected auto inferred email, got %v", autoMap["source_id"])
		}
		emailCIID := uint(autoMap["id"].(float64))

		// 3.4 Delete ContactInbox by ID
		wDelID := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/contact_inboxes/%d", contact1ID, ciID), nil, token1)
		if wDelID.Code != http.StatusOK {
			t.Fatalf("expected 200 on delete by id, got %d: %s", wDelID.Code, wDelID.Body.String())
		}

		// 3.5 Delete ContactInbox by inbox_id alias
		wDelInbox := doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/inboxes/%d", contact1ID, inboxEmail.ID), nil, token1)
		if wDelInbox.Code != http.StatusOK {
			t.Fatalf("expected 200 on delete by inbox_id, got %d: %s", wDelInbox.Code, wDelInbox.Body.String())
		}

		// Verify deletion
		var count int64
		db.Model(&domain.ContactInbox{}).Where("id IN (?, ?)", ciID, emailCIID).Count(&count)
		if count != 0 {
			t.Fatalf("expected 0 contact inboxes, got %d", count)
		}
	})

	// =========================================================================
	// Scenario 4: Contact Conversations Query
	// =========================================================================
	t.Run("Scenario4_ContactConversations_Query", func(t *testing.T) {
		w := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/conversations", contact1ID), nil, token1)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		data := getMap(w.Body.Bytes())
		total := int(data["total"].(float64))
		if total != 2 {
			t.Fatalf("expected 2 conversations for Contact 1, got %d", total)
		}
		items := data["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("expected 2 conversations items, got %d", len(items))
		}

		// Filter by status = resolved
		wResolved := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/conversations?status=resolved", contact1ID), nil, token1)
		dataRes := getMap(wResolved.Body.Bytes())
		if int(dataRes["total"].(float64)) != 1 {
			t.Fatalf("expected 1 resolved conversation, got %v", dataRes["total"])
		}

		// Filter by inbox_id = inboxWeb.ID
		wInbox := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/conversations?inbox_id=%d", contact1ID, inboxWeb.ID), nil, token1)
		dataInbox := getMap(wInbox.Body.Bytes())
		if int(dataInbox["total"].(float64)) != 1 {
			t.Fatalf("expected 1 conversation for inboxWeb, got %v", dataInbox["total"])
		}
	})

	// =========================================================================
	// Scenario 5: Contact Stats / Profile Interaction Metrics
	// =========================================================================
	t.Run("Scenario5_ContactStats_Summary", func(t *testing.T) {
		w := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/1/contacts/%d/stats", contact1ID), nil, token1)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		data := getMap(w.Body.Bytes())
		if int(data["conversations_count"].(float64)) != 2 {
			t.Fatalf("expected 2 conversations_count, got %v", data["conversations_count"])
		}
		if int(data["open_conversations_count"].(float64)) != 1 {
			t.Fatalf("expected 1 open_conversations_count, got %v", data["open_conversations_count"])
		}
		if int(data["resolved_conversations_count"].(float64)) != 1 {
			t.Fatalf("expected 1 resolved_conversations_count, got %v", data["resolved_conversations_count"])
		}
		if int(data["messages_count"].(float64)) != 3 {
			t.Fatalf("expected 3 messages_count, got %v", data["messages_count"])
		}
		if int(data["attachments_count"].(float64)) != 4 {
			t.Fatalf("expected 4 attachments_count, got %v", data["attachments_count"])
		}
		if data["first_seen_at"] == nil || data["last_activity_at"] == nil {
			t.Fatalf("expected timestamps for first_seen_at and last_activity_at, got %+v", data)
		}
	})

	// =========================================================================
	// Scenario 6: Multi-Tenant Security & Access Boundary Isolation
	// =========================================================================
	t.Run("Scenario6_MultiTenant_Security_Isolation", func(t *testing.T) {
		// Account 2 attempts to query Contact 1 (Account 1)
		// 6.1 Attachments -> 404
		wAtt := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/contacts/%d/attachments", contact1ID), nil, token2)
		if wAtt.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant attachments query, got %d", wAtt.Code)
		}

		// 6.2 Contactable Inboxes -> 404
		wInboxes := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/contacts/%d/contactable_inboxes", contact1ID), nil, token2)
		if wInboxes.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant contactable inboxes query, got %d", wInboxes.Code)
		}

		// 6.3 Contact Inboxes list -> 404
		wCI := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/contacts/%d/contact_inboxes", contact1ID), nil, token2)
		if wCI.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant contact inboxes query, got %d", wCI.Code)
		}

		// 6.4 Create Contact Inbox -> 404
		wCreateCI := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/2/contacts/%d/contact_inboxes", contact1ID), map[string]any{
			"inbox_id": 1,
		}, token2)
		if wCreateCI.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant create contact inbox, got %d", wCreateCI.Code)
		}

		// 6.5 Conversations -> 404
		wConv := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/contacts/%d/conversations", contact1ID), nil, token2)
		if wConv.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant conversations query, got %d", wConv.Code)
		}

		// 6.6 Stats -> 404
		wStats := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/2/contacts/%d/stats", contact1ID), nil, token2)
		if wStats.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for cross-tenant stats query, got %d", wStats.Code)
		}
	})
}
