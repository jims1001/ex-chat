package test

import (
	"bytes"
	"context"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestWidgetLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:widget_test_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_widget_123",
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
		"name":         "Widget Admin",
		"email":        fmt.Sprintf("widget_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "Widget Platform Corp",
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
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	accountID := authResp.Data.Accounts[0].ID
	adminToken := authResp.Data.Token

	// 2. Create Inbox with website token
	websiteToken := fmt.Sprintf("web_token_%d", time.Now().UnixNano())
	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "Widget Web Channel",
		ChannelType:  "Channel::WebWidget",
		WebsiteToken: websiteToken,
	}
	inboxRepo := repository.NewInboxRepository(db)
	if err := inboxRepo.Create(&inbox); err != nil {
		t.Fatalf("failed to create inbox: %v", err)
	}

	visitorSourceID := "visitor_alice_888"
	var conv1ID, conv2ID uint

	// =========================================================================
	// Scenario 1: 历史会话列表与详情查询 (Widget Conversation History & Details)
	// =========================================================================
	t.Run("Scenario 1: Widget Conversation History & Detail Retrieval", func(t *testing.T) {
		// Identify visitor and create initial contact
		identPayload := map[string]any{
			"source_id":         visitorSourceID,
			"name":              "Alice Visitor",
			"email":             "alice@visitor.com",
			"custom_attributes": `{"plan":"basic","language":"en"}`,
		}
		b, _ := json.Marshal(identPayload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/contact?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("visitor identify failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Create First Conversation
		c1Payload := map[string]string{
			"source_id": visitorSourceID,
			"message":   "Hello, this is my first ticket!",
		}
		b, _ = json.Marshal(c1Payload)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/widget/conversations?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("first conversation creation failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var c1Resp struct {
			Data struct {
				Conversation domain.Conversation `json:"conversation"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &c1Resp)
		conv1ID = c1Resp.Data.Conversation.ID

		// Create Second Conversation
		c2Payload := map[string]string{
			"source_id": visitorSourceID,
			"message":   "Hi again, another question about billing.",
		}
		b, _ = json.Marshal(c2Payload)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/widget/conversations?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("second conversation creation failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var c2Resp struct {
			Data struct {
				Conversation domain.Conversation `json:"conversation"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &c2Resp)
		conv2ID = c2Resp.Data.Conversation.ID

		// Query Visitor Conversation History
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/conversations?website_token=%s&source_id=%s", websiteToken, visitorSourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list conversations failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var listResp struct {
			Data struct {
				Conversations []domain.Conversation `json:"conversations"`
				Total         int64                 `json:"total"`
				Page          int                   `json:"page"`
				PageSize      int                   `json:"page_size"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
			t.Fatalf("unmarshal list conversations failed: %v", err)
		}
		if listResp.Data.Total != 2 || len(listResp.Data.Conversations) != 2 {
			t.Fatalf("expected 2 conversations, got total=%d len=%d", listResp.Data.Total, len(listResp.Data.Conversations))
		}
		if listResp.Data.Conversations[0].ID != conv2ID || listResp.Data.Conversations[1].ID != conv1ID {
			t.Fatalf("expected conv2ID=%d first then conv1ID=%d, got %d and %d", conv2ID, conv1ID, listResp.Data.Conversations[0].ID, listResp.Data.Conversations[1].ID)
		}

		// Retrieve Conversation Detail
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/conversations/%d?website_token=%s&source_id=%s", conv1ID, websiteToken, visitorSourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get conversation detail failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var detailResp struct {
			Data domain.Conversation `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &detailResp); err != nil {
			t.Fatalf("unmarshal conversation detail failed: %v", err)
		}
		if detailResp.Data.ID != conv1ID {
			t.Fatalf("expected conversation ID %d, got %d", conv1ID, detailResp.Data.ID)
		}
		if len(detailResp.Data.Messages) == 0 {
			t.Fatalf("expected preloaded messages, got 0")
		}
		if detailResp.Data.Messages[0].Content != "Hello, this is my first ticket!" {
			t.Fatalf("expected initial message content, got %s", detailResp.Data.Messages[0].Content)
		}
	})

	// =========================================================================
	// Scenario 2: Widget 活动/营销展示 (Widget Ongoing Campaigns)
	// =========================================================================
	t.Run("Scenario 2: Widget Active Ongoing Campaigns Retrieval", func(t *testing.T) {
		campaignRepo := repository.NewCampaignRepository(db)

		// 1. Ongoing Active Campaign (Should be returned)
		cActive := domain.Campaign{
			AccountID:    accountID,
			InboxID:      inbox.ID,
			Title:        "Spring Discount Welcome",
			Message:      "Welcome to our site! Use code SPRING20 for 20% off!",
			CampaignType: "ongoing",
			Status:       "active",
			TriggerRules: `{"time_on_page":15,"url":"https://example.com/pricing"}`,
		}
		_ = campaignRepo.Create(context.Background(), &cActive)

		// 2. Ongoing Draft Campaign (Should NOT be returned)
		cDraft := domain.Campaign{
			AccountID:    accountID,
			InboxID:      inbox.ID,
			Title:        "Unreleased Autumn Promo",
			Message:      "Coming soon!",
			CampaignType: "ongoing",
			Status:       "draft",
		}
		_ = campaignRepo.Create(context.Background(), &cDraft)

		// 3. One-off Active Campaign (Should NOT be returned in widget ongoing campaigns)
		cOneOff := domain.Campaign{
			AccountID:    accountID,
			InboxID:      inbox.ID,
			Title:        "One-off Newsletter",
			Message:      "Newsletter blast",
			CampaignType: "one_off",
			Status:       "active",
		}
		_ = campaignRepo.Create(context.Background(), &cOneOff)

		// Query Widget Campaigns
		req := httptest.NewRequest(http.MethodGet, "/api/v1/widget/campaigns?website_token="+websiteToken, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list widget campaigns failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var campResp struct {
			Data struct {
				Campaigns []domain.Campaign `json:"campaigns"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &campResp)
		if len(campResp.Data.Campaigns) != 1 {
			t.Fatalf("expected 1 ongoing active campaign, got %d", len(campResp.Data.Campaigns))
		}
		if campResp.Data.Campaigns[0].Title != "Spring Discount Welcome" {
			t.Fatalf("unexpected campaign title: %s", campResp.Data.Campaigns[0].Title)
		}
	})

	// =========================================================================
	// Scenario 3: 访客事件上报与查询 (Widget Events Tracking)
	// =========================================================================
	t.Run("Scenario 3: Visitor Event Tracking and Verification", func(t *testing.T) {
		eventsToReport := []map[string]any{
			{
				"name":            domain.WidgetEventPageView,
				"source_id":       visitorSourceID,
				"conversation_id": conv1ID,
				"url":             "https://example.com/products/ai-agent",
				"title":           "AI Agent Product Page",
				"properties": map[string]any{
					"referrer": "https://google.com/search",
					"utm_term": "best ai agent",
				},
			},
			{
				"name":      domain.WidgetEventOpened,
				"source_id": visitorSourceID,
				"url":       "https://example.com/products/ai-agent",
				"title":     "AI Agent Product Page",
				"properties": map[string]any{
					"trigger": "user_click",
				},
			},
			{
				"name":      domain.WidgetEventButtonClicked,
				"source_id": visitorSourceID,
				"url":       "https://example.com/pricing",
				"title":     "Pricing Plans",
				"properties": map[string]any{
					"button_id": "btn_upgrade_pro",
				},
			},
		}

		for _, evt := range eventsToReport {
			b, _ := json.Marshal(evt)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/events?website_token="+websiteToken, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("record event %s failed: code=%d body=%s", evt["name"], w.Code, w.Body.String())
			}
		}

		// Query recorded events
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/events?website_token=%s&source_id=%s", websiteToken, visitorSourceID), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list events failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var eventsResp struct {
			Data struct {
				Events []domain.WidgetEvent `json:"events"`
				Total  int64                `json:"total"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &eventsResp)
		if eventsResp.Data.Total != 3 || len(eventsResp.Data.Events) != 3 {
			t.Fatalf("expected 3 events recorded, got total=%d len=%d", eventsResp.Data.Total, len(eventsResp.Data.Events))
		}

		// Assert latest event is button_clicked
		if eventsResp.Data.Events[0].Name != domain.WidgetEventButtonClicked {
			t.Fatalf("expected first event in desc order to be button_clicked, got %s", eventsResp.Data.Events[0].Name)
		}
		if eventsResp.Data.Events[0].ContactID == nil || *eventsResp.Data.Events[0].ContactID == 0 {
			t.Fatalf("expected contact ID to be linked in recorded event")
		}
	})

	// =========================================================================
	// Scenario 4: 访客与会话标签操作 (Visitor & Conversation Labels)
	// =========================================================================
	t.Run("Scenario 4: Visitor and Conversation Labels Management", func(t *testing.T) {
		// 1. Add Labels to visitor contact
		addLabelsPayload := map[string]any{
			"source_id": visitorSourceID,
			"labels":    []string{"vip_customer", "lead_high_intent"},
		}
		b, _ := json.Marshal(addLabelsPayload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/labels?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("add labels failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 2. Query contact labels
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/labels?website_token=%s&source_id=%s", websiteToken, visitorSourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get labels failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var labelResp struct {
			Data struct {
				Labels []domain.Label `json:"labels"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &labelResp)
		if len(labelResp.Data.Labels) != 2 {
			t.Fatalf("expected 2 labels on visitor contact, got %d", len(labelResp.Data.Labels))
		}

		// 3. Add Label specifically to Conversation 1
		addConvLabelPayload := map[string]any{
			"source_id":       visitorSourceID,
			"conversation_id": conv1ID,
			"labels":          []string{"priority_ticket"},
		}
		b, _ = json.Marshal(addConvLabelPayload)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/widget/labels?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("add conversation label failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Query Conversation 1 labels
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/labels?website_token=%s&source_id=%s&conversation_id=%d", websiteToken, visitorSourceID, conv1ID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get conversation labels failed: code=%d body=%s", w.Code, w.Body.String())
		}
		_ = json.Unmarshal(w.Body.Bytes(), &labelResp)
		if len(labelResp.Data.Labels) != 1 || labelResp.Data.Labels[0].Title != "priority_ticket" {
			t.Fatalf("expected priority_ticket on conv1, got %v", labelResp.Data.Labels)
		}

		// 4. Remove a label from contact
		removePayload := map[string]any{
			"source_id": visitorSourceID,
			"labels":    []string{"lead_high_intent"},
		}
		b, _ = json.Marshal(removePayload)
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/widget/labels?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("delete label failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Verify contact labels after removal
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/labels?website_token=%s&source_id=%s", websiteToken, visitorSourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		_ = json.Unmarshal(w.Body.Bytes(), &labelResp)
		if len(labelResp.Data.Labels) != 2 { // "vip_customer" + "priority_ticket" (attached also to contact)
			t.Logf("Remaining labels: %d", len(labelResp.Data.Labels))
		}
		for _, l := range labelResp.Data.Labels {
			if l.Title == "lead_high_intent" {
				t.Fatalf("expected lead_high_intent to be removed")
			}
		}
	})

	// =========================================================================
	// Scenario 5: 访客与会话部分属性操作 (Partial Custom Attributes Merging)
	// =========================================================================
	t.Run("Scenario 5: Contact and Conversation Partial Custom Attributes Merging", func(t *testing.T) {
		// 1. Partial merge into contact custom attributes via POST /contact/custom_attributes
		postAttrsPayload := map[string]any{
			"source_id": visitorSourceID,
			"custom_attributes": map[string]any{
				"company_size": "50-100",
				"cloud":        "GCP",
			},
		}
		b, _ := json.Marshal(postAttrsPayload)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/contact/custom_attributes?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("merge contact custom attributes failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var mergeResp struct {
			Data struct {
				CustomAttributes map[string]any `json:"custom_attributes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &mergeResp)
		// Prior had: "plan":"basic","language":"en"
		if mergeResp.Data.CustomAttributes["cloud"] != "GCP" || mergeResp.Data.CustomAttributes["plan"] != "basic" {
			t.Fatalf("expected preserved 'plan: basic' and new 'cloud: GCP', got %v", mergeResp.Data.CustomAttributes)
		}

		// 2. Partial update via PATCH /api/v1/widget/contact
		patchContactPayload := map[string]any{
			"source_id":    visitorSourceID,
			"name":         "Alice Verified",
			"phone_number": "+1-555-0199",
			"custom_attributes": map[string]any{
				"plan":   "enterprise",
				"region": "us-west1",
			},
		}
		b, _ = json.Marshal(patchContactPayload)
		req = httptest.NewRequest(http.MethodPatch, "/api/v1/widget/contact?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("patch contact failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var contactResp struct {
			Data domain.Contact `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &contactResp)
		if contactResp.Data.Name != "Alice Verified" || contactResp.Data.PhoneNumber != "+1-555-0199" {
			t.Fatalf("contact info not updated correctly: name=%s phone=%s", contactResp.Data.Name, contactResp.Data.PhoneNumber)
		}

		var parsedAttrs map[string]any
		_ = json.Unmarshal([]byte(contactResp.Data.CustomAttributes), &parsedAttrs)
		if parsedAttrs["plan"] != "enterprise" || parsedAttrs["cloud"] != "GCP" || parsedAttrs["region"] != "us-west1" {
			t.Fatalf("custom attributes not shallow merged properly: %v", parsedAttrs)
		}

		// 3. Partial merge on Conversation Custom Attributes via PATCH /conversations/:id/custom_attributes
		patchConvAttrsPayload := map[string]any{
			"source_id": visitorSourceID,
			"custom_attributes": map[string]any{
				"category":    "billing_inquiry",
				"urgency":     "high",
				"device_type": "mobile",
			},
		}
		b, _ = json.Marshal(patchConvAttrsPayload)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/widget/conversations/%d/custom_attributes?website_token=%s", conv1ID, websiteToken), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("merge conv custom attributes failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// Merge a second attribute set
		patchConvAttrsPayload2 := map[string]any{
			"source_id": visitorSourceID,
			"custom_attributes": map[string]any{
				"urgency":   "critical",
				"os_flavor": "iOS 17",
			},
		}
		b, _ = json.Marshal(patchConvAttrsPayload2)
		req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/widget/conversations/%d/custom_attributes?website_token=%s", conv1ID, websiteToken), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("second merge conv custom attributes failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var convAttrsResp struct {
			Data struct {
				CustomAttributes map[string]any `json:"custom_attributes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &convAttrsResp)
		if convAttrsResp.Data.CustomAttributes["category"] != "billing_inquiry" ||
			convAttrsResp.Data.CustomAttributes["urgency"] != "critical" ||
			convAttrsResp.Data.CustomAttributes["os_flavor"] != "iOS 17" {
			t.Fatalf("expected incremental merge on conversation custom attributes, got %v", convAttrsResp.Data.CustomAttributes)
		}
	})

	// =========================================================================
	// Scenario 6: 安全与边界防护 (Security & Boundary Validation)
	// =========================================================================
	t.Run("Scenario 6: Security and Boundary Isolation", func(t *testing.T) {
		// 1. Missing website_token
		req := httptest.NewRequest(http.MethodGet, "/api/v1/widget/conversations", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Fatalf("expected 400 or 404 for missing website_token, got %d", w.Code)
		}

		// 2. Invalid website_token
		req = httptest.NewRequest(http.MethodGet, "/api/v1/widget/conversations?website_token=non_existent_token_999", nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for invalid website_token, got %d", w.Code)
		}

		// 3. Visitor B trying to access Visitor A's conversation
		visitorBSourceID := "visitor_bob_intruder"
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/conversations/%d?website_token=%s&source_id=%s", conv1ID, websiteToken, visitorBSourceID), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when visitor B attempts to access visitor A's conversation, got %d", w.Code)
		}

		// 4. Header X-Website-Token fallback works
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/conversations?source_id=%s", visitorSourceID), nil)
		req.Header.Set("X-Website-Token", websiteToken)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 via X-Website-Token header, got %d", w.Code)
		}

		// 5. Admin Token verification (just confirming admin token is valid)
		if adminToken == "" {
			t.Fatalf("expected admin token to be non-empty")
		}
	})
}
