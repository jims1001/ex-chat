package test

import (
	"bytes"
	"encoding/json"
	"errors"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func slaSourceHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "sla-source-http-regression-12345678", JWTExpirationHours: 24}
}
func TestSLASourceReadHTTP(t *testing.T) {
	cfg := slaSourceHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "sla-source-http.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runSLASourceReadHTTP(t, cfg, db)
}

type slaHTTPFailure struct {
	name, table string
	at          int
}

func slaHTTPFailures(list bool) []slaHTTPFailure {
	out := []slaHTTPFailure{{"policy_preload", "sla_policies", 1}, {"policy_deadline", "sla_policies", 2}, {"inbox", "inboxes", 1}, {"timeline", "messages", 1}, {"first_breach", "sla_breach_logs", 1}, {"next_breach", "sla_breach_logs", 2}, {"resolution_breach", "sla_breach_logs", 3}, {"applied", "applied_slas", 1}, {"events", "sla_events", 1}, {"conversation", "conversations", 1}}
	if list {
		out = append(out, slaHTTPFailure{"second_policy", "sla_policies", 3}, slaHTTPFailure{"second_inbox", "inboxes", 2}, slaHTTPFailure{"second_timeline", "messages", 2}, slaHTTPFailure{"second_first_breach", "sla_breach_logs", 4}, slaHTTPFailure{"second_next_breach", "sla_breach_logs", 5}, slaHTTPFailure{"second_resolution_breach", "sla_breach_logs", 6}, slaHTTPFailure{"applied_rows", "applied_slas", 2}, slaHTTPFailure{"second_conversation", "conversations", 2})
	}
	return out
}

func runSLASourceReadHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	admin := domain.User{Name: "SLA admin", Email: "sla-source-admin@example.test"}
	require.NoError(t, db.Create(&admin).Error)
	agent := domain.User{Name: "SLA agent", Email: "sla-source-agent@example.test"}
	require.NoError(t, db.Create(&agent).Error)
	token, err := auth.GenerateToken(&admin, cfg.JWTSecret, 24)
	require.NoError(t, err)
	agentToken, err := auth.GenerateToken(&agent, cfg.JWTSecret, 24)
	require.NoError(t, err)
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	seedAccount := func(account uint) {
		require.NoError(t, db.Create(&domain.Account{ID: account, Name: "SLA source account"}).Error)
		require.NoError(t, db.Create(&domain.AccountUser{AccountID: account, UserID: admin.ID, Role: domain.RoleAdministrator}).Error)
		require.NoError(t, db.Create(&domain.Contact{ID: account, AccountID: account, Name: "SLA customer"}).Error)
		require.NoError(t, db.Create(&domain.Inbox{ID: account, AccountID: account, Name: "SLA inbox", WebsiteToken: fmt.Sprintf("sla-source-%d", account), ChannelType: "Channel::WebWidget"}).Error)
	}
	seedAccount(91)
	seedAccount(92)
	require.NoError(t, db.Create(&domain.AccountUser{AccountID: 91, UserID: agent.ID, Role: domain.RoleAgent}).Error)
	policy := domain.SLAPolicy{AccountID: 91, Name: "SLA source policy", FirstResponseTimeThreshold: 60, NextResponseTimeThreshold: 60, ResolutionTimeThreshold: 7200}
	require.NoError(t, db.Create(&policy).Error)
	for _, id := range []uint{1, 2, 3} {
		conv := domain.Conversation{ID: id, DisplayID: id, AccountID: 91, InboxID: 91, ContactID: 91, Status: domain.ConversationStatusOpen, SLAPolicyID: &policy.ID, CreatedAt: start, UpdatedAt: start, LastActivityAt: start}
		require.NoError(t, db.Create(&conv).Error)
		require.NoError(t, db.Create(&domain.Message{AccountID: 91, ConversationID: id, MessageType: "incoming", SenderType: "Contact", Status: "sent", CreatedAt: start}).Error)
		require.NoError(t, db.Create(&domain.Message{AccountID: 91, ConversationID: id, MessageType: "outgoing", SenderType: "User", Status: "sent", CreatedAt: start.Add(30 * time.Second)}).Error)
		if id != 3 {
			_, err := repository.NewAppliedSLARepository(db).EnsureAppliedSLA(91, id, policy.ID)
			require.NoError(t, err)
		}
	}
	require.NoError(t, db.Create(&domain.Conversation{ID: 902, DisplayID: 902, AccountID: 92, InboxID: 92, ContactID: 92, Status: domain.ConversationStatusOpen, CreatedAt: start}).Error)
	var active bool
	var fault slaHTTPFailure
	var seen, injections, writes int
	const sentinel = "SLA-internal-SQL-source-sentinel"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("sla_http_read_failure", func(tx *gorm.DB) {
		if active && fault.table != "" && tx.Statement.Table == fault.table {
			seen++
			if seen == fault.at {
				injections++
				_ = tx.AddError(errors.New(sentinel))
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("sla_http_read_failure")) })
	recordWrite := func(*gorm.DB) {
		if active {
			writes++
		}
	}
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("sla_http_read_writes", recordWrite))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("sla_http_read_writes", recordWrite))
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("sla_http_read_writes", recordWrite))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Create().Remove("sla_http_read_writes"))
		require.NoError(t, db.Callback().Update().Remove("sla_http_read_writes"))
		require.NoError(t, db.Callback().Delete().Remove("sla_http_read_writes"))
	})
	request := func(method, path, authToken, body string, fail slaHTTPFailure) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		out := httptest.NewRecorder()
		active, fault, seen, injections, writes = true, fail, 0, 0, 0
		defer func() { active = false }()
		r.ServeHTTP(out, req)
		return out
	}
	assertFailure := func(t *testing.T, out *httptest.ResponseRecorder, status int) {
		t.Helper()
		require.Equal(t, status, out.Code, out.Body.String())
		require.NotContains(t, out.Body.String(), sentinel)
		require.NotContains(t, out.Body.String(), "SQLSTATE")
		var body map[string]any
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
		require.Equal(t, false, body["success"])
		require.Nil(t, body["data"])
		require.Nil(t, body["payload"])
		require.NotEmpty(t, body["error"])
		require.Len(t, body, 2)
	}
	for _, route := range []struct {
		name, path string
		list       bool
	}{
		{"detail_sla", "/api/v1/accounts/91/conversations/1/sla", false},
		{"detail_applied", "/api/v1/accounts/91/conversations/1/applied_sla", false},
		{"list_v1", "/api/v1/accounts/91/applied_slas", true},
		{"list_v1_reports", "/api/v1/accounts/91/reports/applied_slas", true},
		{"list_v2", "/api/v2/accounts/91/reports/applied_slas", true},
	} {
		t.Run(route.name, func(t *testing.T) {
			for _, fail := range slaHTTPFailures(route.list) {
				t.Run(fail.name, func(t *testing.T) {
					out := request(http.MethodGet, route.path, token, "", fail)
					assertFailure(t, out, 500)
					require.Equal(t, 1, injections)
					require.Zero(t, writes, "failed reads cannot publish or persist partial deadlines")
					recovered := request(http.MethodGet, route.path, token, "", slaHTTPFailure{})
					require.Equal(t, 200, recovered.Code, recovered.Body.String())
					require.Zero(t, writes)
					if route.list {
						var body struct {
							Payload []any `json:"payload"`
						}
						require.NoError(t, json.Unmarshal(recovered.Body.Bytes(), &body))
						require.Len(t, body.Payload, 2)
					} else {
						var body struct {
							Data struct {
								First bool `json:"first_response_breached"`
								Next  bool `json:"next_response_breached"`
							} `json:"data"`
						}
						require.NoError(t, json.Unmarshal(recovered.Body.Bytes(), &body))
						require.False(t, body.Data.First)
						require.False(t, body.Data.Next)
					}
				})
			}
		})
	}
	for _, tc := range []struct {
		name, path string
		status     int
	}{
		{"missing_applied_success", "/api/v1/accounts/91/conversations/3/sla", 200},
		{"empty_account_list", "/api/v1/accounts/92/applied_slas", 200},
		{"missing_conversation", "/api/v1/accounts/91/conversations/999/sla", 404},
		{"foreign_conversation", "/api/v1/accounts/91/conversations/902/sla", 404},
	} {
		t.Run("compatibility/"+tc.name, func(t *testing.T) {
			out := request(http.MethodGet, tc.path, token, "", slaHTTPFailure{})
			require.Equal(t, tc.status, out.Code, out.Body.String())
			require.Zero(t, writes)
		})
	}
	for index, alias := range []string{"sla_policies/process", "sla/process", "slas/evaluate", "sla/evaluate"} {
		t.Run("process/"+alias, func(t *testing.T) {
			account := uint(101 + index)
			seedAccount(account)
			p := policy
			p.ID = 0
			p.AccountID = account
			require.NoError(t, db.Create(&p).Error)
			for i := uint(1); i <= 2; i++ {
				id := account*10 + i
				require.NoError(t, db.Create(&domain.Conversation{ID: id, DisplayID: id, AccountID: account, InboxID: account, ContactID: account, Status: domain.ConversationStatusOpen, SLAPolicyID: &p.ID, CreatedAt: start, UpdatedAt: start, LastActivityAt: start}).Error)
			}
			var before []domain.Conversation
			require.NoError(t, db.Where("account_id = ?", account).Order("id").Find(&before).Error)
			path := fmt.Sprintf("/api/v1/accounts/%d/%s", account, alias)
			out := request(http.MethodPost, path, token, "", slaHTTPFailure{"timeline", "messages", 1})
			assertFailure(t, out, 500)
			require.Equal(t, 1, injections)
			require.JSONEq(t, `{"success":false,"error":"Failed to process SLAs"}`, out.Body.String())
			var applied []domain.AppliedSLA
			require.NoError(t, db.Where("account_id = ?", account).Find(&applied).Error)
			require.Len(t, applied, 1, "the other conversation independently succeeds")
			var convs []domain.Conversation
			require.NoError(t, db.Where("account_id = ?", account).Order("id").Find(&convs).Error)
			unchanged := 0
			for i, conv := range convs {
				if conv.ID != applied[0].ConversationID {
					require.Equal(t, before[i], conv, "failed conversation remains unchanged regardless of model defaults")
					unchanged++
				} else {
					require.Equal(t, "breached", conv.SLAStatus)
				}
			}
			require.Equal(t, 1, unchanged)
			recovered := request(http.MethodPost, path, token, "", slaHTTPFailure{})
			require.Equal(t, 200, recovered.Code, recovered.Body.String())
			require.Contains(t, recovered.Body.String(), `"processed":true`)
			require.NoError(t, db.Where("account_id = ?", account).Find(&applied).Error)
			require.Len(t, applied, 2)
		})
	}
	t.Run("process_failure_preserves_timely_reply", func(t *testing.T) {
		const account, conversation = uint(111), uint(1111)
		seedAccount(account)
		p := policy
		p.ID, p.AccountID = 0, account
		require.NoError(t, db.Create(&p).Error)
		conv := domain.Conversation{ID: conversation, DisplayID: conversation, AccountID: account, InboxID: account, ContactID: account, Status: domain.ConversationStatusOpen, SLAPolicyID: &p.ID, CreatedAt: start, UpdatedAt: start, LastActivityAt: start}
		require.NoError(t, db.Create(&conv).Error)
		for _, message := range []domain.Message{
			{AccountID: account, ConversationID: conversation, MessageType: "incoming", SenderType: "Contact", Status: "sent", CreatedAt: start},
			{AccountID: account, ConversationID: conversation, MessageType: "outgoing", SenderType: "User", Status: "sent", CreatedAt: start.Add(30 * time.Second)},
		} {
			require.NoError(t, db.Create(&message).Error)
		}
		var before domain.Conversation
		require.NoError(t, db.First(&before, conversation).Error)
		out := request(http.MethodPost, "/api/v1/accounts/111/sla/process", token, "", slaHTTPFailure{"timeline", "messages", 1})
		require.Equal(t, 1, injections)
		// Check durable state before the status assertion. The unchanged baseline
		// records a false first-response breach despite the saved timely reply.
		for _, model := range []any{&domain.SLABreachLog{}, &domain.SLAEvent{}, &domain.AppliedSLA{}} {
			var count int64
			require.NoError(t, db.Model(model).Where("account_id = ? AND conversation_id = ?", account, conversation).Count(&count).Error)
			require.Zero(t, count, "a failed timeline must not persist SLA facts for a timely-replied conversation: %T", model)
		}
		var after domain.Conversation
		require.NoError(t, db.First(&after, conversation).Error)
		require.Equal(t, before, after)
		require.Zero(t, writes)
		assertFailure(t, out, 500)
		recovered := request(http.MethodPost, "/api/v1/accounts/111/sla/process", token, "", slaHTTPFailure{})
		require.Equal(t, 200, recovered.Code, recovered.Body.String())
		var breaches int64
		require.NoError(t, db.Model(&domain.SLABreachLog{}).Where("account_id = ?", account).Count(&breaches).Error)
		require.Zero(t, breaches)
		var applied domain.AppliedSLA
		require.NoError(t, db.Where("account_id = ?", account).First(&applied).Error)
		require.Equal(t, "active", applied.SLAStatus)
	})
	t.Run("unconfigured_process", func(t *testing.T) {
		h := handler.NewAdvancedHandler(db, nil, nil, nil, nil, nil)
		local := gin.New()
		local.POST("/process", h.ProcessSLA)
		out := httptest.NewRecorder()
		local.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/process", nil))
		assertFailure(t, out, 503)
		require.JSONEq(t, `{"success":false,"error":"SLA service unavailable"}`, out.Body.String())
	})
	t.Run("authorization_before_sources", func(t *testing.T) {
		out := request(http.MethodPost, "/api/v1/accounts/91/sla/process", agentToken, "", slaHTTPFailure{"policy", "sla_policies", 1})
		require.Equal(t, 403, out.Code, out.Body.String())
		require.Zero(t, injections)
		require.Zero(t, writes)
		out = request(http.MethodGet, "/api/v1/accounts/91/conversations/1/sla", "", "", slaHTTPFailure{"policy", "sla_policies", 1})
		require.Equal(t, 401, out.Code, out.Body.String())
		require.Zero(t, injections)
		require.Zero(t, writes)
	})
	// Fail only the SLA timeline query, after the underlying customer operation
	// committed. It must remain successful, without a retry or rollback.
	for _, operation := range []string{"message", "status"} {
		t.Run("optional_sla/"+operation, func(t *testing.T) {
			failed := 0
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("sla_optional_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "messages" && strings.Contains(tx.Statement.SQL.String(), "message_type <>") {
					failed++
					_ = tx.AddError(errors.New(sentinel))
				}
			}))
			defer func() { require.NoError(t, db.Callback().Query().Remove("sla_optional_failure")) }()
			method, path, body := http.MethodPost, "/api/v1/accounts/91/conversations/3/messages", `{"content":"saved despite optional SLA failure"}`
			status := 201
			if operation == "status" {
				path = "/api/v1/accounts/91/conversations/3/toggle_status"
				body = `{"status":"resolved"}`
				status = 200
			}
			out := request(method, path, token, body, slaHTTPFailure{})
			require.Equal(t, status, out.Code, out.Body.String())
			require.Equal(t, 1, failed)
			require.NotContains(t, out.Body.String(), sentinel)
			if operation == "message" {
				var count int64
				require.NoError(t, db.Model(&domain.Message{}).Where("conversation_id = ? AND content = ?", 3, "saved despite optional SLA failure").Count(&count).Error)
				require.Equal(t, int64(1), count)
			} else {
				var conv domain.Conversation
				require.NoError(t, db.First(&conv, 3).Error)
				require.Equal(t, domain.ConversationStatusResolved, conv.Status)
			}
			var count int64
			require.NoError(t, db.Model(&domain.AppliedSLA{}).Where("account_id = ? AND conversation_id = ?", 91, 3).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
