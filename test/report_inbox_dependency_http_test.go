package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportInboxDependencyHTTP(t *testing.T) {
	cfg := reportInboxDependencyHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "inbox-dependency.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runReportInboxDependencyHTTP(t, cfg, db)
}

func reportInboxDependencyHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "inbox-dependency-http-test-key-12345678", JWTExpirationHours: 24}
}

type reportInboxDependencyHTTPRoute struct {
	name, path, failure string
}

func (r reportInboxDependencyHTTPRoute) accept() string {
	if r.name == "conversations_summary_json" {
		return "application/json"
	}
	return "text/csv"
}

func reportInboxDependencyHTTPRoutes(version string) []reportInboxDependencyHTTPRoute {
	routes := []reportInboxDependencyHTTPRoute{
		{"summary", "/reports/summary", "Failed to generate report summary"},
		{"conversations_summary_json", "/reports/conversations_summary", "Failed to generate conversations summary"},
		{"conversations_summary_csv", "/reports/conversations_summary", "Failed to generate conversations summary"},
		{"agents", "/reports/agents", "Failed to generate agent metrics"},
		{"teams", "/reports/teams", "Failed to generate team metrics"},
		{"inboxes", "/reports/inboxes", "Failed to generate inbox metrics"},
		{"labels", "/reports/labels", "Failed to generate label metrics"},
		{"first_response", "/reports/first_response", "Failed to calculate first response distribution"},
		{"first_response_distribution", "/reports/first_response_distribution", "Failed to calculate first response distribution"},
		{"first_response_time_distribution", "/reports/first_response_time_distribution", "Failed to calculate first response distribution"},
		{"export", "/reports/conversations/export", "Failed to export conversations"},
		{"trends", "/reports/trends", "Failed to generate conversation trends"},
	}
	if version == "v2" {
		routes = append(routes,
			reportInboxDependencyHTTPRoute{"summary_agent", "/summary_reports/agent", "Failed to generate agent metrics"},
			reportInboxDependencyHTTPRoute{"summary_team", "/summary_reports/team", "Failed to generate team metrics"},
			reportInboxDependencyHTTPRoute{"summary_inbox", "/summary_reports/inbox", "Failed to generate inbox metrics"},
			reportInboxDependencyHTTPRoute{"summary_label", "/summary_reports/label", "Failed to generate label metrics"})
	}
	return routes
}

// Exercise real V1/V2 routes, account membership and CHN repository failures.
// Failure injection only affects the first inbox read in a request, so a later
// successful list cannot disguise the initial dependency failure.
func runReportInboxDependencyHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	start := time.Date(2024, 2, 29, 16, 0, 0, 0, time.UTC)
	end := start.Add(19 * time.Hour)
	for _, id := range []uint{91, 92, 93} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: "Dependency account"}).Error)
	}
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Inbox{ID: id, AccountID: id, Name: "Dependency inbox", WebsiteToken: fmt.Sprintf("inbox-dependency-%d", id), ChannelType: "Channel::Email", Timezone: "UTC", WorkingHoursEnabled: true, WorkingHours: `[{"day_of_week":4,"open_hour":9,"close_hour":17},{"day_of_week":5,"open_hour":9,"close_hour":17}]`}).Error)
		require.NoError(t, db.Create(&domain.Contact{ID: id, AccountID: id, Name: "Dependency contact", CreatedAt: start}).Error)
	}
	tokens := map[string]string{}
	var adminID uint
	for _, principal := range []string{"admin", "agent", "foreign"} {
		user := domain.User{Name: principal, Email: principal + "@inbox-dependency.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		membership := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		if principal == "admin" {
			adminID = user.ID
			membership.Role = domain.RoleAdministrator
		} else if principal == "foreign" {
			membership.AccountID, membership.Role = 92, domain.RoleAdministrator
		}
		require.NoError(t, db.Create(&membership).Error)
		var err error
		tokens[principal], err = auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	require.NoError(t, db.Create(&domain.AccountUser{AccountID: 93, UserID: adminID, Role: domain.RoleAdministrator}).Error)
	require.NoError(t, db.Create(&domain.Team{ID: 91, AccountID: 91, Name: "Dependency team"}).Error)
	require.NoError(t, db.Create(&domain.TeamMember{TeamID: 91, UserID: adminID}).Error)
	require.NoError(t, db.Create(&domain.Label{ID: 91, AccountID: 91, Title: "Dependency label"}).Error)
	teamID := uint(91)
	for _, row := range []struct {
		id, account uint
		at          time.Time
	}{{101, 91, start}, {102, 91, end}, {900, 92, start}} {
		require.NoError(t, db.Create(&domain.Conversation{ID: row.id, DisplayID: row.id, AccountID: row.account, InboxID: row.account, ContactID: row.account, AssigneeID: &adminID, TeamID: &teamID, Status: domain.ConversationStatusResolved, CreatedAt: row.at, UpdatedAt: row.at.Add(18 * time.Hour), LastActivityAt: row.at.Add(18 * time.Hour)}).Error)
		require.NoError(t, db.Create(&domain.ConversationLabel{ConversationID: row.id, LabelID: 91}).Error)
	}
	for _, row := range []struct {
		account      uint
		kind, sender string
		at           time.Time
	}{{91, "incoming", "Contact", start}, {91, "outgoing", "User", start.Add(18 * time.Hour)}, {92, "outgoing", "User", start.Add(time.Minute)}} {
		require.NoError(t, db.Create(&domain.Message{AccountID: row.account, ConversationID: 101, SenderID: adminID, SenderType: row.sender, MessageType: row.kind, Content: "inbox-dependency-private-content", CreatedAt: row.at}).Error)
	}

	inRequest, failNext := false, false
	injected := 0
	const ownerFailure = "CHN-owner-internal-diagnostic-sentinel"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("report_inbox_dependency_http_failure", func(tx *gorm.DB) {
		if inRequest && failNext && tx.Statement.Table == "inboxes" {
			failNext = false
			injected++
			_ = tx.AddError(errors.New(ownerFailure))
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("report_inbox_dependency_http_failure")) })
	var writes []string
	for _, operation := range []string{"create", "update", "delete"} {
		operation := operation
		guard := func(tx *gorm.DB) {
			if inRequest {
				writes = append(writes, operation+":"+tx.Statement.Table)
				_ = tx.AddError(errors.New("dependency report must be read only"))
			}
		}
		switch operation {
		case "create":
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("report_inbox_dependency_http_read_only", guard))
		case "update":
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("report_inbox_dependency_http_read_only", guard))
		case "delete":
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("report_inbox_dependency_http_read_only", guard))
		}
	}
	t.Cleanup(func() { require.Empty(t, writes) })
	request := func(version, path, principal string, account uint, business, failure bool, accept string) *httptest.ResponseRecorder {
		q := url.Values{"since": {start.Format(time.RFC3339)}, "until": {end.Format(time.RFC3339)}, "business_hours": {strconv.FormatBool(business)}}
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/%s/accounts/%d%s?%s", version, account, path, q.Encode()), nil)
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		req.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		inRequest, failNext = true, failure
		defer func() { inRequest, failNext = false, false }()
		r.ServeHTTP(w, req)
		return w
	}
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			for _, business := range []bool{false, true} {
				mode := "ordinary"
				if business {
					mode = "business_hours"
				}
				t.Run(mode, func(t *testing.T) {
					for _, route := range reportInboxDependencyHTTPRoutes(version) {
						t.Run(route.name, func(t *testing.T) {
							before := request(version, route.path, "admin", 91, business, false, route.accept())
							require.Equal(t, http.StatusOK, before.Code, before.Body.String())
							priorFailures := injected
							failed := request(version, route.path, "admin", 91, business, true, route.accept())
							require.Equal(t, priorFailures+1, injected)
							require.Equal(t, http.StatusInternalServerError, failed.Code, failed.Body.String())
							require.Contains(t, failed.Header().Get("Content-Type"), "application/json")
							require.Empty(t, failed.Header().Get("Content-Disposition"), "failed CSV exports must not become attachments")
							require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, route.failure), failed.Body.String())
							require.NotContains(t, failed.Body.String(), ownerFailure)
							require.NotContains(t, failed.Body.String(), "inbox-dependency-private-content")
							after := request(version, route.path, "admin", 91, business, false, route.accept())
							require.Equal(t, before.Code, after.Code)
							require.Equal(t, before.Header().Get("Content-Type"), after.Header().Get("Content-Type"))
							require.Equal(t, before.Header().Get("Content-Disposition"), after.Header().Get("Content-Disposition"))
							if before.Header().Get("Content-Type") == "text/csv" {
								require.Equal(t, before.Body.String(), after.Body.String())
							} else {
								require.JSONEq(t, before.Body.String(), after.Body.String(), "recovery preserves complete response shape and values")
							}
							if route.name == "summary" || route.name == "conversations_summary_json" {
								var envelope struct {
									Data service.AccountSummaryReport `json:"data"`
								}
								require.NoError(t, json.Unmarshal(after.Body.Bytes(), &envelope))
								wantDuration := float64(64800)
								if business {
									wantDuration = 7200
								}
								require.Equal(t, int64(1), envelope.Data.ConversationsCount)
								require.Equal(t, int64(2), envelope.Data.TotalMessages)
								require.Equal(t, wantDuration, envelope.Data.AvgFirstResponseTime)
							}
						})
					}
				})
			}
			t.Run("permissions_before_dependency", func(t *testing.T) {
				for _, route := range reportInboxDependencyHTTPRoutes(version) {
					for _, principal := range []string{"", "agent", "foreign"} {
						before := injected
						w := request(version, route.path, principal, 91, true, true, route.accept())
						status := http.StatusForbidden
						if principal == "" {
							status = http.StatusUnauthorized
						}
						require.Equal(t, status, w.Code, "%s %s: %s", principal, route.path, w.Body.String())
						require.Equal(t, before, injected, "unauthorized requests must not reach CHN")
					}
				}
			})
		})
	}
	t.Run("healthy_empty_account", func(t *testing.T) {
		for _, version := range []string{"v1", "v2"} {
			for _, business := range []bool{false, true} {
				for _, route := range reportInboxDependencyHTTPRoutes(version) {
					w := request(version, route.path, "admin", 93, business, false, route.accept())
					require.Equal(t, http.StatusOK, w.Code, route.path+": "+w.Body.String())
					if route.name == "inboxes" {
						require.JSONEq(t, `{"success":true,"data":[]}`, w.Body.String())
					}
				}
			}
		}
	})
}
