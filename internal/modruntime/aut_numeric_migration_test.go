package modruntime

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAUTNumericProfileSchemaMigrationPreservesOldRules(t *testing.T) {
	testNumericProfileSchemaMigration(t, newModuleTestDB(t, "AUT"))
}
func TestPostgresAUTNumericProfileSchemaMigrationPreservesOldRules(t *testing.T) {
	testNumericProfileSchemaMigration(t, postgresModuleDB(t, "AUT"))
}
func testNumericProfileSchemaMigration(t *testing.T, db *gorm.DB) {
	raw := `[{"attribute_key":"score","filter_operator":"is_greater_than","values":[1],"query_operator":"OR"},{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`
	rule := domain.AutomationRule{AccountID: 91, Name: "old active rule", EventName: "conversation_created", Conditions: raw, Actions: "[]", Active: true}
	require.NoError(t, db.Create(&rule).Error)
	version := domain.AutomationRuleVersion{AccountID: 91, RuleID: rule.ID, Version: 1, EventName: rule.EventName, Conditions: raw, Actions: "[]"}
	require.NoError(t, db.Create(&version).Error)
	execution := domain.AutomationRuleExecution{AccountID: 91, RuleID: rule.ID, ConversationID: 1, Status: "success", ActionResults: "[]"}
	require.NoError(t, db.Create(&execution).Error)
	// Simulate the actual pre-profile schema using only this disposable owner DB.
	for _, m := range []any{&domain.AutomationRule{}, &domain.AutomationRuleVersion{}} {
		require.NoError(t, db.Migrator().DropColumn(m, "ConditionSemantics"))
	}
	require.NoError(t, db.Migrator().DropColumn(&domain.AutomationRuleExecution{}, "RunnerConditionSemantics"))
	require.NoError(t, database.AutoMigrateModule(db, "AUT"))
	require.False(t, db.Migrator().HasTable(&domain.Conversation{}), "owner migration must not follow foreign associations")
	var saved domain.AutomationRule
	require.NoError(t, db.First(&saved, rule.ID).Error)
	require.True(t, saved.Active)
	require.Empty(t, saved.ConditionSemantics)
	require.Equal(t, raw, saved.Conditions)
	var frozen domain.AutomationRuleVersion
	require.NoError(t, db.First(&frozen, version.ID).Error)
	require.Empty(t, frozen.ConditionSemantics)
	require.Equal(t, raw, frozen.Conditions)
	var old domain.AutomationRuleExecution
	require.NoError(t, db.First(&old, execution.ID).Error)
	require.Empty(t, old.RunnerConditionSemantics)
	require.Equal(t, "success", old.Status)
	require.Empty(t, old.RunnerEvaluation)
	observation := &automationConditionContext{Conversation: conversationView{Status: "open", CustomAttributes: `{"score":2}`}}
	matched, reason := matchAutomationConditionsProfile(frozen.Conditions, frozen.ConditionSemantics, observation)
	require.True(t, matched)
	require.Empty(t, reason)
	matched, reason = matchAutomationConditionsProfile(numericLeafForTest("conversation_attribute", "is_greater_than", "1"), frozen.ConditionSemantics, observation)
	require.False(t, matched)
	require.Empty(t, reason)
}
