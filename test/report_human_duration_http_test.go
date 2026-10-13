package test

import (
	"encoding/csv"
	"encoding/json"
	"errors"
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
	"gorm.io/gorm"
)

func TestReportHumanDurationHTTP(t *testing.T) {
	cfg := reportHumanDurationHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "human-duration.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runReportHumanDurationHTTP(t, cfg, db)
}

func reportHumanDurationHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "human-duration-http-test-secret-12345678", JWTExpirationHours: 24}
}

// SQLite and native PostgreSQL share the same fixtures, router, authorization,
// JSON/CSV contracts and expected numbers. Bots remain records and FCR/bot inputs;
// only their eligibility to complete a human response duration changes.
func runReportHumanDurationHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	start := time.Date(2024, 2, 29, 16, 30, 0, 0, time.UTC) // Thursday
	botStart := start.AddDate(0, 0, 7)
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: fmt.Sprintf("Duration account %d", id)}).Error)
		require.NoError(t, db.Create(&domain.Contact{ID: id, AccountID: id, Name: "Duration contact", CreatedAt: start.AddDate(0, 0, -2)}).Error)
		require.NoError(t, db.Create(&domain.Inbox{ID: id, AccountID: id, Name: "Duration inbox", ChannelType: "Channel::Email", WebsiteToken: fmt.Sprintf("human-duration-%d", id), Timezone: "UTC", WorkingHoursEnabled: true, WorkingHours: `[{"day_of_week":4,"closed":false,"open_hour":9,"close_hour":17},{"day_of_week":5,"closed":false,"open_hour":9,"close_hour":17}]`}).Error)
		require.NoError(t, db.Create(&domain.Team{ID: id, AccountID: id, Name: "Duration team"}).Error)
		require.NoError(t, db.Create(&domain.Label{ID: id, AccountID: id, Title: "Duration label", Color: "#112233"}).Error)
	}
	tokens := map[string]string{}
	users := map[string]domain.User{}
	principals := []string{"admin", "agent", "report_view", "report_manage", "foreign"}
	for _, principal := range principals {
		user := domain.User{Name: principal, Email: principal + "@human-duration.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		users[principal] = user
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
	adminID, foreignID, teamID := users["admin"].ID, users["foreign"].ID, uint(91)
	require.NoError(t, db.Create(&domain.TeamMember{TeamID: 91, UserID: adminID}).Error)
	require.NoError(t, db.Create(&domain.TeamMember{TeamID: 92, UserID: foreignID}).Error)
	for _, row := range []struct {
		id, accountID uint
		at, resolved  time.Time
		assignee      *uint
	}{
		{101, 91, start, start.Add(18*time.Hour + 30*time.Minute), &adminID},
		{102, 91, botStart, botStart.Add(30 * time.Minute), &adminID},
		{900, 92, start, start.Add(time.Minute), &foreignID},
	} {
		require.NoError(t, db.Create(&domain.Conversation{ID: row.id, DisplayID: row.id, AccountID: row.accountID, InboxID: row.accountID, ContactID: row.accountID, AssigneeID: row.assignee, TeamID: &teamID, Status: domain.ConversationStatusResolved, CreatedAt: row.at, UpdatedAt: row.resolved, LastActivityAt: row.resolved}).Error)
		// The foreign conversation deliberately shares the owned team and label:
		// dimensions must still apply the account-scoped conversation cohort.
		require.NoError(t, db.Create(&domain.ConversationLabel{ConversationID: row.id, LabelID: 91}).Error)
	}
	addMessage := func(accountID, conversationID uint, at time.Time, kind, sender, status string, private bool) {
		t.Helper()
		senderID := adminID
		if accountID == 92 {
			senderID = foreignID
		}
		require.NoError(t, db.Create(&domain.Message{AccountID: accountID, ConversationID: conversationID, SenderID: senderID, SenderType: sender, MessageType: kind, Status: status, Private: private, Content: "private-human-duration-secret", ContentAttributes: `{"secret":"duration-metadata"}`, CreatedAt: at}).Error)
	}
	addMessage(91, 101, start, "incoming", "Contact", "sent", false)
	for i, bot := range []string{"AgentBot", "CaptainAssistant", "Captain::Assistant"} {
		addMessage(91, 101, start.Add(time.Duration(i+1)*time.Minute), "outgoing", bot, "failed", false)
	}
	addMessage(91, 101, start.Add(4*time.Minute), "outgoing", "User", "sent", true)
	// First human: Fri 09:30 = 17 wall hours / 1 business hour. Status does
	// not change the existing public-outgoing duration eligibility contract.
	addMessage(91, 101, start.Add(17*time.Hour), "outgoing", "User", "queued", false)
	addMessage(91, 101, start.Add(17*time.Hour+15*time.Minute), "incoming", "Contact", "sent", false)
	for i, bot := range []string{"AgentBot", "CaptainAssistant", "Captain::Assistant"} {
		addMessage(91, 101, start.Add(17*time.Hour+time.Duration(16+i)*time.Minute), "outgoing", bot, "sent", false)
	}
	// A second incoming cannot replace the oldest pending incoming after bots.
	addMessage(91, 101, start.Add(17*time.Hour+20*time.Minute), "incoming", "Contact", "sent", false)
	addMessage(91, 101, start.Add(17*time.Hour+45*time.Minute), "outgoing", "User", "failed", false)
	addMessage(91, 102, botStart, "incoming", "Contact", "sent", false)
	addMessage(91, 102, botStart.Add(time.Minute), "outgoing", "AgentBot", "sent", false)
	// Foreign replies cannot terminate a pending owned incoming, even when a
	// malformed message points to an owned conversation and falls in its window.
	for _, row := range []struct {
		id uint
		at time.Time
	}{{101, start}, {102, botStart}, {900, start}} {
		addMessage(92, row.id, row.at.Add(30*time.Second), "outgoing", "User", "sent", false)
	}

	var inRequest bool
	var writes []string
	for _, operation := range []string{"create", "update", "delete"} {
		operation := operation
		guard := func(tx *gorm.DB) {
			if inRequest {
				writes = append(writes, operation+":"+tx.Statement.Table)
				_ = tx.AddError(errors.New("human duration HTTP must be read only"))
			}
		}
		switch operation {
		case "create":
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("human_duration_http_read_only", guard))
		case "update":
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("human_duration_http_read_only", guard))
		case "delete":
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("human_duration_http_read_only", guard))
		}
	}
	t.Cleanup(func() { require.Empty(t, writes, "duration reports must not write owner data") })
	request := func(version, path, principal string, q url.Values, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/"+version+"/accounts/91"+path+"?"+q.Encode(), nil)
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		req.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		inRequest = true
		defer func() { inRequest = false }()
		r.ServeHTTP(w, req)
		return w
	}
	checkJSON := func(t *testing.T, w *httptest.ResponseRecorder, expected any, payload bool) {
		t.Helper()
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		envelope := map[string]any{"success": true, "data": expected}
		if payload {
			envelope["payload"] = expected
		}
		encoded, err := json.Marshal(envelope)
		require.NoError(t, err)
		var wantJSON, gotJSON map[string]any
		require.NoError(t, json.Unmarshal(encoded, &wantJSON))
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &gotJSON))
		if rows, ok := wantJSON["data"].([]any); ok {
			// Dimension membership order is not part of the report contract.
			// Compare complete rows, retaining unknown-field and omission checks.
			require.ElementsMatch(t, rows, gotJSON["data"])
			delete(wantJSON, "data")
			delete(gotJSON, "data")
		}
		require.Equal(t, wantJSON, gotJSON)
		require.NotContains(t, w.Body.String(), "secret")
		require.NotContains(t, w.Body.String(), "foreign@")
	}
	distributionPaths := []string{"/reports/first_response", "/reports/first_response_distribution", "/reports/first_response_time_distribution"}
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			for _, window := range []string{"mixed", "bot_only", "before_human"} {
				for _, businessHours := range []bool{false, true} {
					t.Run(window+"_business_hours_"+strconv.FormatBool(businessHours), func(t *testing.T) {
						q := url.Values{"since": {"2024-02-29"}, "until": {"2024-03-07"}, "business_hours": {strconv.FormatBool(businessHours)}}
						want := service.AccountSummaryReport{TotalConversations: 2, ResolvedConversations: 2, ConversationsCount: 2, TotalMessages: 14, IncomingMessagesCount: 4, OutgoingMessagesCount: 10, ResolutionsCount: 2, ResolutionRate: 100, AvgFirstResponseTime: 61200, AvgReplyTime: 31500, AvgResolutionTime: 34200, FirstContactResolutionRate: 50, BotHandoffsCount: 2}
						totalSent := int64(3) // two public human messages and the private note
						buckets := service.FirstResponseDistributionBuckets{EightToTwentyFour: 1}
						distribution := service.FirstResponseDistributionReport{Over4h: 1}
						if businessHours {
							want.AvgFirstResponseTime, want.AvgReplyTime, want.AvgResolutionTime = 3600, 2700, 5400
							buckets = service.FirstResponseDistributionBuckets{ZeroToOneHour: 1}
							// Exactly one hour retains the existing, distinct legacy boundary.
							distribution = service.FirstResponseDistributionReport{Under4h: 1}
						}
						if window != "mixed" {
							want.TotalConversations, want.ResolvedConversations, want.ConversationsCount, want.ResolutionsCount, want.BotHandoffsCount = 1, 1, 1, 1, 1
							want.AvgFirstResponseTime, want.AvgReplyTime = 0, 0
							buckets = service.FirstResponseDistributionBuckets{}
							distribution = service.FirstResponseDistributionReport{}
							if window == "bot_only" {
								q.Set("since", "2024-03-07")
								want.TotalMessages, want.IncomingMessagesCount, want.OutgoingMessagesCount = 2, 1, 1
								want.AvgResolutionTime, want.FirstContactResolutionRate, totalSent = 1800, 100, 0
							} else {
								// Human replies outside the requested message window stay out;
								// the conversation and its persisted resolution time remain in.
								q.Set("until", "2024-02-29")
								want.TotalMessages, want.IncomingMessagesCount, want.OutgoingMessagesCount = 5, 1, 4
								want.AvgResolutionTime, want.FirstContactResolutionRate, totalSent = 66600, 0, 1
								if businessHours {
									want.AvgResolutionTime = 9000
								}
							}
						}
						checkJSON(t, request(version, "/reports/summary", "admin", q, "text/csv"), want, false)
						checkJSON(t, request(version, "/reports/conversations_summary", "admin", q, "application/json"), want, true)
						for _, csvByQuery := range []bool{false, true} {
							accept := "text/csv"
							if csvByQuery {
								q.Set("format", "csv")
								accept = "application/json"
							}
							w := request(version, "/reports/conversations_summary", "admin", q, accept)
							q.Del("format")
							require.Equal(t, http.StatusOK, w.Code, w.Body.String())
							require.Contains(t, w.Header().Get("Content-Type"), "text/csv")
							require.Equal(t, "attachment;filename=conversations_summary_report.csv", w.Header().Get("Content-Disposition"))
							rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
							require.NoError(t, err)
							require.Equal(t, [][]string{
								{"Conversations", "Incoming Messages", "Outgoing Messages", "Avg First Response Time (s)", "Avg Resolution Time (s)", "Resolutions Count", "Reply Time (s)"},
								{strconv.FormatInt(want.ConversationsCount, 10), strconv.FormatInt(want.IncomingMessagesCount, 10), strconv.FormatInt(want.OutgoingMessagesCount, 10), fmt.Sprintf("%.1f", want.AvgFirstResponseTime), fmt.Sprintf("%.1f", want.AvgResolutionTime), strconv.FormatInt(want.ResolutionsCount, 10), fmt.Sprintf("%.1f", want.AvgReplyTime)},
							}, rows)
						}
						agents := make([]service.AgentMetric, 0, 4)
						for _, principal := range principals[:4] {
							user := users[principal]
							metric := service.AgentMetric{UserID: user.ID, Name: user.Name, Email: user.Email}
							if principal == "admin" {
								metric.AssignedConversations, metric.ResolvedConversations = want.ConversationsCount, want.ResolvedConversations
								metric.AvgFirstResponseTime, metric.AvgReplyTime, metric.AvgResolutionTime = want.AvgFirstResponseTime, want.AvgReplyTime, want.AvgResolutionTime
								metric.ResolutionRate, metric.TotalMessagesSent = 100, totalSent
							}
							agents = append(agents, metric)
						}
						for _, dimension := range []struct {
							plural, singular string
							want             any
						}{
							{"agents", "agent", agents},
							{"teams", "team", []service.TeamMetric{{TeamID: 91, Name: "Duration team", MemberCount: 1, AssignedConversations: want.ConversationsCount, ResolvedConversations: want.ResolvedConversations, AvgFirstResponseTime: want.AvgFirstResponseTime, AvgResolutionTime: want.AvgResolutionTime, ResolutionRate: 100}}},
							{"inboxes", "inbox", []service.InboxMetric{{InboxID: 91, Name: "Duration inbox", ChannelType: "Channel::Email", TotalConversations: want.ConversationsCount, ResolvedConversations: want.ResolvedConversations, AvgFirstResponseTime: want.AvgFirstResponseTime, AvgReplyTime: want.AvgReplyTime, AvgResolutionTime: want.AvgResolutionTime, ResolutionRate: 100}}},
							{"labels", "label", []service.LabelMetric{{LabelID: 91, Title: "Duration label", Color: "#112233", ConversationCount: want.ConversationsCount, ResolvedConversations: want.ResolvedConversations, AvgFirstResponseTime: want.AvgFirstResponseTime, AvgResolutionTime: want.AvgResolutionTime}}},
						} {
							checkJSON(t, request(version, "/reports/"+dimension.plural, "admin", q, "application/json"), dimension.want, false)
							if version == "v2" {
								checkJSON(t, request(version, "/summary_reports/"+dimension.singular, "admin", q, "application/json"), dimension.want, false)
							}
						}
						distribution.Total = buckets
						distribution.Channels = []service.ChannelFRTDistribution{{ChannelType: "Channel::Email", Distribution: buckets}}
						for _, path := range distributionPaths {
							checkJSON(t, request(version, path, "admin", q, "application/json"), distribution, false)
						}
					})
				}
			}
			t.Run("permissions_and_tenant_scope", func(t *testing.T) {
				paths := []string{"/reports/summary", "/reports/conversations_summary", "/reports/agents", "/reports/teams", "/reports/inboxes", "/reports/labels"}
				paths = append(paths, distributionPaths...)
				if version == "v2" {
					paths = append(paths, "/summary_reports/agent", "/summary_reports/team", "/summary_reports/inbox", "/summary_reports/label")
				}
				q := url.Values{"since": {"2024-02-29"}, "until": {"2024-03-07"}, "business_hours": {"false"}}
				for _, path := range paths {
					admin := request(version, path, "admin", q, "text/csv")
					require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
					for _, principal := range []string{"report_view", "report_manage"} {
						w := request(version, path, principal, q, "text/csv")
						require.Equal(t, http.StatusOK, w.Code, "%s %s: %s", path, principal, w.Body.String())
						require.Equal(t, admin.Header().Get("Content-Type"), w.Header().Get("Content-Type"))
						require.Equal(t, admin.Body.String(), w.Body.String(), "%s %s", path, principal)
					}
					for _, denied := range []struct {
						principal string
						status    int
						message   string
					}{
						{"", 401, "Authorization header is required"},
						{"agent", 403, "You do not have permission to perform this action"},
						{"foreign", 403, "You do not have access to this account"},
					} {
						w := request(version, path, denied.principal, q, "text/csv")
						require.Equal(t, denied.status, w.Code, "%s %s", path, denied.principal)
						require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, denied.message), w.Body.String())
					}
				}
			})
		})
	}
}
