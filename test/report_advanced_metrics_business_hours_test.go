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
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestReportAdvancedMetrics_BusinessHours(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_report_bh_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]any{
		"account_name": "Report Analytics Corp",
		"name":         "BI Lead",
		"email":        "bi.lead@analytics.com",
		"password":     "SecretPass123!",
	}
	body, _ := json.Marshal(signUpPayload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign_up expected 201, got %d: %s", w.Code, w.Body.String())
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
	adminUser := authResp.Data.User
	accID := authResp.Data.Accounts[0].ID
	accStr := strconv.Itoa(int(accID))

	authHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"Content-Type":  "application/json",
	}

	doReq := func(method, path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request, _ := http.NewRequest(method, path, nil)
		for k, v := range authHeaders {
			request.Header.Set(k, v)
		}
		r.ServeHTTP(recorder, request)
		return recorder
	}

	// -------------------------------------------------------------
	// Scenario 1: 真实收件箱非工作日/自定义时区营业时间过滤
	// -------------------------------------------------------------
	t.Run("Scenario1_Dynamic_Inbox_Business_Hours_Filtering", func(t *testing.T) {
		// Custom working hours: Tuesday to Saturday (day 2 to 6) 10:00 to 19:00 in "Asia/Shanghai"
		// Sunday (0) and Monday (1) are closed.
		workingHoursConfig := `[
			{"day_of_week": 0, "closed": true},
			{"day_of_week": 1, "closed": true},
			{"day_of_week": 2, "closed": false, "open_hour": 10, "open_minute": 0, "close_hour": 19, "close_minute": 0},
			{"day_of_week": 3, "closed": false, "open_hour": 10, "open_minute": 0, "close_hour": 19, "close_minute": 0},
			{"day_of_week": 4, "closed": false, "open_hour": 10, "open_minute": 0, "close_hour": 19, "close_minute": 0},
			{"day_of_week": 5, "closed": false, "open_hour": 10, "open_minute": 0, "close_hour": 19, "close_minute": 0},
			{"day_of_week": 6, "closed": false, "open_hour": 10, "open_minute": 0, "close_hour": 19, "close_minute": 0}
		]`

		inboxCST := domain.Inbox{
			AccountID:           accID,
			Name:                "上海旗舰店客服",
			ChannelType:         "Channel::WebWidget",
			WebsiteToken:        "tok_shanghai_cst",
			Timezone:            "Asia/Shanghai",
			WorkingHoursEnabled: true,
			WorkingHours:        workingHoursConfig,
		}
		db.Create(&inboxCST)

		contact := domain.Contact{
			AccountID: accID,
			Name:      "上海用户",
			Email:     "shanghai.user@test.com",
		}
		db.Create(&contact)

		// 1. Monday 11:00 CST (2026-09-07 03:00 UTC) -> Monday is CLOSED for this inbox!
		convMonday := domain.Conversation{
			AccountID: accID,
			InboxID:   inboxCST.ID,
			ContactID: contact.ID,
			Status:    "open",
			CreatedAt: time.Date(2026, 9, 7, 3, 0, 0, 0, time.UTC),
		}
		db.Create(&convMonday)

		// 2. Wednesday 11:00 CST (2026-09-09 03:00 UTC) -> Wednesday is OPEN (10:00-19:00), so within business hours!
		convWednesday := domain.Conversation{
			AccountID: accID,
			InboxID:   inboxCST.ID,
			ContactID: contact.ID,
			Status:    "open",
			CreatedAt: time.Date(2026, 9, 9, 3, 0, 0, 0, time.UTC),
		}
		db.Create(&convWednesday)

		// Query summary with business_hours=true
		res := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/summary?business_hours=true", accStr))
		if res.Code != http.StatusOK {
			t.Fatalf("get summary failed: code=%d, body=%s", res.Code, res.Body.String())
		}

		var summaryResp struct {
			Data service.AccountSummaryReport `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &summaryResp)

		// Under accurate inbox business hours, the Monday conversation is excluded and Wednesday is included.
		if summaryResp.Data.TotalConversations != 1 {
			t.Errorf("expected exactly 1 conversation within custom business hours, got %d", summaryResp.Data.TotalConversations)
		}

		// Query summary with business_hours=false (both Monday and Wednesday counted)
		resAll := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/summary?business_hours=false", accStr))
		_ = json.Unmarshal(resAll.Body.Bytes(), &summaryResp)
		if summaryResp.Data.TotalConversations != 2 {
			t.Errorf("expected 2 conversations without business hours filter, got %d", summaryResp.Data.TotalConversations)
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: 营业时间下首次回复时长 (FRT) 与五档分布
	// -------------------------------------------------------------
	t.Run("Scenario2_Business_Hours_FRT_Calculation", func(t *testing.T) {
		// Inbox with standard Mon-Fri 09:00-18:00 in UTC
		inboxStandard := domain.Inbox{
			AccountID:           accID,
			Name:                "标准UTC客服",
			ChannelType:         "Channel::Email",
			WebsiteToken:        "tok_standard_utc",
			Timezone:            "UTC",
			WorkingHoursEnabled: true,
			WorkingHours: `[
				{"day_of_week": 0, "closed": true},
				{"day_of_week": 1, "closed": false, "open_hour": 9, "close_hour": 18},
				{"day_of_week": 2, "closed": false, "open_hour": 9, "close_hour": 18},
				{"day_of_week": 3, "closed": false, "open_hour": 9, "close_hour": 18},
				{"day_of_week": 4, "closed": false, "open_hour": 9, "close_hour": 18},
				{"day_of_week": 5, "closed": false, "open_hour": 9, "close_hour": 18},
				{"day_of_week": 6, "closed": true}
			]`,
		}
		db.Create(&inboxStandard)

		contact := domain.Contact{
			AccountID: accID,
			Name:      "跨周末客户",
			Email:     "weekend.test@test.com",
		}
		db.Create(&contact)

		// User writes on Friday 17:50 UTC (2026-09-04 17:50 UTC)
		convWeekend := domain.Conversation{
			AccountID: accID,
			InboxID:   inboxStandard.ID,
			ContactID: contact.ID,
			Status:    "resolved",
			CreatedAt: time.Date(2026, 9, 4, 17, 50, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 9, 7, 9, 10, 0, 0, time.UTC),
		}
		db.Create(&convWeekend)

		msgIn := domain.Message{
			AccountID:      accID,
			ConversationID: convWeekend.ID,
			SenderType:     "Contact",
			SenderID:       contact.ID,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "周五傍晚咨询退款问题",
			CreatedAt:      time.Date(2026, 9, 4, 17, 50, 0, 0, time.UTC),
		}
		db.Create(&msgIn)

		// Agent replies on Monday 09:10 UTC (2026-09-07 09:10 UTC)
		msgOut := domain.Message{
			AccountID:      accID,
			ConversationID: convWeekend.ID,
			SenderType:     "User",
			SenderID:       adminUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			Content:        "周一一早为您办理退款成功",
			CreatedAt:      time.Date(2026, 9, 7, 9, 10, 0, 0, time.UTC),
		}
		db.Create(&msgOut)

		// 1. With business_hours=true:
		// Fri 17:50-18:00 (10 min) + Mon 09:00-09:10 (10 min) = 20 minutes (1200 seconds)!
		// MUST fall into 0_to_1h (and under_1h)!
		resBH := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/first_response_time_distribution?business_hours=true", accStr))
		if resBH.Code != http.StatusOK {
			t.Fatalf("get distribution failed: code=%d, body=%s", resBH.Code, resBH.Body.String())
		}
		var distBH struct {
			Data service.FirstResponseDistributionReport `json:"data"`
		}
		_ = json.Unmarshal(resBH.Body.Bytes(), &distBH)

		if distBH.Data.Total.ZeroToOneHour <= 0 {
			t.Errorf("expected 0_to_1h bucket to contain the 20-minute business response, got: %+v", distBH.Data.Total)
		}
		if distBH.Data.Total.TwentyFourHoursPlus != 0 {
			t.Errorf("expected 24h_plus bucket to be 0 under business_hours=true, got %d", distBH.Data.Total.TwentyFourHoursPlus)
		}

		// 2. With business_hours=false:
		// Natural elapsed time is ~63 hours -> MUST fall into 24h_plus!
		resRaw := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/first_response_time_distribution?business_hours=false", accStr))
		var distRaw struct {
			Data service.FirstResponseDistributionReport `json:"data"`
		}
		_ = json.Unmarshal(resRaw.Body.Bytes(), &distRaw)

		if distRaw.Data.Total.TwentyFourHoursPlus <= 0 {
			t.Errorf("expected 24h_plus bucket under raw elapsed time to be > 0, got: %+v", distRaw.Data.Total)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: AccountSummaryReport 高级指标全面核算
	// -------------------------------------------------------------
	t.Run("Scenario3_AccountSummary_Advanced_Metrics", func(t *testing.T) {
		res := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/summary", accStr))
		if res.Code != http.StatusOK {
			t.Fatalf("get summary failed: code=%d", res.Code)
		}

		var summaryResp struct {
			Data service.AccountSummaryReport `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &summaryResp)

		data := summaryResp.Data
		if data.ConversationsCount <= 0 {
			t.Errorf("expected conversations_count > 0, got %d", data.ConversationsCount)
		}
		if data.IncomingMessagesCount <= 0 {
			t.Errorf("expected incoming_messages_count > 0, got %d", data.IncomingMessagesCount)
		}
		if data.OutgoingMessagesCount <= 0 {
			t.Errorf("expected outgoing_messages_count > 0, got %d", data.OutgoingMessagesCount)
		}
		if data.ResolutionRate <= 0 {
			t.Errorf("expected positive resolution_rate, got %.1f", data.ResolutionRate)
		}
		if data.AvgFirstResponseTime <= 0 {
			t.Errorf("expected positive avg_first_response_time, got %.1f", data.AvgFirstResponseTime)
		}
		if data.FirstContactResolutionRate <= 0 {
			t.Errorf("expected positive first_contact_resolution_rate, got %.1f", data.FirstContactResolutionRate)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: 多维度报表（坐席、团队、收件箱、标签）高级指标核算
	// -------------------------------------------------------------
	t.Run("Scenario4_Dimensions_Advanced_Metrics", func(t *testing.T) {
		// 1. Agents Report
		resAgents := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/agents", accStr))
		if resAgents.Code != http.StatusOK {
			t.Fatalf("get agents failed: code=%d", resAgents.Code)
		}
		var agentsResp struct {
			Data []service.AgentMetric `json:"data"`
		}
		_ = json.Unmarshal(resAgents.Body.Bytes(), &agentsResp)
		if len(agentsResp.Data) == 0 {
			t.Fatalf("expected at least 1 agent metric")
		}
		agent := agentsResp.Data[0]
		if agent.TotalMessagesSent <= 0 {
			t.Errorf("expected agent total_messages_sent > 0, got %d", agent.TotalMessagesSent)
		}

		// 2. Inboxes Report
		resInboxes := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/inboxes", accStr))
		if resInboxes.Code != http.StatusOK {
			t.Fatalf("get inboxes failed: code=%d", resInboxes.Code)
		}
		var inboxesResp struct {
			Data []service.InboxMetric `json:"data"`
		}
		_ = json.Unmarshal(resInboxes.Body.Bytes(), &inboxesResp)
		if len(inboxesResp.Data) == 0 {
			t.Fatalf("expected inbox metrics")
		}

		// 3. Teams Report
		resTeams := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/teams", accStr))
		if resTeams.Code != http.StatusOK {
			t.Fatalf("get teams failed: code=%d", resTeams.Code)
		}

		// 4. Labels Report
		resLabels := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/labels", accStr))
		if resLabels.Code != http.StatusOK {
			t.Fatalf("get labels failed: code=%d", resLabels.Code)
		}
	})
}
