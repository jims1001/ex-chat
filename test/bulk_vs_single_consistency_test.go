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

func TestBulkVsSingleConsistency(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_bulk_vs_single_consistency_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]string{
		"name":         "Consistency Admin",
		"email":        "consistency_admin@example.com",
		"password":     "Secret123!",
		"account_name": "Consistency Corp",
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
	json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	convRepo := repository.NewConversationRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	notifRepo := repository.NewNotificationRepository(db)

	// Create Target Agent
	targetAgent := domain.User{
		Name:         "Target Agent",
		Email:        "target.agent@consistency.com",
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&targetAgent)
	_ = accountRepo.AddMember(accountID, targetAgent.ID, domain.RoleAgent)

	// Create Inbox
	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "Consistency Inbox",
		WebsiteToken: "consistency_inbox_token",
	}
	_ = inboxRepo.Create(&inbox)
	_ = inboxRepo.AddMember(inbox.ID, targetAgent.ID)

	contact := &domain.Contact{AccountID: accountID, Name: "Customer Test", Email: "customer@test.com"}
	db.Create(contact)

	// =========================================================================
	// Scenario 1: Status Update Consistency (Single ToggleStatus vs Bulk update_status)
	// Both must execute the same Automation Rules
	// =========================================================================
	t.Run("Scenario 1: Status Update Consistency (Automation Triggering)", func(t *testing.T) {
		// Automation Rule: When conversation is updated to 'resolved', attach label 'AUTO_RESOLVED'
		autoLabel := domain.Label{AccountID: accountID, Title: "AUTO_RESOLVED"}
		db.Create(&autoLabel)

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
		db.Create(&autoRule)

		// Create Conv A (for single toggle_status) and Conv B (for bulk update_status)
		convA := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		convB := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		_ = convRepo.Create(&convA)
		_ = convRepo.Create(&convB)

		// 1. Single ToggleStatus on Conv A
		togglePayload := map[string]any{"status": "resolved"}
		bA, _ := json.Marshal(togglePayload)
		reqA := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/toggle_status", accountID, convA.ID), bytes.NewReader(bA))
		reqA.Header.Set("Authorization", "Bearer "+token)
		reqA.Header.Set("Content-Type", "application/json")
		wA := httptest.NewRecorder()
		r.ServeHTTP(wA, reqA)
		if wA.Code != http.StatusOK {
			t.Fatalf("single toggle_status failed: code=%d body=%s", wA.Code, wA.Body.String())
		}

		// 2. Bulk update_status on Conv B
		bulkPayload := map[string]any{
			"type": "update_status",
			"ids":  []uint{convB.ID},
			"fields": map[string]any{
				"status": "resolved",
			},
		}
		bB, _ := json.Marshal(bulkPayload)
		reqB := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bB))
		reqB.Header.Set("Authorization", "Bearer "+token)
		reqB.Header.Set("Content-Type", "application/json")
		wB := httptest.NewRecorder()
		r.ServeHTTP(wB, reqB)
		if wB.Code != http.StatusOK {
			t.Fatalf("bulk update_status failed: code=%d body=%s", wB.Code, wB.Body.String())
		}

		// Verify BOTH Conv A and Conv B are resolved AND BOTH triggered the automation rule to attach AUTO_RESOLVED
		refA, _ := convRepo.FindByID(accountID, convA.ID)
		refB, _ := convRepo.FindByID(accountID, convB.ID)

		if refA.Status != domain.ConversationStatusResolved {
			t.Fatalf("expected Conv A to be resolved, got %s", refA.Status)
		}
		if refB.Status != domain.ConversationStatusResolved {
			t.Fatalf("expected Conv B to be resolved, got %s", refB.Status)
		}

		var countA, countB int64
		db.Table("conversation_labels").Where("conversation_id = ? AND label_id = ?", convA.ID, autoLabel.ID).Count(&countA)
		db.Table("conversation_labels").Where("conversation_id = ? AND label_id = ?", convB.ID, autoLabel.ID).Count(&countB)

		if countA != 1 {
			t.Fatalf("expected Conv A (single) to trigger automation and have AUTO_RESOLVED label, but count was %d", countA)
		}
		if countB != 1 {
			t.Fatalf("expected Conv B (bulk) to trigger automation and have AUTO_RESOLVED label, but count was %d", countB)
		}
	})

	// =========================================================================
	// Scenario 2: Agent Assignment Consistency (In-App Notifications & Automation)
	// Both must generate In-App Notifications and trigger Automation Rules
	// =========================================================================
	t.Run("Scenario 2: Agent Assignment Consistency (In-App Notifications & Automation)", func(t *testing.T) {
		// Automation Rule: When conversation is assigned to targetAgent, set priority to 'urgent'
		ruleConditions, _ := json.Marshal([]map[string]any{
			{
				"attribute_key":   "assignee_id",
				"filter_operator": "equal_to",
				"values":          []string{fmt.Sprintf("%d", targetAgent.ID)},
				"query_operator":  "AND",
			},
		})
		ruleActions, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "change_priority",
				"action_params": []string{"urgent"},
			},
		})
		autoRule := domain.AutomationRule{
			AccountID:  accountID,
			Name:       "Auto Urgent On Target Agent Assigned",
			EventName:  "conversation_updated",
			Conditions: string(ruleConditions),
			Actions:    string(ruleActions),
			Active:     true,
		}
		db.Create(&autoRule)

		convC := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen, Priority: "low"}
		convD := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen, Priority: "low"}
		_ = convRepo.Create(&convC)
		_ = convRepo.Create(&convD)

		// 1. Single Assign Conv C
		assignPayload := map[string]any{"assignee_id": targetAgent.ID}
		bC, _ := json.Marshal(assignPayload)
		reqC := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", accountID, convC.ID), bytes.NewReader(bC))
		reqC.Header.Set("Authorization", "Bearer "+token)
		reqC.Header.Set("Content-Type", "application/json")
		wC := httptest.NewRecorder()
		r.ServeHTTP(wC, reqC)
		if wC.Code != http.StatusOK {
			t.Fatalf("single assignment failed: code=%d body=%s", wC.Code, wC.Body.String())
		}

		// 2. Bulk Assign Conv D
		bulkAssignPayload := map[string]any{
			"type": "assign_agent",
			"ids":  []uint{convD.ID},
			"fields": map[string]any{
				"assignee_id": targetAgent.ID,
			},
		}
		bD, _ := json.Marshal(bulkAssignPayload)
		reqD := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bD))
		reqD.Header.Set("Authorization", "Bearer "+token)
		reqD.Header.Set("Content-Type", "application/json")
		wD := httptest.NewRecorder()
		r.ServeHTTP(wD, reqD)
		if wD.Code != http.StatusOK {
			t.Fatalf("bulk assignment failed: code=%d body=%s", wD.Code, wD.Body.String())
		}

		// Verify In-App Notifications were created for BOTH Conv C (single) and Conv D (bulk)
		notifs, err := notifRepo.List(nil, accountID, targetAgent.ID)
		if err != nil {
			t.Fatalf("failed to list notifications: %v", err)
		}

		foundConvC := false
		foundConvD := false
		for _, n := range notifs {
			if n.NotificationType == domain.NotificationTypeConversationAssignment {
				if n.PrimaryActorID == convC.ID {
					foundConvC = true
				}
				if n.PrimaryActorID == convD.ID {
					foundConvD = true
				}
			}
		}

		if !foundConvC {
			t.Fatalf("expected in-app notification for Conv C (single assignment), but not found")
		}
		if !foundConvD {
			t.Fatalf("expected in-app notification for Conv D (bulk assignment), but not found")
		}

		// Verify Automation rule was triggered for BOTH Conv C and Conv D, changing priority to 'urgent'
		refC, _ := convRepo.FindByID(accountID, convC.ID)
		refD, _ := convRepo.FindByID(accountID, convD.ID)

		if refC.Priority != "urgent" {
			t.Fatalf("expected Conv C (single) to have priority updated to 'urgent' by automation rule, got %s", refC.Priority)
		}
		if refD.Priority != "urgent" {
			t.Fatalf("expected Conv D (bulk) to have priority updated to 'urgent' by automation rule, got %s", refD.Priority)
		}
	})

	// =========================================================================
	// Scenario 3: Label Operation Consistency (Automation & Lifecycle)
	// Both single label attach and bulk label attach must trigger Automation Rules
	// =========================================================================
	t.Run("Scenario 3: Label Operation Consistency (Automation & Lifecycle)", func(t *testing.T) {
		// Team to assign via automation
		vipTeam := domain.Team{AccountID: accountID, Name: "VIP Support Team"}
		db.Create(&vipTeam)

		vipLabel := domain.Label{AccountID: accountID, Title: "VIP_CLIENT"}
		db.Create(&vipLabel)

		// Automation Rule: When conversation is updated and has label 'VIP_CLIENT', assign team VIP Support Team
		ruleConditions, _ := json.Marshal([]map[string]any{
			{
				"attribute_key":   "labels",
				"filter_operator": "equal_to",
				"values":          []string{"VIP_CLIENT"},
				"query_operator":  "AND",
			},
		})
		ruleActions, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "assign_team",
				"action_params": []string{fmt.Sprintf("%d", vipTeam.ID)},
			},
		})
		autoRule := domain.AutomationRule{
			AccountID:  accountID,
			Name:       "Assign VIP Team On VIP Label",
			EventName:  "conversation_updated",
			Conditions: string(ruleConditions),
			Actions:    string(ruleActions),
			Active:     true,
		}
		db.Create(&autoRule)

		convE := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		convF := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		_ = convRepo.Create(&convE)
		_ = convRepo.Create(&convF)

		// 1. Single attach label to Conv E
		attachPayload := map[string]any{"label_ids": []uint{vipLabel.ID}}
		bE, _ := json.Marshal(attachPayload)
		reqE := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/labels", accountID, convE.ID), bytes.NewReader(bE))
		reqE.Header.Set("Authorization", "Bearer "+token)
		reqE.Header.Set("Content-Type", "application/json")
		wE := httptest.NewRecorder()
		r.ServeHTTP(wE, reqE)
		if wE.Code != http.StatusOK {
			t.Fatalf("single attach labels failed: code=%d body=%s", wE.Code, wE.Body.String())
		}

		// 2. Bulk add labels to Conv F
		bulkLabelPayload := map[string]any{
			"type": "add_labels",
			"ids":  []uint{convF.ID},
			"labels": map[string]any{
				"add": []string{"VIP_CLIENT"},
			},
		}
		bF, _ := json.Marshal(bulkLabelPayload)
		reqF := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", accountID), bytes.NewReader(bF))
		reqF.Header.Set("Authorization", "Bearer "+token)
		reqF.Header.Set("Content-Type", "application/json")
		wF := httptest.NewRecorder()
		r.ServeHTTP(wF, reqF)
		if wF.Code != http.StatusOK {
			t.Fatalf("bulk add labels failed: code=%d body=%s", wF.Code, wF.Body.String())
		}

		// Verify BOTH Conv E (single) and Conv F (bulk) triggered the automation rule and got assigned to vipTeam!
		refE, _ := convRepo.FindByID(accountID, convE.ID)
		refF, _ := convRepo.FindByID(accountID, convF.ID)

		if refE.TeamID == nil || *refE.TeamID != vipTeam.ID {
			t.Fatalf("expected Conv E (single label) to be assigned to VIP Team (%d) by automation rule, got %v", vipTeam.ID, refE.TeamID)
		}
		if refF.TeamID == nil || *refF.TeamID != vipTeam.ID {
			t.Fatalf("expected Conv F (bulk label) to be assigned to VIP Team (%d) by automation rule, got %v", vipTeam.ID, refF.TeamID)
		}

		// 3. Test Single Detach label
		reqDetach := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/labels/%d", accountID, convE.ID, vipLabel.ID), nil)
		reqDetach.Header.Set("Authorization", "Bearer "+token)
		wDetach := httptest.NewRecorder()
		r.ServeHTTP(wDetach, reqDetach)
		if wDetach.Code != http.StatusOK {
			t.Fatalf("single detach label failed: code=%d body=%s", wDetach.Code, wDetach.Body.String())
		}

		var countAfterDetach int64
		db.Table("conversation_labels").Where("conversation_id = ? AND label_id = ?", convE.ID, vipLabel.ID).Count(&countAfterDetach)
		if countAfterDetach != 0 {
			t.Fatalf("expected label to be detached from Conv E, but count was %d", countAfterDetach)
		}
	})
}
