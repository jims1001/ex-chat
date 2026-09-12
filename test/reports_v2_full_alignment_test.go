package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestReportsV2_FullAlignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_reports_v2_alignment_987654",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up account
	signUpPayload := map[string]string{
		"name":         "Report Admin",
		"email":        "reportadmin@example.com",
		"password":     "Secret123!",
		"account_name": "Metrics Global Corp",
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
			User     domain.User      `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	accountID := authResp.Data.Accounts[0].ID
	accIDStr := strconv.Itoa(int(accountID))
	adminToken := authResp.Data.Token
	adminUserID := authResp.Data.User.ID

	// Helper for authorized requests
	doGet := func(path string, acceptCSV bool) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		if acceptCSV {
			req.Header.Set("Accept", "text/csv")
		}
		r.ServeHTTP(rec, req)
		return rec
	}

	// 2. Setup baseline test data
	// Agent 2
	agentUser := domain.User{
		Name:         "Support Agent Anna",
		DisplayName:  "Anna",
		Email:        "anna@example.com",
		Role:         domain.RoleAgent,
		Availability: "online",
	}
	db.Create(&agentUser)
	db.Create(&domain.AccountUser{
		AccountID: accountID,
		UserID:    agentUser.ID,
		Role:      domain.RoleAgent,
	})

	// Team
	team := domain.Team{
		AccountID: accountID,
		Name:      "Tier 1 Support",
	}
	db.Create(&team)

	// Inboxes
	inboxWidget := domain.Inbox{
		AccountID:    accountID,
		Name:         "Website Chat",
		ChannelType:  "Channel::WebWidget",
		WebsiteToken: "web_tok_rep_1",
	}
	db.Create(&inboxWidget)

	inboxEmail := domain.Inbox{
		AccountID:    accountID,
		Name:         "Support Mail",
		ChannelType:  "Channel::Email",
		WebsiteToken: "mail_tok_rep_2",
	}
	db.Create(&inboxEmail)

	// Label
	label := domain.Label{
		AccountID: accountID,
		Title:     "billing-inquiry",
		Color:     "#0088cc",
	}
	db.Create(&label)

	// Contact
	contact := domain.Contact{
		AccountID: accountID,
		Name:      "Customer Alice",
		Email:     "alice@customer.com",
	}
	db.Create(&contact)

	// Agent Bot
	bot := domain.AgentBot{
		AccountID: accountID,
		Name:      "HelpBot AI",
	}
	db.Create(&bot)
	db.Create(&domain.AgentBotInbox{
		AccountID:  accountID,
		InboxID:    inboxWidget.ID,
		AgentBotID: bot.ID,
	})

	// Create Conversations with different statuses
	// Conv 1: Open, unassigned, in inboxWidget, has label, unread
	now := time.Now().UTC()
	conv1 := domain.Conversation{
		AccountID:      accountID,
		DisplayID:      1,
		InboxID:        inboxWidget.ID,
		ContactID:      contact.ID,
		TeamID:         &team.ID,
		Status:         domain.ConversationStatusOpen,
		Priority:       "urgent",
		UnreadCount:    2,
		LastActivityAt: now,
		CreatedAt:      now.Add(-2 * time.Hour),
	}
	db.Create(&conv1)
	db.Create(&domain.ConversationLabel{
		ConversationID: conv1.ID,
		LabelID:        label.ID,
	})

	// Conv 2: Open, assigned to agentUser, in inboxWidget
	conv2 := domain.Conversation{
		AccountID:      accountID,
		DisplayID:      2,
		InboxID:        inboxWidget.ID,
		ContactID:      contact.ID,
		AssigneeID:     &agentUser.ID,
		TeamID:         &team.ID,
		Status:         domain.ConversationStatusOpen,
		Priority:       "medium",
		UnreadCount:    0,
		LastActivityAt: now,
		CreatedAt:      now.Add(-1 * time.Hour),
	}
	db.Create(&conv2)

	// Conv 3: Resolved, in inboxEmail
	conv3 := domain.Conversation{
		AccountID:      accountID,
		DisplayID:      3,
		InboxID:        inboxEmail.ID,
		ContactID:      contact.ID,
		AssigneeID:     &adminUserID,
		Status:         domain.ConversationStatusResolved,
		Priority:       "low",
		LastActivityAt: now,
		CreatedAt:      now.Add(-5 * time.Hour),
	}
	db.Create(&conv3)

	// Messages
	msgIn := domain.Message{
		AccountID:      accountID,
		ConversationID: conv1.ID,
		SenderType:     domain.SenderTypeContact,
		SenderID:       contact.ID,
		MessageType:    domain.MessageTypeIncoming,
		Content:        "Need help with my invoice",
		CreatedAt:      now.Add(-100 * time.Minute),
	}
	db.Create(&msgIn)

	msgOutUser := domain.Message{
		AccountID:      accountID,
		ConversationID: conv2.ID,
		SenderType:     domain.SenderTypeUser,
		SenderID:       agentUser.ID,
		MessageType:    domain.MessageTypeOutgoing,
		Content:        "Hello Alice, looking into this now.",
		CreatedAt:      now.Add(-50 * time.Minute),
	}
	db.Create(&msgOutUser)

	// Reporting Events
	db.Create(&domain.ReportingEvent{
		AccountID:      accountID,
		InboxID:        &inboxWidget.ID,
		ConversationID: &conv1.ID,
		Name:           "conversation_bot_resolved",
		CreatedAt:      now.Add(-1 * time.Hour),
	})
	db.Create(&domain.ReportingEvent{
		AccountID:      accountID,
		InboxID:        &inboxWidget.ID,
		ConversationID: &conv2.ID,
		Name:           "conversation_bot_handoff",
		CreatedAt:      now.Add(-40 * time.Minute),
	})
	db.Create(&domain.ReportingEvent{
		AccountID:      accountID,
		UserID:          &adminUserID,
		ConversationID: &conv3.ID,
		Name:           "first_response",
		Value:          150, // 150 seconds
		CreatedAt:      now.Add(-4 * time.Hour),
	})

	// ==========================================
	// 1. Conversations Report
	// ==========================================
	t.Run("1_Conversations_Report", func(t *testing.T) {
		// Type account
		w := doGet("/api/v2/accounts/"+accIDStr+"/reports/conversations?type=account", false)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var accMetrics struct {
			Open       int64 `json:"open"`
			Unattended int64 `json:"unattended"`
			Unassigned int64 `json:"unassigned"`
			Pending    int64 `json:"pending"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &accMetrics)
		if accMetrics.Open < 2 {
			t.Errorf("expected at least 2 open conversations, got %d", accMetrics.Open)
		}
		if accMetrics.Unassigned < 1 {
			t.Errorf("expected at least 1 unassigned conversation, got %d", accMetrics.Unassigned)
		}

		// Type agent
		wAgent := doGet("/api/v2/accounts/"+accIDStr+"/reports/conversations?type=agent", false)
		if wAgent.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", wAgent.Code, wAgent.Body.String())
		}
		var agentReports []struct {
			ID     uint `json:"id"`
			Metric struct {
				Open int64 `json:"open"`
			} `json:"metric"`
		}
		_ = json.Unmarshal(wAgent.Body.Bytes(), &agentReports)
		if len(agentReports) == 0 {
			t.Errorf("expected agent reports to return elements")
		}
	})

	// ==========================================
	// 2. Conversations Summary
	// ==========================================
	t.Run("2_Conversations_Summary", func(t *testing.T) {
		// JSON response
		w := doGet("/api/v2/accounts/"+accIDStr+"/reports/conversations_summary", false)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				ConversationsCount    int64 `json:"conversations_count"`
				IncomingMessagesCount int64 `json:"incoming_messages_count"`
				OutgoingMessagesCount int64 `json:"outgoing_messages_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !resp.Success || resp.Data.ConversationsCount < 3 {
			t.Errorf("unexpected conversations summary data: %+v", resp.Data)
		}

		// CSV response
		wCSV := doGet("/api/v2/accounts/"+accIDStr+"/reports/conversations_summary?format=csv", true)
		if wCSV.Code != http.StatusOK {
			t.Fatalf("expected 200 CSV, got %d", wCSV.Code)
		}
		if !strings.Contains(wCSV.Body.String(), "Conversations,Incoming Messages") {
			t.Errorf("expected CSV header, got: %s", wCSV.Body.String())
		}
	})

	// ==========================================
	// 3. Conversation Traffic
	// ==========================================
	t.Run("3_Conversation_Traffic", func(t *testing.T) {
		w := doGet("/api/v2/accounts/"+accIDStr+"/reports/conversation_traffic?timezone_offset=0", false)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var matrix [][]any
		if err := json.Unmarshal(w.Body.Bytes(), &matrix); err != nil {
			t.Fatalf("failed to unmarshal traffic matrix: %v", err)
		}
		// Expect 25 rows (1 header row + 24 hourly rows)
		if len(matrix) != 25 {
			t.Errorf("expected 25 rows in heatmap matrix, got %d", len(matrix))
		}
		if matrix[0][0] != "Start of the hour" {
			t.Errorf("expected header 'Start of the hour', got %v", matrix[0][0])
		}

		// CSV version
		wCSV := doGet("/api/v2/accounts/"+accIDStr+"/reports/conversation_traffic?format=csv", true)
		if wCSV.Code != http.StatusOK {
			t.Fatalf("expected 200 CSV, got %d", wCSV.Code)
		}
		if !strings.Contains(wCSV.Body.String(), "Start of the hour") {
			t.Errorf("expected CSV header in traffic export")
		}
	})

	// ==========================================
	// 4. Drilldown
	// ==========================================
	t.Run("4_Drilldown", func(t *testing.T) {
		bucketTs := strconv.FormatInt(now.Unix(), 10)
		path := fmt.Sprintf("/api/v2/accounts/%s/reports/drilldown?metric=conversations_count&bucket_timestamp=%s&type=account", accIDStr, bucketTs)
		w := doGet(path, false)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var drilldown struct {
			Meta struct {
				Metric     string `json:"metric"`
				TotalCount int64  `json:"total_count"`
			} `json:"meta"`
			Payload []any `json:"payload"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &drilldown); err != nil {
			t.Fatalf("failed to decode drilldown: %v", err)
		}
		if drilldown.Meta.Metric != "conversations_count" || drilldown.Meta.TotalCount == 0 {
			t.Errorf("unexpected drilldown result: %+v", drilldown.Meta)
		}
	})

	// ==========================================
	// 5. Channel Summary
	// ==========================================
	t.Run("5_Channel_Summary", func(t *testing.T) {
		// Test both /reports/channel_summary and /summary_reports/channel
		paths := []string{
			"/api/v2/accounts/" + accIDStr + "/reports/channel_summary",
			"/api/v2/accounts/" + accIDStr + "/summary_reports/channel",
		}
		for _, p := range paths {
			w := doGet(p, false)
			if w.Code != http.StatusOK {
				t.Fatalf("path %s expected 200, got %d: %s", p, w.Code, w.Body.String())
			}
			var chSummary map[string]struct {
				Open     int64 `json:"open"`
				Resolved int64 `json:"resolved"`
				Total    int64 `json:"total"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &chSummary)
			if webStats, ok := chSummary["Channel::WebWidget"]; !ok || webStats.Open < 2 {
				t.Errorf("expected Channel::WebWidget stats, got %+v", chSummary)
			}
		}
	})

	// ==========================================
	// 6. Bot Summary
	// ==========================================
	t.Run("6_Bot_Summary", func(t *testing.T) {
		w := doGet("/api/v2/accounts/"+accIDStr+"/reports/bot_summary", false)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var botSummary struct {
			BotResolutionsCount int64 `json:"bot_resolutions_count"`
			BotHandoffsCount    int64 `json:"bot_handoffs_count"`
			Previous            struct {
				BotResolutionsCount int64 `json:"bot_resolutions_count"`
				BotHandoffsCount    int64 `json:"bot_handoffs_count"`
			} `json:"previous"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &botSummary)
		if botSummary.BotResolutionsCount < 1 {
			t.Errorf("expected at least 1 bot resolution, got %d", botSummary.BotResolutionsCount)
		}
		if botSummary.BotHandoffsCount < 1 {
			t.Errorf("expected at least 1 bot handoff, got %d", botSummary.BotHandoffsCount)
		}
	})

	// ==========================================
	// 7. Bot Metrics
	// ==========================================
	t.Run("7_Bot_Metrics", func(t *testing.T) {
		w := doGet("/api/v2/accounts/"+accIDStr+"/reports/bot_metrics", false)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var botMetrics struct {
			ConversationCount int64   `json:"conversation_count"`
			ResolutionRate    float64 `json:"resolution_rate"`
			HandoffRate       float64 `json:"handoff_rate"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &botMetrics)
		if botMetrics.ConversationCount == 0 {
			t.Errorf("expected bot conversations count > 0")
		}
	})

	// ==========================================
	// 8. Inbox-Label Matrix
	// ==========================================
	t.Run("8_Inbox_Label_Matrix", func(t *testing.T) {
		w := doGet("/api/v2/accounts/"+accIDStr+"/reports/inbox_label_matrix", false)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var matrixResp struct {
			Inboxes []struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
			} `json:"inboxes"`
			Labels []struct {
				ID    uint   `json:"id"`
				Title string `json:"title"`
			} `json:"labels"`
			Matrix [][]int64 `json:"matrix"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &matrixResp)
		if len(matrixResp.Inboxes) == 0 || len(matrixResp.Labels) == 0 {
			t.Errorf("expected populated inboxes and labels in matrix")
		}
		if len(matrixResp.Matrix) != len(matrixResp.Inboxes) {
			t.Errorf("expected matrix row count %d, got %d", len(matrixResp.Inboxes), len(matrixResp.Matrix))
		}
	})

	// ==========================================
	// 9. Outgoing Message Count
	// ==========================================
	t.Run("9_Outgoing_Message_Count", func(t *testing.T) {
		for _, grp := range []string{"agent", "inbox"} {
			w := doGet("/api/v2/accounts/"+accIDStr+"/reports/outgoing_messages_count?group_by="+grp, false)
			if w.Code != http.StatusOK {
				t.Fatalf("group_by=%s expected 200, got %d: %s", grp, w.Code, w.Body.String())
			}
			var items []struct {
				ID                    uint   `json:"id"`
				Name                  string `json:"name"`
				OutgoingMessagesCount int64  `json:"outgoing_messages_count"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &items)
			if len(items) == 0 {
				t.Errorf("group_by=%s expected items, got 0", grp)
			}
		}
	})

	// ==========================================
	// 10. Live Conversation Metrics
	// ==========================================
	t.Run("10_Live_Conversation_Metrics", func(t *testing.T) {
		paths := []string{
			"/api/v2/accounts/" + accIDStr + "/live_reports/conversation_metrics",
			"/api/v2/accounts/" + accIDStr + "/reports/live_conversation_metrics",
		}
		for _, p := range paths {
			w := doGet(p, false)
			if w.Code != http.StatusOK {
				t.Fatalf("path %s expected 200, got %d: %s", p, w.Code, w.Body.String())
			}
			var live struct {
				Open       int64 `json:"open"`
				Unassigned int64 `json:"unassigned"`
				Unattended int64 `json:"unattended"`
				Pending    int64 `json:"pending"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &live)
			if live.Open < 2 {
				t.Errorf("expected live open >= 2, got %d", live.Open)
			}
		}
	})

	// ==========================================
	// 11. Grouped Live Metrics
	// ==========================================
	t.Run("11_Grouped_Live_Metrics", func(t *testing.T) {
		paths := []string{
			"/api/v2/accounts/" + accIDStr + "/live_reports/grouped_conversation_metrics?group_by=assignee_id",
			"/api/v2/accounts/" + accIDStr + "/reports/grouped_live_metrics?group_by=assignee_id",
		}
		for _, p := range paths {
			w := doGet(p, false)
			if w.Code != http.StatusOK {
				t.Fatalf("path %s expected 200, got %d: %s", p, w.Code, w.Body.String())
			}
			var items []struct {
				Open       int64 `json:"open"`
				AssigneeID *uint `json:"assignee_id"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &items)
			if len(items) == 0 {
				t.Errorf("expected grouped live metric items")
			}
		}
	})

	// ==========================================
	// 12. Year In Review
	// ==========================================
	t.Run("12_Year_In_Review", func(t *testing.T) {
		currYear := strconv.Itoa(now.Year())
		paths := []string{
			"/api/v2/accounts/" + accIDStr + "/year_in_review?year=" + currYear,
			"/api/v2/accounts/" + accIDStr + "/reports/year_in_review?year=" + currYear,
		}
		for _, p := range paths {
			w := doGet(p, false)
			if w.Code != http.StatusOK {
				t.Fatalf("path %s expected 200, got %d: %s", p, w.Code, w.Body.String())
			}
			var review struct {
				Year               int   `json:"year"`
				TotalConversations int64 `json:"total_conversations"`
				SupportPersonality struct {
					AvgResponseTimeSeconds int `json:"avg_response_time_seconds"`
				} `json:"support_personality"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &review)
			if review.Year != now.Year() {
				t.Errorf("expected year %d, got %d", now.Year(), review.Year)
			}
		}
	})

	// ==========================================
	// 13. Summary Reports V2 Endpoints
	// ==========================================
	t.Run("13_Summary_Reports_V2_Collection", func(t *testing.T) {
		for _, dim := range []string{"agent", "team", "inbox", "label"} {
			w := doGet("/api/v2/accounts/"+accIDStr+"/summary_reports/"+dim, false)
			if w.Code != http.StatusOK {
				t.Fatalf("/summary_reports/%s expected 200, got %d: %s", dim, w.Code, w.Body.String())
			}
			var resp struct {
				Success bool `json:"success"`
				Data    any  `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if !resp.Success {
				t.Errorf("expected success for /summary_reports/%s", dim)
			}
		}
	})
}
