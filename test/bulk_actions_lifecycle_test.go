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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestBulkActions_FullLifecycle_Automation_Notifications_Events(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_bulk_actions_123456",
		JWTExpirationHours: 72,
	}
	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user and tenant
	signUpPayload := map[string]string{
		"name":         "Bulk Admin",
		"email":        "bulk_admin@example.com",
		"password":     "Secret123!",
		"account_name": "Bulk Enterprise",
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
	adminUser := authResp.Data.User

	// 2. Create Agent B in the account
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	convRepo := repository.NewConversationRepository(db)
	notifRepo := repository.NewNotificationRepository(db)

	agentB := domain.User{
		Name:         "Agent Bob",
		Email:        "bob@example.com",
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&agentB)
	_ = accountRepo.AddMember(accountID, agentB.ID, domain.RoleAgent)

	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "Bulk Support Inbox",
		WebsiteToken: "bulk_token_123",
	}
	_ = inboxRepo.Create(&inbox)
	_ = inboxRepo.AddMember(inbox.ID, adminUser.ID)
	_ = inboxRepo.AddMember(inbox.ID, agentB.ID)

	// -------------------------------------------------------------
	// Scenario 1: 批量更新状态并触发自动化规则 (Automation Execution)
	// -------------------------------------------------------------
	t.Run("Scenario1_BulkStatusUpdate_TriggersAutomationRules", func(t *testing.T) {
		// Create Automation Rule: When conversation is updated to 'resolved', add label 'AUTO_RESOLVED'
		autoLabel := domain.Label{
			AccountID: accountID,
			Title:     "AUTO_RESOLVED",
		}
		_ = db.Create(&autoLabel).Error

		ruleConditions, _ := json.Marshal([]map[string]any{
			{
				"attribute_key":   "status",
				"filter_operator": "equal_to",
				"values":          []string{"resolved"},
				"query_operator":  "AND",
			},
		})
		ruleActions, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "add_label",
				"action_params": []string{"AUTO_RESOLVED"},
			},
		})
		autoRule := domain.AutomationRule{
			AccountID:  accountID,
			Name:       "Auto Label On Resolved",
			EventName:  "conversation_updated",
			Conditions: string(ruleConditions),
			Actions:    string(ruleActions),
			Active:     true,
		}
		_ = db.Create(&autoRule).Error

		// Create 3 open conversations
		var convIDs []uint
		for i := 1; i <= 3; i++ {
			c := domain.Conversation{
				AccountID: accountID,
				InboxID:   inbox.ID,
				ContactID: uint(10 + i),
				Status:    domain.ConversationStatusOpen,
			}
			_ = convRepo.Create(&c)
			convIDs = append(convIDs, c.ID)
		}

		// Perform Bulk Action: update_status -> resolved
		bulkPayload := map[string]any{
			"type": "update_status",
			"ids":  convIDs,
			"fields": map[string]any{
				"status": "resolved",
			},
		}
		bPayload, _ := json.Marshal(bulkPayload)
		reqBulk := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bPayload))
		reqBulk.Header.Set("Authorization", "Bearer "+token)
		reqBulk.Header.Set("Content-Type", "application/json")
		wBulk := httptest.NewRecorder()
		r.ServeHTTP(wBulk, reqBulk)

		if wBulk.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", wBulk.Code, wBulk.Body.String())
		}

		var resp struct {
			Data struct {
				Status       string `json:"status"`
				UpdatedCount int64  `json:"updated_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wBulk.Body.Bytes(), &resp)
		if resp.Data.UpdatedCount != 3 {
			t.Fatalf("expected updated_count 3, got %d", resp.Data.UpdatedCount)
		}

		// Verify that ALL 3 conversations were resolved AND have the AUTO_RESOLVED label attached by automation!
		for _, cid := range convIDs {
			refreshed, _ := convRepo.FindByID(accountID, cid)
			if refreshed.Status != domain.ConversationStatusResolved {
				t.Fatalf("conversation %d status expected 'resolved', got %s", cid, refreshed.Status)
			}

			// Check label
			var count int64
			db.Table("conversation_labels").Where("conversation_id = ? AND label_id = ?", cid, autoLabel.ID).Count(&count)
			if count != 1 {
				t.Fatalf("expected conversation %d to have AUTO_RESOLVED label attached by automation rule, but count was %d", cid, count)
			}
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: 批量指派坐席并触发站内通知与推送 (Notifications Pipeline)
	// -------------------------------------------------------------
	t.Run("Scenario2_BulkAssignAgent_TriggersNotifications", func(t *testing.T) {
		// Create 2 open conversations
		c1 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: 31, Status: domain.ConversationStatusOpen}
		c2 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: 32, Status: domain.ConversationStatusOpen}
		_ = convRepo.Create(&c1)
		_ = convRepo.Create(&c2)

		// Perform Bulk Action: assign_agent -> agentB
		bulkPayload := map[string]any{
			"type": "assign_agent",
			"ids":  []uint{c1.ID, c2.ID},
			"fields": map[string]any{
				"assignee_id": agentB.ID,
			},
		}
		bPayload, _ := json.Marshal(bulkPayload)
		reqBulk := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bPayload))
		reqBulk.Header.Set("Authorization", "Bearer "+token)
		reqBulk.Header.Set("Content-Type", "application/json")
		wBulk := httptest.NewRecorder()
		r.ServeHTTP(wBulk, reqBulk)

		if wBulk.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", wBulk.Code, wBulk.Body.String())
		}

		// Verify In-App Notifications were generated for agentB
		notifs, err := notifRepo.List(nil, accountID, agentB.ID)
		if err != nil {
			t.Fatalf("failed to list notifications: %v", err)
		}

		foundC1 := false
		foundC2 := false
		for _, n := range notifs {
			if n.NotificationType == domain.NotificationTypeConversationAssignment {
				if n.PrimaryActorID == c1.ID {
					foundC1 = true
				}
				if n.PrimaryActorID == c2.ID {
					foundC2 = true
				}
			}
		}

		if !foundC1 || !foundC2 {
			t.Fatalf("expected in-app notifications for both conversations c1 (%v) and c2 (%v), but got notifs: %+v", foundC1, foundC2, notifs)
		}

		// Verify conversations assignees updated
		ref1, _ := convRepo.FindByID(accountID, c1.ID)
		ref2, _ := convRepo.FindByID(accountID, c2.ID)
		if ref1.AssigneeID == nil || *ref1.AssigneeID != agentB.ID {
			t.Fatalf("expected ref1 assignee to be agentB (%d)", agentB.ID)
		}
		if ref2.AssigneeID == nil || *ref2.AssigneeID != agentB.ID {
			t.Fatalf("expected ref2 assignee to be agentB (%d)", agentB.ID)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: 批量指派坐席容量拦截 (Capacity Limit Enforcement)
	// -------------------------------------------------------------
	t.Run("Scenario3_BulkAssignAgent_EnforcesCapacityLimit", func(t *testing.T) {
		agentLimited := domain.User{
			Name:         "Limited Agent",
			Email:        "limited.bulk@example.com",
			Role:         domain.RoleAgent,
			Availability: domain.AvailabilityOnline,
		}
		_ = userRepo.Create(&agentLimited)
		_ = accountRepo.AddMember(accountID, agentLimited.ID, domain.RoleAgent)
		_ = inboxRepo.AddMember(inbox.ID, agentLimited.ID)

		// Set capacity policy to limit of 1
		capPolicy := domain.CapacityPolicy{
			AccountID:         accountID,
			UserID:            agentLimited.ID,
			ConversationLimit: 1,
		}
		_ = db.Create(&capPolicy).Error

		// Create 2 open conversations
		cx1 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: 41, Status: domain.ConversationStatusOpen}
		cx2 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: 42, Status: domain.ConversationStatusOpen}
		_ = convRepo.Create(&cx1)
		_ = convRepo.Create(&cx2)

		// Attempt to bulk assign 2 conversations to agentLimited (limit is 1)
		bulkPayload := map[string]any{
			"type": "assign_agent",
			"ids":  []uint{cx1.ID, cx2.ID},
			"fields": map[string]any{
				"assignee_id": agentLimited.ID,
			},
		}
		bPayload, _ := json.Marshal(bulkPayload)
		reqBulk := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bPayload))
		reqBulk.Header.Set("Authorization", "Bearer "+token)
		reqBulk.Header.Set("Content-Type", "application/json")
		wBulk := httptest.NewRecorder()
		r.ServeHTTP(wBulk, reqBulk)

		// Must be rejected with 400 Bad Request
		if wBulk.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request due to capacity limit, got %d: %s", wBulk.Code, wBulk.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: 批量打标签与分配团队 (Labels & Teams)
	// -------------------------------------------------------------
	t.Run("Scenario4_BulkAddLabelsAndAssignTeam", func(t *testing.T) {
		// 1. Assign Team
		team := domain.Team{
			AccountID: accountID,
			Name:      "Tier 2 Escalations",
		}
		_ = db.Create(&team).Error

		cz1 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: 51, Status: domain.ConversationStatusOpen}
		cz2 := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: 52, Status: domain.ConversationStatusOpen}
		_ = convRepo.Create(&cz1)
		_ = convRepo.Create(&cz2)

		teamPayload := map[string]any{
			"type": "assign_team",
			"ids":  []uint{cz1.ID, cz2.ID},
			"fields": map[string]any{
				"team_id": team.ID,
			},
		}
		bTeam, _ := json.Marshal(teamPayload)
		reqTeam := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bTeam))
		reqTeam.Header.Set("Authorization", "Bearer "+token)
		reqTeam.Header.Set("Content-Type", "application/json")
		wTeam := httptest.NewRecorder()
		r.ServeHTTP(wTeam, reqTeam)

		if wTeam.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for assign_team, got %d: %s", wTeam.Code, wTeam.Body.String())
		}

		refZ1, _ := convRepo.FindByID(accountID, cz1.ID)
		if refZ1.TeamID == nil || *refZ1.TeamID != team.ID {
			t.Fatalf("expected refZ1 TeamID to be %d", team.ID)
		}

		// 2. Add Labels
		labelPayload := map[string]any{
			"type": "add_labels",
			"ids":  []uint{cz1.ID, cz2.ID},
			"labels": map[string]any{
				"add": []string{"VIP_SUPPORT", "BILLING_ISSUE"},
			},
		}
		bLabel, _ := json.Marshal(labelPayload)
		reqLabel := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bLabel))
		reqLabel.Header.Set("Authorization", "Bearer "+token)
		reqLabel.Header.Set("Content-Type", "application/json")
		wLabel := httptest.NewRecorder()
		r.ServeHTTP(wLabel, reqLabel)

		if wLabel.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for add_labels, got %d: %s", wLabel.Code, wLabel.Body.String())
		}

		var countLabel int64
		db.Table("conversation_labels").Where("conversation_id = ?", cz1.ID).Count(&countLabel)
		if countLabel != 2 {
			t.Fatalf("expected 2 labels for cz1, got %d", countLabel)
		}
	})
}
