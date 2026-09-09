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

func TestSLAFirstResponseBreach_LateReplyAndScan(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:sla_first_resp_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_secret_sla_frt_123456",
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
		"name":         "SLA Admin",
		"email":        fmt.Sprintf("sla_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "SLA Late Reply Account",
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
		AccountID:    accountID,
		Name:         "Support Web Inbox",
		WebsiteToken: "web_inbox_token",
	}
	_ = db.Create(&inbox).Error

	contact := domain.Contact{
		AccountID: accountID,
		Name:      "Customer Alice",
		Email:     "alice@example.com",
	}
	_ = db.Create(&contact).Error

	// 3. Create SLA Policy with 60 seconds first response threshold
	slaPolicy := domain.SLAPolicy{
		AccountID:                  accountID,
		Name:                       "Strict 60s First Response SLA",
		Description:                "First response within 60 seconds",
		FirstResponseTimeThreshold: 60,   // 60 seconds
		ResolutionTimeThreshold:    7200, // 2 hours
		OnlyDuringBusinessHours:    false,
	}
	_ = db.Create(&slaPolicy).Error

	slaService := service.NewSLAService(db)
	convRepo := repository.NewConversationRepository(db)

	now := time.Now().UTC()

	// -------------------------------------------------------------
	// Scenario 1: User reported scenario:
	// Limit 60 seconds, agent replied at the 59th minute, then scanned.
	// Must record breach (actual seconds ~3540s), not skip!
	// -------------------------------------------------------------
	t.Run("Scenario1_AgentRepliedAt59thMin_Scanned_MustRecordBreach", func(t *testing.T) {
		convStartTime := now.Add(-60 * time.Minute)           // 60 minutes ago
		agentReplyTime := convStartTime.Add(59 * time.Minute) // 59 minutes later (= 1 minute ago)

		convLate := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      convStartTime,
			LastActivityAt: agentReplyTime,
		}
		_ = convRepo.Create(&convLate)

		// Customer message at T = 0
		custMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: convLate.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Urgent issue: please help immediately!",
			CreatedAt:      convStartTime,
		}
		_ = db.Create(&custMsg).Error

		// Agent replied at the 59th minute (far beyond 60 seconds!)
		agentMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: convLate.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Sorry for the delay, investigating now.",
			CreatedAt:      agentReplyTime,
		}
		_ = db.Create(&agentMsg).Error

		// "随后扫描" - Trigger SLA scan via EvaluateAccountSLAs
		breaches, err := slaService.EvaluateAccountSLAs(accountID)
		if err != nil {
			t.Fatalf("EvaluateAccountSLAs failed: %v", err)
		}

		// Verify breach WAS recorded for convLate
		var foundBreach *domain.SLABreachLog
		for _, b := range breaches {
			if b.ConversationID == convLate.ID && b.BreachType == "first_response" {
				foundBreach = &b
				break
			}
		}

		if foundBreach == nil {
			t.Fatalf("FAIL: SLA first response breach was NOT recorded! Agent replied late (59 min vs 60s) but was skipped!")
		}

		if foundBreach.ThresholdSeconds != 60 {
			t.Errorf("expected ThresholdSeconds 60, got %d", foundBreach.ThresholdSeconds)
		}
		if foundBreach.ActualSeconds < 3500 || foundBreach.ActualSeconds > 3600 {
			t.Errorf("expected ActualSeconds ~3540 (59 min), got %d", foundBreach.ActualSeconds)
		}

		// Verify conversation's SLAStatus is updated to "breached"
		refreshedConv, err := convRepo.FindByID(accountID, convLate.ID)
		if err != nil {
			t.Fatalf("FindByID failed: %v", err)
		}
		if refreshedConv.SLAStatus != "breached" {
			t.Errorf("expected conversation SLAStatus to be 'breached', got '%s'", refreshedConv.SLAStatus)
		}

		// Verify via HTTP API endpoint GET /api/v1/accounts/:account_id/conversations/:id/sla
		reqSLA := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/sla", accountID, convLate.ID), nil)
		reqSLA.Header.Set("Authorization", "Bearer "+token)
		wSLA := httptest.NewRecorder()
		r.ServeHTTP(wSLA, reqSLA)

		if wSLA.Code != http.StatusOK {
			t.Fatalf("GET /sla failed with status %d: %s", wSLA.Code, wSLA.Body.String())
		}

		var apiResp struct {
			Data struct {
				SLAStatus             string `json:"sla_status"`
				FirstResponseBreached bool   `json:"first_response_breached"`
			} `json:"data"`
		}
		if err := json.Unmarshal(wSLA.Body.Bytes(), &apiResp); err != nil {
			t.Fatalf("failed to parse SLA API response: %v", err)
		}

		if !apiResp.Data.FirstResponseBreached {
			t.Errorf("expected first_response_breached to be true in API response")
		}
		if apiResp.Data.SLAStatus != "breached" {
			t.Errorf("expected sla_status 'breached' in API response, got '%s'", apiResp.Data.SLAStatus)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Agent replied on time (within 30 seconds):
	// Must NOT record breach.
	// -------------------------------------------------------------
	t.Run("Scenario2_AgentRepliedOnTime_MustNotBreach", func(t *testing.T) {
		convStartTime := now.Add(-10 * time.Minute)
		agentReplyTime := convStartTime.Add(30 * time.Second) // 30s <= 60s

		convOnTime := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      convStartTime,
			LastActivityAt: agentReplyTime,
		}
		_ = convRepo.Create(&convOnTime)

		custMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: convOnTime.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Hello there",
			CreatedAt:      convStartTime,
		}
		_ = db.Create(&custMsg).Error

		agentMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: convOnTime.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Hi, how can I help you?",
			CreatedAt:      agentReplyTime,
		}
		_ = db.Create(&agentMsg).Error

		// Trigger SLA scan
		breaches, err := slaService.EvaluateAccountSLAs(accountID)
		if err != nil {
			t.Fatalf("EvaluateAccountSLAs failed: %v", err)
		}

		for _, b := range breaches {
			if b.ConversationID == convOnTime.ID && b.BreachType == "first_response" {
				t.Fatalf("unexpected breach recorded for on-time response: %+v", b)
			}
		}

		refreshedConv, _ := convRepo.FindByID(accountID, convOnTime.ID)
		if refreshedConv.SLAStatus != "active" {
			t.Errorf("expected sla_status 'active', got '%s'", refreshedConv.SLAStatus)
		}

		_, _, _, isFRT, _, _, _ := slaService.GetConversationSLADeadlines(refreshedConv)
		if isFRT {
			t.Errorf("GetConversationSLADeadlines returned isFRTBreached=true for on-time reply")
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Real-time agent message via API triggers SLA check
	// -------------------------------------------------------------
	t.Run("Scenario3_RealtimeAgentReply_APIEndpoint", func(t *testing.T) {
		convStartTime := time.Now().UTC().Add(-10 * time.Minute)

		convRT := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      convStartTime,
			LastActivityAt: convStartTime,
		}
		_ = convRepo.Create(&convRT)

		custMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: convRT.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Need assistance",
			CreatedAt:      convStartTime,
		}
		_ = db.Create(&custMsg).Error

		// Agent sends reply now via POST /conversations/:id/messages (10 min after message, limit 60s)
		payload := map[string]any{
			"content": "Replying after 10 minutes",
		}
		bodyBytes, _ := json.Marshal(payload)
		reqMsg := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, convRT.ID), bytes.NewReader(bodyBytes))
		reqMsg.Header.Set("Authorization", "Bearer "+token)
		reqMsg.Header.Set("Content-Type", "application/json")
		wMsg := httptest.NewRecorder()
		r.ServeHTTP(wMsg, reqMsg)

		if wMsg.Code != http.StatusCreated {
			t.Fatalf("POST /messages failed with status %d: %s", wMsg.Code, wMsg.Body.String())
		}

		refreshedConv, _ := convRepo.FindByID(accountID, convRT.ID)
		if refreshedConv.SLAStatus != "breached" {
			t.Errorf("expected real-time conversation SLAStatus to be 'breached', got '%s'", refreshedConv.SLAStatus)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: Process SLA via POST /api/v1/accounts/:account_id/sla/process
	// -------------------------------------------------------------
	t.Run("Scenario4_ProcessSLA_Endpoint", func(t *testing.T) {
		reqProc := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/sla/process", accountID), nil)
		reqProc.Header.Set("Authorization", "Bearer "+token)
		wProc := httptest.NewRecorder()
		r.ServeHTTP(wProc, reqProc)

		if wProc.Code != http.StatusOK {
			t.Fatalf("POST /sla/process failed: %d, body: %s", wProc.Code, wProc.Body.String())
		}
	})
}
