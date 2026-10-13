package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
)

func numericLeafForTest(model, op, threshold string) string {
	return fmt.Sprintf(`[{"attribute_key":"score","custom_attribute_type":%q,"custom_attribute_definition_id":501,"filter_operator":%q,"values":[%s]}]`, model, op, threshold)
}
func numericObservationForTest(model, value string) *automationConditionContext {
	d := &automationCustomDefinitionView{ID: 501, AccountID: 91, AttributeKey: "score", AttributeModel: model, AttributeDisplayType: "number"}
	return &automationConditionContext{Conversation: conversationView{ID: 1, AccountID: 91, ContactID: 21, CustomAttributes: `{"score":` + value + `}`}, Contact: &contactView{ID: 21, AccountID: 91, CustomAttributes: `{"score":` + value + `}`}, contactLoaded: true, NumericCatalog: []automationNumericCatalogObservation{{DefinitionID: 501, Definition: d}}}
}
func TestAUTNumericStrictBoundariesAndLegacyLeaves(t *testing.T) {
	for _, model := range []string{"conversation_attribute", "contact_attribute"} {
		for _, tc := range []struct {
			value, threshold, op string
			matched              bool
			reason               string
		}{
			{"9007199254740993", "9007199254740992", "is_greater_than", true, ""}, {"9007199254740992", "9007199254740993", "is_less_than", true, ""}, {"1.000e0", "1", "is_greater_than", false, ""}, {"1", "1.0", "is_less_than", false, ""}, {"-1.01", "-1", "is_less_than", true, ""}, {"0.1000000000000000000000000001", "0.1", "is_greater_than", true, ""}, {"1e1000", "9e999", "is_greater_than", true, ""}, {"null", "0", "is_greater_than", false, ""}, {`"12"`, "0", "is_greater_than", false, "invalid_frozen_definition"}, {"true", "0", "is_greater_than", false, "invalid_frozen_definition"}, {"[]", "0", "is_greater_than", false, "invalid_frozen_definition"}, {"{}", "0", "is_greater_than", false, "invalid_frozen_definition"}, {"1e1025", "0", "is_greater_than", false, "invalid_frozen_definition"},
		} {
			t.Run(model+"/"+tc.value+tc.op, func(t *testing.T) {
				o := numericObservationForTest(model, tc.value)
				raw := numericLeafForTest(model, tc.op, tc.threshold)
				got, reason := matchAutomationConditionsProfile(raw, domain.AutomationNumericCustomV1, o)
				require.Equal(t, tc.matched, got)
				require.Equal(t, tc.reason, reason)
			})
		}
	}
	raw := numericLeafForTest("contact_attribute", "is_greater_than", "1")
	o := numericObservationForTest("contact_attribute", "2")
	o.Conversation.CustomAttributes = `{"score":0}`
	got, reason := matchAutomationConditionsProfile(raw, domain.AutomationNumericCustomV1, o)
	require.True(t, got)
	require.Empty(t, reason)
	o.Contact.CustomAttributes = `{}`
	got, reason = matchAutomationConditionsProfile(raw, domain.AutomationNumericCustomV1, o)
	require.False(t, got)
	require.Empty(t, reason)
	o.Contact.CustomAttributes = `{"score":2}`
	require.False(t, matchAutomationCondition(automationCondition{AttributeKey: "score", FilterOperator: "is_greater_than", Values: []any{1}}, o))
	legacyOR := strings.TrimSuffix(raw, "]") + `,{"key":"status","operator":"is","value":"open"}]`
	legacyOR = strings.Replace(legacyOR, `"values":[1]`, `"values":[1],"query_operator":"OR"`, 1)
	o.Conversation.Status = "open"
	got, reason = matchAutomationConditionsProfile(legacyOR, "", o)
	require.True(t, got)
	require.Empty(t, reason)
	o.Contact.CustomAttributes = `{"score":"2"}`
	got, reason = matchAutomationConditionsProfile(legacyOR, domain.AutomationNumericCustomV1, o)
	require.False(t, got)
	require.Equal(t, "invalid_frozen_definition", reason, "OR must not swallow a typed validation failure")
	for _, mutate := range []func(*automationConditionContext){func(o *automationConditionContext) { o.NumericCatalog = nil }, func(o *automationConditionContext) { o.NumericCatalog[0].Definition = nil }, func(o *automationConditionContext) { o.NumericCatalog[0].Definition.AttributeDisplayType = "currency" }, func(o *automationConditionContext) {
		o.NumericCatalog[0].Definition.AttributeModel = "conversation_attribute"
	}, func(o *automationConditionContext) { o.NumericCatalog[0].Definition.AttributeKey = "renamed" }} {
		o = numericObservationForTest("contact_attribute", "2")
		mutate(o)
		got, reason = matchAutomationConditionsProfile(raw, domain.AutomationNumericCustomV1, o)
		require.False(t, got)
		require.NotEmpty(t, reason)
	}
}
func numericAdmissionFixture(t *testing.T, model string) *automationActionFixture {
	f := newAutomationActionFixture(t, false, siblingRecoveryUrgent)
	raw := numericLeafForTest(model, "is_greater_than", "9007199254740992")
	for _, m := range []any{&domain.AutomationRule{}, &domain.AutomationRuleVersion{}} {
		require.NoError(t, f.aut.DB.Model(m).Where("account_id = ?", 91).Updates(map[string]any{"condition_semantics": domain.AutomationNumericCustomV1, "conditions": raw}).Error)
	}
	require.NoError(t, f.owners["CUS"].DB.Create(&domain.CustomAttributeDefinition{ID: 501, AccountID: 91, AttributeKey: "score", AttributeModel: model, AttributeDisplayType: "number"}).Error)
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", `{"score":9007199254740993}`).Error)
	require.NoError(t, f.owners["CUS"].DB.Create(&domain.Contact{ID: 21, AccountID: 91, CustomAttributes: `{"score":9007199254740993}`}).Error)
	return f
}
func TestAUTNumericAdmissionOwnedCatalogAndIsolation(t *testing.T) {
	for _, model := range []string{"conversation_attribute", "contact_attribute"} {
		t.Run(model, func(t *testing.T) {
			f := numericAdmissionFixture(t, model)
			e := f.call(f.startCommand(), 200)
			require.Equal(t, "received", e.Status)
			require.Equal(t, domain.AutomationNumericCustomV1, f.execution(e.ID).RunnerConditionSemantics)
			require.Equal(t, "succeeded", f.drain(e.ID).Status)
			f.assertTarget("open", "urgent", 2)
		})
	}
	for _, test := range []string{"missing", "foreign", "type", "model", "key", "duplicate", "value_type"} {
		t.Run(test, func(t *testing.T) {
			f := numericAdmissionFixture(t, "conversation_attribute")
			db := f.owners["CUS"].DB
			switch test {
			case "missing":
				require.NoError(t, db.Delete(&domain.CustomAttributeDefinition{}, 501).Error)
			case "foreign":
				require.NoError(t, db.Model(&domain.CustomAttributeDefinition{}).Where("id = ?", 501).Update("account_id", 92).Error)
			case "type":
				require.NoError(t, db.Model(&domain.CustomAttributeDefinition{}).Where("id = ?", 501).Update("attribute_display_type", "text").Error)
			case "model":
				require.NoError(t, db.Model(&domain.CustomAttributeDefinition{}).Where("id = ?", 501).Update("attribute_model", "contact_attribute").Error)
			case "key":
				require.NoError(t, db.Model(&domain.CustomAttributeDefinition{}).Where("id = ?", 501).Update("attribute_key", "changed").Error)
			case "duplicate":
				require.NoError(t, db.Create(&domain.CustomAttributeDefinition{ID: 502, AccountID: 91, AttributeKey: "score", AttributeModel: "conversation_attribute", AttributeDisplayType: "number"}).Error)
			case "value_type":
				require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", `{"score":"9007199254740993"}`).Error)
			}
			e := f.call(f.startCommand(), 200)
			require.Equal(t, "failed", e.Status)
			require.Equal(t, "invalid_frozen_definition", f.execution(e.ID).Error)
			f.assertTarget("open", "medium", 1)
		})
	}
}
func TestAUTNumericAdmissionDependencyActorAndTenant(t *testing.T) {
	for _, mode := range []string{"outage", "wrong_tenant", "wrong_id", "user_actor"} {
		t.Run(mode, func(t *testing.T) {
			f := numericAdmissionFixture(t, "contact_attribute")
			dep := f.aut.Config.Dependencies["CUS"]
			next := dep.LocalHandler
			dep.LocalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "number_definition") {
					if mode == "outage" {
						w.WriteHeader(503)
						return
					}
					if mode == "wrong_tenant" || mode == "wrong_id" {
						view := automationCustomDefinitionView{ID: 501, AccountID: 91, AttributeKey: "score", AttributeModel: "contact_attribute", AttributeDisplayType: "number"}
						if mode == "wrong_tenant" {
							view.AccountID = 92
						} else {
							view.ID = 502
						}
						json.NewEncoder(w).Encode(map[string]any{"data": view})
						return
					}
				}
				next.ServeHTTP(w, r)
			})
			f.aut.Config.Dependencies["CUS"] = dep
			command := f.startCommand()
			if mode == "user_actor" {
				command.Actor = foundation.ContractActor{Type: "User", ID: "7", AuthSource: "session"}
			}
			_, err := f.aut.prepareAutomationExecution(context.Background(), command)
			require.Error(t, err)
			var count int64
			require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
