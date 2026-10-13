package router_test

import (
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
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These tests enter through the real router rather than supplying trusted tenant
// or permission context to a handler. The legacy composition is intentional:
// inspecting rules must work before an operator enables durable execution.
type compatibilityRouterFixture struct {
	t               *testing.T
	db              *gorm.DB
	cfg             *config.Config
	r               *gin.Engine
	account, other  domain.Account
	admin           domain.User
	adminToken      string
	agentToken      string
	otherToken      string
	principalNumber int
}

func newCompatibilityRouterFixture(t *testing.T) *compatibilityRouterFixture {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	f := &compatibilityRouterFixture{t: t, cfg: &config.Config{
		Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "compatibility.db"),
		JWTSecret: "automation-inventory-router-test-key", AutomationExecutionMode: "legacy",
	}}
	var err error
	f.db, err = database.InitDB(f.cfg)
	require.NoError(t, err)
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	f.account = domain.Account{Name: "Inventory account"}
	f.other = domain.Account{Name: "Other inventory account"}
	require.NoError(t, f.db.Create(&f.account).Error)
	require.NoError(t, f.db.Create(&f.other).Error)
	f.admin, f.adminToken = f.principal(f.account.ID, domain.RoleAdministrator, nil)
	_, f.agentToken = f.principal(f.account.ID, domain.RoleAgent, nil)
	_, f.otherToken = f.principal(f.other.ID, domain.RoleAdministrator, nil)
	f.r = router.SetupRouter(f.cfg, f.db, ws.NewHub())
	return f
}

func (f *compatibilityRouterFixture) principal(accountID uint, membershipRole string, customRoleID *uint) (domain.User, string) {
	f.t.Helper()
	f.principalNumber++
	// A global administrator claim is deliberately insufficient for a tenant
	// member whose live membership grants only agent or custom-role permissions.
	user := domain.User{Name: "Inventory user", Email: fmt.Sprintf("inventory-%d@example.test", f.principalNumber), Role: domain.RoleAdministrator, Type: "User"}
	require.NoError(f.t, f.db.Create(&user).Error)
	require.NoError(f.t, f.db.Create(&domain.AccountUser{AccountID: accountID, UserID: user.ID, Role: membershipRole, CustomRoleID: customRoleID}).Error)
	token, err := auth.GenerateToken(&user, f.cfg.JWTSecret, 1)
	require.NoError(f.t, err)
	return user, token
}

func (f *compatibilityRouterFixture) rule(accountID uint, active bool) domain.AutomationRule {
	f.t.Helper()
	rule := domain.AutomationRule{AccountID: accountID, Name: "Private rule title", EventName: "conversation_created", Conditions: "[]", Actions: `[{"action_name":"change_priority","action_params":["urgent"]}]`, Active: active}
	require.NoError(f.t, f.db.Create(&rule).Error)
	return rule
}

func (f *compatibilityRouterFixture) path(accountID uint) string {
	return fmt.Sprintf("/api/v1/accounts/%d/automation_rules/compatibility", accountID)
}

func (f *compatibilityRouterFixture) get(path, token string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func (f *compatibilityRouterFixture) page(query string) (map[string]any, *httptest.ResponseRecorder) {
	f.t.Helper()
	w := f.get(f.path(f.account.ID)+"?"+query, f.adminToken)
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(f.t, w.Header().Get("Cache-Control"), "no-store")
	var envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(f.t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.True(f.t, envelope.Success)
	require.NotNil(f.t, envelope.Data)
	return envelope.Data, w
}

func compatibilityPageRuleIDs(t *testing.T, page map[string]any) []string {
	t.Helper()
	rules, ok := page["rules"].([]any)
	require.True(t, ok, "rules must be an array, including for an empty page: %#v", page["rules"])
	ids := make([]string, 0, len(rules))
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		require.True(t, ok)
		id, ok := rule["id"].(string)
		require.True(t, ok, "rule IDs must remain lossless JSON strings")
		ids = append(ids, id)
	}
	return ids
}

func TestAutomationCompatibilityRouterRequiresLiveTenantManagePermission(t *testing.T) {
	f := newCompatibilityRouterFixture(t)
	f.rule(f.account.ID, true)
	path := f.path(f.account.ID) + "?target=durable_local_v1"
	for _, tc := range []struct {
		name, path, token string
		status            int
	}{
		{"administrator", path, f.adminToken, http.StatusOK},
		{"unauthenticated", path, "", http.StatusUnauthorized},
		{"invalid token", path, "invalid-token", http.StatusUnauthorized},
		{"agent despite global admin claim", path, f.agentToken, http.StatusForbidden},
		{"other account administrator", path, f.otherToken, http.StatusForbidden},
		{"cross account path", f.path(f.other.ID) + "?target=durable_local_v1", f.adminToken, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.get(tc.path, tc.token)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
	for _, tc := range []struct {
		name, permission string
		status           int
	}{
		{"manager", "automation_manage", http.StatusOK},
		{"viewer", "automation_view", http.StatusForbidden},
		{"unrelated", "conversation_manage", http.StatusForbidden},
	} {
		t.Run("custom role "+tc.name, func(t *testing.T) {
			role := domain.CustomRole{AccountID: f.account.ID, Name: tc.name, Permissions: fmt.Sprintf("[%q]", tc.permission)}
			require.NoError(t, f.db.Create(&role).Error)
			_, token := f.principal(f.account.ID, domain.RoleAgent, &role.ID)
			w := f.get(path, token)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
	// An already issued JWT must not preserve a tenant permission after demotion.
	require.NoError(t, f.db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", f.account.ID, f.admin.ID).Update("role", domain.RoleAgent).Error)
	require.Equal(t, http.StatusForbidden, f.get(path, f.adminToken).Code)
}

func TestAutomationCompatibilityRouterStrictQueryContract(t *testing.T) {
	f := newCompatibilityRouterFixture(t)
	base := "target=durable_local_v1"
	queries := []string{
		"", "target", "target=", "target=other", "target=durable_local_v1&target=durable_local_v1",
		base + "&active=", base + "&active", base + "&active=yes", base + "&active=True", base + "&active=1", base + "&active=true&active=false",
		base + "&rule_id=", base + "&rule_id", base + "&rule_id=0", base + "&rule_id=-1", base + "&rule_id=1.5", base + "&rule_id=18446744073709551616", base + "&rule_id=1&rule_id=2",
		base + "&limit=", base + "&limit", base + "&limit=0", base + "&limit=-1", base + "&limit=101", base + "&limit=1.5", base + "&limit=18446744073709551616", base + "&limit=1&limit=1",
		base + "&cursor=", base + "&cursor", base + "&cursor=not-a-cursor", base + "&cursor=x&cursor=y",
		base + "&unknown=private-query-secret", base + "&unknown", base + "&=private-query-secret", base + "&active=%zz", base + ";active=true",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			w := f.get(f.path(f.account.ID)+"?"+query, f.adminToken)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			require.NotContains(t, w.Body.String(), "private-query-secret")
			var envelope map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.Equal(t, false, envelope["success"])
			require.NotEmpty(t, envelope["error"])
			require.NotContains(t, envelope, "data")
		})
	}
	for _, query := range []string{base, base + "&active=all", base + "&active=true", base + "&active=false", base + "&limit=100"} {
		page, _ := f.page(query)
		require.Empty(t, compatibilityPageRuleIDs(t, page))
	}
}

func TestAutomationCompatibilityRouterFiltersAndPageMetadata(t *testing.T) {
	f := newCompatibilityRouterFixture(t)
	active := f.rule(f.account.ID, true)
	inactive := f.rule(f.account.ID, false)
	foreign := f.rule(f.other.ID, true)
	base := "target=durable_local_v1"
	page, _ := f.page(base)
	require.Equal(t, []string{strconv.FormatUint(uint64(active.ID), 10), strconv.FormatUint(uint64(inactive.ID), 10)}, compatibilityPageRuleIDs(t, page))
	require.Equal(t, "aut.rule_compatibility.v1", page["schema_version"])
	require.Equal(t, "durable_local_v1", page["target"])
	require.NotEmpty(t, page["implementation_revision"])
	require.Equal(t, "saved_account_page", page["source_kind"])
	require.Equal(t, strconv.FormatUint(uint64(f.account.ID), 10), page["account_id"])
	require.Equal(t, "page_snapshot_live_collection", page["consistency"])
	require.Equal(t, false, page["collection_snapshot"])
	require.Equal(t, true, page["page_exhausted"])
	require.EqualValues(t, 2, page["inspected_rule_count"])
	require.EqualValues(t, 0, page["uninspected_rule_count"])
	observed, ok := page["observed_at"].(string)
	require.True(t, ok)
	_, err := time.Parse(time.RFC3339Nano, observed)
	require.NoError(t, err)
	for _, key := range []string{"complete", "filters", "severity_counts", "active_counts", "not_checked", "compiler_policy", "condition_policy", "horizon"} {
		require.Contains(t, page, key)
	}
	filters, ok := page["filters"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "all", filters["active"])
	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{base + "&active=true", []string{strconv.FormatUint(uint64(active.ID), 10)}},
		{base + "&active=false", []string{strconv.FormatUint(uint64(inactive.ID), 10)}},
		{fmt.Sprintf("%s&rule_id=%d", base, inactive.ID), []string{strconv.FormatUint(uint64(inactive.ID), 10)}},
		{fmt.Sprintf("%s&active=true&rule_id=%d", base, inactive.ID), []string{}},
		{fmt.Sprintf("%s&rule_id=%d", base, foreign.ID), []string{}},
	} {
		filtered, _ := f.page(tc.query)
		require.Equal(t, tc.ids, compatibilityPageRuleIDs(t, filtered), tc.query)
	}
}

func TestAutomationCompatibilityRouterBoundedCursorPages(t *testing.T) {
	f := newCompatibilityRouterFixture(t)
	rules := make([]domain.AutomationRule, 102)
	for i := range rules {
		rules[i] = domain.AutomationRule{AccountID: f.account.ID, Name: "Bounded rule", EventName: "conversation_created", Conditions: "[]", Actions: "[]", Active: i%2 == 0}
	}
	require.NoError(t, f.db.Create(&rules).Error)
	first, _ := f.page("target=durable_local_v1")
	firstIDs := compatibilityPageRuleIDs(t, first)
	require.Len(t, firstIDs, 100)
	require.Equal(t, false, first["page_exhausted"])
	require.EqualValues(t, 100, first["inspected_rule_count"])
	require.EqualValues(t, 2, first["uninspected_rule_count"])
	cursor, ok := first["next_cursor"].(string)
	require.True(t, ok)
	require.NotEmpty(t, cursor)
	// Higher IDs inserted after page one are excluded by its fixed horizon, but
	// edits to existing rows are intentionally live on the next page.
	later := f.rule(f.account.ID, true)
	require.NoError(t, f.db.Model(&domain.AutomationRule{}).Where("id = ?", rules[100].ID).Update("active", false).Error)
	second, _ := f.page("target=durable_local_v1&cursor=" + url.QueryEscape(cursor))
	secondIDs := compatibilityPageRuleIDs(t, second)
	require.Len(t, secondIDs, 2)
	require.Equal(t, first["horizon"], second["horizon"])
	require.NotContains(t, secondIDs, strconv.FormatUint(uint64(later.ID), 10))
	require.Equal(t, map[string]any{"active": float64(0), "inactive": float64(2)}, second["active_counts"])
	require.Equal(t, true, second["page_exhausted"])
	require.EqualValues(t, 0, second["uninspected_rule_count"])
	require.Empty(t, second["next_cursor"])
	allIDs := append(firstIDs, secondIDs...)
	for i, rule := range rules {
		require.Equal(t, strconv.FormatUint(uint64(rule.ID), 10), allIDs[i])
	}
	// Limit controls only this page's size; normalized "all" preserves the
	// semantic filter even when it was implicit on page one.
	smaller, _ := f.page("target=durable_local_v1&active=all&limit=1&cursor=" + url.QueryEscape(cursor))
	require.Equal(t, []string{secondIDs[0]}, compatibilityPageRuleIDs(t, smaller))
	require.EqualValues(t, 1, smaller["uninspected_rule_count"])
	// A cursor is scoped to both the authorized tenant and normalized filters.
	for _, path := range []string{
		f.path(f.other.ID) + "?target=durable_local_v1&cursor=" + url.QueryEscape(cursor),
		f.path(f.account.ID) + "?target=durable_local_v1&active=true&cursor=" + url.QueryEscape(cursor),
		fmt.Sprintf("%s?target=durable_local_v1&rule_id=%d&cursor=%s", f.path(f.account.ID), rules[100].ID, url.QueryEscape(cursor)),
	} {
		token := f.adminToken
		if strings.HasPrefix(path, f.path(f.other.ID)+"?") {
			token = f.otherToken
		}
		w := f.get(path, token)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	}
}

func TestAutomationCompatibilityRouterRedactsDefinitionsWithoutFreezingOrMutating(t *testing.T) {
	f := newCompatibilityRouterFixture(t)
	rule := domain.AutomationRule{
		AccountID: f.account.ID, Name: "private-title-secret", Description: "private-description-secret", EventName: "conversation_created", Active: true,
		Conditions: `[{"attribute_key":"email","filter_operator":"equal_to","values":["private-condition@example.invalid"]}]`,
		Actions:    `[{"action_name":"send_message","action_params":["private-message-body-secret"]},{"action_name":"send_webhook_event","action_params":["https://private.example.invalid/hook?token=private-token-secret"]},{"action_name":"private-action-name-secret","action_params":["private-action-value-secret"]}]`,
	}
	require.NoError(t, f.db.Create(&rule).Error)
	frozenRule := f.rule(f.account.ID, false)
	version := domain.AutomationRuleVersion{AccountID: f.account.ID, RuleID: frozenRule.ID, Version: 1,
		EventName: "private-frozen-event-secret", Conditions: `[{"attribute_key":"private-condition-key-secret","filter_operator":"private-operator-secret","values":["private-frozen-condition-secret"]}]`,
		Actions: `[{"action_name":"private-frozen-action-secret","action_params":["private-frozen-value-secret"]}]`,
	}
	require.NoError(t, f.db.Create(&version).Error)
	// Defensively scope historical versions by tenant as well as rule ID, even
	// if a malformed/imported row refers to a rule owned by a different account.
	foreignVersion := domain.AutomationRuleVersion{AccountID: f.other.ID, RuleID: rule.ID, Version: 999,
		EventName: "message_created", Conditions: "[]", Actions: `[{"action_name":"send_message","action_params":["private-foreign-version-secret"]}]`,
	}
	require.NoError(t, f.db.Create(&foreignVersion).Error)
	// Seed the actual execution and shared journal models. Inventory must leave
	// existing runner state, event/command receipts, and the real legacy mode
	// claim intact, as well as refraining from creating any new rows.
	stamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	executionKey := "inventory-sentinel-execution-key"
	executions := []domain.AutomationRuleExecution{
		{AccountID: f.account.ID, RuleID: frozenRule.ID, RuleVersion: 1, ExecutionKey: &executionKey,
			ConversationID: 701, EventName: "conversation_created", EventID: "inventory-sentinel-event", Status: "running",
			RunnerContext: `{"sentinel":"private-runner-context-secret"}`, RunnerEvaluation: `{"sentinel":"private-runner-evaluation-secret"}`,
			RunnerNextAction: 2, RunnerTargetVersion: 7, RunnerLease: "private-runner-lease-secret", RunnerLeaseUntil: &stamp,
			RunnerRetryAt: &stamp, FactBatchKey: "inventory-sentinel-fact-batch", TargetVersion: 3,
			ActionResults: `[{"sentinel":"private-action-result-secret"}]`, TriggeredAt: stamp, CreatedAt: stamp},
		{AccountID: f.account.ID, RuleID: rule.ID, ConversationID: 702, EventName: "message_created", Status: "failed",
			ActionResults: "[]", Error: "private-execution-error-secret", CancelRequested: true, CompletedAt: &stamp,
			DurationMs: 137, TriggeredAt: stamp, CreatedAt: stamp},
	}
	require.NoError(t, f.db.Create(&executions).Error)
	journalSentinels := []domain.LocalChangeJournal{
		{ChangeID: "inventory-sentinel-event-outbox", SourceModule: "AUT", AccountID: f.account.ID,
			Action: "event_outbox", ObjectType: "AutomationRuleExecution", ObjectID: executions[0].ID,
			ActorType: "system", AfterState: `{"sentinel":"private-event-receipt-secret"}`, Status: "pending",
			EventID: "inventory-sentinel-event", CorrelationID: "inventory-sentinel-correlation", OccurredAt: stamp, CreatedAt: stamp},
		{ChangeID: "inventory-sentinel-command-receipt", SourceModule: "CON", AccountID: f.account.ID,
			Action: "command_receipt", ObjectType: "Conversation", ObjectID: 701, ActorType: "system",
			AfterState: `{"sentinel":"private-command-receipt-secret"}`, Status: "delivered", Result: "applied",
			CommandID: "inventory-sentinel-command", ObjectVersionBefore: 6, ObjectVersionAfter: 7, OccurredAt: stamp, CreatedAt: stamp},
	}
	require.NoError(t, f.db.Create(&journalSentinels).Error)
	var beforeRules []domain.AutomationRule
	var beforeVersions []domain.AutomationRuleVersion
	var beforeExecutions []domain.AutomationRuleExecution
	var beforeJournal []domain.LocalChangeJournal
	require.NoError(t, f.db.Order("id").Find(&beforeRules).Error)
	require.NoError(t, f.db.Order("id").Find(&beforeVersions).Error)
	require.NoError(t, f.db.Order("id").Find(&beforeExecutions).Error)
	require.NoError(t, f.db.Order("id").Find(&beforeJournal).Error)
	require.Len(t, beforeExecutions, 2)
	var modeClaim domain.LocalChangeJournal
	for _, row := range beforeJournal {
		if row.ChangeID == "aut-monolith-execution-mode-v1" {
			modeClaim = row
		}
	}
	require.NotZero(t, modeClaim.ID, "the snapshot must include the actual router-created mode claim")
	require.Equal(t, "AUT", modeClaim.SourceModule)
	require.Equal(t, "automation_execution_mode", modeClaim.Action)
	require.Equal(t, "legacy", modeClaim.AfterState)
	// Query-only protects every SQLite table, including raw SQL writes that do
	// not pass GORM mutation callbacks. There is one connection in this fixture.
	require.NoError(t, f.db.Exec("PRAGMA query_only = ON").Error)
	page, w := f.page("target=durable_local_v1")
	require.Len(t, compatibilityPageRuleIDs(t, page), 2)
	for _, secret := range []string{
		"private-title-secret", "private-description-secret", "private-condition@example.invalid", "private-message-body-secret",
		"private.example.invalid", "private-token-secret", "private-action-name-secret", "private-action-value-secret",
		"private-frozen-event-secret", "private-condition-key-secret", "private-operator-secret", "private-frozen-condition-secret", "private-frozen-action-secret", "private-frozen-value-secret",
		"private-foreign-version-secret",
		"private-runner-context-secret", "private-runner-evaluation-secret", "private-runner-lease-secret", "private-action-result-secret",
		"private-execution-error-secret", "private-event-receipt-secret", "private-command-receipt-secret",
	} {
		require.NotContains(t, w.Body.String(), secret)
	}
	for i, raw := range page["rules"].([]any) {
		projected := raw.(map[string]any)
		for _, field := range []string{"name", "description", "conditions", "actions"} {
			require.NotContains(t, projected, field)
		}
		sources, ok := projected["sources"].([]any)
		require.True(t, ok)
		require.Len(t, sources, 2)
		require.Equal(t, "saved", sources[0].(map[string]any)["source"])
		if i == 0 {
			require.Nil(t, projected["latest_version"])
			require.Equal(t, "activation_preview", sources[1].(map[string]any)["source"])
		} else {
			require.Equal(t, "1", projected["latest_version"])
			require.Equal(t, "effective_frozen", sources[1].(map[string]any)["source"])
		}
	}
	var afterRules []domain.AutomationRule
	var afterVersions []domain.AutomationRuleVersion
	var afterExecutions []domain.AutomationRuleExecution
	var afterJournal []domain.LocalChangeJournal
	require.NoError(t, f.db.Order("id").Find(&afterRules).Error)
	require.NoError(t, f.db.Order("id").Find(&afterVersions).Error)
	require.NoError(t, f.db.Order("id").Find(&afterExecutions).Error)
	require.NoError(t, f.db.Order("id").Find(&afterJournal).Error)
	require.Equal(t, beforeRules, afterRules)
	require.Equal(t, beforeVersions, afterVersions, "inventory must not lazily freeze unfrozen rules or replace historical versions")
	require.Equal(t, beforeExecutions, afterExecutions, "inventory must preserve full execution and runner state without adding executions")
	require.Equal(t, beforeJournal, afterJournal, "inventory must preserve the actual mode claim and event/command receipts without adding journal rows")
	// The existing read API continues to return the original rule wire shape.
	ordinary := f.get(fmt.Sprintf("/api/v1/accounts/%d/automation_rules", f.account.ID), f.agentToken)
	require.Equal(t, http.StatusOK, ordinary.Code, ordinary.Body.String())
	var envelope struct {
		Success bool                    `json:"success"`
		Data    []domain.AutomationRule `json:"data"`
	}
	require.NoError(t, json.Unmarshal(ordinary.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Len(t, envelope.Data, 2)
	require.Contains(t, ordinary.Body.String(), "private-title-secret")
	require.Contains(t, ordinary.Body.String(), "private-message-body-secret")
	require.NotContains(t, ordinary.Body.String(), "schema_version")
	byID := make(map[uint]domain.AutomationRule)
	for _, saved := range envelope.Data {
		byID[saved.ID] = saved
	}
	require.Equal(t, rule, byID[rule.ID])
}

func TestAutomationCompatibilityRouterReadFailureIsSafeAndNotCached(t *testing.T) {
	f := newCompatibilityRouterFixture(t)
	f.rule(f.account.ID, true)
	// A broken AUT store must fail closed instead of returning a partial success
	// or exposing the SQL statement/table name through the public error envelope.
	require.NoError(t, f.db.Exec("DROP TABLE automation_rule_versions").Error)
	w := f.get(f.path(f.account.ID)+"?target=durable_local_v1", f.adminToken)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	require.NotContains(t, w.Body.String(), "automation_rule_versions")
	require.NotContains(t, strings.ToLower(w.Body.String()), "select ")
	require.NotContains(t, strings.ToLower(w.Body.String()), "no such table")
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, false, envelope["success"])
	require.NotEmpty(t, envelope["error"])
	require.NotContains(t, envelope, "data")
}
