package test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
)

func TestAutomation_LabelExactMatch(t *testing.T) {
	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:automation_label_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_auto_label",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	account := domain.Account{Name: "Automation Label Test Account"}
	_ = db.Create(&account).Error

	inbox := domain.Inbox{
		AccountID: account.ID,
		Name:      "Web Inbox",
	}
	_ = db.Create(&inbox).Error

	contact := domain.Contact{
		AccountID: account.ID,
		Name:      "Bob",
		Email:     "bob@example.com",
	}
	_ = db.Create(&contact).Error

	// Create Labels
	labelVip := domain.Label{
		AccountID: account.ID,
		Title:     "vip",
	}
	_ = db.Create(&labelVip).Error

	labelNotVip := domain.Label{
		AccountID: account.ID,
		Title:     "not-vip",
	}
	_ = db.Create(&labelNotVip).Error

	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	autoService := service.NewAutomationService(db, convRepo, msgRepo)

	// Create Automation Rule:
	// Conditions: labels equal_to "vip"
	// Action: resolve_conversation (close)
	conditionsJSON, _ := json.Marshal([]map[string]any{
		{
			"attribute_key":   "labels",
			"filter_operator": "equal_to",
			"values":          []string{"vip"},
		},
	})
	actionsJSON, _ := json.Marshal([]map[string]any{
		{
			"action_name": "resolve_conversation",
		},
	})

	autoRule := domain.AutomationRule{
		AccountID:  account.ID,
		Name:       "Close VIP Conversations",
		EventName:  "conversation_updated",
		Conditions: string(conditionsJSON),
		Actions:    string(actionsJSON),
		Active:     true,
	}
	_ = db.Create(&autoRule).Error

	// -------------------------------------------------------------
	// Scenario 1: User reported issue:
	// Conversation has label "not-vip".
	// Automation condition: equal_to "vip".
	// MUST NOT match and MUST NOT close conversation!
	// -------------------------------------------------------------
	t.Run("Scenario1_NotVipLabel_MustNotMatch_EqualToVip", func(t *testing.T) {
		convNotVip := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&convNotVip)

		// Associate label "not-vip"
		_ = db.Exec("INSERT INTO conversation_labels (conversation_id, label_id) VALUES (?, ?)", convNotVip.ID, labelNotVip.ID).Error
		convNotVip.Labels = []domain.Label{labelNotVip}

		// Trigger automation rule
		autoService.HandleConversationUpdated(&convNotVip)

		// Check conversation status in DB
		refreshed, err := convRepo.FindByID(account.ID, convNotVip.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}

		if refreshed.Status != domain.ConversationStatusOpen {
			t.Fatalf("FAIL: Conversation with 'not-vip' was closed! Expected status 'open', got '%s'", refreshed.Status)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Conversation has exact label "vip".
	// MUST match and close conversation.
	// -------------------------------------------------------------
	t.Run("Scenario2_VipLabel_Matches_EqualToVip", func(t *testing.T) {
		convVip := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&convVip)

		// Associate label "vip"
		_ = db.Exec("INSERT INTO conversation_labels (conversation_id, label_id) VALUES (?, ?)", convVip.ID, labelVip.ID).Error
		convVip.Labels = []domain.Label{labelVip}

		// Trigger automation rule
		autoService.HandleConversationUpdated(&convVip)

		// Check conversation status in DB
		refreshed, err := convRepo.FindByID(account.ID, convVip.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}

		if refreshed.Status != domain.ConversationStatusResolved {
			t.Fatalf("Expected conversation with 'vip' to be resolved, got '%s'", refreshed.Status)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Rule with "contains" vs "not-vip"
	// "contains vip" SHOULD match "not-vip"
	// -------------------------------------------------------------
	t.Run("Scenario3_ContainsOperator_MatchesSubstring", func(t *testing.T) {
		conditionsContainsJSON, _ := json.Marshal([]map[string]any{
			{
				"attribute_key":   "labels",
				"filter_operator": "contains",
				"values":          []string{"vip"},
			},
		})
		actionsContainsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name": "resolve_conversation",
			},
		})

		ruleContains := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Close Contains VIP",
			EventName:  "conversation_updated",
			Conditions: string(conditionsContainsJSON),
			Actions:    string(actionsContainsJSON),
			Active:     true,
		}
		_ = db.Create(&ruleContains).Error

		convContains := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&convContains)
		_ = db.Exec("INSERT INTO conversation_labels (conversation_id, label_id) VALUES (?, ?)", convContains.ID, labelNotVip.ID).Error
		convContains.Labels = []domain.Label{labelNotVip}

		autoService.HandleConversationUpdated(&convContains)

		refreshed, _ := convRepo.FindByID(account.ID, convContains.ID)
		if refreshed.Status != domain.ConversationStatusResolved {
			t.Fatalf("Expected 'contains' operator to match 'not-vip', but status is '%s'", refreshed.Status)
		}
	})
}
