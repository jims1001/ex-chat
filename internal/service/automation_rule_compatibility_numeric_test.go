package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
	"github.com/stretchr/testify/require"
)

func TestAutomationCompatibilityNumericSQLProjectionAndReadOnlyPreview(t *testing.T) {
	db, _, recorder := compatibilityTestDB(t)
	conditions := `[{"attribute_key":"PRIVATE-NUMERIC-KEY","filter_operator":"is_less_than","custom_attribute_type":"contact_attribute","custom_attribute_definition_id":812345671,"values":[9007199254740993]}]`
	profiles := []string{"", domain.AutomationNumericCustomV1, strings.Repeat("PRIVATE-PROFILE-", 200000)}
	for i, profile := range profiles {
		rule := compatibilityTestRule(uint(i+1), 91, true)
		rule.Conditions = conditions
		rule.Actions = "[]"
		rule.ConditionSemantics = profile
		require.NoError(t, db.Create(&rule).Error)
		require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: rule.ID, Version: 2, EventName: rule.EventName, Conditions: conditions, ConditionSemantics: profile, Actions: "[]"}).Error)
	}
	previewRule := compatibilityTestRule(4, 91, true)
	previewRule.Conditions = conditions
	previewRule.ConditionSemantics = domain.AutomationNumericCustomV1
	previewRule.Actions = "[]"
	require.NoError(t, db.Create(&previewRule).Error)
	require.NoError(t, db.Exec("PRAGMA query_only=ON").Error)
	recorder.reset()
	var inputs []domain.AutomationCompatibilityInput
	s := compatibilityTestService(db)
	s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
		inputs = append(inputs, in)
		return modruntime.AutomationRuleCompatibilityView(in)
	}
	page, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Equal(t, "per_rule_version.v1", page.ConditionPolicy)
	require.Equal(t, "phase17.inventory.v1", page.ImplementationRevision)
	for _, item := range []string{"custom_attribute_catalog", "custom_attribute_type", "custom_attribute_value"} {
		require.Contains(t, page.NotChecked, item)
	}
	inputs = compatibilityFinalInputs(inputs)
	require.Len(t, inputs, 4)
	for i, expected := range []string{"", domain.AutomationNumericCustomV1, "unknown"} {
		require.Equal(t, expected, inputs[i].Rule.ConditionSemantics)
		require.Equal(t, expected, inputs[i].Frozen.ConditionSemantics)
		for _, source := range page.Rules[i].Sources {
			require.Equal(t, expected, source.ConditionSemantics)
		}
	}
	require.NotNil(t, inputs[3].Preview)
	require.Equal(t, domain.AutomationNumericCustomV1, inputs[3].Preview.ConditionSemantics)
	require.Equal(t, conditions, inputs[3].Preview.Conditions)
	for _, source := range page.Rules[3].Sources {
		require.Equal(t, domain.AutomationNumericCustomV1, source.ConditionSemantics)
	}
	for _, query := range recorder.queries() {
		require.True(t, strings.HasPrefix(query, "SELECT"), query)
		require.True(t, strings.Contains(query, "automation_rules") || strings.Contains(query, "automation_rule_versions"), query)
		require.NotContains(t, query, "PRIVATE-")
	}
	require.Contains(t, strings.Join(recorder.queries(), "\n"), automationCompatibilitySemanticsProjection)
	var frozenCount int64
	require.NoError(t, db.Model(&domain.AutomationRuleVersion{}).Count(&frozenCount).Error)
	require.EqualValues(t, 3, frozenCount)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	for _, secret := range []string{"PRIVATE-", "812345671", "9007199254740993", "contact_attribute"} {
		require.NotContains(t, string(encoded), secret)
	}
}

func TestAutomationCompatibilityNumericSQLProfileDriftAndSummary(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	rule := compatibilityTestRule(1, 91, true)
	rule.Conditions = `[{"attribute_key":"PRIVATE-KEY","filter_operator":"is_greater_than","custom_attribute_type":"conversation_attribute","custom_attribute_definition_id":1,"values":[2]}]`
	rule.Actions = "[]"
	rule.ConditionSemantics = domain.AutomationNumericCustomV1
	require.NoError(t, db.Create(&rule).Error)
	require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 1, Version: 1, EventName: rule.EventName, Conditions: rule.Conditions, Actions: "[]"}).Error)
	s := compatibilityTestService(db)
	s.compatibilityProjector = modruntime.AutomationRuleCompatibilityView
	page, err := s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Equal(t, domain.AutomationNumericCustomV1, page.Rules[0].Sources[0].ConditionSemantics)
	require.Empty(t, page.Rules[0].Sources[1].ConditionSemantics)
	codes := []string{}
	for _, issue := range page.Rules[0].Sources[0].Issues {
		codes = append(codes, issue.Code)
	}
	require.Contains(t, codes, "saved_frozen_definition_drift")
	summary := automationCompatibilitySummary(page.Rules[0])
	require.Equal(t, domain.AutomationNumericCustomV1, summary.Sources[0].ConditionSemantics)
	require.Empty(t, summary.Sources[1].ConditionSemantics)
	require.False(t, summary.Complete)
}
