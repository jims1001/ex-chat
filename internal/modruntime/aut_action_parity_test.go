package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type automationActionFixture struct {
	*autRunnerFixture
	owners   map[string]*Runtime
	lost     map[string]*atomic.Bool
	captures map[string][]string
	mu       sync.Mutex
}

func newAutomationActionFixture(t *testing.T, pg bool, actions string) *automationActionFixture {
	t.Helper()
	f := &automationActionFixture{autRunnerFixture: &autRunnerFixture{t: t}, owners: map[string]*Runtime{}, lost: map[string]*atomic.Bool{}, captures: map[string][]string{}}
	dbMaker := newModuleTestDB
	if pg {
		dbMaker = postgresModuleDB
	}
	for _, owner := range []string{"IAM", "TEN", "CUS", "CON", "MSG", "AUT"} {
		cfg := businessConfig(owner)
		cfg.MaxTimeout = 2 * time.Second
		cfg.MaxRequestBytes = 1 << 20
		cfg.EnableBusiness = true
		cfg.AllowedCallers = map[string]bool{"API": true, "AUT": true, "CON": true, "MSG": true, "TEN": true}
		edges := map[string][]string{"TEN": {"IAM"}, "CON": {"TEN", "CUS"}, "MSG": {"CON"}, "AUT": {"CON", "MSG", "CUS"}}
		for _, target := range edges[owner] {
			r := f.owners[target]
			cfg.Dependencies[target] = DependencyConfig{URL: "http://local-" + target, Token: r.Config.AuthToken, LocalHandler: f.handler(target)}
		}
		r := &Runtime{Config: cfg, DB: dbMaker(t, owner), Contracts: NewContractRegistry(owner)}
		if !pg {
			sqlDB, _ := r.DB.DB()
			sqlDB.SetMaxOpenConns(1)
		}
		if err := r.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		f.owners[owner] = r
		f.lost[owner] = &atomic.Bool{}
	}
	f.aut = *f.owners["AUT"]
	f.con = f.owners["CON"].DB
	now := time.Now().UTC()
	f.conversation = domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: "open", Priority: "medium", ObjectVersion: 1, CreatedAt: now, UpdatedAt: now, LastActivityAt: now}
	if err := f.con.Create(&f.conversation).Error; err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{&domain.AccountUser{AccountID: 91, UserID: 7}, &domain.AccountUser{AccountID: 92, UserID: 8}, &domain.Team{ID: 3, AccountID: 91, Name: "support"}, &domain.Team{ID: 4, AccountID: 92, Name: "foreign"}} {
		if err := f.owners["TEN"].DB.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.rule = domain.AutomationRule{AccountID: 91, Name: "Parity", EventName: "conversation_created", Conditions: "[]", Actions: actions, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := f.aut.DB.Create(&f.rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.aut.DB.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: f.rule.ID, Version: 1, EventName: f.rule.EventName, Conditions: "[]", Actions: actions, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *automationActionFixture) handler(owner string) http.Handler {
	handler := f.owners[owner].Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isCommand := strings.Contains(r.URL.Path, "/commands/")
		var raw string
		if isCommand {
			var command foundation.CommandEnvelope
			json.NewDecoder(r.Body).Decode(&command)
			encoded, _ := json.Marshal(command)
			raw = string(encoded)
			r.Body = http.NoBody
			r2, _ := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), strings.NewReader(raw))
			r2.Header = r.Header
			r = r2
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		if isCommand {
			f.mu.Lock()
			f.captures[owner] = append(f.captures[owner], raw)
			f.mu.Unlock()
			if recorder.Code == 200 && f.lost[owner].Swap(false) {
				w.WriteHeader(503)
				return
			}
		}
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorder.Code)
		w.Write(recorder.Body.Bytes())
	})
}
func (f *automationActionFixture) drain(id uint) domain.AutomationRuleExecution {
	f.t.Helper()
	for i := 0; i < 100; i++ {
		e := f.execution(id)
		switch e.Status {
		case "received", "running", "waiting":
			if e.RunnerRetryAt != nil {
				f.aut.DB.Model(&e).Update("runner_retry_at", time.Now().UTC().Add(-time.Second))
			}
			f.run()
		default:
			return e
		}
	}
	f.t.Fatalf("execution did not terminate: %+v", f.execution(id))
	return domain.AutomationRuleExecution{}
}
func (f *automationActionFixture) conversationState() domain.Conversation {
	f.t.Helper()
	var c domain.Conversation
	if err := f.con.First(&c, f.conversation.ID).Error; err != nil {
		f.t.Fatal(err)
	}
	return c
}

const parityActions = `[
{"action_name":"remove_assigned_agent"},
{"action_name":"assign_agent","action_params":{"user_id":7}},
{"action_name":"assign_agent","action_params":["7"]},
{"action_name":"assign_team","action_params":{"team_ids":[3]}},
{"action_name":"remove_assigned_team"},
{"action_name":"remove_assigned_agent"},
{"action_name":"add_labels","action_params":{"labels":["vip","blue"]}},
{"action_name":"add_label","action_params":"vip"},
{"action_name":"remove_labels","action_params":{"remove":["blue","missing"]}},
{"action_name":"mute_conversation"},
{"action_name":"snooze_conversation","action_params":{"duration":"2h"}},
{"action_name":"open_conversation"},
{"action_name":"change_priority","action_params":"urgent"},
{"action_name":"send_reply","action_params":{"message":"public queued"}},
{"action_name":"private_note","action_params":{"content":"private secret"}}
]`

func TestAUTLocalActionParityRealOwners(t *testing.T)         { testAUTLocalActionParity(t, false) }
func TestPostgresAUTLocalActionParityRealOwners(t *testing.T) { testAUTLocalActionParity(t, true) }
func testAUTLocalActionParity(t *testing.T, pg bool) {
	f := newAutomationActionFixture(t, pg, parityActions)
	e := f.call(f.startCommand(), 200)
	final := f.drain(e.ID)
	if final.Status != "succeeded" {
		t.Fatalf("final=%+v", final)
	}
	c := f.conversationState()
	if c.AssigneeID != nil || c.TeamID != nil || !c.Muted || c.Status != "open" || c.Priority != "urgent" || c.SnoozedUntil != nil {
		t.Fatalf("state=%+v", c)
	}
	var labels []domain.Label
	f.owners["CUS"].DB.Order("id").Find(&labels)
	if len(labels) != 2 {
		t.Fatalf("labels=%+v", labels)
	}
	var links []domain.ConversationLabel
	f.con.Find(&links)
	if len(links) != 1 || links[0].LabelID != labels[0].ID {
		t.Fatalf("links=%+v", links)
	}
	var messages []domain.Message
	f.owners["MSG"].DB.Order("id").Find(&messages)
	if len(messages) != 2 || messages[0].Status != "queued" || messages[0].Private || messages[0].Content != "public queued" || messages[1].Status != "saved" || !messages[1].Private || messages[1].Content != "private secret" {
		t.Fatalf("messages=%+v", messages)
	}
	var results []automationActionResult
	json.Unmarshal([]byte(final.ActionResults), &results)
	last := results[len(results)-1]
	if last.Outcome != "saved" || results[len(results)-3].Outcome != "queued" {
		t.Fatalf("results=%s", final.ActionResults)
	}
	view, _ := json.Marshal(automationExecutionView(&final))
	if strings.Contains(string(view), "private secret") || strings.Contains(string(view), "public queued") {
		t.Fatalf("execution leaked content: %s", view)
	}
	var events []domain.LocalChangeJournal
	f.owners["MSG"].DB.Where("action = ?", "event_outbox").Find(&events)
	if len(events) != 2 {
		t.Fatalf("message events=%d", len(events))
	}
	for _, row := range events {
		if strings.Contains(row.AfterState, "private secret") || strings.Contains(row.AfterState, "public queued") {
			t.Fatal("event leaked message body")
		}
		var event foundation.EventEnvelope
		json.Unmarshal([]byte(row.AfterState), &event)
		if origin, err := uintFilter(event.Payload, "origin_execution_id"); err != nil || origin != e.ID {
			t.Fatalf("ancestry missing: %s", row.AfterState)
		}
	}
	// Every owner can be snapshotted independently, including no-op receipts and
	// queued message outcomes in AUT's private dispatch checkpoint.
	for _, owner := range []string{"AUT", "CON", "CUS", "MSG"} {
		path := filepath.Join(t.TempDir(), owner+".json")
		if err := CreateSnapshot(f.owners[owner].DB, owner, "parity", path); err != nil {
			t.Fatal(err)
		}
		target := newModuleTestDB(t, owner)
		if err := RestoreSnapshot(context.Background(), target, owner, "parity", path, nil); err != nil {
			t.Fatalf("%s restore: %v", owner, err)
		}
	}
}
func TestAUTLocalActionFrozenSnoozeAndMultiOwnerLostResponses(t *testing.T) {
	for _, actions := range []string{`[{"action_name":"snooze","action_params":["30m"]}]`, `[{"action_name":"add_label","action_params":["vip"]}]`, `[{"action_name":"send_message","action_params":["hello"]}]`, `[{"action_name":"add_private_note","action_params":["secret"]}]`} {
		t.Run(actions, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, actions)
			e := f.call(f.startCommand(), 200)
			var c automationRunnerContext
			json.Unmarshal([]byte(f.execution(e.ID).RunnerContext), &c)
			for index, a := range c.Actions {
				owner := automationActionOwner(a)
				f.lost[owner].Store(true)
				f.run()
				unknown := f.execution(e.ID)
				if unknown.Status != "waiting" {
					t.Fatalf("unknown=%+v", unknown)
				}
				f.aut.DB.Model(&unknown).Update("runner_retry_at", time.Now().UTC().Add(-time.Second))
				restarted := f.aut
				f.aut = restarted
				f.run()
				f.mu.Lock()
				captures := f.captures[owner]
				n := len(captures)
				if n < 2 || captures[n-1] != captures[n-2] {
					t.Fatalf("step %d changed envelope: %+v", index, captures)
				}
				f.mu.Unlock()
			}
			final := f.execution(e.ID)
			if final.Status != "succeeded" {
				t.Fatalf("final=%+v", final)
			}
			if strings.Contains(actions, "snooze") {
				state := f.conversationState()
				if state.SnoozedUntil == nil || state.ObjectVersion != 2 {
					t.Fatalf("snooze=%+v", state)
				}
			}
			if strings.Contains(actions, "message") || strings.Contains(actions, "private_note") {
				var count int64
				f.owners["MSG"].DB.Model(&domain.Message{}).Count(&count)
				if count != 1 {
					t.Fatalf("duplicate rows %d", count)
				}
			}
		})
	}
}
func TestAUTLocalActionsRejectStaleAndTenantTargets(t *testing.T) {
	for _, actions := range []string{`[{"action_name":"assign_agent","action_params":[8]}]`, `[{"action_name":"assign_team","action_params":[4]}]`} {
		f := newAutomationActionFixture(t, false, actions)
		e := f.call(f.startCommand(), 200)
		if final := f.drain(e.ID); final.Status != "failed" {
			t.Fatalf("foreign target=%+v", final)
		}
		f.assertTarget("open", "medium", 1)
	}
	for _, actions := range []string{`[{"action_name":"add_label","action_params":["vip"]}]`, `[{"action_name":"send_message","action_params":["hello"]}]`, `[{"action_name":"remove_assigned_agent"},{"action_name":"assign_agent","action_params":[7]}]`} {
		f := newAutomationActionFixture(t, false, actions)
		e := f.call(f.startCommand(), 200)
		f.run()
		f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"priority": "high", "object_version": gorm.Expr("object_version + 1")})
		final := f.drain(e.ID)
		if final.Status != "partially_succeeded" {
			t.Fatalf("stale=%+v", final)
		}
		var count int64
		f.owners["MSG"].DB.Model(&domain.Message{}).Count(&count)
		if count != 0 {
			t.Fatal("stale message was created")
		}
	}
}
func TestAUTMessageGrantConsumedOnceAndReceiptsSurviveLaterState(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":["hello"]}]`)
	e := f.call(f.startCommand(), 200)
	f.drain(e.ID)
	var runner automationRunnerContext
	json.Unmarshal([]byte(f.execution(e.ID).RunnerContext), &runner)
	command := runner.Dispatches[1]
	command.CommandID = "alternate-message-key"
	command.IdempotencyKey = command.CommandID
	f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"status": "resolved", "object_version": gorm.Expr("object_version + 1")})
	callAutomationOwner(t, f.owners["MSG"], command, 200)
	var count int64
	f.owners["MSG"].DB.Model(&domain.Message{}).Count(&count)
	if count != 1 {
		t.Fatalf("grant created %d messages", count)
	}
	changed := command
	changed.CommandID = "different-content"
	changed.IdempotencyKey = changed.CommandID
	changed.Input = map[string]any{}
	for k, v := range command.Input {
		changed.Input[k] = v
	}
	changed.Input["content"] = "tampered"
	callAutomationOwner(t, f.owners["MSG"], changed, 403)
}
func TestAUTLocalActionCausalNoopsAndQueuedMessages(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"remove_assigned_agent"}]`)
	addCausalRule(t, f.autRunnerFixture, `[{"action_name":"send_message","action_params":["one"]}]`)
	addCausalRule(t, f.autRunnerFixture, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	for _, row := range rows {
		f.drain(row.ID)
	}
	f.assertTarget("open", "urgent", 4) // valid no-op activity, message preparation, priority
	rows = causalExecutions(t, f.autRunnerFixture, event.EventID)
	for _, row := range rows {
		if row.Status != "succeeded" {
			t.Fatalf("rows=%+v", rows)
		}
	}
}
func TestAUTActionCompilerLegacyFormsAndUnsupportedBoundaries(t *testing.T) {
	for _, raw := range []string{`[{"action_name":"snooze"}]`, `[{"action_name":"assign_agent","action_params":"7"}]`, `[{"action_name":"assign_team","action_params":{"id":3}}]`, `[{"action_name":"change_status"}]`, `[{"action_name":"reply","action_params":"ok"}]`, `[{"action_name":"remove_labels","action_params":{"labels":["7","vip"]}}]`} {
		actions, failure := compileAutomationActions(raw)
		if failure != "" || !validAutomationPlan(actions) {
			t.Fatalf("%s => %+v %s", raw, actions, failure)
		}
	}
	for _, raw := range []string{`[{"action_name":"assign_agent","action_params":[1.5]}]`, `[{"action_name":"send_message"}]`, `[{"action_name":"webhook","action_params":"https://example.test"}]`} {
		if actions, failure := compileAutomationActions(raw); failure == "" {
			t.Fatalf("accepted %s %+v", raw, actions)
		}
	}
}

func TestAUTNewActionUnknownSnapshotRestoresEveryOwner(t *testing.T) {
	for _, raw := range []string{`[{"action_name":"snooze","action_params":["1h"]}]`, `[{"action_name":"add_label","action_params":"vip"}]`, `[{"action_name":"private_note","action_params":"snapshot secret"}]`} {
		t.Run(raw, func(t *testing.T) {
			source := newAutomationActionFixture(t, false, raw)
			e := source.call(source.startCommand(), 200)
			var plan automationRunnerContext
			json.Unmarshal([]byte(source.execution(e.ID).RunnerContext), &plan)
			for step, action := range plan.Actions {
				owner := automationActionOwner(action)
				source.lost[owner].Store(true)
				source.run()
				current := source.execution(e.ID)
				if current.Status != "waiting" {
					t.Fatalf("step %d=%+v", step, current)
				}
				restored := newAutomationActionFixture(t, false, raw)
				// The fixture rows are replaced by each independent owner's snapshot.
				for _, module := range []string{"IAM", "TEN", "CUS", "CON", "MSG", "AUT"} {
					path := filepath.Join(t.TempDir(), module+".json")
					if err := CreateSnapshot(source.owners[module].DB, module, fmt.Sprintf("unknown-%d", step), path); err != nil {
						t.Fatal(err)
					}
					if err := RestoreSnapshot(context.Background(), restored.owners[module].DB, module, fmt.Sprintf("unknown-%d", step), path, nil); err != nil {
						t.Fatalf("restore %s step %d: %v", module, step, err)
					}
				}
				restored.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_retry_at", time.Now().UTC().Add(-time.Second))
				restored.run()
				reconciled := restored.execution(e.ID)
				if reconciled.RunnerNextAction != step+1 {
					t.Fatalf("reconciled=%+v", reconciled)
				}
				source = restored
			}
			final := source.execution(e.ID)
			if final.Status != "succeeded" {
				t.Fatalf("final=%+v", final)
			}
			if strings.Contains(raw, "private_note") {
				var n int64
				source.owners["MSG"].DB.Model(&domain.Message{}).Count(&n)
				if n != 1 {
					t.Fatalf("message rows=%d", n)
				}
			}
		})
	}
}
func TestAUTNewActionSnapshotRejectsUnfrozenAndTamperedDispatch(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"snooze","action_params":["1h"]}]`)
	e := f.call(f.startCommand(), 200)
	f.lost["CON"].Store(true)
	f.run()
	stored := f.execution(e.ID)
	for _, change := range []func(*automationRunnerContext){func(c *automationRunnerContext) { c.Dispatches = nil }, func(c *automationRunnerContext) { c.Dispatches[0].Target.ExpectedVersion++ },
		func(c *automationRunnerContext) {
			c.Dispatches[0].Input["snoozed_until"] = time.Now().UTC().Add(9 * time.Hour).Format(time.RFC3339Nano)
		}, func(c *automationRunnerContext) { c.Dispatches[0].CommandType = "con.conversation.update.v1" }, func(c *automationRunnerContext) { c.Dispatches[0].Actor.ID = "automation:999" }} {
		copy := stored
		var c automationRunnerContext
		json.Unmarshal([]byte(copy.RunnerContext), &c)
		change(&c)
		raw, _ := json.Marshal(c)
		copy.RunnerContext = string(raw)
		if validateAutomationSnapshotExecution(copy) == nil {
			t.Fatalf("accepted corrupted dispatch: %s", raw)
		}
	}
}
func TestAUTPhase9CheckpointExpansionPreservesConfirmedPrefix(t *testing.T) {
	f := newAUTRunnerFixture(t, false, localTwoActions)
	e := f.call(f.startCommand(), 200)
	f.run()
	stored := f.execution(e.ID)
	var c automationRunnerContext
	json.Unmarshal([]byte(stored.RunnerContext), &c)
	c.Dispatches = nil
	c.FailurePolicy = ""
	for i := range c.Actions {
		c.Actions[i].LogicalIndex = nil
	}
	var results []automationActionResult
	json.Unmarshal([]byte(stored.ActionResults), &results)
	for i := range results {
		results[i].LogicalIndex = nil
	}
	resultRaw, _ := json.Marshal(results)
	f.aut.DB.Model(&stored).Update("action_results", string(resultRaw))
	raw, _ := json.Marshal(c)
	f.aut.DB.Model(&stored).Update("runner_context", string(raw))
	f.run()
	if final := f.execution(e.ID); final.Status != "succeeded" {
		t.Fatalf("legacy restore=%+v", final)
	}
}
func TestAUTMessageGrantAlternateKeysConcurrent(t *testing.T) {
	testAUTMessageGrantAlternateKeysConcurrent(t, false)
}
func TestPostgresAUTMessageGrantAlternateKeysConcurrent(t *testing.T) {
	testAUTMessageGrantAlternateKeysConcurrent(t, true)
}
func testAUTMessageGrantAlternateKeysConcurrent(t *testing.T, pg bool) {
	f := newAutomationActionFixture(t, pg, `[{"action_name":"send_message","action_params":"once"}]`)
	e := f.call(f.startCommand(), 200)
	f.run()
	stored := f.execution(e.ID)
	var plan automationRunnerContext
	var results []automationActionResult
	json.Unmarshal([]byte(stored.RunnerContext), &plan)
	json.Unmarshal([]byte(stored.ActionResults), &results)
	base := automationDispatch(plan, stored, results, 1, 2000, time.Now().UTC())
	handler := f.owners["MSG"].Handler()
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			command := base
			command.CommandID = foundation.GenerateUUID()
			command.IdempotencyKey = command.CommandID
			for retry := 0; retry < 15; retry++ {
				response := performContractRequest(handler, "/contracts/commands/"+command.CommandType, command, func(r *http.Request) {
					r.Header.Set("Authorization", "Bearer "+f.owners["MSG"].Config.AuthToken)
					r.Header.Set(headerCaller, "AUT")
					r.Header.Set(headerCorrelationID, command.CorrelationID)
				})
				if response.Code == 200 {
					return
				}
				if response.Code != 503 && response.Code != 504 {
					t.Errorf("concurrent grant=%d %s", response.Code, response.Body.String())
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Error("grant retry did not converge")
		}(i)
	}
	group.Wait()
	var count int64
	f.owners["MSG"].DB.Model(&domain.Message{}).Count(&count)
	if count != 1 {
		t.Fatalf("messages=%d", count)
	}
	f.run()
	if final := f.execution(e.ID); final.Status != "succeeded" {
		t.Fatalf("runner=%+v", final)
	}
}
func TestAUTTerminalNewActionSnapshotRejectsForgedIdentity(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"remove_assigned_agent"}]`)
	e := f.call(f.startCommand(), 200)
	terminal := f.drain(e.ID)
	var c automationRunnerContext
	json.Unmarshal([]byte(terminal.RunnerContext), &c)
	c.Actor.ID = "automation:999"
	c.Dispatches[0].Actor = c.Actor
	raw, _ := json.Marshal(c)
	terminal.RunnerContext = string(raw)
	if validateAutomationSnapshotExecution(terminal) == nil {
		t.Fatal("terminal private snapshot accepted forged rule identity")
	}
}
func TestAUTBridgeOnlyRequestsNeededConversationAttributes(t *testing.T) {
	for _, conditions := range []string{`[]`, `[{"attribute_key":"status","filter_operator":"is","values":["open"]}]`, `[{"attribute_key":"tier","filter_operator":"is","values":["gold"]}]`} {
		f := newAutomationActionFixture(t, false, `[{"action_name":"remove_assigned_agent"}]`)
		f.aut.DB.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", f.rule.ID).Update("conditions", conditions)
		f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", `{"tier":"gold"}`)
		dependency := f.aut.Config.Dependencies["CON"]
		original := dependency.LocalHandler
		requested := false
		dependency.LocalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var q foundation.QueryEnvelope
			json.NewDecoder(r.Body).Decode(&q)
			for _, field := range q.Fields {
				requested = requested || field == "custom_attributes"
			}
			raw, _ := json.Marshal(q)
			r.Body = http.NoBody
			copy, _ := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), strings.NewReader(string(raw)))
			copy.Header = r.Header
			original.ServeHTTP(w, copy)
		})
		f.aut.Config.Dependencies["CON"] = dependency
		event := bridgeFact(f.autRunnerFixture, "conversation_created")
		if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
			t.Fatal(err)
		}
		if requested != strings.Contains(conditions, "tier") {
			t.Fatalf("unneeded/missing custom attributes %s => %v", conditions, requested)
		}
	}
}
func TestAUTNoMatchSnapshotCannotCarryOwnerEffects(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"change_priority","action_params":"high"}]`)
	e := f.call(f.startCommand(), 200)
	terminal := f.drain(e.ID)
	terminal.Status, terminal.Error = "no_match", "conditions_not_matched"
	if validateAutomationSnapshotExecution(terminal) == nil {
		t.Fatal("no-match accepted owner effects")
	}
	if err := f.aut.DB.Save(&terminal).Error; err != nil {
		t.Fatal(err)
	}
	if validateRestoredAutomationSnapshot(f.aut.DB) == nil {
		t.Fatal("restored no-match accepted owner effects")
	}
}
func TestAUTExpandedActionsMultipleInstances(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"add_labels","action_params":["vip","blue"]},{"action_name":"snooze","action_params":"1h"},{"action_name":"reply","action_params":"once"},{"action_name":"private_note","action_params":"private once"}]`)
	e := f.call(f.startCommand(), 200)
	for _, owner := range []string{"CUS", "CON", "MSG"} {
		f.lost[owner].Store(true)
	}
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			instance := f.aut
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				var current domain.AutomationRuleExecution
				if err := instance.DB.First(&current, e.ID).Error; err != nil {
					time.Sleep(time.Millisecond)
					continue
				}
				if current.Status == "succeeded" || current.Status == "failed" || current.Status == "partially_succeeded" || current.Status == "manual_action_required" {
					return
				}
				if current.RunnerRetryAt != nil {
					instance.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_retry_at", time.Now().UTC().Add(-time.Second))
				}
				instance.RunAutomationOnce(context.Background())
				time.Sleep(time.Millisecond)
			}
		}()
	}
	workers.Wait()
	final := f.execution(e.ID)
	if final.Status != "succeeded" {
		t.Fatalf("multi-instance final=%+v", final)
	}
	var messages []domain.Message
	f.owners["MSG"].DB.Find(&messages)
	if len(messages) != 2 {
		t.Fatalf("messages=%d", len(messages))
	}
	var labels []domain.Label
	f.owners["CUS"].DB.Find(&labels)
	if len(labels) != 2 {
		t.Fatalf("labels=%d", len(labels))
	}
	var events int64
	f.owners["MSG"].DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events)
	if events != 2 {
		t.Fatalf("message facts=%d", events)
	}
}
