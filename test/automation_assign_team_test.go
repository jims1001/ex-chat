package test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAutomationTeamTestDB(t *testing.T) (*gorm.DB, *domain.Account, *domain.Inbox, *domain.User, *domain.Team, *domain.Team) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to connect database: %v", err)
	}

	if err := db.AutoMigrate(
		&domain.Account{},
		&domain.User{},
		&domain.Inbox{},
		&domain.Contact{},
		&domain.Conversation{},
		&domain.Message{},
		&domain.Label{},
		&domain.ConversationLabel{},
		&domain.AutomationRule{},
		&domain.Team{},
		&domain.TeamMember{},
		&domain.Macro{},
	); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	account := domain.Account{Name: "Team Automation Account"}
	_ = db.Create(&account).Error

	inbox := domain.Inbox{AccountID: account.ID, Name: "Main Inbox", ChannelType: "Channel::WebWidget"}
	_ = db.Create(&inbox).Error

	agent := domain.User{Email: "agent_support@example.com", Name: "Support Agent", Role: domain.RoleAgent}
	_ = db.Create(&agent).Error

	teamAlpha := domain.Team{AccountID: account.ID, Name: "Alpha Team"}
	_ = db.Create(&teamAlpha).Error

	teamBeta := domain.Team{AccountID: account.ID, Name: "Beta Escalations"}
	_ = db.Create(&teamBeta).Error

	return db, &account, &inbox, &agent, &teamAlpha, &teamBeta
}

func TestAutomation_AssignTeam_IndependentFromAssignAgent(t *testing.T) {
	db, account, inbox, agent, teamAlpha, teamBeta := setupAutomationTeamTestDB(t)

	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	autoService := service.NewAutomationService(db, convRepo, msgRepo)
	macroRepo := repository.NewMacroRepository(db)

	contact := domain.Contact{AccountID: account.ID, Name: "Test Contact", Email: "contact@example.com"}
	_ = db.Create(&contact).Error

	clearRules := func() {
		_ = db.Where("account_id = ?", account.ID).Delete(&domain.AutomationRule{}).Error
	}

	// -------------------------------------------------------------
	// Scenario 1: Chatwoot standard array format `actions: [{"action_name": "assign_team", "action_params": [teamID]}]`
	// Verify TeamID is assigned, and AssigneeID is NOT mistakenly assigned!
	// -------------------------------------------------------------
	t.Run("Scenario1_AssignTeam_ArrayParam_DoesNotCorruptAssignee", func(t *testing.T) {
		clearRules()
		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "assign_team",
				"action_params": []uint{teamAlpha.ID},
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Assign Alpha Team On Create",
			EventName:  "conversation_created",
			Conditions: "[]",
			Actions:    string(actionsJSON),
			Active:     true,
		}
		_ = db.Create(&rule).Error

		conv := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		if err := convRepo.Create(&conv); err != nil {
			t.Fatalf("failed to create conversation: %v", err)
		}

		autoService.HandleConversationCreated(&conv)

		refreshed, err := convRepo.FindByID(account.ID, conv.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}

		if refreshed.TeamID == nil || *refreshed.TeamID != teamAlpha.ID {
			t.Fatalf("expected TeamID %d, got %+v", teamAlpha.ID, refreshed.TeamID)
		}
		if refreshed.AssigneeID != nil {
			t.Fatalf("expected AssigneeID to remain nil, but was mistakenly set to %d!", *refreshed.AssigneeID)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Object format `action_params: {"team_id": teamID}`
	// -------------------------------------------------------------
	t.Run("Scenario2_AssignTeam_ObjectParam", func(t *testing.T) {
		clearRules()
		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name": "assign_team",
				"action_params": map[string]any{
					"team_id": teamBeta.ID,
				},
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Assign Beta Team On Update",
			EventName:  "conversation_updated",
			Conditions: "[]",
			Actions:    string(actionsJSON),
			Active:     true,
		}
		_ = db.Create(&rule).Error

		conv := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&conv)

		autoService.HandleConversationUpdated(&conv)

		refreshed, err := convRepo.FindByID(account.ID, conv.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}

		if refreshed.TeamID == nil || *refreshed.TeamID != teamBeta.ID {
			t.Fatalf("expected TeamID %d, got %+v", teamBeta.ID, refreshed.TeamID)
		}
		if refreshed.AssigneeID != nil {
			t.Fatalf("expected AssigneeID to remain nil, got %+v", refreshed.AssigneeID)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Co-existence of both assign_team and assign_agent
	// Verify BOTH team and agent are set correctly without overwriting each other
	// -------------------------------------------------------------
	t.Run("Scenario3_AssignTeamAndAssignAgent_BothApplied", func(t *testing.T) {
		clearRules()
		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "assign_team",
				"action_params": []uint{teamAlpha.ID},
			},
			{
				"action_name":   "assign_agent",
				"action_params": []uint{agent.ID},
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Assign Team and Agent Together",
			EventName:  "message_created",
			Conditions: "[]",
			Actions:    string(actionsJSON),
			Active:     true,
		}
		_ = db.Create(&rule).Error

		conv := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&conv)

		msg := domain.Message{
			AccountID:      account.ID,
			ConversationID: conv.ID,
			Content:        "Hello support team",
			MessageType:    domain.MessageTypeIncoming,
		}
		_ = msgRepo.Create(&msg)

		autoService.HandleMessageCreated(&conv, &msg)

		refreshed, err := convRepo.FindByID(account.ID, conv.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}

		if refreshed.TeamID == nil || *refreshed.TeamID != teamAlpha.ID {
			t.Fatalf("expected TeamID %d, got %+v", teamAlpha.ID, refreshed.TeamID)
		}
		if refreshed.AssigneeID == nil || *refreshed.AssigneeID != agent.ID {
			t.Fatalf("expected AssigneeID %d, got %+v", agent.ID, refreshed.AssigneeID)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: remove_assigned_team and remove_assigned_agent
	// -------------------------------------------------------------
	t.Run("Scenario4_RemoveAssignedTeam", func(t *testing.T) {
		clearRules()
		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name": "remove_assigned_team",
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Remove Team Assignment",
			EventName:  "conversation_updated",
			Conditions: "[]",
			Actions:    string(actionsJSON),
			Active:     true,
		}
		_ = db.Create(&rule).Error

		conv := domain.Conversation{
			AccountID:  account.ID,
			InboxID:    inbox.ID,
			ContactID:  contact.ID,
			TeamID:     &teamAlpha.ID,
			AssigneeID: &agent.ID,
			Status:     domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&conv)

		autoService.HandleConversationUpdated(&conv)

		refreshed, err := convRepo.FindByID(account.ID, conv.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}

		if refreshed.TeamID != nil {
			t.Fatalf("expected TeamID to be nil, got %d", *refreshed.TeamID)
		}
		if refreshed.AssigneeID == nil || *refreshed.AssigneeID != agent.ID {
			t.Fatalf("expected AssigneeID to remain %d, got %+v", agent.ID, refreshed.AssigneeID)
		}
	})

	// -------------------------------------------------------------
	// Scenario 5: Macro execution with assign_team
	// -------------------------------------------------------------
	t.Run("Scenario5_Macro_AssignTeam", func(t *testing.T) {
		clearRules()
		macroActions, _ := json.Marshal([]repository.MacroAction{
			{
				ActionName:   "assign_team",
				ActionParams: []string{strconv.Itoa(int(teamBeta.ID))},
			},
		})

		macro := domain.Macro{
			AccountID: account.ID,
			Name:      "Escalate to Beta Team Macro",
			Actions:   string(macroActions),
		}
		_ = macroRepo.Create(context.Background(), &macro)

		conv := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&conv)

		res, err := macroRepo.Execute(context.Background(), account.ID, macro.ID, []uint{conv.ID})
		if err != nil {
			t.Fatalf("macro execution failed: %v", err)
		}
		if res[conv.ID] != "success" {
			t.Fatalf("expected success, got %v", res[conv.ID])
		}

		refreshed, err := convRepo.FindByID(account.ID, conv.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}
		if refreshed.TeamID == nil || *refreshed.TeamID != teamBeta.ID {
			t.Fatalf("expected TeamID %d, got %+v", teamBeta.ID, refreshed.TeamID)
		}
		if refreshed.AssigneeID != nil {
			t.Fatalf("expected AssigneeID to remain nil after macro assign_team, got %+v", refreshed.AssigneeID)
		}
	})

	// -------------------------------------------------------------
	// Scenario 6: Automation condition matching on team_id
	// -------------------------------------------------------------
	t.Run("Scenario6_ConditionMatching_OnTeamID", func(t *testing.T) {
		clearRules()
		conditionsJSON, _ := json.Marshal([]map[string]any{
			{
				"attribute_key":   "team_id",
				"filter_operator": "equal_to",
				"values":          []any{teamBeta.ID},
			},
		})
		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "change_priority",
				"action_params": []string{"urgent"},
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Beta Team Priority Escalation",
			EventName:  "conversation_updated",
			Conditions: string(conditionsJSON),
			Actions:    string(actionsJSON),
			Active:     true,
		}
		_ = db.Create(&rule).Error

		// Conv 1: has teamBeta -> should match and change priority to urgent
		convMatch := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			TeamID:    &teamBeta.ID,
			Priority:  "low",
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&convMatch)
		autoService.HandleConversationUpdated(&convMatch)

		ref1, _ := convRepo.FindByID(account.ID, convMatch.ID)
		if ref1.Priority != "urgent" {
			t.Fatalf("expected priority 'urgent', got '%s'", ref1.Priority)
		}

		// Conv 2: has teamAlpha -> should NOT match and remain low
		convNoMatch := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			TeamID:    &teamAlpha.ID,
			Priority:  "low",
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&convNoMatch)
		autoService.HandleConversationUpdated(&convNoMatch)

		ref2, _ := convRepo.FindByID(account.ID, convNoMatch.ID)
		if ref2.Priority != "low" {
			t.Fatalf("expected priority 'low', got '%s'", ref2.Priority)
		}
	})
}
