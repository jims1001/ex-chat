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

func TestMacro_PrivacyAndExecutionIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_key_1234567890123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Register Admin / User 1
	adminPayload := map[string]string{
		"name":         "Admin One",
		"email":        "admin1@example.com",
		"password":     "Secret123!",
		"account_name": "Isolation Corp",
	}
	body, _ := json.Marshal(adminPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up admin failed: %d", w.Code)
	}

	var adminResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
			User     domain.User      `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &adminResp)
	adminToken := adminResp.Data.Token
	accountID := adminResp.Data.Accounts[0].ID
	accStr := strconv.Itoa(int(accountID))

	// Register Agent 1
	agent1Payload := map[string]string{
		"name":         "Agent One",
		"email":        "agent1@example.com",
		"password":     "Secret123!",
		"account_name": "Dummy Corp 1",
	}
	body, _ = json.Marshal(agent1Payload)
	req = httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var agent1Resp struct {
		Data struct {
			Token string      `json:"token"`
			User  domain.User `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &agent1Resp)
	agent1Token := agent1Resp.Data.Token
	agent1ID := agent1Resp.Data.User.ID

	// Register Agent 2
	agent2Payload := map[string]string{
		"name":         "Agent Two",
		"email":        "agent2@example.com",
		"password":     "Secret123!",
		"account_name": "Dummy Corp 2",
	}
	body, _ = json.Marshal(agent2Payload)
	req = httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var agent2Resp struct {
		Data struct {
			Token string      `json:"token"`
			User  domain.User `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &agent2Resp)
	agent2Token := agent2Resp.Data.Token
	agent2ID := agent2Resp.Data.User.ID

	// Associate Agent 1 and Agent 2 to accountID as role "agent"
	db.Create(&domain.AccountUser{AccountID: accountID, UserID: agent1ID, Role: domain.RoleAgent})
	db.Create(&domain.AccountUser{AccountID: accountID, UserID: agent2ID, Role: domain.RoleAgent})

	// Create a test conversation in accountID
	conv := domain.Conversation{
		AccountID: accountID,
		DisplayID: 101,
		Status:    domain.ConversationStatusOpen,
		Priority:  domain.PriorityMedium,
	}
	db.Create(&conv)

	// 1. Agent 1 creates a personal macro
	macro1Payload := map[string]any{
		"name":       "Agent1 Personal Macro",
		"visibility": "personal",
		"actions":    `[{"action_name":"add_private_note","action_params":["Agent1 private note text"]}]`,
	}
	body, _ = json.Marshal(macro1Payload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/macros", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agent1Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("agent1 create personal macro failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var createdMacro1 struct {
		Data domain.Macro `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &createdMacro1)
	macro1ID := createdMacro1.Data.ID

	// 2. Action Validation check: attempt to create a macro with illegal action -> expect 400
	badMacroPayload := map[string]any{
		"name":       "Malicious Macro",
		"visibility": "personal",
		"actions":    `[{"action_name":"destroy_database_tables","action_params":["all"]}]`,
	}
	body, _ = json.Marshal(badMacroPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/macros", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agent1Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for unsupported macro action, got %d", w.Code)
	}

	// 3. Privacy isolation on List:
	// Agent 2 lists macros in accountID -> must NOT see Agent 1's personal macro
	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accStr+"/macros", nil)
	req.Header.Set("Authorization", "Bearer "+agent2Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent2 list macros failed: %d", w.Code)
	}
	var listResp2 struct {
		Data []domain.Macro `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &listResp2)
	for _, m := range listResp2.Data {
		if m.ID == macro1ID {
			t.Fatalf("agent2 can see agent1's personal macro in list!")
		}
	}

	// 4. Privacy isolation on GetByID:
	// Agent 2 attempts to get Agent 1's personal macro -> expect 404
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/macros/%d", accStr, macro1ID), nil)
	req.Header.Set("Authorization", "Bearer "+agent2Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for agent2 accessing agent1 personal macro, got %d", w.Code)
	}

	// 5. Execution ownership check:
	// Agent 2 attempts to execute Agent 1's personal macro -> expect 404 or 500 error
	execPayload := map[string]any{"conversation_ids": []uint{conv.ID}}
	body, _ = json.Marshal(execPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/macros/%d/execute", accStr, macro1ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agent2Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("agent2 was able to execute agent1's personal macro!")
	}

	// 6. Agent 1 executes their own macro -> success
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/macros/%d/execute", accStr, macro1ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agent1Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent1 failed to execute own personal macro: %d %s", w.Code, w.Body.String())
	}

	// 7. Verify message type of private note: must be "outgoing" with private=true
	var createdMsg domain.Message
	if err := db.Where("conversation_id = ?", conv.ID).Order("id DESC").First(&createdMsg).Error; err != nil {
		t.Fatalf("failed to find message created by macro: %v", err)
	}
	if createdMsg.MessageType != domain.MessageTypeOutgoing {
		t.Errorf("expected private note message_type to be 'outgoing', got '%s'", createdMsg.MessageType)
	}
	if !createdMsg.Private {
		t.Errorf("expected private note private to be true")
	}

	// 8. Test mute_conversation and snooze_conversation macros
	macroMuteSnooze := domain.Macro{
		AccountID:  accountID,
		Name:       "Mute and Snooze Global",
		Visibility: "global",
		CreatedBy:  adminResp.Data.User.ID,
		Actions:    `[{"action_name":"mute_conversation"},{"action_name":"snooze_conversation","action_params":["48h"]}]`,
	}
	db.Create(&macroMuteSnooze)

	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/macros/%d/execute", accStr, macroMuteSnooze.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agent1Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to execute mute and snooze macro: %d", w.Code)
	}

	var checkedConv domain.Conversation
	db.First(&checkedConv, conv.ID)
	if !checkedConv.Muted {
		t.Errorf("expected conversation Muted to be true")
	}
	if checkedConv.Status != domain.ConversationStatusSnoozed {
		t.Errorf("expected conversation Status to be snoozed, got %s", checkedConv.Status)
	}
	if checkedConv.SnoozedUntil == nil || checkedConv.SnoozedUntil.Before(time.Now().UTC().Add(47*time.Hour)) {
		t.Errorf("expected SnoozedUntil to be set ~48h in future")
	}

	// 9. Test cross-tenant assign_agent protection in macro:
	// User from external account should not be assignable
	alienUser := domain.User{Email: "alien@othercorp.com", Name: "Alien Agent"}
	db.Create(&alienUser)

	macroAssignAlien := domain.Macro{
		AccountID:  accountID,
		Name:       "Cross Tenant Assign",
		Visibility: "global",
		CreatedBy:  adminResp.Data.User.ID,
		Actions:    fmt.Sprintf(`[{"action_name":"assign_agent","action_params":["%d"]}]`, alienUser.ID),
	}
	db.Create(&macroAssignAlien)

	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/macros/%d/execute", accStr, macroAssignAlien.ID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("macro execute request failed: %d", w.Code)
	}

	db.First(&checkedConv, conv.ID)
	if checkedConv.AssigneeID != nil && *checkedConv.AssigneeID == alienUser.ID {
		t.Fatalf("cross-tenant user was assigned to conversation!")
	}
}

func TestAutomation_RuleValidationAndCrossTenantProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_key_1234567890123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Register Admin
	adminPayload := map[string]string{
		"name":         "Auto Admin",
		"email":        "autoadmin@example.com",
		"password":     "Secret123!",
		"account_name": "Automation Corp",
	}
	body, _ := json.Marshal(adminPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up admin failed: %d", w.Code)
	}

	var adminResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &adminResp)
	token := adminResp.Data.Token
	accountID := adminResp.Data.Accounts[0].ID
	accStr := strconv.Itoa(int(accountID))

	// 1. Validation: Invalid event name -> 400
	badEventRule := map[string]any{
		"name":       "Bad Event Rule",
		"event_name": "non_existent_event",
		"conditions": `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`,
		"actions":    `[{"action_name":"change_status","action_params":["resolved"]}]`,
	}
	body, _ = json.Marshal(badEventRule)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/automation_rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid event name, got %d", w.Code)
	}

	// 2. Validation: Invalid condition attribute -> 400
	badAttrRule := map[string]any{
		"name":       "Bad Attribute Rule",
		"event_name": "conversation_created",
		"conditions": `[{"attribute_key":"unknown_fake_field","filter_operator":"equal_to","values":["val"]}]`,
		"actions":    `[{"action_name":"change_status","action_params":["resolved"]}]`,
	}
	body, _ = json.Marshal(badAttrRule)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/automation_rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid condition attribute, got %d", w.Code)
	}

	// 3. Validation: Invalid query_operator -> 400
	badQOpRule := map[string]any{
		"name":       "Bad Query Op Rule",
		"event_name": "conversation_created",
		"conditions": `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"],"query_operator":"XOR"}]`,
		"actions":    `[{"action_name":"change_status","action_params":["resolved"]}]`,
	}
	body, _ = json.Marshal(badQOpRule)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/automation_rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid query_operator, got %d", w.Code)
	}

	// 4. Validation: Invalid action name -> 400
	badActionRule := map[string]any{
		"name":       "Bad Action Rule",
		"event_name": "conversation_created",
		"conditions": `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`,
		"actions":    `[{"action_name":"hack_system","action_params":["root"]}]`,
	}
	body, _ = json.Marshal(badActionRule)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/automation_rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid action, got %d", w.Code)
	}

	// 5. Create valid automation rule matching contact email and conversation language
	validRule := map[string]any{
		"name":       "Email and Lang Matcher",
		"event_name": "conversation_created",
		"conditions": `[{"attribute_key":"email","filter_operator":"contains","values":["vip"]},{"attribute_key":"conversation_language","filter_operator":"equal_to","values":["en"],"query_operator":"AND"}]`,
		"actions":    `[{"action_name":"add_label","action_params":["vip_english"]},{"action_name":"add_private_note","action_params":["Auto note added"]}]`,
	}
	body, _ = json.Marshal(validRule)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/automation_rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for valid rule, got %d: %s", w.Code, w.Body.String())
	}

	// Execute automation service evaluation
	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	autoService := service.NewAutomationService(db, convRepo, msgRepo)

	// Create contact with VIP email
	contact := domain.Contact{
		AccountID: accountID,
		Name:      "VIP Customer",
		Email:     "vip_customer@example.com",
	}
	db.Create(&contact)

	// Create conversation matching VIP email and English conversation_language
	conv := domain.Conversation{
		AccountID:        accountID,
		DisplayID:        201,
		ContactID:        contact.ID,
		Status:           domain.ConversationStatusOpen,
		CustomAttributes: `{"conversation_language":"en"}`,
	}
	db.Create(&conv)

	autoService.HandleConversationCreated(&conv)

	// Verify label was added
	var lbl domain.Label
	if err := db.Where("account_id = ? AND title = ?", accountID, "vip_english").First(&lbl).Error; err != nil {
		t.Fatalf("automation rule failed to match contact email and conversation_language: %v", err)
	}

	// Verify private note message type is outgoing with private=true
	var autoMsg domain.Message
	if err := db.Where("conversation_id = ?", conv.ID).First(&autoMsg).Error; err != nil {
		t.Fatalf("failed to find auto-created private note: %v", err)
	}
	if autoMsg.MessageType != domain.MessageTypeOutgoing {
		t.Errorf("expected automation private note message_type outgoing, got %s", autoMsg.MessageType)
	}
	if !autoMsg.Private {
		t.Errorf("expected automation private note private to be true")
	}

	// 6. Test cross-tenant assign_agent in AutomationService
	alienUser := domain.User{Email: "alien_auto@other.com", Name: "Alien Auto"}
	db.Create(&alienUser)

	ruleAlienAssign := domain.AutomationRule{
		AccountID:  accountID,
		Name:       "Cross Tenant Assign Rule",
		EventName:  "conversation_created",
		Conditions: `[]`,
		Actions:    fmt.Sprintf(`[{"action_name":"assign_agent","action_params":{"user_id":%d}}]`, alienUser.ID),
		Active:     true,
	}
	db.Create(&ruleAlienAssign)

	conv2 := domain.Conversation{
		AccountID: accountID,
		DisplayID: 202,
		ContactID: contact.ID,
		Status:    domain.ConversationStatusOpen,
	}
	db.Create(&conv2)

	autoService.HandleConversationCreated(&conv2)

	var checkedConv domain.Conversation
	db.First(&checkedConv, conv2.ID)
	if checkedConv.AssigneeID != nil && *checkedConv.AssigneeID == alienUser.ID {
		t.Fatalf("automation rule cross-tenant assignment was not blocked!")
	}
}
