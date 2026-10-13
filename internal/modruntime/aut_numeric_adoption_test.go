package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
)

const autNumericAdoptionDefinition = ` [{"attribute_key":"risk_score","custom_attribute_type":"conversation_attribute","custom_attribute_definition_id":17,"filter_operator":"is_greater_than","values":[9007199254740993.0000000000000001]}] `

func TestAUTNumericAdoptionCommandsFreezeExactProfile(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	cfg := businessConfig("AUT")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("AUT")}
	require.NoError(t, runtime.RegisterBusinessContracts())
	seq := 0
	call := func(kind string, target foundation.ContractTarget, input map[string]any, want int) *httptest.ResponseRecorder {
		seq++
		key := fmt.Sprintf("numeric-adoption-%d", seq)
		command := foundation.CommandEnvelope{CommandID: key, CommandType: kind, IdempotencyKey: key, CorrelationID: key, AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: target, Input: input, CausationID: key, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "aut.rule.get.v1"}}
		response := performContractRequest(runtime.Handler(), "/contracts/commands/"+kind, command, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			r.Header.Set(headerCaller, "API")
			r.Header.Set(headerCorrelationID, key)
		})
		require.Equal(t, want, response.Code, response.Body.String())
		return response
	}
	account := foundation.ContractTarget{Type: "account", ID: "91"}
	input := map[string]any{"name": "exact", "event_name": "conversation_created", "conditions": autNumericAdoptionDefinition, "actions": []any{}, "condition_semantics": domain.AutomationNumericCustomV1}
	response := call("aut.rule.create.v1", account, input, http.StatusOK)
	require.Contains(t, response.Body.String(), `"condition_semantics":"numeric_custom.v1"`)
	var rule domain.AutomationRule
	require.NoError(t, db.First(&rule).Error)
	require.Equal(t, autNumericAdoptionDefinition, rule.Conditions)
	target := foundation.ContractTarget{Type: "rule", ID: strconv.FormatUint(uint64(rule.ID), 10)}
	call("aut.rule.manage.v1", target, map[string]any{"name": "renamed"}, http.StatusOK)
	updated := strings.Replace(autNumericAdoptionDefinition, "9007199254740993.0000000000000001", "1.0000000000000000001", 1)
	call("aut.rule.manage.v1", target, map[string]any{"conditions": updated}, http.StatusOK)
	var versions []domain.AutomationRuleVersion
	require.NoError(t, db.Where("rule_id = ?", rule.ID).Order("version").Find(&versions).Error)
	require.Len(t, versions, 2)
	require.Equal(t, autNumericAdoptionDefinition, versions[0].Conditions)
	require.Equal(t, updated, versions[1].Conditions)
	for _, version := range versions {
		require.Equal(t, domain.AutomationNumericCustomV1, version.ConditionSemantics)
	}
	for _, profile := range []any{nil, true, json.Number("17"), []any{}, map[string]any{}, "unknown.v1"} {
		bad := map[string]any{"name": "must not commit", "condition_semantics": profile}
		call("aut.rule.manage.v1", target, bad, http.StatusBadRequest)
		bad["event_name"] = "conversation_created"
		bad["conditions"] = autNumericAdoptionDefinition
		call("aut.rule.create.v1", account, bad, http.StatusBadRequest)
	}
	var numericArray any
	require.NoError(t, json.Unmarshal([]byte(autNumericAdoptionDefinition), &numericArray))
	call("aut.rule.manage.v1", target, map[string]any{"conditions": numericArray}, http.StatusBadRequest)
	call("aut.rule.manage.v1", target, map[string]any{"conditions": `[{"attribute_key":"risk_score","filter_operator":"is_greater_than","values":[1]}]`}, http.StatusBadRequest)
	require.NoError(t, db.First(&rule, rule.ID).Error)
	require.Equal(t, "renamed", rule.Name)
	require.Equal(t, updated, rule.Conditions)
	var versionCount int64
	require.NoError(t, db.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", rule.ID).Count(&versionCount).Error)
	require.EqualValues(t, 2, versionCount)
	// Profile-only adoption creates a new immutable version; ordinary edits do not adopt.
	legacy := domain.AutomationRule{AccountID: 91, Name: "legacy", EventName: "conversation_created", Conditions: autNumericAdoptionDefinition, Actions: "[]"}
	require.NoError(t, db.Create(&legacy).Error)
	target.ID = strconv.FormatUint(uint64(legacy.ID), 10)
	call("aut.rule.manage.v1", target, map[string]any{"description": "still legacy"}, http.StatusOK)
	require.NoError(t, db.First(&legacy, legacy.ID).Error)
	require.Empty(t, legacy.ConditionSemantics)
	call("aut.rule.manage.v1", target, map[string]any{"condition_semantics": domain.AutomationNumericCustomV1}, http.StatusOK)
	var adopted domain.AutomationRuleVersion
	require.NoError(t, db.Where("rule_id = ?", legacy.ID).First(&adopted).Error)
	require.Equal(t, domain.AutomationNumericCustomV1, adopted.ConditionSemantics)
	require.Equal(t, autNumericAdoptionDefinition, adopted.Conditions)
	query := foundation.QueryEnvelope{AccountID: 91, Filter: map[string]any{"rule_id": legacy.ID}}
	view, err := runtime.Contracts.queries["aut.rule_version.get.v1"](context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, domain.AutomationNumericCustomV1, view.(map[string]any)["condition_semantics"])
}

func TestAUTNumericAdoptionInProcessExactInputs(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	runtime := Runtime{Config: businessConfig("AUT"), DB: db, Contracts: NewContractRegistry("AUT")}
	require.NoError(t, runtime.RegisterBusinessContracts())
	var exact any
	decoder := json.NewDecoder(strings.NewReader(autNumericAdoptionDefinition))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&exact))
	for i, conditions := range []any{autNumericAdoptionDefinition, json.RawMessage(autNumericAdoptionDefinition), exact} {
		command := foundation.CommandEnvelope{CommandID: fmt.Sprintf("direct-%d", i), CommandType: "aut.rule.create.v1", AccountID: 91, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"name": "direct exact", "event_name": "conversation_created", "conditions": conditions, "condition_semantics": domain.AutomationNumericCustomV1}, CorrelationID: "direct-exact", CausationID: "direct-exact", Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, RequestedAt: time.Now().UTC()}
		_, err := runtime.Contracts.commands[command.CommandType](context.Background(), command)
		require.NoError(t, err)
	}
	var versions []domain.AutomationRuleVersion
	require.NoError(t, db.Find(&versions).Error)
	require.Len(t, versions, 3)
	for _, v := range versions {
		require.Contains(t, v.Conditions, "9007199254740993.0000000000000001")
		require.Equal(t, domain.AutomationNumericCustomV1, v.ConditionSemantics)
	}
}
