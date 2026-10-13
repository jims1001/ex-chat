package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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

type explicitRetryHTTPTransport struct {
	calls atomic.Int64
	body  string
}

func (s *explicitRetryHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls.Add(1)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	s.body = string(b)
	return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
}
func webhookExplicitRetryHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "webhook-retry-test-1234567890123456", JWTExpirationHours: 24}
}
func TestWebhookExplicitRetryHTTP(t *testing.T) {
	cfg := webhookExplicitRetryHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "webhook-retry.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runWebhookExplicitRetryHTTP(t, cfg, db)
}
func runWebhookExplicitRetryHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	transport := &explicitRetryHTTPTransport{}
	r := router.SetupRouterWithOptions(cfg, db, ws.NewHub(), router.Options{WebhookHTTPClient: &http.Client{Transport: transport}})
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: "Webhook scope"}).Error)
	}
	tokens := map[string]string{}
	for _, principal := range []string{"admin", "agent", "foreign", "settings", "restricted"} {
		user := domain.User{Name: principal, Email: principal + "@webhook-retry.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		member := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		if principal == "admin" {
			member.Role = domain.RoleAdministrator
		}
		if principal == "foreign" {
			member.AccountID = 92
			member.Role = domain.RoleAdministrator
		}
		if principal == "settings" {
			role := domain.CustomRole{AccountID: 91, Name: "settings retry", Permissions: `["settings_manage"]`}
			require.NoError(t, db.Create(&role).Error)
			member.CustomRoleID = &role.ID
		}
		if principal == "restricted" {
			role := domain.CustomRole{AccountID: 91, Name: "restricted retry", Permissions: `["conversation_manage"]`}
			require.NoError(t, db.Create(&role).Error)
			member.CustomRoleID = &role.ID
		}
		require.NoError(t, db.Create(&member).Error)
		var err error
		tokens[principal], err = auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	wh := domain.Webhook{ID: 11, AccountID: 91, URL: "https://synthetic.invalid/retry", Secret: "synthetic-secret", Subscriptions: `["*"]`}
	require.NoError(t, db.Create(&wh).Error)
	target := domain.WebhookDelivery{ID: 101, AccountID: 91, WebhookID: 11, Event: "message.created", URL: wh.URL, Payload: `{"target":101}`, Attempts: 3, Status: "failed"}
	latest := target
	latest.ID = 102
	latest.Payload = `{"target":102}`
	foreign := target
	foreign.ID = 201
	foreign.AccountID = 92
	for _, del := range []domain.WebhookDelivery{target, latest, foreign} {
		require.NoError(t, db.Create(&del).Error)
	}
	snapshot := func() string {
		var ds []domain.WebhookDelivery
		var hooks []domain.Webhook
		require.NoError(t, db.Order("id").Find(&ds).Error)
		require.NoError(t, db.Order("id").Find(&hooks).Error)
		raw, err := json.Marshal([]any{ds, hooks})
		require.NoError(t, err)
		return string(raw)
	}
	var reads, writes int
	fault := false
	populated := false
	require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("retry_http_read", func(tx *gorm.DB) {
		if tx.Statement.Table == "webhook_deliveries" {
			reads++
			if fault {
				if d, ok := tx.Statement.Dest.(*domain.WebhookDelivery); ok && d.ID != 0 {
					populated = true
				}
				_ = tx.AddError(errors.New("private-retry-http-diagnostic"))
			}
		}
	}))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("retry_http_write_create", func(tx *gorm.DB) {
		if tx.Statement.Table == "webhook_deliveries" {
			writes++
		}
	}))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("retry_http_write_update", func(tx *gorm.DB) {
		if tx.Statement.Table == "webhook_deliveries" {
			writes++
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove("retry_http_read"))
		require.NoError(t, db.Callback().Create().Remove("retry_http_write_create"))
		require.NoError(t, db.Callback().Update().Remove("retry_http_write_update"))
	})
	for _, tc := range []struct {
		name, id, principal string
		want                int
		readFault           bool
	}{
		{"zero", "0", "admin", 400, false}, {"negative", "-1", "admin", 400, false}, {"text", "abc", "admin", 400, false}, {"overflow", "18446744073709551616", "admin", 400, false},
		{"missing", "999", "admin", 404, false}, {"foreign_delivery", "201", "admin", 404, false},
		{"empty_read_error", "999", "admin", 500, true}, {"populated_read_error", "101", "admin", 500, true},
		{"unauthenticated", "101", "", 401, true}, {"agent_fallback_read_error", "101", "agent", 500, true}, {"restricted_role_forbidden", "101", "restricted", 403, true}, {"foreign_membership", "101", "foreign", 403, true},
		{"settings_permission_read_error", "101", "settings", 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshot()
			reads, writes, populated = 0, 0, false
			fault = tc.readFault
			calls := transport.calls.Load()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/91/webhooks/deliveries/"+tc.id+"/retry", nil)
			if tc.principal != "" {
				req.Header.Set("Authorization", "Bearer "+tokens[tc.principal])
			}
			out := httptest.NewRecorder()
			r.ServeHTTP(out, req)
			fault = false
			require.Equal(t, tc.want, out.Code, out.Body.String())
			require.NotContains(t, out.Body.String(), "private-retry-http-diagnostic")
			require.Zero(t, writes)
			require.Equal(t, calls, transport.calls.Load())
			if tc.want == 400 || tc.want == 401 || tc.want == 403 {
				require.Zero(t, reads)
			} else {
				require.Equal(t, 1, reads)
			}
			if tc.name == "populated_read_error" || tc.name == "settings_permission_read_error" {
				require.True(t, populated)
			}
			require.Equal(t, before, snapshot())
		})
	}
	for _, name := range []string{"exact_target", "legacy_latest", "custom_permission", "agent_fallback"} {
		t.Run(name, func(t *testing.T) {
			id := fmt.Sprint(target.ID)
			principal := "admin"
			if name == "legacy_latest" {
				id = ""
			}
			if name == "custom_permission" {
				principal = "settings"
			}
			if name == "agent_fallback" {
				principal = "agent"
			}
			path := "/api/v1/accounts/91/webhooks/deliveries/"
			if id != "" {
				path += id + "/"
			}
			path += "retry"
			req := httptest.NewRequest(http.MethodPost, path, nil)
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
			out := httptest.NewRecorder()
			calls := transport.calls.Load()
			r.ServeHTTP(out, req)
			require.Equal(t, 200, out.Code, out.Body.String())
			require.Equal(t, calls+1, transport.calls.Load())
			var body struct {
				Success bool `json:"success"`
				Data    struct {
					Success      bool   `json:"success"`
					DeliveryID   uint   `json:"delivery_id"`
					AccountID    uint   `json:"account_id"`
					Status       string `json:"status"`
					ResponseCode int    `json:"response_code"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
			require.True(t, body.Success)
			require.True(t, body.Data.Success)
			require.Equal(t, uint(91), body.Data.AccountID)
			require.Equal(t, "delivered", body.Data.Status)
			require.Equal(t, 204, body.Data.ResponseCode)
			want := target
			if name == "legacy_latest" {
				want = latest
			}
			require.Equal(t, want.ID, body.Data.DeliveryID)
			require.Equal(t, want.Payload, transport.body)
		})
	}
}
