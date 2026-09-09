package test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAppliedSLALifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:applied_sla_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_applied_sla_123",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user to obtain token & account 1
	signUpPayload := map[string]string{
		"name":         "Applied SLA Admin",
		"email":        fmt.Sprintf("sla_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Applied SLA Test Corp",
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

	// 2. Sign up Account 2 (for tenant isolation check)
	signUpPayload2 := map[string]string{
		"name":         "Other Admin",
		"email":        fmt.Sprintf("other_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Tenant 2 Corp",
	}
	body2, _ := json.Marshal(signUpPayload2)
	req2 := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	var authResp2 struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	token2 := authResp2.Data.Token
	accountID2 := authResp2.Data.Accounts[0].ID

	// Create Inboxes and Contacts
	inbox := domain.Inbox{
		AccountID: accountID,
		Name:      "Tier 1 Support",
	}
	_ = db.Create(&inbox).Error

	contact := domain.Contact{
		AccountID: accountID,
		Name:      "Alice Customer",
		Email:     "alice@example.com",
	}
	_ = db.Create(&contact).Error

	// Create SLA Policy
	slaPolicy := domain.SLAPolicy{
		AccountID:                  accountID,
		Name:                       "Standard SLA Policy",
		Description:                "FRT 300s, NRT 120s, RT 3600s",
		FirstResponseTimeThreshold: 300,  // 5m
		NextResponseTimeThreshold:  120,  // 2m
		ResolutionTimeThreshold:    3600, // 1h
	}
	_ = db.Create(&slaPolicy).Error

	slaService := service.NewSLAService(db)
	appliedSLARepo := repository.NewAppliedSLARepository(db)
	slaService.SetAppliedSLARepo(appliedSLARepo)
	convRepo := repository.NewConversationRepository(db)

	now := time.Now().UTC()

	// -------------------------------------------------------------
	// Scenario 1: SLA Binding and Initial State
	// -------------------------------------------------------------
	t.Run("Scenario1_SLABinding_InitialStateActive", func(t *testing.T) {
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
			CreatedAt: now,
		}
		_ = convRepo.Create(&conv)

		// Bind SLA policy via POST /api/v1/accounts/:account_id/conversations/:id/applied_sla
		applyPayload := map[string]uint{"sla_policy_id": slaPolicy.ID}
		applyBody, _ := json.Marshal(applyPayload)
		reqApply := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/applied_sla", accountID, conv.ID), bytes.NewReader(applyBody))
		reqApply.Header.Set("Authorization", "Bearer "+token)
		reqApply.Header.Set("Content-Type", "application/json")
		wApply := httptest.NewRecorder()
		r.ServeHTTP(wApply, reqApply)

		if wApply.Code != http.StatusOK {
			t.Fatalf("POST /applied_sla failed: code=%d body=%s", wApply.Code, wApply.Body.String())
		}

		// Verify AppliedSLA record exists and is active
		applied, err := appliedSLARepo.GetAppliedSLAByConversation(accountID, conv.ID)
		if err != nil || applied == nil {
			t.Fatalf("failed to find applied sla for conv: %v", err)
		}

		if applied.SLAStatus != domain.AppliedSLAStatusActive {
			t.Errorf("expected SLAStatus '%s', got '%s'", domain.AppliedSLAStatusActive, applied.SLAStatus)
		}
		if applied.SLAPolicyID != slaPolicy.ID {
			t.Errorf("expected SLAPolicyID %d, got %d", slaPolicy.ID, applied.SLAPolicyID)
		}

		// Verify query via GET /api/v1/accounts/:account_id/conversations/:id/applied_sla
		reqGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/applied_sla", accountID, conv.ID), nil)
		reqGet.Header.Set("Authorization", "Bearer "+token)
		wGet := httptest.NewRecorder()
		r.ServeHTTP(wGet, reqGet)

		if wGet.Code != http.StatusOK {
			t.Fatalf("GET /applied_sla failed: code=%d body=%s", wGet.Code, wGet.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: First Response Breach (FRT) -> active_with_misses
	// -------------------------------------------------------------
	t.Run("Scenario2_FirstResponseBreach_StateTransition", func(t *testing.T) {
		t0 := now.Add(-30 * time.Minute) // 30 minutes ago, FRT threshold is 5m (300s)

		conv := domain.Conversation{
			AccountID:   accountID,
			InboxID:     inbox.ID,
			ContactID:   contact.ID,
			Status:      domain.ConversationStatusOpen,
			SLAPolicyID: &slaPolicy.ID,
			CreatedAt:   t0,
		}
		_ = convRepo.Create(&conv)
		_, _ = appliedSLARepo.EnsureAppliedSLA(accountID, conv.ID, slaPolicy.ID)

		// Customer message created at t0
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Need help urgently!",
			CreatedAt:      t0,
		}).Error

		// Trigger evaluation
		breaches, err := slaService.EvaluateConversation(&conv)
		if err != nil {
			t.Fatalf("EvaluateConversation failed: %v", err)
		}

		if len(breaches) == 0 {
			t.Fatalf("expected FRT breach, got 0 breaches")
		}

		// Verify AppliedSLA state became active_with_misses
		applied, err := appliedSLARepo.GetAppliedSLAByConversation(accountID, conv.ID)
		if err != nil || applied == nil {
			t.Fatalf("failed to retrieve applied SLA: %v", err)
		}

		if applied.SLAStatus != domain.AppliedSLAStatusActiveWithMisses {
			t.Errorf("expected status '%s', got '%s'", domain.AppliedSLAStatusActiveWithMisses, applied.SLAStatus)
		}

		// Verify SLAEvent recorded
		if len(applied.SLAEvents) == 0 {
			t.Fatalf("expected at least 1 SLAEvent on applied SLA")
		}
		foundFRT := false
		for _, ev := range applied.SLAEvents {
			if ev.EventType == domain.SLAEventTypeFRT {
				foundFRT = true
				break
			}
		}
		if !foundFRT {
			t.Errorf("expected frt SLAEvent, but none found")
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Next Response Breach (NRT) with message_id Meta
	// -------------------------------------------------------------
	t.Run("Scenario3_NextResponseBreach_MetadataBinding", func(t *testing.T) {
		t0 := now.Add(-50 * time.Minute)
		tAgentFirstReply := t0.Add(1 * time.Minute)      // Replied on time for FRT
		tCustomerFollowUp := t0.Add(15 * time.Minute)    // Customer asks again
		tAgentLateReply := tCustomerFollowUp.Add(10 * time.Minute) // Replied after 10m (NRT threshold is 2m = 120s)

		conv := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inbox.ID,
			ContactID:      contact.ID,
			Status:         domain.ConversationStatusOpen,
			SLAPolicyID:    &slaPolicy.ID,
			CreatedAt:      t0,
			LastActivityAt: tAgentLateReply,
		}
		_ = convRepo.Create(&conv)
		_, _ = appliedSLARepo.EnsureAppliedSLA(accountID, conv.ID, slaPolicy.ID)

		// 1. Initial customer message
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Can I change my order?",
			CreatedAt:      t0,
		}).Error

		// 2. Prompt agent reply
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Yes, what would you like to change?",
			CreatedAt:      tAgentFirstReply,
		}).Error

		// 3. Customer follow-up message
		customerFollowUpMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Change item to XL size please",
			CreatedAt:      tCustomerFollowUp,
		}
		_ = db.Create(&customerFollowUpMsg).Error

		// 4. Agent late reply
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "Updated to XL!",
			CreatedAt:      tAgentLateReply,
		}).Error

		// Evaluate SLA
		_, err := slaService.EvaluateConversation(&conv)
		if err != nil {
			t.Fatalf("EvaluateConversation failed: %v", err)
		}

		applied, err := appliedSLARepo.GetAppliedSLAByConversation(accountID, conv.ID)
		if err != nil || applied == nil {
			t.Fatalf("failed to retrieve applied SLA: %v", err)
		}

		if applied.SLAStatus != domain.AppliedSLAStatusActiveWithMisses {
			t.Errorf("expected SLAStatus '%s', got '%s'", domain.AppliedSLAStatusActiveWithMisses, applied.SLAStatus)
		}

		// Verify SLAEvent for NRT contains the message_id in Meta
		var nrtEvent *domain.SLAEvent
		for _, ev := range applied.SLAEvents {
			if ev.EventType == domain.SLAEventTypeNRT {
				nrtEvent = &ev
				break
			}
		}

		if nrtEvent == nil {
			t.Fatalf("FAIL: SLAEvent with event_type 'nrt' was not found!")
		}

		if !strings.Contains(nrtEvent.Meta, fmt.Sprintf("%d", customerFollowUpMsg.ID)) {
			t.Errorf("expected nrt event meta to contain message_id %d, got '%s'", customerFollowUpMsg.ID, nrtEvent.Meta)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: Final Status Resolution (hit / missed / reopen)
	// -------------------------------------------------------------
	t.Run("Scenario4_FinalStatusResolution", func(t *testing.T) {
		// 4A: Clean conversation without breaches -> hit on resolve
		convClean := domain.Conversation{
			AccountID:   accountID,
			InboxID:     inbox.ID,
			ContactID:   contact.ID,
			Status:      domain.ConversationStatusOpen,
			SLAPolicyID: &slaPolicy.ID,
			CreatedAt:   now.Add(-10 * time.Minute),
		}
		_ = convRepo.Create(&convClean)
		_, _ = appliedSLARepo.EnsureAppliedSLA(accountID, convClean.ID, slaPolicy.ID)

		// Customer message and quick agent reply (10s)
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: convClean.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Hello, thanks for quick help",
			CreatedAt:      convClean.CreatedAt,
		}).Error
		_ = db.Create(&domain.Message{
			AccountID:      accountID,
			ConversationID: convClean.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "You're welcome!",
			CreatedAt:      convClean.CreatedAt.Add(10 * time.Second),
		}).Error

		// Resolve conversation via POST /api/v1/accounts/:account_id/conversations/:id/toggle_status
		reqResolve := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/toggle_status", accountID, convClean.ID), bytes.NewReader([]byte(`{"status":"resolved"}`)))
		reqResolve.Header.Set("Authorization", "Bearer "+token)
		reqResolve.Header.Set("Content-Type", "application/json")
		wResolve := httptest.NewRecorder()
		r.ServeHTTP(wResolve, reqResolve)

		if wResolve.Code != http.StatusOK {
			t.Fatalf("toggle_status resolved failed: code=%d body=%s", wResolve.Code, wResolve.Body.String())
		}

		appliedClean, _ := appliedSLARepo.GetAppliedSLAByConversation(accountID, convClean.ID)
		if appliedClean.SLAStatus != domain.AppliedSLAStatusHit {
			t.Errorf("expected clean conversation applied SLA to be 'hit', got '%s'", appliedClean.SLAStatus)
		}

		// 4B: Reopen clean conversation -> reverts to 'active'
		reqReopen := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/toggle_status", accountID, convClean.ID), bytes.NewReader([]byte(`{"status":"open"}`)))
		reqReopen.Header.Set("Authorization", "Bearer "+token)
		reqReopen.Header.Set("Content-Type", "application/json")
		wReopen := httptest.NewRecorder()
		r.ServeHTTP(wReopen, reqReopen)

		appliedReopened, _ := appliedSLARepo.GetAppliedSLAByConversation(accountID, convClean.ID)
		if appliedReopened.SLAStatus != domain.AppliedSLAStatusActive {
			t.Errorf("expected reopened clean conversation applied SLA to be 'active', got '%s'", appliedReopened.SLAStatus)
		}

		// 4C: Breached conversation -> missed on resolve
		convBreached := domain.Conversation{
			AccountID:   accountID,
			InboxID:     inbox.ID,
			ContactID:   contact.ID,
			Status:      domain.ConversationStatusOpen,
			SLAPolicyID: &slaPolicy.ID,
			CreatedAt:   now.Add(-60 * time.Minute),
		}
		_ = convRepo.Create(&convBreached)
		appliedB, _ := appliedSLARepo.EnsureAppliedSLA(accountID, convBreached.ID, slaPolicy.ID)
		// Insert an FRT breach event
		_ = appliedSLARepo.RecordSLAEvent(&domain.SLAEvent{
			AccountID:      accountID,
			ConversationID: convBreached.ID,
			AppliedSLAID:   appliedB.ID,
			SLAPolicyID:    slaPolicy.ID,
			InboxID:        inbox.ID,
			EventType:      domain.SLAEventTypeFRT,
		})
		_, _ = slaService.EvaluateConversation(&convBreached)

		// Resolve breached conversation
		reqResolveB := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/toggle_status", accountID, convBreached.ID), bytes.NewReader([]byte(`{"status":"resolved"}`)))
		reqResolveB.Header.Set("Authorization", "Bearer "+token)
		reqResolveB.Header.Set("Content-Type", "application/json")
		wResolveB := httptest.NewRecorder()
		r.ServeHTTP(wResolveB, reqResolveB)

		appliedBreached, _ := appliedSLARepo.GetAppliedSLAByConversation(accountID, convBreached.ID)
		if appliedBreached.SLAStatus != domain.AppliedSLAStatusMissed {
			t.Errorf("expected breached conversation applied SLA to be 'missed', got '%s'", appliedBreached.SLAStatus)
		}
	})

	// -------------------------------------------------------------
	// Scenario 5: List, Metrics & UTF-8 BOM CSV Export
	// -------------------------------------------------------------
	t.Run("Scenario5_List_Metrics_and_CSV_Export", func(t *testing.T) {
		// 5A: List Applied SLAs with filtering
		reqList := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/applied_slas?page=1&per_page=20", accountID), nil)
		reqList.Header.Set("Authorization", "Bearer "+token)
		wList := httptest.NewRecorder()
		r.ServeHTTP(wList, reqList)

		if wList.Code != http.StatusOK {
			t.Fatalf("GET /applied_slas failed: code=%d body=%s", wList.Code, wList.Body.String())
		}

		var listResp struct {
			Payload []gin.H `json:"payload"`
			Meta    struct {
				Count       int `json:"count"`
				CurrentPage int `json:"current_page"`
			} `json:"meta"`
		}
		_ = json.Unmarshal(wList.Body.Bytes(), &listResp)

		if listResp.Meta.Count == 0 || len(listResp.Payload) == 0 {
			t.Errorf("expected count > 0 in applied_slas list, got count=%d, items=%d", listResp.Meta.Count, len(listResp.Payload))
		}

		// Test only_missed filter
		reqOnlyMissed := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/applied_slas?only_missed=true", accountID), nil)
		reqOnlyMissed.Header.Set("Authorization", "Bearer "+token)
		wOnlyMissed := httptest.NewRecorder()
		r.ServeHTTP(wOnlyMissed, reqOnlyMissed)

		if wOnlyMissed.Code != http.StatusOK {
			t.Fatalf("GET /applied_slas?only_missed=true failed: code=%d", wOnlyMissed.Code)
		}

		// 5B: Metrics calculation
		reqMetrics := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/applied_slas/metrics", accountID), nil)
		reqMetrics.Header.Set("Authorization", "Bearer "+token)
		wMetrics := httptest.NewRecorder()
		r.ServeHTTP(wMetrics, reqMetrics)

		if wMetrics.Code != http.StatusOK {
			t.Fatalf("GET /applied_slas/metrics failed: code=%d body=%s", wMetrics.Code, wMetrics.Body.String())
		}

		var metricsResp repository.AppliedSLAMetrics
		_ = json.Unmarshal(wMetrics.Body.Bytes(), &metricsResp)

		if metricsResp.TotalAppliedSLAs == 0 {
			t.Errorf("expected TotalAppliedSLAs > 0")
		}
		if metricsResp.NumberOfSLAMisses == 0 {
			t.Errorf("expected NumberOfSLAMisses > 0 from previous breached tests")
		}

		// Also check reports route /api/v2/accounts/:account_id/reports/applied_slas/metrics
		reqReportsMetrics := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%d/reports/applied_slas/metrics", accountID), nil)
		reqReportsMetrics.Header.Set("Authorization", "Bearer "+token)
		wReportsMetrics := httptest.NewRecorder()
		r.ServeHTTP(wReportsMetrics, reqReportsMetrics)
		if wReportsMetrics.Code != http.StatusOK {
			t.Errorf("expected v2 reports metrics to succeed, got code %d", wReportsMetrics.Code)
		}

		// 5C: Download CSV with UTF-8 BOM
		reqDownload := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/applied_slas/download", accountID), nil)
		reqDownload.Header.Set("Authorization", "Bearer "+token)
		wDownload := httptest.NewRecorder()
		r.ServeHTTP(wDownload, reqDownload)

		if wDownload.Code != http.StatusOK {
			t.Fatalf("GET /applied_slas/download failed: code=%d body=%s", wDownload.Code, wDownload.Body.String())
		}

		contentDisposition := wDownload.Header().Get("Content-Disposition")
		if !strings.Contains(contentDisposition, "breached_conversation.csv") {
			t.Errorf("expected Content-Disposition to contain breached_conversation.csv, got '%s'", contentDisposition)
		}

		rawBytes := wDownload.Body.Bytes()
		// Check UTF-8 BOM \xEF\xBB\xBF
		if len(rawBytes) < 3 || rawBytes[0] != 0xEF || rawBytes[1] != 0xBB || rawBytes[2] != 0xBF {
			t.Errorf("expected CSV response to start with UTF-8 BOM (0xEF, 0xBB, 0xBF)")
		}

		// Strip BOM and parse CSV
		csvReader := csv.NewReader(bytes.NewReader(rawBytes[3:]))
		header, err := csvReader.Read()
		if err != nil {
			t.Fatalf("failed to read CSV header: %v", err)
		}
		if len(header) < 5 {
			t.Errorf("expected at least 5 header columns in CSV, got %d", len(header))
		}

		// Read rows
		rowCount := 0
		for {
			row, err := csvReader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("failed reading CSV row: %v", err)
			}
			if len(row) > 0 {
				rowCount++
			}
		}

		if rowCount == 0 {
			t.Errorf("expected CSV rows to be exported, got 0 rows")
		}
	})

	// -------------------------------------------------------------
	// Scenario 6: Multi-tenant Security Isolation and Unbinding
	// -------------------------------------------------------------
	t.Run("Scenario6_TenantIsolation_and_Unbinding", func(t *testing.T) {
		// Create conversation in account 1
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&conv)
		_, _ = appliedSLARepo.EnsureAppliedSLA(accountID, conv.ID, slaPolicy.ID)

		// 6A: Access from Account 2 must fail or return not found
		reqCross := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/applied_sla", accountID2, conv.ID), nil)
		reqCross.Header.Set("Authorization", "Bearer "+token2)
		wCross := httptest.NewRecorder()
		r.ServeHTTP(wCross, reqCross)

		if wCross.Code != http.StatusNotFound {
			t.Errorf("expected 404 for cross-tenant applied_sla access, got %d", wCross.Code)
		}

		// 6B: Unbind SLA policy via DELETE /api/v1/accounts/:account_id/conversations/:id/applied_sla
		reqUnbind := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/applied_sla", accountID, conv.ID), nil)
		reqUnbind.Header.Set("Authorization", "Bearer "+token)
		wUnbind := httptest.NewRecorder()
		r.ServeHTTP(wUnbind, reqUnbind)

		if wUnbind.Code != http.StatusOK {
			t.Fatalf("DELETE /applied_sla failed: code=%d body=%s", wUnbind.Code, wUnbind.Body.String())
		}

		// Verify AppliedSLA is deleted
		appliedAfter, err := appliedSLARepo.GetAppliedSLAByConversation(accountID, conv.ID)
		if err == nil && appliedAfter != nil {
			t.Errorf("expected applied SLA to be nil after deletion, got %+v", appliedAfter)
		}

		// Verify conversation's SLAPolicyID is cleared
		reloadedConv, _ := convRepo.FindByID(accountID, conv.ID)
		if reloadedConv.SLAPolicyID != nil {
			t.Errorf("expected conversation SLAPolicyID to be nil after unbinding, got %d", *reloadedConv.SLAPolicyID)
		}
	})
}
