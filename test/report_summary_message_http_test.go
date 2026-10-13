package test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportSummaryMessageHTTP(t *testing.T) {
	cfg := reportSummaryMessageHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "summary-messages.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runReportSummaryMessageHTTP(t, cfg, db)
}

func reportSummaryMessageHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "summary-message-http-test-secret-12345678", JWTExpirationHours: 24}
}

// Both engines exercise the native router, current account membership and owner
// repositories. In particular, the message fixture never belongs to a newly
// created conversation until the final business-hours compatibility case.
func runReportSummaryMessageHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	day := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	end := day.AddDate(0, 0, 1)
	for _, accountID := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: accountID, Name: "Summary account"}).Error)
		require.NoError(t, db.Create(&domain.Contact{ID: accountID, AccountID: accountID, Name: "Summary contact", CreatedAt: day.AddDate(0, 0, -2)}).Error)
		require.NoError(t, db.Create(&domain.Inbox{ID: accountID, AccountID: accountID, Name: "Summary inbox", WebsiteToken: fmt.Sprint("summary-message-", accountID), Timezone: "UTC", WorkingHoursEnabled: true, WorkingHours: `[{"day_of_week":4,"closed":false,"open_hour":9,"open_minutes":0,"close_hour":17,"close_minutes":0}]`}).Error)
	}
	require.NoError(t, db.Create(&domain.Inbox{ID: 93, AccountID: 91, Name: "Closed inbox", WebsiteToken: "summary-message-closed", Timezone: "UTC", WorkingHoursEnabled: true, WorkingHours: `[{"day_of_week":4,"closed":true}]`}).Error)
	tokens := map[string]string{}
	for _, principal := range []string{"admin", "agent", "report_view", "report_manage", "foreign"} {
		user := domain.User{Name: principal, Email: principal + "@summary-message.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		membership := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		switch principal {
		case "admin":
			membership.Role = domain.RoleAdministrator
		case "foreign":
			membership.AccountID, membership.Role = 92, domain.RoleAdministrator
		case "report_view", "report_manage":
			role := domain.CustomRole{AccountID: 91, Name: principal, Permissions: `["` + principal + `"]`}
			require.NoError(t, db.Create(&role).Error)
			membership.CustomRoleID = &role.ID
		}
		require.NoError(t, db.Create(&membership).Error)
		var err error
		tokens[principal], err = auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	addConversation := func(id, accountID, inboxID uint, at time.Time) {
		t.Helper()
		require.NoError(t, db.Create(&domain.Conversation{ID: id, DisplayID: id, AccountID: accountID, InboxID: inboxID, ContactID: accountID, Status: domain.ConversationStatusOpen, CreatedAt: at, UpdatedAt: at, LastActivityAt: at}).Error)
	}
	addConversation(101, 91, 91, day.AddDate(0, 0, -500))
	addConversation(102, 91, 91, day.AddDate(0, 0, 3))
	addConversation(900, 92, 92, day)
	for _, row := range []struct {
		at                   time.Time
		kind, sender, status string
		private              bool
	}{
		{day.AddDate(0, 0, -450), "outgoing", "User", "sent", false},
		{day.Add(-time.Microsecond), "outgoing", "User", "sent", false},
		{day, "incoming", "Contact", "saved", false},
		{day.Add(time.Microsecond), "outgoing", "User", "sent", false},
		{day.Add(time.Hour), "incoming", "Contact", "sent", false},
		{day.Add(2 * time.Hour), "outgoing", "User", "saved", true},
		{day.Add(3 * time.Hour), "outgoing", "AgentBot", "failed", false},
		{day.Add(4 * time.Hour), "outgoing", "User", "queued", false},
		{day.Add(5 * time.Hour), "activity", "User", "saved", false},
		{day.Add(6 * time.Hour), "template", "User", "sent", false},
		{day.Add(7 * time.Hour), "Outgoing", "User", "sent", false},
		{day.Add(8 * time.Hour), "incoming ", "Contact", "sent", false},
		{day.Add(12 * time.Hour), "incoming", "Contact", "sent", false},
		{end.Add(-time.Microsecond), "outgoing", "User", "sent", false},
		{end, "outgoing", "User", "sent", false},
		{day.AddDate(0, 0, 2), "incoming", "Contact", "sent", false},
	} {
		require.NoError(t, db.Create(&domain.Message{AccountID: 91, ConversationID: 101, SenderID: 1, MessageType: row.kind, SenderType: row.sender, Status: row.status, Private: row.private, Content: "private-summary-message-body", ContentAttributes: `{"private":"summary-metadata"}`, CreatedAt: row.at}).Error)
	}
	// Even a foreign row pointing at an owned conversation must stay excluded.
	for _, conversationID := range []uint{900, 101} {
		require.NoError(t, db.Create(&domain.Message{AccountID: 92, ConversationID: conversationID, SenderID: 1, SenderType: "User", MessageType: "outgoing", Content: "foreign-private-summary-body", CreatedAt: day}).Error)
	}

	var inRequest bool
	var writes []string
	for _, operation := range []string{"create", "update", "delete"} {
		operation := operation
		guard := func(tx *gorm.DB) {
			if inRequest {
				writes = append(writes, operation+":"+tx.Statement.Table)
				_ = tx.AddError(errors.New("summary HTTP must be read only"))
			}
		}
		switch operation {
		case "create":
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("summary_http_read_only", guard))
		case "update":
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("summary_http_read_only", guard))
		case "delete":
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("summary_http_read_only", guard))
		}
	}
	t.Cleanup(func() { require.Empty(t, writes, "summary requests must not write owner data") })
	request := func(version, path, principal string, query url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/"+version+"/accounts/91/reports"+path+"?"+query.Encode(), nil)
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		// The summary endpoint has always returned JSON, including CSV requests.
		req.Header.Set("Accept", "text/csv")
		w := httptest.NewRecorder()
		inRequest = true
		defer func() { inRequest = false }()
		r.ServeHTTP(w, req)
		return w
	}
	baseQuery := func() url.Values {
		return url.Values{"since": {"2024-02-29"}, "until": {"2024-02-29"}}
	}
	readSummary := func(t *testing.T, w *httptest.ResponseRecorder) service.AccountSummaryReport {
		t.Helper()
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Len(t, envelope, 2)
		require.JSONEq(t, "true", string(envelope["success"]))
		var result service.AccountSummaryReport
		require.NoError(t, json.Unmarshal(envelope["data"], &result))
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), string(envelope["data"]), "preserve every summary field and numeric representation")
		require.NotContains(t, w.Body.String(), "private-summary")
		return result
	}
	checkCounts := func(t *testing.T, got service.AccountSummaryReport, total, incoming, outgoing int64) {
		t.Helper()
		require.Equal(t, total, got.TotalMessages, "total counts every persisted message type")
		require.Equal(t, incoming, got.IncomingMessagesCount, "incoming is an exact type match")
		require.Equal(t, outgoing, got.OutgoingMessagesCount, "outgoing includes private, bot, queued and failed records")
	}
	t.Run("insertion_invariance", func(t *testing.T) {
		for _, stage := range []int{0, 1, 3} {
			if stage == 1 {
				addConversation(201, 91, 91, day.Add(10*time.Hour))
			} else if stage == 3 {
				addConversation(202, 91, 91, day.Add(20*time.Hour))
				addConversation(203, 91, 93, day.Add(11*time.Hour))
			}
			for _, version := range []string{"v1", "v2"} {
				for _, businessHours := range []string{"", "false"} {
					q := baseQuery()
					if businessHours != "" {
						q.Set("business_hours", businessHours)
					}
					got := readSummary(t, request(version, "/summary", "admin", q))
					want := service.AccountSummaryReport{TotalConversations: int64(stage), OpenConversations: int64(stage), ConversationsCount: int64(stage), TotalMessages: 12, IncomingMessagesCount: 3, OutgoingMessagesCount: 5}
					require.Equal(t, want, got, "%s: inserting %d unrelated in-window conversations cannot remove historical-conversation messages", version, stage)
				}
			}
		}
	})
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			t.Run("shape_permissions_and_tenant_scope", func(t *testing.T) {
				for _, principal := range []string{"admin", "report_view", "report_manage"} {
					got := readSummary(t, request(version, "/summary", principal, baseQuery()))
					require.Equal(t, service.AccountSummaryReport{TotalConversations: 3, OpenConversations: 3, ConversationsCount: 3, TotalMessages: 12, IncomingMessagesCount: 3, OutgoingMessagesCount: 5}, got)
				}
				for _, tc := range []struct {
					principal string
					status    int
					message   string
				}{
					{"", 401, "Authorization header is required"},
					{"agent", 403, "You do not have permission to perform this action"},
					{"foreign", 403, "You do not have access to this account"},
				} {
					w := request(version, "/summary", tc.principal, baseQuery())
					require.Equal(t, tc.status, w.Code)
					require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, tc.message), w.Body.String())
				}
			})
			t.Run("date_aliases_and_exact_bounds", func(t *testing.T) {
				for _, q := range []url.Values{
					baseQuery(),
					{"from": {"2024-02-29"}, "to": {"2024-02-29"}},
					{"since": {strconv.FormatInt(day.Unix(), 10)}, "until": {strconv.FormatInt(end.Unix(), 10)}},
					{"since": {"2024-02-29T05:45:00+05:45"}, "until": {"2024-03-01T05:45:00+05:45"}},
					{"since": {"2024-02-29"}, "until": {"2024-02-29"}, "from": {"bad"}, "to": {"bad"}},
					{"since": {""}, "until": {""}, "from": {"2024-02-29"}, "to": {"2024-02-29"}},
				} {
					checkCounts(t, readSummary(t, request(version, "/summary", "admin", q)), 12, 3, 5)
				}
			})
			t.Run("submicrosecond_bounds", func(t *testing.T) {
				for _, tc := range []struct {
					since, until              time.Time
					total, incoming, outgoing int64
				}{
					{day, day.Add(time.Nanosecond), 1, 1, 0},
					{day.Add(time.Nanosecond), day.Add(time.Microsecond + time.Nanosecond), 1, 0, 1},
					{day.Add(999 * time.Nanosecond), day.Add(2 * time.Microsecond), 1, 0, 1},
					{day.Add(-time.Microsecond), day.Add(time.Nanosecond), 2, 1, 1},
					{end.Add(-time.Microsecond), end, 1, 0, 1},
					{end, end.Add(time.Nanosecond), 1, 0, 1},
				} {
					q := url.Values{"since": {tc.since.Format(time.RFC3339Nano)}, "until": {tc.until.Format(time.RFC3339Nano)}}
					checkCounts(t, readSummary(t, request(version, "/summary", "admin", q)), tc.total, tc.incoming, tc.outgoing)
				}
			})
			t.Run("optional_empty_reverse_and_long_windows", func(t *testing.T) {
				for _, tc := range []struct {
					q                         url.Values
					total, incoming, outgoing int64
				}{
					{url.Values{}, 16, 4, 8},
					{url.Values{"since": {"2024-02-29"}}, 14, 4, 6},
					{url.Values{"until": {"2024-02-29"}}, 14, 3, 7},
					{url.Values{"since": {day.Format(time.RFC3339)}, "until": {day.Format(time.RFC3339)}}, 0, 0, 0},
					{url.Values{"since": {"2024-03-01T00:00:00Z"}, "until": {"2024-02-29T00:00:00Z"}}, 0, 0, 0},
					{url.Values{"since": {"1991-01-01"}, "until": {"1991-01-01"}}, 0, 0, 0},
					{url.Values{"since": {day.AddDate(0, 0, -451).Format(time.RFC3339)}, "until": {day.AddDate(0, 0, 401).Format(time.RFC3339)}}, 16, 4, 8},
				} {
					checkCounts(t, readSummary(t, request(version, "/summary", "admin", tc.q)), tc.total, tc.incoming, tc.outgoing)
				}
			})
			t.Run("legacy_ignored_parameters", func(t *testing.T) {
				q := baseQuery()
				for key, value := range map[string]string{"metric": "unknown", "type": "agent", "id": "bad", "group_by": "year", "timezone": "bad", "timezone_offset": "NaN", "business_hours": "maybe", "format": "csv", "unknown": "private-query-value"} {
					q.Set(key, value)
				}
				q["since"] = append(q["since"], "bad")
				checkCounts(t, readSummary(t, request(version, "/summary", "admin", q)), 12, 3, 5)
				// Invalid nonempty primary bounds remain unbounded and win over aliases.
				q = url.Values{"since": {"bad"}, "until": {"bad"}, "from": {"2024-02-29"}, "to": {"2024-02-29"}}
				checkCounts(t, readSummary(t, request(version, "/summary", "admin", q)), 16, 4, 8)
			})
			t.Run("daily_series_message_sum_parity", func(t *testing.T) {
				for _, q := range []url.Values{
					baseQuery(),
					{"since": {"2024-02-29T00:00:00.000000001Z"}, "until": {"2024-03-03T00:00:00Z"}},
				} {
					summary := readSummary(t, request(version, "/summary", "admin", q))
					for metric, want := range map[string]int64{"incoming_messages_count": summary.IncomingMessagesCount, "outgoing_messages_count": summary.OutgoingMessagesCount} {
						seriesQuery := url.Values{"metric": {metric}, "since": q["since"], "until": q["until"]}
						w := request(version, "", "admin", seriesQuery)
						require.Equal(t, 200, w.Code, w.Body.String())
						var points []domain.ReportCountPoint
						require.NoError(t, json.Unmarshal(w.Body.Bytes(), &points))
						var total int64
						for _, point := range points {
							total += point.Value
						}
						require.Equal(t, want, total, metric)
					}
				}
			})
		})
	}
	t.Run("business_hours_retains_creation_cohort", func(t *testing.T) {
		// Legacy business-hours filtering selects conversations by their creation
		// time. Message time itself is not checked against the inbox opening hours.
		for _, row := range []struct {
			conversationID uint
			kind           string
		}{{201, "incoming"}, {201, "outgoing"}, {202, "outgoing"}, {203, "incoming"}} {
			require.NoError(t, db.Create(&domain.Message{AccountID: 91, ConversationID: row.conversationID, MessageType: row.kind, SenderType: "User", SenderID: 1, Content: "business-hour-fixture", CreatedAt: day.Add(18 * time.Hour)}).Error)
		}
		for _, version := range []string{"v1", "v2"} {
			for _, value := range []string{"true", "1", " TRUE "} {
				q := baseQuery()
				q.Set("business_hours", value)
				got := readSummary(t, request(version, "/summary", "admin", q))
				require.Equal(t, int64(1), got.TotalConversations)
				checkCounts(t, got, 2, 1, 1)
			}
			q := baseQuery()
			q.Set("business_hours", "false")
			checkCounts(t, readSummary(t, request(version, "/summary", "admin", q)), 16, 5, 7)
			q = url.Values{"since": {day.Format(time.RFC3339)}, "until": {day.Add(time.Hour).Format(time.RFC3339)}, "business_hours": {"true"}}
			checkCounts(t, readSummary(t, request(version, "/summary", "admin", q)), 0, 0, 0)
		}
	})
	t.Run("message_failure_is_private_and_read_only", func(t *testing.T) {
		const secret = "summary-storage-password-token-private-sentinel"
		var logs bytes.Buffer
		logger.SetOutput(&logs, slog.LevelDebug, "json")
		defer logger.SetOutput(os.Stdout, slog.LevelInfo, "json")
		injected := 0
		inject := func(tx *gorm.DB) {
			if tx.Statement.Table == "messages" || strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "messages") {
				injected++
				_ = tx.AddError(errors.New(secret))
			}
		}
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("summary_http_message_failure", inject))
		require.NoError(t, db.Callback().Row().Before("gorm:row").Register("summary_http_message_failure", inject))
		defer func() {
			require.NoError(t, db.Callback().Query().Remove("summary_http_message_failure"))
			require.NoError(t, db.Callback().Row().Remove("summary_http_message_failure"))
		}()
		for _, version := range []string{"v1", "v2"} {
			// No conversation rows match: the failure must reach the independent
			// message aggregate even when the timing-metric cohort is empty.
			q := url.Values{"since": {day.Format(time.RFC3339)}, "until": {day.Add(time.Hour).Format(time.RFC3339)}}
			w := request(version, "/summary", "admin", q)
			require.Equal(t, 500, w.Code, w.Body.String())
			require.JSONEq(t, `{"success":false,"error":"Failed to generate report summary"}`, w.Body.String())
			require.NotContains(t, w.Body.String(), secret)
		}
		require.GreaterOrEqual(t, injected, 2)
		require.NotContains(t, logs.String(), secret)
		require.NotContains(t, logs.String(), tokens["admin"])
		require.NotContains(t, logs.String(), "private-summary-message-body")
		require.NotContains(t, logs.String(), "SELECT")
		require.Empty(t, writes)
	})
}
