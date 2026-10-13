package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCSATMetricsDependencyHTTP(t *testing.T) {
	cfg := csatMetricsDependencyHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "csat-metrics-dependency.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runCSATMetricsDependencyHTTP(t, cfg, db)
}

func csatMetricsDependencyHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "csat-metrics-dependency-http-key-12345678", JWTExpirationHours: 24}
}

type csatMetricsDependencyHTTPRoute struct {
	name, version, path, failure string
	advanced                     bool
}

// The same fixture covers the actual router on SQLite and native PostgreSQL.
// Query and Row callbacks jointly count QLT reads: Count uses Query, while both
// Row().Scan and GORM Scan use Row. A one-shot late failure must discard earlier
// aggregates and must not be disguised by a later successful query.
func runCSATMetricsDependencyHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	for _, accountID := range []uint{91, 92, 93} {
		require.NoError(t, db.Create(&domain.Account{ID: accountID, Name: "CSAT dependency account"}).Error)
	}
	tokens := map[string]string{}
	users := map[string]domain.User{}
	roles := map[string]uint{}
	for _, principal := range []string{"admin", "agent", "report_view", "report_manage", "no_report", "foreign"} {
		user := domain.User{Name: principal, Email: principal + "@csat-dependency.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		users[principal] = user
		membership := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		switch principal {
		case "admin":
			membership.Role = domain.RoleAdministrator
		case "foreign":
			membership.AccountID, membership.Role = 92, domain.RoleAdministrator
		case "report_view", "report_manage", "no_report":
			permission := principal
			if principal == "no_report" {
				permission = domain.PermissionSettingsManage
			}
			role := domain.CustomRole{AccountID: 91, Name: principal, Permissions: `["` + permission + `"]`}
			require.NoError(t, db.Create(&role).Error)
			roles[principal] = role.ID
			membership.CustomRoleID = &role.ID
		}
		require.NoError(t, db.Create(&membership).Error)
		var err error
		tokens[principal], err = auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	adminID, agentID, foreignID := users["admin"].ID, users["agent"].ID, users["foreign"].ID
	require.NoError(t, db.Create(&domain.AccountUser{AccountID: 93, UserID: adminID, Role: domain.RoleAdministrator}).Error)
	at := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
	for _, accountID := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Contact{ID: accountID, AccountID: accountID, Name: "CSAT dependency contact"}).Error)
		require.NoError(t, db.Create(&domain.Inbox{ID: accountID, AccountID: accountID, Name: "CSAT dependency inbox", WebsiteToken: fmt.Sprintf("csat-dependency-%d", accountID)}).Error)
	}
	for _, row := range []struct {
		id, account uint
		agent       *uint
		rating      int
		review      string
	}{
		{101, 91, &adminID, 5, "approved"},
		{102, 91, &adminID, 4, "pending"},
		{103, 91, &agentID, 1, "rejected"},
		{104, 91, &adminID, 0, "pending"},
		{901, 92, &foreignID, 2, "approved"},
	} {
		require.NoError(t, db.Create(&domain.Conversation{ID: row.id, DisplayID: row.id, AccountID: row.account, InboxID: row.account, ContactID: row.account, AssigneeID: row.agent, Status: domain.ConversationStatusResolved, CreatedAt: at, UpdatedAt: at, LastActivityAt: at}).Error)
		require.NoError(t, db.Create(&domain.CSATSurvey{AccountID: row.account, ConversationID: row.id, AssignedAgentID: row.agent, Rating: row.rating, ReviewStatus: row.review, FeedbackText: "csat-dependency-private-feedback", ReviewNotes: "csat-dependency-private-review", CreatedAt: at, UpdatedAt: at}).Error)
	}

	var inRequest bool
	var failAt, injections int
	var qltReads []string
	const dependencyFailure = "QLT-csat-metrics-internal-diagnostic-sentinel"
	observe := func(operation string) func(*gorm.DB) {
		return func(tx *gorm.DB) {
			if !inRequest || tx.Statement.Table != "csat_surveys" {
				return
			}
			qltReads = append(qltReads, operation)
			if failAt > 0 && len(qltReads) == failAt {
				injections++
				_ = tx.AddError(errors.New(dependencyFailure))
			}
		}
	}
	const callback = "csat_metrics_dependency_http"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, observe("query")))
	require.NoError(t, db.Callback().Row().Before("gorm:row").Register(callback, observe("row")))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callback))
		require.NoError(t, db.Callback().Row().Remove(callback))
	})
	var writes []string
	for _, operation := range []string{"create", "update", "delete"} {
		operation := operation
		guard := func(tx *gorm.DB) {
			if inRequest {
				writes = append(writes, operation+":"+tx.Statement.Table)
				_ = tx.AddError(errors.New("CSAT metrics requests must remain read only"))
			}
		}
		switch operation {
		case "create":
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, guard))
		case "update":
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, guard))
		case "delete":
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callback, guard))
		}
	}
	t.Cleanup(func() {
		require.Empty(t, writes)
		require.NoError(t, db.Callback().Create().Remove(callback))
		require.NoError(t, db.Callback().Update().Remove(callback))
		require.NoError(t, db.Callback().Delete().Remove(callback))
	})
	request := func(route csatMetricsDependencyHTTPRoute, account uint, principal string, failureAt int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/%s/accounts/%d%s", route.version, account, route.path), nil)
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		req.Header.Set("Accept", "application/json")
		out := httptest.NewRecorder()
		inRequest, failAt, injections, qltReads = true, failureAt, 0, nil
		defer func() { inRequest, failAt = false, 0 }()
		r.ServeHTTP(out, req)
		return out
	}
	assertError := func(t *testing.T, out *httptest.ResponseRecorder, status int, message string) {
		t.Helper()
		require.Equal(t, status, out.Code, out.Body.String())
		require.Contains(t, out.Header().Get("Content-Type"), "application/json")
		require.Empty(t, out.Header().Get("Content-Disposition"))
		require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, message), out.Body.String(), "failure must not include data or partial aggregates")
		for _, private := range []string{dependencyFailure, "csat-dependency-private-feedback", "csat-dependency-private-review", tokens["admin"], "csat_surveys", "SQLSTATE"} {
			require.NotContains(t, out.Body.String(), private)
		}
	}
	assertHealthy := func(t *testing.T, route csatMetricsDependencyHTTPRoute, out *httptest.ResponseRecorder, empty bool) {
		t.Helper()
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		require.Contains(t, out.Header().Get("Content-Type"), "application/json")
		require.Empty(t, out.Header().Get("Content-Disposition"))
		if empty {
			want := `{"success":true,"data":{"total_responses":0,"average_rating":0,"distribution":null}}`
			if route.advanced {
				// AgentMetrics is nil and has omitempty; the HTTP field is absent.
				want = `{"success":true,"data":{"total_responses":0,"approved_responses":0,"pending_review":0,"rejected_responses":0,"average_rating":0,"satisfaction_rate":0,"rating_breakdown":{"1":0,"2":0,"3":0,"4":0,"5":0},"review_status_breakdown":{"approved":0,"pending":0,"rejected":0}}}`
			}
			require.JSONEq(t, want, out.Body.String())
			return
		}
		want := `{"success":true,"data":{"total_responses":4,"average_rating":2.5,"distribution":[{"rating":0,"count":1},{"rating":1,"count":1},{"rating":4,"count":1},{"rating":5,"count":1}]}}`
		arrayField := "distribution"
		if route.advanced {
			want = fmt.Sprintf(`{"success":true,"data":{"total_responses":3,"approved_responses":1,"pending_review":1,"rejected_responses":1,"average_rating":3.33,"satisfaction_rate":66.67,"rating_breakdown":{"1":1,"2":0,"3":0,"4":1,"5":1},"review_status_breakdown":{"approved":1,"pending":1,"rejected":1},"agent_metrics":[{"agent_id":%d,"agent_name":"admin","total_responses":2,"approved_responses":1,"average_rating":4.5,"satisfaction_rate":100},{"agent_id":%d,"agent_name":"agent","total_responses":1,"approved_responses":0,"average_rating":1,"satisfaction_rate":0}]}}`, adminID, agentID)
			arrayField = "agent_metrics"
		}
		var actual, expected map[string]any
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &actual))
		require.NoError(t, json.Unmarshal([]byte(want), &expected))
		actualData, ok := actual["data"].(map[string]any)
		require.True(t, ok)
		expectedData := expected["data"].(map[string]any)
		// Neither SQL GROUP BY query promises row order, especially on PostgreSQL.
		require.ElementsMatch(t, expectedData[arrayField], actualData[arrayField])
		delete(actualData, arrayField)
		delete(expectedData, arrayField)
		require.Equal(t, expected, actual, "preserve the complete success envelope and metric values")
	}

	for _, route := range []csatMetricsDependencyHTTPRoute{
		{"v1_reports_csat", "v1", "/reports/csat", "Failed to get csat report", false},
		{"v2_reports_csat", "v2", "/reports/csat", "Failed to get csat report", false},
		{"v1_csat_surveys_metrics", "v1", "/csat_surveys/metrics", "Failed to get csat metrics", true},
	} {
		t.Run(route.name, func(t *testing.T) {
			stages := []string{"total_count", "average", "rating_distribution"}
			operations := []string{"query", "row", "row"}
			emptyOperations := []string{"query", "row", "row"}
			if route.advanced {
				stages = []string{"total_count", "approved_count", "pending_count", "rejected_count", "average", "positive_count", "rating_distribution", "agent_aggregate"}
				operations = []string{"query", "query", "query", "query", "row", "query", "row", "row"}
				emptyOperations = []string{"query", "query", "query", "query", "row", "row"}
			}
			t.Run("healthy_contract", func(t *testing.T) {
				assertHealthy(t, route, request(route, 91, "admin", 0), false)
				require.Equal(t, operations, qltReads)
				assertHealthy(t, route, request(route, 93, "admin", 0), true)
				require.Equal(t, emptyOperations, qltReads, "a genuinely empty result still depends on healthy SQL reads")
				for _, principal := range []string{"report_view", "report_manage"} {
					assertHealthy(t, route, request(route, 91, principal, 0), false)
				}
			})
			t.Run("sentinel_failure_and_recovery", func(t *testing.T) {
				for i, stage := range stages {
					t.Run(stage, func(t *testing.T) {
						assertHealthy(t, route, request(route, 91, "admin", 0), false)
						failed := request(route, 91, "admin", i+1)
						require.Equal(t, 1, injections)
						require.Equal(t, operations[:i+1], qltReads, "stop before reading or returning any later partial metrics")
						assertError(t, failed, http.StatusInternalServerError, route.failure)
						assertHealthy(t, route, request(route, 91, "admin", 0), false)
						require.Equal(t, operations, qltReads)
						if route.advanced && (stage == "average" || stage == "positive_count") {
							return // These two aggregates run only when total responses is positive.
						}
						emptyFailureAt := i + 1
						if route.advanced && i >= 6 {
							emptyFailureAt -= 2
						}
						failed = request(route, 93, "admin", emptyFailureAt)
						require.Equal(t, 1, injections)
						require.Equal(t, emptyOperations[:emptyFailureAt], qltReads)
						assertError(t, failed, http.StatusInternalServerError, route.failure)
						assertHealthy(t, route, request(route, 93, "admin", 0), true)
					})
				}
			})
			t.Run("table_unavailable_and_recovery", func(t *testing.T) {
				require.NoError(t, db.Migrator().RenameTable("csat_surveys", "csat_surveys_offline"))
				restored := false
				defer func() {
					if !restored {
						require.NoError(t, db.Migrator().RenameTable("csat_surveys_offline", "csat_surveys"))
					}
				}()
				for _, account := range []uint{91, 93} {
					failed := request(route, account, "admin", 0)
					require.Zero(t, injections, "exercise a real SQL failure without callback injection")
					require.Equal(t, []string{"query"}, qltReads)
					assertError(t, failed, http.StatusInternalServerError, route.failure)
				}
				require.NoError(t, db.Migrator().RenameTable("csat_surveys_offline", "csat_surveys"))
				restored = true
				assertHealthy(t, route, request(route, 91, "admin", 0), false)
				assertHealthy(t, route, request(route, 93, "admin", 0), true)
			})
			t.Run("permissions_before_dependency", func(t *testing.T) {
				for _, tc := range []struct {
					principal, message string
					status             int
				}{
					{"", "Authorization header is required", http.StatusUnauthorized},
					{"foreign", "You do not have access to this account", http.StatusForbidden},
				} {
					assertError(t, request(route, 91, tc.principal, 1), tc.status, tc.message)
					require.Empty(t, qltReads, "authentication and membership denial must precede QLT reads")
					require.Zero(t, injections)
				}
				for _, principal := range []string{"agent", "no_report"} {
					if route.advanced {
						// This route currently allows every account member and has no report permission gate.
						assertHealthy(t, route, request(route, 91, principal, 0), false)
						assertError(t, request(route, 91, principal, 1), http.StatusInternalServerError, route.failure)
						require.Equal(t, 1, injections)
					} else {
						assertError(t, request(route, 91, principal, 1), http.StatusForbidden, "You do not have permission to perform this action")
						require.Empty(t, qltReads, "report permission denial must precede QLT reads")
						require.Zero(t, injections)
					}
				}
			})
			t.Run("membership_changes_and_permissions", func(t *testing.T) {
				assertHealthy(t, route, request(route, 91, "admin", 0), false)
				var membership domain.AccountUser
				require.NoError(t, db.Where("account_id = ? AND user_id = ?", 91, adminID).First(&membership).Error)
				require.NoError(t, db.Delete(&membership).Error)
				restored := false
				defer func() {
					if !restored {
						require.NoError(t, db.Create(&membership).Error)
					}
				}()
				assertError(t, request(route, 91, "admin", 1), http.StatusForbidden, "You do not have access to this account")
				require.Empty(t, qltReads, "membership removal must take effect before QLT reads even with an existing token")
				require.Zero(t, injections)
				require.NoError(t, db.Create(&membership).Error)
				restored = true
				assertHealthy(t, route, request(route, 91, "admin", 0), false)
				require.NoError(t, db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", 91, adminID).Update("role", domain.RoleAgent).Error)
				defer func() {
					require.NoError(t, db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", 91, adminID).Update("role", domain.RoleAdministrator).Error)
				}()
				if route.advanced {
					assertHealthy(t, route, request(route, 91, "admin", 0), false)
					assertError(t, request(route, 91, "admin", 1), http.StatusInternalServerError, route.failure)
					require.Equal(t, 1, injections, "demotion retains this route's ordinary member access")
				} else {
					assertError(t, request(route, 91, "admin", 1), http.StatusForbidden, "You do not have permission to perform this action")
					require.Empty(t, qltReads, "fresh membership must override a previously authorized request")
					require.Zero(t, injections)
				}
				// Revoke a custom role's report permission and reuse the same bearer token.
				require.NoError(t, db.Model(&domain.CustomRole{}).Where("id = ?", roles["report_view"]).Update("permissions", `[]`).Error)
				defer func() {
					require.NoError(t, db.Model(&domain.CustomRole{}).Where("id = ?", roles["report_view"]).Update("permissions", `["report_view"]`).Error)
				}()
				if route.advanced {
					assertHealthy(t, route, request(route, 91, "report_view", 0), false)
				} else {
					assertError(t, request(route, 91, "report_view", 1), http.StatusForbidden, "You do not have permission to perform this action")
					require.Empty(t, qltReads)
					require.Zero(t, injections)
				}
			})
		})
	}
}
