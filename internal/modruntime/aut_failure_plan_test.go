package modruntime

import (
	"encoding/json"
	"fmt"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAUTFailurePlanBoundsReserveLaterLogicalActions(t *testing.T) {
	labels := make([]string, 100)
	for i := range labels {
		labels[i] = fmt.Sprintf("label-%d", i)
	}
	definitions := make([]map[string]any, 100)
	for i := 0; i < 99; i++ {
		definitions[i] = map[string]any{"action_name": "add_labels", "action_params": labels}
	}
	definitions[99] = map[string]any{"action_name": "change_priority", "action_params": "urgent"}
	raw, err := json.Marshal(definitions)
	require.NoError(t, err)
	plan, reason := compileAutomationContinuingActions(string(raw))
	require.Empty(t, reason)
	require.True(t, validAutomationPlan(plan))
	require.LessOrEqual(t, len(plan), automationMaxSteps)
	last := plan[len(plan)-1]
	require.Equal(t, "con.priority.change.v1", last.Command)
	require.Equal(t, 99, *last.LogicalIndex)
	failed := false
	for _, step := range plan {
		if step.FailureReason != "" {
			failed = true
			require.Equal(t, "invalid_action_parameters", step.FailureReason)
		}
	}
	require.True(t, failed)
	definitions = append(definitions, definitions[99])
	raw, err = json.Marshal(definitions)
	require.NoError(t, err)
	plan, reason = compileAutomationContinuingActions(string(raw))
	require.Nil(t, plan)
	require.Equal(t, "invalid_frozen_definition", reason)
}

func TestAUTFailureSnapshotStandaloneTerminalResultValidation(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_webhook"},{"action_name":"change_priority","action_params":"urgent"}]`)
	e := f.call(f.startCommand(), 200)
	final := f.drain(e.ID)
	require.Equal(t, "partially_succeeded", final.Status)
	require.NoError(t, validateAutomationSnapshotExecution(final))
	for name, edit := range map[string]func(*domain.AutomationRuleExecution){
		"false_success":        func(e *domain.AutomationRuleExecution) { e.Status = "succeeded"; e.Error = "" },
		"false_failure":        func(e *domain.AutomationRuleExecution) { e.Status = "failed" },
		"missing_completion":   func(e *domain.AutomationRuleExecution) { e.CompletedAt = nil },
		"unfinished_suffix":    func(e *domain.AutomationRuleExecution) { e.RunnerNextAction = 1 },
		"wrong_failure_reason": func(e *domain.AutomationRuleExecution) { e.Error = "owner_command_rejected" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := final
			edit(&copy)
			require.Error(t, validateAutomationSnapshotExecution(copy))
		})
	}
}
