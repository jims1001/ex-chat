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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestPublicAPILifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:public_api_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_public_api_123",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user to obtain Account 1
	signUpPayload := map[string]string{
		"name":         "Public API Admin",
		"email":        fmt.Sprintf("pub_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Public API Platform Corp",
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
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	accountID := authResp.Data.Accounts[0].ID

	// 2. Create Inbox with website token
	websiteToken := fmt.Sprintf("pub_inbox_token_%d", time.Now().UnixNano())
	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "Public Web Channel",
		ChannelType:  "Channel::WebWidget",
		WebsiteToken: websiteToken,
	}
	inboxRepo := repository.NewInboxRepository(db)
	if err := inboxRepo.Create(&inbox); err != nil {
		t.Fatalf("failed to create inbox: %v", err)
	}

	contactIdentifier := "pub_cust_999"
	var contactID uint
	var conv1ID, conv2ID uint

	// =========================================================================
	// Scenario 1: 客户详情查询与增量属性更新 (Contact Show & Update)
	// =========================================================================
	t.Run("Scenario 1: Contact Show & Update with Custom Attributes", func(t *testing.T) {
		// 1. Create Contact
		createPayload := map[string]any{
			"name":        "Charlie Brown",
			"email":       "charlie@example.com",
			"phone_number": "+1-555-0100",
			"identifier":  contactIdentifier,
			"custom_attributes": map[string]any{
				"plan":   "basic",
				"locale": "en",
			},
		}
		b, _ := json.Marshal(createPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts", websiteToken), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create public contact failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var cResp struct {
			Data domain.Contact `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &cResp)
		contactID = cResp.Data.ID
		if contactID == 0 {
			t.Fatalf("expected contactID > 0")
		}

		// 2. Query contact by string identifier / source_id
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s", websiteToken, contactIdentifier), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get contact by identifier failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var getResp struct {
			Data domain.Contact `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &getResp)
		if getResp.Data.Name != "Charlie Brown" || getResp.Data.Email != "charlie@example.com" {
			t.Fatalf("unexpected contact info: name=%s email=%s", getResp.Data.Name, getResp.Data.Email)
		}

		// 3. Query contact by numeric ID (Dual ID/source_id compatibility test)
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d", websiteToken, contactID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get contact by numeric ID failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 4. Partial update contact info & shallow merge custom attributes
		updatePayload := map[string]any{
			"phone_number": "+1-800-555-0123",
			"custom_attributes": map[string]any{
				"plan":    "enterprise",
				"country": "Canada",
			},
		}
		b, _ = json.Marshal(updatePayload)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s", websiteToken, contactIdentifier), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("patch public contact failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var patchResp struct {
			Data domain.Contact `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &patchResp)
		if patchResp.Data.PhoneNumber != "+1-800-555-0123" {
			t.Fatalf("expected updated phone number, got %s", patchResp.Data.PhoneNumber)
		}

		var parsedAttrs map[string]any
		_ = json.Unmarshal([]byte(patchResp.Data.CustomAttributes), &parsedAttrs)
		if parsedAttrs["plan"] != "enterprise" || parsedAttrs["country"] != "Canada" || parsedAttrs["locale"] != "en" {
			t.Fatalf("custom attributes shallow merge failed, expected preserved locale: en, got %v", parsedAttrs)
		}
	})

	// =========================================================================
	// Scenario 2: 会话列表分页与单条会话详情 (Conversation List & Detail)
	// =========================================================================
	t.Run("Scenario 2: Public Conversation List and Detail Retrieval", func(t *testing.T) {
		// 1. Create Conversation 1 using numeric contact ID
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d/conversations", websiteToken, contactID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conv 1 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var c1Resp struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &c1Resp)
		conv1ID = c1Resp.Data.ID

		// 2. Create Conversation 2 using string contact identifier
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations", websiteToken, contactIdentifier), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conv 2 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var c2Resp struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &c2Resp)
		conv2ID = c2Resp.Data.ID

		// 3. Query Conversation List
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations", websiteToken, contactIdentifier), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list public conversations failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var listResp struct {
			Data struct {
				Conversations []domain.Conversation `json:"conversations"`
				Total         int64                 `json:"total"`
				Page          int                   `json:"page"`
				PageSize      int                   `json:"page_size"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &listResp)
		if listResp.Data.Total != 2 || len(listResp.Data.Conversations) != 2 {
			t.Fatalf("expected 2 conversations, got total=%d len=%d", listResp.Data.Total, len(listResp.Data.Conversations))
		}
		if listResp.Data.Conversations[0].ID != conv2ID || listResp.Data.Conversations[1].ID != conv1ID {
			t.Fatalf("expected conv2ID first then conv1ID in desc order")
		}

		// 4. Query Conversation Detail
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d", websiteToken, contactIdentifier, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get public conversation detail failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var detailResp struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &detailResp)
		if detailResp.Data.ID != conv1ID {
			t.Fatalf("expected conv ID %d, got %d", conv1ID, detailResp.Data.ID)
		}
	})

	// =========================================================================
	// Scenario 3: 历史消息分页读取与私有备注防泄露 (Message History & Private Notes Filtering)
	// =========================================================================
	t.Run("Scenario 3: Message History Reading with Private Note Leak Prevention", func(t *testing.T) {
		// 1. Post Public Customer Message 1
		m1 := map[string]string{
			"content": "Customer initial greeting",
			"echo_id": "echo_public_001",
		}
		b, _ := json.Marshal(m1)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/messages", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create public msg 1 failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 2. Post Public Customer Message 2
		m2 := map[string]string{
			"content": "Customer second message",
			"echo_id": "echo_public_002",
		}
		b, _ = json.Marshal(m2)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/messages", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create public msg 2 failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 3. Inject Agent Internal Private Note (private = true, MUST NOT LEAK to public visitor)
		privateNote := domain.Message{
			AccountID:      accountID,
			ConversationID: conv1ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       1,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        "INTERNAL NOTE: Client flagged for vip billing, keep confidential!",
			Private:        true,
			Status:         domain.MessageStatusSent,
		}
		if err := db.Create(&privateNote).Error; err != nil {
			t.Fatalf("failed to insert private note: %v", err)
		}

		// 4. Inject Agent Public Response (private = false, should be visible)
		publicReply := domain.Message{
			AccountID:      accountID,
			ConversationID: conv1ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       1,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        "Hello Charlie! We are here to help you.",
			Private:        false,
			Status:         domain.MessageStatusSent,
		}
		if err := db.Create(&publicReply).Error; err != nil {
			t.Fatalf("failed to insert public reply: %v", err)
		}

		// 5. Query Message History via Public API
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/messages", websiteToken, contactIdentifier, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list public messages failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var msgsResp struct {
			Data struct {
				Messages []domain.Message `json:"messages"`
				Total    int64            `json:"total"`
				Page     int              `json:"page"`
				PageSize int              `json:"page_size"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &msgsResp)

		// Expected 3 public messages: m1, m2, publicReply. Private note must NOT appear!
		if msgsResp.Data.Total != 3 || len(msgsResp.Data.Messages) != 3 {
			t.Fatalf("expected 3 public messages, got total=%d len=%d", msgsResp.Data.Total, len(msgsResp.Data.Messages))
		}

		for _, msg := range msgsResp.Data.Messages {
			if msg.Private {
				t.Fatalf("CRITICAL SECURITY DEFECT: private note leaked in public message history! id=%d content=%s", msg.ID, msg.Content)
			}
			if msg.Content == "INTERNAL NOTE: Client flagged for vip billing, keep confidential!" {
				t.Fatalf("CRITICAL SECURITY DEFECT: internal note content found in public messages!")
			}
		}

		// Test pagination with page_size=2
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/messages?page=1&page_size=2", websiteToken, contactIdentifier, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		_ = json.Unmarshal(w.Body.Bytes(), &msgsResp)
		if len(msgsResp.Data.Messages) != 2 || msgsResp.Data.Total != 3 {
			t.Fatalf("expected page_size=2 pagination result with total=3, got len=%d total=%d", len(msgsResp.Data.Messages), msgsResp.Data.Total)
		}
	})

	// =========================================================================
	// Scenario 4: 会话状态切换 (Toggle Status / Resolve / Reopen)
	// =========================================================================
	t.Run("Scenario 4: Conversation Status Toggle and Setting", func(t *testing.T) {
		// Conv1 is currently open. Calling toggle_status with no body should resolve it.
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/toggle_status", websiteToken, contactIdentifier, conv1ID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("toggle status to resolved failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var statusResp struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &statusResp)
		if statusResp.Data.Status != domain.ConversationStatusResolved {
			t.Fatalf("expected status 'resolved', got %s", statusResp.Data.Status)
		}

		// Calling toggle_status again should automatically toggle back to open.
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/toggle_status", websiteToken, contactIdentifier, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("toggle status back to open failed: code=%d body=%s", w.Code, w.Body.String())
		}
		_ = json.Unmarshal(w.Body.Bytes(), &statusResp)
		if statusResp.Data.Status != domain.ConversationStatusOpen {
			t.Fatalf("expected status 'open', got %s", statusResp.Data.Status)
		}

		// Set explicit status: snoozed
		setPayload := map[string]string{
			"status": domain.ConversationStatusSnoozed,
		}
		b, _ := json.Marshal(setPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/toggle_status", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("set status to snoozed failed: code=%d body=%s", w.Code, w.Body.String())
		}
		_ = json.Unmarshal(w.Body.Bytes(), &statusResp)
		if statusResp.Data.Status != domain.ConversationStatusSnoozed {
			t.Fatalf("expected status 'snoozed', got %s", statusResp.Data.Status)
		}

		// Invalid status rejection test
		badPayload := map[string]string{
			"status": "invalid_status_xyz",
		}
		b, _ = json.Marshal(badPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/toggle_status", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for invalid status, got %d", w.Code)
		}
	})

	// =========================================================================
	// Scenario 5: 访客打字状态与会话自定义属性更新 (Typing & Custom Attributes)
	// =========================================================================
	t.Run("Scenario 5: Typing Indicator and Conversation Custom Attributes", func(t *testing.T) {
		// 1. Send Typing On
		typingPayload := map[string]string{
			"typing_status": "on",
		}
		b, _ := json.Marshal(typingPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/toggle_typing", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("toggle typing on failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 2. Send Typing Off
		typingPayload["typing_status"] = "off"
		b, _ = json.Marshal(typingPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/toggle_typing", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("toggle typing off failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 3. Update Conversation Custom Attributes (Initial batch)
		attrPayload := map[string]any{
			"custom_attributes": map[string]any{
				"category": "technical_support",
				"urgency":  "high",
			},
		}
		b, _ = json.Marshal(attrPayload)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/custom_attributes", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("patch conv custom attributes 1 failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 4. Update Conversation Custom Attributes (Incremental shallow merge)
		attrPayload2 := map[string]any{
			"custom_attributes": map[string]any{
				"urgency": "critical",
				"browser": "Chrome 120",
			},
		}
		b, _ = json.Marshal(attrPayload2)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s/conversations/%d/custom_attributes", websiteToken, contactIdentifier, conv1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("patch conv custom attributes 2 failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var attrsResp struct {
			Data struct {
				CustomAttributes map[string]any `json:"custom_attributes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &attrsResp)
		if attrsResp.Data.CustomAttributes["category"] != "technical_support" ||
			attrsResp.Data.CustomAttributes["urgency"] != "critical" ||
			attrsResp.Data.CustomAttributes["browser"] != "Chrome 120" {
			t.Fatalf("expected preserved category and merged urgency/browser, got %v", attrsResp.Data.CustomAttributes)
		}
	})

	// =========================================================================
	// Scenario 6: 租户安全隔离与越权访问防御 (Security & Boundary Isolation)
	// =========================================================================
	t.Run("Scenario 6: Security and Boundary Isolation", func(t *testing.T) {
		// 1. Invalid inbox identifier -> 404
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/invalid_inbox_token_999/contacts/%s", contactIdentifier), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for invalid inbox identifier, got %d", w.Code)
		}

		// 2. Non-existent contact -> 404
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/non_existent_contact_uuid", websiteToken), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent contact, got %d", w.Code)
		}

		// 3. Contact attempting to access another contact's conversation
		otherContactPayload := map[string]any{
			"name":       "Stranger Bob",
			"identifier": "stranger_bob_777",
		}
		b, _ := json.Marshal(otherContactPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts", websiteToken), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create stranger bob failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Stranger Bob attempts to read Charlie's Conv 1 detail -> MUST BE 404
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/stranger_bob_777/conversations/%d", websiteToken, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when unauthorized contact accesses conversation, got %d", w.Code)
		}

		// Stranger Bob attempts to read Charlie's Conv 1 messages -> MUST BE 404
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/stranger_bob_777/conversations/%d/messages", websiteToken, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when unauthorized contact accesses messages, got %d", w.Code)
		}

		// Stranger Bob attempts to toggle Charlie's Conv 1 status -> MUST BE 404
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/stranger_bob_777/conversations/%d/toggle_status", websiteToken, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when unauthorized contact attempts status toggle, got %d", w.Code)
		}
	})
}
