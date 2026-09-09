package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAutomationRemoveLabelTestDB(t *testing.T) (*gorm.DB, *domain.Account, *domain.Inbox, *domain.Contact, *domain.User) {
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
		&domain.Macro{},
	); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	account := domain.Account{Name: "Remove Label Account"}
	_ = db.Create(&account).Error

	inbox := domain.Inbox{AccountID: account.ID, Name: "Main Inbox", ChannelType: "Channel::WebWidget"}
	_ = db.Create(&inbox).Error

	contact := domain.Contact{AccountID: account.ID, Name: "Label Contact", Email: "label_test@example.com"}
	_ = db.Create(&contact).Error

	user := domain.User{Email: "admin@example.com", Name: "Admin User", Role: domain.RoleAdministrator}
	_ = db.Create(&user).Error

	return db, &account, &inbox, &contact, &user
}

func TestAutomation_RemoveLabel_Action(t *testing.T) {
	db, account, inbox, contact, user := setupAutomationRemoveLabelTestDB(t)

	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	labelRepo := repository.NewLabelRepository(db)
	macroRepo := repository.NewMacroRepository(db)
	autoService := service.NewAutomationService(db, convRepo, msgRepo)

	// Create test labels
	lblVip := domain.Label{AccountID: account.ID, Title: "vip"}
	lblSupport := domain.Label{AccountID: account.ID, Title: "support"}
	lblUrgent := domain.Label{AccountID: account.ID, Title: "urgent"}
	lblBilling := domain.Label{AccountID: account.ID, Title: "billing"}
	_ = db.Create(&lblVip).Error
	_ = db.Create(&lblSupport).Error
	_ = db.Create(&lblUrgent).Error
	_ = db.Create(&lblBilling).Error

	clearRules := func() {
		_ = db.Where("account_id = ?", account.ID).Delete(&domain.AutomationRule{}).Error
	}

	// -------------------------------------------------------------
	// Scenario 1: Chatwoot array format action_params: ["vip"]
	// Verify "vip" is removed, and "support" remains
	// -------------------------------------------------------------
	t.Run("Scenario1_RemoveLabel_ArrayParam_SingleLabel", func(t *testing.T) {
		clearRules()

		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "remove_label",
				"action_params": []string{"vip"},
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Remove VIP Tag",
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
		_ = labelRepo.AttachToConversation(conv.ID, lblVip.ID)
		_ = labelRepo.AttachToConversation(conv.ID, lblSupport.ID)

		// Before trigger: 2 labels
		labelsBefore, _ := labelRepo.GetConversationLabels(conv.ID)
		if len(labelsBefore) != 2 {
			t.Fatalf("expected 2 labels before, got %d", len(labelsBefore))
		}

		// Trigger automation
		autoService.HandleConversationUpdated(&conv)

		// After trigger: only "support" should remain
		labelsAfter, _ := labelRepo.GetConversationLabels(conv.ID)
		if len(labelsAfter) != 1 {
			t.Fatalf("expected 1 label after remove_label, got %d: %+v", len(labelsAfter), labelsAfter)
		}
		if labelsAfter[0].Title != "support" {
			t.Fatalf("expected remaining label 'support', got '%s'", labelsAfter[0].Title)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Multiple labels in action_params: ["vip", "urgent"]
	// -------------------------------------------------------------
	t.Run("Scenario2_RemoveLabel_MultipleLabels", func(t *testing.T) {
		clearRules()

		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name":   "remove_label",
				"action_params": []string{"vip", "urgent"},
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Remove Multiple Tags",
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
		_ = labelRepo.AttachToConversation(conv.ID, lblVip.ID)
		_ = labelRepo.AttachToConversation(conv.ID, lblUrgent.ID)
		_ = labelRepo.AttachToConversation(conv.ID, lblBilling.ID)

		msg := domain.Message{
			AccountID:      account.ID,
			ConversationID: conv.ID,
			Content:        "Resolved customer issue",
			MessageType:    domain.MessageTypeIncoming,
		}
		_ = msgRepo.Create(&msg)

		autoService.HandleMessageCreated(&conv, &msg)

		labelsAfter, _ := labelRepo.GetConversationLabels(conv.ID)
		if len(labelsAfter) != 1 {
			t.Fatalf("expected 1 label remaining, got %d: %+v", len(labelsAfter), labelsAfter)
		}
		if labelsAfter[0].Title != "billing" {
			t.Fatalf("expected remaining label 'billing', got '%s'", labelsAfter[0].Title)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Object format action_params: {"labels": ["support"]}
	// -------------------------------------------------------------
	t.Run("Scenario3_RemoveLabel_ObjectParam", func(t *testing.T) {
		clearRules()

		actionsJSON, _ := json.Marshal([]map[string]any{
			{
				"action_name": "remove_label",
				"action_params": map[string]any{
					"labels": []string{"support"},
				},
			},
		})

		rule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Remove Support Via Map",
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
		_ = labelRepo.AttachToConversation(conv.ID, lblSupport.ID)

		autoService.HandleConversationUpdated(&conv)

		labelsAfter, _ := labelRepo.GetConversationLabels(conv.ID)
		if len(labelsAfter) != 0 {
			t.Fatalf("expected 0 labels remaining, got %d", len(labelsAfter))
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: Macro execution with remove_label
	// -------------------------------------------------------------
	t.Run("Scenario4_Macro_RemoveLabel", func(t *testing.T) {
		macroActions, _ := json.Marshal([]repository.MacroAction{
			{
				ActionName:   "remove_label",
				ActionParams: []string{"vip", "support"},
			},
		})

		macro := domain.Macro{
			AccountID: account.ID,
			Name:      "Clear VIP & Support Macro",
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
		_ = labelRepo.AttachToConversation(conv.ID, lblVip.ID)
		_ = labelRepo.AttachToConversation(conv.ID, lblSupport.ID)
		_ = labelRepo.AttachToConversation(conv.ID, lblBilling.ID)

		res, err := macroRepo.Execute(context.Background(), account.ID, macro.ID, []uint{conv.ID})
		if err != nil {
			t.Fatalf("macro execute failed: %v", err)
		}
		if res[conv.ID] != "success" {
			t.Fatalf("expected macro success, got %v", res[conv.ID])
		}

		labelsAfter, _ := labelRepo.GetConversationLabels(conv.ID)
		if len(labelsAfter) != 1 {
			t.Fatalf("expected 1 label after macro remove_label, got %d", len(labelsAfter))
		}
		if labelsAfter[0].Title != "billing" {
			t.Fatalf("expected remaining label 'billing', got '%s'", labelsAfter[0].Title)
		}
	})

	// -------------------------------------------------------------
	// Scenario 5: Bulk Action remove_labels
	// -------------------------------------------------------------
	t.Run("Scenario5_BulkAction_RemoveLabels", func(t *testing.T) {
		advHandler := handler.NewAdvancedHandler(db, nil, nil, nil, nil, nil)

		c1 := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		c2 := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		_ = convRepo.Create(&c1)
		_ = convRepo.Create(&c2)

		_ = labelRepo.AttachToConversation(c1.ID, lblVip.ID)
		_ = labelRepo.AttachToConversation(c1.ID, lblBilling.ID)
		_ = labelRepo.AttachToConversation(c2.ID, lblVip.ID)

		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.POST("/api/v1/accounts/:account_id/bulk_actions", func(c *gin.Context) {
			c.Set("user_id", user.ID)
			c.Set("user_role", domain.RoleAdministrator)
			advHandler.BulkActions(c)
		})

		payload := map[string]any{
			"type": "remove_labels",
			"ids":  []uint{c1.ID, c2.ID},
			"labels": map[string]any{
				"remove": []string{"vip"},
			},
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/bulk_actions", account.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		l1, _ := labelRepo.GetConversationLabels(c1.ID)
		if len(l1) != 1 || l1[0].Title != "billing" {
			t.Fatalf("expected c1 to have only 'billing', got %+v", l1)
		}

		l2, _ := labelRepo.GetConversationLabels(c2.ID)
		if len(l2) != 0 {
			t.Fatalf("expected c2 to have 0 labels, got %+v", l2)
		}
	})
}
