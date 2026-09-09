package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestReport_Consistency_Verification(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_report_consistency_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Seed base Account & Users
	account := domain.Account{Name: "Consistency Corp"}
	db.Create(&account)
	accStr := strconv.Itoa(int(account.ID))

	admin := domain.User{
		Email:        "admin@consistency.com",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuvwxyz123456",
		Name:         "Admin Alice",
		Role:         domain.RoleAdministrator,
	}
	db.Create(&admin)

	agentBob := domain.User{
		Email:        "bob@consistency.com",
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuvwxyz123456",
		Name:         "Agent Bob",
		Role:         domain.RoleAgent,
	}
	db.Create(&agentBob)

	db.Create(&domain.AccountUser{AccountID: account.ID, UserID: admin.ID, Role: domain.RoleAdministrator, Availability: "online"})
	db.Create(&domain.AccountUser{AccountID: account.ID, UserID: agentBob.ID, Role: domain.RoleAgent, Availability: "online"})

	token, _ := auth.GenerateToken(&admin, cfg.JWTSecret, 24)
	authHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"Content-Type":  "application/json",
	}

	doReq := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, nil)
		for k, v := range authHeaders {
			req.Header.Set(k, v)
		}
		r.ServeHTTP(rec, req)
		return rec
	}

	// 2. Setup 2 Inboxes: Inbox1 (WebWidget with Mon-Fri 09:00-18:00), Inbox2 (Email 24/7)
	inbox1 := domain.Inbox{
		AccountID:           account.ID,
		Name:                "Web Widget Support",
		ChannelType:         domain.ChannelWebWidget,
		WebsiteToken:        "tok_inbox1_consistency",
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
	db.Create(&inbox1)

	inbox2 := domain.Inbox{
		AccountID:           account.ID,
		Name:                "Email Channel Support",
		ChannelType:         domain.ChannelEmail,
		WebsiteToken:        "tok_inbox2_consistency",
		Timezone:            "UTC",
		WorkingHoursEnabled: false,
	}
	db.Create(&inbox2)

	// 3. Setup Team and Labels
	team := domain.Team{AccountID: account.ID, Name: "Core Support Team"}
	db.Create(&team)
	db.Create(&domain.TeamMember{TeamID: team.ID, UserID: agentBob.ID})

	labelVIP := domain.Label{AccountID: account.ID, Title: "VIP", Color: "#FF0000"}
	db.Create(&labelVIP)
	labelBug := domain.Label{AccountID: account.ID, Title: "Bug", Color: "#00FF00"}
	db.Create(&labelBug)

	contact := domain.Contact{AccountID: account.ID, Name: "Tester", Email: "test@test.com"}
	db.Create(&contact)

	// 4. Seed 5 Conversations across different dates, business hours, and dimensions:
	// Target testing window: 2026-09-02 00:00:00 to 2026-09-08 23:59:59 UTC (7 days)
	tWedOpen := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)  // Wed 10:00 (In Business Hours)
	tThuOpen := time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)  // Thu 14:00 (In Business Hours)
	tFriOff := time.Date(2026, 9, 4, 21, 0, 0, 0, time.UTC)   // Fri 21:00 (Outside Business Hours for Inbox1)
	tMonOpen := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)  // Mon 11:00 (In Business Hours)
	tPastOld := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC) // Past old outside time range!

	// Conv 1: Inbox 1, Assigned to Alice, Open, In BH, Labels: VIP & Bug
	conv1 := domain.Conversation{
		AccountID: account.ID, InboxID: inbox1.ID, ContactID: contact.ID,
		AssigneeID: &admin.ID, Status: domain.ConversationStatusOpen, CreatedAt: tWedOpen,
	}
	db.Create(&conv1)
	db.Create(&domain.ConversationLabel{ConversationID: conv1.ID, LabelID: labelVIP.ID})
	db.Create(&domain.ConversationLabel{ConversationID: conv1.ID, LabelID: labelBug.ID})

	// Conv 2: Inbox 1, Assigned to Bob, Resolved, In BH, Team assigned, Label: VIP
	conv2 := domain.Conversation{
		AccountID: account.ID, InboxID: inbox1.ID, ContactID: contact.ID,
		AssigneeID: &agentBob.ID, TeamID: &team.ID, Status: domain.ConversationStatusResolved, CreatedAt: tThuOpen, UpdatedAt: tThuOpen.Add(30 * time.Minute),
	}
	db.Create(&conv2)
	db.Create(&domain.ConversationLabel{ConversationID: conv2.ID, LabelID: labelVIP.ID})
	// Message with reply for FRT
	mIn := domain.Message{AccountID: account.ID, ConversationID: conv2.ID, SenderType: "Contact", SenderID: contact.ID, MessageType: "incoming", CreatedAt: tThuOpen}
	mOut := domain.Message{AccountID: account.ID, ConversationID: conv2.ID, SenderType: "User", SenderID: agentBob.ID, MessageType: "outgoing", CreatedAt: tThuOpen.Add(10 * time.Minute)}
	db.Create(&mIn)
	db.Create(&mOut)

	// Conv 3: Inbox 1, Unassigned, Pending, Outside BH (Fri 21:00)
	conv3 := domain.Conversation{
		AccountID: account.ID, InboxID: inbox1.ID, ContactID: contact.ID,
		AssigneeID: nil, Status: domain.ConversationStatusPending, CreatedAt: tFriOff,
	}
	db.Create(&conv3)

	// Conv 4: Inbox 2 (Email), Assigned to Bob, Resolved, In BH, Label: Bug
	conv4 := domain.Conversation{
		AccountID: account.ID, InboxID: inbox2.ID, ContactID: contact.ID,
		AssigneeID: &agentBob.ID, Status: domain.ConversationStatusResolved, CreatedAt: tMonOpen, UpdatedAt: tMonOpen.Add(time.Hour),
	}
	db.Create(&conv4)
	db.Create(&domain.ConversationLabel{ConversationID: conv4.ID, LabelID: labelBug.ID})

	// Conv 5: Historical outside time window (August 15)
	conv5 := domain.Conversation{
		AccountID: account.ID, InboxID: inbox1.ID, ContactID: contact.ID,
		AssigneeID: &admin.ID, Status: domain.ConversationStatusResolved, CreatedAt: tPastOld,
	}
	db.Create(&conv5)

	timeWindowQuery := "since=2026-09-01&until=2026-09-08"

	// -------------------------------------------------------------
	// 1. 测试时间范围一致性 (Time Range Consistency)
	// -------------------------------------------------------------
	t.Run("1_Time_Range_Consistency", func(t *testing.T) {
		resSum := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/summary?%s", accStr, timeWindowQuery))
		var sumResp struct{ Data service.AccountSummaryReport `json:"data"` }
		_ = json.Unmarshal(resSum.Body.Bytes(), &sumResp)

		// Conversations 1, 2, 3, 4 fall in window; conv 5 is outside. Total must be 4.
		if sumResp.Data.TotalConversations != 4 {
			t.Fatalf("expected 4 conversations in time window, got %d", sumResp.Data.TotalConversations)
		}

		// Also check with unix timestamp
		sinceSec := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Unix()
		untilSec := time.Date(2026, 9, 8, 23, 59, 59, 0, time.UTC).Unix()
		resUnix := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/summary?since=%d&until=%d", accStr, sinceSec, untilSec))
		var unixResp struct{ Data service.AccountSummaryReport `json:"data"` }
		_ = json.Unmarshal(resUnix.Body.Bytes(), &unixResp)
		if unixResp.Data.TotalConversations != 4 {
			t.Fatalf("expected 4 conversations with unix timestamp filter, got %d", unixResp.Data.TotalConversations)
		}
	})

	// -------------------------------------------------------------
	// 2. 测试营业时间一致性 (Business Hours Consistency)
	// -------------------------------------------------------------
	t.Run("2_Business_Hours_Consistency", func(t *testing.T) {
		// Conv 3 was created on Fri 21:00 (outside Inbox1 business hours 09:00-18:00)
		// Convs 1, 2 (Inbox 1) and Conv 4 (Inbox 2) are in business hours -> exactly 3 conversations.
		resBH := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/summary?%s&business_hours=true", accStr, timeWindowQuery))
		var sumBH struct{ Data service.AccountSummaryReport `json:"data"` }
		_ = json.Unmarshal(resBH.Body.Bytes(), &sumBH)

		if sumBH.Data.TotalConversations != 3 {
			t.Fatalf("expected 3 conversations within business hours, got %d", sumBH.Data.TotalConversations)
		}

		// Trends with business_hours=true must also equal 3
		resTrendsBH := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/trends?%s&business_hours=true", accStr, timeWindowQuery))
		var trendsBH struct{ Data service.ConversationTrendsReport `json:"data"` }
		_ = json.Unmarshal(resTrendsBH.Body.Bytes(), &trendsBH)
		if trendsBH.Data.CurrentPeriodTotal != 3 {
			t.Fatalf("expected trends CurrentPeriodTotal to be 3 with business hours, got %d", trendsBH.Data.CurrentPeriodTotal)
		}
	})

	// -------------------------------------------------------------
	// 3. 测试图表与总数一致性 (Charts vs Total Count Consistency)
	// -------------------------------------------------------------
	t.Run("3_Charts_vs_Totals_Consistency", func(t *testing.T) {
		resTrends := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/trends?%s", accStr, timeWindowQuery))
		var trendsResp struct{ Data service.ConversationTrendsReport `json:"data"` }
		_ = json.Unmarshal(resTrends.Body.Bytes(), &trendsResp)

		var sumChartPoints int64
		for _, pt := range trendsResp.Data.Trends {
			sumChartPoints += pt.Count
		}

		// 1) The sum of daily data points in the chart MUST exactly equal CurrentPeriodTotal
		if sumChartPoints != trendsResp.Data.CurrentPeriodTotal {
			t.Fatalf("chart points sum %d does not match CurrentPeriodTotal %d", sumChartPoints, trendsResp.Data.CurrentPeriodTotal)
		}

		// 2) Trends total MUST equal Summary TotalConversations for the same period
		resSum := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/summary?%s", accStr, timeWindowQuery))
		var sumResp struct{ Data service.AccountSummaryReport `json:"data"` }
		_ = json.Unmarshal(resSum.Body.Bytes(), &sumResp)

		if trendsResp.Data.CurrentPeriodTotal != sumResp.Data.TotalConversations {
			t.Fatalf("trends total %d does not match summary total %d", trendsResp.Data.CurrentPeriodTotal, sumResp.Data.TotalConversations)
		}
	})

	// -------------------------------------------------------------
	// 4. 测试各维度统计一致性 (Cross-Dimension Statistics Consistency)
	// -------------------------------------------------------------
	t.Run("4_Cross_Dimensions_Consistency", func(t *testing.T) {
		// A. 收件箱维度 (Inboxes)
		resInb := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/inboxes?%s", accStr, timeWindowQuery))
		var inbResp struct{ Data []service.InboxMetric `json:"data"` }
		_ = json.Unmarshal(resInb.Body.Bytes(), &inbResp)

		var sumInboxTotal int64
		for _, m := range inbResp.Data {
			sumInboxTotal += m.TotalConversations
			// Check internal consistency: total == open + resolved + pending
			if m.TotalConversations != (m.OpenConversations + m.ResolvedConversations + m.PendingConversations) {
				t.Fatalf("inbox %s internal mismatch: total=%d != open(%d)+resolved(%d)+pending(%d)",
					m.Name, m.TotalConversations, m.OpenConversations, m.ResolvedConversations, m.PendingConversations)
			}
		}

		// Sum of inboxes MUST equal summary TotalConversations (4)
		if sumInboxTotal != 4 {
			t.Fatalf("expected sum of inbox totals to be 4, got %d", sumInboxTotal)
		}

		// B. 坐席维度 (Agents)
		resAgents := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/agents?%s", accStr, timeWindowQuery))
		var agentResp struct{ Data []service.AgentMetric `json:"data"` }
		_ = json.Unmarshal(resAgents.Body.Bytes(), &agentResp)

		var sumAgentAssigned int64
		for _, a := range agentResp.Data {
			sumAgentAssigned += a.AssignedConversations
		}

		// Alice has 1 (conv1), Bob has 2 (conv2, conv4) = 3 assigned.
		// Conv3 is unassigned (1).
		// Assigned (3) + Unassigned (1) == Total (4)
		if sumAgentAssigned != 3 {
			t.Fatalf("expected sum of agent assigned conversations to be 3, got %d", sumAgentAssigned)
		}

		// C. 团队维度 (Teams)
		resTeams := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/teams?%s", accStr, timeWindowQuery))
		var teamResp struct{ Data []service.TeamMetric `json:"data"` }
		_ = json.Unmarshal(resTeams.Body.Bytes(), &teamResp)

		if len(teamResp.Data) > 0 {
			// Bob is a member of team and has conv2 (team assigned) and conv4 (assigned to Bob)
			teamMetric := teamResp.Data[0]
			if teamMetric.AssignedConversations < 1 {
				t.Fatalf("expected team assigned conversations >= 1, got %d", teamMetric.AssignedConversations)
			}
		}

		// D. 标签维度 (Labels)
		resLabels := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/labels?%s", accStr, timeWindowQuery))
		var labelResp struct{ Data []service.LabelMetric `json:"data"` }
		_ = json.Unmarshal(resLabels.Body.Bytes(), &labelResp)

		// VIP label is on conv1 and conv2 (2)
		// Bug label is on conv1 and conv4 (2)
		for _, l := range labelResp.Data {
			if l.Title == "VIP" && l.ConversationCount != 2 {
				t.Errorf("expected VIP label count to be 2, got %d", l.ConversationCount)
			}
			if l.Title == "Bug" && l.ConversationCount != 2 {
				t.Errorf("expected Bug label count to be 2, got %d", l.ConversationCount)
			}
		}

		// E. 首响分布维度 (FRT Distribution)
		resFRT := doReq(http.MethodGet, fmt.Sprintf("/api/v2/accounts/%s/reports/first_response_time_distribution?%s", accStr, timeWindowQuery))
		var frtResp struct{ Data service.FirstResponseDistributionReport `json:"data"` }
		_ = json.Unmarshal(resFRT.Body.Bytes(), &frtResp)

		var channelSumBuckets int64
		for _, ch := range frtResp.Data.Channels {
			b := ch.Distribution
			channelSumBuckets += (b.ZeroToOneHour + b.OneToFourHours + b.FourToEightHours + b.EightToTwentyFour + b.TwentyFourHoursPlus)
		}
		totalBuckets := frtResp.Data.Total.ZeroToOneHour + frtResp.Data.Total.OneToFourHours +
			frtResp.Data.Total.FourToEightHours + frtResp.Data.Total.EightToTwentyFour + frtResp.Data.Total.TwentyFourHoursPlus

		// Total distribution MUST equal the sum of channel distribution buckets
		if totalBuckets != channelSumBuckets {
			t.Fatalf("FRT Total buckets %d does not match channel sum %d", totalBuckets, channelSumBuckets)
		}
	})
}
