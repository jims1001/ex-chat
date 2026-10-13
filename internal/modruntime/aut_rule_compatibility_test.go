package modruntime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func compatibilityFixture(conditions, actions string) domain.AutomationCompatibilityInput {
	rule := domain.AutomationRule{ID: 7, AccountID: 91, Active: true, EventName: "conversation_created", Conditions: conditions, Actions: actions, UpdatedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)}
	return domain.AutomationCompatibilityInput{Rule: rule, Frozen: &domain.AutomationRuleVersion{AccountID: 91, RuleID: 7, Version: 2, EventName: rule.EventName, Conditions: conditions, Actions: actions}}
}
func compatibilityHas(out domain.AutomationCompatibilitySource, code string) bool {
	for _, issue := range out.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
func TestAUTCompatibilityConditionMatrix(t *testing.T) {
	cases := []struct {
		name, raw, code string
		want            bool
	}{
		{"empty", "", "invalid_condition_definition", false}, {"array", "[]", "invalid_condition_definition", false}, {"object", "{}", "invalid_condition_definition", false},
		{"null", "null", "invalid_condition_definition", true}, {"empty_wrapper", `{"conditions":[]}`, "invalid_condition_definition", true}, {"spaced_object", "{ }", "invalid_condition_definition", true}, {"spaced_array", "[ ]", "invalid_condition_definition", true}, {"malformed", "[", "invalid_condition_definition", true},
		{"single", `{"key":"status","operator":"is","value":"open"}`, "invalid_condition_definition", false},
		{"wrappers", `{"conditions":[{"key":"status"}],"rules":[{"key":"priority"}],"values":[{"key":"email"}]}`, "multiple_definition_wrappers", true},
		{"aliases", `[{"attribute_key":"status","key":"priority","filter_operator":"is","operator":"is_not"}]`, "conflicting_condition_alias", true},
		{"empty_values_fallback", `[{"key":"status","operator":"is","values":[],"value":"open"}]`, "empty_condition_operands", false},
		{"values_conflict", `[{"key":"status","operator":"is","values":["open"],"value":"pending"}]`, "conflicting_condition_values", true},
		{"labels_prefix", `[{"key":"labels","operator":"starts_with","values":["x"]}]`, "unsupported_label_operator", true},
		{"lowercase_or", `[{"key":"status","operator":"is","query_operator":"or"},{"key":"status","operator":"is"}]`, "noncanonical_connector", false},
		{"spaced_or", `[{"key":"status","operator":"is","query_operator":" OR "},{"key":"status","operator":"is"}]`, "noncanonical_connector", true},
		{"left_fold", `[{"key":"status","operator":"is","query_operator":"OR"},{"key":"status","operator":"is","query_operator":"AND"},{"key":"status","operator":"is"}]`, "mixed_connectors_left_fold", true},
		{"last_connector", `[{"key":"status","operator":"is","query_operator":"OR"}]`, "ignored_final_connector", true},
		{"empty_operands", `[{"key":"status","operator":"is_not","values":[]}]`, "empty_condition_operands", true},
		{"presence_ignored", `[{"key":"status","operator":"is_present","values":["x"]}]`, "presence_operands_ignored", true},
		{"typed", `[{"key":"tier","operator":"is","custom_attribute_type":"contact_attribute","value":"x"}]`, "typed_custom_scope_ignored", true},
		{"literal_namespace", `[{"key":"custom_attribute.tier","operator":"is_not_present"}]`, "custom_definition_unverified", true},
		{"missing_message", `[{"key":"content","operator":"is_not_present"}]`, "message_context_absent", true},
		{"precision", `[{"key":"status","operator":"is","values":[9007199254740993]}]`, "condition_numeric_precision_risk", true},
		{"object_values", `[{"key":"status","operator":"is","values":{"x":"secret"}}]`, "invalid_condition_definition", true},
		{"duplicates", `[{"attribute_key":"status","attribute_key":"priority","Filter_Operator":"is"}]`, "ambiguous_json_fields", true},
		{"nested", `[{"conditions":[{"key":"status","operator":"is"}]}]`, "unsupported_condition_shape", true},
		{"uppercase", `[{"key":"status","operator":"IS","values":["open"]}]`, "unsupported_condition_operator", true},
		{"null_bool_number", `[{"key":"status","operator":"is","values":[null,false,0,{},[]]}]`, "condition_stringification", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := AutomationRuleCompatibilityView(compatibilityFixture(tc.raw, "[]"))
			require.Equal(t, tc.want, compatibilityHas(out.Sources[0], tc.code), "%+v", out.Sources[0])
		})
	}
	for _, op := range []string{"is_greater_than", "is_less_than", "days_before", "attribute_changed", "changed", "SECRET-OPERATOR"} {
		raw := fmt.Sprintf(`[{"key":"priority","operator":%q,"values":[1],"query_operator":"OR"},{"key":"status","operator":"is","values":["open"]}]`, op)
		out := AutomationRuleCompatibilityView(compatibilityFixture(raw, "[]"))
		require.True(t, compatibilityHas(out.Sources[0], "unsupported_condition_operator"))
		match, reason := matchAutomationConditions(raw, &automationConditionContext{Conversation: conversationView{Status: "open"}})
		require.True(t, match)
		require.Empty(t, reason)
		for _, issue := range out.Sources[0].Issues {
			if issue.Code == "unsupported_condition_operator" {
				require.Equal(t, "condition_leaf_false", issue.RuntimeEffect)
			}
		}
	}
}
func TestAUTCompatibilityEventsVersionsAndNormalization(t *testing.T) {
	for _, event := range []string{"conversation_created", "conversation_updated", "conversation_opened", "conversation_resolved", "message_created", "SECRET-EVENT", ""} {
		in := compatibilityFixture("[]", "[]")
		in.Rule.EventName = event
		in.Frozen.EventName = event
		out := AutomationRuleCompatibilityView(in)
		require.Equal(t, compatEvent(event) == "unknown", compatibilityHas(out.Sources[0], "unsupported_event"))
		require.Equal(t, event == "conversation_opened" || event == "conversation_resolved", compatibilityHas(out.Sources[0], "newly_effective_transition_behavior"))
	}
	in := compatibilityFixture(`[{"key":"tier","operator":"is","values":["gold"],"custom_attribute_type":"contact_attribute"}]`, `[{"action_name":"change_status","action_params":"open"}]`)
	in.Frozen.Conditions = `[{"attribute_key":"tier","filter_operator":"is","values":["gold"]}]`
	in.Frozen.Actions = `[{"action_name":"change_status","action_params":["open"]}]`
	out := AutomationRuleCompatibilityView(in)
	require.True(t, compatibilityHas(out.Sources[0], "saved_frozen_definition_drift"))
	require.True(t, compatibilityHas(out.Sources[1], "frozen_metadata_not_preserved"))
	in.Rule.Conditions = `[{"key":"tier","operator":"is","value":"gold"}]`
	out = AutomationRuleCompatibilityView(in)
	require.False(t, compatibilityHas(out.Sources[0], "saved_frozen_definition_drift"))
	in.Rule.Conditions = `[{"attribute_key":"tier","filter_operator":"equal_to","values":["gold"]}]`
	out = AutomationRuleCompatibilityView(in)
	require.False(t, compatibilityHas(out.Sources[0], "saved_frozen_definition_drift"))
	in.Rule.Conditions = "{}"
	in.Frozen.Conditions = "[]"
	out = AutomationRuleCompatibilityView(in)
	require.False(t, compatibilityHas(out.Sources[0], "saved_frozen_definition_drift"))
	in.Frozen.Actions = `[{"action_name":"change_status","action_params":["pending"]}]`
	out = AutomationRuleCompatibilityView(in)
	require.True(t, compatibilityHas(out.Sources[0], "saved_frozen_definition_drift"))
	in.Preview = in.Frozen
	in.Frozen = nil
	out = AutomationRuleCompatibilityView(in)
	require.Nil(t, out.LatestVersion)
	require.Equal(t, "activation_preview", out.Sources[1].Source)
	require.True(t, compatibilityHas(out.Sources[1], "version_not_frozen"))
	in.Rule.Active = false
	inactive := AutomationRuleCompatibilityView(in)
	require.Equal(t, out.Sources, inactive.Sources)
	in.Preview = nil
	out = AutomationRuleCompatibilityView(in)
	require.False(t, out.Complete)
	require.True(t, compatibilityHas(out.Sources[1], "activation_preview_unavailable"))
}
func TestAUTCompatibilityAnalysisBounds(t *testing.T) {
	for _, size := range []int{domain.AutomationCompatibilityMaxBytes - 1, domain.AutomationCompatibilityMaxBytes, domain.AutomationCompatibilityMaxBytes + 1} {
		raw := strings.Repeat(" ", size)
		out := AutomationRuleCompatibilityView(compatibilityFixture(raw, "[]"))
		require.Equal(t, size <= domain.AutomationCompatibilityMaxBytes, out.Complete)
		require.Equal(t, size > domain.AutomationCompatibilityMaxBytes, compatibilityHas(out.Sources[0], "analysis_limit_exceeded"))
	}
	for _, depth := range []int{32, 33} {
		raw := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		out := AutomationRuleCompatibilityView(compatibilityFixture(raw, "[]"))
		require.Equal(t, depth > 32, compatibilityHas(out.Sources[0], "analysis_limit_exceeded"))
	}
	for _, count := range []int{1000, 1001} {
		items := make([]string, count)
		for i := range items {
			items[i] = `{"key":"status","operator":"is_present","query_operator":"AND"}`
		}
		out := AutomationRuleCompatibilityView(compatibilityFixture("["+strings.Join(items, ",")+"]", "[]"))
		require.Equal(t, count > 1000, compatibilityHas(out.Sources[0], "analysis_limit_exceeded"))
	}
	for _, count := range []int{10000, 10001} {
		raw := `[{"key":"status","operator":"is","values":[` + strings.Repeat(`"x",`, count-1) + `"x"]}]`
		out := AutomationRuleCompatibilityView(compatibilityFixture(raw, "[]"))
		require.Equal(t, count > 10000, compatibilityHas(out.Sources[0], "analysis_limit_exceeded"))
	}
	items := make([]string, 201)
	for i := range items {
		items[i] = `{"key":"SECRET-KEY","operator":"SECRET-OP"}`
	}
	out := AutomationRuleCompatibilityView(compatibilityFixture("["+strings.Join(items, ",")+"]", "[]"))
	require.False(t, out.Complete)
	require.Len(t, out.Sources[0].Issues, 200)
	require.True(t, out.Sources[0].IssuesTruncated)
	require.Greater(t, out.Sources[0].IssuesOmitted, 0)
	in := compatibilityFixture("", "[]")
	in.SavedConditionBytes = domain.AutomationCompatibilityMaxBytes + 1
	in.EffectiveConditionBytes = in.SavedConditionBytes
	out = AutomationRuleCompatibilityView(in)
	require.False(t, out.Complete)
	require.True(t, compatibilityHas(out.Sources[0], "analysis_limit_exceeded"))
	require.True(t, compatibilityHas(out.Sources[1], "analysis_limit_exceeded"))
}
func TestAUTCompatibilityNumericInspectionCostBound(t *testing.T) {
	for _, number := range []string{"1e4097", "1e-4097", "1e100000000000000000000000", strings.Repeat("1", 129)} {
		in := compatibilityFixture("[]", `[{"action_name":"assign_agent","action_params":`+number+`}]`)
		out := AutomationRuleCompatibilityView(in)
		require.False(t, out.Complete)
		require.True(t, compatibilityHas(out.Sources[0], "analysis_limit_exceeded"))
		require.False(t, compatibilityHas(out.Sources[0], "invalid_action_parameters"))
	}
	out := AutomationRuleCompatibilityView(compatibilityFixture("[]", `[{"action_name":"assign_agent","action_params":1e4096}]`))
	require.True(t, out.Complete)
	require.True(t, compatibilityHas(out.Sources[0], "invalid_action_parameters"))
}

func TestAUTCompatibilityRedactionDeterminismAndConfig(t *testing.T) {
	in := compatibilityFixture(`[{"attribute_key":"SECRET-CUSTOM","filter_operator":"SECRET-OP","values":["SECRET-VALUE",{"SECRET-KEY":"SECRET-NESTED"}],"SECRET-META":{"SECRET-CONFIG":"SECRET-DATA"},"custom_attribute_type":"SECRET-TYPE"}]`, `[{"action_name":"send_message","action_params":{"message":"SECRET-MESSAGE","content":"SECRET-OTHER"}},{"action_name":"SECRET-ACTION","action_params":"https://SECRET-URL/?x=SECRET-TOKEN"}]`)
	in.Rule.Name = "SECRET-NAME"
	in.Rule.Description = "SECRET-DESC"
	in.Rule.EventName = "SECRET-EVENT"
	in.Frozen.EventName = in.Rule.EventName
	out := AutomationRuleCompatibilityView(in)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET")
	require.NotContains(t, string(raw), "https:")
	for i := 0; i < 20; i++ {
		other, err := json.Marshal(AutomationRuleCompatibilityView(in))
		require.NoError(t, err)
		require.Equal(t, raw, other)
	}
	in.Profile = domain.AutomationCompatibilityProfile{Assessed: true, Owners: map[string]bool{"CON": true}}
	out = AutomationRuleCompatibilityView(in)
	require.Equal(t, "composition_profile", out.Sources[0].AssessmentLevel)
	require.Contains(t, out.Sources[0].RequiredOwners, "MSG")
	require.True(t, compatibilityHas(out.Sources[0], "dependency_missing"))
	in = compatibilityFixture(`[{"key":"email","operator":"is_present"}]`, `[]`)
	in.Profile = domain.AutomationCompatibilityProfile{Assessed: true, Owners: map[string]bool{"CON": true}}
	out = AutomationRuleCompatibilityView(in)
	require.True(t, compatibilityHas(out.Sources[0], "conditional_dependency_missing"))
	require.Contains(t, out.Sources[0].RequiredOwners, "CUS")
	in = compatibilityFixture("[]", "[]")
	a := AutomationRuleCompatibilityView(in)
	in.Rule.Conditions = `[{"operator":"is","key":"status","value":"open"}]`
	in.Frozen.Conditions = in.Rule.Conditions
	b := AutomationRuleCompatibilityView(in)
	in.Rule.Conditions = `[{"value":"open","key":"status","operator":"is"}]`
	in.Frozen.Conditions = in.Rule.Conditions
	c := AutomationRuleCompatibilityView(in)
	require.Equal(t, b, c)
	require.NotEqual(t, a, b)
}
