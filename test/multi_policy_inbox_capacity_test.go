package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestMultiPolicyInboxCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_multi_policy_capacity_123456",
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
		"name":         "Policy Admin",
		"email":        "policy_admin@example.com",
		"password":     "Secret123!",
		"account_name": "MultiPolicy Corp",
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

	// Helper to create agents
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

	agentSenior := createAgent("Senior Agent", "senior@multipolicy.com")
	agentJunior := createAgent("Junior Agent", "junior@multipolicy.com")
	agentTrainee := createAgent("Trainee Agent", "trainee@multipolicy.com")
	agentFallback := createAgent("Fallback Agent", "fallback@multipolicy.com")

	// Helper to create inboxes
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
			t.Fatalf("failed to create inbox %s: %d body=%s", name, w.Code, w.Body.String())
		}
		var resp struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Data.ID
	}

	inboxVIP := createInbox("VIP Inbox")
	inboxStandard := createInbox("Standard Inbox")

	// Add Inbox Members
	// VIP Inbox: Senior, Junior
	db.Create(&domain.InboxMember{InboxID: inboxVIP, UserID: agentSenior.ID})
	db.Create(&domain.InboxMember{InboxID: inboxVIP, UserID: agentJunior.ID})

	// Standard Inbox: Senior, Junior, Trainee
	db.Create(&domain.InboxMember{InboxID: inboxStandard, UserID: agentSenior.ID})
	db.Create(&domain.InboxMember{InboxID: inboxStandard, UserID: agentJunior.ID})
	db.Create(&domain.InboxMember{InboxID: inboxStandard, UserID: agentTrainee.ID})

	// 2. Create Assignment Policies
	// Policy 1 for VIP Inbox: Round Robin, with Fallback Assignee
	apVIPPayload := map[string]interface{}{
		"name":                 "VIP Round Robin Policy",
		"strategy_type":        "round_robin",
		"enabled":              true,
		"fallback_assignee_id": agentFallback.ID,
	}
	b, _ := json.Marshal(apVIPPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies", accountID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create VIP assignment policy failed: %d body=%s", w.Code, w.Body.String())
	}
	var apVIPResp struct {
		Data domain.AssignmentPolicy `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &apVIPResp)
	apVIPID := apVIPResp.Data.ID

	// Policy 2 for Standard Inbox: Least Active, with Fallback Assignee
	apStdPayload := map[string]interface{}{
		"name":                 "Standard Least Active Policy",
		"strategy_type":        "least_active",
		"enabled":              true,
		"fallback_assignee_id": agentFallback.ID,
	}
	b, _ = json.Marshal(apStdPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies", accountID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create Standard assignment policy failed: %d body=%s", w.Code, w.Body.String())
	}
	var apStdResp struct {
		Data domain.AssignmentPolicy `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &apStdResp)
	apStdID := apStdResp.Data.ID

	// Bind policies to inboxes
	bindPolicy := func(inboxID, policyID uint) {
		payload := map[string]interface{}{"assignment_policy_id": policyID}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountID, inboxID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("bind policy %d to inbox %d failed: %d", policyID, inboxID, w.Code)
		}
	}
	bindPolicy(inboxVIP, apVIPID)
	bindPolicy(inboxStandard, apStdID)

	// 3. Create Agent Capacity Policies
	// Policy A: Senior Capacity (VIP limit: 2, Standard limit: 5, exclusion rules: exclude waiting_customer label)
	capSeniorPayload := map[string]interface{}{
		"name":            "Senior Capacity Policy",
		"description":     "High capacity limits for senior agents",
		"exclusion_rules": `{"excluded_labels":["waiting_customer"]}`,
	}
	b, _ = json.Marshal(capSeniorPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies", accountID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create senior capacity policy failed: %d body=%s", w.Code, w.Body.String())
	}
	var capSeniorResp struct {
		Data domain.AgentCapacityPolicy `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &capSeniorResp)
	capSeniorID := capSeniorResp.Data.ID

	// Add Senior limits: VIP = 2, Standard = 5
	addLimit := func(policyID, inboxID uint, limit int) {
		payload := map[string]interface{}{
			"inbox_id":           inboxID,
			"conversation_limit": limit,
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/inbox_limits", accountID, policyID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("add limit for policy %d, inbox %d failed: %d body=%s", policyID, inboxID, w.Code, w.Body.String())
		}
	}
	addLimit(capSeniorID, inboxVIP, 2)
	addLimit(capSeniorID, inboxStandard, 5)

	// Assign Senior Agent to Policy A
	assignAgentToPolicy := func(policyID, agentID uint) {
		payload := map[string]interface{}{"user_id": agentID}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies/%d/users", accountID, policyID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("assign agent %d to policy %d failed: %d", agentID, policyID, w.Code)
		}
	}
	assignAgentToPolicy(capSeniorID, agentSenior.ID)

	// Policy B: Junior Capacity (VIP limit: 1, Standard limit: 2, exclusion rules: older than 24h)
	capJuniorPayload := map[string]interface{}{
		"name":            "Junior Capacity Policy",
		"description":     "Conservative limits for junior agents",
		"exclusion_rules": `{"exclude_older_than_hours":24}`,
	}
	b, _ = json.Marshal(capJuniorPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agent_capacity_policies", accountID), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create junior capacity policy failed: %d body=%s", w.Code, w.Body.String())
	}
	var capJuniorResp struct {
		Data domain.AgentCapacityPolicy `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &capJuniorResp)
	capJuniorID := capJuniorResp.Data.ID

	// Add Junior limits: VIP = 1, Standard = 2
	addLimit(capJuniorID, inboxVIP, 1)
	addLimit(capJuniorID, inboxStandard, 2)

	// Assign Junior Agent to Policy B
	assignAgentToPolicy(capJuniorID, agentJunior.ID)

	// Contact for conversation creation
	contact := &domain.Contact{AccountID: accountID, Name: "VIP Customer", Email: "customer@vip.com"}
	db.Create(contact)

	// Helper to create conversation via API triggering auto-assignment
	createConversation := func(inboxID uint) domain.Conversation {
		payload := map[string]interface{}{
			"inbox_id":   inboxID,
			"contact_id": contact.ID,
			"status":     "open",
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation in inbox %d failed: %d body=%s", inboxID, w.Code, w.Body.String())
		}
		var resp struct {
			Data domain.Conversation `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Data
	}

	// =========================================================================
	// Scenario 1: Multi-member Capacity Enforcement & Skipping in VIP Inbox
	// =========================================================================
	var conv1, conv2, conv3, conv4 domain.Conversation
	t.Run("Multi-member Capacity Enforcement & Skipping in VIP Inbox", func(t *testing.T) {
		// VIP Inbox has Round-Robin: Senior (limit 2), Junior (limit 1)
		// Conversation 1 -> Assigned to Senior (load: Senior=1/2, Junior=0/1)
		conv1 = createConversation(inboxVIP)
		if conv1.AssigneeID == nil || *conv1.AssigneeID != agentSenior.ID {
			t.Fatalf("expected conv1 to be assigned to Senior (%d), got %v", agentSenior.ID, conv1.AssigneeID)
		}

		// Conversation 2 -> Assigned to Junior (load: Senior=1/2, Junior=1/1 -> Junior reaches capacity!)
		conv2 = createConversation(inboxVIP)
		if conv2.AssigneeID == nil || *conv2.AssigneeID != agentJunior.ID {
			t.Fatalf("expected conv2 to be assigned to Junior (%d), got %v", agentJunior.ID, conv2.AssigneeID)
		}

		// Conversation 3 -> Junior is full (1/1). System MUST skip Junior and assign to Senior (Senior=2/2)!
		conv3 = createConversation(inboxVIP)
		if conv3.AssigneeID == nil || *conv3.AssigneeID != agentSenior.ID {
			t.Fatalf("expected conv3 to skip full Junior and assign to Senior (%d), got %v", agentSenior.ID, conv3.AssigneeID)
		}

		// Conversation 4 -> Both Senior (2/2) and Junior (1/1) are full!
		// System MUST trigger Fallback Assignee!
		conv4 = createConversation(inboxVIP)
		if conv4.AssigneeID == nil || *conv4.AssigneeID != agentFallback.ID {
			t.Fatalf("expected conv4 to route to Fallback (%d) because all agents reached capacity, got %v", agentFallback.ID, conv4.AssigneeID)
		}
	})

	// =========================================================================
	// Scenario 2: Cross-Inbox Capacity Isolation
	// =========================================================================
	var convStd1 domain.Conversation
	t.Run("Cross-Inbox Capacity Isolation", func(t *testing.T) {
		// Junior is completely full in VIP Inbox (1/1).
		// Senior is completely full in VIP Inbox (2/2).
		// Now a new conversation arrives in Standard Inbox!
		// In Standard Inbox: Junior limit=2 (currently 0/2), Senior limit=5 (currently 0/5), Trainee=unlimited (0).
		// Verify Junior is NOT blocked in Standard Inbox despite being full in VIP Inbox!
		convStd1 = createConversation(inboxStandard)
		if convStd1.AssigneeID == nil {
			t.Fatalf("expected convStd1 to be assigned to an available agent in Standard Inbox, got nil")
		}
		if *convStd1.AssigneeID == agentFallback.ID {
			t.Fatalf("convStd1 routed to fallback unexpectedly! Agents should have capacity in Standard Inbox")
		}

		// Explicitly assign a conversation in Standard Inbox to Junior to verify Junior can hold up to 2
		assignPayload := map[string]interface{}{"assignee_id": agentJunior.ID}
		b, _ := json.Marshal(assignPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", accountID, convStd1.ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("failed to assign convStd1 to Junior in Standard Inbox: %d body=%s", w.Code, w.Body.String())
		}
	})

	// =========================================================================
	// Scenario 3: Dynamic Capacity Recovery on Resolution
	// =========================================================================
	t.Run("Dynamic Capacity Recovery upon Resolution", func(t *testing.T) {
		// Junior currently has conv2 in VIP Inbox (1/1 = full).
		// Resolve conv2 via toggle_status endpoint
		resolvePayload := map[string]interface{}{"status": domain.ConversationStatusResolved}
		b, _ := json.Marshal(resolvePayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/toggle_status", accountID, conv2.ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("failed to resolve conv2: %d body=%s", w.Code, w.Body.String())
		}

		// Senior is still at 2/2 in VIP Inbox (conv1, conv3).
		// Junior is now at 0/1 (capacity recovered!).
		// Create Conversation in VIP Inbox -> Should route to Junior!
		convVIPRecover := createConversation(inboxVIP)
		if convVIPRecover.AssigneeID == nil || *convVIPRecover.AssigneeID != agentJunior.ID {
			t.Fatalf("expected convVIPRecover to assign to Junior (%d) after resolution, got %v", agentJunior.ID, convVIPRecover.AssigneeID)
		}
	})

	// =========================================================================
	// Scenario 4: Exclusion Rules by Label
	// =========================================================================
	t.Run("Exclusion Rules by Label", func(t *testing.T) {
		// Senior is currently at 2/2 in VIP Inbox (conv1 and conv3).
		// Senior's capacity policy has exclusion_rules: {"excluded_labels":["waiting_customer"]}
		// Create label "waiting_customer"
		label := &domain.Label{AccountID: accountID, Title: "waiting_customer"}
		db.Create(label)

		// Attach label to conv1 (assigned to Senior)
		db.Create(&domain.ConversationLabel{ConversationID: conv1.ID, LabelID: label.ID})

		// Now conv1 is excluded from Senior's active capacity count!
		// Senior's active counted load in VIP Inbox is now 1/2 (has capacity!).
		// Junior is currently at 1/1 (convVIPRecover).
		// Create Conversation in VIP Inbox -> Should route to Senior!
		convExclusionLabel := createConversation(inboxVIP)
		if convExclusionLabel.AssigneeID == nil || *convExclusionLabel.AssigneeID != agentSenior.ID {
			t.Fatalf("expected convExclusionLabel to assign to Senior (%d) due to label exclusion, got %v", agentSenior.ID, convExclusionLabel.AssigneeID)
		}
	})

	// =========================================================================
	// Scenario 5: Exclusion Rules by Age (Inactive Time)
	// =========================================================================
	t.Run("Exclusion Rules by Age", func(t *testing.T) {
		// Junior's policy has exclusion_rules: {"exclude_older_than_hours":24}
		// In Standard Inbox, Junior currently has convStd1 (1 open conversation).
		// Create a second conversation in Standard Inbox and assign to Junior -> Junior reaches limit 2/2!
		cStdB := createConversation(inboxStandard)
		db.Model(&domain.Conversation{}).Where("id = ?", cStdB.ID).Update("assignee_id", agentJunior.ID)

		// At this point Junior has 2 active in Standard Inbox (convStd1 + cStdB = 2/2 = full).
		// Attempting manual assignment of a third conversation to Junior in Standard should fail (400).
		cStdC := createConversation(inboxStandard)
		assignPayload := map[string]interface{}{"assignee_id": agentJunior.ID}
		b, _ := json.Marshal(assignPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", accountID, cStdC.ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected manual assign to fail due to capacity, got code=%d body=%s", w.Code, w.Body.String())
		}

		// Now simulate convStd1 becoming older than 24 hours
		oldTime := time.Now().UTC().Add(-25 * time.Hour)
		db.Model(&domain.Conversation{}).Where("id = ?", convStd1.ID).Updates(map[string]interface{}{
			"created_at":       oldTime,
			"last_activity_at": oldTime,
		})

		// Now convStd1 is excluded by age! Junior's active counted load in Standard drops to 1/2 (< 2).
		// Manual assign of cStdC to Junior should now SUCCEED!
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", accountID, cStdC.ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected manual assign to succeed after age exclusion, got code=%d body=%s", w.Code, w.Body.String())
		}
	})

	// =========================================================================
	// Scenario 6: Manual Assignment Capacity Interception
	// =========================================================================
	t.Run("Manual Assignment Capacity Interception", func(t *testing.T) {
		// Junior is at capacity (1/1) in VIP Inbox.
		// Create an unassigned conversation in VIP Inbox
		cUnassigned := domain.Conversation{
			AccountID: accountID,
			InboxID:   inboxVIP,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&cUnassigned)

		// Try to manually assign to Junior (who is at 1/1 limit in VIP)
		assignPayload := map[string]interface{}{"assignee_id": agentJunior.ID}
		b, _ := json.Marshal(assignPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", accountID, cUnassigned.ID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request when manually assigning over-capacity agent, got code=%d body=%s", w.Code, w.Body.String())
		}

		var errResp struct {
			Error string `json:"error"`
		}
		json.Unmarshal(w.Body.Bytes(), &errResp)
		if errResp.Error != "Agent has reached maximum conversation capacity limit" {
			t.Fatalf("unexpected error message: %s", errResp.Error)
		}
	})
}
