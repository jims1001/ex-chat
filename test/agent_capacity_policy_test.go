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
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAgentCapacityPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_capacity_policy_123456",
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
		"name":         "Capacity Admin",
		"email":        "cap_admin@example.com",
		"password":     "Secret123!",
		"account_name": "Capacity Management Corp",
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

	// Create test agents
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

	agent1 := createAgent("Agent Alpha", "alpha@capacity.com")
	agent2 := createAgent("Agent Beta", "beta@capacity.com")
	agentFallback := createAgent("Agent Fallback", "fallback@capacity.com")

	// Create Inboxes
	createInbox := func(name string) uint {
		inboxPayload := map[string]interface{}{
			"name":         name,
			"channel_type": "Channel::WebWidget",
		}
		b, _ := json.Marshal(inboxPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create inbox %s failed: code=%d body=%s", name, w.Code, w.Body.String())
		}
		var resp struct {
			Data domain.Inbox `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Data.ID
	}

	inbox1ID := createInbox("Support Inbox")
	inbox2ID := createInbox("VIP Inbox")

	// Add agents to inboxes
	db.Create(&domain.InboxMember{InboxID: inbox1ID, UserID: agent1.ID})
	db.Create(&domain.InboxMember{InboxID: inbox1ID, UserID: agent2.ID})
	db.Create(&domain.InboxMember{InboxID: inbox2ID, UserID: agent1.ID})

	var policyID uint
	var limitID uint

	// Test 1: Policy CRUD
	t.Run("Create Agent Capacity Policy", func(t *testing.T) {
		payload := map[string]interface{}{
			"name":            "Tier 1 Support Capacity",
			"description":     "Limits active tickets per inbox for Tier 1 agents",
			"exclusion_rules": `{"exclude_resolved":true}`,
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("create capacity policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.AgentCapacityPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.ID == 0 || resp.Data.Name != "Tier 1 Support Capacity" {
			t.Fatalf("unexpected capacity policy created: %+v", resp.Data)
		}
		policyID = resp.Data.ID
	})

	t.Run("Get Agent Capacity Policy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("get capacity policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.AgentCapacityPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Name != "Tier 1 Support Capacity" {
			t.Fatalf("unexpected policy name: %s", resp.Data.Name)
		}
	})

	t.Run("List Agent Capacity Policies", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("list capacity policies failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data []domain.AgentCapacityPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Data) < 1 {
			t.Fatalf("expected at least 1 policy, got %d", len(resp.Data))
		}
	})

	t.Run("Update Agent Capacity Policy", func(t *testing.T) {
		payload := map[string]interface{}{
			"name":        "Updated Tier 1 Policy",
			"description": "Updated description",
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d", accountID, policyID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("update capacity policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.AgentCapacityPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Name != "Updated Tier 1 Policy" {
			t.Fatalf("policy name not updated: %s", resp.Data.Name)
		}
	})

	// Test 2: Member Management
	t.Run("Add Member to Policy", func(t *testing.T) {
		payload := map[string]interface{}{
			"user_id": agent1.ID,
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/users", accountID, policyID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("add user to policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify database state
		var au domain.AccountUser
		if err := db.Where("account_id = ? AND user_id = ?", accountID, agent1.ID).First(&au).Error; err != nil {
			t.Fatalf("failed to query account_user: %v", err)
		}
		if au.AgentCapacityPolicyID == nil || *au.AgentCapacityPolicyID != policyID {
			t.Fatalf("expected agent1 capacity policy ID %d, got %v", policyID, au.AgentCapacityPolicyID)
		}
	})

	t.Run("List Policy Members", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/users", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("list policy users failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data []domain.User `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Data) != 1 || resp.Data[0].ID != agent1.ID {
			t.Fatalf("expected [agent1], got: %+v", resp.Data)
		}
	})

	// Test 3: Inbox Capacity Limits
	t.Run("Add Inbox Capacity Limit", func(t *testing.T) {
		payload := map[string]interface{}{
			"inbox_id":           inbox1ID,
			"conversation_limit": 1, // Max 1 active conversation in Support Inbox
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/inbox_limits", accountID, policyID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("create inbox limit failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.InboxCapacityLimit `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.ID == 0 || resp.Data.ConversationLimit != 1 {
			t.Fatalf("unexpected inbox limit: %+v", resp.Data)
		}
		limitID = resp.Data.ID
	})

	t.Run("Reject Duplicate Inbox Capacity Limit", func(t *testing.T) {
		payload := map[string]interface{}{
			"inbox_id":           inbox1ID,
			"conversation_limit": 5,
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/inbox_limits", accountID, policyID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for duplicate inbox limit, got %d (body=%s)", w.Code, w.Body.String())
		}
	})

	t.Run("Update Inbox Capacity Limit", func(t *testing.T) {
		payload := map[string]interface{}{
			"conversation_limit": 1, // Keep at 1 for subsequent routing test
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/inbox_limits/%d", accountID, policyID, limitID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("update inbox limit failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.InboxCapacityLimit `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.ConversationLimit != 1 {
			t.Fatalf("expected limit 1, got %d", resp.Data.ConversationLimit)
		}
	})

	// Test 4: Capacity Enforcement in Routing Engine
	t.Run("Routing Engine Capacity Enforcement", func(t *testing.T) {
		// Inbox 1 has:
		// Agent 1: has AgentCapacityPolicy with limit=1 for Inbox 1
		// Agent 2: no capacity policy (unlimited)
		// Set assignment policy for Inbox 1: Round Robin with fallback
		assignPolicyPayload := map[string]interface{}{
			"name":                 "Inbox 1 RR Policy",
			"strategy_type":        "round_robin",
			"enabled":              true,
			"agent_capacity_limit": 0, // unlimited at inbox level, governed by agent capacity policy
			"fallback_assignee_id": agentFallback.ID,
		}
		b, _ := json.Marshal(assignPolicyPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create assign policy failed: %d", w.Code)
		}
		var pResp struct {
			Data domain.AssignmentPolicy `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &pResp)

		// Bind assignment policy to inbox 1
		bindPayload := map[string]interface{}{
			"assignment_policy_id": pResp.Data.ID,
		}
		b, _ = json.Marshal(bindPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountID, inbox1ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("bind assign policy failed: %d", w.Code)
		}

		// Create Contact
		contact := &domain.Contact{
			AccountID: accountID,
			Name:      "Customer 1",
			Email:     "c1@example.com",
		}
		db.Create(contact)

		// Let's create conversation through endpoint so AutoAssign is triggered
		createConvPayload := map[string]interface{}{
			"inbox_id":   inbox1ID,
			"contact_id": contact.ID,
			"status":     "open",
		}
		b, _ = json.Marshal(createConvPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var convResp1 struct {
			Data domain.Conversation `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &convResp1)
		firstAssigneeID := convResp1.Data.AssigneeID
		if firstAssigneeID == nil {
			t.Fatalf("expected conversation 1 to be assigned, got nil")
		}

		// Ensure Agent 1 has 1 open conversation in inbox1ID so agent 1 is at limit (1)
		db.Model(&domain.Conversation{}).Where("id = ?", convResp1.Data.ID).Update("assignee_id", agent1.ID)

		// Create Conversation 2: Agent 1 has 1 active ticket (= limit 1), so Agent 1 must NOT be assigned.
		// Agent 2 should be assigned instead!
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation 2 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var convResp2 struct {
			Data domain.Conversation `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &convResp2)

		if convResp2.Data.AssigneeID == nil || *convResp2.Data.AssigneeID != agent2.ID {
			t.Fatalf("expected conversation 2 to be assigned to agent2 (ID=%d) because agent1 is at capacity, got: %v", agent2.ID, convResp2.Data.AssigneeID)
		}

		// Now also give Agent 2 the same capacity policy (limit=1)
		addAgent2Payload := map[string]interface{}{"user_id": agent2.ID}
		b, _ = json.Marshal(addAgent2Payload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/users", accountID, policyID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("add agent2 to policy failed: %d", w.Code)
		}

		// Explicitly ensure convResp2 is assigned to agent 2
		db.Model(&domain.Conversation{}).Where("id = ?", convResp2.Data.ID).Update("assignee_id", agent2.ID)

		// Now both Agent 1 (1 open) and Agent 2 (1 open) are at limit 1!
		// Create Conversation 3: Both agents are at capacity -> AutoAssign falls back to fallback_assignee_id
		b, _ = json.Marshal(createConvPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation 3 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var convResp3 struct {
			Data domain.Conversation `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &convResp3)
		if convResp3.Data.AssigneeID == nil || *convResp3.Data.AssigneeID != agentFallback.ID {
			t.Fatalf("expected fallback assignee %d when all agents at capacity, got: %v", agentFallback.ID, convResp3.Data.AssigneeID)
		}

		// When Agent 1's conversation is resolved, Agent 1 is free again!
		db.Model(&domain.Conversation{}).Where("id = ?", convResp1.Data.ID).Update("status", domain.ConversationStatusResolved)

		// Create Conversation 4: Agent 1 has 0 open (< 1 limit), Agent 2 has 1 open (== 1 limit)
		// Agent 1 should be selected!
		b, _ = json.Marshal(createConvPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation 4 failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var convResp4 struct {
			Data domain.Conversation `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &convResp4)
		if convResp4.Data.AssigneeID == nil || *convResp4.Data.AssigneeID != agent1.ID {
			t.Fatalf("expected conversation 4 to be assigned to agent1 after freeing capacity, got: %v", convResp4.Data.AssigneeID)
		}
	})

	// Test 5: Member Removal
	t.Run("Remove Member from Policy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/users/%d", accountID, policyID, agent2.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("remove user from policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify account_user has nil policy ID
		var au domain.AccountUser
		if err := db.Where("account_id = ? AND user_id = ?", accountID, agent2.ID).First(&au).Error; err != nil {
			t.Fatalf("failed to query account_user: %v", err)
		}
		if au.AgentCapacityPolicyID != nil {
			t.Fatalf("expected nil policy ID for agent2, got: %v", *au.AgentCapacityPolicyID)
		}
	})

	// Test 6: Delete Inbox Capacity Limit
	t.Run("Delete Inbox Capacity Limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/inbox_limits/%d", accountID, policyID, limitID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("delete inbox limit failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify deleted from db
		var count int64
		db.Model(&domain.InboxCapacityLimit{}).Where("id = ?", limitID).Count(&count)
		if count != 0 {
			t.Fatalf("expected inbox limit to be deleted, count=%d", count)
		}
	})

	// Test 7: Cascade Policy Deletion & User Nullification
	t.Run("Delete Policy with Cascade & Nullify", func(t *testing.T) {
		// Re-add an inbox limit and re-assign agent1 to policy
		newLimit := &domain.InboxCapacityLimit{
			AgentCapacityPolicyID: policyID,
			InboxID:               inbox2ID,
			ConversationLimit:     3,
		}
		db.Create(newLimit)

		// Delete the policy
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("delete policy failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 1. Policy should be gone (404)
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted policy, got %d", w.Code)
		}

		// 2. Inbox capacity limits should be cascade deleted
		var limitCount int64
		db.Model(&domain.InboxCapacityLimit{}).Where("agent_capacity_policy_id = ?", policyID).Count(&limitCount)
		if limitCount != 0 {
			t.Fatalf("expected inbox limits to be deleted, count=%d", limitCount)
		}

		// 3. User agent1 should still exist, but AgentCapacityPolicyID nullified
		var au domain.AccountUser
		if err := db.Where("account_id = ? AND user_id = ?", accountID, agent1.ID).First(&au).Error; err != nil {
			t.Fatalf("failed to query agent1 account_user: %v", err)
		}
		if au.AgentCapacityPolicyID != nil {
			t.Fatalf("expected AgentCapacityPolicyID to be nullified, got: %v", *au.AgentCapacityPolicyID)
		}
	})
}
