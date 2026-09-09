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
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAssignmentPolicyEnhancement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_assignment_policy_123456",
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
		"name":         "Manager Mike",
		"email":        "mike@example.com",
		"password":     "Secret123!",
		"account_name": "Routing Policy Corp",
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
	adminUserID := authResp.Data.User.ID

	// Create additional test agents
	createAgent := func(name, email string) *domain.User {
		user := &domain.User{
			Name:         name,
			Email:        email,
			PasswordHash: "dummy_hash",
			Role:         domain.RoleAgent,
			Availability: domain.AvailabilityOnline,
		}
		if err := db.Create(user).Error; err != nil {
			t.Fatalf("failed to create agent %s: %v", name, err)
		}
		accountUser := &domain.AccountUser{
			AccountID: accountID,
			UserID:    user.ID,
			Role:      domain.RoleAgent,
		}
		db.Create(accountUser)
		return user
	}

	agent1 := createAgent("Agent Alpha", "alpha@example.com")
	agent2 := createAgent("Agent Beta", "beta@example.com")
	agentFallback := createAgent("Agent Fallback", "fallback@example.com")

	// Create Inbox
	inboxPayload := map[string]interface{}{
		"name":         "Sales Inbox",
		"channel_type": "Channel::WebWidget",
	}
	body, _ = json.Marshal(inboxPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes", accountID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create inbox failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var inboxResp struct {
		Data domain.Inbox `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &inboxResp)
	inboxID := inboxResp.Data.ID

	// Add agent1 and agent2 to the inbox members
	db.Create(&domain.InboxMember{InboxID: inboxID, UserID: agent1.ID})
	db.Create(&domain.InboxMember{InboxID: inboxID, UserID: agent2.ID})

	var policyID uint

	// 2. Test Policy CRUD
	t.Run("Create Assignment Policy", func(t *testing.T) {
		createPayload := map[string]interface{}{
			"name":                 "Round Robin With Capacity",
			"description":          "Equally distribute traffic with cap of 5",
			"strategy_type":        "round_robin",
			"enabled":              true,
			"agent_capacity_limit": 5,
			"fallback_assignee_id": agentFallback.ID,
		}
		body, _ := json.Marshal(createPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies", accountID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var policyResp struct {
			Data domain.AssignmentPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &policyResp)
		if policyResp.Data.ID == 0 || policyResp.Data.Name != "Round Robin With Capacity" {
			t.Fatalf("unexpected policy created: %+v", policyResp.Data)
		}
		policyID = policyResp.Data.ID
	})

	t.Run("Get Assignment Policy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var getResp struct {
			Data domain.AssignmentPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &getResp)
		if getResp.Data.StrategyType != "round_robin" || getResp.Data.AgentCapacityLimit != 5 {
			t.Fatalf("unexpected policy fetched: %+v", getResp.Data)
		}
	})

	t.Run("List Assignment Policies", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list policies failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var listResp struct {
			Data []domain.AssignmentPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &listResp)
		if len(listResp.Data) < 1 {
			t.Fatalf("expected at least 1 policy, got %d", len(listResp.Data))
		}
	})

	t.Run("Update Assignment Policy", func(t *testing.T) {
		updatePayload := map[string]interface{}{
			"name":                 "Updated RR Policy",
			"description":          "Updated description",
			"strategy_type":        "round_robin",
			"enabled":              true,
			"agent_capacity_limit": 10,
			"fallback_assignee_id": agentFallback.ID,
		}
		body, _ := json.Marshal(updatePayload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d", accountID, policyID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var updateResp struct {
			Data domain.AssignmentPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &updateResp)
		if updateResp.Data.Name != "Updated RR Policy" || updateResp.Data.AgentCapacityLimit != 10 {
			t.Fatalf("unexpected update response: %+v", updateResp.Data)
		}
	})

	// 3. Test Inbox Binding
	t.Run("Bind Policy to Inbox", func(t *testing.T) {
		bindPayload := map[string]interface{}{
			"policy_id": policyID,
		}
		body, _ := json.Marshal(bindPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountID, inboxID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("bind policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify inbox has assignment_policy preloaded
		inboxRepo := repository.NewInboxRepository(db)
		ib, err := inboxRepo.FindByID(accountID, inboxID)
		if err != nil || ib == nil {
			t.Fatalf("failed to fetch inbox: %v", err)
		}
		if ib.AssignmentPolicyID == nil || *ib.AssignmentPolicyID != policyID {
			t.Fatalf("inbox AssignmentPolicyID not set properly: %v", ib.AssignmentPolicyID)
		}
		if ib.AssignmentPolicy == nil || ib.AssignmentPolicy.Name != "Updated RR Policy" {
			t.Fatalf("inbox AssignmentPolicy not preloaded: %+v", ib.AssignmentPolicy)
		}
	})

	// 4. Test Policy Routing Logic
	convRepo := repository.NewConversationRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	policyRepo := repository.NewAssignmentPolicyRepository(db)
	routingService := service.NewRoutingService(db, convRepo, hub)

	t.Run("Round Robin Distribution Between Agents", func(t *testing.T) {
		// Create 3 conversations sequentially
		conv1 := &domain.Conversation{AccountID: accountID, InboxID: inboxID, Status: domain.ConversationStatusOpen}
		db.Create(conv1)
		assignedUser, err := routingService.AutoAssign(conv1)
		if err != nil {
			t.Fatalf("AutoAssign conv1 failed: %v", err)
		}
		if assignedUser == nil || conv1.AssigneeID == nil {
			t.Fatalf("conv1 should have been assigned")
		}
		firstAssigneeID := *conv1.AssigneeID
		if firstAssigneeID != agent1.ID && firstAssigneeID != agent2.ID {
			t.Fatalf("conv1 assigned to unexpected agent: %d", firstAssigneeID)
		}

		conv2 := &domain.Conversation{AccountID: accountID, InboxID: inboxID, Status: domain.ConversationStatusOpen}
		db.Create(conv2)
		assignedUser, err = routingService.AutoAssign(conv2)
		if err != nil {
			t.Fatalf("AutoAssign conv2 failed: %v", err)
		}
		if assignedUser == nil || conv2.AssigneeID == nil {
			t.Fatalf("conv2 should have been assigned")
		}
		secondAssigneeID := *conv2.AssigneeID
		if secondAssigneeID == firstAssigneeID {
			t.Fatalf("conv2 should be round-robined to different agent, got %d for both", firstAssigneeID)
		}

		// Third conversation should loop back to firstAssigneeID
		conv3 := &domain.Conversation{AccountID: accountID, InboxID: inboxID, Status: domain.ConversationStatusOpen}
		db.Create(conv3)
		assignedUser, err = routingService.AutoAssign(conv3)
		if err != nil {
			t.Fatalf("AutoAssign conv3 failed: %v", err)
		}
		if assignedUser == nil || conv3.AssigneeID == nil || *conv3.AssigneeID != firstAssigneeID {
			t.Fatalf("conv3 should be round-robined back to first assignee (%d), got: %v", firstAssigneeID, conv3.AssigneeID)
		}
	})

	t.Run("Capacity Limit and Fallback Routing", func(t *testing.T) {
		// Update policy: capacity limit = 1, fallback to agentFallback
		pol, err := policyRepo.FindByID(accountID, policyID)
		if err != nil || pol == nil {
			t.Fatalf("failed to find policy: %v", err)
		}
		pol.AgentCapacityLimit = 1
		pol.FallbackAssigneeID = &agentFallback.ID
		if err := policyRepo.Update(pol); err != nil {
			t.Fatalf("failed to update policy: %v", err)
		}

		// Mark existing open conversations for agent1 and agent2
		// Currently agent1 and agent2 both have at least 1 open conversation.
		// Therefore, with capacity limit = 1, neither agent1 nor agent2 has remaining capacity.
		convOverflow := &domain.Conversation{AccountID: accountID, InboxID: inboxID, Status: domain.ConversationStatusOpen}
		db.Create(convOverflow)
		assignedUser, err := routingService.AutoAssign(convOverflow)
		if err != nil {
			t.Fatalf("AutoAssign convOverflow failed: %v", err)
		}
		if assignedUser == nil || convOverflow.AssigneeID == nil || *convOverflow.AssigneeID != agentFallback.ID {
			t.Fatalf("convOverflow should route to fallback assignee (%d), got: %v", agentFallback.ID, convOverflow.AssigneeID)
		}
	})

	t.Run("Disabled Policy Skips Routing", func(t *testing.T) {
		// Disable policy
		pol, err := policyRepo.FindByID(accountID, policyID)
		if err != nil || pol == nil {
			t.Fatalf("failed to find policy: %v", err)
		}
		pol.Enabled = false
		if err := policyRepo.Update(pol); err != nil {
			t.Fatalf("failed to update policy: %v", err)
		}

		convDisabled := &domain.Conversation{AccountID: accountID, InboxID: inboxID, Status: domain.ConversationStatusOpen}
		db.Create(convDisabled)
		assignedUser, err := routingService.AutoAssign(convDisabled)
		if err != nil {
			t.Fatalf("AutoAssign convDisabled failed: %v", err)
		}
		if assignedUser != nil || convDisabled.AssigneeID != nil {
			t.Fatalf("disabled policy should not assign conversation, got assignee: %v", convDisabled.AssigneeID)
		}
	})

	// 5. Test Delete Policy
	t.Run("Delete Assignment Policy and Clear Inbox Association", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify inbox has no policy bound
		ib, _ := inboxRepo.FindByID(accountID, inboxID)
		if ib.AssignmentPolicyID != nil {
			t.Fatalf("inbox AssignmentPolicyID should be nil after policy deletion, got: %v", ib.AssignmentPolicyID)
		}

		// Verify 404 when getting deleted policy
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted policy, got %d", w.Code)
		}
	})

	_ = adminUserID
}
