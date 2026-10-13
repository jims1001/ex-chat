package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
)

func cascadeRule(t *testing.T, f *automationActionFixture, event, conditions, actions string) domain.AutomationRule {
	t.Helper()
	rule := domain.AutomationRule{AccountID: 91, Name: "Cascade " + event, EventName: event, Conditions: conditions, Actions: actions, Active: true}
	svc := service.NewAutomationService(f.aut.DB, nil, nil)
	svc.SetDurableExecution(true)
	if err := svc.CreateRule(&rule); err != nil {
		t.Fatal(err)
	}
	return rule
}
func cascadeSources(t *testing.T, f *automationActionFixture) (*repository.ConversationRepository, *repository.MessageRepository) {
	t.Helper()
	con := repository.NewConversationRepositoryWithPorts(f.con, nil, nil)
	con.SetAutomationCapture(true)
	if _, err := con.PendingAutomationFacts(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	msg := repository.NewMessageRepositoryWithConversationPort(f.owners["MSG"].DB, con)
	msg.SetAutomationCapture(true)
	return con, msg
}
func cascadeOwnerEvents(t *testing.T, f *automationActionFixture, owner string) []foundation.EventEnvelope {
	t.Helper()
	var rows []domain.LocalChangeJournal
	if err := f.owners[owner].DB.Where("source_module = ? AND action = ?", owner, "event_outbox").Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	result := []foundation.EventEnvelope{}
	for _, row := range rows {
		var event foundation.EventEnvelope
		if json.Unmarshal([]byte(row.AfterState), &event) != nil {
			t.Fatal("invalid owner event")
		}
		result = append(result, event)
	}
	return result
}
func drainCascadeBridge(t *testing.T, b *AutomationBridge) {
	t.Helper()
	for i := 0; i < 100; i++ {
		n, err := b.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("cascade failed to become idle")
}
func TestAutomationCascadeWaitsForTerminalOriginAndOriginalVersion(t *testing.T) {
	for _, state := range []string{"running", "unknown", "cancel_unknown", "manual"} {
		t.Run(state, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"hello"},{"action_name":"change_priority","action_params":"urgent"}]`)
			cascadeRule(t, f, "message_created", "[]", `[{"action_name":"change_status","action_params":"resolved"}]`)
			e := f.call(f.startCommand(), 200)
			f.run()
			if state == "unknown" || state == "cancel_unknown" {
				f.lost["MSG"].Store(true)
			}
			f.run()
			event := cascadeOwnerEvents(t, f, "MSG")[0]
			original, _ := json.Marshal(event)
			if err := f.aut.AcceptAutomationFact(context.Background(), event); !errors.Is(err, errAutomationCascadePending) {
				t.Fatalf("premature intake: %v", err)
			}
			if state == "cancel_unknown" {
				f.cancel(e.ID)
			}
			if state == "manual" {
				if err := f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"priority": "low", "object_version": 3}).Error; err != nil {
					t.Fatal(err)
				}
			}
			restarted := f.aut
			f.aut = restarted
			parent := f.drain(e.ID)
			// The later CON priority fact follows the MSG event in action order.
			if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			child := bridgeExecution(t, f.autRunnerFixture)
			if state == "manual" {
				if child.Status != "failed" || child.Error != "target_version_stale" {
					t.Fatalf("manual rebase: %+v", child)
				}
			} else {
				want := uint(3)
				if state == "cancel_unknown" {
					want = 2
				}
				if child.TargetVersion != want || child.OriginExecutionID == nil || *child.OriginExecutionID != e.ID {
					t.Fatalf("child=%+v parent=%+v", child, parent)
				}
				f.drain(child.ID)
			}
			again, _ := json.Marshal(event)
			if string(again) != string(original) {
				t.Fatal("owner envelope mutated")
			}
			var receipt domain.LocalChangeJournal
			if err := f.aut.DB.Where("change_id = ?", automationFactReceipt(event)).First(&receipt).Error; err != nil {
				t.Fatal(err)
			}
			if receipt.BeforeState != string(original) {
				t.Fatal("original owner event not retained")
			}
			if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestAutomationCascadeMultipleMessagesAndSiblingRules(t *testing.T) {
	testAutomationCascadeMultipleMessages(t, false)
}
func TestPostgresAutomationCascadeMultipleMessagesAndSiblingRules(t *testing.T) {
	testAutomationCascadeMultipleMessages(t, true)
}
func testAutomationCascadeMultipleMessages(t *testing.T, pg bool) {
	f := newAutomationActionFixture(t, pg, `[{"action_name":"send_message","action_params":"one"},{"action_name":"private_note","action_params":"two"}]`)
	con, msg := cascadeSources(t, f)
	cascadeRule(t, f, "conversation_created", "[]", `[{"action_name":"send_message","action_params":"three"}]`)
	public := cascadeRule(t, f, "message_created", `[{"attribute_key":"private_note","filter_operator":"equal_to","values":[false]}]`, `[{"action_name":"change_priority","action_params":"urgent"}]`)
	private := cascadeRule(t, f, "message_created", `[{"attribute_key":"private_note","filter_operator":"equal_to","values":[true]}]`, `[{"action_name":"change_status","action_params":"resolved"}]`)
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	b := &AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{con, msg}}
	drainCascadeBridge(t, b)
	var messages []domain.Message
	f.owners["MSG"].DB.Order("id ASC").Find(&messages)
	if len(messages) != 3 || messages[0].Private || !messages[1].Private || messages[2].Private {
		t.Fatalf("messages=%+v", messages)
	}
	if c := f.conversationState(); c.ObjectVersion != 7 || c.Status != "resolved" || c.Priority != "urgent" {
		t.Fatalf("final=%+v", c)
	}
	var runs []domain.AutomationRuleExecution
	f.aut.DB.Where("event_name = ?", "message_created").Order("id ASC").Find(&runs)
	if len(runs) != 6 {
		t.Fatalf("runs=%+v", runs)
	}
	successes := map[uint]int{}
	for _, e := range runs {
		if e.Error == "target_version_stale" || e.Status == "loop_blocked" {
			t.Fatalf("arbitrary sibling staleness: %+v", e)
		}
		if e.Status == "succeeded" {
			successes[e.RuleID]++
		}
	}
	if successes[public.ID] != 2 || successes[private.ID] != 1 {
		t.Fatalf("distinctions=%v", successes)
	}
	before := len(runs)
	drainCascadeBridge(t, b)
	f.aut.DB.Where("event_name = ?", "message_created").Find(&runs)
	if len(runs) != before {
		t.Fatal("duplicate executions")
	}
	if err := validateRestoredAutomationSnapshot(f.aut.DB); err != nil {
		t.Fatal(err)
	}
}
func TestAutomationCascadeSiblingManualInterleaveCannotRebase(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"one"},{"action_name":"send_message","action_params":"two"}]`)
	cascadeRule(t, f, "message_created", "[]", `[{"action_name":"change_priority","action_params":"urgent"}]`)
	e := f.call(f.startCommand(), 200)
	f.drain(e.ID)
	events := cascadeOwnerEvents(t, f, "MSG")
	if err := f.aut.AcceptAutomationFact(context.Background(), events[1]); !errors.Is(err, errAutomationCascadePending) {
		t.Fatalf("later sibling accepted first: %v", err)
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), events[0]); err != nil {
		t.Fatal(err)
	}
	child := bridgeExecution(t, f.autRunnerFixture)
	f.drain(child.ID)
	if err := f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"object_version": 5, "priority": "low"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), events[1]); err != nil {
		t.Fatal(err)
	}
	child = bridgeExecution(t, f.autRunnerFixture)
	if child.Status != "failed" || child.Error != "target_version_stale" || child.TargetVersion != 4 {
		t.Fatalf("child=%+v", child)
	}
}
func TestAutomationCascadeReplayIdentityAndTenantBoundaries(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"one"}]`)
	cascadeRule(t, f, "message_created", "[]", "[]")
	e := f.call(f.startCommand(), 200)
	f.drain(e.ID)
	event := cascadeOwnerEvents(t, f, "MSG")[0]
	for _, field := range []string{"account", "conversation", "origin", "depth", "version", "message", "cause"} {
		var copy foundation.EventEnvelope
		raw, _ := json.Marshal(event)
		json.Unmarshal(raw, &copy)
		switch field {
		case "account":
			copy.AccountID = 92
		case "conversation":
			copy.Payload["conversation_id"] = 999
		case "origin":
			copy.Payload["origin_execution_id"] = 999
		case "depth":
			copy.Payload["automation_depth"] = 2
		case "version":
			copy.Payload["object_version"] = 99
		case "message":
			copy.Payload["message_id"] = 999
		case "cause":
			copy.CausationID = "wrong"
		}
		if err := f.aut.AcceptAutomationFact(context.Background(), copy); err == nil {
			t.Fatalf("accepted tampered %s", field)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- f.aut.AcceptAutomationFact(context.Background(), event) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("event_id = ?", event.EventID).Count(&n)
	if n != 1 {
		t.Fatalf("duplicate=%d", n)
	}
	copy := event
	copy.EventID += "-duplicate"
	if err := f.aut.AcceptAutomationFact(context.Background(), copy); !errors.Is(err, errAutomationCascadeUnverified) {
		t.Fatalf("alternative event ID was not safely quarantined: %v", err)
	}
}
func TestAutomationCascadeMutualRulesAndDepthBound(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"root"}]`)
	con, msg := cascadeSources(t, f)
	for i := 0; i < 4; i++ {
		cascadeRule(t, f, "message_created", "[]", fmt.Sprintf(`[{"action_name":"send_message","action_params":"reply-%d"}]`, i))
	}
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	b := &AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{con, msg}}
	drainCascadeBridge(t, b)
	var messages int64
	f.owners["MSG"].DB.Model(&domain.Message{}).Count(&messages)
	// root + four depth-1 actions + twelve depth-2 actions. Depth-3 and repeated
	// rules remain durable blocked outcomes with no new local message.
	if messages != 17 {
		t.Fatalf("unbounded/incorrect cascade messages=%d", messages)
	}
	var runs []domain.AutomationRuleExecution
	f.aut.DB.Order("id ASC").Find(&runs)
	blocked := 0
	for _, run := range runs {
		if run.AutomationDepth > 3 {
			t.Fatal("depth exceeded")
		}
		if run.AutomationDepth == 3 && run.Status != "loop_blocked" {
			t.Fatalf("depth3=%+v", run)
		}
		if run.Status == "loop_blocked" {
			blocked++
		}
	}
	if blocked == 0 {
		t.Fatal("mutual loop guard never fired")
	}
}
func TestAutomationCascadeSnapshotRestartAndReceiptCorruption(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"one"},{"action_name":"send_message","action_params":"two"}]`)
	cascadeRule(t, f, "message_created", "[]", `[{"action_name":"change_priority","action_params":"urgent"}]`)
	e := f.call(f.startCommand(), 200)
	f.drain(e.ID)
	events := cascadeOwnerEvents(t, f, "MSG")
	if err := f.aut.AcceptAutomationFact(context.Background(), events[0]); err != nil {
		t.Fatal(err)
	}
	child := bridgeExecution(t, f.autRunnerFixture)
	f.drain(child.ID)
	path := filepath.Join(t.TempDir(), "cascade-aut.json")
	if err := CreateSnapshot(f.aut.DB, "AUT", "cascade", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "AUT")
	if err := RestoreSnapshot(context.Background(), target, "AUT", "cascade", path, nil); err != nil {
		t.Fatal(err)
	}
	f.aut.DB = target
	if err := f.aut.AcceptAutomationFact(context.Background(), events[1]); err != nil {
		t.Fatal(err)
	}
	child = bridgeExecution(t, f.autRunnerFixture)
	if child.TargetVersion != 4 {
		t.Fatalf("restarted=%+v", child)
	}
	f.drain(child.ID)
	if err := target.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", automationCascadeClaim(events[0])).Update("after_state", "{}").Error; err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "corrupt-aut.json")
	if err := CreateSnapshot(target, "AUT", "corrupt", path); err != nil {
		t.Fatal(err)
	}
	empty := newModuleTestDB(t, "AUT")
	if err := RestoreSnapshot(context.Background(), empty, "AUT", "corrupt", path, nil); err == nil {
		t.Fatal("invalid causal claim restored")
	}
}

func TestAutomationCascadePageBoundaryAndDelayedSiblingAfterRestart(t *testing.T) {
	actions := make([]map[string]any, 0, 105)
	for i := 0; i < 105; i++ {
		actions = append(actions, map[string]any{"action_name": "send_message", "action_params": fmt.Sprintf("page-%d", i)})
	}
	raw, _ := json.Marshal(actions[:60])
	f := newAutomationActionFixture(t, false, string(raw))
	more, _ := json.Marshal(append(actions[60:], map[string]any{"action_name": "change_priority", "action_params": "urgent"}))
	cascadeRule(t, f, "conversation_created", "[]", string(more))
	con, msg := cascadeSources(t, f)
	cascadeRule(t, f, "message_created", "[]", "[]")
	rootFact := bridgeFact(f.autRunnerFixture, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), rootFact); err != nil {
		t.Fatal(err)
	}
	roots := causalExecutions(t, f.autRunnerFixture, rootFact.EventID)
	if len(roots) != 2 {
		t.Fatalf("setup roots=%d", len(roots))
	}
	root := roots[0]
	advanceCascadeSetup(t, f, roots[0].ID, 120)
	advanceCascadeSetup(t, f, roots[1].ID, 90)
	assertCascadeSetupCheckpoint(t, f, roots, 60, 105)
	if e := f.execution(root.ID); e.Status != "succeeded" {
		t.Fatalf("root=%+v", e)
	}
	events := cascadeOwnerEvents(t, f, "MSG")
	if len(events) != 105 {
		t.Fatalf("events=%d", len(events))
	}
	// The expected prior sibling set is reconstructed from dispatches even when
	// only the final event was received after a restart/source page change.
	restarted := f.aut
	f.aut = restarted
	if err := f.aut.AcceptAutomationFact(context.Background(), events[104]); !errors.Is(err, errAutomationCascadePending) {
		t.Fatalf("skipped missing page: %v", err)
	}
	page, err := msg.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(page) != 100 {
		t.Fatalf("page=%d err=%v", len(page), err)
	}
	// Leave the final origin action genuinely unknown so every first-page
	// descendant waits. An unrelated healthy runtime fact must still be reached.
	f.lost["CON"].Store(true)
	f.run()
	waiting := f.execution(roots[1].ID)
	assertCascadeFinalUnknown(t, f, roots[1].ID)
	if waiting.Status != "waiting" {
		t.Fatalf("origin not unknown: %+v", waiting)
	}
	if err := f.aut.DB.Model(&waiting).Update("runner_retry_at", time.Now().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	healthy := f.conversation
	healthy.ID = 0
	healthy.DisplayID = 2
	healthy.ObjectVersion = 1
	if err := f.con.Create(&healthy).Error; err != nil {
		t.Fatal(err)
	}
	rule := cascadeRule(t, f, "conversation_created", "[]", `[{"action_name":"send_message","action_params":"healthy after blocked page"}]`)
	command := f.startCommand()
	command.CommandID = "healthy-page-command"
	command.IdempotencyKey = command.CommandID
	command.Target.ID = strconv.Itoa(int(rule.ID))
	command.Input["conversation_id"] = healthy.ID
	command.Input["event_id"] = "healthy-page-fact"
	f.call(command, 200)
	b := &AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{con, msg}}
	drainCascadeBridge(t, b)
	var n int64
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("event_name = ? AND status = ?", "message_created", "succeeded").Count(&n)
	if n != 1 {
		t.Fatalf("healthy fact hidden behind blocked first page: %d", n)
	}
	f.drain(waiting.ID)
	drainCascadeBridge(t, b)
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("event_name = ? AND status = ?", "message_created", "succeeded").Count(&n)
	if n != 106 {
		t.Fatalf("page lost or collapsed equal-version sibling facts: %d", n)
	}
	if c := f.conversationState(); c.ObjectVersion != 107 {
		t.Fatalf("empty child actions changed CON: %+v", c)
	}
}

func TestPostgresAutomationCascadeConcurrentWorkers(t *testing.T) {
	f := newAutomationActionFixture(t, true, `[{"action_name":"send_message","action_params":"one"},{"action_name":"private_note","action_params":"two"}]`)
	con, msg := cascadeSources(t, f)
	cascadeRule(t, f, "message_created", "[]", `[{"action_name":"change_priority","action_params":"urgent"}]`)
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	b := &AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{con, msg}}
	errs := make(chan error, 6)
	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 12; i++ {
				if _, err := b.RunOnce(context.Background()); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	drainCascadeBridge(t, b)
	var n int64
	f.owners["MSG"].DB.Model(&domain.Message{}).Count(&n)
	if n != 2 {
		t.Fatalf("duplicate messages=%d", n)
	}
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("event_name = ? AND status = ?", "message_created", "succeeded").Count(&n)
	if n != 2 {
		t.Fatalf("duplicate/lost executions=%d", n)
	}
	if c := f.conversationState(); c.ObjectVersion != 5 {
		t.Fatalf("concurrent version=%d", c.ObjectVersion)
	}
}

func TestAutomationCascadeWaitsForCancelledOriginSibling(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"one"}]`)
	cascadeRule(t, f, "conversation_created", "[]", `[{"action_name":"change_priority","action_params":"urgent"}]`)
	cascadeRule(t, f, "message_created", "[]", `[{"action_name":"change_status","action_params":"resolved"}]`)
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	f.run()
	f.run()
	message := cascadeOwnerEvents(t, f, "MSG")[0]
	if err := f.aut.AcceptAutomationFact(context.Background(), message); !errors.Is(err, errAutomationCascadePending) {
		t.Fatalf("skipped active origin sibling: %v", err)
	}
	f.cancel(rows[1].ID)
	if err := f.aut.AcceptAutomationFact(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	child := bridgeExecution(t, f.autRunnerFixture)
	f.drain(child.ID)
	if c := f.conversationState(); c.ObjectVersion != 3 || c.Priority != "medium" || c.Status != "resolved" {
		t.Fatalf("cancelled sibling proof=%+v", c)
	}
}

func TestAutomationCascadeManifestRejectsMissingFinalSibling(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"one"}]`)
	cascadeRule(t, f, "conversation_created", "[]", `[{"action_name":"send_message","action_params":"two"}]`)
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	for _, e := range rows {
		f.drain(e.ID)
	}
	if err := f.aut.DB.Delete(&rows[1]).Error; err != nil {
		t.Fatal(err)
	}
	message := cascadeOwnerEvents(t, f, "MSG")[0]
	if err := f.aut.AcceptAutomationFact(context.Background(), message); !errors.Is(err, errAutomationCascadeUnverified) {
		t.Fatalf("missing final sibling went unnoticed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "missing-tail.json")
	if err := CreateSnapshot(f.aut.DB, "AUT", "missing-tail", path); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSnapshot(context.Background(), newModuleTestDB(t, "AUT"), "AUT", "missing-tail", path, nil); err == nil {
		t.Fatal("incomplete batch restored")
	}
}

func TestAutomationCascadeRestoresUnknownOwnerSnapshots(t *testing.T) {
	testAutomationCascadeOwnerRestore(t, false)
}
func TestPostgresAutomationCascadeRestoresUnknownOwnerSnapshots(t *testing.T) {
	testAutomationCascadeOwnerRestore(t, true)
}
func testAutomationCascadeOwnerRestore(t *testing.T, pg bool) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			actions := `[{"action_name":"private_note","action_params":"restore note"},{"action_name":"change_priority","action_params":"urgent"}]`
			source := newAutomationActionFixture(t, pg, actions)
			cascadeRule(t, source, "message_created", "[]", `[{"action_name":"change_status","action_params":"resolved"}]`)
			parent := source.call(source.startCommand(), 200)
			source.run()
			source.lost["MSG"].Store(true)
			source.run()
			if e := source.execution(parent.ID); e.Status != "waiting" {
				t.Fatalf("parent=%+v", e)
			}
			if cancel {
				source.cancel(parent.ID)
			}
			original := cascadeOwnerEvents(t, source, "MSG")[0]
			if err := source.aut.AcceptAutomationFact(context.Background(), original); !errors.Is(err, errAutomationCascadePending) {
				t.Fatalf("unknown child intake=%v", err)
			}
			restored := newAutomationActionFixture(t, pg, actions)
			for _, owner := range []string{"IAM", "TEN", "CUS", "CON", "MSG", "AUT"} {
				path := filepath.Join(t.TempDir(), owner+".json")
				if err := CreateSnapshot(source.owners[owner].DB, owner, "cascade-unknown", path); err != nil {
					t.Fatal(err)
				}
				if err := RestoreSnapshot(context.Background(), restored.owners[owner].DB, owner, "cascade-unknown", path, nil); err != nil {
					t.Fatalf("restore %s: %v", owner, err)
				}
			}
			con, msg := cascadeSources(t, restored)
			restored.drain(parent.ID)
			b := &AutomationBridge{Runtime: &restored.aut, Sources: []AutomationFactSource{con, msg}}
			drainCascadeBridge(t, b)
			want := 4
			if cancel {
				want = 3
			}
			if c := restored.conversationState(); c.ObjectVersion != want || c.Status != "resolved" {
				t.Fatalf("restored CON=%+v", c)
			}
			var messages int64
			restored.owners["MSG"].DB.Model(&domain.Message{}).Count(&messages)
			if messages != 1 {
				t.Fatalf("replayed messages=%d", messages)
			}
			rows := causalExecutions(t, restored.autRunnerFixture, original.EventID)
			if len(rows) != 1 || rows[0].Status != "succeeded" {
				t.Fatalf("children=%+v", rows)
			}
			if err := restored.aut.AcceptAutomationFact(context.Background(), original); err != nil {
				t.Fatal(err)
			}
		})
	}
}
