package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/modruntime"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Exercise the ordinary monolith API with durable mode selected. The router has
// no BackgroundContext so each durable transition is driven deterministically.
// No module-server listener, external provider, or legacy service callback is used
// to complete any automation action in these tests.
type durableAutomationAPIFixture struct {
	t      *testing.T
	db     *gorm.DB
	router http.Handler
	con    *repository.ConversationRepository
	msg    *repository.MessageRepository
	bridge *modruntime.AutomationBridge
}

type durableAutomationAPITenant struct {
	id        uint
	token     string
	inboxID   uint
	contactID uint
}

func newDurableAutomationAPIFixture(t *testing.T) *durableAutomationAPIFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Environment:             "test",
		DBDriver:                "sqlite",
		DBPath:                  ":memory:",
		JWTSecret:               "durable-automation-api-test-secret-2026",
		JWTExpirationHours:      24,
		AutomationExecutionMode: "durable",
	}
	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("initialize durable API database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	f := &durableAutomationAPIFixture{t: t, db: db, router: router.SetupRouter(cfg, db, ws.NewHub())}
	f.con = repository.NewConversationRepository(db)
	f.con.SetAutomationCapture(true)
	f.msg = repository.NewMessageRepositoryWithConversationPort(db, f.con)
	f.msg.SetAutomationCapture(true)
	f.restartBridge()
	return f
}

func (f *durableAutomationAPIFixture) restartBridge() {
	f.t.Helper()
	var err error
	f.bridge, err = modruntime.NewMonolithAutomation(f.db, repository.NewCreationWorkflowCoordinator(f.con, f.msg), f.con, f.msg)
	if err != nil {
		f.t.Fatalf("compose durable bridge: %v", err)
	}
}

func (f *durableAutomationAPIFixture) request(token, method, path string, payload any, wantStatus int, out any) *httptest.ResponseRecorder {
	f.t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp := httptest.NewRecorder()
	f.router.ServeHTTP(resp, req)
	if resp.Code != wantStatus {
		f.t.Fatalf("%s %s: status=%d, want=%d, body=%s", method, path, resp.Code, wantStatus, resp.Body.String())
	}
	if out != nil {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
			f.t.Fatalf("decode response: %v", err)
		}
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			f.t.Fatalf("decode response data: %v, body=%s", err, resp.Body.String())
		}
	}
	return resp
}

func (f *durableAutomationAPIFixture) tenant(name string) durableAutomationAPITenant {
	f.t.Helper()
	var auth struct {
		Token    string           `json:"token"`
		Accounts []domain.Account `json:"accounts"`
	}
	f.request("", http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Durable " + name, "name": "Admin " + name,
		"email": name + "@durable-automation.example", "password": "Password123!",
	}, http.StatusCreated, &auth)
	if auth.Token == "" || len(auth.Accounts) != 1 || auth.Accounts[0].ID == 0 {
		f.t.Fatalf("signup returned invalid authentication: %+v", auth)
	}
	a := durableAutomationAPITenant{id: auth.Accounts[0].ID, token: auth.Token}
	var inbox domain.Inbox
	f.request(a.token, http.MethodPost, a.path("/inboxes"), map[string]any{
		"name": "Durable API Inbox", "channel_type": "api",
	}, http.StatusCreated, &inbox)
	a.inboxID = inbox.ID
	var contact domain.Contact
	f.request(a.token, http.MethodPost, a.path("/contacts"), map[string]any{
		"name": "Customer " + name, "email": name + "-customer@durable-automation.example",
	}, http.StatusCreated, &contact)
	a.contactID = contact.ID
	if a.inboxID == 0 || a.contactID == 0 {
		f.t.Fatalf("API tenant fixture lacks inbox or contact: %+v", a)
	}
	return a
}

func (a durableAutomationAPITenant) path(suffix string) string {
	return fmt.Sprintf("/api/v1/accounts/%d%s", a.id, suffix)
}

func durableAutomationAction(name, value string) map[string]any {
	return map[string]any{"action_name": name, "action_params": []string{value}}
}

func (f *durableAutomationAPIFixture) rule(a durableAutomationAPITenant, event string, conditions, actions any) domain.AutomationRule {
	f.t.Helper()
	var rule domain.AutomationRule
	f.request(a.token, http.MethodPost, a.path("/automation_rules"), map[string]any{
		"name": "Durable " + event, "event_name": event,
		"conditions": conditions, "actions": actions, "active": true,
	}, http.StatusCreated, &rule)
	if rule.ID == 0 || rule.AccountID != a.id {
		f.t.Fatalf("invalid created rule: %+v", rule)
	}
	return rule
}

func (f *durableAutomationAPIFixture) conversation(a durableAutomationAPITenant) domain.Conversation {
	f.t.Helper()
	var conv domain.Conversation
	f.request(a.token, http.MethodPost, a.path("/conversations"), map[string]any{
		"inbox_id": a.inboxID, "contact_id": a.contactID, "priority": "low",
	}, http.StatusCreated, &conv)
	if conv.ID == 0 || conv.AccountID != a.id {
		f.t.Fatalf("invalid created conversation: %+v", conv)
	}
	return conv
}

func (f *durableAutomationAPIFixture) state(a durableAutomationAPITenant, id uint, status, priority string) domain.Conversation {
	f.t.Helper()
	var conv domain.Conversation
	f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/conversations/%d", id)), nil, http.StatusOK, &conv)
	if conv.Status != status || conv.Priority != priority {
		f.t.Fatalf("conversation %d: status=%q priority=%q, want status=%q priority=%q", id, conv.Status, conv.Priority, status, priority)
	}
	return conv
}

func (f *durableAutomationAPIFixture) executions(a durableAutomationAPITenant, ruleID uint) []domain.AutomationRuleExecution {
	f.t.Helper()
	var executions []domain.AutomationRuleExecution
	f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/automation_rules/executions?rule_id=%d", ruleID)), nil, http.StatusOK, &executions)
	return executions
}

func (f *durableAutomationAPIFixture) execution(a durableAutomationAPITenant, ruleID uint, wantStatus, wantError string) domain.AutomationRuleExecution {
	f.t.Helper()
	executions := f.executions(a, ruleID)
	if len(executions) != 1 {
		f.t.Fatalf("rule %d has %d executions, want exactly one: %+v", ruleID, len(executions), executions)
	}
	e := executions[0]
	if e.Status != wantStatus || e.Error != wantError || e.RuleVersion == 0 || e.EventID == "" || e.TargetVersion == 0 {
		f.t.Fatalf("invalid durable execution: %+v; want status=%s error=%s", e, wantStatus, wantError)
	}
	if wantStatus != "received" && wantStatus != "running" && wantStatus != "waiting" && wantStatus != "evaluating" && e.CompletedAt == nil {
		f.t.Fatalf("terminal durable execution lacks completion timestamp: %+v", e)
	}
	return e
}

func (f *durableAutomationAPIFixture) versions(a durableAutomationAPITenant, ruleID uint) []domain.AutomationRuleVersion {
	f.t.Helper()
	var versions []domain.AutomationRuleVersion
	if err := f.db.Where("account_id = ? AND rule_id = ?", a.id, ruleID).Order("version ASC").Find(&versions).Error; err != nil {
		f.t.Fatal(err)
	}
	return versions
}

func (f *durableAutomationAPIFixture) flush() int {
	f.t.Helper()
	total := 0
	// Owner actions can publish additional CON facts. Drain those as well so
	// assertions cover the resulting causal chain, including the loop guard.
	for pass := 0; pass < 20; pass++ {
		count, err := f.bridge.RunOnce(context.Background())
		if err != nil {
			f.t.Fatalf("drain durable automation: %v", err)
		}
		total += count
		if count == 0 {
			return total
		}
	}
	f.t.Fatal("durable automation did not quiesce within 20 bounded passes")
	return total
}

func (f *durableAutomationAPIFixture) fact(source modruntime.AutomationFactSource, eventName string, conversationID uint) foundation.EventEnvelope {
	f.t.Helper()
	events, err := source.PendingAutomationFacts(context.Background(), 100)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, event := range events {
		if event.Payload["event_name"] == eventName && fmt.Sprint(event.Payload["conversation_id"]) == fmt.Sprint(conversationID) {
			if err := event.Validate(); err != nil {
				f.t.Fatalf("invalid committed source fact: %v", err)
			}
			return event
		}
	}
	f.t.Fatalf("missing committed %s fact for conversation %d: %+v", eventName, conversationID, events)
	return foundation.EventEnvelope{}
}

func (f *durableAutomationAPIFixture) admit(event foundation.EventEnvelope) {
	f.t.Helper()
	if err := f.bridge.Runtime.AcceptAutomationFact(context.Background(), event); err != nil {
		f.t.Fatalf("admit committed source fact: %v", err)
	}
}

func TestAutomationDurableBridgeAPIConversationCreated(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("created")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("change_status", "pending"), durableAutomationAction("change_priority", "urgent"),
	})
	versions := f.versions(a, rule.ID)
	if len(versions) != 1 || versions[0].Version != 1 || versions[0].Conditions != "[]" || versions[0].EventName != "conversation_created" || !json.Valid([]byte(versions[0].Actions)) {
		t.Fatalf("API did not persist initial frozen rule version: %+v", versions)
	}
	conv := f.conversation(a)
	before := f.state(a, conv.ID, "open", "low")
	if executions := f.executions(a, rule.ID); len(executions) != 0 {
		t.Fatalf("legacy callback ran while durable mode selected: %+v", executions)
	}
	event := f.fact(f.con, "conversation_created", conv.ID)
	if event.AccountID != a.id || event.SourceModule != "CON" {
		t.Fatalf("incorrect conversation source fact: %+v", event)
	}
	if f.flush() == 0 {
		t.Fatal("ordinary conversation request produced no durable work")
	}
	after := f.state(a, conv.ID, "pending", "urgent")
	e := f.execution(a, rule.ID, "succeeded", "")
	if e.RuleVersion != 1 || e.EventID != event.EventID || e.ConversationID != conv.ID || e.MessageID != nil || e.TargetVersion != uint(before.ObjectVersion) || after.ObjectVersion != before.ObjectVersion+2 {
		t.Fatalf("durable owner actions or target version are incorrect: before=%+v after=%+v execution=%+v", before, after, e)
	}
	var results []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(e.ActionResults), &results); err != nil || len(results) != 2 || results[0].Status != "succeeded" || results[1].Status != "succeeded" {
		t.Fatalf("durable action results not persisted: %s", e.ActionResults)
	}
	// Recreate all runtime state and redeliver the same committed fact. Exactly-once
	// admission and stable owner command receipts must survive runtime recreation.
	f.restartBridge()
	f.admit(event)
	f.admit(event)
	if got := f.flush(); got != 0 {
		t.Fatalf("replayed completed fact created %d new work items", got)
	}
	replayed := f.state(a, conv.ID, "pending", "urgent")
	if replayed.ObjectVersion != after.ObjectVersion || f.execution(a, rule.ID, "succeeded", "").ID != e.ID {
		t.Fatal("replay duplicated a durable execution or owner mutation")
	}
	if pending, err := f.con.PendingAutomationFacts(context.Background(), 100); err != nil || len(pending) != 0 {
		t.Fatalf("conversation source fact was not acknowledged: pending=%+v err=%v", pending, err)
	}
}

func TestAutomationDurableBridgeAPIMessageCreated(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("message")
	conv := f.conversation(a)
	f.flush()
	rule := f.rule(a, "message_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	var msg domain.Message
	f.request(a.token, http.MethodPost, a.path(fmt.Sprintf("/conversations/%d/messages", conv.ID)), map[string]any{
		"content": "Ordinary API message triggers durable work", "private": true,
		"creation_workflow_id": "untrusted-request-field", "suppress_automation": true,
	}, http.StatusCreated, &msg)
	before := f.state(a, conv.ID, "open", "low")
	if len(f.executions(a, rule.ID)) != 0 {
		t.Fatal("message callback executed legacy automation")
	}
	event := f.fact(f.msg, "message_created", conv.ID)
	if event.SourceModule != "MSG" || event.AccountID != a.id || fmt.Sprint(event.Payload["message_id"]) != fmt.Sprint(msg.ID) {
		t.Fatalf("incorrect committed message fact: %+v", event)
	}
	f.flush()
	after := f.state(a, conv.ID, "open", "high")
	e := f.execution(a, rule.ID, "succeeded", "")
	if e.MessageID == nil || *e.MessageID != msg.ID || e.EventID != event.EventID || e.TargetVersion != uint(before.ObjectVersion) || after.ObjectVersion != before.ObjectVersion+1 {
		t.Fatalf("message execution did not resolve the committed owner target: %+v", e)
	}
	f.restartBridge()
	f.admit(event)
	if f.flush() != 0 || f.execution(a, rule.ID, "succeeded", "").ID != e.ID {
		t.Fatal("message redelivery duplicated an execution")
	}
	var count int64
	if err := f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("message trigger created duplicate/provider messages: count=%d err=%v", count, err)
	}
	if pending, err := f.msg.PendingAutomationFacts(context.Background(), 100); err != nil || len(pending) != 0 {
		t.Fatalf("message source fact was not acknowledged: pending=%+v err=%v", pending, err)
	}
}

func TestAutomationDurableBridgeAPIConversationUpdated(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("updated")
	conv := f.conversation(a)
	f.flush()
	rule := f.rule(a, "conversation_updated", []any{}, []any{durableAutomationAction("change_status", "pending")})
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/conversations/%d", conv.ID)), map[string]any{"priority": "high"}, http.StatusOK, nil)
	before := f.state(a, conv.ID, "open", "high")
	if len(f.executions(a, rule.ID)) != 0 {
		t.Fatal("conversation update callback executed legacy automation")
	}
	event := f.fact(f.con, "conversation_updated", conv.ID)
	f.flush()
	after := f.state(a, conv.ID, "pending", "high")
	executions := f.executions(a, rule.ID)
	if len(executions) != 2 || after.ObjectVersion != before.ObjectVersion+1 {
		t.Fatalf("expected one owner action and one suppressed causal loop: %+v", executions)
	}
	var original, blocked domain.AutomationRuleExecution
	for _, execution := range executions {
		if execution.EventID == event.EventID {
			original = execution
		} else {
			blocked = execution
		}
	}
	if original.ID == 0 || original.Status != "succeeded" || original.Error != "" || blocked.Status != "loop_blocked" || blocked.Error != "automation_loop_guard" || blocked.OriginExecutionID == nil || *blocked.OriginExecutionID != original.ID || blocked.AutomationDepth != 1 {
		t.Fatalf("conversation update did not preserve causal loop suppression: original=%+v blocked=%+v", original, blocked)
	}
	f.admit(event)
	if f.flush() != 0 || len(f.executions(a, rule.ID)) != 2 {
		t.Fatal("conversation update replay duplicated work")
	}
}

func TestAutomationDurableBridgeAPIUnsupportedActionsFailWithoutSuppressingLocalEffects(t *testing.T) {
	cases := []struct {
		name       string
		conditions any
		actions    []any
		reason     string
	}{
		{
			name: "action", conditions: []any{},
			// Unsupported work fails explicitly while the supported source action remains eligible.
			actions: []any{durableAutomationAction("change_priority", "urgent"), durableAutomationAction("send_email_to_team", "must never be sent")},
			reason:  "action_capability_executor_unavailable",
		},
		{
			name: "capability-version", conditions: []any{},
			actions: []any{map[string]any{
				"action_name": "change_priority", "action_params": []string{"urgent"}, "capability_version": "v2",
			}},
			reason: "invalid_action_parameters",
		},
		{
			name: "mixed-capability", conditions: []any{},
			actions: []any{map[string]any{
				"action_name": "change_priority", "action_params": []string{"urgent"}, "capability_id": "conversation.change_priority",
			}},
			reason: "invalid_action_parameters",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDurableAutomationAPIFixture(t)
			a := f.tenant("unsupported-" + tc.name)
			rule := f.rule(a, "conversation_created", tc.conditions, tc.actions)
			conv := f.conversation(a)
			before := f.state(a, conv.ID, "open", "low")
			if len(f.executions(a, rule.ID)) != 0 {
				t.Fatal("unsupported durable rule fell back to the legacy executor")
			}
			f.flush()
			priority, status, increment := "low", "failed", 0
			if tc.name == "action" {
				priority, status, increment = "urgent", "partially_succeeded", 1
			}
			after := f.state(a, conv.ID, "open", priority)
			f.execution(a, rule.ID, status, tc.reason)
			if after.ObjectVersion != before.ObjectVersion+increment {
				t.Fatal("unexpected supported action version effect")
			}
			var count int64
			if err := f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("unsupported definition emitted a legacy message: count=%d err=%v", count, err)
			}
			if f.flush() != 0 {
				t.Fatal("failed definition was repeatedly dispatched")
			}
		})
	}
}

func TestAutomationDurableBridgeAPIFrozenRuleAfterAdmission(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("frozen")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	original := f.versions(a, rule.ID)
	conv := f.conversation(a)
	event := f.fact(f.con, "conversation_created", conv.ID)
	f.admit(event)
	queued := f.execution(a, rule.ID, "received", "")
	f.state(a, conv.ID, "open", "low")
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), map[string]any{
		"actions": []any{durableAutomationAction("change_priority", "urgent")},
	}, http.StatusOK, nil)
	versions := f.versions(a, rule.ID)
	if len(original) != 1 || len(versions) != 2 || versions[0] != original[0] || versions[1].Version != 2 || versions[1].Actions == versions[0].Actions {
		t.Fatalf("API update mutated the frozen definition or failed to append a version: before=%+v after=%+v", original, versions)
	}
	f.restartBridge()
	f.flush()
	f.state(a, conv.ID, "open", "high")
	executed := f.execution(a, rule.ID, "succeeded", "")
	if executed.ID != queued.ID || executed.RuleVersion != 1 {
		t.Fatalf("queued execution switched to edited rule: %+v", executed)
	}
	newConv := f.conversation(a)
	f.flush()
	f.state(a, newConv.ID, "open", "urgent")
	executions := f.executions(a, rule.ID)
	if len(executions) != 2 {
		t.Fatalf("expected one execution for each committed conversation, got %+v", executions)
	}
	foundNew := false
	for _, e := range executions {
		if e.ConversationID == newConv.ID {
			foundNew = true
			if e.RuleVersion != 2 || e.Status != "succeeded" {
				t.Fatalf("new trigger did not use new frozen rule version: %+v", e)
			}
		}
	}
	if !foundNew {
		t.Fatal("new conversation has no execution for the edited rule")
	}
}

func TestAutomationDurableBridgeAPIRuleChangedBeforeAdmission(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("changed")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	conv := f.conversation(a)
	event := f.fact(f.con, "conversation_created", conv.ID)
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), map[string]any{
		"actions": []any{durableAutomationAction("change_priority", "urgent")},
	}, http.StatusOK, nil)
	f.flush()
	f.state(a, conv.ID, "open", "low")
	e := f.execution(a, rule.ID, "failed", "rule_changed_before_dispatch")
	if e.RuleVersion != 1 || e.EventID != event.EventID {
		t.Fatalf("late admission silently selected a new rule version: %+v", e)
	}
	f.admit(event)
	if f.flush() != 0 || f.execution(a, rule.ID, "failed", "rule_changed_before_dispatch").ID != e.ID {
		t.Fatal("late fact replay changed its terminal outcome")
	}
}

func TestAutomationDurableBridgeAPIOwnerVersionConflict(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("conflict")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("change_priority", "high"), durableAutomationAction("change_status", "resolved"),
	})
	conv := f.conversation(a)
	f.admit(f.fact(f.con, "conversation_created", conv.ID))
	queued := f.execution(a, rule.ID, "received", "")
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/conversations/%d", conv.ID)), map[string]any{"priority": "urgent"}, http.StatusOK, nil)
	manual := f.state(a, conv.ID, "open", "urgent")
	if manual.ObjectVersion <= int(queued.TargetVersion) {
		t.Fatal("manual API mutation did not advance owner version")
	}
	f.flush()
	e := f.execution(a, rule.ID, "failed", "owner_command_rejected")
	after := f.state(a, conv.ID, "open", "urgent")
	if after.ObjectVersion != manual.ObjectVersion {
		t.Fatal("stale durable action overwrote the newer owner mutation")
	}
	var results []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(e.ActionResults), &results); err != nil || len(results) != 2 || results[0].Status != "failed" || results[1].Status != "failed" {
		t.Fatalf("continued actions did not independently reject the same stale owner target: %s", e.ActionResults)
	}
}

func TestAutomationDurableBridgeAPITenantIsolation(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a, b := f.tenant("tenant-a"), f.tenant("tenant-b")
	ruleA := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	ruleB := f.rule(b, "conversation_created", []any{}, []any{durableAutomationAction("change_status", "resolved")})
	convA := f.conversation(a)
	f.flush()
	f.state(a, convA.ID, "open", "high")
	e := f.execution(a, ruleA.ID, "succeeded", "")
	if len(f.executions(b, ruleB.ID)) != 0 || len(f.executions(b, ruleA.ID)) != 0 {
		t.Fatal("tenant A's fact or execution was visible to tenant B")
	}
	f.request(b.token, http.MethodGet, b.path(fmt.Sprintf("/automation_rules/%d", ruleA.ID)), nil, http.StatusNotFound, nil)
	f.request(b.token, http.MethodPut, b.path(fmt.Sprintf("/automation_rules/%d", ruleA.ID)), map[string]any{"active": false}, http.StatusNotFound, nil)
	f.request(b.token, http.MethodGet, b.path(fmt.Sprintf("/automation_rules/executions/%d", e.ID)), nil, http.StatusNotFound, nil)
	f.request(b.token, http.MethodPost, b.path(fmt.Sprintf("/conversations/%d/messages", convA.ID)), map[string]any{"content": "cross-tenant attempt"}, http.StatusNotFound, nil)
	convB := f.conversation(b)
	f.flush()
	f.state(b, convB.ID, "resolved", "low")
	f.state(a, convA.ID, "open", "high")
	eB := f.execution(b, ruleB.ID, "succeeded", "")
	if eB.AccountID != b.id || eB.ConversationID != convB.ID || f.execution(a, ruleA.ID, "succeeded", "").ID != e.ID {
		t.Fatalf("durable tenant routing is incorrect: tenant A=%+v tenant B=%+v", e, eB)
	}
	if versions := f.versions(a, ruleA.ID); len(versions) != 1 {
		t.Fatalf("cross-tenant rule update changed frozen versions: %+v", versions)
	}
}

func TestAutomationDurableBridgeAPICanonicalFrozenDefinition(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("canonical")
	// The established API accepts JSON strings, wrapper objects, and name/params
	// aliases. Its frozen version must be consumable by the durable compiler.
	rule := f.rule(a, "conversation_created", "{}", `{"actions":[{"name":"change_priority","params":{"priority":"high"}}]}`)
	versions := f.versions(a, rule.ID)
	if len(versions) != 1 || versions[0].Conditions != "[]" {
		t.Fatalf("empty conditions were not canonicalized: %+v", versions)
	}
	var actions []struct {
		ActionName   string            `json:"action_name"`
		ActionParams map[string]string `json:"action_params"`
	}
	if err := json.Unmarshal([]byte(versions[0].Actions), &actions); err != nil || len(actions) != 1 || actions[0].ActionName != "change_priority" || actions[0].ActionParams["priority"] != "high" {
		t.Fatalf("legacy API action shape was not frozen canonically: %s", versions[0].Actions)
	}
	conv := f.conversation(a)
	f.state(a, conv.ID, "open", "low")
	if len(f.executions(a, rule.ID)) != 0 {
		t.Fatal("canonicalization selected the legacy executor")
	}
	f.flush()
	f.state(a, conv.ID, "open", "high")
	f.execution(a, rule.ID, "succeeded", "")
}

func TestAutomationDurableBridgeAPINonUTCRuleClock(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	// SQLite persists timestamp offsets as text. Exercise the real API with a
	// non-UTC GORM clock even when the test process itself runs in UTC, so an
	// older rule can never be excluded by lexical timestamp comparison.
	zone := time.FixedZone("non-utc-rule-clock", 4*60*60)
	f.db.Config.NowFunc = func() time.Time { return time.Now().In(zone) }
	a := f.tenant("non-utc")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	conv := f.conversation(a)
	f.state(a, conv.ID, "open", "low")
	f.flush()
	f.state(a, conv.ID, "open", "high")
	f.execution(a, rule.ID, "succeeded", "")
}

func TestAutomationDurableBridgeAPIStaleFactBeforeAdmission(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("stale-fact")
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	conv := f.conversation(a)
	event := f.fact(f.con, "conversation_created", conv.ID)
	// The fact is durable, but has not yet been admitted by AUT. A user's later
	// owner mutation must not be overwritten by rebasing that old fact.
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/conversations/%d", conv.ID)), map[string]any{"priority": "urgent"}, http.StatusOK, nil)
	manual := f.state(a, conv.ID, "open", "urgent")
	f.flush()
	e := f.execution(a, rule.ID, "failed", "target_version_stale")
	after := f.state(a, conv.ID, "open", "urgent")
	if e.EventID != event.EventID || e.TargetVersion != uint(event.AggregateVersion) || e.TargetVersion >= uint(manual.ObjectVersion) || after.ObjectVersion != manual.ObjectVersion {
		t.Fatalf("stale fact rebased its target or overwrote a newer API change: execution=%+v manual_version=%d final_version=%d", e, manual.ObjectVersion, after.ObjectVersion)
	}
	f.admit(event)
	if f.flush() != 0 || f.execution(a, rule.ID, "failed", "target_version_stale").ID != e.ID {
		t.Fatal("stale fact redelivery changed the terminal result")
	}
}

func TestAutomationDurableBridgeAPICancelQueuedExecution(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a, b := f.tenant("cancel-owner"), f.tenant("cancel-other")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("change_priority", "high"), durableAutomationAction("change_status", "resolved"),
	})
	conv := f.conversation(a)
	f.admit(f.fact(f.con, "conversation_created", conv.ID))
	queued := f.execution(a, rule.ID, "received", "")
	path := fmt.Sprintf("/automation_executions/%d/cancel", queued.ID)
	f.request(b.token, http.MethodPost, b.path(path), map[string]any{}, http.StatusNotFound, nil)
	if f.execution(a, rule.ID, "received", "").CancelRequested {
		t.Fatal("cross-tenant cancellation changed the owner's execution")
	}
	// Confirm the route enforces automation_manage even for an account member.
	if err := f.db.Model(&domain.AccountUser{}).Where("account_id = ?", a.id).Update("role", domain.RoleAgent).Error; err != nil {
		t.Fatal(err)
	}
	f.request(a.token, http.MethodPost, a.path(path), map[string]any{}, http.StatusForbidden, nil)
	if err := f.db.Model(&domain.AccountUser{}).Where("account_id = ?", a.id).Update("role", domain.RoleAdministrator).Error; err != nil {
		t.Fatal(err)
	}
	var result struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}
	f.request(a.token, http.MethodPost, a.path(path), map[string]any{}, http.StatusOK, &result)
	if result.ID != queued.ID || result.Status != "cancelled" {
		t.Fatalf("queued cancellation was not acknowledged: %+v", result)
	}
	f.request(a.token, http.MethodPost, a.path(path), map[string]any{}, http.StatusOK, &result)
	f.restartBridge()
	f.flush()
	f.state(a, conv.ID, "open", "low")
	e := f.execution(a, rule.ID, "cancelled", "cancelled_by_request")
	if e.ID != queued.ID {
		t.Fatal("cancellation replay replaced the durable execution")
	}
	var results []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(e.ActionResults), &results); err != nil || len(results) != 2 || results[0].Status != "skipped" || results[1].Status != "skipped" {
		t.Fatalf("cancelled queued actions were not skipped: %s", e.ActionResults)
	}
}

func TestAutomationDurableBridgeAPICancelWaitingExecution(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("cancel-waiting")
	rule := f.rule(a, "conversation_created", []any{}, []any{
		durableAutomationAction("change_priority", "high"), durableAutomationAction("change_status", "resolved"),
	})
	conv := f.conversation(a)
	f.admit(f.fact(f.con, "conversation_created", conv.ID))
	// A temporarily unavailable owner produces the runner's real durable
	// result_unknown checkpoint, without any provider or network operation.
	dependency := f.bridge.Runtime.Config.Dependencies["CON"]
	delete(f.bridge.Runtime.Config.Dependencies, "CON")
	if ran, err := f.bridge.Runtime.RunAutomationOnce(context.Background()); err != nil || !ran {
		t.Fatalf("create waiting owner checkpoint: ran=%v err=%v", ran, err)
	}
	waiting := f.execution(a, rule.ID, "waiting", "")
	var result struct {
		Status          string `json:"status"`
		CancelRequested bool   `json:"cancel_requested"`
	}
	f.request(a.token, http.MethodPost, a.path(fmt.Sprintf("/automation_executions/%d/cancel", waiting.ID)), map[string]any{}, http.StatusOK, &result)
	if result.Status != "waiting" || !result.CancelRequested {
		t.Fatalf("in-flight cancellation falsely claimed a terminal outcome: %+v", result)
	}
	f.state(a, conv.ID, "open", "low")
	pending := f.execution(a, rule.ID, "waiting", "")
	if !pending.CancelRequested || pending.CompletedAt != nil {
		t.Fatalf("unknown owner result was prematurely finalized: %+v", pending)
	}
	f.bridge.Runtime.Config.Dependencies["CON"] = dependency
	// Advance only the retry deadline so the deterministic test need not sleep.
	if err := f.db.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ?", a.id, waiting.ID).Update("runner_retry_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	f.flush()
	f.state(a, conv.ID, "open", "high")
	e := f.execution(a, rule.ID, "cancelled", "cancelled_by_request")
	var results []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(e.ActionResults), &results); err != nil || len(results) != 2 || results[0].Status != "succeeded" || results[1].Status != "skipped" {
		t.Fatalf("cancellation did not reconcile accepted work then stop the next action: %s", e.ActionResults)
	}
}

func TestAutomationDurableBridgeAPIResolvedInboundMessageUsesCompletedWorkflow(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("resolved-inbound")
	var inbox domain.Inbox
	f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/inboxes/%d", a.inboxID)), nil, http.StatusOK, &inbox)
	sourceID, visitorToken, contactID := bootstrapWidgetVisitor(t, f.router, inbox.WebsiteToken)
	a.contactID = contactID
	conv := f.conversation(a)
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/conversations/%d", conv.ID)), map[string]any{"status": "resolved"}, http.StatusOK, nil)
	f.flush()
	rule := f.rule(a, "message_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	body, err := json.Marshal(map[string]any{"source_id": sourceID, "conversation_id": conv.ID, "content": "Reopen this resolved conversation"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/messages?website_token="+inbox.WebsiteToken, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Visitor-Session", visitorToken)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("create inbound widget message: code=%d body=%s", response.Code, response.Body.String())
	}
	var message struct {
		Data domain.Message `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil || message.Data.ID == 0 {
		t.Fatalf("decode inbound widget message: err=%v body=%s", err, response.Body.String())
	}
	reopened := f.state(a, conv.ID, "open", "low")
	event := f.fact(f.msg, "message_created", conv.ID)
	f.flush()
	after := f.state(a, conv.ID, "open", "high")
	e := f.execution(a, rule.ID, "succeeded", "")
	if e.MessageID == nil || *e.MessageID != message.Data.ID || e.EventID != event.EventID || e.TargetVersion != uint(reopened.ObjectVersion) || after.ObjectVersion != reopened.ObjectVersion+1 {
		t.Fatalf("message fact did not bind the completed reopen workflow: %+v", e)
	}
	f.restartBridge()
	f.admit(event)
	if f.flush() != 0 || f.execution(a, rule.ID, "succeeded", "").ID != e.ID {
		t.Fatal("replaying the completed inbound workflow duplicated execution")
	}
}

func TestAutomationDurableBridgeAPIRejectsUndeclaredEvent(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("unsupported-event")
	f.request(a.token, http.MethodPost, a.path("/automation_rules"), map[string]any{"name": "Never emitted", "event_name": "conversation_closed", "conditions": []any{}, "actions": []any{durableAutomationAction("change_status", "resolved")}, "active": true}, http.StatusBadRequest, nil)
	rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_status", "resolved")})
	f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), map[string]any{"event_name": "conversation_closed"}, http.StatusBadRequest, nil)
	var stored domain.AutomationRule
	if err := f.db.First(&stored, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EventName != "conversation_created" {
		t.Fatal("unsupported update committed")
	}
}

func TestAutomationDurableBridgeAPIScalarSupportedParameters(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("scalar-parameters")
	f.rule(a, "conversation_created", []any{}, []any{map[string]any{"action_name": "change_status", "action_params": "resolved"}, map[string]any{"action_name": "change_priority", "action_params": "urgent"}})
	conv := f.conversation(a)
	for i := 0; i < 5; i++ {
		n, err := f.bridge.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	f.state(a, conv.ID, "resolved", "urgent")
}

func TestAutomationDurableBridgeAPICreatedAfterInitialRouting(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual_interleaving_%v", manual), func(t *testing.T) {
			f := newDurableAutomationAPIFixture(t)
			a := f.tenant("initial-routing")
			var membership domain.AccountUser
			if err := f.db.Where("account_id = ?", a.id).First(&membership).Error; err != nil {
				t.Fatal(err)
			}
			users := repository.NewUserRepository(f.db)
			if err := users.UpdateAvailability(membership.UserID, domain.AvailabilityOnline); err != nil {
				t.Fatal(err)
			}
			inboxes := repository.NewInboxRepository(f.db)
			if err := inboxes.AddMember(a.inboxID, membership.UserID); err != nil {
				t.Fatal(err)
			}
			rule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
			conv := f.conversation(a)
			if conv.AssigneeID == nil || *conv.AssigneeID != membership.UserID || conv.ObjectVersion != 1 {
				t.Fatalf("authenticated create did not commit initial routing atomically: %+v", conv)
			}
			event := f.fact(f.con, "conversation_created", conv.ID)
			if event.AggregateVersion != 1 || fmt.Sprint(event.Payload["object_version"]) != "1" {
				t.Fatalf("created fact does not bind routing-completed owner: %+v", event)
			}
			if manual {
				f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/conversations/%d", conv.ID)), map[string]any{"priority": "urgent"}, http.StatusOK, nil)
			}
			f.restartBridge()
			f.flush()
			if manual {
				after := f.state(a, conv.ID, "open", "urgent")
				e := f.execution(a, rule.ID, "failed", "target_version_stale")
				if e.TargetVersion != 1 || after.ObjectVersion != 2 {
					t.Fatalf("manual edit was rebased or overwritten: state=%+v execution=%+v", after, e)
				}
			} else {
				after := f.state(a, conv.ID, "open", "high")
				e := f.execution(a, rule.ID, "succeeded", "")
				if e.TargetVersion != 1 || after.ObjectVersion != 2 || after.AssigneeID == nil || *after.AssigneeID != membership.UserID {
					t.Fatalf("routed create failed to execute: state=%+v execution=%+v", after, e)
				}
				f.admit(event)
				if f.flush() != 0 || f.execution(a, rule.ID, "succeeded", "").ID != e.ID {
					t.Fatal("replay duplicated routed-create execution")
				}
			}
		})
	}
}

func TestAutomationDurableBridgeAPIInitialLocalMessagesBelongToCreatedWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name             string
		widget, offHours bool
		messages         int
	}{
		{"widget_initial", true, false, 1},
		{"off_hours_reply", false, true, 1},
		{"widget_initial_and_reply", true, true, 2},
		{"widget_initial_matches_reply", true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDurableAutomationAPIFixture(t)
			a := f.tenant(tc.name)
			if tc.offHours {
				reply := "We will reply during office hours"
				if tc.name == "widget_initial_matches_reply" {
					reply = "Start this conversation"
				}
				if err := f.db.Model(&domain.Inbox{}).Where("account_id = ? AND id = ?", a.id, a.inboxID).Updates(map[string]any{"working_hours_enabled": true, "working_hours": "[]", "out_of_office_message": reply}).Error; err != nil {
					t.Fatal(err)
				}
			}
			createdRule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "urgent")})
			messageRule := f.rule(a, "message_created", []any{}, []any{durableAutomationAction("change_status", "resolved")})
			var conv domain.Conversation
			if tc.widget {
				var inbox domain.Inbox
				f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/inboxes/%d", a.inboxID)), nil, http.StatusOK, &inbox)
				_, visitorToken, _ := bootstrapWidgetVisitor(t, f.router, inbox.WebsiteToken)
				request := httptest.NewRequest(http.MethodPost, "/api/v1/widget/conversations?website_token="+inbox.WebsiteToken, bytes.NewBufferString(`{"message":"Start this conversation"}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Visitor-Session", visitorToken)
				response := httptest.NewRecorder()
				f.router.ServeHTTP(response, request)
				if response.Code != http.StatusCreated {
					t.Fatalf("widget create: %d %s", response.Code, response.Body.String())
				}
				var payload struct {
					Data struct {
						Conversation domain.Conversation `json:"conversation"`
						Message      domain.Message      `json:"message"`
					} `json:"data"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				conv = payload.Data.Conversation
				if payload.Data.Message.ID == 0 || payload.Data.Message.ConversationID != conv.ID {
					t.Fatalf("missing durable initial message: %s", response.Body.String())
				}
			} else {
				conv = f.conversation(a)
			}
			if conv.ID == 0 || conv.ObjectVersion != 1+tc.messages {
				t.Fatalf("creation response lacks completed local workflow: %+v", conv)
			}
			fact := f.fact(f.con, "conversation_created", conv.ID)
			if fmt.Sprint(fact.Payload["object_version"]) != fmt.Sprint(conv.ObjectVersion) {
				t.Fatalf("wrong completed creation target: %+v", fact)
			}
			if messages, err := f.msg.PendingAutomationFacts(context.Background(), 100); err != nil || len(messages) != 0 {
				t.Fatalf("internal initial message exposed a standalone callback: %+v %v", messages, err)
			}
			f.restartBridge()
			f.flush()
			after := f.state(a, conv.ID, "open", "urgent")
			e := f.execution(a, createdRule.ID, "succeeded", "")
			if e.TargetVersion != uint(conv.ObjectVersion) || after.ObjectVersion != conv.ObjectVersion+1 {
				t.Fatalf("created workflow did not execute: %+v", e)
			}
			if executions := f.executions(a, messageRule.ID); len(executions) != 0 {
				t.Fatalf("initial internal messages fired ordinary message callbacks: %+v", executions)
			}
			f.admit(fact)
			f.restartBridge()
			f.flush()
			var count int64
			if err := f.db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Count(&count).Error; err != nil || count != int64(tc.messages) {
				t.Fatalf("workflow replay duplicated planned messages: %d %v", count, err)
			}
			if f.execution(a, createdRule.ID, "succeeded", "").ID != e.ID {
				t.Fatal("workflow replay duplicated execution")
			}
		})
	}
}

func TestAutomationDurableBridgeAPIInitialWorkflowRecoversCommittedMessage(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual_interleaving_%v", manual), func(t *testing.T) {
			f := newDurableAutomationAPIFixture(t)
			a := f.tenant("interrupted-widget")
			var inbox domain.Inbox
			f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/inboxes/%d", a.inboxID)), nil, http.StatusOK, &inbox)
			_, visitorToken, _ := bootstrapWidgetVisitor(t, f.router, inbox.WebsiteToken)
			createdRule := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
			messageRule := f.rule(a, "message_created", []any{}, []any{durableAutomationAction("change_status", "resolved")})
			const callback = "fail_initial_owner_receipt"
			if err := f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.SourceModule == "CON" && row.SourceType == "msg.message.created.v1" {
					_ = tx.AddError(errors.New("scripted owner receipt outage"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/widget/conversations?website_token="+inbox.WebsiteToken, bytes.NewBufferString(`{"message":"Persist before owner outage"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Visitor-Session", visitorToken)
			response := httptest.NewRecorder()
			f.router.ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("expected delivery outage, got %d %s", response.Code, response.Body.String())
			}
			if err := f.db.Callback().Create().Remove(callback); err != nil {
				t.Fatal(err)
			}
			var conv domain.Conversation
			if err := f.db.Where("account_id = ?", a.id).First(&conv).Error; err != nil {
				t.Fatal(err)
			}
			if conv.ObjectVersion != 1 || conv.UnreadCount != 0 {
				t.Fatalf("failed activity did not roll back: %+v", conv)
			}
			if facts, err := f.con.PendingAutomationFacts(context.Background(), 100); err != nil || len(facts) != 0 {
				t.Fatalf("unfinished creation escaped gate: %+v %v", facts, err)
			}
			if manual {
				f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/conversations/%d", conv.ID)), map[string]any{"priority": "urgent"}, http.StatusOK, nil)
			}
			f.restartBridge()
			f.flush()
			if manual {
				after := f.state(a, conv.ID, "open", "urgent")
				e := f.execution(a, createdRule.ID, "failed", "target_version_stale")
				if e.TargetVersion != 1 || after.ObjectVersion != 3 {
					t.Fatalf("recovered workflow rebased manual owner: %+v %+v", after, e)
				}
			} else {
				after := f.state(a, conv.ID, "open", "high")
				e := f.execution(a, createdRule.ID, "succeeded", "")
				if e.TargetVersion != 2 || after.ObjectVersion != 3 {
					t.Fatalf("recovered workflow target incorrect: %+v %+v", after, e)
				}
			}
			var messages []domain.Message
			if err := f.db.Where("account_id = ? AND conversation_id = ?", a.id, conv.ID).Find(&messages).Error; err != nil || len(messages) != 1 {
				t.Fatalf("recovery duplicated message: %+v %v", messages, err)
			}
			if len(f.executions(a, messageRule.ID)) != 0 {
				t.Fatal("recovery emitted a new standalone message callback")
			}
		})
	}
}

func TestAutomationDurableBridgeAPIConditionsObservePrecedingRules(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("frozen-conditions")
	condition := func(key, value string) []any {
		return []any{map[string]any{"attribute_key": key, "filter_operator": "equal_to", "values": []string{value}}}
	}
	noMatch := f.rule(a, "conversation_created", condition("status", "pending"), []any{durableAutomationAction("change_priority", "urgent")})
	first := f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")})
	last := f.rule(a, "conversation_created", condition("priority", "low"), []any{durableAutomationAction("change_status", "resolved")})
	conv := f.conversation(a)
	// Freeze the original rule definitions and defer the later condition.
	facts, err := f.con.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts=%d err=%v", len(facts), err)
	}
	if err := f.bridge.Runtime.AcceptAutomationFact(context.Background(), facts[0]); err != nil {
		t.Fatal(err)
	}
	f.execution(a, noMatch.ID, "no_match", "conditions_not_matched")
	f.execution(a, first.ID, "received", "")
	f.execution(a, last.ID, "evaluating", "")
	f.restartBridge()
	f.flush()
	f.state(a, conv.ID, "open", "high")
	f.execution(a, noMatch.ID, "no_match", "conditions_not_matched")
	f.execution(a, first.ID, "succeeded", "")
	f.execution(a, last.ID, "no_match", "conditions_not_matched")
	if f.flush() != 0 {
		t.Fatal("frozen conditions were reevaluated on replay")
	}
}

func TestAutomationDurableBridgeAPIOwnerConditionFailureKeepsFactPending(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("condition-dependency")
	rule := f.rule(a, "conversation_created", []any{map[string]any{"attribute_key": "email", "filter_operator": "contains", "values": []string{"customer"}}}, []any{durableAutomationAction("change_priority", "urgent")})
	conv := f.conversation(a)
	dependency := f.bridge.Runtime.Config.Dependencies["CUS"]
	delete(f.bridge.Runtime.Config.Dependencies, "CUS")
	if _, err := f.bridge.RunOnce(context.Background()); err == nil {
		t.Fatal("missing condition owner was accepted as no-match")
	}
	if len(f.executions(a, rule.ID)) != 0 {
		t.Fatal("dependency failure finalized an execution")
	}
	f.state(a, conv.ID, "open", "low")
	facts, err := f.con.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(facts) != 1 {
		t.Fatalf("dependency failure lost fact: %d %v", len(facts), err)
	}
	f.bridge.Runtime.Config.Dependencies["CUS"] = dependency
	f.flush()
	f.state(a, conv.ID, "open", "urgent")
	f.execution(a, rule.ID, "succeeded", "")
}
