package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
)

func bridgeFact(f *autRunnerFixture, eventName string) foundation.EventEnvelope {
	eventType := "con.conversation.created.v1"
	if eventName == "conversation_updated" {
		eventType = "con.conversation.changed.v1"
	}
	now := time.Now().UTC()
	return foundation.EventEnvelope{EventID: "ordinary-fact-1", EventType: eventType, EventVersion: "v1", SourceModule: "CON", AccountID: 91, AggregateType: "Conversation", AggregateID: strconv.Itoa(int(f.conversation.ID)), AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "ordinary-api", AuthSource: "module"}, CorrelationID: "ordinary-correlation", CausationID: "ordinary-request", ChangedFields: []string{"created"}, Payload: map[string]any{"conversation_id": f.conversation.ID, "object_version": 1}, DataClassification: "internal"}
}
func bridgeExecution(t *testing.T, f *autRunnerFixture) domain.AutomationRuleExecution {
	t.Helper()
	var e domain.AutomationRuleExecution
	if err := f.aut.DB.Order("id DESC").First(&e).Error; err != nil {
		t.Fatal(err)
	}
	return e
}
func TestAutomationBridgeDurableAdmissionAndFrozenRunner(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_status","action_params":["resolved"]},{"action_name":"change_priority","action_params":["urgent"]}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	e := bridgeExecution(t, f)
	if e.Status != "received" || e.RuleVersion != 1 {
		t.Fatalf("execution=%+v", e)
	}
	f.assertTarget("open", "medium", 1)
	// The immutable execution survives a rule edit and a new runtime value.
	svc := service.NewAutomationService(f.aut.DB, nil, nil)
	if err := svc.UpdateRule(&f.rule, map[string]any{"actions": `[{"action_name":"change_status","action_params":["pending"]}]`}); err != nil {
		t.Fatal(err)
	}
	restarted := f.aut
	if _, err := restarted.RunAutomationOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RunAutomationOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertTarget("resolved", "urgent", 3)
	if err := restarted.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var count int64
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Count(&count)
	if count != 1 {
		t.Fatalf("duplicate executions=%d", count)
	}
	if e = f.execution(e.ID); e.Status != "succeeded" || e.RuleVersion != 1 {
		t.Fatalf("execution=%+v", e)
	}
}
func TestAutomationBridgeOldFactNeverUsesEditedRule(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_status","action_params":["resolved"]}]`)
	event := bridgeFact(f, "conversation_created")
	svc := service.NewAutomationService(f.aut.DB, nil, nil)
	if err := svc.UpdateRule(&f.rule, map[string]any{"actions": `[{"action_name":"change_status","action_params":["pending"]}]`}); err != nil {
		t.Fatal(err)
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	e := bridgeExecution(t, f)
	if e.Status != "failed" || e.Error != "rule_changed_before_dispatch" || e.RuleVersion != 1 {
		t.Fatalf("execution=%+v", e)
	}
	if ran, err := f.aut.RunAutomationOnce(context.Background()); err != nil || ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	f.assertTarget("open", "medium", 1)
}
func TestAutomationBridgeCancelledBeforeAction(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	if err := f.aut.AcceptAutomationFact(context.Background(), bridgeFact(f, "conversation_created")); err != nil {
		t.Fatal(err)
	}
	e := bridgeExecution(t, f)
	f.cancel(e.ID)
	if ran, err := f.aut.RunAutomationOnce(context.Background()); err != nil || ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if e = f.execution(e.ID); e.Status != "cancelled" {
		t.Fatalf("execution=%+v", e)
	}
	f.assertTarget("open", "medium", 1)
}
func TestAutomationBridgePersistedLoopAncestry(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	for _, model := range []any{&domain.AutomationRule{}, &domain.AutomationRuleVersion{}} {
		if err := f.aut.DB.Model(model).Where("account_id = ?", 91).Update("event_name", "conversation_updated").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), bridgeFact(f, "conversation_updated")); err != nil {
		t.Fatal(err)
	}
	f.run()
	var journal domain.LocalChangeJournal
	if err := f.con.Where("source_module = ? AND action = ?", "CON", "event_outbox").First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	var caused foundation.EventEnvelope
	if err := json.Unmarshal([]byte(journal.AfterState), &caused); err != nil {
		t.Fatal(err)
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), caused); err != nil {
		t.Fatal(err)
	}
	e := bridgeExecution(t, f)
	if e.Status != "loop_blocked" || e.OriginExecutionID == nil || e.AutomationDepth != 1 {
		t.Fatalf("execution=%+v", e)
	}
	f.assertTarget("open", "urgent", 2)
}
func TestAutomationBridgeRejectsForeignTarget(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_status","action_params":["resolved"]}]`)
	if err := f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("account_id", 92).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), bridgeFact(f, "conversation_created")); err != nil {
		t.Fatal(err)
	}
	e := bridgeExecution(t, f)
	if e.Status != "failed" || e.Error != "target_unavailable" {
		t.Fatalf("execution=%+v", e)
	}
	f.assertTarget("open", "medium", 1)
}
func TestAutomationBridgeUnsupportedDefinitionFailsExplicitly(t *testing.T) {
	for _, actions := range []string{`[{"action_name":"send_webhook","action_params":["never send"]}]`, `[{"action_name":"change_status","action_params":["resolved"]}]`} {
		t.Run(fmt.Sprint(len(actions)), func(t *testing.T) {
			f := newAUTRunnerFixture(t, false, actions)
			expected := "action_capability_executor_unavailable"
			if actions == `[{"action_name":"change_status","action_params":["resolved"]}]` {
				expected = "invalid_frozen_definition"
				if err := f.aut.DB.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", f.rule.ID).Update("conditions", `{"conditions":[]}`).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := f.aut.AcceptAutomationFact(context.Background(), bridgeFact(f, "conversation_created")); err != nil {
				t.Fatal(err)
			}
			if expected == "action_capability_executor_unavailable" {
				f.run()
			}
			e := bridgeExecution(t, f)
			if e.Status != "failed" || e.Error != expected {
				t.Fatalf("execution=%+v expected=%s", e, expected)
			}
			f.assertTarget("open", "medium", 1)
		})
	}
}

func TestAutomationModeClaimRejectsMixedInstances(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	if err := ClaimAutomationMode(context.Background(), db, "durable"); err != nil {
		t.Fatal(err)
	}
	if err := ClaimAutomationMode(context.Background(), db, "durable"); err != nil {
		t.Fatal(err)
	}
	if err := ClaimAutomationMode(context.Background(), db, "legacy"); err == nil {
		t.Fatal("mixed executors accepted")
	}
}
func TestAutomationBridgeFutureFactRejected(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[]`)
	event := bridgeFact(f, "conversation_created")
	event.OccurredAt = time.Now().Add(time.Hour)
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err == nil {
		t.Fatal("future fact accepted")
	}
	var count int64
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Count(&count)
	if count != 0 {
		t.Fatal("future fact created run")
	}
}

func TestAutomationBridgeRejectsStaleOwnerFact(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_status","action_params":["resolved"]}]`)
	event := bridgeFact(f, "conversation_created")
	if err := f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"status": "pending", "object_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	e := bridgeExecution(t, f)
	if e.Status != "failed" || e.Error != "target_version_stale" || e.TargetVersion != 1 {
		t.Fatalf("execution=%+v", e)
	}
	f.assertTarget("pending", "medium", 2)
}
func TestAutomationBridgeMultipleRulesFollowCausalOwnerVersion(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_status","action_params":["resolved"]}]`)
	svc := service.NewAutomationService(f.aut.DB, nil, nil)
	second := domain.AutomationRule{AccountID: 91, Name: "Second", EventName: "conversation_created", Conditions: "[]", Actions: `[{"action_name":"change_priority","action_params":["urgent"]}]`, Active: true}
	if err := svc.CreateRule(&second); err != nil {
		t.Fatal(err)
	}
	if err := f.aut.AcceptAutomationFact(context.Background(), bridgeFact(f, "conversation_created")); err != nil {
		t.Fatal(err)
	}
	f.run()
	f.run()
	f.assertTarget("resolved", "urgent", 3)
	var e domain.AutomationRuleExecution
	if err := f.aut.DB.Where("rule_id = ?", second.ID).First(&e).Error; err != nil {
		t.Fatal(err)
	}
	if e.Status != "succeeded" || e.RunnerTargetVersion != 3 || e.TargetVersion != 1 || e.RunnerPredecessorID == nil || e.FactBatchKey == "" {
		t.Fatalf("execution=%+v", e)
	}
}
func TestAutomationBridgePostgresConcurrentIntake(t *testing.T) {
	f := newAUTRunnerFixture(t, true, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	event := bridgeFact(f, "conversation_created")
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { replica := f.aut; errs <- replica.AcceptAutomationFact(context.Background(), event) }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := f.aut.DB.Model(&domain.AutomationRuleExecution{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("executions=%d", count)
	}
	for i := 0; i < 8; i++ {
		go func() { replica := f.aut; _, err := replica.RunAutomationOnce(context.Background()); errs <- err }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	f.assertTarget("open", "urgent", 2)
	if e := bridgeExecution(t, f); e.Status != "succeeded" {
		t.Fatalf("execution=%+v", e)
	}
}
func TestAutomationBridgeCancelInflightUsesSharedLifecycle(t *testing.T) {
	f := newAUTRunnerFixture(t, false, localTwoActions)
	if err := f.aut.AcceptAutomationFact(context.Background(), bridgeFact(f, "conversation_created")); err != nil {
		t.Fatal(err)
	}
	e := bridgeExecution(t, f)
	f.block.Store(true)
	done := make(chan error, 1)
	go func() { _, err := f.aut.RunAutomationOnce(context.Background()); done <- err }()
	select {
	case <-f.committed:
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not commit")
	}
	b := AutomationBridge{Runtime: &f.aut}
	if _, err := b.CancelAutomation(context.Background(), 91, e.ID); err != nil {
		t.Fatal(err)
	}
	close(f.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.assertTarget("open", "high", 2)
	if e = f.execution(e.ID); e.Status != "cancelled" || !e.CancelRequested {
		t.Fatalf("execution=%+v", e)
	}
}

type retryingCreationWorkflowSource struct{ failure error }

func (s *retryingCreationWorkflowSource) ReconcileCreationWorkflows(context.Context, int) (int, error) {
	return 0, s.failure
}
func (*retryingCreationWorkflowSource) PendingAutomationFacts(context.Context, int) ([]foundation.EventEnvelope, error) {
	return nil, nil
}
func (*retryingCreationWorkflowSource) MarkAutomationFactDelivered(context.Context, string) error {
	return nil
}

func TestAutomationBridgeWorkflowRetryDoesNotStarveAdmittedActions(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	e := f.call(f.startCommand(), 200)
	unavailable := fmt.Errorf("planned MSG owner temporarily unavailable")
	bridge := AutomationBridge{Runtime: &f.aut, Workflow: &retryingCreationWorkflowSource{failure: unavailable}}
	count, err := bridge.RunOnce(context.Background())
	if err != unavailable || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	f.assertTarget("resolved", "medium", 2)
	if got := f.execution(e.ID); got.Status != "succeeded" {
		t.Fatalf("execution=%+v", got)
	}
}
