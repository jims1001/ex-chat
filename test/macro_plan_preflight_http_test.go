package test

import (
	"encoding/json"
	"fmt"
	"net/http"
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

func macroPlanHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "macro-plan-http-local-test-secret-123456", JWTExpirationHours: 24, AutomationExecutionMode: "durable"}
}
func TestMacroPlanPreflightHTTP(t *testing.T) {
	cfg := macroPlanHTTPConfig()
	cfg.DBDriver, cfg.DBPath = "sqlite", filepath.Join(t.TempDir(), "macro-plan-http.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runMacroPlanPreflightHTTP(t, cfg, db)
}
func runMacroPlanPreflightHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r, stop := router.SetupRouterWithLifecycle(cfg, db, ws.NewHub(), router.Options{})
	t.Cleanup(stop)
	stores := map[string]*gorm.DB{"monolith": db}
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: "Macro HTTP account"}).Error)
	}
	tokens := map[string]string{}
	for i, principal := range []string{"owner", "other", "admin", "foreign"} {
		u := domain.User{ID: uint(91 + i), Name: principal, Email: principal + "@macro-plan.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&u).Error)
		member := domain.AccountUser{AccountID: 91, UserID: u.ID, Role: domain.RoleAgent}
		if principal == "admin" || principal == "foreign" {
			member.Role = domain.RoleAdministrator
		}
		if principal == "foreign" {
			member.AccountID = 92
		}
		require.NoError(t, db.Create(&member).Error)
		token, err := auth.GenerateToken(&u, cfg.JWTSecret, 24)
		require.NoError(t, err)
		tokens[principal] = token
	}
	inbox := domain.Inbox{AccountID: 91, Name: "Macro inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "macro-plan-widget"}
	contact := domain.Contact{AccountID: 91, Name: "Macro contact"}
	require.NoError(t, db.Create(&inbox).Error)
	require.NoError(t, db.Create(&contact).Error)
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	var conversations []domain.Conversation
	for _, id := range []uint{1, 2} {
		c := domain.Conversation{DisplayID: id, AccountID: 91, InboxID: inbox.ID, ContactID: contact.ID, Status: "open", Priority: "low", ObjectVersion: 7, LastActivityAt: at, CreatedAt: at, UpdatedAt: at}
		require.NoError(t, db.Create(&c).Error)
		conversations = append(conversations, c)
	}
	// Preserve a real pre-existing AUT record, including all execution fields.
	rule := domain.AutomationRule{AccountID: 91, Name: "Existing automation", EventName: "conversation_updated", Actions: "[]", Conditions: "[]"}
	require.NoError(t, db.Create(&rule).Error)
	require.NoError(t, db.Create(&domain.AutomationRuleExecution{AccountID: 91, RuleID: rule.ID, ConversationID: conversations[0].ID, EventName: "conversation_updated", Status: "succeeded", TriggeredAt: at, ActionResults: "[]", RuleVersion: 1, TargetVersion: 7}).Error)
	persist := func(t *testing.T, raw, visibility string) domain.Macro {
		t.Helper()
		m := domain.Macro{AccountID: 91, Name: "Legacy persisted macro", Visibility: visibility, CreatedBy: 91, Actions: raw}
		require.NoError(t, db.Create(&m).Error)
		return m
	}
	request := func(t *testing.T, principal, path string, payload any) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(macroPlanJSON(t, payload)))
		req.Header.Set("Content-Type", "application/json")
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out
	}
	path := func(m domain.Macro) string { return fmt.Sprintf("/api/v1/accounts/91/macros/%d/execute", m.ID) }
	payload := map[string]any{"conversation_ids": []uint{conversations[0].ID, conversations[1].ID}}
	t.Run("reject_persisted_plan_without_any_effects", func(t *testing.T) {
		for _, action := range []string{"send_email_transcript", "send_webhook_event", "future_macro_action"} {
			t.Run(action, func(t *testing.T) {
				m := persist(t, macroPlanJSON(t, []any{macroPlanAction("add_label", "no-http-label"), macroPlanAction("send_message", "no-http-message"), macroPlanAction("send_attachment", "https://example.test/no-http.bin"), macroPlanAction("resolve"), macroPlanAction(action)}), "global")
				before := macroPlanSnapshot(t, stores)
				counts := macroPlanObserve(t, stores)
				out := request(t, "owner", path(m), payload)
				require.Equal(t, http.StatusInternalServerError, out.Code, out.Body.String())
				require.JSONEq(t, macroPlanJSON(t, map[string]any{"success": false, "error": fmt.Sprintf("unsupported macro action %q", action)}), out.Body.String())
				require.NotContains(t, out.Body.String(), "results")
				macroPlanAssertUnchanged(t, stores, before, counts)
			})
		}
	})
	t.Run("authority_and_visibility_remain_first", func(t *testing.T) {
		for _, tc := range []struct {
			name, principal string
			status          int
			want            string
		}{
			{"unauthenticated", "", 401, ""}, {"foreign_membership", "foreign", 403, ""},
			{"other_personal_owner", "other", 500, "macro not found or not accessible"},
			{"personal_owner", "owner", 500, "unsupported macro action"}, {"personal_admin", "admin", 500, "unsupported macro action"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				m := persist(t, `[{"name":"send_webhook_event"}]`, "personal")
				before := macroPlanSnapshot(t, stores)
				counts := macroPlanObserve(t, stores)
				out := request(t, tc.principal, path(m), payload)
				require.Equal(t, tc.status, out.Code, out.Body.String())
				if tc.want != "" {
					require.Contains(t, out.Body.String(), tc.want)
				}
				if tc.principal == "other" {
					require.NotContains(t, out.Body.String(), "unsupported")
				}
				macroPlanAssertUnchanged(t, stores, before, counts)
			})
		}
	})
	t.Run("empty_plan_existing_error_envelope", func(t *testing.T) {
		m := persist(t, "[]", "global")
		before := macroPlanSnapshot(t, stores)
		counts := macroPlanObserve(t, stores)
		out := request(t, "owner", path(m), payload)
		require.Equal(t, 500, out.Code, out.Body.String())
		require.JSONEq(t, `{"success":false,"error":"invalid macro actions: no executable actions found"}`, out.Body.String())
		macroPlanAssertUnchanged(t, stores, before, counts)
	})
	t.Run("external_creation_contract_unchanged", func(t *testing.T) {
		for _, action := range []string{"send_email_transcript", "send_webhook_event"} {
			t.Run(action, func(t *testing.T) {
				out := request(t, "admin", "/api/v1/accounts/91/macros", map[string]any{"name": "Configured external macro", "visibility": "global", "actions": macroPlanJSON(t, []any{macroPlanAction(action)})})
				require.Equal(t, 201, out.Code, out.Body.String())
				var body struct {
					Data domain.Macro `json:"data"`
				}
				require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
				require.NotZero(t, body.Data.ID)
				before := macroPlanSnapshot(t, stores)
				counts := macroPlanObserve(t, stores)
				out = request(t, "admin", path(body.Data), payload)
				require.Equal(t, 500, out.Code, out.Body.String())
				require.Contains(t, out.Body.String(), "unsupported macro action")
				macroPlanAssertUnchanged(t, stores, before, counts)
			})
		}
	})
	t.Run("valid_plan_existing_success_envelope", func(t *testing.T) {
		m := persist(t, `{"values":[{"action_name":"add_private_note","params":["valid local note"]},{"name":"resolve"}]}`, "personal")
		out := request(t, "owner", path(m), map[string]any{"conversation_id": conversations[0].ID})
		require.Equal(t, 200, out.Code, out.Body.String())
		var body struct {
			Success bool `json:"success"`
			Data    struct {
				Status    string          `json:"status"`
				Total     int             `json:"total"`
				Succeeded int             `json:"succeeded"`
				Failed    int             `json:"failed"`
				Results   map[uint]string `json:"results"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
		require.True(t, body.Success)
		require.Equal(t, "success", body.Data.Status)
		require.Equal(t, 1, body.Data.Total)
		require.Equal(t, 1, body.Data.Succeeded)
		require.Zero(t, body.Data.Failed)
		require.Equal(t, map[uint]string{conversations[0].ID: "success"}, body.Data.Results)
		var stored domain.Conversation
		require.NoError(t, db.First(&stored, conversations[0].ID).Error)
		require.Equal(t, "resolved", stored.Status)
		var notes []domain.Message
		require.NoError(t, db.Where("conversation_id = ?", stored.ID).Find(&notes).Error)
		require.Len(t, notes, 1)
		require.Equal(t, "valid local note", notes[0].Content)
		require.True(t, notes[0].Private)
	})
}
