package service

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

const numericAdoptionConditions = ` [ {"attribute_key":"risk_score","custom_attribute_type":"conversation_attribute","custom_attribute_definition_id":17,"filter_operator":"is_greater_than","values":[9007199254740993.0000000000000001]} ] `

func TestAutomationNumericAdoptionPreservesExactVersions(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	s := NewAutomationService(db, nil, nil)
	rule := domain.AutomationRule{AccountID: 91, Name: "exact", EventName: "conversation_created", Conditions: numericAdoptionConditions, ConditionSemantics: domain.AutomationNumericCustomV1, Actions: `[{"action_name":"change_priority","action_params":"urgent"}]`}
	require.NoError(t, s.CreateRule(&rule), "the profile can be saved before durable mode is enabled")
	require.NoError(t, s.UpdateRule(&rule, map[string]any{"name": "renamed"}))
	require.Equal(t, domain.AutomationNumericCustomV1, rule.ConditionSemantics)
	var versions []domain.AutomationRuleVersion
	require.NoError(t, db.Where("rule_id = ?", rule.ID).Order("version").Find(&versions).Error)
	require.Len(t, versions, 2)
	for _, version := range versions {
		require.Equal(t, numericAdoptionConditions, version.Conditions)
		require.Equal(t, domain.AutomationNumericCustomV1, version.ConditionSemantics)
		require.Contains(t, version.Actions, `["urgent"]`)
	}
	require.NoError(t, s.UpdateRule(&rule, map[string]any{"conditions": `[{"attribute_key":"risk_score","custom_attribute_type":"conversation_attribute","custom_attribute_definition_id":17,"filter_operator":"is_less_than","values":[1.0000000000000000001]}]`}))
	var original domain.AutomationRuleVersion
	require.NoError(t, db.Where("rule_id = ? AND version = 1", rule.ID).First(&original).Error)
	require.Equal(t, numericAdoptionConditions, original.Conditions)
}

func TestAutomationNumericAdoptionIsExplicitAndAtomic(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	s := NewAutomationService(db, nil, nil)
	rule := domain.AutomationRule{AccountID: 91, Name: "legacy", EventName: "conversation_created", Conditions: numericAdoptionConditions, Actions: "[]"}
	require.NoError(t, s.CreateRule(&rule))
	require.NoError(t, s.UpdateRule(&rule, map[string]any{"name": "still legacy"}))
	require.Empty(t, rule.ConditionSemantics)
	var oldVersion domain.AutomationRuleVersion
	require.NoError(t, db.Where("rule_id = ? AND version = 1", rule.ID).First(&oldVersion).Error)
	require.Empty(t, oldVersion.ConditionSemantics)
	require.NotEqual(t, numericAdoptionConditions, oldVersion.Conditions)
	require.NoError(t, s.UpdateRule(&rule, map[string]any{"condition_semantics": domain.AutomationNumericCustomV1}))
	require.Equal(t, domain.AutomationNumericCustomV1, rule.ConditionSemantics)
	var adopted domain.AutomationRuleVersion
	require.NoError(t, db.Where("rule_id = ?", rule.ID).Order("version DESC").First(&adopted).Error)
	require.Equal(t, numericAdoptionConditions, adopted.Conditions)
	for _, updates := range []map[string]any{
		{"name": "must rollback", "condition_semantics": "unknown.v1"},
		{"name": "must rollback", "condition_semantics": true},
		{"name": "must rollback", "condition_semantics": nil},
		{"name": "must rollback", "conditions": `[{"attribute_key":"risk_score","filter_operator":"is_greater_than","values":[1]}]`},
		{"name": "must rollback", "conditions": nil},
	} {
		require.ErrorIs(t, s.UpdateRule(&rule, updates), ErrAutomationConditionSemanticsInvalid)
		var stored domain.AutomationRule
		require.NoError(t, db.First(&stored, rule.ID).Error)
		require.Equal(t, "still legacy", stored.Name)
		require.Equal(t, numericAdoptionConditions, stored.Conditions)
		require.Equal(t, domain.AutomationNumericCustomV1, stored.ConditionSemantics)
	}
	var count int64
	require.NoError(t, db.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", rule.ID).Count(&count).Error)
	require.EqualValues(t, 3, count)
	for _, profile := range []string{"unknown.v1", " numeric_custom.v1"} {
		invalid := rule
		invalid.ID = 0
		invalid.ConditionSemantics = profile
		require.ErrorIs(t, s.CreateRule(&invalid), ErrAutomationConditionSemanticsInvalid)
	}
}

func TestAutomationNumericAdoptionKeepsLegacyFreezeAndEvaluator(t *testing.T) {
	rule := domain.AutomationRule{Conditions: `[{"key":"status","operator":"is","value":"open"}]`, Actions: `[{"action_name":"change_priority","action_params":"urgent"}]`}
	version := previewAutomationRule(rule)
	require.Equal(t, `[{"attribute_key":"status","filter_operator":"is","values":["open"],"value":"open","key":"status","operator":"is","query_operator":""}]`, version.Conditions)
	require.Empty(t, version.ConditionSemantics)
	s := &AutomationService{}
	require.False(t, s.matchConditions(numericAdoptionConditions, &domain.Conversation{}, nil))
}

func TestAutomationNumericAdoptionRequiresDurableModeWhenActive(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	s := NewAutomationService(db, nil, nil)
	active := domain.AutomationRule{AccountID: 91, Name: "active numeric", EventName: "conversation_created", Conditions: numericAdoptionConditions, ConditionSemantics: domain.AutomationNumericCustomV1, Actions: "[]", Active: true}
	require.ErrorIs(t, s.CreateRule(&active), ErrAutomationConditionMode)
	require.Zero(t, active.ID)
	active.Active = false
	require.NoError(t, s.CreateRule(&active))
	require.ErrorIs(t, s.UpdateRule(&active, map[string]any{"active": true}), ErrAutomationConditionMode)
	require.False(t, active.Active)
	require.NoError(t, s.UpdateRule(&active, map[string]any{"description": "prepared offline"}))
	s.SetDurableExecution(true)
	require.NoError(t, s.UpdateRule(&active, map[string]any{"active": true}))
	s.SetDurableExecution(false)
	require.ErrorIs(t, s.UpdateRule(&active, map[string]any{"name": "unsafe edit"}), ErrAutomationConditionMode)
	require.ErrorIs(t, s.UpdateRule(&active, map[string]any{"conditions": numericAdoptionConditions}), ErrAutomationConditionMode)
	require.NoError(t, s.UpdateRule(&active, map[string]any{"active": false}))
	legacy := domain.AutomationRule{AccountID: 91, Name: "old active", EventName: "conversation_created", Conditions: numericAdoptionConditions, Actions: "[]", Active: true}
	require.NoError(t, s.CreateRule(&legacy))
	require.ErrorIs(t, s.UpdateRule(&legacy, map[string]any{"condition_semantics": domain.AutomationNumericCustomV1}), ErrAutomationConditionMode)
	require.Empty(t, legacy.ConditionSemantics)
	require.NoError(t, s.UpdateRule(&legacy, map[string]any{"condition_semantics": domain.AutomationNumericCustomV1, "active": false}))
	imported := domain.AutomationRule{AccountID: 91, Name: "imported active", EventName: "conversation_created", Conditions: numericAdoptionConditions, ConditionSemantics: domain.AutomationNumericCustomV1, Actions: "[]", Active: true}
	require.NoError(t, db.Create(&imported).Error)
	require.NoError(t, s.UpdateRule(&imported, map[string]any{"active": false}), "operators can explicitly deactivate an imported active numeric rule in legacy mode")
	require.False(t, imported.Active)
	require.Equal(t, domain.AutomationNumericCustomV1, imported.ConditionSemantics)
}

func TestAutomationNumericAdoptionGuardsLegacyORFallback(t *testing.T) {
	db, _, _ := compatibilityTestDB(t)
	require.NoError(t, db.AutoMigrate(&domain.AutomationRuleExecution{}))
	s := NewAutomationService(db, repository.NewConversationRepository(db), nil)
	conditions := `[{"attribute_key":"risk_score","custom_attribute_type":"conversation_attribute","custom_attribute_definition_id":17,"filter_operator":"is_greater_than","values":[1],"query_operator":"OR"},{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`
	conv := &domain.Conversation{ID: 11, AccountID: 91, Status: "open"}
	require.True(t, s.matchConditions(conditions, conv, nil), "ordinary legacy OR behavior stays unchanged")
	var legacyID uint
	for _, profile := range []string{"", domain.AutomationNumericCustomV1, "unknown.v1"} {
		restored := domain.AutomationRule{AccountID: 91, Name: "restored", EventName: "conversation_created", Conditions: conditions, ConditionSemantics: profile, Actions: `[{"action_name":"unknown_noop"}]`, Active: true}
		require.NoError(t, db.Create(&restored).Error)
		if profile == "" {
			legacyID = restored.ID
		}
	}
	s.HandleConversationCreated(conv)
	var executions []domain.AutomationRuleExecution
	require.NoError(t, db.Find(&executions).Error)
	require.Len(t, executions, 1, "adopted and unknown profiles must never enter the legacy engine")
	require.Equal(t, legacyID, executions[0].RuleID)
}
