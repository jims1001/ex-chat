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

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestWidgetSecurityAndRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:widget_security_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_security_rbac_32chars!!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user in Account 1
	signUpPayload := map[string]string{
		"name":         "Security Admin",
		"email":        fmt.Sprintf("sec_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Security Corp",
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
	adminToken := authResp.Data.Token
	account1ID := authResp.Data.Accounts[0].ID
	adminUser := authResp.Data.User

	// Create Account 2 and Admin 2 for cross-account testing
	account2 := domain.Account{Name: "Account Two Org"}
	db.Create(&account2)

	user2 := domain.User{
		Name:         "Foreign Agent",
		Email:        fmt.Sprintf("foreign_agent_%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	db.Create(&user2)
	db.Create(&domain.AccountUser{AccountID: account2.ID, UserID: user2.ID, Role: domain.RoleAgent})

	// Create Team in Account 2
	teamAcc2 := domain.Team{AccountID: account2.ID, Name: "Account 2 Team"}
	db.Create(&teamAcc2)

	// Create Agent in Account 1
	agentAcc1 := domain.User{
		Name:         "Account 1 Agent",
		Email:        fmt.Sprintf("agent1_%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	db.Create(&agentAcc1)
	db.Create(&domain.AccountUser{AccountID: account1ID, UserID: agentAcc1.ID, Role: domain.RoleAgent})
	agent1Token, _ := auth.GenerateToken(&agentAcc1, cfg.JWTSecret, 24)

	// Create Website Inbox in Account 1
	websiteToken := fmt.Sprintf("web_token_%d", time.Now().UnixNano())
	inbox := domain.Inbox{
		AccountID:    account1ID,
		Name:         "Widget Inbox",
		ChannelType:  "Channel::WebWidget",
		WebsiteToken: websiteToken,
	}
	db.Create(&inbox)

	// Create Visitor 1 & Visitor 2
	visitor1SourceID := "visitor_alice_token_111"
	visitor2SourceID := "visitor_bob_token_222"

	// Visitor 1 creates a conversation
	v1ConvBody, _ := json.Marshal(map[string]string{
		"source_id": visitor1SourceID,
		"message":   "Hi from Alice!",
	})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations?website_token=%s", websiteToken), bytes.NewReader(v1ConvBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("v1 conversation creation failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var v1ConvResp struct {
		Data struct {
			Conversation domain.Conversation `json:"conversation"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v1ConvResp)
	conv1ID := v1ConvResp.Data.Conversation.ID
	contact1ID := v1ConvResp.Data.Conversation.ContactID

	// Visitor 2 identifies
	v2IdentifyBody, _ := json.Marshal(map[string]string{
		"source_id": visitor2SourceID,
		"name":      "Bob Visitor",
	})
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/contact?website_token=%s", websiteToken), bytes.NewReader(v2IdentifyBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("v2 identify failed: code=%d", w.Code)
	}

	// =========================================================================
	// 1. Widget 越权访问防护测试
	// =========================================================================
	t.Run("Widget Cross-Visitor Enumeration and Impersonation Prevention", func(t *testing.T) {
		// A. Visitor 2 attempts to list messages of Visitor 1's conversation -> 404
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/messages?website_token=%s&conversation_id=%d&source_id=%s", websiteToken, conv1ID, visitor2SourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for visitor 2 reading visitor 1 messages, got %d", w.Code)
		}

		// Visitor 1 reading own messages -> 200
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/messages?website_token=%s&conversation_id=%d&source_id=%s", websiteToken, conv1ID, visitor1SourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for visitor 1 reading own messages, got %d", w.Code)
		}

		// B. Visitor 2 attempts to write a message into Visitor 1's conversation -> 404
		impersonateMsgBody, _ := json.Marshal(map[string]any{
			"source_id":       visitor2SourceID,
			"conversation_id": conv1ID,
			"content":         "Hacked message into Alice conversation",
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/messages?website_token=%s", websiteToken), bytes.NewReader(impersonateMsgBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for visitor 2 posting to visitor 1 conversation, got %d", w.Code)
		}

		// C. Visitor 2 attempts update_last_seen on Visitor 1's conversation -> 404
		lastSeenBody, _ := json.Marshal(map[string]any{
			"source_id":       visitor2SourceID,
			"conversation_id": conv1ID,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/update_last_seen?website_token=%s", websiteToken), bytes.NewReader(lastSeenBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for visitor 2 updating last_seen on visitor 1 conversation, got %d", w.Code)
		}

		// D. Visitor 2 attempts toggle_typing on Visitor 1's conversation -> 404
		typingBody, _ := json.Marshal(map[string]any{
			"source_id":       visitor2SourceID,
			"conversation_id": conv1ID,
			"typing_status":   "on",
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/toggle_typing?website_token=%s", websiteToken), bytes.NewReader(typingBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for visitor 2 toggling typing on visitor 1 conversation, got %d", w.Code)
		}

		// E. Visitor 2 attempts send_transcript on Visitor 1's conversation -> 404
		transcriptBody, _ := json.Marshal(map[string]any{
			"source_id":       visitor2SourceID,
			"conversation_id": conv1ID,
			"email":           "intruder@example.com",
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/transcript?website_token=%s", websiteToken), bytes.NewReader(transcriptBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for visitor 2 requesting transcript of visitor 1 conversation, got %d", w.Code)
		}
	})

	// =========================================================================
	// 2. 会话状态与优先级白名单校验
	// =========================================================================
	t.Run("Status and Priority Enum Whitelist Validation", func(t *testing.T) {
		// A. Update conversation with invalid status -> 400
		invalidStatusBody, _ := json.Marshal(map[string]any{
			"status": "flying_away",
		})
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", account1ID, conv1ID), bytes.NewReader(invalidStatusBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid status, got %d", w.Code)
		}

		// B. Update conversation with invalid priority -> 400
		invalidPriorityBody, _ := json.Marshal(map[string]any{
			"priority": "super_mega_urgent",
		})
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", account1ID, conv1ID), bytes.NewReader(invalidPriorityBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid priority in UpdateConversation, got %d", w.Code)
		}

		// C. SetPriority with invalid priority -> 400
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/priority", account1ID, conv1ID), bytes.NewReader(invalidPriorityBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid priority in SetPriority, got %d", w.Code)
		}

		// D. Valid priority -> 200
		validPriorityBody, _ := json.Marshal(map[string]any{
			"priority": domain.PriorityUrgent,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/priority", account1ID, conv1ID), bytes.NewReader(validPriorityBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for valid priority, got %d", w.Code)
		}
	})

	// =========================================================================
	// 3. 跨账号客服与团队分配校验
	// =========================================================================
	t.Run("Cross-Account Assignee and Team Validation", func(t *testing.T) {
		// A. Assign user2 (from Account 2) to conversation in Account 1 via /assignments -> 400
		crossAssignBody, _ := json.Marshal(map[string]any{
			"assignee_id": user2.ID,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", account1ID, conv1ID), bytes.NewReader(crossAssignBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for cross-account assignee in /assignments, got %d", w.Code)
		}

		// B. Assign teamAcc2 (from Account 2) to conversation in Account 1 via /assignments -> 400
		crossTeamBody, _ := json.Marshal(map[string]any{
			"team_id": teamAcc2.ID,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", account1ID, conv1ID), bytes.NewReader(crossTeamBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for cross-account team in /assignments, got %d", w.Code)
		}

		// C. Assign via PUT /conversations/:id with user2 -> 400
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", account1ID, conv1ID), bytes.NewReader(crossAssignBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for cross-account assignee in UpdateConversation, got %d", w.Code)
		}

		// D. Assign via PUT /conversations/:id with teamAcc2 -> 400
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", account1ID, conv1ID), bytes.NewReader(crossTeamBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for cross-account team in UpdateConversation, got %d", w.Code)
		}

		// E. Valid assignment with agentAcc1 -> 200
		validAssignBody, _ := json.Marshal(map[string]any{
			"assignee_id": agentAcc1.ID,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", account1ID, conv1ID), bytes.NewReader(validAssignBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for valid local assignee, got %d", w.Code)
		}
	})

	// =========================================================================
	// 4. 消息编辑与删除的作者与类型保护
	// =========================================================================
	t.Run("Message Editing and Deletion Restrictions", func(t *testing.T) {
		// Agent 1 sends outgoing message
		agentMsgBody, _ := json.Marshal(map[string]any{
			"content": "Message from Agent 1",
			"private": false,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", account1ID, conv1ID), bytes.NewReader(agentMsgBody))
		req.Header.Set("Authorization", "Bearer "+agent1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("agent 1 create message failed: %d", w.Code)
		}

		var msgResp struct {
			Data domain.Message `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &msgResp)
		agentMsgID := msgResp.Data.ID

		// Create another agent in Account 1
		agentAcc1B := domain.User{
			Name:         "Account 1 Agent B",
			Email:        fmt.Sprintf("agent1b_%d@example.com", time.Now().UnixNano()),
			PasswordHash: "hashed",
			Role:         domain.RoleAgent,
		}
		db.Create(&agentAcc1B)
		db.Create(&domain.AccountUser{AccountID: account1ID, UserID: agentAcc1B.ID, Role: domain.RoleAgent})
		agent1BToken, _ := auth.GenerateToken(&agentAcc1B, cfg.JWTSecret, 24)

		// A. Agent 1B tries to edit Agent 1's message -> 403 Forbidden
		editBody, _ := json.Marshal(map[string]any{
			"content": "Malicious edit by Agent 1B",
		})
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", account1ID, conv1ID, agentMsgID), bytes.NewReader(editBody))
		req.Header.Set("Authorization", "Bearer "+agent1BToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for non-author non-admin edit, got %d", w.Code)
		}

		// B. Agent 1 (author) edits own message -> 200 OK
		editBodyAuthor, _ := json.Marshal(map[string]any{
			"content": "Author edit by Agent 1",
		})
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", account1ID, conv1ID, agentMsgID), bytes.NewReader(editBodyAuthor))
		req.Header.Set("Authorization", "Bearer "+agent1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for author edit, got %d", w.Code)
		}

		// C. Attempt to edit Visitor 1's message -> 400 Bad Request
		// Find visitor 1's first incoming message
		var v1Msg domain.Message
		db.Where("conversation_id = ? AND sender_type = ?", conv1ID, domain.SenderTypeContact).First(&v1Msg)
		if v1Msg.ID > 0 {
			req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", account1ID, conv1ID, v1Msg.ID), bytes.NewReader(editBodyAuthor))
			req.Header.Set("Authorization", "Bearer "+adminToken)
			req.Header.Set("Content-Type", "application/json")
			w = httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 Bad Request when editing visitor message, got %d", w.Code)
			}
		}

		// D. Create an activity message and attempt to delete -> 400 Bad Request
		activityMsg := domain.Message{
			AccountID:      account1ID,
			ConversationID: conv1ID,
			MessageType:    domain.MessageTypeActivity,
			ContentType:    domain.ContentTypeText,
			Content:        "Conversation resolved by system",
		}
		db.Create(&activityMsg)

		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", account1ID, conv1ID, activityMsg.ID), nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request when deleting activity message, got %d", w.Code)
		}

		// E. Agent 1B tries to delete Agent 1's message -> 403 Forbidden
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", account1ID, conv1ID, agentMsgID), nil)
		req.Header.Set("Authorization", "Bearer "+agent1BToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for non-author non-admin delete, got %d", w.Code)
		}

		// F. Admin can delete Agent 1's message -> 200 OK
		req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", account1ID, conv1ID, agentMsgID), nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for admin deleting message, got %d", w.Code)
		}
	})

	// =========================================================================
	// 5. 自定义角色与 RBAC 严格权限控制
	// =========================================================================
	t.Run("Custom Role RBAC for Contacts and Conversations", func(t *testing.T) {
		// Create custom role with ONLY "contact_manage", but NO "conversation_manage"
		customRoleContactsOnly := domain.CustomRole{
			AccountID:   account1ID,
			Name:        "CRM Specialist",
			Permissions: `["contact_manage"]`,
		}
		db.Create(&customRoleContactsOnly)

		userCrmOnly := domain.User{
			Name:         "CRM Only User",
			Email:        fmt.Sprintf("crm_only_%d@example.com", time.Now().UnixNano()),
			PasswordHash: "hashed",
			Role:         domain.RoleAgent,
		}
		db.Create(&userCrmOnly)
		db.Create(&domain.AccountUser{
			AccountID:    account1ID,
			UserID:       userCrmOnly.ID,
			Role:         domain.RoleAgent,
			CustomRoleID: &customRoleContactsOnly.ID,
		})
		crmOnlyToken, _ := auth.GenerateToken(&userCrmOnly, cfg.JWTSecret, 24)

		// A. userCrmOnly can create contact (has contact_manage) -> 201 Created
		newContactBody, _ := json.Marshal(map[string]any{
			"name":  "New Lead",
			"email": "lead@example.com",
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts", account1ID), bytes.NewReader(newContactBody))
		req.Header.Set("Authorization", "Bearer "+crmOnlyToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("expected success for user with contact_manage creating contact, got %d", w.Code)
		}

		// B. userCrmOnly tries to create conversation (lacks conversation_manage) -> 403 Forbidden
		newConvBody, _ := json.Marshal(map[string]any{
			"inbox_id":   inbox.ID,
			"contact_id": contact1ID,
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", account1ID), bytes.NewReader(newConvBody))
		req.Header.Set("Authorization", "Bearer "+crmOnlyToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for user without conversation_manage creating conversation, got %d", w.Code)
		}

		// C. userCrmOnly tries to send message in conversation -> 403 Forbidden
		sendMsgBody, _ := json.Marshal(map[string]any{
			"content": "Hello from CRM only",
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", account1ID, conv1ID), bytes.NewReader(sendMsgBody))
		req.Header.Set("Authorization", "Bearer "+crmOnlyToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for user without conversation_manage sending message, got %d", w.Code)
		}
	})

	// =========================================================================
	// 6. Widget 私密备注隐藏防护测试 (GetConversationDetail & ListConversations)
	// =========================================================================
	t.Run("Widget Private Note Masking", func(t *testing.T) {
		// Create a private internal note in conv1
		privateMsg := domain.Message{
			AccountID:      account1ID,
			ConversationID: conv1ID,
			MessageType:    domain.MessageTypeActivity,
			SenderType:     domain.SenderTypeUser,
			SenderID:       adminUser.ID,
			Content:        "Confidential agent internal note - secret discount code 999",
			Private:        true,
		}
		db.Create(&privateMsg)

		// Visitor 1 fetches conversation detail
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/conversations/%d?website_token=%s&source_id=%s", conv1ID, websiteToken, visitor1SourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for visitor 1 reading conversation, got %d", w.Code)
		}

		var detailResp struct {
			Data struct {
				Messages []domain.Message `json:"messages"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &detailResp)
		for _, m := range detailResp.Data.Messages {
			if m.Private {
				t.Fatalf("leak detected: visitor can see private message ID %d", m.ID)
			}
			if strings.Contains(m.Content, "Confidential agent internal note") {
				t.Fatalf("leak detected: visitor can see confidential content")
			}
		}

		// Visitor 1 lists conversation history
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/conversations?website_token=%s&source_id=%s", websiteToken, visitor1SourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for listing conversations, got %d", w.Code)
		}
		var listResp struct {
			Data []struct {
				Messages []domain.Message `json:"messages"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &listResp)
		for _, conv := range listResp.Data {
			for _, m := range conv.Messages {
				if m.Private || strings.Contains(m.Content, "Confidential agent internal note") {
					t.Fatalf("leak detected in list conversations: private message exposed")
				}
			}
		}
	})

	// =========================================================================
	// 7. Widget 跨访客消息修改与客服消息防篡改防护
	// =========================================================================
	t.Run("Widget UpdateMessage Protection", func(t *testing.T) {
		// Find Alice's message in conv1
		var aliceMsg domain.Message
		db.Where("conversation_id = ? AND sender_type = ?", conv1ID, domain.SenderTypeContact).First(&aliceMsg)
		if aliceMsg.ID == 0 {
			t.Fatalf("alice message not found")
		}

		// Visitor 2 (Bob) tries to modify Alice's message -> 404 (or visitor profile mismatch)
		updateBodyBob, _ := json.Marshal(map[string]any{
			"source_id": visitor2SourceID,
			"content":   "Hacked by Bob!",
		})
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/widget/messages/%d?website_token=%s", aliceMsg.ID, websiteToken), bytes.NewReader(updateBodyBob))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when visitor 2 tries to update visitor 1 message, got %d", w.Code)
		}

		// Find an agent message in conv1
		var agentMsg domain.Message
		db.Where("conversation_id = ? AND sender_type = ?", conv1ID, domain.SenderTypeUser).First(&agentMsg)
		if agentMsg.ID != 0 {
			// Visitor 1 tries to alter agent message content -> 403 Forbidden
			updateAgentMsg, _ := json.Marshal(map[string]any{
				"source_id": visitor1SourceID,
				"content":   "Forged agent message",
			})
			req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/widget/messages/%d?website_token=%s", agentMsg.ID, websiteToken), bytes.NewReader(updateAgentMsg))
			req.Header.Set("Content-Type", "application/json")
			w = httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 Forbidden when visitor tries to rewrite agent message content, got %d", w.Code)
			}
		}
	})

	// =========================================================================
	// 8. Widget 事件查询隔离与跨访客保护
	// =========================================================================
	t.Run("Widget ListEvents Isolation", func(t *testing.T) {
		// Requesting events with no source_id -> should return empty array
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/events?website_token=%s", websiteToken), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for empty events request, got %d", w.Code)
		}
		var emptyResp struct {
			Data []any `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &emptyResp)
		if len(emptyResp.Data) != 0 {
			t.Fatalf("expected 0 events without source_id, got %d", len(emptyResp.Data))
		}

		// Visitor 2 queries events specifying conv1ID (Visitor 1's conversation) -> 404
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/events?website_token=%s&source_id=%s&conversation_id=%d", websiteToken, visitor2SourceID, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when querying events of another visitor's conversation, got %d", w.Code)
		}
	})

	// =========================================================================
	// 9. Widget 标签操作跨访客防护
	// =========================================================================
	t.Run("Widget Labels Cross-Visitor Protection", func(t *testing.T) {
		// Visitor 2 attempts to add labels to Visitor 1's conversation -> 404
		addLabelBody, _ := json.Marshal(map[string]any{
			"source_id":       visitor2SourceID,
			"conversation_id": conv1ID,
			"labels":          []string{"vip", "escalated"},
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/labels?website_token=%s", websiteToken), bytes.NewReader(addLabelBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when visitor 2 tries to label visitor 1 conversation, got %d", w.Code)
		}

		// Visitor 2 attempts to get labels for conv1ID -> 404
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/labels?website_token=%s&source_id=%s&conversation_id=%d", websiteToken, visitor2SourceID, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when visitor 2 reads labels of visitor 1 conversation, got %d", w.Code)
		}
	})

	// =========================================================================
	// 10. DirectUpload 安全校验与带 Key 接收端真实上传测试
	// =========================================================================
	t.Run("DirectUpload Security and WithKey Handler", func(t *testing.T) {
		// A. Direct upload without visitor identity -> 400
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/direct_uploads?website_token=%s", websiteToken), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 without visitor identity, got %d", w.Code)
		}

		// B. Direct upload with dangerous HTML/SVG file -> 400
		svgReqBody, _ := json.Marshal(map[string]any{
			"blob": map[string]any{
				"filename":     "attack.svg",
				"content_type": "image/svg+xml",
				"byte_size":    100,
			},
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/direct_uploads?website_token=%s&source_id=%s", websiteToken, visitor1SourceID), bytes.NewReader(svgReqBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when uploading svg/html, got %d", w.Code)
		}

		// C. Direct upload init for valid image -> 200 with direct_upload url
		validImgBody, _ := json.Marshal(map[string]any{
			"blob": map[string]any{
				"filename":     "photo.png",
				"content_type": "image/png",
				"byte_size":    2048,
			},
		})
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/direct_uploads?website_token=%s&source_id=%s", websiteToken, visitor1SourceID), bytes.NewReader(validImgBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for valid direct upload init, got %d body=%s", w.Code, w.Body.String())
		}
		var uploadInitResp struct {
			Key          string `json:"key"`
			DirectUpload struct {
				URL string `json:"url"`
			} `json:"direct_upload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &uploadInitResp)
		if uploadInitResp.Key == "" || uploadInitResp.DirectUpload.URL == "" {
			t.Fatalf("missing key or url in direct upload init resp")
		}

		// D. Client writes binary data to direct_upload PUT endpoint
		fakeImageData := []byte("fake png binary bytes header 1234567890")
		req = httptest.NewRequest(http.MethodPut, uploadInitResp.DirectUpload.URL, bytes.NewReader(fakeImageData))
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for direct upload binary PUT, got %d body=%s", w.Code, w.Body.String())
		}
	})

	// =========================================================================
	// 11. 联系人导出权限与导入上限测试
	// =========================================================================
	t.Run("Contact Export RBAC and Import Limits", func(t *testing.T) {
		// Create an agent user who DOES NOT have contact_manage permission
		customRoleNoContact := domain.CustomRole{
			AccountID:   account1ID,
			Name:        "Support Agent No Contact",
			Permissions: `["conversation_manage"]`,
		}
		db.Create(&customRoleNoContact)

		userNoContact := domain.User{
			Name:         "No Contact Agent",
			Email:        fmt.Sprintf("no_contact_%d@example.com", time.Now().UnixNano()),
			PasswordHash: "hashed",
			Role:         domain.RoleAgent,
		}
		db.Create(&userNoContact)
		db.Create(&domain.AccountUser{
			AccountID:    account1ID,
			UserID:       userNoContact.ID,
			Role:         domain.RoleAgent,
			CustomRoleID: &customRoleNoContact.ID,
		})
		noContactToken, _ := auth.GenerateToken(&userNoContact, cfg.JWTSecret, 24)

		// A. User without contact_manage tries to export contacts -> 403 Forbidden
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/export", account1ID), nil)
		req.Header.Set("Authorization", "Bearer "+noContactToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for contacts export without permission, got %d", w.Code)
		}

		// Admin user exports contacts -> 200 OK
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/contacts/export", account1ID), nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for admin exporting contacts, got %d", w.Code)
		}

		// B. Import contacts exceeding 5000 records limit -> 400
		var largeBatch []map[string]string
		for i := 0; i < 5005; i++ {
			largeBatch = append(largeBatch, map[string]string{
				"name":  fmt.Sprintf("Bulk Contact %d", i),
				"email": fmt.Sprintf("bulk_%d@example.com", i),
			})
		}
		largeBatchBody, _ := json.Marshal(largeBatch)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/import", account1ID), bytes.NewReader(largeBatchBody))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request when importing > 5000 contacts, got %d", w.Code)
		}
	})

	_ = adminUser
}
