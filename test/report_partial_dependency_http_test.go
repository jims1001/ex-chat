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

func TestReportPartialDependencyHTTP(t *testing.T) {
	cfg := reportPartialDependencyHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "partial-dependency.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runReportPartialDependencyHTTP(t, cfg, db)
}

func reportPartialDependencyHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "partial-dependency-http-test-key-12345678", JWTExpirationHours: 24}
}

// Both database engines exercise the real router, IAM membership/permission
// checks, CON reads, and RPT annual aggregate. A single failed read must never
// produce a shortened agent array or a successful annual report with zero time.
func runReportPartialDependencyHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, accountID := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: accountID, Name: "Partial dependency account"}).Error)
		require.NoError(t, db.Create(&domain.Contact{ID: accountID, AccountID: accountID, Name: "Partial dependency contact", CreatedAt: start}).Error)
		require.NoError(t, db.Create(&domain.Inbox{ID: accountID, AccountID: accountID, Name: "Partial dependency inbox", WebsiteToken: fmt.Sprintf("partial-dependency-%d", accountID)}).Error)
	}
	tokens := map[string]string{}
	users := map[string]domain.User{}
	wantAgents := map[uint]service.AgentLiveConversationMetric{}
	for _, principal := range []string{"admin", "agent", "report_view", "report_manage", "foreign"} {
		user := domain.User{Name: principal, Email: principal + "@partial-dependency.example.test", Role: domain.RoleAgent, AvatarURL: "avatar-" + principal}
		require.NoError(t, db.Create(&user).Error)
		users[principal] = user
		membership := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent, Availability: domain.AvailabilityOnline}
		switch principal {
		case "admin":
			membership.Role, membership.Availability = domain.RoleAdministrator, domain.AvailabilityBusy
		case "agent":
			membership.Availability = domain.AvailabilityOffline
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
		if principal != "foreign" {
			wantAgents[user.ID] = service.AgentLiveConversationMetric{ID: user.ID, Name: user.Name, Email: user.Email, Thumbnail: user.AvatarURL, Availability: membership.Availability, Metric: service.LiveMetricsReport{Pending: 1}}
		}
	}
	admin, agent := users["admin"].ID, users["agent"].ID
	viewer, manager, foreign := users["report_view"].ID, users["report_manage"].ID, users["foreign"].ID
	seen := start.Add(time.Minute)
	for _, row := range []struct {
		id, account uint
		assignee    *uint
		status      string
		at          time.Time
		seen        *time.Time
		unread      int
	}{
		{101, 91, &admin, domain.ConversationStatusResolved, start.Add(-time.Microsecond), nil, 0},
		{102, 91, &admin, domain.ConversationStatusOpen, start, nil, 0},
		{103, 91, &admin, domain.ConversationStatusOpen, start.Add(time.Hour), &seen, 0},
		{104, 91, &admin, domain.ConversationStatusResolved, end.Add(-time.Microsecond), nil, 0},
		{105, 91, &admin, domain.ConversationStatusResolved, end, nil, 0},
		{106, 91, &admin, domain.ConversationStatusResolved, end.Add(time.Microsecond), nil, 0},
		{201, 91, &agent, domain.ConversationStatusOpen, start, &seen, 1},
		{202, 91, &viewer, domain.ConversationStatusPending, start, nil, 0},
		{203, 91, &manager, domain.ConversationStatusSnoozed, start, nil, 0},
		{204, 91, nil, domain.ConversationStatusOpen, start, nil, 0},
		{901, 92, &admin, domain.ConversationStatusOpen, start, nil, 1},
		{902, 92, &foreign, domain.ConversationStatusPending, start, nil, 0},
	} {
		require.NoError(t, db.Create(&domain.Conversation{ID: row.id, DisplayID: row.id, AccountID: row.account, InboxID: row.account, ContactID: row.account, AssigneeID: row.assignee, Status: row.status, CreatedAt: row.at, UpdatedAt: row.at, LastActivityAt: row.at, AgentLastSeenAt: row.seen, UnreadCount: row.unread}).Error)
	}
	for id, metric := range map[uint]service.LiveMetricsReport{
		admin: {Open: 2, Unattended: 1, Pending: 1},
		agent: {Open: 1, Unattended: 1, Pending: 1},
	} {
		row := wantAgents[id]
		row.Metric = metric
		wantAgents[id] = row
	}
	for _, row := range []struct {
		account uint
		user    *uint
		name    string
		value   float64
		at      time.Time
	}{
		{91, &admin, "first_response", 2000, start.Add(-time.Microsecond)},
		{91, &admin, "first_response", 10.75, start},
		{91, &admin, "first_response", 30.75, end.Add(-time.Microsecond)},
		{91, &admin, "first_response", 2000, end},
		{91, &admin, "first_response", 2000, end.Add(time.Microsecond)},
		{91, &admin, "resolution", 2000, start},
		{91, nil, "first_response", 2000, start},
		{91, &agent, "first_response", 80, start},
		{92, &admin, "first_response", 2000, start},
	} {
		require.NoError(t, db.Create(&domain.ReportingEvent{AccountID: row.account, UserID: row.user, Name: row.name, Value: row.value, CreatedAt: row.at, MetadataJSON: `{"private":"partial-dependency-private-event"}`}).Error)
	}

	var inRequest, failRPT bool
	var failCONAt, conReads, rptReads, injections int
	const conFailure = "CON-partial-dependency-internal-diagnostic-sentinel"
	const rptFailure = "RPT-partial-dependency-internal-diagnostic-sentinel"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("report_partial_dependency_http_con", func(tx *gorm.DB) {
		if inRequest && tx.Statement.Table == "conversations" {
			conReads++
			if failCONAt > 0 && conReads == failCONAt {
				injections++
				_ = tx.AddError(errors.New(conFailure))
			}
		}
	}))
	require.NoError(t, db.Callback().Row().Before("gorm:row").Register("report_partial_dependency_http_rpt", func(tx *gorm.DB) {
		if inRequest && tx.Statement.Table == "reporting_events" {
			rptReads++
			if failRPT {
				failRPT = false
				injections++
				_ = tx.AddError(errors.New(rptFailure))
			}
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove("report_partial_dependency_http_con"))
		require.NoError(t, db.Callback().Row().Remove("report_partial_dependency_http_rpt"))
	})
	var writes []string
	for _, operation := range []string{"create", "update", "delete"} {
		operation := operation
		guard := func(tx *gorm.DB) {
			if inRequest {
				writes = append(writes, operation+":"+tx.Statement.Table)
				_ = tx.AddError(errors.New("partial dependency reports must be read only"))
			}
		}
		switch operation {
		case "create":
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("report_partial_dependency_http_read_only", guard))
		case "update":
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("report_partial_dependency_http_read_only", guard))
		case "delete":
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("report_partial_dependency_http_read_only", guard))
		}
	}
	t.Cleanup(func() {
		require.Empty(t, writes)
		require.NoError(t, db.Callback().Create().Remove("report_partial_dependency_http_read_only"))
		require.NoError(t, db.Callback().Update().Remove("report_partial_dependency_http_read_only"))
		require.NoError(t, db.Callback().Delete().Remove("report_partial_dependency_http_read_only"))
	})
	request := func(version, path, principal string, q url.Values, conFailureAt int, rptFailure bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/"+version+"/accounts/91"+path+"?"+q.Encode(), nil)
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		inRequest, failCONAt, failRPT = true, conFailureAt, rptFailure
		conReads, rptReads, injections = 0, 0, 0
		defer func() { inRequest, failCONAt, failRPT = false, 0, false }()
		r.ServeHTTP(w, req)
		return w
	}
	assertFailure := func(t *testing.T, w *httptest.ResponseRecorder, message string) {
		t.Helper()
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		require.Empty(t, w.Header().Get("Content-Disposition"))
		require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, message), w.Body.String())
		for _, private := range []string{conFailure, rptFailure, "partial-dependency-private-event", tokens["admin"]} {
			require.NotContains(t, w.Body.String(), private)
		}
	}
	readAgents := func(t *testing.T, w *httptest.ResponseRecorder) []service.AgentLiveConversationMetric {
		t.Helper()
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		var got []service.AgentLiveConversationMetric
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got), "healthy agent report remains a bare array")
		require.NotNil(t, got, "empty arrays must not become null")
		encoded, err := json.Marshal(got)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), w.Body.String(), "preserve every public field")
		return got
	}
	assertFullAgents := func(t *testing.T, w *httptest.ResponseRecorder) []service.AgentLiveConversationMetric {
		t.Helper()
		got := readAgents(t, w)
		require.Len(t, got, len(wantAgents))
		byID := map[uint]service.AgentLiveConversationMetric{}
		for _, row := range got {
			byID[row.ID] = row
		}
		require.Equal(t, wantAgents, byID, "all local members and their complete metrics must survive recovery")
		require.NotContains(t, w.Body.String(), users["foreign"].Email)
		return got
	}
	assertReview := func(t *testing.T, w *httptest.ResponseRecorder, want string) {
		t.Helper()
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		require.JSONEq(t, want, w.Body.String(), "annual report remains a bare object with unchanged fields")
		require.NotContains(t, w.Body.String(), "partial-dependency-private-event")
	}
	permissions := func(t *testing.T, version, path string, q url.Values) {
		t.Helper()
		for _, tc := range []struct {
			principal, message string
			status             int
		}{
			{"", "Authorization header is required", http.StatusUnauthorized},
			{"agent", "You do not have permission to perform this action", http.StatusForbidden},
			{"foreign", "You do not have access to this account", http.StatusForbidden},
		} {
			w := request(version, path, tc.principal, q, 1, true)
			require.Equal(t, tc.status, w.Code, tc.principal+": "+w.Body.String())
			require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, tc.message), w.Body.String())
			require.Zero(t, conReads, "IAM denial must precede CON access")
			require.Zero(t, rptReads, "IAM denial must precede RPT access")
			require.Zero(t, injections)
		}
	}
	const populatedReview = `{"year":2024,"total_conversations":3,"busiest_day":{"date":"Jan 01","count":2},"support_personality":{"avg_response_time_seconds":20}}`
	const emptyReview = `{"year":2022,"total_conversations":0,"busiest_day":null,"support_personality":{"avg_response_time_seconds":0}}`
	annualTests := func(t *testing.T, version, path string) {
		t.Run("sql_failure_and_recovery", func(t *testing.T) {
			for _, year := range []string{"2024", "2022"} {
				q := url.Values{"year": {year}}
				want := populatedReview
				if year == "2022" {
					want = emptyReview
				}
				assertReview(t, request(version, path, "admin", q, 0, false), want)
				failed := request(version, path, "admin", q, 0, true)
				require.Equal(t, 1, conReads)
				require.Equal(t, 1, rptReads)
				require.Equal(t, 1, injections)
				assertFailure(t, failed, "Failed to get year in review")
				assertReview(t, request(version, path, "admin", q, 0, false), want)
			}
			// Also cover a real SQL error, independent of callback injection.
			// Change only this disposable fixture between requests, then restore
			// the table so the same route proves full recovery.
			require.NoError(t, db.Migrator().RenameTable("reporting_events", "reporting_events_offline"))
			restored := false
			defer func() {
				if !restored {
					require.NoError(t, db.Migrator().RenameTable("reporting_events_offline", "reporting_events"))
				}
			}()
			for _, year := range []string{"2024", "2022"} {
				failed := request(version, path, "admin", url.Values{"year": {year}}, 0, false)
				require.Equal(t, 1, conReads)
				require.Equal(t, 1, rptReads)
				require.Zero(t, injections)
				assertFailure(t, failed, "Failed to get year in review")
			}
			require.NoError(t, db.Migrator().RenameTable("reporting_events_offline", "reporting_events"))
			restored = true
			assertReview(t, request(version, path, "admin", url.Values{"year": {"2024"}}, 0, false), populatedReview)
			assertReview(t, request(version, path, "admin", url.Values{"year": {"2022"}}, 0, false), emptyReview)
		})
		t.Run("empty_average_and_empty_year", func(t *testing.T) {
			q := url.Values{"year": {"2024"}, "user_id": {strconv.FormatUint(uint64(manager), 10)}}
			assertReview(t, request(version, path, "admin", q, 0, false), `{"year":2024,"total_conversations":1,"busiest_day":{"date":"Jan 01","count":1},"support_personality":{"avg_response_time_seconds":0}}`)
			assertReview(t, request(version, path, "admin", url.Values{"year": {"2022"}}, 0, false), emptyReview)
			require.Equal(t, 1, rptReads, "an empty cohort still performs a healthy aggregate")
		})
		t.Run("user_tenant_time_scope", func(t *testing.T) {
			q := url.Values{"year": {"2024"}}
			assertReview(t, request(version, path, "admin", q, 0, false), populatedReview)
			q.Set("user_id", strconv.FormatUint(uint64(agent), 10))
			assertReview(t, request(version, path, "admin", q, 0, false), `{"year":2024,"total_conversations":1,"busiest_day":{"date":"Jan 01","count":1},"support_personality":{"avg_response_time_seconds":80}}`)
			q.Set("user_id", strconv.FormatUint(uint64(foreign), 10))
			assertReview(t, request(version, path, "admin", q, 0, false), `{"year":2024,"total_conversations":0,"busiest_day":null,"support_personality":{"avg_response_time_seconds":0}}`)
			q.Set("user_id", strconv.FormatUint(uint64(admin), 10))
			for _, principal := range []string{"report_view", "report_manage"} {
				assertReview(t, request(version, path, principal, q, 0, false), populatedReview)
			}
		})
		t.Run("permissions_before_dependency", func(t *testing.T) {
			permissions(t, version, path, url.Values{"year": {"2024"}})
		})
	}
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			t.Run("agent_conversations", func(t *testing.T) {
				const path = "/reports/conversations"
				q := url.Values{"type": {"agent"}}
				for _, tc := range []struct {
					name   string
					readAt int
				}{
					{"first_live_read", 1}, {"first_agent_read", 2},
					{"late_live_read", 3}, {"late_agent_read", 4},
				} {
					t.Run(tc.name, func(t *testing.T) {
						before := request(version, path, "admin", q, 0, false)
						assertFullAgents(t, before)
						require.Equal(t, 2*len(wantAgents), conReads)
						failed := request(version, path, "admin", q, tc.readAt, false)
						require.Equal(t, 1, injections)
						require.Equal(t, tc.readAt, conReads, "stop immediately even after earlier agents were computed")
						assertFailure(t, failed, "Failed to get conversations report")
						after := request(version, path, "admin", q, 0, false)
						assertFullAgents(t, after)
						require.JSONEq(t, before.Body.String(), after.Body.String())
					})
				}
				t.Run("healthy_contract", func(t *testing.T) {
					full := assertFullAgents(t, request(version, path, "admin", q, 0, false))
					for _, principal := range []string{"report_view", "report_manage"} {
						assertFullAgents(t, request(version, path, principal, q, 0, false))
					}
					assertFullAgents(t, request(version, path, "admin", url.Values{"type": {"AGENT"}, "page": {"0"}, "per_page": {"0"}}, 0, false))
					page := readAgents(t, request(version, path, "admin", url.Values{"type": {"agent"}, "page": {"2"}, "per_page": {"1"}}, 0, false))
					require.Equal(t, full[1:2], page)
					empty := request(version, path, "admin", url.Values{"type": {"agent"}, "page": {"999"}}, 0, false)
					require.Empty(t, readAgents(t, empty))
					require.JSONEq(t, `[]`, empty.Body.String())
					require.Zero(t, conReads)
					for _, reportType := range []string{"", "account"} {
						w := request(version, path, "admin", url.Values{"type": {reportType}}, 0, false)
						require.Equal(t, http.StatusOK, w.Code, w.Body.String())
						require.JSONEq(t, `{"open":4,"unattended":3,"unassigned":1,"pending":1}`, w.Body.String())
					}
				})
				t.Run("permissions_before_dependency", func(t *testing.T) {
					permissions(t, version, path, q)
				})
			})
			t.Run("year_in_review", func(t *testing.T) {
				annualTests(t, version, "/reports/year_in_review")
			})
			if version == "v2" {
				t.Run("year_in_review_alias", func(t *testing.T) {
					annualTests(t, version, "/year_in_review")
				})
			}
		})
	}
}
