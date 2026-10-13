package modruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
)

func historyViewFixture(t *testing.T, actions string, statuses ...string) (domain.AutomationRuleExecution, domain.AutomationRuleVersion) {
	t.Helper()
	plan, reason := compileAutomationContinuingActions(actions)
	require.Empty(t, reason)
	require.Len(t, statuses, len(plan))
	version := domain.AutomationRuleVersion{AccountID: 91, RuleID: 7, Version: 3, EventName: "conversation_created", Actions: actions, Conditions: "[]"}
	now := time.Now().UTC()
	e := domain.AutomationRuleExecution{ID: 41, AccountID: 91, RuleID: 7, RuleVersion: 3, ConversationID: 17, EventName: version.EventName, TargetVersion: 1, RunnerTargetVersion: 1, Status: "succeeded", TriggeredAt: now, CreatedAt: now, CompletedAt: &now}
	runner := automationRunnerContext{FailurePolicy: automationContinueDefinite, Actions: plan, Dispatches: make([]foundation.CommandEnvelope, len(plan))}
	results := make([]automationActionResult, len(plan))
	objectVersion := 1
	for i, a := range plan {
		result := automationActionResult{Index: i, LogicalIndex: a.LogicalIndex, Capability: a.Capability, Step: automationActionStep(a), Status: statuses[i]}
		if result.Status == "succeeded" {
			switch a.Command {
			case "con.status.change.v1", "con.priority.change.v1", "con.automation.message.prepare.v1", "con.automation.apply.v1":
				objectVersion++
			case "cus.automation.label.resolve.v1":
				result.ResourceID, result.Outcome = uint(i+100), "resolved"
			case "msg.automation.message.create.v1":
				result.ResourceID, result.Outcome = uint(i+100), "queued"
				if a.Private {
					result.Outcome = "saved"
				}
			}
			result.ObjectVersion = objectVersion
		} else if result.Status == "failed" {
			result.Reason = "owner_command_rejected"
			if a.FailureReason != "" {
				result.Reason = a.FailureReason
			}
		} else if result.Status == "skipped" {
			result.Reason = "dependency_failed"
		}
		results[i] = result
	}

	e.RunnerNextAction = len(plan)
	e.RunnerTargetVersion = uint(objectVersion)
	e.Status, e.Error = automationCompletedStatus(results)
	for i, result := range results {
		if result.Status == "pending" || result.Status == "accepted" || result.Status == "result_unknown" {
			e.RunnerNextAction, e.CompletedAt, e.Status = i, nil, "received"
			if result.Status == "accepted" {
				e.Status = "running"
			}
			if result.Status == "result_unknown" {
				e.Status = "waiting"
			}
			break
		}
	}
	raw, err := json.Marshal(runner)
	require.NoError(t, err)
	e.RunnerContext = string(raw)
	raw, err = json.Marshal(results)
	require.NoError(t, err)
	e.ActionResults = string(raw)
	return e, version
}

func historyProjection(t *testing.T, e domain.AutomationRuleExecution, version *domain.AutomationRuleVersion) (automationHistoryView, []automationHistoryAction) {
	t.Helper()
	view := AutomationHistoryCompatibilityView(e, version).(automationHistoryView)
	var entries []automationHistoryAction
	require.NoError(t, json.Unmarshal([]byte(view.ActionResults), &entries))
	require.NotNil(t, entries)
	return view, entries
}

func TestAutomationHistoryCompatibilityLogicalOwnerProgress(t *testing.T) {
	cases := []struct {
		name, action, status, outcome string
		steps                         []string
		completed                     bool
	}{
		{"queued_reply", `{"action_name":"send_reply","action_params":"private content"}`, "success", "queued", []string{"succeeded", "succeeded"}, true},
		{"saved_note", `{"action_name":"private_note","action_params":"private content"}`, "success", "saved", []string{"succeeded", "succeeded"}, true},
		{"prepare_only", `{"action_name":"send_reply","action_params":"private content"}`, "failed", "partial_progress", []string{"succeeded", "failed"}, false},
		{"cancel_after_prepare", `{"action_name":"send_reply","action_params":"private content"}`, "skipped", "partial_progress", []string{"succeeded", "skipped"}, false},
		{"lookup_only", `{"action_name":"add_label","action_params":"private-label"}`, "failed", "partial_progress", []string{"succeeded", "failed"}, false},
		{"lookup_pending", `{"action_name":"add_label","action_params":"private-label"}`, "pending", "partial_progress", []string{"succeeded", "pending"}, false},
		{"unknown_create", `{"action_name":"send_reply","action_params":"private content"}`, "result_unknown", "partial_progress", []string{"succeeded", "result_unknown"}, false},
		{"accepted_create", `{"action_name":"send_reply","action_params":"private content"}`, "running", "partial_progress", []string{"succeeded", "accepted"}, false},
		{"multi_label_partial", `{"action_name":"add_labels","action_params":["one","two"]}`, "partial_failed", "partial", []string{"succeeded", "succeeded", "succeeded", "failed"}, false},
		{"unsupported", `{"action_name":"send_webhook","action_params":"https://private.invalid"}`, "failed", "not_completed", []string{"failed"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, version := historyViewFixture(t, "["+tc.action+"]", tc.steps...)
			before := e
			view, entries := historyProjection(t, e, &version)
			require.Equal(t, before, e, "projection must not mutate persisted state")
			require.Equal(t, "logical", view.ActionResultsState)
			require.Len(t, entries, 1)
			entry := entries[0]
			require.Equal(t, tc.status, entry.Status)
			require.Equal(t, tc.outcome, entry.Outcome)
			require.Equal(t, tc.completed, entry.Completed)
			require.Equal(t, 0, *entry.LogicalIndex)
			require.Len(t, entry.OwnerSteps, len(tc.steps))
			for i, step := range entry.OwnerSteps {
				require.Equal(t, tc.steps[i], step.Status)
				require.Equal(t, i, *step.Index)
				require.Equal(t, 0, *step.LogicalIndex)
			}
			raw, err := json.Marshal(view)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "private content")
			require.NotContains(t, string(raw), "private-label")
			require.NotContains(t, string(raw), "private.invalid")
		})
	}
}

func TestAutomationHistoryCompatibilityFrozenAliasesAndStrictPlans(t *testing.T) {
	raw := `[{"name":"reply","params":"message body"},{"capability_id":"conversation.change_priority","action_params":"high"},{"action_name":"close"}]`
	e, version := historyViewFixture(t, raw, "succeeded", "succeeded", "succeeded", "succeeded")
	view, entries := historyProjection(t, e, &version)
	require.Equal(t, "logical", view.ActionResultsState)
	require.Equal(t, []string{"reply", "conversation.change_priority", "close"}, []string{entries[0].ActionName, entries[1].ActionName, entries[2].ActionName})
	var runner automationRunnerContext
	var results []automationActionResult
	require.NoError(t, json.Unmarshal([]byte(e.RunnerContext), &runner))
	require.NoError(t, json.Unmarshal([]byte(e.ActionResults), &results))
	runner.FailurePolicy = ""
	for i := range runner.Actions {
		runner.Actions[i].LogicalIndex, results[i].LogicalIndex = nil, nil
	}
	encoded, _ := json.Marshal(runner)
	e.RunnerContext = string(encoded)
	encoded, _ = json.Marshal(results)
	e.ActionResults = string(encoded)
	view, entries = historyProjection(t, e, &version)
	require.Equal(t, "logical", view.ActionResultsState)
	require.Len(t, entries, 3)
	require.Equal(t, 1, *entries[1].LogicalIndex)
	require.Nil(t, entries[0].OwnerSteps[0].LogicalIndex, "do not invent historical owner indexes")
	require.True(t, entries[0].Completed)
	version.Actions = `[{"action_name":"send_message","action_params":"changed body"},{"action_name":"change_priority","action_params":"low"},{"action_name":"close"}]`
	view, entries = historyProjection(t, e, &version)
	require.Equal(t, "plan_mismatch", view.ActionResultsState)
	require.False(t, entries[0].Completed)
	require.Empty(t, entries[0].ActionName)
}

func TestAutomationHistoryCompatibilityDiagnosticsAndSanitization(t *testing.T) {
	base, frozen := historyViewFixture(t, `[{"action_name":"reply","action_params":"SECRET-BODY"}]`, "succeeded", "succeeded")
	cases := []struct {
		name, state string
		change      func(*domain.AutomationRuleExecution, *domain.AutomationRuleVersion)
	}{
		{"missing", "missing", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) { e.ActionResults = "" }},
		{"null", "missing", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) { e.ActionResults = "null" }},
		{"broken", "malformed", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = "[SECRET-BODY"
		}},
		{"object", "malformed", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) { e.ActionResults = `{}` }},
		{"null_row", "malformed", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) { e.ActionResults = `[null]` }},
		{"missing_status", "malformed", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = `[{"action_index":0}]`
		}},
		{"duplicate_status", "malformed", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = `[{"status":"failed","status":"succeeded"}]`
		}},
		{"missing_index", "results_mismatch", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = strings.Replace(e.ActionResults, `"action_index":0,`, "", 1)
		}},
		{"missing_group", "results_mismatch", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = strings.Replace(e.ActionResults, `"logical_action_index":0,`, "", 1)
		}},
		{"wrong_group", "results_mismatch", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = strings.Replace(e.ActionResults, `"logical_action_index":0`, `"logical_action_index":9`, 1)
		}},
		{"empty_results", "results_mismatch", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) { e.ActionResults = "[]" }},
		{"wrong_version", "version_mismatch", func(_ *domain.AutomationRuleExecution, v *domain.AutomationRuleVersion) { v.Version++ }},
		{"foreign_version", "version_mismatch", func(_ *domain.AutomationRuleExecution, v *domain.AutomationRuleVersion) { v.AccountID++ }},
		{"wrong_event", "version_mismatch", func(_ *domain.AutomationRuleExecution, v *domain.AutomationRuleVersion) {
			v.EventName = "message_created"
		}},
		{"bad_definition", "definition_invalid", func(_ *domain.AutomationRuleExecution, v *domain.AutomationRuleVersion) { v.Actions = "[broken" }},
		{"bad_runner", "plan_mismatch", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) { e.RunnerContext = "[broken" }},
		{"changed_runner", "plan_mismatch", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.RunnerContext = strings.ReplaceAll(e.RunnerContext, "SECRET-BODY", "NEW-SECRET")
		}},
		{"missing_cursor", "unresolved", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) { e.RunnerNextAction = 0 }},
		{"bad_receipt_version", "unresolved", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = strings.ReplaceAll(e.ActionResults, `"object_version":2`, `"object_version":8`)
		}},
		{"future_status", "unresolved", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = strings.Replace(e.ActionResults, `"status":"succeeded"`, `"status":"future_status"`, 1)
		}},
		{"future_outcome", "unresolved", func(e *domain.AutomationRuleExecution, _ *domain.AutomationRuleVersion) {
			e.ActionResults = strings.Replace(e.ActionResults, `"outcome":"queued"`, `"outcome":"future_outcome"`, 1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, version := base, frozen
			tc.change(&e, &version)
			e.Error = "PRIVATE error body"
			e.Rule = &domain.AutomationRule{Name: "PRIVATE rule name", Actions: "PRIVATE"}
			e.Conversation = &domain.Conversation{CustomAttributes: "PRIVATE"}
			before := e.ActionResults
			view, entries := historyProjection(t, e, &version)
			require.Equal(t, tc.state, view.ActionResultsState)
			require.NotEmpty(t, entries)
			for _, entry := range entries {
				require.False(t, entry.Completed)
				require.NotEqual(t, "success", entry.Status)
			}
			require.Equal(t, before, e.ActionResults)
			raw, _ := json.Marshal(view)
			for _, secret := range []string{"PRIVATE", "SECRET-BODY", "NEW-SECRET", "runner_context", "runner_evaluation", "dispatches", "action_params", "snapshot"} {
				require.NotContains(t, string(raw), secret)
			}
			if tc.name == "future_status" {
				require.Contains(t, view.ActionResults, "future_status")
			}
			if tc.name == "future_outcome" {
				require.Contains(t, view.ActionResults, "future_outcome")
			}
		})
	}
	view, entries := historyProjection(t, base, nil)
	require.Equal(t, "version_missing", view.ActionResultsState)
	require.False(t, entries[0].Completed)
}

func TestAutomationHistoryCompatibilityLegacyAndEmpty(t *testing.T) {
	now := time.Now().UTC()
	e := domain.AutomationRuleExecution{ID: 3, AccountID: 91, RuleID: 7, ConversationID: 2, Status: "partial_failed", Error: "future_reason", TriggeredAt: now, CreatedAt: now, ActionResults: `[{"action_name":"reply","status":"success","private_content":"SECRET"},{"action_name":"send_webhook","status":"failed","error":"future_reason","dispatch":{"secret":"SECRET"}}]`}
	view, entries := historyProjection(t, e, nil)
	require.Equal(t, "legacy", view.ActionResultsState)
	require.Equal(t, "partial_failed", view.Status)
	require.Equal(t, "partial_failed", view.RawStatus)
	require.Equal(t, "future_reason", view.ReasonCode)
	require.Nil(t, entries[0].LogicalIndex)
	require.True(t, entries[0].Completed)
	require.Equal(t, "legacy_success", entries[0].Outcome)
	require.Equal(t, "future_reason", entries[1].Error)
	require.NotContains(t, view.ActionResults, "SECRET")
	require.NotContains(t, view.ActionResults, "dispatch")
	for raw, want := range map[string]string{"succeeded": "success", "partially_succeeded": "partial_failed", "loop_blocked": "depth_exceeded", "no_match": "no_match", "waiting": "waiting", "future_status": "future_status", "future / status": "future / status"} {
		e.Status = raw
		view, _ = historyProjection(t, e, nil)
		require.Equal(t, raw, view.RawStatus)
		require.Equal(t, want, view.Status)
	}
	e, version := historyViewFixture(t, "[]")
	e.RunnerContext = ""
	view, entries = historyProjection(t, e, &version)
	require.Equal(t, "empty", view.ActionResultsState)
	require.Empty(t, entries)
	require.Equal(t, "[]", view.ActionResults)
}

func TestAutomationHistoryCompatibilityEarlyTerminalResults(t *testing.T) {
	for _, tc := range []struct {
		status, reason string
		first          string
	}{
		{"no_match", "conditions_not_matched", "skipped"},
		{"loop_blocked", "automation_loop_guard", "skipped"},
		{"failed", "action_capability_executor_unavailable", "failed"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			e, version := historyViewFixture(t, `[{"action_name":"send_webhook"},{"action_name":"close"}]`, "failed", "pending")
			e.RunnerContext = ""
			e.Status, e.Error = tc.status, tc.reason
			now := time.Now().UTC()
			e.CompletedAt = &now
			e.ActionResults = `[{"action_index":0,"status":"` + tc.first + `","reason_code":"` + tc.reason + `"},{"action_index":1,"status":"skipped","reason_code":"` + tc.reason + `"}]`
			view, entries := historyProjection(t, e, &version)
			require.Equal(t, "logical", view.ActionResultsState)
			require.Equal(t, "send_webhook", entries[0].ActionName)
			require.Equal(t, tc.first, entries[0].Status)
			for _, entry := range entries {
				require.False(t, entry.Completed)
				require.Empty(t, entry.OwnerSteps)
				require.Equal(t, "not_executed", entry.Outcome)
			}
		})
	}
}

func TestAutomationHistoryCompatibilityDeferredAndRestoredCancellation(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryResolved, `[{"action_name":"send_reply","action_params":"PRIVATE-DEFERRED"}]`)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	require.Len(t, rows, 2)
	pending := f.execution(rows[1].ID)
	var version domain.AutomationRuleVersion
	require.NoError(t, f.aut.DB.Where("account_id = ? AND rule_id = ? AND version = ?", pending.AccountID, pending.RuleID, pending.RuleVersion).First(&version).Error)
	view, entries := historyProjection(t, pending, &version)
	require.Equal(t, "logical", view.ActionResultsState)
	require.Equal(t, "pending", entries[0].Status)
	require.Equal(t, "not_evaluated", entries[0].Outcome)
	restored := failureRecoveryRestore(t, f)
	pending = restored.execution(pending.ID)
	// Use the fixture's ordinary module path to retain account/command validation.
	command := restored.startCommand()
	command.CommandID, command.CommandType = "history-cancel", "aut.execution.cancel.v1"
	command.Target.Type, command.Target.ID = "execution", strconv.Itoa(int(pending.ID))
	command.Input = map[string]any{}
	restored.call(command, http.StatusOK)
	cancelled := restored.execution(pending.ID)
	view, entries = historyProjection(t, cancelled, &version)
	require.Equal(t, "logical", view.ActionResultsState)
	require.Equal(t, "cancelled", view.Status)
	require.Equal(t, "skipped", entries[0].Status)
	require.Equal(t, "not_executed", entries[0].Outcome)
	require.False(t, entries[0].Completed)
	require.NotContains(t, view.ActionResults, "PRIVATE-DEFERRED")
}

func TestAutomationHistoryCompatibilityCausalSiblingHistoricalBase(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"change_priority","action_params":"high"}]`)
	for _, raw := range []string{`[{"action_name":"add_label","action_params":"private-label"}]`, `[{"action_name":"send_reply","action_params":"private-body"}]`} {
		siblingRecoveryRule(t, f.autRunnerFixture, "[]", raw)
	}
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	require.Len(t, rows, 3)
	for _, row := range rows {
		final := f.drain(row.ID)
		require.Equal(t, "succeeded", final.Status)
		require.Empty(t, final.RunnerEvaluation)
		var version domain.AutomationRuleVersion
		require.NoError(t, f.aut.DB.Where("account_id = ? AND rule_id = ? AND version = ?", final.AccountID, final.RuleID, final.RuleVersion).First(&version).Error)
		view, entries := historyProjection(t, final, &version)
		require.Equal(t, "logical", view.ActionResultsState)
		require.True(t, entries[0].Completed)
		require.Equal(t, "success", entries[0].Status)
	}
}
