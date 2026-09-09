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
)

func TestCampaignLifecycle(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "campaign-lifecycle-test-jwt-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up Account A
	signUpPayloadA := map[string]string{
		"account_name": "Campaign Corp A",
		"name":         "Campaign Admin A",
		"email":        "admin@campaign-a.com",
		"password":     "Password123!",
	}
	bodyA, _ := json.Marshal(signUpPayloadA)
	reqA := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyA))
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	r.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusCreated {
		t.Fatalf("sign up account A failed: code=%d body=%s", wA.Code, wA.Body.String())
	}

	var authRespA struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wA.Body.Bytes(), &authRespA)
	tokenA := authRespA.Data.Token
	accIDA := authRespA.Data.Accounts[0].ID
	accIDStrA := fmt.Sprintf("%d", accIDA)

	// 2. Sign up Account B (Tenant isolation check)
	signUpPayloadB := map[string]string{
		"account_name": "Campaign Corp B",
		"name":         "Campaign Admin B",
		"email":        "admin@campaign-b.com",
		"password":     "Password123!",
	}
	bodyB, _ := json.Marshal(signUpPayloadB)
	reqB := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyB))
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	r.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusCreated {
		t.Fatalf("sign up account B failed: code=%d body=%s", wB.Code, wB.Body.String())
	}

	var authRespB struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wB.Body.Bytes(), &authRespB)
	tokenB := authRespB.Data.Token
	accIDB := authRespB.Data.Accounts[0].ID
	accIDStrB := fmt.Sprintf("%d", accIDB)

	// Create test inbox in Account A
	inboxA := domain.Inbox{
		AccountID:    accIDA,
		Name:         "Campaign Inbox A",
		ChannelType:  domain.ChannelWebWidget,
		WebsiteToken: "camp_inbox_token_a",
	}
	db.Create(&inboxA)

	// Create test contacts in Account A
	contactA1 := domain.Contact{
		AccountID: accIDA,
		Name:      "Alice Wonderland",
		Email:     "alice@example.com",
	}
	db.Create(&contactA1)

	contactA2 := domain.Contact{
		AccountID: accIDA,
		Name:      "Bob Builder",
		Email:     "bob@example.com",
	}
	db.Create(&contactA2)

	var createdCampaignID uint

	// ==========================================
	// Scenario 1: Create Campaign & Get Details
	// ==========================================
	t.Run("Scenario_1:_Create_Campaign_And_Get_Details", func(t *testing.T) {
		campPayload := map[string]any{
			"inbox_id":      inboxA.ID,
			"title":         "Welcome Onboarding Campaign",
			"description":   "Onboarding welcome campaign for new contacts",
			"message":       "Welcome to our service! How can we assist you today?",
			"campaign_type": "ongoing",
			"status":        "active",
			"trigger_rules": map[string]any{"url": "https://example.com/welcome"},
			"audience":      "all",
		}
		body, _ := json.Marshal(campPayload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/campaigns", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("create campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.Campaign `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.ID == 0 {
			t.Fatalf("expected valid campaign ID, got 0")
		}
		if resp.Data.Title != "Welcome Onboarding Campaign" {
			t.Errorf("expected title 'Welcome Onboarding Campaign', got %s", resp.Data.Title)
		}
		if resp.Data.CampaignType != "ongoing" {
			t.Errorf("expected campaign_type 'ongoing', got %s", resp.Data.CampaignType)
		}
		if resp.Data.Status != "active" {
			t.Errorf("expected status 'active', got %s", resp.Data.Status)
		}

		createdCampaignID = resp.Data.ID
		campIDStr := fmt.Sprintf("%d", createdCampaignID)

		// Fetch Campaign Details via GET /campaigns/:id
		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr, nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("get campaign detail failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var detailResp struct {
			Data domain.Campaign `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &detailResp)
		if detailResp.Data.ID != createdCampaignID {
			t.Errorf("expected campaign ID %d, got %d", createdCampaignID, detailResp.Data.ID)
		}
		if detailResp.Data.InboxID != inboxA.ID {
			t.Errorf("expected inbox ID %d, got %d", inboxA.ID, detailResp.Data.InboxID)
		}
	})

	// ==========================================
	// Scenario 2: Update Campaign
	// ==========================================
	t.Run("Scenario_2:_Update_Campaign", func(t *testing.T) {
		campIDStr := fmt.Sprintf("%d", createdCampaignID)
		updatedTitle := "Updated Welcome Onboarding Campaign"
		updatedMessage := "Updated welcome greeting! Check out our new docs."
		updatePayload := map[string]any{
			"title":   updatedTitle,
			"message": updatedMessage,
		}
		body, _ := json.Marshal(updatePayload)
		req := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("update campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.Campaign `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Title != updatedTitle {
			t.Errorf("expected updated title %s, got %s", updatedTitle, resp.Data.Title)
		}
		if resp.Data.Message != updatedMessage {
			t.Errorf("expected updated message %s, got %s", updatedMessage, resp.Data.Message)
		}
	})

	// ==========================================
	// Scenario 3: Ongoing Campaign Lifecycle (Pause, Resume, Stop)
	// ==========================================
	t.Run("Scenario_3:_Campaign_Lifecycle_Transitions", func(t *testing.T) {
		campIDStr := fmt.Sprintf("%d", createdCampaignID)

		// 3.1 Pause Campaign
		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr+"/pause", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("pause campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify status is paused
		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr, nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var getResp struct {
			Data domain.Campaign `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &getResp)
		if getResp.Data.Status != "paused" {
			t.Fatalf("expected status 'paused', got %s", getResp.Data.Status)
		}

		// 3.2 Try Triggering Paused Campaign -> Should be rejected
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr+"/trigger", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected bad request when triggering paused campaign, got %d", w.Code)
		}

		// 3.3 Resume Campaign
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr+"/resume", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("resume campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify status is active
		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr, nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		_ = json.Unmarshal(w.Body.Bytes(), &getResp)
		if getResp.Data.Status != "active" {
			t.Fatalf("expected status 'active', got %s", getResp.Data.Status)
		}
	})

	// ==========================================
	// Scenario 4: Ongoing Campaign Real-time Inbound Trigger & Anti-spam Deduplication
	// ==========================================
	t.Run("Scenario_4:_Ongoing_Trigger_And_Deduplication", func(t *testing.T) {
		// Contact A1 creates conversation 1 on Inbox A
		convPayload1 := map[string]any{
			"inbox_id":   inboxA.ID,
			"contact_id": contactA1.ID,
		}
		body, _ := json.Marshal(convPayload1)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/conversations", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation 1 failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var convResp1 struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &convResp1)
		convID1 := convResp1.Data.ID

		// Verify that the Ongoing Campaign was triggered and delivered a message to conversation 1
		var messages1 []domain.Message
		db.Where("conversation_id = ?", convID1).Find(&messages1)
		if len(messages1) != 1 {
			t.Fatalf("expected 1 campaign message in conversation 1, got %d", len(messages1))
		}
		if messages1[0].Content != "Updated welcome greeting! Check out our new docs." {
			t.Errorf("unexpected message content: %s", messages1[0].Content)
		}

		// Verify that a CampaignDelivery record exists for Contact A1
		var deliveries []domain.CampaignDelivery
		db.Where("campaign_id = ? AND contact_id = ?", createdCampaignID, contactA1.ID).Find(&deliveries)
		if len(deliveries) != 1 {
			t.Fatalf("expected 1 CampaignDelivery for contact A1, got %d", len(deliveries))
		}

		// Contact A1 creates conversation 2 on the same Inbox A -> Should NOT trigger campaign again (Anti-spam Deduplication)
		convPayload2 := map[string]any{
			"inbox_id":   inboxA.ID,
			"contact_id": contactA1.ID,
		}
		body2, _ := json.Marshal(convPayload2)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/conversations", bytes.NewReader(body2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation 2 failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var convResp2 struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &convResp2)
		convID2 := convResp2.Data.ID

		var messages2 []domain.Message
		db.Where("conversation_id = ?", convID2).Find(&messages2)
		if len(messages2) != 0 {
			t.Fatalf("expected 0 campaign messages in conversation 2 (deduplicated), got %d", len(messages2))
		}

		// Contact A2 creates a conversation on Inbox A -> Should trigger campaign for Contact A2
		convPayload3 := map[string]any{
			"inbox_id":   inboxA.ID,
			"contact_id": contactA2.ID,
		}
		body3, _ := json.Marshal(convPayload3)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/conversations", bytes.NewReader(body3))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation 3 failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var convResp3 struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &convResp3)
		convID3 := convResp3.Data.ID

		var messages3 []domain.Message
		db.Where("conversation_id = ?", convID3).Find(&messages3)
		if len(messages3) != 1 {
			t.Fatalf("expected 1 campaign message in conversation 3 for Contact A2, got %d", len(messages3))
		}
	})

	// ==========================================
	// Scenario 5: Deliveries Listing & Metrics
	// ==========================================
	t.Run("Scenario_5:_Deliveries_Listing_And_Metrics", func(t *testing.T) {
		campIDStr := fmt.Sprintf("%d", createdCampaignID)

		// 5.1 GET /campaigns/:id/deliveries
		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr+"/deliveries", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("list deliveries failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var delResp struct {
			Data struct {
				Deliveries []domain.CampaignDelivery `json:"deliveries"`
				Total      int64                     `json:"total"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &delResp)
		if delResp.Data.Total != 2 {
			t.Errorf("expected 2 total deliveries (for contact 1 and 2), got %d", delResp.Data.Total)
		}

		// 5.2 GET /campaigns/:id/metrics
		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr+"/metrics", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("get metrics failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var metricsResp struct {
			Data struct {
				DeliveriesCount int64   `json:"deliveries_count"`
				SentCount       int64   `json:"sent_count"`
				DeliveryRate    float64 `json:"delivery_rate"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &metricsResp)
		if metricsResp.Data.DeliveriesCount != 2 {
			t.Errorf("expected metrics deliveries_count 2, got %d", metricsResp.Data.DeliveriesCount)
		}
		if metricsResp.Data.DeliveryRate != 1.0 {
			t.Errorf("expected 100%% delivery rate (1.0), got %f", metricsResp.Data.DeliveryRate)
		}
	})

	// ==========================================
	// Scenario 6: Stop Campaign & Cancel
	// ==========================================
	t.Run("Scenario_6:_Stop_And_Cancel_Campaign", func(t *testing.T) {
		campIDStr := fmt.Sprintf("%d", createdCampaignID)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr+"/stop", nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("stop campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify status is cancelled
		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr, nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var getResp struct {
			Data domain.Campaign `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &getResp)
		if getResp.Data.Status != "cancelled" {
			t.Fatalf("expected status 'cancelled', got %s", getResp.Data.Status)
		}
	})

	// ==========================================
	// Scenario 7: Cross-tenant Isolation
	// ==========================================
	t.Run("Scenario_7:_Cross_Tenant_Isolation", func(t *testing.T) {
		campIDStr := fmt.Sprintf("%d", createdCampaignID)

		// Tenant B attempts to fetch Tenant A's campaign -> Should return 404
		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrB+"/campaigns/"+campIDStr, nil)
		req.Header.Set("Authorization", "Bearer "+tokenB)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 for cross-tenant get campaign, got %d", w.Code)
		}

		// Tenant B attempts to update Tenant A's campaign -> Should return 404
		body, _ := json.Marshal(map[string]any{"title": "Hacked Title"})
		req = httptest.NewRequest(http.MethodPut, "/api/v1/accounts/"+accIDStrB+"/campaigns/"+campIDStr, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenB)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 for cross-tenant update campaign, got %d", w.Code)
		}

		// Tenant B attempts to pause Tenant A's campaign -> Should return 404 / 400
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStrB+"/campaigns/"+campIDStr+"/pause", nil)
		req.Header.Set("Authorization", "Bearer "+tokenB)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
			t.Errorf("expected error for cross-tenant pause campaign, got %d", w.Code)
		}
	})

	// ==========================================
	// Scenario 8: Delete Campaign
	// ==========================================
	t.Run("Scenario_8:_Delete_Campaign", func(t *testing.T) {
		campIDStr := fmt.Sprintf("%d", createdCampaignID)

		// Delete Campaign
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr, nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("delete campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Subsequent GET -> Should return 404
		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStrA+"/campaigns/"+campIDStr, nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 after deletion, got %d", w.Code)
		}
	})
}
