package modruntime

import (
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
