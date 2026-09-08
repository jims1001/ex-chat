package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestEndToEndBusinessScenarioVerifications(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "business_scenarios_jwt_secret_key_32b!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Admin Setup
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Business Verifier",
		"email":        "verifier@example.com",
		"password":     "Password123!",
		"account_name": "Scenario Verification Org",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(signUpBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
			User     domain.User      `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accID := authResp.Data.Accounts[0].ID
	accStr := strconv.FormatUint(uint64(accID), 10)
	adminUser := authResp.Data.User

	authReq := func(method, url string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		rq := httptest.NewRequest(method, url, reqBody)
		rq.Header.Set("Authorization", "Bearer "+token)
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, rq)
		return rec
	}

	// Create common Inbox & Contact
	inbox := domain.Inbox{
		AccountID:    accID,
		Name:         "Support Channel",
		ChannelType:  "Channel::WebWidget",
		WebsiteToken: "token_support_channel",
	}
	_ = db.Create(&inbox).Error

	contact := domain.Contact{
		AccountID: accID,
		Name:      "Test Customer",
		Email:     "customer@example.com",
	}
	_ = db.Create(&contact).Error

	// -------------------------------------------------------------
	// 场景 1：坐席容量限制 (Capacity Limit)
	// 坐席容量设为 1，已有 1 个未结束会话，不能再分配第 2 个会话
	// -------------------------------------------------------------
	t.Run("Scenario 1: Agent Capacity Limit", func(t *testing.T) {
		// 1. Create a dedicated agent
		agentUser := domain.User{
			Name:         "Limited Agent",
			Email:        "limited.agent@example.com",
			PasswordHash: "hashed",
			Role:         domain.RoleAgent,
			Availability: domain.AvailabilityOnline,
		}
		_ = db.Create(&agentUser).Error

		// Add agent to inbox members
		inboxMember := domain.InboxMember{
			InboxID: inbox.ID,
			UserID:  agentUser.ID,
		}
		_ = db.Create(&inboxMember).Error

		// Set capacity policy to 1 for this agent via API
		recCap := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/agent_capacity_policies", map[string]any{
			"user_id":            agentUser.ID,
			"conversation_limit": 1,
		})
		if recCap.Code != http.StatusOK && recCap.Code != http.StatusCreated {
			t.Fatalf("Set capacity policy failed: code=%d, body=%s", recCap.Code, recCap.Body.String())
		}

		// Create 1st conversation assigned to agent (status: open)
		conv1 := domain.Conversation{
			AccountID:  accID,
			InboxID:    inbox.ID,
			ContactID:  contact.ID,
			AssigneeID: &agentUser.ID,
			Status:     domain.ConversationStatusOpen,
		}
		_ = db.Create(&conv1).Error

		// Verify 1st conversation exists and is assigned
		var checkConv1 domain.Conversation
		db.First(&checkConv1, conv1.ID)
		if checkConv1.AssigneeID == nil || *checkConv1.AssigneeID != agentUser.ID {
			t.Fatalf("Expected conv1 assigned to agent %d", agentUser.ID)
		}

		// Test A: Auto-assign routing for 2nd conversation -> Agent reached capacity limit 1, must NOT be assigned
		convRepo := repository.NewConversationRepository(db)
		routingService := service.NewRoutingService(db, convRepo, hub)

		conv2 := domain.Conversation{
			AccountID: accID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = db.Create(&conv2).Error

		assignedAgent, err := routingService.AutoAssign(&conv2)
		if err != nil {
			t.Fatalf("AutoAssign error: %v", err)
		}
		if assignedAgent != nil {
			t.Fatalf("Expected conv2 to remain unassigned due to capacity limit 1, but got assigned to %d", assignedAgent.ID)
		}
		if conv2.AssigneeID != nil {
			t.Fatalf("Expected conv2.AssigneeID to be nil, got %v", *conv2.AssigneeID)
		}

		// Test B: Manual Assignment via API (POST /conversations/:id/assignments) -> Must reject with 400 Bad Request
		recAssign := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/assignments", accStr, conv2.ID), map[string]any{
			"assignee_id": agentUser.ID,
		})
		if recAssign.Code != http.StatusBadRequest {
			t.Fatalf("Expected manual assignment to be rejected with 400 when over capacity, got %d: %s", recAssign.Code, recAssign.Body.String())
		}

		// Test C: Direct creation with assignee_id via API (POST /conversations) -> Must reject with 400 Bad Request
		recCreateAssigned := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/conversations", map[string]any{
			"inbox_id":    inbox.ID,
			"contact_id":  contact.ID,
			"assignee_id": agentUser.ID,
		})
		if recCreateAssigned.Code != http.StatusBadRequest {
			t.Fatalf("Expected conversation creation with assignee over capacity to return 400, got %d: %s", recCreateAssigned.Code, recCreateAssigned.Body.String())
		}

		// Test D: Resolve 1st conversation -> Capacity freed up -> Auto-assign should now succeed
		_ = db.Model(&conv1).Update("status", domain.ConversationStatusResolved).Error

		assignedAfterResolve, err := routingService.AutoAssign(&conv2)
		if err != nil {
			t.Fatalf("AutoAssign error after resolve: %v", err)
		}
		if assignedAfterResolve == nil || assignedAfterResolve.ID != agentUser.ID {
			t.Fatalf("Expected conv2 assigned to agent %d after capacity freed up, got %+v", agentUser.ID, assignedAfterResolve)
		}
	})

	// -------------------------------------------------------------
	// 场景 2：自动化：仅 VIP 标签会话自动关闭
	// 没有 VIP 标签的会话不能被关闭
	// -------------------------------------------------------------
	t.Run("Scenario 2: Automation VIP Label Condition", func(t *testing.T) {
		// Create VIP and Normal labels
		vipLabel := domain.Label{AccountID: accID, Title: "VIP"}
		_ = db.FirstOrCreate(&vipLabel, vipLabel).Error

		normalLabel := domain.Label{AccountID: accID, Title: "Normal"}
		_ = db.FirstOrCreate(&normalLabel, normalLabel).Error

		// Create Automation Rule via API: Close conversation ONLY IF label is VIP
		ruleConditions := []map[string]any{
			{
				"attribute_key":   "labels",
				"filter_operator": "equal_to",
				"values":          []string{"VIP"},
				"query_operator":  "AND",
			},
		}
		ruleActions := []map[string]any{
			{
				"action_name": "resolve_conversation",
			},
		}
		recRule := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/automation_rules", map[string]any{
			"name":        "Auto Resolve VIP Tickets",
			"event_name":  "conversation_updated",
			"conditions":  ruleConditions,
			"actions":     ruleActions,
			"active":      true,
		})
		if recRule.Code != http.StatusOK && recRule.Code != http.StatusCreated {
			t.Fatalf("Create automation rule failed: code=%d, body=%s", recRule.Code, recRule.Body.String())
		}

		// Create VIP Conversation
		convVIP := domain.Conversation{
			AccountID: accID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = db.Create(&convVIP).Error
		clVIP := domain.ConversationLabel{ConversationID: convVIP.ID, LabelID: vipLabel.ID}
		_ = db.Create(&clVIP).Error

		// Create Non-VIP Conversation (with Normal label)
		convNonVIP := domain.Conversation{
			AccountID: accID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = db.Create(&convNonVIP).Error
		clNonVIP := domain.ConversationLabel{ConversationID: convNonVIP.ID, LabelID: normalLabel.ID}
		_ = db.Create(&clNonVIP).Error

		convRepo := repository.NewConversationRepository(db)
		msgRepo := repository.NewMessageRepository(db)
		autoService := service.NewAutomationService(db, convRepo, msgRepo)

		// Trigger automation for VIP conversation -> Must be RESOLVED
		autoService.HandleConversationUpdated(&convVIP)
		var checkVIP domain.Conversation
		db.First(&checkVIP, convVIP.ID)
		if checkVIP.Status != domain.ConversationStatusResolved {
			t.Fatalf("Expected VIP conversation to be resolved by automation rule, but status is %s", checkVIP.Status)
		}

		// Trigger automation for Non-VIP conversation -> Must REMAIN OPEN
		autoService.HandleConversationUpdated(&convNonVIP)
		var checkNonVIP domain.Conversation
		db.First(&checkNonVIP, convNonVIP.ID)
		if checkNonVIP.Status != domain.ConversationStatusOpen {
			t.Fatalf("Expected Non-VIP conversation to remain OPEN, but was resolved/closed: %s", checkNonVIP.Status)
		}
	})

	// -------------------------------------------------------------
	// 场景 3：SLA 首次回复判定 (内部备注不能当作对客回复)
	// 客户等待一小时，坐席只写内部备注，必须记录首次回复超时
	// -------------------------------------------------------------
	t.Run("Scenario 3: SLA First Response Ignores Internal Private Notes", func(t *testing.T) {
		// 1. Create SLA policy: First response time threshold = 60s
		recSLA := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/sla_policies", map[string]any{
			"name":                          "VIP Fast First Response",
			"first_response_time_threshold": 60,
			"resolution_time_threshold":     3600,
		})
		if recSLA.Code != http.StatusOK && recSLA.Code != http.StatusCreated {
			t.Fatalf("Create SLA policy failed: code=%d, body=%s", recSLA.Code, recSLA.Body.String())
		}
		var slaResp struct {
			Data domain.SLAPolicy `json:"data"`
		}
		_ = json.Unmarshal(recSLA.Body.Bytes(), &slaResp)
		slaPolicyID := slaResp.Data.ID

		// 2. Customer started conversation 2 hours ago
		twoHoursAgo := time.Now().UTC().Add(-2 * time.Hour)
		convSLA := domain.Conversation{
			AccountID:      accID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			SLAPolicyID:    &slaPolicyID,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      twoHoursAgo,
			LastActivityAt: twoHoursAgo,
		}
		_ = db.Create(&convSLA).Error
		_ = db.Model(&convSLA).Update("created_at", twoHoursAgo).Error

		// Customer incoming message 2 hours ago
		custMsg := domain.Message{
			AccountID:      accID,
			ConversationID: convSLA.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "I need help urgently!",
			CreatedAt:      twoHoursAgo,
		}
		_ = db.Create(&custMsg).Error
		_ = db.Model(&custMsg).Update("created_at", twoHoursAgo).Error

		// 3. Agent writes an internal private note via API (private: true)
		recNote := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/messages", accStr, convSLA.ID), map[string]any{
			"content":      "Agent private internal note: checking with engineering team",
			"private_note": true,
		})
		if recNote.Code != http.StatusOK && recNote.Code != http.StatusCreated {
			t.Fatalf("Create internal note message failed: code=%d, body=%s", recNote.Code, recNote.Body.String())
		}

		// 4. Trigger SLA evaluation via API
		recProc := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/sla_policies/process", nil)
		if recProc.Code != http.StatusOK {
			t.Fatalf("Process SLA failed: code=%d, body=%s", recProc.Code, recProc.Body.String())
		}

		// 5. Query SLA breaches via API
		recBreaches := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/sla_policies/breaches", nil)
		if recBreaches.Code != http.StatusOK {
			t.Fatalf("List SLA breaches failed: code=%d, body=%s", recBreaches.Code, recBreaches.Body.String())
		}

		var breachesResp struct {
			Data []domain.SLABreachLog `json:"data"`
		}
		_ = json.Unmarshal(recBreaches.Body.Bytes(), &breachesResp)

		foundFirstResponseBreach := false
		for _, b := range breachesResp.Data {
			if b.ConversationID == convSLA.ID && b.BreachType == "first_response" {
				foundFirstResponseBreach = true
				break
			}
		}

		if !foundFirstResponseBreach {
			t.Fatalf("Expected first_response SLA breach when agent only wrote internal note, but none was recorded: %+v", breachesResp.Data)
		}

		// Verify conversation sla_status is marked 'breached'
		var checkConvSLA domain.Conversation
		db.First(&checkConvSLA, convSLA.ID)
		if checkConvSLA.SLAStatus != "breached" {
			t.Fatalf("Expected conversation SLAStatus to be 'breached', got '%s'", checkConvSLA.SLAStatus)
		}
	})

	// -------------------------------------------------------------
	// 场景 4：宏：发送回复后更新会话活跃时间与排序
	// 宏发送消息后，会话最近活跃时间必须更新为当前时间，会话列表排序更新
	// -------------------------------------------------------------
	t.Run("Scenario 4: Macro Execution Updates LastActivityAt and List Ordering", func(t *testing.T) {
		// Create older conversation with last_activity_at 3 hours ago
		threeHoursAgo := time.Now().UTC().Add(-3 * time.Hour)
		convOld := domain.Conversation{
			AccountID:      accID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			CreatedAt:      threeHoursAgo,
			LastActivityAt: threeHoursAgo,
		}
		_ = db.Create(&convOld).Error
		_ = db.Model(&convOld).Updates(map[string]any{
			"created_at":       threeHoursAgo,
			"last_activity_at": threeHoursAgo,
		}).Error

		// Create newer conversation with last_activity_at 1 hour ago
		oneHourAgo := time.Now().UTC().Add(-1 * time.Hour)
		convNewer := domain.Conversation{
			AccountID:      accID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			CreatedAt:      oneHourAgo,
			LastActivityAt: oneHourAgo,
		}
		_ = db.Create(&convNewer).Error
		_ = db.Model(&convNewer).Updates(map[string]any{
			"created_at":       oneHourAgo,
			"last_activity_at": oneHourAgo,
		}).Error

		// Verify before macro: convNewer (1 hr ago) appears before convOld (3 hrs ago) in conversation list
		recListBefore := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/conversations", nil)
		if recListBefore.Code != http.StatusOK {
			t.Fatalf("List conversations before macro failed: %d", recListBefore.Code)
		}
		var listBefore struct {
			Data struct {
				Items []domain.Conversation `json:"items"`
			} `json:"data"`
		}
		_ = json.Unmarshal(recListBefore.Body.Bytes(), &listBefore)
		posOld := -1
		posNewer := -1
		for idx, item := range listBefore.Data.Items {
			if item.ID == convOld.ID {
				posOld = idx
			}
			if item.ID == convNewer.ID {
				posNewer = idx
			}
		}
		if posNewer == -1 || posOld == -1 || posNewer > posOld {
			t.Fatalf("Expected convNewer (%d, pos %d) to appear before convOld (%d, pos %d) before macro", convNewer.ID, posNewer, convOld.ID, posOld)
		}

		// Create Macro via API to send automated reply
		recMacro := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/macros", map[string]any{
			"name": "Send Automated Customer Reply",
			"actions": []map[string]any{
				{
					"action_name":   "send_message",
					"action_params": []string{"Thank you for contacting us! We are resolving your issue."},
				},
			},
		})
		if recMacro.Code != http.StatusOK && recMacro.Code != http.StatusCreated {
			t.Fatalf("Create macro failed: code=%d, body=%s", recMacro.Code, recMacro.Body.String())
		}
		var macroResp struct {
			Data domain.Macro `json:"data"`
		}
		_ = json.Unmarshal(recMacro.Body.Bytes(), &macroResp)
		macroID := macroResp.Data.ID

		// Execute macro on convOld via API
		recExec := authReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/macros/%d/execute", accStr, macroID), map[string]any{
			"conversation_ids": []uint{convOld.ID},
		})
		if recExec.Code != http.StatusOK {
			t.Fatalf("Execute macro failed: code=%d, body=%s", recExec.Code, recExec.Body.String())
		}

		// Verify convOld LastActivityAt is updated to now (diff < 10 seconds)
		var checkConvOld domain.Conversation
		db.First(&checkConvOld, convOld.ID)
		if checkConvOld.LastActivityAt.Before(time.Now().UTC().Add(-10 * time.Second)) {
			t.Fatalf("Expected convOld LastActivityAt to be refreshed to recent time, got %v (diff %v)",
				checkConvOld.LastActivityAt, time.Since(checkConvOld.LastActivityAt))
		}

		// Verify conversation list ordering: convOld should now be at the very TOP (index 0) of the list
		recListAfter := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/conversations", nil)
		if recListAfter.Code != http.StatusOK {
			t.Fatalf("List conversations after macro failed: %d", recListAfter.Code)
		}
		var listAfter struct {
			Data struct {
				Items []domain.Conversation `json:"items"`
			} `json:"data"`
		}
		_ = json.Unmarshal(recListAfter.Body.Bytes(), &listAfter)
		if len(listAfter.Data.Items) == 0 {
			t.Fatalf("Expected conversations in list after macro, got 0")
		}
		if listAfter.Data.Items[0].ID != convOld.ID {
			t.Fatalf("Expected convOld (%d) to move to index 0 of conversation list after macro execution, but got %d (convOld last_activity_at: %v, top last_activity_at: %v)",
				convOld.ID, listAfter.Data.Items[0].ID, checkConvOld.LastActivityAt, listAfter.Data.Items[0].LastActivityAt)
		}
	})

	_ = adminUser
}
