package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func notificationMutationHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "notification-mutation-http-key-12345678", JWTExpirationHours: 24}
}
func TestNotificationMutationHTTP(t *testing.T) {
	cfg := notificationMutationHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "notification-http.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runNotificationMutationHTTP(t, cfg, db)
}
func runNotificationMutationHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: "Notification fixture"}).Error)
	}
	tokens := map[string]string{}
	users := map[string]uint{}
	for _, principal := range []string{"agent", "admin", "other_user", "foreign"} {
		user := domain.User{Name: principal, Email: principal + "@notification-http.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		users[principal] = user.ID
		member := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		if principal == "admin" {
			member.Role = domain.RoleAdministrator
		}
		if principal == "foreign" {
			member.AccountID = 92
		}
		require.NoError(t, db.Create(&member).Error)
		token, err := auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
		tokens[principal] = token
	}
	seed := func(account, user uint, read bool) domain.Notification {
		stamp := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
		n := domain.Notification{AccountID: account, UserID: user, NotificationType: domain.NotificationTypeSystemAlert, CreatedAt: stamp, UpdatedAt: stamp}
		if read {
			n.ReadAt = &stamp
		}
		require.NoError(t, db.Create(&n).Error)
		return n
	}
	rows := func() []domain.Notification {
		var all []domain.Notification
		require.NoError(t, db.Order("id").Find(&all).Error)
		return all
	}
	fault := ""
	hits, writes := 0, 0
	require.NoError(t, db.Callback().Update().After("gorm:update").Before("gorm:commit_or_rollback_transaction").Register("notification_http_update", func(tx *gorm.DB) {
		if tx.Statement.Table == "notifications" {
			writes++
			if fault == "update" {
				hits++
				_ = tx.AddError(errors.New("private-ntf-http-error"))
			}
		}
	}))
	require.NoError(t, db.Callback().Delete().After("gorm:delete").Before("gorm:commit_or_rollback_transaction").Register("notification_http_delete", func(tx *gorm.DB) {
		if tx.Statement.Table == "notifications" {
			writes++
			if fault == "delete" {
				hits++
				_ = tx.AddError(errors.New("private-ntf-http-error"))
			}
		}
	}))
	require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("notification_http_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "notifications" && (fault == "readback" || fault == "readback_missing") {
			hits++
			err := errors.New("private-ntf-http-error")
			if fault == "readback_missing" {
				err = gorm.ErrRecordNotFound
			}
			_ = tx.AddError(err)
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Update().Remove("notification_http_update"))
		require.NoError(t, db.Callback().Delete().Remove("notification_http_delete"))
		require.NoError(t, db.Callback().Query().Remove("notification_http_read"))
	})
	request := func(method, path, body, principal, kind string) *httptest.ResponseRecorder {
		fault, hits, writes = kind, 0, 0
		defer func() { fault = "" }()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out
	}
	base := "/api/v1/accounts/91/notifications"
	t.Run("authentication_and_membership", func(t *testing.T) {
		n := seed(91, users["agent"], false)
		before := rows()
		for _, tc := range []struct {
			name, principal string
			status          int
		}{{"unauthenticated", "", 401}, {"foreign_membership", "foreign", 403}} {
			t.Run(tc.name, func(t *testing.T) {
				out := request("PATCH", fmt.Sprintf("%s/%d", base, n.ID), `{"read":true}`, tc.principal, "update")
				require.Equal(t, tc.status, out.Code, out.Body.String())
				require.Zero(t, writes)
				require.Zero(t, hits)
				require.Equal(t, before, rows())
			})
		}
	})
	t.Run("current_user_scope", func(t *testing.T) {
		for _, target := range []string{"other_user", "other_account", "missing", "deleted"} {
			t.Run(target, func(t *testing.T) {
				id := uint(99999)
				switch target {
				case "other_user":
					id = seed(91, users["other_user"], false).ID
				case "other_account":
					id = seed(92, users["agent"], false).ID
				case "deleted":
					n := seed(91, users["agent"], false)
					id = n.ID
					require.NoError(t, db.Delete(&n).Error)
				}
				before := rows()
				out := request("PATCH", fmt.Sprintf("%s/%d", base, id), `{"read":true}`, "agent", "")
				require.Equal(t, 404, out.Code, out.Body.String())
				require.Equal(t, before, rows())
			})
		}
	})
	t.Run("storage_error_recovery", func(t *testing.T) {
		for _, tc := range []struct{ name, method, suffix, body, fault string }{{"patch_update", "PATCH", "", `{"read":true,"snoozed_until":"2035-01-02T03:04:05Z"}`, "update"}, {"patch_readback", "PATCH", "", `{"read":true,"snoozed_until":"2035-01-02T03:04:05Z"}`, "readback"}, {"patch_missing_readback", "PATCH", "", `{"read":true,"snoozed_until":"2035-01-02T03:04:05Z"}`, "readback_missing"}, {"read_readback", "POST", "/read", "", "readback"}, {"delete", "DELETE", "", "", "delete"}} {
			t.Run(tc.name, func(t *testing.T) {
				n := seed(91, users["agent"], false)
				before := rows()
				path := fmt.Sprintf("%s/%d%s", base, n.ID, tc.suffix)
				out := request(tc.method, path, tc.body, "agent", tc.fault)
				require.Equal(t, 500, out.Code, out.Body.String())
				require.JSONEq(t, `{"success":false,"error":"Notification storage unavailable"}`, out.Body.String())
				require.Equal(t, 1, hits)
				require.Equal(t, 1, writes)
				require.Equal(t, before, rows())
				out = request(tc.method, path, tc.body, "agent", "")
				require.Equal(t, 200, out.Code, out.Body.String())
				if tc.name != "delete" {
					var body struct {
						Success bool                 `json:"success"`
						Data    *domain.Notification `json:"data"`
					}
					require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
					require.True(t, body.Success)
					require.NotNil(t, body.Data)
					require.Equal(t, n.ID, body.Data.ID)
					require.NotNil(t, body.Data.ReadAt)
				}
			})
		}
	})
	t.Run("batch_aliases_validate_and_scope", func(t *testing.T) {
		for _, route := range []struct{ name, method, path string }{{"post", "POST", base + "/batch_destroy"}, {"delete", "DELETE", base}} {
			t.Run(route.name, func(t *testing.T) {
				n := seed(91, users["agent"], true)
				unread := seed(91, users["agent"], false)
				foreign := seed(91, users["other_user"], true)
				other := seed(92, users["agent"], true)
				before := rows()
				out := request(route.method, route.path, `{"only_read":"true"}`, "agent", "")
				require.Equal(t, 400, out.Code, out.Body.String())
				require.Zero(t, writes)
				require.Equal(t, before, rows())
				body := fmt.Sprintf(`{"ids":[%d,%d,%d,%d],"only_read":true}`, n.ID, unread.ID, foreign.ID, other.ID)
				out = request(route.method, route.path, body, "agent", "delete")
				require.Equal(t, 500, out.Code, out.Body.String())
				require.Equal(t, before, rows())
				out = request(route.method, route.path, body, "agent", "")
				require.Equal(t, 200, out.Code, out.Body.String())
				require.JSONEq(t, `{"success":true,"data":{"status":"ok","deleted_count":1}}`, out.Body.String())
				for _, id := range []uint{unread.ID, foreign.ID, other.ID} {
					var row domain.Notification
					require.NoError(t, db.First(&row, id).Error)
				}
			})
		}
	})
	t.Run("agent_and_admin_own_mutations", func(t *testing.T) {
		for _, principal := range []string{"agent", "admin"} {
			t.Run(principal, func(t *testing.T) {
				n := seed(91, users[principal], false)
				out := request("POST", fmt.Sprintf("%s/%d/read", base, n.ID), "", principal, "")
				require.Equal(t, 200, out.Code, out.Body.String())
				var stored domain.Notification
				require.NoError(t, db.First(&stored, n.ID).Error)
				require.NotNil(t, stored.ReadAt)
			})
		}
	})
	t.Run("snooze_invalid_body_preserves_state", func(t *testing.T) {
		n := seed(91, users["agent"], false)
		before := rows()
		out := request("POST", fmt.Sprintf("%s/%d/snooze", base, n.ID), `{"duration":20}`, "agent", "")
		require.Equal(t, 400, out.Code, out.Body.String())
		require.Zero(t, writes)
		require.Equal(t, before, rows())
		out = request("POST", fmt.Sprintf("%s/%d/snooze", base, n.ID), "", "agent", "")
		require.Equal(t, 200, out.Code, out.Body.String())
	})
}
