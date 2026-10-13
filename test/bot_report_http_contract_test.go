package test

import (
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
)

func TestBotReportHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "reports.db"), JWTSecret: "bot-report-contract-test-key-12345678", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	r := router.SetupRouter(cfg, db, ws.NewHub())
	require.NoError(t, db.Create(&domain.Account{ID: 91, Name: "Report account"}).Error)
	require.NoError(t, db.Create(&domain.Account{ID: 92, Name: "Foreign account"}).Error)
	role := domain.CustomRole{AccountID: 91, Name: "Report reader", Permissions: `["report_manage"]`}
	require.NoError(t, db.Create(&role).Error)
	tokens := map[string]string{}
	for _, kind := range []string{"administrator", "agent", "report_role", "foreign"} {
		user := domain.User{Name: kind, Email: kind + "@bot-report.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		member := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		switch kind {
		case "administrator":
			member.Role = domain.RoleAdministrator
		case "report_role":
			member.CustomRoleID = &role.ID
		case "foreign":
			member.AccountID = 92
			member.Role = domain.RoleAdministrator
		}
		require.NoError(t, db.Create(&member).Error)
		token, err := auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
		tokens[kind] = token
	}
	require.NoError(t, db.Create(&domain.Inbox{ID: 10, AccountID: 91, Name: "Bot", WebsiteToken: "bot-report-http"}).Error)
	require.NoError(t, db.Create(&domain.AgentBot{ID: 20, AccountID: 91, Name: "Bot"}).Error)
	require.NoError(t, db.Create(&domain.AgentBotInbox{AccountID: 91, InboxID: 10, AgentBotID: 20}).Error)
	at := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	for id := uint(1); id <= 3; id++ {
		require.NoError(t, db.Create(&domain.Conversation{ID: id, AccountID: 91, InboxID: 10, ContactID: 1, DisplayID: id, CreatedAt: at}).Error)
	}
	one, two := uint(1), uint(2)
	require.NoError(t, db.Create(&domain.ReportingEvent{AccountID: 91, ConversationID: &one, Name: "conversation_bot_resolved", CreatedAt: at}).Error)
	require.NoError(t, db.Create(&domain.ReportingEvent{AccountID: 91, ConversationID: &two, Name: "conversation_bot_handoff", CreatedAt: at}).Error)
	request := func(version, endpoint, token, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/%s/accounts/91/reports/%s%s", version, endpoint, query), nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, version := range []string{"v1", "v2"} {
		for _, endpoint := range []string{"bot_metrics", "bot_summary"} {
			t.Run(version+"/"+endpoint+"/authorization_and_shape", func(t *testing.T) {
				for _, kind := range []string{"administrator", "report_role"} {
					w := request(version, endpoint, tokens[kind], "")
					require.Equal(t, http.StatusOK, w.Code, w.Body.String())
					if endpoint == "bot_metrics" {
						require.JSONEq(t, `{"conversation_count":3,"message_count":0,"resolution_rate":33.3,"handoff_rate":33.3}`, w.Body.String())
					} else {
						require.JSONEq(t, `{"bot_resolutions_count":1,"bot_handoffs_count":1,"previous":{"bot_resolutions_count":0,"bot_handoffs_count":0}}`, w.Body.String())
					}
					empty := request(version, endpoint, tokens[kind], "?since=&until=&business_hours=1&type=inbox&id=999&timezone_offset=8")
					require.Equal(t, http.StatusOK, empty.Code)
					require.JSONEq(t, w.Body.String(), empty.Body.String(), "existing bot endpoints consume only time and business_hours; no new dimension semantics")
				}
				require.Equal(t, http.StatusForbidden, request(version, endpoint, tokens["agent"], "").Code)
				require.Equal(t, http.StatusForbidden, request(version, endpoint, tokens["foreign"], "").Code)
				require.Equal(t, http.StatusUnauthorized, request(version, endpoint, "", "").Code)
			})
		}
	}
	t.Run("invalid_event_returns_existing_error_envelope", func(t *testing.T) {
		require.NoError(t, db.Model(&domain.ReportingEvent{}).Where("account_id = ?", 91).Updates(map[string]any{"conversation_id": nil, "metadata_json": "private-bot-event-marker"}).Error)
		for _, endpoint := range []string{"bot_metrics", "bot_summary"} {
			w := request("v2", endpoint, tokens["administrator"], "")
			require.Equal(t, http.StatusInternalServerError, w.Code)
			message := "Failed to get bot metrics"
			if endpoint == "bot_summary" {
				message = "Failed to get bot summary"
			}
			require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, message), w.Body.String())
			require.NotContains(t, w.Body.String(), "private-bot-event-marker")
		}
	})
}
