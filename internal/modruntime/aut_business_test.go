package modruntime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

func TestAUTQueriesUseOwnedRulesVersionsAndExecutionViews(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	now := time.Now().UTC()
	rule := domain.AutomationRule{AccountID: 91, Name: "Assign", EventName: "conversation_created", Conditions: `[{"type":"always"}]`, Actions: `[{"type":"assign_agent"}]`, Active: true, CreatedAt: now, UpdatedAt: now}
	foreignRule := domain.AutomationRule{AccountID: 92, Name: "Foreign rule", EventName: "conversation_created", Conditions: `[]`, Actions: `[]`, CreatedAt: now, UpdatedAt: now}
	for _, item := range []*domain.AutomationRule{&rule, &foreignRule} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, version := range []domain.AutomationRuleVersion{
		{AccountID: 91, RuleID: rule.ID, Version: 1, EventName: rule.EventName, Conditions: `[]`, Actions: `[{"type":"notify"}]`, CreatedAt: now.Add(-time.Hour)},
		{AccountID: 91, RuleID: rule.ID, Version: 2, EventName: rule.EventName, Conditions: rule.Conditions, Actions: rule.Actions, CreatedAt: now},
		{AccountID: 92, RuleID: foreignRule.ID, Version: 1, EventName: foreignRule.EventName, Conditions: `[]`, Actions: `[]`, CreatedAt: now},
	} {
		if err := db.Create(&version).Error; err != nil {
			t.Fatal(err)
		}
	}
	execution := domain.AutomationRuleExecution{AccountID: 91, RuleID: rule.ID, ConversationID: 11, EventName: rule.EventName, Status: "failed", ActionResults: `[{"secret":"private-action"}]`, Error: "private execution error", TriggeredAt: now, CreatedAt: now}
	foreignExecution := domain.AutomationRuleExecution{AccountID: 92, RuleID: foreignRule.ID, ConversationID: 12, EventName: foreignRule.EventName, Status: "success", TriggeredAt: now, CreatedAt: now}
	for _, item := range []*domain.AutomationRuleExecution{&execution, &foreignExecution} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	cfg := businessConfig("AUT")
	registry := NewContractRegistry("AUT")
	runtime := Runtime{Config: cfg, DB: db, Contracts: registry}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	call := func(kind string, account uint, filter map[string]any) *httptest.ResponseRecorder {
		query := foundation.QueryEnvelope{QueryType: kind, AccountID: account, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Filter: filter, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: now}
		return performContractRequest(runtime.Handler(), "/contracts/queries/"+kind, query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	if response := call("aut.rule.get.v1", 91, map[string]any{"id": rule.ID}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Assign"`) || !strings.Contains(response.Body.String(), `"conditions":[`) {
		t.Fatalf("rule=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.rule.get.v1", 91, map[string]any{"id": foreignRule.ID}); response.Code != http.StatusNotFound {
		t.Fatalf("foreign rule=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.rule_version.get.v1", 91, map[string]any{"rule_id": rule.ID}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":2`) {
		t.Fatalf("latest version=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.rule_version.get.v1", 91, map[string]any{"rule_id": rule.ID, "version": 1}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":1`) {
		t.Fatalf("frozen version=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.rule_version.get.v1", 91, map[string]any{"rule_id": foreignRule.ID}); response.Code != http.StatusNotFound {
		t.Fatalf("foreign version=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.execution.get.v1", 91, map[string]any{"id": execution.ID}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"failed"`) || strings.Contains(response.Body.String(), "private-action") || strings.Contains(response.Body.String(), "private execution error") {
		t.Fatalf("execution=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.execution.get.v1", 91, map[string]any{"id": foreignExecution.ID}); response.Code != http.StatusNotFound {
		t.Fatalf("foreign execution=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.rule_version.get.v1", 91, map[string]any{"rule_id": strconv.FormatUint(uint64(rule.ID), 10), "version": 99}); response.Code != http.StatusNotFound {
		t.Fatalf("missing version=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("aut.rule_version.get.v1", 91, map[string]any{"rule_id": rule.ID, "version": "invalid"}); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid version=%d body=%s", response.Code, response.Body.String())
	}
	for _, foreign := range []any{&domain.Account{}, &domain.User{}, &domain.Conversation{}, &domain.Message{}, &domain.Contact{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("AUT runtime created foreign table %T", foreign)
		}
	}
}

func TestAUTRuleCommandsFreezeVersionsAndKeepAccountBoundary(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	cfg := businessConfig("AUT")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("AUT")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := foundation.CommandEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CausationID: "aut-rule-test", RequestedAt: now, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "aut.rule.get.v1"}}
	call := func(command foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), "/contracts/commands/"+command.CommandType, command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
	}
	create := base
	create.CommandID, create.CommandType, create.IdempotencyKey, create.CorrelationID = "aut-create", "aut.rule.create.v1", "aut-create-91", "aut-create-correlation"
	create.Target = foundation.ContractTarget{Type: "account", ID: "91"}
	create.Input = map[string]any{"name": "Original", "event_name": "conversation_created", "conditions": []any{}, "actions": []any{map[string]any{"action_name": "send_message"}}}
	var ruleID uint
	for range 2 {
		response := call(create)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Original"`) {
			t.Fatalf("create=%d body=%s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if ruleID == 0 {
			ruleID = envelope.Data.ID
		} else if ruleID != envelope.Data.ID {
			t.Fatalf("replay id=%d", envelope.Data.ID)
		}
	}
	manage := base
	manage.CommandID, manage.CommandType, manage.IdempotencyKey, manage.CorrelationID = "aut-manage", "aut.rule.manage.v1", "aut-manage-91", "aut-manage-correlation"
	manage.Target = foundation.ContractTarget{Type: "rule", ID: strconv.FormatUint(uint64(ruleID), 10)}
	manage.Input = map[string]any{"name": "Revised", "actions": []any{map[string]any{"action_name": "assign_agent"}}}
	for range 2 {
		response := call(manage)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"Revised"`) {
			t.Fatalf("manage=%d body=%s", response.Code, response.Body.String())
		}
	}
	nameOnly := manage
	nameOnly.CommandID, nameOnly.IdempotencyKey, nameOnly.CorrelationID = "aut-name", "aut-name-91", "aut-name-correlation"
	nameOnly.Input = map[string]any{"description": "New description"}
	if response := call(nameOnly); response.Code != http.StatusOK {
		t.Fatalf("name update=%d body=%s", response.Code, response.Body.String())
	}
	enable := manage
	enable.CommandID, enable.CommandType, enable.IdempotencyKey, enable.CorrelationID = "aut-enable", "aut.rule.enable.v1", "aut-enable-91", "aut-enable-correlation"
	enable.Input = map[string]any{"active": false}
	for range 2 {
		response := call(enable)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"active":false`) {
			t.Fatalf("enable=%d body=%s", response.Code, response.Body.String())
		}
	}
	foreign := domain.AutomationRule{AccountID: 92, Name: "Foreign", EventName: "message_created", Conditions: `[]`, Actions: `[]`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	bad := manage
	bad.CommandID, bad.IdempotencyKey, bad.CorrelationID = "aut-foreign", "aut-foreign-91", "aut-foreign-correlation"
	bad.Target.ID = strconv.FormatUint(uint64(foreign.ID), 10)
	if response := call(bad); response.Code != http.StatusNotFound {
		t.Fatalf("foreign=%d body=%s", response.Code, response.Body.String())
	}
	var versions []domain.AutomationRuleVersion
	if err := db.Where("account_id = ? AND rule_id = ?", 91, ruleID).Order("version ASC").Find(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 1 || versions[1].Version != 2 || !strings.Contains(versions[0].Actions, "send_message") || !strings.Contains(versions[1].Actions, "assign_agent") {
		t.Fatalf("versions=%+v", versions)
	}
	var rules, receipts, events int64
	_ = db.Model(&domain.AutomationRule{}).Where("account_id = ?", 91).Count(&rules).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if rules != 1 || receipts != 4 || events != 4 {
		t.Fatalf("rules=%d receipts=%d events=%d", rules, receipts, events)
	}
}
