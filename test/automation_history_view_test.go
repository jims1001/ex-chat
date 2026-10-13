package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
)

// These tests use ordinary authenticated account routes and real local owner
// commands. The compatibility view must not alter admission, owner execution,
// cancellation permissions, or the established raw response contracts.
func historyDetail(f *durableAutomationAPIFixture, a durableAutomationAPITenant, id uint, view string) map[string]any {
	f.t.Helper()
	var result map[string]any
	f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/automation_rules/executions/%d%s", id, view)), nil, http.StatusOK, &result)
	return result
}

func historyList(f *durableAutomationAPIFixture, a durableAutomationAPITenant, ruleID uint, query string) []map[string]any {
	f.t.Helper()
	var result []map[string]any
	f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/automation_rules/executions?rule_id=%d%s", ruleID, query)), nil, http.StatusOK, &result)
	return result
}

func historyLogicalResults(t *testing.T, view map[string]any) []map[string]any {
	t.Helper()
	require.Equal(t, "compat_v1", view["result_schema"])
	raw, ok := view["action_results"].(string)
	require.True(t, ok, "ordinary compatibility action_results must remain a JSON string: %#v", view)
	var results []map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &results))
	require.NotNil(t, results, "results must be a JSON array, never null")
	return results
}

func historyAssertLogical(t *testing.T, result map[string]any, index int, name, status, outcome string, completed bool, ownerStatuses ...string) {
	t.Helper()
	require.Equal(t, float64(index), result["logical_action_index"])
	require.Equal(t, name, result["action_name"])
	require.Equal(t, status, result["status"])
	require.Equal(t, completed, result["completed"], "completed must be explicit, including false")
	if outcome != "" {
		require.Equal(t, outcome, result["outcome"])
	}
	steps, ok := result["owner_steps"].([]any)
	require.True(t, ok, "owner_steps must be a typed JSON array: %#v", result)
	require.Len(t, steps, len(ownerStatuses))
	for i, status := range ownerStatuses {
		step, ok := steps[i].(map[string]any)
		require.True(t, ok)
		require.Equal(t, status, step["status"])
	}
}

func historyAssertPrivate(t *testing.T, view map[string]any, secrets ...string) {
	t.Helper()
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	for _, forbidden := range append([]string{"runner_context", "runner_evaluation", "runner_lease", "dispatches", "action_params", "snapshot", "evaluation_context"}, secrets...) {
		require.NotContains(t, string(raw), forbidden)
	}
}

func historyAssertRaw(t *testing.T, f *durableAutomationAPIFixture, a durableAutomationAPITenant, execution domain.AutomationRuleExecution) {
	t.Helper()
	var stored domain.AutomationRuleExecution
	require.NoError(t, f.db.Where("account_id = ? AND id = ?", a.id, execution.ID).First(&stored).Error)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	var expected map[string]any
	require.NoError(t, json.Unmarshal(encoded, &expected))
	require.Equal(t, expected, historyDetail(f, a, execution.ID, ""))
	require.Equal(t, expected, historyDetail(f, a, execution.ID, "?view=raw"))
	for _, query := range []string{"", "&view=raw"} {
		found := false
		for _, result := range historyList(f, a, execution.RuleID, query) {
			if result["id"] == float64(execution.ID) {
				require.Equal(t, expected, result)
				found = true
			}
		}
		require.True(t, found, "raw list omitted execution")
	}
}

func historyRunStep(f *durableAutomationAPIFixture) {
	f.t.Helper()
	ran, err := f.bridge.Runtime.RunAutomationOnce(context.Background())
	require.NoError(f.t, err)
	require.True(f.t, ran)
}

func historyModuleView(f *durableAutomationAPIFixture, a durableAutomationAPITenant, id uint) map[string]any {
	f.t.Helper()
	query := foundation.QueryEnvelope{
		QueryType: "aut.execution.get.v1", AccountID: a.id,
		Actor:  foundation.ContractActor{Type: "System", ID: "history-test", AuthSource: "module"},
		Filter: map[string]any{"id": id}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"},
		Consistency: "strong", RequestedAt: time.Now().UTC(),
	}
	body, err := json.Marshal(query)
	require.NoError(f.t, err)
	request := httptest.NewRequest(http.MethodPost, "/contracts/queries/aut.execution.get.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+f.bridge.Runtime.Config.AuthToken)
	request.Header.Set("X-Module-Caller", "API")
	request.Header.Set("X-Contract-Version", "v1")
	request.Header.Set("X-Correlation-ID", "history-test")
	response := httptest.NewRecorder()
	f.bridge.Runtime.Handler().ServeHTTP(response, request)
	require.Equal(f.t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(f.t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestAutomationHistoryCompatibilityLogicalMessagesFrozenAndReplay(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-message")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("send_reply", "public-history-body"),
		durableAutomationAction("private_note", "private-history-body"),
		map[string]any{"capability_id": "message.create_private_note", "capability_version": "v1", "action_params": "capability-history-body"},
	})
	conv := f.conversation(a)
	event := f.fact(f.con, "conversation_created", conv.ID)
	f.admit(event)
	queued := f.execution(a, rule.ID, "received", "")
	// Editing the current definition must not rename admitted history.
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), map[string]any{
		"actions": []any{durableAutomationAction("change_priority", "urgent")},
	}, http.StatusOK, nil)
	f.restartBridge()
	f.flush()
	execution := f.execution(a, rule.ID, "succeeded", "")
	require.Equal(t, queued.ID, execution.ID)
	historyAssertRaw(t, f, a, execution)
	view := historyDetail(f, a, execution.ID, "?view=compat_v1")
	require.Equal(t, "success", view["status"])
	require.Equal(t, "succeeded", view["raw_status"])
	results := historyLogicalResults(t, view)
	require.Len(t, results, 3)
	historyAssertLogical(t, results[0], 0, "send_reply", "success", "queued", true, "succeeded", "succeeded")
	historyAssertLogical(t, results[1], 1, "private_note", "success", "saved", true, "succeeded", "succeeded")
	historyAssertLogical(t, results[2], 2, "message.create_private_note", "success", "saved", true, "succeeded", "succeeded")
	historyAssertPrivate(t, view, "public-history-body", "private-history-body", "capability-history-body")
	require.Equal(t, []map[string]any{view}, historyList(f, a, rule.ID, "&view=compat_v1"))
	var messages []domain.Message
	require.NoError(t, f.db.Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Order("id").Find(&messages).Error)
	require.Len(t, messages, 3)
	require.Equal(t, "queued", messages[0].Status)
	require.Equal(t, "saved", messages[1].Status)
	require.Equal(t, "saved", messages[2].Status)
	// Deleting the mutable rule and restarting/redelivering cannot change the
	// original alias, the serialized view, or any owner effect.
	f.request(a.token, http.MethodDelete, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), nil, http.StatusOK, nil)
	f.restartBridge()
	f.admit(event)
	f.admit(event)
	require.Zero(t, f.flush())
	require.Equal(t, view, historyDetail(f, a, execution.ID, "?view=compat_v1"))
	require.Equal(t, []map[string]any{view}, historyList(f, a, rule.ID, "&view=compat_v1"))
	var count int64
	require.NoError(t, f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&count).Error)
	require.EqualValues(t, 3, count)
}

func TestAutomationHistoryCompatibilityMixedActionsContinue(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-mixed")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("send_webhook_event", "unsupported-provider-secret"),
		durableAutomationAction("change_priority", "urgent"),
		durableAutomationAction("send_email_to_team", "unsupported-email-secret"),
		durableAutomationAction("private_note", "retained-private-body"),
	})
	conv := f.conversation(a)
	f.flush()
	f.state(a, conv.ID, "open", "urgent")
	execution := f.execution(a, rule.ID, "partially_succeeded", "action_capability_executor_unavailable")
	historyAssertRaw(t, f, a, execution)
	view := historyDetail(f, a, execution.ID, "?view=compat_v1")
	require.Equal(t, "partial_failed", view["status"])
	require.Equal(t, "partially_succeeded", view["raw_status"])
	require.Equal(t, execution.Error, view["error"])
	results := historyLogicalResults(t, view)
	require.Len(t, results, 4)
	historyAssertLogical(t, results[0], 0, "send_webhook_event", "failed", "", false, "failed")
	historyAssertLogical(t, results[1], 1, "change_priority", "success", "completed", true, "succeeded")
	historyAssertLogical(t, results[2], 2, "send_email_to_team", "failed", "", false, "failed")
	historyAssertLogical(t, results[3], 3, "private_note", "success", "saved", true, "succeeded", "succeeded")
	historyAssertPrivate(t, view, "unsupported-provider-secret", "unsupported-email-secret", "retained-private-body")
	module := historyModuleView(f, a, execution.ID)
	require.Equal(t, "partially_succeeded", module["status"])
	require.Equal(t, "action_capability_executor_unavailable", module["reason_code"])
	require.NotContains(t, module, "error")
	require.NotContains(t, module, "raw_status")
	require.NotContains(t, module, "result_schema")
	steps, ok := module["action_results"].([]any)
	require.True(t, ok)
	require.Len(t, steps, 5, "module query retains raw owner steps rather than four logical actions")
	historyAssertPrivate(t, module, "unsupported-provider-secret", "unsupported-email-secret", "retained-private-body")
	var count int64
	require.NoError(t, f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestAutomationHistoryCompatibilitySuccessfulPreparationIsNotLogicalSuccess(t *testing.T) {
	for _, action := range []string{"add_label", "send_reply", "private_note"} {
		t.Run(action, func(t *testing.T) {
			f := newDurableAutomationAPIFixture(t)
			a := f.tenant("history-preparation-" + action)
			value, rejectedOwner := "never-stored-message-secret", "MSG"
			if action == "add_label" {
				value, rejectedOwner = "existing-history-label", "CON"
				require.NoError(t, f.db.Create(&domain.Label{AccountID: a.id, Title: value}).Error)
			}
			rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction(action, value)})
			conv := f.conversation(a)
			f.admit(f.fact(f.con, "conversation_created", conv.ID))
			historyRunStep(f)
			// The first real owner step has succeeded. Reject only the subsequent
			// owner command, retaining its independently committed preparation.
			dependency := f.bridge.Runtime.Config.Dependencies[rejectedOwner]
			bad := dependency
			bad.Token = "history-test-rejected-owner-token"
			f.bridge.Runtime.Config.Dependencies[rejectedOwner] = bad
			historyRunStep(f)
			f.bridge.Runtime.Config.Dependencies[rejectedOwner] = dependency
			execution := f.execution(a, rule.ID, "partially_succeeded", "owner_command_rejected")
			view := historyDetail(f, a, execution.ID, "?view=compat_v1")
			require.Equal(t, "partial_failed", view["status"])
			require.Equal(t, "partially_succeeded", view["raw_status"])
			results := historyLogicalResults(t, view)
			require.Len(t, results, 1)
			historyAssertLogical(t, results[0], 0, action, "failed", "partial_progress", false, "succeeded", "failed")
			historyAssertPrivate(t, view, "never-stored-message-secret")
			var messageCount, appliedLabels, labelCount int64
			require.NoError(t, f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&messageCount).Error)
			require.Zero(t, messageCount)
			require.NoError(t, f.db.Model(&domain.ConversationLabel{}).Where("conversation_id = ?", conv.ID).Count(&appliedLabels).Error)
			require.Zero(t, appliedLabels)
			if action == "add_label" {
				require.NoError(t, f.db.Model(&domain.Label{}).Where("account_id = ?", a.id).Count(&labelCount).Error)
				require.EqualValues(t, 1, labelCount, "existing-label resolution must not create another label")
			}
			historyAssertRaw(t, f, a, execution)
		})
	}
}

func TestAutomationHistoryCompatibilityCancellationPermissionsAndShapes(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a, other := f.tenant("history-cancel"), f.tenant("history-other")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("send_reply", "cancelled-body-secret")})
	conv := f.conversation(a)
	f.admit(f.fact(f.con, "conversation_created", conv.ID))
	execution := f.execution(a, rule.ID, "received", "")
	cancel := fmt.Sprintf("/automation_executions/%d/cancel", execution.ID)
	for _, view := range []string{"", "?view=raw", "?view=compat_v1"} {
		f.request(other.token, http.MethodGet, other.path(fmt.Sprintf("/automation_rules/executions/%d%s", execution.ID, view)), nil, http.StatusNotFound, nil)
		f.request(other.token, http.MethodPost, other.path(cancel+view), map[string]any{}, http.StatusNotFound, nil)
	}
	require.Empty(t, historyList(f, other, rule.ID, ""))
	require.Empty(t, historyList(f, other, rule.ID, "&view=compat_v1"))
	// An ordinary account agent may read history, but cannot cancel it even
	// when explicitly selecting the new view.
	require.NoError(t, f.db.Model(&domain.AccountUser{}).Where("account_id = ?", a.id).Update("role", domain.RoleAgent).Error)
	historyDetail(f, a, execution.ID, "?view=compat_v1")
	for _, view := range []string{"", "?view=compat_v1"} {
		f.request(a.token, http.MethodPost, a.path(cancel+view), map[string]any{}, http.StatusForbidden, nil)
	}
	require.False(t, f.execution(a, rule.ID, "received", "").CancelRequested)
	require.NoError(t, f.db.Model(&domain.AccountUser{}).Where("account_id = ?", a.id).Update("role", domain.RoleAdministrator).Error)
	// Unknown view selectors fail before performing the cancellation.
	f.request(a.token, http.MethodPost, a.path(cancel+"?view=unknown"), map[string]any{}, http.StatusBadRequest, nil)
	f.execution(a, rule.ID, "received", "")
	var compat map[string]any
	f.request(a.token, http.MethodPost, a.path(cancel+"?view=compat_v1"), map[string]any{}, http.StatusOK, &compat)
	require.Equal(t, "cancelled", compat["status"])
	require.Equal(t, "cancelled", compat["raw_status"])
	require.Equal(t, "cancelled_by_request", compat["error"])
	results := historyLogicalResults(t, compat)
	require.Len(t, results, 1)
	historyAssertLogical(t, results[0], 0, "send_reply", "skipped", "", false, "skipped", "skipped")
	historyAssertPrivate(t, compat, "cancelled-body-secret")
	// Existing cancellation callers continue receiving parsed raw owner steps
	// and reason_code, unlike the model-shaped list/detail and compat responses.
	for _, query := range []string{"", "?view=raw"} {
		var raw map[string]any
		f.request(a.token, http.MethodPost, a.path(cancel+query), map[string]any{}, http.StatusOK, &raw)
		require.Equal(t, "cancelled", raw["status"])
		require.Equal(t, "cancelled_by_request", raw["reason_code"])
		require.NotContains(t, raw, "error")
		require.NotContains(t, raw, "raw_status")
		require.NotContains(t, raw, "result_schema")
		steps, ok := raw["action_results"].([]any)
		require.True(t, ok)
		require.Len(t, steps, 2)
	}
	f.restartBridge()
	f.flush()
	f.state(a, conv.ID, "open", "low")
	require.Equal(t, compat, historyDetail(f, a, execution.ID, "?view=compat_v1"))
	historyAssertRaw(t, f, a, f.execution(a, rule.ID, "cancelled", "cancelled_by_request"))
}

func TestAutomationHistoryCompatibilityCancelAfterPreparation(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-cancel-partial")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("send_reply", "prepared-unsent-secret")})
	conv := f.conversation(a)
	f.admit(f.fact(f.con, "conversation_created", conv.ID))
	historyRunStep(f)
	execution := f.execution(a, rule.ID, "received", "")
	progress := historyLogicalResults(t, historyDetail(f, a, execution.ID, "?view=compat_v1"))
	require.Len(t, progress, 1)
	require.Equal(t, false, progress[0]["completed"])
	require.Equal(t, "partial_progress", progress[0]["outcome"])
	var cancelled map[string]any
	f.request(a.token, http.MethodPost, a.path(fmt.Sprintf("/automation_executions/%d/cancel?view=compat_v1", execution.ID)), map[string]any{}, http.StatusOK, &cancelled)
	require.Equal(t, "cancelled", cancelled["status"])
	results := historyLogicalResults(t, cancelled)
	require.Len(t, results, 1)
	require.Equal(t, false, results[0]["completed"])
	require.NotEqual(t, "success", results[0]["status"])
	require.Equal(t, "partial_progress", results[0]["outcome"])
	steps := results[0]["owner_steps"].([]any)
	require.Equal(t, "succeeded", steps[0].(map[string]any)["status"])
	require.Equal(t, "skipped", steps[1].(map[string]any)["status"])
	f.restartBridge()
	f.flush()
	var count int64
	require.NoError(t, f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&count).Error)
	require.Zero(t, count, "cancellation must not manufacture a queued public reply")
	require.Equal(t, cancelled, historyDetail(f, a, execution.ID, "?view=compat_v1"))
}

func TestAutomationHistoryCompatibilityCancelUnknownOwnerResult(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-cancel-inflight")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("send_reply", "inflight-public-secret"), durableAutomationAction("change_priority", "urgent"),
	})
	conv := f.conversation(a)
	f.admit(f.fact(f.con, "conversation_created", conv.ID))
	historyRunStep(f) // CON commits message preparation.
	dependency := f.bridge.Runtime.Config.Dependencies["MSG"]
	delete(f.bridge.Runtime.Config.Dependencies, "MSG")
	historyRunStep(f)
	execution := f.execution(a, rule.ID, "waiting", "")
	var waiting map[string]any
	f.request(a.token, http.MethodPost, a.path(fmt.Sprintf("/automation_executions/%d/cancel?view=compat_v1", execution.ID)), map[string]any{}, http.StatusOK, &waiting)
	require.Equal(t, "waiting", waiting["status"])
	require.Equal(t, "waiting", waiting["raw_status"])
	require.Equal(t, true, waiting["cancel_requested"])
	results := historyLogicalResults(t, waiting)
	require.Len(t, results, 2)
	require.Equal(t, false, results[0]["completed"])
	require.NotEqual(t, "success", results[0]["status"])
	require.Equal(t, "partial_progress", results[0]["outcome"])
	steps := results[0]["owner_steps"].([]any)
	require.Equal(t, "result_unknown", steps[1].(map[string]any)["status"])
	// Reconcile accepted work before skipping the independent later action.
	f.bridge.Runtime.Config.Dependencies["MSG"] = dependency
	require.NoError(t, f.db.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ?", a.id, execution.ID).Update("runner_retry_at", time.Now().UTC().Add(-time.Second)).Error)
	f.flush()
	f.state(a, conv.ID, "open", "low")
	f.execution(a, rule.ID, "cancelled", "cancelled_by_request")
	finished := historyDetail(f, a, execution.ID, "?view=compat_v1")
	results = historyLogicalResults(t, finished)
	historyAssertLogical(t, results[0], 0, "send_reply", "success", "queued", true, "succeeded", "succeeded")
	historyAssertLogical(t, results[1], 1, "change_priority", "skipped", "", false, "skipped")
	historyAssertPrivate(t, finished, "inflight-public-secret")
	var count int64
	require.NoError(t, f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	f.restartBridge()
	require.Zero(t, f.flush())
	require.Equal(t, finished, historyDetail(f, a, execution.ID, "?view=compat_v1"))
}

func TestAutomationHistoryCompatibilityNoMatchAndLoopGuard(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-no-match")
	noMatch := f.rule(a, "conversation_created", []any{map[string]any{"attribute_key": "status", "filter_operator": "equal_to", "values": []string{"pending"}}}, []any{durableAutomationAction("private_note", "unmatched-private-secret")})
	conv := f.conversation(a)
	f.flush()
	execution := f.execution(a, noMatch.ID, "no_match", "conditions_not_matched")
	historyAssertRaw(t, f, a, execution)
	view := historyDetail(f, a, execution.ID, "?view=compat_v1")
	require.Equal(t, "no_match", view["status"])
	require.Equal(t, "no_match", view["raw_status"])
	for _, result := range historyLogicalResults(t, view) {
		require.Equal(t, false, result["completed"])
		require.NotEqual(t, "success", result["status"])
	}
	historyAssertPrivate(t, view, "unmatched-private-secret")
	loop := f.rule(a, "conversation_updated", []any{}, []any{durableAutomationAction("change_status", "pending")})
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/conversations/%d", conv.ID)), map[string]any{"priority": "high"}, http.StatusOK, nil)
	f.flush()
	views := historyList(f, a, loop.ID, "&view=compat_v1")
	require.Len(t, views, 2)
	statuses := map[string]string{}
	for _, result := range views {
		statuses[result["raw_status"].(string)] = result["status"].(string)
	}
	require.Equal(t, map[string]string{"succeeded": "success", "loop_blocked": "depth_exceeded"}, statuses)
}

func TestAutomationHistoryCompatibilityUnknownViewRejected(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-view-query")
	for _, suffix := range []string{"/automation_rules/executions?view=typo", "/automation_rules/executions/1?view=typo"} {
		f.request(a.token, http.MethodGet, a.path(suffix), nil, http.StatusBadRequest, nil)
	}
}

func TestAutomationHistoryCompatibilityKeepsUnmappedExecutionStatuses(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-status")
	// New lifecycle statuses remain distinct; none may be silently coerced into
	// old success/failure categories by ordinary history serialization.
	for _, status := range []string{"received", "evaluating", "running", "waiting", "cancelled", "manual_action_required", "no_match", "future_execution_status", "success", "partial_failed", "depth_exceeded"} {
		t.Run(status, func(t *testing.T) {
			execution := domain.AutomationRuleExecution{AccountID: a.id, RuleID: 900, ConversationID: 800, EventName: "conversation_created", Status: status, ActionResults: "[]", TriggeredAt: time.Now().UTC()}
			require.NoError(t, f.db.Create(&execution).Error)
			view := historyDetail(f, a, execution.ID, "?view=compat_v1")
			require.Equal(t, status, view["status"])
			require.Equal(t, status, view["raw_status"])
			require.Empty(t, historyLogicalResults(t, view))
			historyAssertRaw(t, f, a, execution)
		})
	}
}

func TestAutomationHistoryCompatibilityRejectsMalformedOrUnknownResults(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-invalid-results")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	f.conversation(a)
	f.flush()
	original := f.execution(a, rule.ID, "succeeded", "")
	for _, tc := range []struct{ name, results, state string }{
		{"missing", "", "missing"},
		{"null", "null", "missing"},
		{"empty-array", "[]", "results_mismatch"},
		{"malformed", `[{"status":"succeeded","secret":"malformed-private-secret"}`, "malformed"},
		{"wrong-shape", `{"status":"succeeded","secret":"wrong-shape-private-secret"}`, "malformed"},
		{"unknown-status", strings.ReplaceAll(original.ActionResults, `"succeeded"`, `"future_owner_status"`), "unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, f.db.Model(&domain.AutomationRuleExecution{}).Where("id = ?", original.ID).Update("action_results", tc.results).Error)
			view := historyDetail(f, a, original.ID, "?view=compat_v1")
			require.Equal(t, tc.state, view["action_results_state"], "invalid history must carry the diagnostic rather than claim a verified logical result")
			results := historyLogicalResults(t, view)
			require.NotEmpty(t, results, "missing evidence must not turn into an empty successful result")
			for _, result := range results {
				require.NotEqual(t, true, result["completed"])
				require.NotEqual(t, "success", result["status"])
			}
			historyAssertPrivate(t, view, "malformed-private-secret", "wrong-shape-private-secret")
			// Read-only compatibility diagnosis must not rewrite persisted evidence.
			raw := historyDetail(f, a, original.ID, "")
			require.Equal(t, tc.results, raw["action_results"])
			require.Equal(t, "succeeded", raw["status"])
		})
	}
}

func TestAutomationHistoryCompatibilitySanitizesTypedOwnerSteps(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-typed-steps")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("private_note", "original-private-message")})
	f.conversation(a)
	f.flush()
	execution := f.execution(a, rule.ID, "succeeded", "")
	before := historyDetail(f, a, execution.ID, "?view=compat_v1")
	var stored []map[string]any
	require.NoError(t, json.Unmarshal([]byte(execution.ActionResults), &stored))
	for _, step := range stored {
		step["body"] = "untrusted-owner-body"
		step["snapshot"] = map[string]any{"content": "untrusted-snapshot-body"}
		step["action_params"] = []string{"untrusted-action-params"}
		step["runner_context"] = "untrusted-runner-context"
	}
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, f.db.Model(&domain.AutomationRuleExecution{}).Where("id = ?", execution.ID).Update("action_results", string(encoded)).Error)
	view := historyDetail(f, a, execution.ID, "?view=compat_v1")
	require.Equal(t, before, view, "unknown fields must not enter the allowlisted public owner records")
	historyAssertPrivate(t, view, "original-private-message", "untrusted-owner-body", "untrusted-snapshot-body", "untrusted-action-params", "untrusted-runner-context")
	require.Equal(t, string(encoded), historyDetail(f, a, execution.ID, "")["action_results"], "raw history evidence must remain untouched")
}

func TestAutomationHistoryCompatibilityMissingFrozenVersionNeverUsesLiveRule(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-missing-frozen")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("send_reply", "frozen-reply-body")})
	f.conversation(a)
	f.flush()
	execution := f.execution(a, rule.ID, "succeeded", "")
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), map[string]any{
		"actions": []any{durableAutomationAction("private_note", "current-private-body")},
	}, http.StatusOK, nil)
	// Simulate incomplete historical retention. A current definition with the
	// same step count is still not evidence of the original logical action.
	require.NoError(t, f.db.Where("account_id = ? AND rule_id = ? AND version = ?", a.id, rule.ID, execution.RuleVersion).Delete(&domain.AutomationRuleVersion{}).Error)
	view := historyDetail(f, a, execution.ID, "?view=compat_v1")
	require.Equal(t, "version_missing", view["action_results_state"])
	results := historyLogicalResults(t, view)
	require.NotEmpty(t, results)
	for _, result := range results {
		require.Equal(t, "unresolved", result["status"])
		require.Equal(t, false, result["completed"])
		require.Empty(t, result["action_name"])
		require.NotContains(t, result, "logical_action_index")
	}
	historyAssertPrivate(t, view, "frozen-reply-body", "current-private-body", `"action_name":"private_note"`)
	require.Equal(t, []map[string]any{view}, historyList(f, a, rule.ID, "&view=compat_v1"))
	historyAssertRaw(t, f, a, execution)
}

func TestAutomationHistoryCompatibilityMixesActualLegacyAndDurableRows(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("history-legacy-mixed")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("change_priority", "high"), durableAutomationAction("change_status", "resolved"),
	})
	first := f.conversation(a)
	f.flush()
	f.state(a, first.ID, "resolved", "high")
	durable := f.execution(a, rule.ID, "succeeded", "")
	second := f.conversation(a)
	// Exercise the existing legacy service directly to produce an authentic
	// historical row. The production router remains in durable mode throughout;
	// the second conversation's captured facts are deliberately not drained.
	legacyService := service.NewAutomationService(f.db, f.con, f.msg)
	legacyService.HandleConversationCreated(&second)
	f.state(a, second.ID, "resolved", "high")
	executions := f.executions(a, rule.ID)
	require.Len(t, executions, 2)
	var legacy domain.AutomationRuleExecution
	for _, execution := range executions {
		if execution.RuleVersion == 0 {
			legacy = execution
		}
	}
	require.NotZero(t, legacy.ID)
	require.Equal(t, second.ID, legacy.ConversationID)
	require.Equal(t, "success", legacy.Status)
	require.Empty(t, legacy.Error)
	var legacyResults []service.ActionResult
	require.NoError(t, json.Unmarshal([]byte(legacy.ActionResults), &legacyResults))
	require.Len(t, legacyResults, 2)
	require.Equal(t, "success", legacyResults[0].Status)
	require.Equal(t, "success", legacyResults[1].Status)
	require.Empty(t, legacyResults[1].Error)
	historyAssertRaw(t, f, a, legacy)
	historyAssertRaw(t, f, a, durable)
	views := historyList(f, a, rule.ID, "&view=compat_v1")
	require.Len(t, views, 2)
	for _, view := range views {
		id := uint(view["id"].(float64))
		require.Equal(t, view, historyDetail(f, a, id, "?view=compat_v1"))
		require.Equal(t, "success", view["status"])
		if id == durable.ID {
			require.Equal(t, "succeeded", view["raw_status"])
			require.Equal(t, "logical", view["action_results_state"])
			continue
		}
		require.Equal(t, legacy.ID, id)
		require.Equal(t, "success", view["raw_status"])
		require.Equal(t, "legacy", view["action_results_state"])
		results := historyLogicalResults(t, view)
		require.Len(t, results, len(legacyResults))
		for i, result := range results {
			require.Equal(t, legacyResults[i].ActionName, result["action_name"])
			require.Equal(t, legacyResults[i].Status, result["status"])
			require.Equal(t, legacyResults[i].Status == "success", result["completed"])
			require.Empty(t, result["owner_steps"])
			require.NotContains(t, result, "logical_action_index", "legacy rows cannot prove a frozen logical index")
		}
	}
	for _, query := range []string{"", "?view=compat_v1"} {
		f.request(a.token, http.MethodPost, a.path(fmt.Sprintf("/automation_executions/%d/cancel%s", legacy.ID, query)), map[string]any{}, http.StatusConflict, nil)
	}
	historyAssertRaw(t, f, a, legacy)
}
