package test

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise native owner repositories, authentication, tenant and report-permission
// middleware through both public route versions. No report service is stubbed.
func TestReportTimeBoundaryHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "report-boundaries.db"), JWTSecret: "report-time-boundary-test-key-12345678", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	r := router.SetupRouter(cfg, db, ws.NewHub())
	for _, accountID := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: accountID, Name: fmt.Sprintf("Report account %d", accountID)}).Error)
	}
	role := domain.CustomRole{AccountID: 91, Name: "Report reader", Permissions: `["report_manage"]`}
	require.NoError(t, db.Create(&role).Error)
	tokens := map[string]string{}
	for _, kind := range []string{"administrator", "agent", "report_role", "foreign"} {
		user := domain.User{Name: kind, Email: kind + "@report-boundaries.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		member := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		switch kind {
		case "administrator":
			member.Role = domain.RoleAdministrator
		case "report_role":
			member.CustomRoleID = &role.ID
		case "foreign":
			member.AccountID, member.Role = 92, domain.RoleAdministrator
		}
		require.NoError(t, db.Create(&member).Error)
		tokens[kind], err = auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	for _, accountID := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Inbox{ID: accountID, AccountID: accountID, Name: "Bot inbox", WebsiteToken: fmt.Sprintf("boundary-inbox-%d", accountID), Timezone: "UTC"}).Error)
		require.NoError(t, db.Create(&domain.AgentBot{ID: accountID, AccountID: accountID, Name: "Bot"}).Error)
		require.NoError(t, db.Create(&domain.AgentBotInbox{AccountID: accountID, InboxID: accountID, AgentBotID: accountID}).Error)
	}
	since := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	until := since.AddDate(0, 0, 1)
	previousStart := since.AddDate(0, 0, -1)
	// Include exact boundaries and the last representable microsecond on either
	// side. The terminal fraction also distinguishes a named end day from 23:59:59.
	rows := []struct {
		at      time.Time
		handoff bool
	}{
		{previousStart.Add(-time.Microsecond), false},
		{previousStart, false},
		{since.Add(-time.Microsecond), true},
		{since, false},
		{since.Add(12 * time.Hour), true},
		{until.Add(-time.Microsecond), false},
		{until, false},
		{until.Add(time.Microsecond), false},
	}
	for i, row := range rows {
		id := uint(101 + i)
		require.NoError(t, db.Create(&domain.Contact{ID: id, AccountID: 91, Name: "Boundary contact", CreatedAt: row.at}).Error)
		require.NoError(t, db.Create(&domain.Conversation{ID: id, DisplayID: id, AccountID: 91, InboxID: 91, ContactID: id, Status: domain.ConversationStatusOpen, CreatedAt: row.at, UpdatedAt: row.at, LastActivityAt: row.at}).Error)
		for _, messageType := range []string{domain.MessageTypeIncoming, domain.MessageTypeOutgoing} {
			require.NoError(t, db.Create(&domain.Message{AccountID: 91, ConversationID: id, SenderType: domain.SenderTypeUser, SenderID: 1, MessageType: messageType, Content: "Boundary message", CreatedAt: row.at}).Error)
		}
		name := "conversation_bot_resolved"
		if row.handoff {
			name = "conversation_bot_handoff"
		}
		require.NoError(t, db.Create(&domain.ReportingEvent{AccountID: 91, ConversationID: &id, Name: name, CreatedAt: row.at}).Error)
	}
	selectedID := uint(104)
	for _, at := range []time.Time{since.Add(-time.Microsecond), until} {
		require.NoError(t, db.Create(&domain.Message{AccountID: 91, ConversationID: selectedID, SenderType: domain.SenderTypeUser, SenderID: 1, MessageType: domain.MessageTypeOutgoing, Content: "Outside selected message window", CreatedAt: at}).Error)
	}
	// An in-window message on an older conversation is counted by the default
	// summary; bot metrics and drilldown retain their existing creation cohort.
	require.NoError(t, db.Create(&domain.Message{AccountID: 91, ConversationID: 101, SenderType: domain.SenderTypeUser, SenderID: 1, MessageType: domain.MessageTypeOutgoing, Content: "Historical creation cohort", CreatedAt: since.Add(time.Hour)}).Error)
	// Handoff precedence applies within its event window: a handoff at until
	// must not remove this conversation's resolution from the preceding window.
	require.NoError(t, db.Create(&domain.ReportingEvent{AccountID: 91, ConversationID: &selectedID, Name: "conversation_bot_handoff", CreatedAt: until}).Error)
	foreignID := uint(900)
	require.NoError(t, db.Create(&domain.Contact{ID: foreignID, AccountID: 92, Name: "Foreign contact", CreatedAt: since}).Error)
	require.NoError(t, db.Create(&domain.Conversation{ID: foreignID, DisplayID: foreignID, AccountID: 92, InboxID: 92, ContactID: foreignID, Status: domain.ConversationStatusOpen, CreatedAt: since}).Error)
	require.NoError(t, db.Create(&domain.Message{AccountID: 92, ConversationID: foreignID, SenderType: domain.SenderTypeUser, SenderID: 1, MessageType: domain.MessageTypeOutgoing, CreatedAt: since}).Error)
	require.NoError(t, db.Create(&domain.ReportingEvent{AccountID: 92, ConversationID: &foreignID, Name: "conversation_bot_resolved", CreatedAt: since}).Error)

	request := func(version, endpoint, token string, query url.Values, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/%s/accounts/91/reports/%s?%s", version, endpoint, query.Encode()), nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	windows := []struct {
		name  string
		query url.Values
	}{
		{"date", url.Values{"since": {"2026-01-31"}, "until": {"2026-01-31"}}},
		{"unix", url.Values{"since": {strconv.FormatInt(since.Unix(), 10)}, "until": {strconv.FormatInt(until.Unix(), 10)}}},
		{"RFC3339_offset", url.Values{"since": {"2026-01-31T05:45:00+05:45"}, "until": {"2026-02-01T05:45:00+05:45"}}},
		{"aliases", url.Values{"from": {"2026-01-31"}, "to": {"2026-01-31"}}},
		{"primary_precedence", url.Values{"since": {"2026-01-31"}, "until": {"2026-01-31"}, "from": {"2026-01-29"}, "to": {"2026-02-02"}}},
	}
	wantSummary := service.AccountSummaryReport{
		TotalConversations: 3, OpenConversations: 3, TotalMessages: 7, TotalContacts: 3,
		ConversationsCount: 3, IncomingMessagesCount: 3, OutgoingMessagesCount: 4,
	}
	wantTrends := service.ConversationTrendsReport{CurrentPeriodTotal: 3, PreviousPeriodTotal: 2, GrowthPercentage: 50, Trends: []service.TrendPoint{{Date: "2026-01-31", Count: 3}}}
	jsonEqual := func(t *testing.T, want any, w *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		encoded, err := json.Marshal(want)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), w.Body.String())
	}

	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			for _, window := range windows {
				t.Run(window.name, func(t *testing.T) {
					for _, principal := range []string{"administrator", "report_role"} {
						t.Run(principal, func(t *testing.T) {
							token := tokens[principal]
							jsonEqual(t, map[string]any{"success": true, "data": wantSummary}, request(version, "summary", token, window.query, ""))
							jsonEqual(t, map[string]any{"success": true, "data": wantSummary, "payload": wantSummary}, request(version, "conversations_summary", token, window.query, ""))
							jsonEqual(t, map[string]any{"success": true, "data": wantTrends}, request(version, "trends", token, window.query, ""))
							jsonEqual(t, service.BotMetricsReport{ConversationCount: 3, MessageCount: 3, ResolutionRate: 66.7, HandoffRate: 33.3}, request(version, "bot_metrics", token, window.query, ""))
							jsonEqual(t, service.BotSummaryReport{BotResolutionsCount: 2, BotHandoffsCount: 1, Previous: service.BotSummaryMetrics{BotResolutionsCount: 1, BotHandoffsCount: 1}}, request(version, "bot_summary", token, window.query, ""))
						})
					}
				})
			}
			t.Run("CSV_JSON_parity", func(t *testing.T) {
				for _, window := range windows[:3] {
					for _, viaAccept := range []bool{false, true} {
						query := cloneReportBoundaryQuery(window.query)
						accept := ""
						if viaAccept {
							accept = "text/csv"
						} else {
							query.Set("format", "csv")
						}
						w := request(version, "conversations_summary", tokens["administrator"], query, accept)
						require.Equal(t, http.StatusOK, w.Code, w.Body.String())
						require.Equal(t, "text/csv", w.Header().Get("Content-Type"))
						require.Equal(t, "attachment;filename=conversations_summary_report.csv", w.Header().Get("Content-Disposition"))
						records, err := csv.NewReader(w.Body).ReadAll()
						require.NoError(t, err)
						require.Equal(t, [][]string{{"Conversations", "Incoming Messages", "Outgoing Messages", "Avg First Response Time (s)", "Avg Resolution Time (s)", "Resolutions Count", "Reply Time (s)"}, {"3", "3", "4", "0.0", "0.0", "0", "0.0"}}, records)
					}
					export := request(version, "conversations/export", tokens["administrator"], window.query, "")
					require.Equal(t, http.StatusOK, export.Code, export.Body.String())
					require.Equal(t, "text/csv", export.Header().Get("Content-Type"))
					records, err := csv.NewReader(export.Body).ReadAll()
					require.NoError(t, err)
					require.Len(t, records, 4)
					require.Equal(t, []string{"ID", "DisplayID", "Status", "Priority", "InboxID", "ContactID", "AssigneeID", "CreatedAt", "LastActivityAt"}, records[0])
					ids := []string{records[1][0], records[2][0], records[3][0]}
					require.ElementsMatch(t, []string{"104", "105", "106"}, ids, "include since and terminal fraction; exclude until")
				}
			})
			t.Run("traffic_offsets_preserve_selection_and_all_counts", func(t *testing.T) {
				for _, tc := range []struct {
					offset string
					dates  []string
					cells  map[string]int64
				}{
					{"0", []string{"2026-01-31"}, map[string]int64{"00:00/2026-01-31": 1, "12:00/2026-01-31": 1, "23:00/2026-01-31": 1}},
					{"5.5", []string{"2026-01-31", "2026-02-01"}, map[string]int64{"05:00/2026-01-31": 1, "17:00/2026-01-31": 1, "05:00/2026-02-01": 1}},
					{"-5.5", []string{"2026-01-30", "2026-01-31"}, map[string]int64{"18:00/2026-01-30": 1, "06:00/2026-01-31": 1, "18:00/2026-01-31": 1}},
				} {
					t.Run(tc.offset, func(t *testing.T) {
						want := [][]any{{"Start of the hour"}}
						for _, date := range tc.dates {
							want[0] = append(want[0], date)
						}
						for hour := 0; hour < 24; hour++ {
							label := fmt.Sprintf("%02d:00", hour)
							row := []any{label}
							for _, date := range tc.dates {
								row = append(row, tc.cells[label+"/"+date])
							}
							want = append(want, row)
						}
						for _, window := range windows[:3] {
							query := cloneReportBoundaryQuery(window.query)
							query.Set("timezone_offset", tc.offset)
							jsonEqual(t, want, request(version, "conversation_traffic", tokens["administrator"], query, ""))
							// Other reporting endpoints retain UTC dates and ignore this traffic-only option.
							jsonEqual(t, map[string]any{"success": true, "data": wantTrends}, request(version, "trends", tokens["administrator"], query, ""))
							jsonEqual(t, map[string]any{"success": true, "data": wantSummary}, request(version, "summary", tokens["administrator"], query, ""))
							for _, viaAccept := range []bool{false, true} {
								csvQuery := cloneReportBoundaryQuery(query)
								accept := "text/csv"
								if !viaAccept {
									csvQuery.Set("format", "csv")
									accept = ""
								}
								w := request(version, "conversation_traffic", tokens["administrator"], csvQuery, accept)
								require.Equal(t, http.StatusOK, w.Code, w.Body.String())
								require.Equal(t, "text/csv", w.Header().Get("Content-Type"))
								require.Equal(t, "attachment;filename=conversation_traffic_reports.csv", w.Header().Get("Content-Disposition"))
								records, err := csv.NewReader(w.Body).ReadAll()
								require.NoError(t, err)
								require.Len(t, records, len(want))
								for i, row := range want {
									expected := make([]string, len(row))
									for j, cell := range row {
										expected[j] = fmt.Sprint(cell)
									}
									require.Equal(t, expected, records[i], "CSV row %d", i)
								}
							}
						}
					})
				}
			})
			t.Run("exact_partial_trends_and_previous_period", func(t *testing.T) {
				query := url.Values{"since": {"2026-01-31T11:30:00Z"}, "until": {"2026-02-01T00:00:00.000001Z"}}
				want := service.ConversationTrendsReport{CurrentPeriodTotal: 3, PreviousPeriodTotal: 2, GrowthPercentage: 50, Trends: []service.TrendPoint{{Date: "2026-01-31", Count: 2}, {Date: "2026-02-01", Count: 1}}}
				jsonEqual(t, map[string]any{"success": true, "data": want}, request(version, "trends", tokens["administrator"], query, ""))
				w := request(version, "summary", tokens["administrator"], query, "")
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				var summary struct {
					Success bool                         `json:"success"`
					Data    service.AccountSummaryReport `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))
				require.True(t, summary.Success)
				require.Equal(t, want.CurrentPeriodTotal, summary.Data.TotalConversations)
			})
			t.Run("drilldown_retains_UTC_day_and_creation_cohort", func(t *testing.T) {
				for _, metric := range []string{"conversations_count", "outgoing_messages_count"} {
					query := url.Values{"metric": {metric}, "bucket_timestamp": {"2026-02-01T05:29:59.999999+05:30"}, "type": {"account"}}
					baseline := request(version, "drilldown", tokens["administrator"], query, "")
					require.Equal(t, http.StatusOK, baseline.Code, baseline.Body.String())
					query.Set("since", "2026-02-04")
					query.Set("until", "2026-02-04")
					query.Set("business_hours", "true")
					query.Set("timezone_offset", "12.5")
					w := request(version, "drilldown", tokens["administrator"], query, "")
					require.Equal(t, http.StatusOK, w.Code, w.Body.String())
					require.JSONEq(t, baseline.Body.String(), w.Body.String(), "drilldown uses its UTC bucket and existing cohort, ignoring ReportFilter and traffic offset")
					var envelope map[string]json.RawMessage
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
					require.Len(t, envelope, 2)
					require.Contains(t, envelope, "meta")
					require.Contains(t, envelope, "payload")
					var result struct {
						Meta    service.DrilldownMeta `json:"meta"`
						Payload []struct {
							ID             uint `json:"id"`
							ConversationID uint `json:"conversation_id"`
						} `json:"payload"`
					}
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
					require.Equal(t, metric, result.Meta.Metric)
					require.Equal(t, since.Unix(), result.Meta.Bucket.Since)
					require.Equal(t, int64(3), result.Meta.TotalCount)
					require.Equal(t, int64(3), result.Meta.ConversationCount)
					require.Equal(t, 1, result.Meta.CurrentPage)
					require.Equal(t, 25, result.Meta.PerPage)
					require.Len(t, result.Payload, 3)
					var ids []uint
					for _, row := range result.Payload {
						if metric == "conversations_count" {
							require.Equal(t, "Conversation", result.Meta.RecordType)
							ids = append(ids, row.ID)
						} else {
							require.Equal(t, "Message", result.Meta.RecordType)
							ids = append(ids, row.ConversationID)
						}
					}
					require.ElementsMatch(t, []uint{104, 105, 106}, ids)
				}
			})
			t.Run("native_authorization_and_error_envelopes", func(t *testing.T) {
				for _, endpoint := range []string{"summary", "conversations_summary", "conversations/export", "trends", "bot_metrics", "bot_summary", "conversation_traffic"} {
					for _, principal := range []struct {
						kind   string
						status int
						error  string
					}{
						{"agent", http.StatusForbidden, "You do not have permission to perform this action"},
						{"foreign", http.StatusForbidden, "You do not have access to this account"},
						{"", http.StatusUnauthorized, "Authorization header is required"},
					} {
						w := request(version, endpoint, tokens[principal.kind], windows[0].query, "text/csv")
						require.Equal(t, principal.status, w.Code, "%s: %s", endpoint, w.Body.String())
						require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, principal.error), w.Body.String())
						require.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "application/json"))
					}
				}
			})
		})
	}
}

func cloneReportBoundaryQuery(query url.Values) url.Values {
	result := make(url.Values, len(query))
	for key, values := range query {
		result[key] = append([]string(nil), values...)
	}
	return result
}
