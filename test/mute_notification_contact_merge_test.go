package test

import (
	"bytes"
	"context"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestConversationMuteAndNotificationSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "mute_notification_test_secret_32b!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Agent Tester",
		"email":        "agent_tester@example.com",
		"password":     "Password123!",
		"account_name": "Test Mute Org",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(signUpBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("agent sign up failed: code=%d, body=%s", w.Code, w.Body.String())
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

	// 2. Test Notification Settings validation & update
	t.Run("Notification Settings Validation & Update", func(t *testing.T) {
		// A. Invalid flag rejection
		badFlagsBody, _ := json.Marshal(map[string]any{
			"selected_push_flags": []string{"invalid_flag_xyz"},
		})
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/notification_settings", accountID), bytes.NewReader(badFlagsBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid notification flag, got: %d (%s)", w.Code, w.Body.String())
		}

		// B. Invalid quiet hours format rejection
		badTimeBody, _ := json.Marshal(map[string]any{
			"quiet_hours_start": "25:99",
		})
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/notification_settings", accountID), bytes.NewReader(badTimeBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid quiet hours time, got: %d (%s)", w.Code, w.Body.String())
		}

		// C. Valid settings update
		validBody, _ := json.Marshal(map[string]any{
			"selected_push_flags": []string{"conversation_assignment", "conversation_creation"},
			"muted":               false,
			"quiet_hours_enabled": true,
			"quiet_hours_start":   "22:00",
			"quiet_hours_end":     "08:00",
		})
		req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/notification_settings", accountID), bytes.NewReader(validBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for valid settings update, got: %d (%s)", w.Code, w.Body.String())
		}

		// D. Query settings
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/notification_settings", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for get settings, got: %d (%s)", w.Code, w.Body.String())
		}
	})

	// 3. Test PushService Dispatch Filtering (Muted, Quiet Hours, Flag filter)
	t.Run("PushService Dispatch Filtering", func(t *testing.T) {
		deviceRepo := repository.NewDeviceRepository(db)
		notifRepo := repository.NewNotificationRepository(db)
		pushService := service.NewPushService(db, deviceRepo)
		pushService.SetNotificationRepo(notifRepo)

		// Create a mock device subscription
		_ = deviceRepo.UpsertSubscription(context.Background(), &domain.NotificationSubscription{
			AccountID:        accountID,
			UserID:           userID,
			PushToken:        "mock_fcm_token_123",
			SubscriptionType: "fcm",
		})

		// 3.1 Unselected Flag should be skipped
		sent, err := pushService.Dispatch(context.Background(), userID, accountID, service.PushPayload{
			Title:            "System Alert",
			Body:             "Alert body",
			AccountID:        accountID,
			NotificationType: domain.NotificationTypeSystemAlert, // Not in selected push flags
		})
		if err != nil {
			t.Fatalf("unexpected error on dispatch: %v", err)
		}
		if sent != 0 {
			t.Fatalf("expected 0 push sent for unselected flag, got %d", sent)
		}

		// 3.2 Global Muted should be skipped
		mutedSetting := &domain.NotificationSetting{
			AccountID:          accountID,
			UserID:             userID,
			Muted:              true,
			SelectedPushFlags:  `["conversation_assignment"]`,
			SelectedEmailFlags: `[]`,
			SelectedInAppFlags: `[]`,
		}
		_ = notifRepo.UpsertNotificationSetting(context.Background(), mutedSetting)

		sent, err = pushService.Dispatch(context.Background(), userID, accountID, service.PushPayload{
			Title:            "Assigned",
			Body:             "Assigned body",
			AccountID:        accountID,
			NotificationType: domain.NotificationTypeConversationAssignment,
		})
		if err != nil {
			t.Fatalf("unexpected error on dispatch: %v", err)
		}
		if sent != 0 {
			t.Fatalf("expected 0 push sent when user muted=true, got %d", sent)
		}
	})

	// 4. Test Conversation Mute/Unmute Chatwoot Semantics & Notification Suppression
	t.Run("Conversation Mute/Unmute Semantics and Suppression", func(t *testing.T) {
		// A. Create an Inbox
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "Web Support",
			ChannelType:  domain.ChannelWebWidget,
			WebsiteToken: "web_token_mute_test_123",
		}
		if err := db.Create(&inbox).Error; err != nil {
			t.Fatalf("failed to create inbox: %v", err)
		}

		// B. Create a Contact
		contact := domain.Contact{
			AccountID: accountID,
			Name:      "Customer John",
			Email:     "john@customer.com",
			Blocked:   false,
		}
		if err := db.Create(&contact).Error; err != nil {
			t.Fatalf("failed to create contact: %v", err)
		}
		_ = db.Create(&domain.ContactInbox{
			ContactID: contact.ID,
			InboxID:   inbox.ID,
			SourceID:  "src_john_123",
		})

		// C. Create a Conversation
		conv := domain.Conversation{
			AccountID:  accountID,
			InboxID:    inbox.ID,
			ContactID:  contact.ID,
			AssigneeID: &userID,
			Status:     domain.ConversationStatusOpen,
			Muted:      false,
		}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatalf("failed to create conversation: %v", err)
		}

		// D. Mute Conversation via API
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/mute", accountID, conv.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on mute, got %d (%s)", w.Code, w.Body.String())
		}

		// Verify conversation is muted and resolved
		var refreshedConv domain.Conversation
		db.First(&refreshedConv, conv.ID)
		if !refreshedConv.Muted {
			t.Fatalf("expected conversation muted=true")
		}
		if refreshedConv.Status != domain.ConversationStatusResolved {
			t.Fatalf("expected conversation status resolved, got %s", refreshedConv.Status)
		}

		// Verify contact is blocked
		var refreshedContact domain.Contact
		db.First(&refreshedContact, contact.ID)
		if !refreshedContact.Blocked {
			t.Fatalf("expected contact blocked=true after conversation mute")
		}

		// Verify activity message was created
		var actMsg domain.Message
		err = db.Where("conversation_id = ? AND message_type = ?", conv.ID, domain.MessageTypeActivity).First(&actMsg).Error
		if err != nil {
			t.Fatalf("expected activity message to be created on mute, got error: %v", err)
		}
		if actMsg.Content != "Agent Tester has muted the conversation" {
			t.Fatalf("unexpected activity message content: %s", actMsg.Content)
		}

		// E. Customer posts a message while conversation is muted & contact is blocked
		// Through widget endpoint: /api/v1/widget/messages
		widgetMsgBody, _ := json.Marshal(map[string]any{
			"content":         "Hello while muted!",
			"conversation_id": conv.ID,
			"source_id":       "src_john_123",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/widget/messages", bytes.NewReader(widgetMsgBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Token", inbox.WebsiteToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("expected 201/200 on widget message, got %d (%s)", w.Code, w.Body.String())
		}

		// But conversation MUST remain resolved because it is muted!
		db.First(&refreshedConv, conv.ID)
		if refreshedConv.Status != domain.ConversationStatusResolved {
			t.Fatalf("conversation should remain resolved when muted, but status is %s", refreshedConv.Status)
		}

		// F. Unmute Conversation via API
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/unmute", accountID, conv.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on unmute, got %d (%s)", w.Code, w.Body.String())
		}

		// Verify unmuted and contact unblocked
		db.First(&refreshedConv, conv.ID)
		if refreshedConv.Muted {
			t.Fatalf("expected conversation muted=false after unmute")
		}
		db.First(&refreshedContact, contact.ID)
		if refreshedContact.Blocked {
			t.Fatalf("expected contact blocked=false after unmute")
		}

		// Verify unmute activity message
		var unmutedActMsg domain.Message
		err = db.Where("conversation_id = ? AND message_type = ? AND content LIKE ?", conv.ID, domain.MessageTypeActivity, "%has unmuted%").First(&unmutedActMsg).Error
		if err != nil {
			t.Fatalf("expected unmute activity message, got err: %v", err)
		}
	})

	// 5. Test Contact Deep Merge and All Foreign Key Migrations
	t.Run("Contact Deep Merge Full Migration", func(t *testing.T) {
		// Setup Base Contact
		baseContact := domain.Contact{
			AccountID:        accountID,
			Name:             "Base Customer",
			Email:            "base@example.com",
			CustomAttributes: `{"role":"admin","tags":["vip"]}`,
		}
		_ = db.Create(&baseContact)

		// Setup Mergee Contact with additional attributes
		mergeeContact := domain.Contact{
			AccountID:        accountID,
			PhoneNumber:      "+18001234567",
			Identifier:       "MERGEE_EXTERNAL_ID",
			AvatarURL:        "https://example.com/avatar.png",
			CustomAttributes: `{"role":"user","tier":"platinum"}`,
		}
		_ = db.Create(&mergeeContact)

		// Setup associated objects for Mergee
		inbox := domain.Inbox{
			AccountID:    accountID,
			Name:         "Merge Inbox",
			ChannelType:  domain.ChannelWebWidget,
			WebsiteToken: "merge_token_xyz_123",
		}
		_ = db.Create(&inbox)

		mergeeContactID := mergeeContact.ID

		_ = db.Create(&domain.ContactInbox{ContactID: mergeeContact.ID, InboxID: inbox.ID, SourceID: "source_mergee"})
		_ = db.Create(&domain.Conversation{AccountID: accountID, ContactID: mergeeContact.ID, InboxID: inbox.ID, Status: domain.ConversationStatusOpen})
		_ = db.Create(&domain.ContactNote{AccountID: accountID, ContactID: mergeeContact.ID, Content: "Important customer note"})
		_ = db.Create(&domain.Ticket{AccountID: accountID, ContactID: &mergeeContactID, TicketNumber: "TCK-001", Title: "Ticket 1"})
		_ = db.Create(&domain.TicketComment{AccountID: accountID, ContactID: &mergeeContactID, Content: "Comment 1"})
		_ = db.Create(&domain.Order{AccountID: accountID, ContactID: &mergeeContactID, OrderID: "ORD-001"})
		_ = db.Create(&domain.CampaignDelivery{AccountID: accountID, ContactID: mergeeContact.ID, Status: "delivered"})
		_ = db.Create(&domain.Call{AccountID: accountID, ContactID: mergeeContact.ID, InboxID: inbox.ID, Status: "ended"})
		_ = db.Create(&domain.WidgetEvent{AccountID: accountID, ContactID: &mergeeContactID, InboxID: inbox.ID, Name: "pageview"})

		// Execute Merge Contact API
		mergePayload, _ := json.Marshal(map[string]any{
			"base_contact_id":   baseContact.ID,
			"mergee_contact_id": mergeeContact.ID,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts/merge", accountID), bytes.NewReader(mergePayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on merge contacts, got %d (%s)", w.Code, w.Body.String())
		}

		// Verify API response returned latest merged base contact
		var mergeResp struct {
			Data domain.Contact `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &mergeResp)
		if mergeResp.Data.ID != baseContact.ID {
			t.Fatalf("expected returned contact to be baseContact ID %d, got %d", baseContact.ID, mergeResp.Data.ID)
		}
		if mergeResp.Data.PhoneNumber != "+18001234567" {
			t.Fatalf("expected merged phone number, got %s", mergeResp.Data.PhoneNumber)
		}
		if mergeResp.Data.Identifier != "MERGEE_EXTERNAL_ID" {
			t.Fatalf("expected merged identifier, got %s", mergeResp.Data.Identifier)
		}
		if mergeResp.Data.AvatarURL != "https://example.com/avatar.png" {
			t.Fatalf("expected merged avatar, got %s", mergeResp.Data.AvatarURL)
		}

		// Verify Custom Attributes merged: base 'role:admin' kept, mergee 'tier:platinum' added
		var attrs map[string]any
		_ = json.Unmarshal([]byte(mergeResp.Data.CustomAttributes), &attrs)
		if attrs["role"] != "admin" {
			t.Fatalf("expected base role=admin preserved, got %v", attrs["role"])
		}
		if attrs["tier"] != "platinum" {
			t.Fatalf("expected tier=platinum merged, got %v", attrs["tier"])
		}

		// Verify Mergee contact is deleted
		var count int64
		db.Model(&domain.Contact{}).Where("id = ?", mergeeContact.ID).Count(&count)
		if count != 0 {
			t.Fatalf("expected mergee contact to be deleted, count = %d", count)
		}

		// Verify all 9 relational entities migrated to baseContact.ID
		var convCount, noteCount, ticketCount, ticketCommentCount, orderCount, campCount, callCount, weCount, ciCount int64
		db.Model(&domain.Conversation{}).Where("contact_id = ?", baseContact.ID).Count(&convCount)
		db.Model(&domain.ContactNote{}).Where("contact_id = ?", baseContact.ID).Count(&noteCount)
		db.Model(&domain.Ticket{}).Where("contact_id = ?", baseContact.ID).Count(&ticketCount)
		db.Model(&domain.TicketComment{}).Where("contact_id = ?", baseContact.ID).Count(&ticketCommentCount)
		db.Model(&domain.Order{}).Where("contact_id = ?", baseContact.ID).Count(&orderCount)
		db.Model(&domain.CampaignDelivery{}).Where("contact_id = ?", baseContact.ID).Count(&campCount)
		db.Model(&domain.Call{}).Where("contact_id = ?", baseContact.ID).Count(&callCount)
		db.Model(&domain.WidgetEvent{}).Where("contact_id = ?", baseContact.ID).Count(&weCount)
		db.Model(&domain.ContactInbox{}).Where("contact_id = ?", baseContact.ID).Count(&ciCount)

		if convCount != 1 || noteCount != 1 || ticketCount != 1 || ticketCommentCount != 1 || orderCount != 1 || campCount != 1 || callCount != 1 || weCount != 1 || ciCount != 1 {
			t.Fatalf("failed foreign key migration: conv=%d note=%d ticket=%d ticketComm=%d order=%d camp=%d call=%d we=%d ci=%d",
				convCount, noteCount, ticketCount, ticketCommentCount, orderCount, campCount, callCount, weCount, ciCount)
		}
	})
}
