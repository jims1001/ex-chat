package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

func TestAUTExecutionLifecycleContracts(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	owner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q foundation.QueryEnvelope
		_ = json.NewDecoder(r.Body).Decode(&q)
		id, _ := uintFilter(q.Filter, "id")
		account := q.AccountID
		if id == 99 {
			account = 92
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id, "account_id": account, "object_version": 1}})
	}))
	defer owner.Close()
	cfg := businessConfig("AUT")
	cfg.Dependencies = map[string]DependencyConfig{"CON": {URL: owner.URL, Token: "test"}}
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("AUT")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	makeRule := func(actions string) domain.AutomationRule {
		rule := domain.AutomationRule{AccountID: 91, Name: "Test", EventName: "conversation_created", Conditions: "[]", Actions: actions, Active: true, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
		v := domain.AutomationRuleVersion{AccountID: 91, RuleID: rule.ID, Version: 1, EventName: rule.EventName, Conditions: rule.Conditions, Actions: rule.Actions, CreatedAt: now}
		if err := db.Create(&v).Error; err != nil {
			t.Fatal(err)
		}
		return rule
	}
	sequence := 0
	command := func(rule domain.AutomationRule, event string) foundation.CommandEnvelope {
		sequence++
		key := "aut-lifecycle-" + strconv.Itoa(sequence)
		return foundation.CommandEnvelope{CommandID: key, CommandType: "aut.execution.start.v1", IdempotencyKey: key, CorrelationID: key, CausationID: "test-trigger", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "rule", ID: strconv.Itoa(int(rule.ID))}, Input: map[string]any{"event_id": event, "event_name": rule.EventName, "conversation_id": 11, "target_version": 1}, RequestedAt: now, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "aut.execution.get.v1"}}
	}
	call := func(c foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), "/contracts/commands/"+c.CommandType, c, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			r.Header.Set(headerCaller, "API")
			r.Header.Set(headerCorrelationID, c.CorrelationID)
		})
	}
	check := func(c foundation.CommandEnvelope, code int, status string) uint {
		t.Helper()
		res := call(c)
		if res.Code != code || (status != "" && !strings.Contains(res.Body.String(), `"status":"`+status+`"`)) {
			t.Fatalf("%s: %d %s", c.CommandID, res.Code, res.Body.String())
		}
		var out struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out.Data.ID
	}
	empty := makeRule("[]")
	first := command(empty, "evt-1")
	id := check(first, 200, "succeeded")
	if check(first, 200, "succeeded") != id {
		t.Fatal("receipt replay changed execution")
	}
	replay := command(empty, "evt-1")
	if check(replay, 200, "succeeded") != id {
		t.Fatal("semantic replay changed execution")
	}
	check(command(empty, "evt-2"), 200, "loop_blocked")
	var count int64
	db.Model(&domain.AutomationRuleExecution{}).Count(&count)
	if count != 2 {
		t.Fatalf("executions=%d", count)
	}
	nonempty := makeRule(`[{"action_name":"send_webhook","secret":"must-not-leak"}]`)
	bad := command(nonempty, "evt-3")
	badID := check(bad, 200, "received")
	if ran, err := runtime.RunAutomationOnce(context.Background()); !ran || err != nil {
		t.Fatalf("validation ran=%v err=%v", ran, err)
	}
	var execution domain.AutomationRuleExecution
	db.First(&execution, badID)
	if execution.Error != "action_capability_executor_unavailable" || execution.RuleVersion != 1 || execution.CompletedAt == nil {
		t.Fatalf("execution=%+v", execution)
	}
	// Updating the mutable rule cannot alter the already frozen execution.
	db.Model(&domain.AutomationRule{}).Where("id = ?", nonempty.ID).Update("actions", "[]")
	v := domain.AutomationRuleVersion{AccountID: 91, RuleID: nonempty.ID, Version: 2, EventName: nonempty.EventName, Conditions: "[]", Actions: "[]", CreatedAt: now}
	db.Create(&v)
	bad.Input["rule_version"] = 1
	bad.CommandID = "old-replay"
	bad.IdempotencyKey = "old-replay"
	if check(bad, 200, "failed") != badID {
		t.Fatal("frozen replay changed")
	}
	stale := command(nonempty, "evt-new-old")
	stale.Input["rule_version"] = 1
	check(stale, 409, "")
	foreign := command(empty, "evt-foreign")
	foreign.AccountID = 92
	check(foreign, 404, "")
	wrongTarget := command(empty, "evt-wrong-target")
	wrongTarget.Input["conversation_id"] = 99
	check(wrongTarget, 404, "")
	wrongVersion := command(empty, "evt-stale-target")
	wrongVersion.Input["target_version"] = 2
	check(wrongVersion, 409, "")
	disabled := makeRule("[]")
	db.Model(&domain.AutomationRule{}).Where("id = ?", disabled.ID).Update("active", false)
	check(command(disabled, "evt-disabled"), 409, "")
	recursive := command(nonempty, "evt-recursive")
	recursive.Input["origin_execution_id"] = badID
	recursive.Input["automation_depth"] = 1
	check(recursive, 200, "loop_blocked")
	invalidDepth := command(nonempty, "evt-invalid-depth")
	invalidDepth.Input["automation_depth"] = -1
	check(invalidDepth, 400, "")
	foreignOrigin := domain.AutomationRuleExecution{AccountID: 92, RuleID: empty.ID, ConversationID: 11, EventName: empty.EventName, Status: "waiting", TriggeredAt: now, CreatedAt: now}
	db.Create(&foreignOrigin)
	foreignChain := command(empty, "evt-foreign-origin")
	foreignChain.Input["origin_execution_id"] = foreignOrigin.ID
	foreignChain.Input["automation_depth"] = 1
	check(foreignChain, 404, "")
	// Cancel only work that has not started; waiting or running needs a runner handshake.
	waiting := domain.AutomationRuleExecution{AccountID: 91, RuleID: empty.ID, RuleVersion: 1, ConversationID: 11, EventName: empty.EventName, Status: "received", TriggeredAt: now, CreatedAt: now}
	db.Create(&waiting)
	cancel := command(empty, "unused")
	cancel.CommandType = "aut.execution.cancel.v1"
	cancel.Target = foundation.ContractTarget{Type: "execution", ID: strconv.Itoa(int(waiting.ID))}
	cancel.Input = map[string]any{}
	check(cancel, 200, "cancelled")
	check(cancel, 200, "cancelled")
	cancel.CommandID = "cancel-again"
	cancel.IdempotencyKey = "cancel-again"
	check(cancel, 200, "cancelled")
	cancel.CommandID = "cancel-success"
	cancel.IdempotencyKey = "cancel-success"
	cancel.Target.ID = strconv.Itoa(int(id))
	check(cancel, 409, "")
	cancel.CommandID = "cancel-foreign"
	cancel.IdempotencyKey = "cancel-foreign"
	cancel.Target.ID = strconv.Itoa(int(foreignOrigin.ID))
	check(cancel, 404, "")

	// In-flight work cannot be reported as cancelled without its runner's acknowledgement.
	inflight := domain.AutomationRuleExecution{AccountID: 91, RuleID: empty.ID, ConversationID: 11, EventName: empty.EventName, Status: "running", TriggeredAt: now, CreatedAt: now}
	db.Create(&inflight)
	cancel.CommandID, cancel.IdempotencyKey, cancel.Target.ID = "cancel-running", "cancel-running", strconv.Itoa(int(inflight.ID))
	check(cancel, 409, "")
	// Depth guard uses persisted parent depth, not a client-controlled reset.
	deepParent := domain.AutomationRuleExecution{AccountID: 91, RuleID: empty.ID, ConversationID: 11, EventName: empty.EventName, Status: "succeeded", AutomationDepth: 2, TriggeredAt: now, CreatedAt: now}
	db.Create(&deepParent)
	depthRule := makeRule("[]")
	depthCommand := command(depthRule, "evt-depth")
	depthCommand.Input["origin_execution_id"], depthCommand.Input["automation_depth"] = deepParent.ID, 3
	check(depthCommand, 200, "loop_blocked")
	resetDepth := command(depthRule, "evt-reset-depth")
	resetDepth.Input["origin_execution_id"], resetDepth.Input["automation_depth"] = deepParent.ID, 0
	check(resetDepth, 400, "")
	conditionRule := makeRule("[]")
	db.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", conditionRule.ID).Update("conditions", `[{"attribute_key":"status","values":["open"]}]`)
	conditionID := check(command(conditionRule, "evt-condition"), 200, "no_match")
	var conditionExecution domain.AutomationRuleExecution
	db.First(&conditionExecution, conditionID)
	if conditionExecution.Error != "conditions_not_matched" {
		t.Fatal("condition with no operator unexpectedly matched")
	}
	messageRule := makeRule("[]")
	messageRule.EventName = "message_created"
	db.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", messageRule.ID).Update("event_name", messageRule.EventName)
	check(command(messageRule, "evt-missing-message"), 400, "")
	// An outbox failure must roll back both new execution and command receipt.
	rollbackRule := makeRule("[]")
	rollback := command(rollbackRule, "evt-rollback")
	if err := db.Callback().Create().Before("gorm:create").Register("test_aut_outbox_failure", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.Action == "event_outbox" {
			tx.AddError(errors.New("forced outbox failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	check(rollback, 503, "")
	db.Callback().Create().Remove("test_aut_outbox_failure")
	var rolledBack int64
	db.Model(&domain.AutomationRuleExecution{}).Where("rule_id = ?", rollbackRule.ID).Count(&rolledBack)
	if rolledBack != 0 {
		t.Fatal("execution survived failed outbox transaction")
	}
	db.Model(&domain.LocalChangeJournal{}).Where("command_id = ?", rollback.CommandID).Count(&rolledBack)
	if rolledBack != 0 {
		t.Fatal("receipt survived failed outbox transaction")
	}
	var events []domain.LocalChangeJournal
	db.Where("action = ?", "event_outbox").Find(&events)
	types := map[string]int{}
	for _, e := range events {
		var event foundation.EventEnvelope
		if err := json.Unmarshal([]byte(e.AfterState), &event); err != nil {
			t.Fatal(err)
		}
		types[event.EventType]++
		if strings.Contains(e.AfterState, "must-not-leak") {
			t.Fatal("secret leaked")
		}
	}
	if types["aut.automation.executed.v1"] != 3 || types["aut.action.failed.v1"] != 1 || types["aut.loop.blocked.v1"] != 3 {
		t.Fatalf("events=%v", types)
	}
}
