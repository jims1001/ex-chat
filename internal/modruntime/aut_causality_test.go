package modruntime

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
)

func addCausalRule(t *testing.T, f *autRunnerFixture, actions string) domain.AutomationRule {
	t.Helper()
	r := domain.AutomationRule{AccountID: 91, Name: "Ordered rule", EventName: "conversation_created", Conditions: "[]", Actions: actions, Active: true}
	if err := service.NewAutomationService(f.aut.DB, nil, nil).CreateRule(&r); err != nil {
		t.Fatal(err)
	}
	return r
}
func causalExecutions(t *testing.T, f *autRunnerFixture, eventID string) []domain.AutomationRuleExecution {
	t.Helper()
	var rows []domain.AutomationRuleExecution
	if err := f.aut.DB.Where("event_id = ?", eventID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAutomationCausalChainCarriesNoOpAndUnsupportedRules(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	addCausalRule(t, f, `[]`)
	addCausalRule(t, f, `[{"action_name":"send_webhook","action_params":["never"]}]`)
	addCausalRule(t, f, `[{"action_name":"open_conversation"}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	f.run()
	restarted := f.aut
	if ran, err := restarted.RunAutomationOnce(context.Background()); err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	f.run() // Explicit unsupported logical result is checkpointed before its successor
	f.assertTarget("open", "medium", 3)
	rows := causalExecutions(t, f, event.EventID)
	if len(rows) != 4 {
		t.Fatalf("rows=%+v", rows)
	}
	for i, want := range []string{"succeeded", "succeeded", "failed", "succeeded"} {
		if rows[i].Status != want || rows[i].TargetVersion != 1 {
			t.Fatalf("rows=%+v", rows)
		}
		if i > 0 && (rows[i].RunnerPredecessorID == nil || *rows[i].RunnerPredecessorID != rows[i-1].ID) {
			t.Fatalf("broken chain %+v", rows)
		}
	}
}

func TestAutomationCausalChainQueuedCancellationCarriesEarlierEffects(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	addCausalRule(t, f, `[{"action_name":"change_priority","action_params":["low"]}]`)
	addCausalRule(t, f, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rows := causalExecutions(t, f, event.EventID)
	f.cancel(rows[1].ID)
	f.run()
	f.run()
	f.assertTarget("resolved", "urgent", 3)
	rows = causalExecutions(t, f, event.EventID)
	if rows[1].Status != "cancelled" || rows[2].Status != "succeeded" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestAutomationCausalChainCancellationAfterPartialEffects(t *testing.T) {
	f := newAUTRunnerFixture(t, false, localTwoActions)
	addCausalRule(t, f, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	first := causalExecutions(t, f, event.EventID)[0]
	f.run()
	f.cancel(first.ID)
	f.run()
	f.assertTarget("open", "urgent", 3)
	rows := causalExecutions(t, f, event.EventID)
	if rows[0].Status != "cancelled" || rows[0].RunnerNextAction != 1 || rows[1].Status != "succeeded" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestAutomationCausalChainManualInterleavingNeverRebases(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	addCausalRule(t, f, `[{"action_name":"open_conversation"}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	f.run()
	if err := f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"status": "pending", "object_version": 3}).Error; err != nil {
		t.Fatal(err)
	}
	f.run()
	f.assertTarget("pending", "medium", 3)
	rows := causalExecutions(t, f, event.EventID)
	if rows[1].Status != "failed" || rows[1].Error != "owner_command_rejected" || rows[1].RunnerTargetVersion != 2 {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestAutomationCausalChainUnknownPredecessorDoesNotStarveOtherFacts(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	addCausalRule(t, f, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	f.lost.Store(true)
	f.run()
	f.assertTarget("resolved", "medium", 2)
	rows := causalExecutions(t, f, event.EventID)
	if len(rows) != 2 || rows[0].Status != "waiting" || rows[1].Status != "received" || f.lost.Load() {
		t.Fatalf("lost-response predecessor fixture was not established: rows=%+v fault_pending=%v", rows, f.lost.Load())
	}
	// Keep this receipt ineligible until the explicit recovery step below. The
	// unrelated work must not race the runner's one-second retry backoff.
	retryAfter := time.Now().UTC().Add(time.Hour)
	if deadline, ok := t.Deadline(); ok {
		retryAfter = deadline.UTC().Add(time.Hour)
	}
	update := f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ? AND status = ?", rows[0].ID, "waiting").Update("runner_retry_at", retryAfter)
	if update.Error != nil || update.RowsAffected != 1 {
		t.Fatalf("hold unknown predecessor: rows=%d err=%v", update.RowsAffected, update.Error)
	}
	// A different conversation/fact remains runnable while the first receipt is unknown.
	other := f.conversation
	other.ID = 0
	other.DisplayID = 2
	if err := f.con.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	next := event
	next.EventID = "unrelated-fact"
	next.AggregateID = strconv.Itoa(int(other.ID))
	next.Payload = map[string]any{"conversation_id": other.ID, "object_version": 1}
	if err := f.aut.AcceptAutomationFact(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	f.run()
	f.run()
	if rows := causalExecutions(t, f, next.EventID); len(rows) != 2 || rows[1].Status != "succeeded" {
		t.Fatalf("other rows=%+v", rows)
	}
	rows = causalExecutions(t, f, event.EventID)
	if len(rows) != 2 || rows[0].Status != "waiting" || rows[1].Status != "received" {
		t.Fatalf("rows=%+v", rows)
	}
	if err := f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[0].ID).Update("runner_retry_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	restarted := f.aut
	for i := 0; i < 2; i++ {
		if ran, err := restarted.RunAutomationOnce(context.Background()); err != nil || !ran {
			t.Fatalf("restart ran=%v err=%v", ran, err)
		}
	}
	f.assertTarget("resolved", "urgent", 3)
	if err := restarted.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if rows := causalExecutions(t, f, event.EventID); len(rows) != 2 || rows[1].Status != "succeeded" {
		t.Fatalf("replay=%+v", rows)
	}
}

func TestAutomationCausalChainCorruptionAndTenantBoundary(t *testing.T) {
	for _, mutation := range []string{"foreign_predecessor", "quarantined", "forged_version", "wrong_target"} {
		t.Run(mutation, func(t *testing.T) {
			f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			addCausalRule(t, f, `[{"action_name":"open_conversation"}]`)
			event := bridgeFact(f, "conversation_created")
			if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			f.run()
			rows := causalExecutions(t, f, event.EventID)
			var changes map[string]any
			target := rows[0].ID
			switch mutation {
			case "foreign_predecessor":
				changes = map[string]any{"account_id": 92}
			case "quarantined":
				changes = map[string]any{"status": "manual_action_required"}
			case "forged_version":
				changes = map[string]any{"runner_target_version": 99}
			case "wrong_target":
				changes = map[string]any{"conversation_id": rows[0].ConversationID + 100}
			}
			if err := f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", target).Updates(changes).Error; err != nil {
				t.Fatal(err)
			}
			f.run()
			f.assertTarget("resolved", "medium", 2)
			successor := f.execution(rows[1].ID)
			if successor.Status != "manual_action_required" || successor.Error != "causal_predecessor_unverified" {
				t.Fatalf("successor=%+v", successor)
			}
		})
	}
}

func TestAutomationCausalChainPostgresSerializesWorkers(t *testing.T) {
	f := newAUTRunnerFixture(t, true, `[{"action_name":"resolve_conversation"}]`)
	addCausalRule(t, f, `[{"action_name":"open_conversation"}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	f.block.Store(true)
	first := make(chan error, 1)
	go func() { _, err := f.aut.RunAutomationOnce(context.Background()); first <- err }()
	select {
	case <-f.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("first owner did not commit")
	}
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			replica := f.aut
			ran, err := replica.RunAutomationOnce(context.Background())
			if ran && err == nil {
				err = fmt.Errorf("successor ran before causal receipt checkpoint")
			}
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	close(f.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		go func() { replica := f.aut; _, err := replica.RunAutomationOnce(context.Background()); errs <- err }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	f.assertTarget("open", "medium", 3)
	if rows := causalExecutions(t, f, event.EventID); len(rows) != 2 || rows[1].Status != "succeeded" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestAutomationCausalChainRequiresDurableIntakeReceipt(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := f.aut.DB.Where("source_module = ? AND action = ?", "AUT", "automation_fact_received").Delete(&domain.LocalChangeJournal{}).Error; err != nil {
		t.Fatal(err)
	}
	f.run()
	f.assertTarget("open", "medium", 1)
	e := bridgeExecution(t, f)
	if e.Status != "manual_action_required" || e.Error != "causal_predecessor_unverified" {
		t.Fatalf("e=%+v", e)
	}
}

func TestAutomationCausalChainCannotBeInjectedThroughPublicInput(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	command := f.startCommand()
	command.Input["fact_batch_key"] = "attacker-batch"
	command.Input["runner_predecessor_id"] = uint(987)
	command.Input["runner_target_version"] = uint(999)
	e := f.call(command, 200)
	stored := f.execution(e.ID)
	if stored.FactBatchKey != "" || stored.RunnerPredecessorID != nil || stored.RunnerTargetVersion != 1 {
		t.Fatalf("public input changed private checkpoint: %+v", stored)
	}
	f.run()
	f.assertTarget("resolved", "medium", 2)
}

func TestAutomationCausalChainMissingRunnerContextIsQuarantined(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	addCausalRule(t, f, `[{"action_name":"open_conversation"}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	rows := causalExecutions(t, f, event.EventID)
	if err := f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[0].ID).Update("runner_context", "").Error; err != nil {
		t.Fatal(err)
	}
	f.run()
	f.run()
	f.assertTarget("open", "medium", 1)
	rows = causalExecutions(t, f, event.EventID)
	if rows[0].Status != "manual_action_required" || rows[0].Error != "invalid_runner_checkpoint" || rows[1].Status != "manual_action_required" {
		t.Fatalf("rows=%+v", rows)
	}
}
