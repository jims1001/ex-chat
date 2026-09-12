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

func TestConversationManagementEnhancements(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "conversation_mgmt_enhancements_secret_32b!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user and get auth token
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Conv Admin",
		"email":        "conv_admin@example.com",
		"password":     "Password123!",
		"account_name": "Conv Management Org",
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
	userID := authResp.Data.User.ID

	// Helper function for sending authenticated requests
	sendReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var bodyReader *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(b)
		} else {
			bodyReader = bytes.NewReader([]byte{})
		}
		req := httptest.NewRequest(method, path, bodyReader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 2. Create Inbox
	createInboxResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes", accountID), map[string]string{
		"name":         "Support Inbox",
		"channel_type": "api",
	})
	if createInboxResp.Code != http.StatusCreated && createInboxResp.Code != http.StatusOK {
		t.Fatalf("create inbox failed: code=%d, body=%s", createInboxResp.Code, createInboxResp.Body.String())
	}
	var inboxData struct {
		Data domain.Inbox `json:"data"`
	}
	_ = json.Unmarshal(createInboxResp.Body.Bytes(), &inboxData)
	inboxID := inboxData.Data.ID

	// 3. Create Contact
	createContactResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts", accountID), map[string]string{
		"name":  "Alice Customer",
		"email": "alice@customer.com",
	})
	if createContactResp.Code != http.StatusCreated && createContactResp.Code != http.StatusOK {
		t.Fatalf("create contact failed: code=%d, body=%s", createContactResp.Code, createContactResp.Body.String())
	}
	var contactData struct {
		Data domain.Contact `json:"data"`
	}
	_ = json.Unmarshal(createContactResp.Body.Bytes(), &contactData)
	contactID := contactData.Data.ID

	// 4. Create Conversation
	createConvResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), map[string]any{
		"inbox_id":          inboxID,
		"contact_id":        contactID,
		"priority":          "low",
		"custom_attributes": `{"source":"mobile_app"}`,
	})
	if createConvResp.Code != http.StatusCreated {
		t.Fatalf("create conv failed: code=%d, body=%s", createConvResp.Code, createConvResp.Body.String())
	}
	var convData struct {
		Data domain.Conversation `json:"data"`
	}
	_ = json.Unmarshal(createConvResp.Body.Bytes(), &convData)
	convID := convData.Data.ID

	// Create a second conversation (assigned to current user)
	createConv2Resp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), map[string]any{
		"inbox_id":    inboxID,
		"contact_id":  contactID,
		"assignee_id": userID,
		"priority":    "urgent",
	})
	if createConv2Resp.Code != http.StatusCreated {
		t.Fatalf("create conv 2 failed: code=%d, body=%s", createConv2Resp.Code, createConv2Resp.Body.String())
	}
	var conv2Data struct {
		Data domain.Conversation `json:"data"`
	}
	_ = json.Unmarshal(createConv2Resp.Body.Bytes(), &conv2Data)
	conv2ID := conv2Data.Data.ID

	// Mark conv2 unread count
	_ = db.Model(&domain.Conversation{}).Where("id = ?", conv2ID).Update("unread_count", 3).Error

	// ==========================================
	// Test 1: Update Conversation (PUT / PATCH)
	// ==========================================
	t.Run("1_UpdateConversation", func(t *testing.T) {
		updateResp := sendReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", accountID, convID), map[string]any{
			"status":   "pending",
			"priority": "high",
		})
		if updateResp.Code != http.StatusOK {
			t.Fatalf("UpdateConversation failed: code=%d, body=%s", updateResp.Code, updateResp.Body.String())
		}
		var res struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(updateResp.Body.Bytes(), &res)
		if res.Data.Status != "pending" || res.Data.Priority != "high" {
			t.Fatalf("expected status=pending, priority=high, got status=%s, priority=%s", res.Data.Status, res.Data.Priority)
		}

		// Also test PATCH
		patchResp := sendReq(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", accountID, convID), map[string]any{
			"status": "open",
		})
		if patchResp.Code != http.StatusOK {
			t.Fatalf("PATCH UpdateConversation failed: code=%d, body=%s", patchResp.Code, patchResp.Body.String())
		}
	})

	// ==========================================
	// Test 2: Set Priority (POST / PUT)
	// ==========================================
	t.Run("2_SetPriority", func(t *testing.T) {
		prioResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/priority", accountID, convID), map[string]string{
			"priority": "urgent",
		})
		if prioResp.Code != http.StatusOK {
			t.Fatalf("SetPriority POST failed: code=%d, body=%s", prioResp.Code, prioResp.Body.String())
		}
		var res struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(prioResp.Body.Bytes(), &res)
		if res.Data.Priority != "urgent" {
			t.Fatalf("expected priority=urgent, got %s", res.Data.Priority)
		}

		// PUT priority
		prioPutResp := sendReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/priority", accountID, convID), map[string]string{
			"priority": "medium",
		})
		if prioPutResp.Code != http.StatusOK {
			t.Fatalf("SetPriority PUT failed: code=%d, body=%s", prioPutResp.Code, prioPutResp.Body.String())
		}
	})

	// ==========================================
	// Test 3: Update Custom Attributes (POST / PATCH)
	// ==========================================
	t.Run("3_UpdateCustomAttributes", func(t *testing.T) {
		attrResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/custom_attributes", accountID, convID), map[string]any{
			"custom_attributes": map[string]any{
				"order_id": "ORD-998811",
				"tier":     "vip",
			},
		})
		if attrResp.Code != http.StatusOK {
			t.Fatalf("UpdateCustomAttributes failed: code=%d, body=%s", attrResp.Code, attrResp.Body.String())
		}
		var res struct {
			Data struct {
				CustomAttributes map[string]any      `json:"custom_attributes"`
				Conversation     domain.Conversation `json:"conversation"`
			} `json:"data"`
		}
		_ = json.Unmarshal(attrResp.Body.Bytes(), &res)
		if res.Data.CustomAttributes["order_id"] != "ORD-998811" || res.Data.CustomAttributes["tier"] != "vip" {
			t.Fatalf("unexpected custom attributes: %+v", res.Data.CustomAttributes)
		}
	})

	// ==========================================
	// Test 4: Conversation Meta (GET /meta and GET /:id/meta)
	// ==========================================
	t.Run("4_GetConversationMeta", func(t *testing.T) {
		metaResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/meta", accountID), nil)
		if metaResp.Code != http.StatusOK {
			t.Fatalf("GetConversationMeta list failed: code=%d, body=%s", metaResp.Code, metaResp.Body.String())
		}
		var metaData struct {
			Data struct {
				Meta map[string]int64 `json:"meta"`
			} `json:"data"`
		}
		_ = json.Unmarshal(metaResp.Body.Bytes(), &metaData)
		if metaData.Data.Meta["all_count"] < 2 {
			t.Fatalf("expected all_count >= 2, got %d", metaData.Data.Meta["all_count"])
		}
		if metaData.Data.Meta["mine_count"] < 1 {
			t.Fatalf("expected mine_count >= 1, got %d", metaData.Data.Meta["mine_count"])
		}

		// Single conversation meta
		singleMetaResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/meta", accountID, convID), nil)
		if singleMetaResp.Code != http.StatusOK {
			t.Fatalf("single conversation meta failed: code=%d, body=%s", singleMetaResp.Code, singleMetaResp.Body.String())
		}
	})

	// ==========================================
	// Test 5: Unread Counts Summary (GET /unread_count)
	// ==========================================
	t.Run("5_GetUnreadCount", func(t *testing.T) {
		unreadResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/unread_count", accountID), nil)
		if unreadResp.Code != http.StatusOK {
			t.Fatalf("GetUnreadCount failed: code=%d, body=%s", unreadResp.Code, unreadResp.Body.String())
		}
		var unreadData struct {
			Data map[string]int64 `json:"data"`
		}
		_ = json.Unmarshal(unreadResp.Body.Bytes(), &unreadData)
		if unreadData.Data["total"] < 3 {
			t.Fatalf("expected total unread >= 3, got %d", unreadData.Data["total"])
		}
		if unreadData.Data["mine"] < 3 {
			t.Fatalf("expected mine unread >= 3, got %d", unreadData.Data["mine"])
		}
	})

	// ==========================================
	// Test 6: Advanced Filter (POST /conversations/filter)
	// ==========================================
	t.Run("6_FilterConversations", func(t *testing.T) {
		filterPayload := map[string]any{
			"payload": []map[string]any{
				{
					"attribute_key":   "priority",
					"filter_operator": "equal_to",
					"values":          []string{"urgent"},
				},
			},
			"page":      1,
			"page_size": 10,
		}
		filterResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/filter", accountID), filterPayload)
		if filterResp.Code != http.StatusOK {
			t.Fatalf("FilterConversations failed: code=%d, body=%s", filterResp.Code, filterResp.Body.String())
		}
		var filterResult struct {
			Data struct {
				Meta struct {
					Count int64 `json:"count"`
				} `json:"meta"`
				Payload []domain.Conversation `json:"payload"`
			} `json:"data"`
		}
		_ = json.Unmarshal(filterResp.Body.Bytes(), &filterResult)
		if filterResult.Data.Meta.Count != 1 || len(filterResult.Data.Payload) != 1 {
			t.Fatalf("expected 1 conversation matching urgent priority, got count=%d, payload_len=%d",
				filterResult.Data.Meta.Count, len(filterResult.Data.Payload))
		}
		if filterResult.Data.Payload[0].ID != conv2ID {
			t.Fatalf("expected conv2ID (%d), got %d", conv2ID, filterResult.Data.Payload[0].ID)
		}
	})

	// ==========================================
	// Test 7: Conversation Attachments (GET /attachments)
	// ==========================================
	t.Run("7_ListConversationAttachments", func(t *testing.T) {
		// Create a message with an attachment
		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: convID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       userID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Here is the requested receipt file",
		}
		_ = db.Create(&msg).Error

		att := domain.Attachment{
			AccountID: accountID,
			MessageID: msg.ID,
			FileType:  "application/pdf",
			DataURL:   "https://storage.example.com/receipt.pdf",
			FileSize:  10240,
		}
		_ = db.Create(&att).Error

		attResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", accountID, convID), nil)
		if attResp.Code != http.StatusOK {
			t.Fatalf("ListConversationAttachments failed: code=%d, body=%s", attResp.Code, attResp.Body.String())
		}
		var attResult struct {
			Data []domain.Attachment `json:"data"`
		}
		_ = json.Unmarshal(attResp.Body.Bytes(), &attResult)
		if len(attResult.Data) != 1 {
			t.Fatalf("expected 1 attachment, got %d", len(attResult.Data))
		}
		if attResult.Data[0].DataURL != "https://storage.example.com/receipt.pdf" {
			t.Fatalf("unexpected attachment URL: %s", attResult.Data[0].DataURL)
		}
	})

	// ==========================================
	// Test 8: Inbox Assistant & Conversation Assistant
	// ==========================================
	t.Run("8_GetInboxAndConversationAssistant", func(t *testing.T) {
		// Create CaptainAssistant
		assistant := domain.CaptainAssistant{
			AccountID:   accountID,
			Name:        "Support Bot Assistant",
			Description: "Automated support copilot",
			Status:      "active",
		}
		_ = db.Create(&assistant).Error

		// Bind assistant to inbox
		binding := domain.CaptainInbox{
			AccountID:          accountID,
			CaptainAssistantID: assistant.ID,
			InboxID:            inboxID,
		}
		_ = db.Create(&binding).Error

		// 8.1 GET /inboxes/:id/assistant
		inboxAssistantResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assistant", accountID, inboxID), nil)
		if inboxAssistantResp.Code != http.StatusOK {
			t.Fatalf("GetInboxAssistant failed: code=%d, body=%s", inboxAssistantResp.Code, inboxAssistantResp.Body.String())
		}
		var assistantRes1 struct {
			Data domain.CaptainAssistant `json:"data"`
		}
		_ = json.Unmarshal(inboxAssistantResp.Body.Bytes(), &assistantRes1)
		if assistantRes1.Data.ID != assistant.ID {
			t.Fatalf("expected assistant id %d, got %d", assistant.ID, assistantRes1.Data.ID)
		}

		// 8.2 GET /conversations/:id/assistant
		convAssistantResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assistant", accountID, convID), nil)
		if convAssistantResp.Code != http.StatusOK {
			t.Fatalf("GetConversationAssistant failed: code=%d, body=%s", convAssistantResp.Code, convAssistantResp.Body.String())
		}
		var assistantRes2 struct {
			Data domain.CaptainAssistant `json:"data"`
		}
		_ = json.Unmarshal(convAssistantResp.Body.Bytes(), &assistantRes2)
		if assistantRes2.Data.ID != assistant.ID {
			t.Fatalf("expected conv assistant id %d, got %d", assistant.ID, assistantRes2.Data.ID)
		}
	})

	// ==========================================
	// Test 9: Conversation Reporting Events
	// ==========================================
	t.Run("9_ListConversationReportingEvents", func(t *testing.T) {
		convIDVal := convID
		evt := domain.ReportingEvent{
			AccountID:      accountID,
			ConversationID: &convIDVal,
			Name:           "first_response_time",
			Value:          45.5,
			EventStartTime: time.Now().Add(-10 * time.Minute),
			EventEndTime:   time.Now(),
			MetadataJSON:   fmt.Sprintf(`{"conversation_id":%d,"metric":"sla_response"}`, convID),
		}
		_ = db.Create(&evt).Error

		evtResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/reporting_events", accountID, convID), nil)
		if evtResp.Code != http.StatusOK {
			t.Fatalf("ListConversationReportingEvents failed: code=%d, body=%s", evtResp.Code, evtResp.Body.String())
		}
		var evtResult struct {
			Data []domain.ReportingEvent `json:"data"`
		}
		_ = json.Unmarshal(evtResp.Body.Bytes(), &evtResult)
		if len(evtResult.Data) != 1 {
			t.Fatalf("expected 1 reporting event for conv, got %d", len(evtResult.Data))
		}
		if evtResult.Data[0].Name != "first_response_time" {
			t.Fatalf("unexpected event name: %s", evtResult.Data[0].Name)
		}
	})

	// ==========================================
	// Test 10: Translate Message (POST /translate)
	// ==========================================
	t.Run("10_TranslateMessage", func(t *testing.T) {
		// Create a message in convID
		testMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: convID,
			SenderType:     domain.SenderTypeContact,
			SenderID:       contactID,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Bonjour, comment puis-je vous aider?",
		}
		_ = db.Create(&testMsg).Error

		// 10.1 POST /conversations/:id/messages/:message_id/translate with target_language=en
		translateResp1 := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/translate", accountID, convID, testMsg.ID), map[string]string{
			"target_language": "en",
		})
		if translateResp1.Code != http.StatusOK {
			t.Fatalf("TranslateMessage in conversation failed: code=%d, body=%s", translateResp1.Code, translateResp1.Body.String())
		}
		var transRes1 struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(translateResp1.Body.Bytes(), &transRes1)
		if transRes1.Content == "" {
			t.Fatalf("expected non-empty translated content, got empty")
		}

		// 10.2 POST /messages/:id/translate with query parameter
		translateResp2 := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/messages/%d/translate?target_language=zh", accountID, testMsg.ID), nil)
		if translateResp2.Code != http.StatusOK {
			t.Fatalf("TranslateMessage directly failed: code=%d, body=%s", translateResp2.Code, translateResp2.Body.String())
		}
		var transRes2 struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(translateResp2.Body.Bytes(), &transRes2)
		if transRes2.Content == "" {
			t.Fatalf("expected non-empty translated content, got empty")
		}

		// 10.3 Pre-cached translation test
		var updatedMsg domain.Message
		_ = db.First(&updatedMsg, testMsg.ID).Error
		var transMap map[string]string
		_ = json.Unmarshal([]byte(updatedMsg.Translations), &transMap)
		transMap["es"] = "Hola, ¿cómo puedo ayudarte?"
		b, _ := json.Marshal(transMap)
		_ = db.Model(&domain.Message{}).Where("id = ?", testMsg.ID).Update("translations", string(b)).Error

		cachedResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/messages/%d/translate", accountID, testMsg.ID), map[string]string{
			"target_language": "es",
		})
		if cachedResp.Code != http.StatusOK {
			t.Fatalf("cached translate failed: code=%d, body=%s", cachedResp.Code, cachedResp.Body.String())
		}
		var cachedResult struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(cachedResp.Body.Bytes(), &cachedResult)
		if cachedResult.Content != "Hola, ¿cómo puedo ayudarte?" {
			t.Fatalf("expected cached translation 'Hola, ¿cómo puedo ayudarte?', got '%s'", cachedResult.Content)
		}

		// 10.4 Non-existent message 404
		notFoundResp := sendReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/messages/999999/translate", accountID), nil)
		if notFoundResp.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent message, got code=%d", notFoundResp.Code)
		}
	})

	// ==========================================
	// Test 11: Delete Conversation (DELETE)
	// ==========================================
	t.Run("11_DeleteConversation", func(t *testing.T) {
		delResp := sendReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", accountID, convID), nil)
		if delResp.Code != http.StatusOK {
			t.Fatalf("DeleteConversation failed: code=%d, body=%s", delResp.Code, delResp.Body.String())
		}

		// Verify conversation is gone
		getResp := sendReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", accountID, convID), nil)
		if getResp.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted conversation, got code=%d", getResp.Code)
		}

		// Verify messages belonging to deleted conversation are cleaned up
		var msgCount int64
		_ = db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", accountID, convID).Count(&msgCount).Error
		if msgCount != 0 {
			t.Fatalf("expected 0 messages remaining, got %d", msgCount)
		}
	})
}

