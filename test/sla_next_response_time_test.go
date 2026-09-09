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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestSLA_NextResponseTime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:sla_nrt_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_sla_nrt_123",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user to obtain token & account
	signUpPayload := map[string]string{
		"name":         "SLA NRT Admin",
		"email":        fmt.Sprintf("sla_nrt_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "SLA NRT Test Corp",
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
	agentUser := authResp.Data.User

	// 2. Create Inbox and Contact
	inbox := domain.Inbox{
		AccountID: accountID,
		Name:      "Support Web Inbox",
	}
	_ = db.Create(&inbox).Error

	contact := domain.Contact{
		AccountID: accountID,
		Name:      "Customer Carol",
		Email:     "carol@example.com",
	}
	_ = db.Create(&contact).Error

	// 3. Create SLA Policy with both First Response (300s) and Next Response (60s)
	slaPolicy := domain.SLAPolicy{
		AccountID:                  accountID,
		Name:                       "Strict NRT SLA Policy",
		Description:                "First response 300s, Next response 60s",
		FirstResponseTimeThreshold: 300,  // 5 minutes
		NextResponseTimeThreshold:  60,   // 60 seconds
		ResolutionTimeThreshold:    7200, // 2 hours
		OnlyDuringBusinessHours:    false,
	}
	_ = db.Create(&slaPolicy).Error

	slaService := service.NewSLAService(db)
	convRepo := repository.NewConversationRepository(db)

	now := time.Now().UTC()

	// -------------------------------------------------------------
	// Scenario 1: Customer asks initial question, agent responds on time.
	// Then customer asks follow-up question ("客户再次提问").
	// Agent does not reply within 60s (replies 50 minutes later or still unreplied).
	// Subsequent scan -> Next response breach MUST be recorded!
	// -------------------------------------------------------------
	t.Run("Scenario1_CustomerFollowUp_AgentLate_BreachRecorded", func(t *testing.T) {
		t0 := now.Add(-60 * time.Minute) // 1 hour ago
		tFirstReply := t0.Add(10 * time.Second) // First response took only 10s (OK)
		tCustomerFollowUp := t0.Add(10 * time.Minute) // Customer asks again at T+10m
		tAgentLateReply := tCustomerFollowUp.Add(40 * time.Minute) // Agent replies 40 minutes later (threshold is 60s!)

		conv := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      t0,
			LastActivityAt: tAgentLateReply,
		}
		_ = convRepo.Create(&conv)

		// 1. First customer message
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Initial question: can you help?",
			CreatedAt:      t0,
		}).Error

		// 2. First agent reply on time
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Sure, what do you need?",
			CreatedAt:      tFirstReply,
		}).Error

		// 3. Customer asks follow-up question ("客户再次提问")
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "I am having trouble with payment, error code 500.",
			CreatedAt:      tCustomerFollowUp,
		}).Error

		// 4. Agent replies 40 minutes later (far beyond 60s Next Response threshold)
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Sorry for the wait, looking at payment logs now.",
			CreatedAt:      tAgentLateReply,
		}).Error

		// "随后扫描" - Evaluate SLAs
		breaches, err := slaService.EvaluateAccountSLAs(accountID)
		if err != nil {
			t.Fatalf("EvaluateAccountSLAs failed: %v", err)
		}

		// Verify Next Response breach was recorded
		var foundNRTBreach *domain.SLABreachLog
		for _, b := range breaches {
			if b.ConversationID == conv.ID && b.BreachType == "next_response" {
				foundNRTBreach = &b
				break
			}
		}

		if foundNRTBreach == nil {
			t.Fatalf("FAIL: Next response breach was NOT recorded for customer follow-up question!")
		}

		if foundNRTBreach.ThresholdSeconds != 60 {
			t.Errorf("expected ThresholdSeconds 60, got %d", foundNRTBreach.ThresholdSeconds)
		}
		if foundNRTBreach.ActualSeconds < 2300 || foundNRTBreach.ActualSeconds > 2500 { // 40 minutes = 2400s
			t.Errorf("expected ActualSeconds ~2400s (40 min), got %d", foundNRTBreach.ActualSeconds)
		}

		// Verify conversation's SLAStatus is updated to "breached"
		refreshedConv, _ := convRepo.FindByID(accountID, conv.ID)
		if refreshedConv.SLAStatus != "breached" {
			t.Errorf("expected conversation SLAStatus to be 'breached', got '%s'", refreshedConv.SLAStatus)
		}

		// Verify via HTTP API endpoint GET /api/v1/accounts/:account_id/conversations/:id/sla
		reqSLA := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/sla", accountID, conv.ID), nil)
		reqSLA.Header.Set("Authorization", "Bearer "+token)
		wSLA := httptest.NewRecorder()
		r.ServeHTTP(wSLA, reqSLA)

		if wSLA.Code != http.StatusOK {
			t.Fatalf("GET /sla failed: %d, body: %s", wSLA.Code, wSLA.Body.String())
		}

		var apiResp struct {
			Data struct {
				SLAStatus            string `json:"sla_status"`
				FirstResponseBreached bool   `json:"first_response_breached"`
				NextResponseBreached  bool   `json:"next_response_breached"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wSLA.Body.Bytes(), &apiResp)

		if apiResp.Data.FirstResponseBreached {
			t.Errorf("expected FirstResponseBreached to be false (replied in 10s)")
		}
		if !apiResp.Data.NextResponseBreached {
			t.Errorf("expected NextResponseBreached to be true in API response")
		}
		if apiResp.Data.SLAStatus != "breached" {
			t.Errorf("expected sla_status 'breached', got '%s'", apiResp.Data.SLAStatus)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Agent replies to customer follow-up on time (within 30s)
	// Must NOT record Next Response breach.
	// -------------------------------------------------------------
	t.Run("Scenario2_CustomerFollowUp_AgentOnTime_NoBreach", func(t *testing.T) {
		t0 := now.Add(-20 * time.Minute)
		tFirstReply := t0.Add(15 * time.Second)
		tCustomerFollowUp := t0.Add(5 * time.Minute)
		tAgentReplyOnTime := tCustomerFollowUp.Add(30 * time.Second) // 30s <= 60s

		conv := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      t0,
			LastActivityAt: tAgentReplyOnTime,
		}
		_ = convRepo.Create(&conv)

		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Question 1",
			CreatedAt:      t0,
		}).Error

		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Answer 1",
			CreatedAt:      tFirstReply,
		}).Error

		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Follow-up question 2",
			CreatedAt:      tCustomerFollowUp,
		}).Error

		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Answer 2 on time",
			CreatedAt:      tAgentReplyOnTime,
		}).Error

		breaches, err := slaService.EvaluateAccountSLAs(accountID)
		if err != nil {
			t.Fatalf("EvaluateAccountSLAs failed: %v", err)
		}

		for _, b := range breaches {
			if b.ConversationID == conv.ID && b.BreachType == "next_response" {
				t.Fatalf("unexpected next_response breach recorded for on-time reply: %+v", b)
			}
		}

		refreshedConv, _ := convRepo.FindByID(accountID, conv.ID)
		if refreshedConv.SLAStatus != "active" {
			t.Errorf("expected sla_status 'active', got '%s'", refreshedConv.SLAStatus)
		}

		_, _, _, _, isNRT, _, _ := slaService.GetConversationSLADeadlines(refreshedConv)
		if isNRT {
			t.Errorf("expected isNRTBreached=false for on-time reply")
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Customer asks follow-up, agent has not yet replied.
	// Currently waiting and deadline is active.
	// -------------------------------------------------------------
	t.Run("Scenario3_CustomerFollowUp_PendingReply_DeadlinePopulated", func(t *testing.T) {
		t0 := now.Add(-10 * time.Minute)
		tFirstReply := t0.Add(20 * time.Second)
		tCustomerFollowUp := now.Add(-20 * time.Second) // 20s ago (limit is 60s, remaining ~40s)

		conv := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      t0,
			LastActivityAt: tCustomerFollowUp,
		}
		_ = convRepo.Create(&conv)

		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Hi",
			CreatedAt:      t0,
		}).Error

		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Hello",
			CreatedAt:      tFirstReply,
		}).Error

		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "How do I reset password?",
			CreatedAt:      tCustomerFollowUp,
		}).Error

		_, _ = slaService.EvaluateConversation(&conv)

		refreshedConv, _ := convRepo.FindByID(accountID, conv.ID)
		if refreshedConv.NextResponseDueAt == nil {
			t.Fatalf("expected next_response_due_at to be populated on conversation")
		}

		expectedDue := tCustomerFollowUp.Add(60 * time.Second)
		if !refreshedConv.NextResponseDueAt.Equal(expectedDue) {
			t.Errorf("expected next_response_due_at %v, got %v", expectedDue, refreshedConv.NextResponseDueAt)
		}

		// API verification
		reqSLA := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/sla", accountID, conv.ID), nil)
		reqSLA.Header.Set("Authorization", "Bearer "+token)
		wSLA := httptest.NewRecorder()
		r.ServeHTTP(wSLA, reqSLA)

		var apiResp struct {
			Data struct {
				NextResponseDueAt        string `json:"next_response_due_at"`
				NextResponseBreached     bool   `json:"next_response_breached"`
				NextResponseRemainingSec *int   `json:"next_response_remaining_sec"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wSLA.Body.Bytes(), &apiResp)

		if apiResp.Data.NextResponseDueAt == "" {
			t.Errorf("expected non-empty next_response_due_at in API response")
		}
		if apiResp.Data.NextResponseBreached {
			t.Errorf("expected next_response_breached=false because 40s remaining")
		}
		if apiResp.Data.NextResponseRemainingSec == nil || *apiResp.Data.NextResponseRemainingSec <= 0 {
			t.Errorf("expected positive remaining seconds for next response")
		}
	})
}
