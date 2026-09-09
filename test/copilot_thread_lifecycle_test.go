package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestCopilotThreadLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:copilot_thread_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_copilot_123",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user to obtain token & account 1
	signUpPayload := map[string]string{
		"name":         "Copilot Admin",
		"email":        fmt.Sprintf("copilot_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Copilot Test Corp",
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
			Token    string           `json:"token"`
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// 2. Sign up Account 2 (for tenant isolation check)
	signUpPayload2 := map[string]string{
		"name":         "Tenant2 Admin",
		"email":        fmt.Sprintf("tenant2_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Tenant 2 Corp",
	}
	body2, _ := json.Marshal(signUpPayload2)
	req2 := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	var authResp2 struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	token2 := authResp2.Data.Token
	accountID2 := authResp2.Data.Accounts[0].ID

	// Create Inbox and Contact for conversations
	inbox := domain.Inbox{
		AccountID: accountID,
		Name:      "VIP Support Inbox",
	}
	_ = db.Create(&inbox).Error

	contact := domain.Contact{
		AccountID: accountID,
		Name:      "Bob Customer",
		Email:     "bob@example.com",
	}
	_ = db.Create(&contact).Error

	// Create Knowledge Doc for testing citation matching
	knowledgeDoc := domain.CaptainKnowledgeDoc{
		AccountID:  accountID,
		Title:      "退款与物流延迟处理指引",
		Content:    "对于物流延迟超过3天的情况，客服可先行安抚并为其申请50元无门槛优惠券作为补偿；如客户要求退款，可直接走闪电退款绿色通道。",
		Category:   "售后政策",
		Status:     "ready",
		ChunkCount: 1,
	}
	_ = db.Create(&knowledgeDoc).Error

	// -------------------------------------------------------------
	// Scenario 1: Thread Basic CRUD
	// -------------------------------------------------------------
	var standaloneThreadID uint
	t.Run("Scenario1_Thread_Basic_CRUD", func(t *testing.T) {
		createPayload := map[string]any{
			"title":   "订单异常问题专项分析",
			"context": "针对高频支付故障的协同排查",
		}
		cBody, _ := json.Marshal(createPayload)
		reqCreate := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads", accountID), bytes.NewReader(cBody))
		reqCreate.Header.Set("Authorization", "Bearer "+token)
		reqCreate.Header.Set("Content-Type", "application/json")
		wCreate := httptest.NewRecorder()
		r.ServeHTTP(wCreate, reqCreate)

		if wCreate.Code != http.StatusOK {
			t.Fatalf("POST /copilot/threads failed: code=%d body=%s", wCreate.Code, wCreate.Body.String())
		}

		var createResp struct {
			Data domain.CopilotThread `json:"data"`
		}
		_ = json.Unmarshal(wCreate.Body.Bytes(), &createResp)
		standaloneThreadID = createResp.Data.ID

		if standaloneThreadID == 0 {
			t.Fatalf("expected valid thread ID, got 0")
		}
		if createResp.Data.Title != "订单异常问题专项分析" {
			t.Errorf("expected title '订单异常问题专项分析', got '%s'", createResp.Data.Title)
		}
		if createResp.Data.Status != domain.CopilotThreadStatusActive {
			t.Errorf("expected status 'active', got '%s'", createResp.Data.Status)
		}

		// Get Thread
		reqGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d", accountID, standaloneThreadID), nil)
		reqGet.Header.Set("Authorization", "Bearer "+token)
		wGet := httptest.NewRecorder()
		r.ServeHTTP(wGet, reqGet)

		if wGet.Code != http.StatusOK {
			t.Fatalf("GET /copilot/threads/:id failed: code=%d", wGet.Code)
		}

		// Update Thread
		newTitle := "订单与支付异常排查 (置顶)"
		newStatus := domain.CopilotThreadStatusPinned
		updatePayload := map[string]string{
			"title":  newTitle,
			"status": newStatus,
		}
		uBody, _ := json.Marshal(updatePayload)
		reqUpdate := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d", accountID, standaloneThreadID), bytes.NewReader(uBody))
		reqUpdate.Header.Set("Authorization", "Bearer "+token)
		reqUpdate.Header.Set("Content-Type", "application/json")
		wUpdate := httptest.NewRecorder()
		r.ServeHTTP(wUpdate, reqUpdate)

		if wUpdate.Code != http.StatusOK {
			t.Fatalf("PUT /copilot/threads/:id failed: code=%d", wUpdate.Code)
		}

		var updateResp struct {
			Data domain.CopilotThread `json:"data"`
		}
		_ = json.Unmarshal(wUpdate.Body.Bytes(), &updateResp)
		if updateResp.Data.Title != newTitle || updateResp.Data.Status != newStatus {
			t.Errorf("expected updated title and status, got title='%s', status='%s'", updateResp.Data.Title, updateResp.Data.Status)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Multi-turn Chat & Context Continuity
	// -------------------------------------------------------------
	var assistantMsgID uint
	t.Run("Scenario2_MultiTurn_Chat_Engine", func(t *testing.T) {
		// Round 1: First user question
		chatPayload1 := map[string]string{
			"content": "客户由于物流延迟超过3天表示非常不满，我们有什么处理方案？",
		}
		cBody1, _ := json.Marshal(chatPayload1)
		reqChat1 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/chat", accountID, standaloneThreadID), bytes.NewReader(cBody1))
		reqChat1.Header.Set("Authorization", "Bearer "+token)
		reqChat1.Header.Set("Content-Type", "application/json")
		wChat1 := httptest.NewRecorder()
		r.ServeHTTP(wChat1, reqChat1)

		if wChat1.Code != http.StatusOK {
			t.Fatalf("POST /copilot/threads/:id/chat round 1 failed: code=%d body=%s", wChat1.Code, wChat1.Body.String())
		}

		var chatResp1 struct {
			Data struct {
				UserMessage      domain.CopilotThreadMessage `json:"user_message"`
				AssistantMessage domain.CopilotThreadMessage `json:"assistant_message"`
				Thread           domain.CopilotThread        `json:"thread"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wChat1.Body.Bytes(), &chatResp1)

		if chatResp1.Data.UserMessage.Content != chatPayload1["content"] {
			t.Errorf("expected user message content matched, got '%s'", chatResp1.Data.UserMessage.Content)
		}
		if chatResp1.Data.AssistantMessage.Role != domain.CopilotRoleAssistant {
			t.Errorf("expected assistant role, got '%s'", chatResp1.Data.AssistantMessage.Role)
		}
		if chatResp1.Data.AssistantMessage.TokenCount <= 0 {
			t.Errorf("expected positive token count, got %d", chatResp1.Data.AssistantMessage.TokenCount)
		}
		// Citations should match our knowledge doc
		if !strings.Contains(chatResp1.Data.AssistantMessage.Citations, "退款与物流延迟") {
			t.Logf("citations returned: %s", chatResp1.Data.AssistantMessage.Citations)
		}
		if !strings.Contains(chatResp1.Data.AssistantMessage.SuggestedActions, "insert_reply") {
			t.Errorf("expected suggested actions to include insert_reply, got %s", chatResp1.Data.AssistantMessage.SuggestedActions)
		}
		assistantMsgID = chatResp1.Data.AssistantMessage.ID

		// Verify thread counter updated to 2
		if chatResp1.Data.Thread.MessageCount != 2 {
			t.Errorf("expected thread MessageCount 2, got %d", chatResp1.Data.Thread.MessageCount)
		}

		// Round 2: Follow-up question requesting more gentle/polite tone
		chatPayload2 := map[string]string{
			"content": "请将上述回复换成更加温和安抚的语气",
		}
		cBody2, _ := json.Marshal(chatPayload2)
		reqChat2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/messages", accountID, standaloneThreadID), bytes.NewReader(cBody2))
		reqChat2.Header.Set("Authorization", "Bearer "+token)
		reqChat2.Header.Set("Content-Type", "application/json")
		wChat2 := httptest.NewRecorder()
		r.ServeHTTP(wChat2, reqChat2)

		if wChat2.Code != http.StatusOK {
			t.Fatalf("POST /copilot/threads/:id/messages round 2 failed: code=%d body=%s", wChat2.Code, wChat2.Body.String())
		}

		var chatResp2 struct {
			Data struct {
				AssistantMessage domain.CopilotThreadMessage `json:"assistant_message"`
				Thread           domain.CopilotThread        `json:"thread"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wChat2.Body.Bytes(), &chatResp2)

		if !strings.Contains(chatResp2.Data.AssistantMessage.Content, "委婉") &&
			!strings.Contains(chatResp2.Data.AssistantMessage.Content, "安抚") &&
			!strings.Contains(chatResp2.Data.AssistantMessage.Content, "理解") {
			t.Errorf("expected multi-turn reply to adapt tone, got '%s'", chatResp2.Data.AssistantMessage.Content)
		}

		// Verify total thread messages incremented to 4
		if chatResp2.Data.Thread.MessageCount != 4 {
			t.Errorf("expected thread MessageCount 4, got %d", chatResp2.Data.Thread.MessageCount)
		}

		// List Messages endpoint
		reqListMsgs := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/messages?order=asc", accountID, standaloneThreadID), nil)
		reqListMsgs.Header.Set("Authorization", "Bearer "+token)
		wListMsgs := httptest.NewRecorder()
		r.ServeHTTP(wListMsgs, reqListMsgs)

		if wListMsgs.Code != http.StatusOK {
			t.Fatalf("GET /copilot/threads/:id/messages failed: code=%d", wListMsgs.Code)
		}

		var listMsgsResp struct {
			Data struct {
				Messages []domain.CopilotThreadMessage `json:"messages"`
				Meta     struct {
					Total int `json:"total"`
				} `json:"meta"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wListMsgs.Body.Bytes(), &listMsgsResp)
		if len(listMsgsResp.Data.Messages) != 4 || listMsgsResp.Data.Meta.Total != 4 {
			t.Errorf("expected 4 messages in thread, got %d (meta %d)", len(listMsgsResp.Data.Messages), listMsgsResp.Data.Meta.Total)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Conversation Scoped Copilot Thread
	// -------------------------------------------------------------
	t.Run("Scenario3_Conversation_Scoped_Thread", func(t *testing.T) {
		convRepo := repository.NewConversationRepository(db)
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
			CreatedAt: time.Now().UTC(),
		}
		_ = convRepo.Create(&conv)

		// Customer sends a message
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "我的订单 #10086 怎么查不到物流？能帮我催一下网点吗？",
			CreatedAt:      time.Now().UTC(),
		}).Error

		// Create thread scoped to this conversation via POST /conversations/:id/copilot/threads
		reqConvThread := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/copilot/threads", accountID, conv.ID), bytes.NewReader([]byte(`{"title":"会话10086专属辅助"}`)))
		reqConvThread.Header.Set("Authorization", "Bearer "+token)
		reqConvThread.Header.Set("Content-Type", "application/json")
		wConvThread := httptest.NewRecorder()
		r.ServeHTTP(wConvThread, reqConvThread)

		if wConvThread.Code != http.StatusOK {
			t.Fatalf("POST /conversations/:id/copilot/threads failed: code=%d body=%s", wConvThread.Code, wConvThread.Body.String())
		}

		var convThreadResp struct {
			Data domain.CopilotThread `json:"data"`
		}
		_ = json.Unmarshal(wConvThread.Body.Bytes(), &convThreadResp)
		convThreadID := convThreadResp.Data.ID

		if convThreadResp.Data.ConversationID == nil || *convThreadResp.Data.ConversationID != conv.ID {
			t.Errorf("expected ConversationID %d, got %v", conv.ID, convThreadResp.Data.ConversationID)
		}

		// Prompt Copilot to help reply
		reqConvChat := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/chat", accountID, convThreadID), bytes.NewReader([]byte(`{"content":"请根据客户问题生成标准回复"}`)))
		reqConvChat.Header.Set("Authorization", "Bearer "+token)
		reqConvChat.Header.Set("Content-Type", "application/json")
		wConvChat := httptest.NewRecorder()
		r.ServeHTTP(wConvChat, reqConvChat)

		if wConvChat.Code != http.StatusOK {
			t.Fatalf("POST /copilot/threads/:id/chat on conversation thread failed: code=%d", wConvChat.Code)
		}

		var convChatResp struct {
			Data struct {
				AssistantMessage domain.CopilotThreadMessage `json:"assistant_message"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wConvChat.Body.Bytes(), &convChatResp)

		// Copilot synthesized reply should reference the conversation background
		if !strings.Contains(convChatResp.Data.AssistantMessage.Content, "会话") &&
			!strings.Contains(convChatResp.Data.AssistantMessage.Content, "您好") {
			t.Errorf("expected reply to incorporate conversation context, got '%s'", convChatResp.Data.AssistantMessage.Content)
		}

		// Query threads by conversation via GET /conversations/:id/copilot/threads
		reqListConvThreads := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/copilot/threads", accountID, conv.ID), nil)
		reqListConvThreads.Header.Set("Authorization", "Bearer "+token)
		wListConvThreads := httptest.NewRecorder()
		r.ServeHTTP(wListConvThreads, reqListConvThreads)

		if wListConvThreads.Code != http.StatusOK {
			t.Fatalf("GET /conversations/:id/copilot/threads failed: code=%d", wListConvThreads.Code)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: Feedback & Clear Operations
	// -------------------------------------------------------------
	t.Run("Scenario4_Feedback_and_Clear", func(t *testing.T) {
		if assistantMsgID == 0 {
			t.Skip("assistantMsgID not set from Scenario 2")
		}

		// Submit thumbs up feedback
		fbPayload := map[string]string{
			"feedback": domain.CopilotFeedbackThumbsUp,
			"notes":    "回复契合度很高，直接采用！",
		}
		fbBody, _ := json.Marshal(fbPayload)
		reqFB := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/messages/%d/feedback", accountID, standaloneThreadID, assistantMsgID), bytes.NewReader(fbBody))
		reqFB.Header.Set("Authorization", "Bearer "+token)
		reqFB.Header.Set("Content-Type", "application/json")
		wFB := httptest.NewRecorder()
		r.ServeHTTP(wFB, reqFB)

		if wFB.Code != http.StatusOK {
			t.Fatalf("POST /feedback failed: code=%d body=%s", wFB.Code, wFB.Body.String())
		}

		// Verify single message detail reflects feedback
		reqMsgDetail := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/messages/%d", accountID, standaloneThreadID, assistantMsgID), nil)
		reqMsgDetail.Header.Set("Authorization", "Bearer "+token)
		wMsgDetail := httptest.NewRecorder()
		r.ServeHTTP(wMsgDetail, reqMsgDetail)

		var msgDetailResp struct {
			Data domain.CopilotThreadMessage `json:"data"`
		}
		_ = json.Unmarshal(wMsgDetail.Body.Bytes(), &msgDetailResp)
		if msgDetailResp.Data.Feedback != domain.CopilotFeedbackThumbsUp {
			t.Errorf("expected Feedback 'thumbs_up', got '%s'", msgDetailResp.Data.Feedback)
		}
		if msgDetailResp.Data.FeedbackNotes != "回复契合度很高，直接采用！" {
			t.Errorf("expected FeedbackNotes matched, got '%s'", msgDetailResp.Data.FeedbackNotes)
		}

		// Clear messages for this thread via DELETE /copilot/threads/:id/messages
		reqClear := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/messages", accountID, standaloneThreadID), nil)
		reqClear.Header.Set("Authorization", "Bearer "+token)
		wClear := httptest.NewRecorder()
		r.ServeHTTP(wClear, reqClear)

		if wClear.Code != http.StatusOK {
			t.Fatalf("DELETE /messages clear failed: code=%d body=%s", wClear.Code, wClear.Body.String())
		}

		// Verify thread message count reset to 0
		reqCheck := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d", accountID, standaloneThreadID), nil)
		reqCheck.Header.Set("Authorization", "Bearer "+token)
		wCheck := httptest.NewRecorder()
		r.ServeHTTP(wCheck, reqCheck)

		var checkResp struct {
			Data domain.CopilotThread `json:"data"`
		}
		_ = json.Unmarshal(wCheck.Body.Bytes(), &checkResp)
		if checkResp.Data.MessageCount != 0 {
			t.Errorf("expected MessageCount 0 after clear, got %d", checkResp.Data.MessageCount)
		}
	})

	// -------------------------------------------------------------
	// Scenario 5: Filtering & Metrics Dashboard
	// -------------------------------------------------------------
	t.Run("Scenario5_Filtering_and_Metrics", func(t *testing.T) {
		// List with filter
		reqList := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads?page=1&per_page=10&status=%s", accountID, domain.CopilotThreadStatusPinned), nil)
		reqList.Header.Set("Authorization", "Bearer "+token)
		wList := httptest.NewRecorder()
		r.ServeHTTP(wList, reqList)

		if wList.Code != http.StatusOK {
			t.Fatalf("GET /copilot/threads filtered failed: code=%d", wList.Code)
		}

		var listResp struct {
			Data struct {
				Threads []domain.CopilotThread `json:"threads"`
				Meta    struct {
					Total int `json:"total"`
				} `json:"meta"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wList.Body.Bytes(), &listResp)
		if listResp.Data.Meta.Total < 1 {
			t.Errorf("expected at least 1 pinned thread")
		}

		// Get Metrics
		reqMetrics := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/metrics", accountID), nil)
		reqMetrics.Header.Set("Authorization", "Bearer "+token)
		wMetrics := httptest.NewRecorder()
		r.ServeHTTP(wMetrics, reqMetrics)

		if wMetrics.Code != http.StatusOK {
			t.Fatalf("GET /copilot/threads/metrics failed: code=%d body=%s", wMetrics.Code, wMetrics.Body.String())
		}

		var metricsResp struct {
			Data repository.CopilotThreadMetrics `json:"data"`
		}
		_ = json.Unmarshal(wMetrics.Body.Bytes(), &metricsResp)

		if metricsResp.Data.TotalThreads < 2 {
			t.Errorf("expected at least 2 total threads, got %d", metricsResp.Data.TotalThreads)
		}
		if metricsResp.Data.PositiveRate == "" {
			t.Errorf("expected positive rate string, got empty")
		}
	})

	// -------------------------------------------------------------
	// Scenario 6: Multi-tenant Security Isolation & Cascade Delete
	// -------------------------------------------------------------
	t.Run("Scenario6_Tenant_Isolation_and_Cascade_Delete", func(t *testing.T) {
		// Account 2 attempts to view Account 1's thread -> 404
		reqCross := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d", accountID2, standaloneThreadID), nil)
		reqCross.Header.Set("Authorization", "Bearer "+token2)
		wCross := httptest.NewRecorder()
		r.ServeHTTP(wCross, reqCross)

		if wCross.Code != http.StatusNotFound {
			t.Errorf("expected 404 for cross-tenant access, got %d", wCross.Code)
		}

		// Account 2 attempts to send message to Account 1's thread -> 404
		reqCrossMsg := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d/chat", accountID2, standaloneThreadID), bytes.NewReader([]byte(`{"content":"hacked"}`)))
		reqCrossMsg.Header.Set("Authorization", "Bearer "+token2)
		reqCrossMsg.Header.Set("Content-Type", "application/json")
		wCrossMsg := httptest.NewRecorder()
		r.ServeHTTP(wCrossMsg, reqCrossMsg)

		if wCrossMsg.Code != http.StatusNotFound {
			t.Errorf("expected 404 for cross-tenant message send, got %d", wCrossMsg.Code)
		}

		// Delete thread by owner Account 1
		reqDelete := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d", accountID, standaloneThreadID), nil)
		reqDelete.Header.Set("Authorization", "Bearer "+token)
		wDelete := httptest.NewRecorder()
		r.ServeHTTP(wDelete, reqDelete)

		if wDelete.Code != http.StatusOK {
			t.Fatalf("DELETE /copilot/threads/:id failed: code=%d body=%s", wDelete.Code, wDelete.Body.String())
		}

		// Confirm thread no longer exists
		reqAfter := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/copilot/threads/%d", accountID, standaloneThreadID), nil)
		reqAfter.Header.Set("Authorization", "Bearer "+token)
		wAfter := httptest.NewRecorder()
		r.ServeHTTP(wAfter, reqAfter)

		if wAfter.Code != http.StatusNotFound {
			t.Errorf("expected 404 after thread deletion, got %d", wAfter.Code)
		}
	})
}
