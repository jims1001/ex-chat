package modruntime

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func compatibilityNumericFixture(conditions string) domain.AutomationCompatibilityInput {
	in := compatibilityFixture(conditions, "[]")
	in.Rule.ConditionSemantics = domain.AutomationNumericCustomV1
	in.Frozen.ConditionSemantics = domain.AutomationNumericCustomV1
	return in
}

func compatibilityNumericLeaf(key, model, threshold string) string {
	return fmt.Sprintf(`{"attribute_key":%q,"filter_operator":"is_greater_than","custom_attribute_type":%q,"custom_attribute_definition_id":812345671,"values":[%s]}`, key, model, threshold)
}

func TestAUTCompatibilityNumericAdoptionAndRedaction(t *testing.T) {
	for _, model := range []string{"conversation_attribute", "contact_attribute"} {
		for _, threshold := range []string{"9007199254740993", "1e1000", strings.Repeat("7", domain.AutomationNumericMaxLiteralBytes)} {
			in := compatibilityNumericFixture("[" + compatibilityNumericLeaf("PRIVATE-CUSTOM-KEY", model, threshold) + "]")
			out := AutomationRuleCompatibilityView(in)
			require.True(t, out.Complete)
			for _, source := range out.Sources {
				require.Equal(t, domain.AutomationNumericCustomV1, source.ConditionSemantics)
				require.Equal(t, 1, source.Conditions)
				require.Equal(t, []string{"CON", "CUS"}, source.RequiredOwners)
				require.True(t, compatibilityHas(source, "numeric_custom_comparison"))
				require.True(t, compatibilityHas(source, "numeric_custom_catalog_type_value_not_assessed"))
				for _, code := range []string{"unsupported_condition_operator", "invalid_condition_definition", "invalid_numeric_condition_definition", "condition_numeric_precision_risk", "condition_stringification", "typed_custom_scope_ignored", "ignored_definition_metadata", "custom_definition_unverified", "missing_custom_key_false", "empty_condition_operands", "saved_frozen_definition_drift"} {
					require.False(t, compatibilityHas(source, code), "%s: %+v", code, source)
				}
			}
			raw, err := json.Marshal(out)
			require.NoError(t, err)
			for _, secret := range []string{"PRIVATE-CUSTOM-KEY", model, "812345671", threshold} {
				require.NotContains(t, string(raw), secret)
			}
			for i := 0; i < 3; i++ {
				other, err := json.Marshal(AutomationRuleCompatibilityView(in))
				require.NoError(t, err)
				require.Equal(t, raw, other)
			}
		}
	}
	// Matching a builtin name does not change an adopted custom field's scope.
	for _, key := range []string{"labels", "content", "status"} {
		out := AutomationRuleCompatibilityView(compatibilityNumericFixture("[" + compatibilityNumericLeaf(key, "contact_attribute", "3") + "]"))
		for _, code := range []string{"label_comparison_semantics", "unsupported_label_operator", "message_context_absent"} {
			require.False(t, compatibilityHas(out.Sources[0], code))
		}
	}
}

func TestAUTCompatibilityNumericDoesNotChangeLegacyOrOtherLeaves(t *testing.T) {
	leaf := compatibilityNumericLeaf("PRIVATE-CUSTOM-KEY", "contact_attribute", "9007199254740993")
	legacy := AutomationRuleCompatibilityView(compatibilityFixture("["+leaf+"]", "[]"))
	require.Empty(t, legacy.Sources[0].ConditionSemantics)
	for _, code := range []string{"unsupported_condition_operator", "condition_numeric_precision_risk", "condition_stringification", "typed_custom_scope_ignored"} {
		require.True(t, compatibilityHas(legacy.Sources[0], code), code)
	}
	require.False(t, compatibilityHas(legacy.Sources[0], "numeric_custom_comparison"))

	other := `{"attribute_key":"OTHER-PRIVATE-KEY","filter_operator":"is","custom_attribute_type":"contact_attribute","values":[9007199254740993]}`
	in := compatibilityNumericFixture("[" + leaf + "," + other + "]")
	out := AutomationRuleCompatibilityView(in)
	for _, code := range []string{"condition_numeric_precision_risk", "condition_stringification", "typed_custom_scope_ignored"} {
		found := false
		for _, issue := range out.Sources[0].Issues {
			if issue.Code == code {
				found = true
				require.True(t, strings.HasPrefix(issue.Path, "/conditions/1/"), "%+v", issue)
			}
		}
		require.True(t, found, code)
	}
	for _, operator := range []string{"days_before", "attribute_changed", "changed"} {
		in := compatibilityNumericFixture(strings.ReplaceAll("["+leaf+"]", "is_greater_than", operator))
		out := AutomationRuleCompatibilityView(in)
		require.True(t, compatibilityHas(out.Sources[0], "unsupported_condition_operator"))
		require.False(t, compatibilityHas(out.Sources[0], "numeric_custom_comparison"))
	}
}

func TestAUTCompatibilityNumericInvalidAndUnknownProfiles(t *testing.T) {
	valid := compatibilityNumericLeaf("PRIVATE-CUSTOM-KEY", "conversation_attribute", "1")
	invalid := []string{
		strings.Replace(valid, `"custom_attribute_definition_id":812345671,`, "", 1),
		strings.Replace(valid, "812345671", "0", 1),
		strings.Replace(valid, "conversation_attribute", "PRIVATE-MODEL", 1),
		strings.Replace(valid, "PRIVATE-CUSTOM-KEY", "", 1),
		strings.Replace(valid, `"values":[1]`, `"values":["PRIVATE-THRESHOLD"]`, 1),
		strings.Replace(valid, `"values":[1]`, `"values":[1,2]`, 1),
		strings.Replace(valid, `"values":[1]`, `"values":[null]`, 1),
		strings.Replace(valid, `"values":[1]`, `"values":[1e1025]`, 1),
	}
	for _, raw := range invalid {
		out := AutomationRuleCompatibilityView(compatibilityNumericFixture("[" + raw + "]"))
		require.True(t, compatibilityHas(out.Sources[0], "invalid_numeric_condition_definition"), raw)
		require.False(t, compatibilityHas(out.Sources[0], "numeric_custom_comparison"))
		encoded, err := json.Marshal(out)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "PRIVATE")
		require.NotContains(t, string(encoded), "812345671")
	}
	// The profile's actual 100-condition runtime limit is a static rejection.
	tooMany := "[" + strings.Repeat(valid+",", domain.AutomationNumericMaxConditions) + valid + "]"
	out := AutomationRuleCompatibilityView(compatibilityNumericFixture(tooMany))
	require.True(t, compatibilityHas(out.Sources[0], "invalid_numeric_condition_definition"))
	in := compatibilityNumericFixture("[" + valid + "]")
	in.Rule.ConditionSemantics = strings.Repeat("PRIVATE-PROFILE", 100000)
	in.Frozen.ConditionSemantics = in.Rule.ConditionSemantics
	out = AutomationRuleCompatibilityView(in)
	for _, source := range out.Sources {
		require.Equal(t, "unknown", source.ConditionSemantics)
		require.True(t, compatibilityHas(source, "unsupported_condition_semantics"))
		require.False(t, compatibilityHas(source, "numeric_custom_comparison"))
	}
	encoded, err := json.Marshal(out)
	require.NoError(t, err)
	require.Less(t, len(encoded), 10000)
	require.NotContains(t, string(encoded), "PRIVATE")
}

func TestAUTCompatibilityNumericDriftIsExactAndProfileAware(t *testing.T) {
	base := "[" + compatibilityNumericLeaf("PRIVATE-CUSTOM-KEY", "contact_attribute", "9007199254740993") + "]"
	for _, mutate := range []func(*domain.AutomationRuleVersion){
		func(v *domain.AutomationRuleVersion) { v.ConditionSemantics = "" },
		func(v *domain.AutomationRuleVersion) {
			v.Conditions = strings.Replace(v.Conditions, "9007199254740993", "9007199254740992", 1)
		},
		func(v *domain.AutomationRuleVersion) {
			v.Conditions = strings.Replace(v.Conditions, "812345671", "812345672", 1)
		},
		func(v *domain.AutomationRuleVersion) {
			v.Conditions = strings.Replace(v.Conditions, "contact_attribute", "conversation_attribute", 1)
		},
	} {
		in := compatibilityNumericFixture(base)
		mutate(in.Frozen)
		out := AutomationRuleCompatibilityView(in)
		require.True(t, compatibilityHas(out.Sources[0], "saved_frozen_definition_drift"))
	}
	in := compatibilityNumericFixture("[" + compatibilityNumericLeaf("PRIVATE-CUSTOM-KEY", "conversation_attribute", "1e3") + "]")
	in.Frozen.Conditions = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(in.Frozen.Conditions, "1e3", "1000.00"), "attribute_key", "key"), "filter_operator", "operator")
	out := AutomationRuleCompatibilityView(in)
	require.False(t, compatibilityHas(out.Sources[0], "saved_frozen_definition_drift"))
	in.Profile = domain.AutomationCompatibilityProfile{Assessed: true, Owners: map[string]bool{"CON": true, "CUS": true}}
	out = AutomationRuleCompatibilityView(in)
	require.True(t, compatibilityHas(out.Sources[0], "numeric_custom_catalog_type_value_not_assessed"))
	require.False(t, compatibilityHas(out.Sources[0], "conditional_dependency_missing"))
	in.Profile.Owners["CUS"] = false
	out = AutomationRuleCompatibilityView(in)
	require.True(t, compatibilityHas(out.Sources[0], "conditional_dependency_missing"))
}
