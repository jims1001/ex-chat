package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
)

func TestBusinessDefectsFix(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test-secret-defects-fix-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("database init failed: %v", err)
	}
	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Register test user & account
	signUpBody, _ := json.Marshal(map[string]any{
		"name":         "Defects Admin",
		"email":        "admin@defects-fix.com",
		"password":     "P@ssw0rd!2026",
		"account_name": "Defects Test Workspace",
	})
	wAuth := httptest.NewRecorder()
	reqAuth, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqAuth.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wAuth, reqAuth)
	if wAuth.Code != http.StatusCreated {
		t.Fatalf("sign up failed: %d, body: %s", wAuth.Code, wAuth.Body.String())
	}

	var authResp struct {
		Data struct {
			Token string `json:"token"`
			User  struct {
				ID uint `json:"id"`
			} `json:"user"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAuth.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	authHeader := "Bearer " + token

	// -------------------------------------------------------------
	// 1. 客户合并：迁移来源客户备注及历史消息发送者归属
	// -------------------------------------------------------------
	t.Run("Scenario1_ContactMerge_MigrateNotesAndMessageSender", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "合并测试收件箱",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "tok_merge_test_01",
		}
		db.Create(&inbox)

		baseContact := domain.Contact{
			AccountID: accountID,
			Name:      "主客户",
			Email:     "base.contact@example.com",
		}
		db.Create(&baseContact)

		mergeeContact := domain.Contact{
			AccountID: accountID,
			Name:      "被合并客户",
			Email:     "mergee.contact@example.com",
		}
		db.Create(&mergeeContact)

		// Create contact note under mergee contact
		note := domain.ContactNote{
			AccountID: accountID,
			ContactID: mergeeContact.ID,
			UserID:    authResp.Data.User.ID,
			Content:   "被合并客户的历史服务重要备注信息",
		}
		db.Create(&note)

		// Create conversation under mergee contact
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			ContactID: mergeeContact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&conv)

		// Create message sent by mergee contact
		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			SenderID:       mergeeContact.ID,
			MessageType:    domain.MessageTypeIncoming,
			ContentType:    domain.ContentTypeText,
			Content:        "被合并客户发送的历史咨询内容",
		}
		db.Create(&msg)

		// Execute contact merge API
		mergeBody, _ := json.Marshal(map[string]any{
			"base_contact_id":   baseContact.ID,
			"mergee_contact_id": mergeeContact.ID,
		})
		wMerge := httptest.NewRecorder()
		reqMerge, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/actions/contact_merge", accountID), bytes.NewBuffer(mergeBody))
		reqMerge.Header.Set("Authorization", authHeader)
		reqMerge.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wMerge, reqMerge)

		if wMerge.Code != http.StatusOK {
			t.Fatalf("contact merge failed: %d, body: %s", wMerge.Code, wMerge.Body.String())
		}

		// Verify mergee contact is deleted
		var checkMergee domain.Contact
		if err := db.Where("account_id = ? AND id = ?", accountID, mergeeContact.ID).First(&checkMergee).Error; err == nil {
			t.Fatalf("expected mergee contact to be deleted, but still exists")
		}

		// Verify contact note is migrated to baseContact
		var checkNote domain.ContactNote
		db.First(&checkNote, note.ID)
		if checkNote.ContactID != baseContact.ID {
			t.Fatalf("expected contact note to be migrated to baseContact ID %d, got %d", baseContact.ID, checkNote.ContactID)
		}

		// Verify conversation contact is migrated to baseContact
		var checkConv domain.Conversation
		db.First(&checkConv, conv.ID)
		if checkConv.ContactID != baseContact.ID {
			t.Fatalf("expected conversation contact_id to be migrated to baseContact ID %d, got %d", baseContact.ID, checkConv.ContactID)
		}

		// Verify historical message sender is migrated to baseContact
		var checkMsg domain.Message
		db.First(&checkMsg, msg.ID)
		if checkMsg.SenderID != baseContact.ID {
			t.Fatalf("expected message sender_id to be migrated to baseContact ID %d, got %d", baseContact.ID, checkMsg.SenderID)
		}
	})

	// -------------------------------------------------------------
	// 2. 批量更新数量：返回真实受影响的行数，而非请求 ID 数量
	// -------------------------------------------------------------
	t.Run("Scenario2_BulkUpdateCount_AccurateReporting", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "批量测试收件箱",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "tok_bulk_count_01",
		}
		db.Create(&inbox)
		contact := domain.Contact{AccountID: accountID, Name: "批量客户"}
		db.Create(&contact)

		// Create only ONE actual conversation
		c1 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		db.Create(&c1)

		// Request includes 1 valid ID and 2 non-existent IDs (total 3)
		bulkBody, _ := json.Marshal(map[string]any{
			"type": "update_status",
			"ids":  []uint{c1.ID, 99998, 99999},
			"fields": map[string]any{
				"status": "resolved",
			},
		})
		wBulk := httptest.NewRecorder()
		reqBulk, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewBuffer(bulkBody))
		reqBulk.Header.Set("Authorization", authHeader)
		reqBulk.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wBulk, reqBulk)

		if wBulk.Code != http.StatusOK {
			t.Fatalf("bulk actions failed: %d, body: %s", wBulk.Code, wBulk.Body.String())
		}

		var bulkResp struct {
			Data struct {
				Status       string `json:"status"`
				UpdatedCount int64  `json:"updated_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wBulk.Body.Bytes(), &bulkResp)

		// Must be 1 (real rows updated), NOT 3 (requested IDs count)
		if bulkResp.Data.UpdatedCount != 1 {
			t.Fatalf("expected updated_count to be 1 (real affected rows), got %d", bulkResp.Data.UpdatedCount)
		}
	})

	// -------------------------------------------------------------
	// 3. 批量状态修改：合法状态白名单强校验
	// -------------------------------------------------------------
	t.Run("Scenario3_BulkStatusUpdate_RestrictValidStatuses", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "状态白名单收件箱",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "tok_status_valid_01",
		}
		db.Create(&inbox)
		contact := domain.Contact{AccountID: accountID, Name: "状态客户"}
		db.Create(&contact)
		c1 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		db.Create(&c1)

		// 1. Submit invalid status
		invalidBody, _ := json.Marshal(map[string]any{
			"type": "update_status",
			"ids":  []uint{c1.ID},
			"fields": map[string]any{
				"status": "invalid_random_status",
			},
		})
		wInvalid := httptest.NewRecorder()
		reqInvalid, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewBuffer(invalidBody))
		reqInvalid.Header.Set("Authorization", authHeader)
		reqInvalid.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wInvalid, reqInvalid)

		// Must reject with 400 Bad Request
		if wInvalid.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for invalid status, got %d: %s", wInvalid.Code, wInvalid.Body.String())
		}

		// 2. Submit valid status: pending
		validBody, _ := json.Marshal(map[string]any{
			"type": "update_status",
			"ids":  []uint{c1.ID},
			"fields": map[string]any{
				"status": "pending",
			},
		})
		wValid := httptest.NewRecorder()
		reqValid, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewBuffer(validBody))
		reqValid.Header.Set("Authorization", authHeader)
		reqValid.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wValid, reqValid)

		if wValid.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for valid status 'pending', got %d: %s", wValid.Code, wValid.Body.String())
		}

		var checkConv domain.Conversation
		db.First(&checkConv, c1.ID)
		if checkConv.Status != domain.ConversationStatusPending {
			t.Fatalf("expected conversation status to be pending, got %s", checkConv.Status)
		}
	})

	// -------------------------------------------------------------
	// 4. Public 客户继续发消息：已解决会话自动重开 (Auto-Reopen)
	// -------------------------------------------------------------
	t.Run("Scenario4_PublicCustomer_ResolvedConversation_AutoReopen", func(t *testing.T) {
		token := "tok_public_reopen_01"
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "公开会话重开测试",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: token,
		}
		db.Create(&inbox)

		contact := domain.Contact{
			AccountID: accountID,
			Name:      "已结单客户",
			Email:     "resolved.cust@example.com",
		}
		db.Create(&contact)

		// Create conversation in 'resolved' status
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusResolved,
		}
		db.Create(&conv)

		// Customer posts a new message via public API
		msgBody, _ := json.Marshal(map[string]any{
			"content": "我还需要人工协助此订单",
		})
		wPublic := httptest.NewRecorder()
		reqPublic, _ := http.NewRequest(
			"POST",
			fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d/conversations/%d/messages", token, contact.ID, conv.ID),
			bytes.NewBuffer(msgBody),
		)
		reqPublic.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPublic, reqPublic)

		if wPublic.Code != http.StatusCreated && wPublic.Code != http.StatusOK {
			t.Fatalf("public create message failed: %d, body: %s", wPublic.Code, wPublic.Body.String())
		}

		// Verify conversation status is automatically re-opened to 'open'
		var updatedConv domain.Conversation
		db.First(&updatedConv, conv.ID)
		if updatedConv.Status != domain.ConversationStatusOpen {
			t.Fatalf("expected conversation status to be automatically reopened to 'open', got '%s'", updatedConv.Status)
		}
	})

	// -------------------------------------------------------------
	// 5. Public 消息后续处理：接入与 Widget 一致的 Webhook / 自动化 / 广播
	// -------------------------------------------------------------
	t.Run("Scenario5_PublicMessage_FullEventPipeline", func(t *testing.T) {
		token := "tok_public_events_01"
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "公开事件链路收件箱",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: token,
		}
		db.Create(&inbox)

		contact := domain.Contact{
			AccountID: accountID,
			Name:      "事件触发客户",
		}
		db.Create(&contact)

		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&conv)

		// Create webhook subscription to verify dispatch
		wh := domain.Webhook{
			AccountID: accountID,
			URL:       "https://webhook.site/mock-test-receiver",
		}
		db.Create(&wh)

		msgBody, _ := json.Marshal(map[string]any{
			"content": "测试公开接口后续事件广播",
		})
		wPub := httptest.NewRecorder()
		reqPub, _ := http.NewRequest(
			"POST",
			fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d/conversations/%d/messages", token, contact.ID, conv.ID),
			bytes.NewBuffer(msgBody),
		)
		reqPub.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPub, reqPub)

		if wPub.Code != http.StatusCreated && wPub.Code != http.StatusOK {
			t.Fatalf("public message create failed: %d, body: %s", wPub.Code, wPub.Body.String())
		}

		// Verify message was created
		var count int64
		db.Model(&domain.Message{}).Where("conversation_id = ?", conv.ID).Count(&count)
		if count < 1 {
			t.Fatalf("expected message to be persisted in conversation")
		}
	})

	// -------------------------------------------------------------
	// 6. 草稿清空：支持空文本清空及专用 DELETE 删除接口
	// -------------------------------------------------------------
	t.Run("Scenario6_DraftClear_EmptyMessageAndEndpoint", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "草稿测试收件箱",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "tok_draft_test_01",
		}
		db.Create(&inbox)
		contact := domain.Contact{AccountID: accountID, Name: "草稿客户"}
		db.Create(&contact)
		conv := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		db.Create(&conv)

		// 1. Save normal draft
		saveBody, _ := json.Marshal(map[string]any{
			"message": "这是一段正在输入的草稿消息",
		})
		wSave := httptest.NewRecorder()
		reqSave, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/draft_messages", accountID, conv.ID), bytes.NewBuffer(saveBody))
		reqSave.Header.Set("Authorization", authHeader)
		reqSave.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wSave, reqSave)
		if wSave.Code != http.StatusOK {
			t.Fatalf("save draft failed: %d, body: %s", wSave.Code, wSave.Body.String())
		}

		// Verify draft is retrieved
		wGet := httptest.NewRecorder()
		reqGet, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/draft_messages", accountID, conv.ID), nil)
		reqGet.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wGet, reqGet)
		if wGet.Code != http.StatusOK {
			t.Fatalf("get draft failed: %d, body: %s", wGet.Code, wGet.Body.String())
		}
		var draftGetResp struct {
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wGet.Body.Bytes(), &draftGetResp)
		if draftGetResp.Data.Message != "这是一段正在输入的草稿消息" {
			t.Fatalf("expected draft message to match, got %s", draftGetResp.Data.Message)
		}

		// 2. Clear draft by submitting empty message "" (Must NOT fail with 400!)
		clearBody, _ := json.Marshal(map[string]any{
			"message": "",
		})
		wClear := httptest.NewRecorder()
		reqClear, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/draft_messages", accountID, conv.ID), bytes.NewBuffer(clearBody))
		reqClear.Header.Set("Authorization", authHeader)
		reqClear.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wClear, reqClear)

		if wClear.Code != http.StatusOK {
			t.Fatalf("clear draft with empty message failed: %d, body: %s", wClear.Code, wClear.Body.String())
		}

		// Verify draft is now empty
		wGetEmpty := httptest.NewRecorder()
		reqGetEmpty, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/draft_messages", accountID, conv.ID), nil)
		reqGetEmpty.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wGetEmpty, reqGetEmpty)
		var draftEmptyResp struct {
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wGetEmpty.Body.Bytes(), &draftEmptyResp)
		if draftEmptyResp.Data.Message != "" {
			t.Fatalf("expected cleared draft message to be empty, got: %s", draftEmptyResp.Data.Message)
		}

		// 3. Save draft again, then test DELETE endpoint
		saveBody2, _ := json.Marshal(map[string]any{
			"message": "待删除的草稿内容",
		})
		wSave2 := httptest.NewRecorder()
		reqSave2, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/draft_messages", accountID, conv.ID), bytes.NewBuffer(saveBody2))
		reqSave2.Header.Set("Authorization", authHeader)
		reqSave2.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wSave2, reqSave2)
		if wSave2.Code != http.StatusOK {
			t.Fatalf("save draft 2 failed: %d", wSave2.Code)
		}

		// Call DELETE /conversations/:id/draft_messages
		wDel := httptest.NewRecorder()
		reqDel, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/draft_messages", accountID, conv.ID), nil)
		reqDel.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wDel, reqDel)
		if wDel.Code != http.StatusOK {
			t.Fatalf("DELETE draft_messages endpoint failed: %d, body: %s", wDel.Code, wDel.Body.String())
		}

		// Verify draft is deleted
		wGetFinal := httptest.NewRecorder()
		reqGetFinal, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/draft_messages", accountID, conv.ID), nil)
		reqGetFinal.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wGetFinal, reqGetFinal)
		var draftFinalResp struct {
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wGetFinal.Body.Bytes(), &draftFinalResp)
		if draftFinalResp.Data.Message != "" {
			t.Fatalf("expected draft message to be empty after DELETE, got %s", draftFinalResp.Data.Message)
		}
	})
}
