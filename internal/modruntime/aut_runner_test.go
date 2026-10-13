package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type autRunnerFixture struct {
	t            *testing.T
	aut          Runtime
	con          *gorm.DB
	rule         domain.AutomationRule
	conversation domain.Conversation
	lost         atomic.Bool
	block        atomic.Bool
	committed    chan struct{}
	release      chan struct{}
}

func autRunnerCONConfig() Config {
	cfg := businessConfig("CON")
	// These tests exercise durable AUT semantics, not a short owner deadline.
	// Match the existing AUT client and multi-owner fixture's healthy-path budget;
	// lost responses and in-flight actions are injected explicitly below.
	cfg.MaxTimeout = 2 * time.Second
	cfg.AllowedCallers["AUT"] = true
	return cfg
}

func newAUTRunnerFixture(t *testing.T, pg bool, actions string) *autRunnerFixture {
	t.Helper()
	createDB := newModuleTestDB
	if pg {
		createDB = postgresModuleDB
	}
	autDB, conDB := createDB(t, "AUT"), createDB(t, "CON")
	f := &autRunnerFixture{t: t, con: conDB, committed: make(chan struct{}), release: make(chan struct{})}
	cfg := autRunnerCONConfig()
	con := Runtime{Config: cfg, DB: conDB, Contracts: NewContractRegistry("CON")}
	if err := con.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	handler := con.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if strings.Contains(r.URL.Path, "/commands/") && rec.Code == 200 {
			if f.block.Swap(false) {
				close(f.committed)
				<-f.release
			}
			if f.lost.Swap(false) {
				w.WriteHeader(503)
				return
			}
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	cfg = businessConfig("AUT")
	cfg.MaxTimeout = 2 * time.Second
	cfg.EnableBusiness = true
	cfg.Dependencies["CON"] = DependencyConfig{URL: server.URL, Token: con.Config.AuthToken}
	f.aut = Runtime{Config: cfg, DB: autDB, Contracts: NewContractRegistry("AUT")}
	if err := f.aut.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	f.conversation = domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: "open", Priority: "medium", ObjectVersion: 1, CreatedAt: now, UpdatedAt: now, LastActivityAt: now}
	if err := conDB.Create(&f.conversation).Error; err != nil {
		t.Fatal(err)
	}
	f.rule = domain.AutomationRule{AccountID: 91, Name: "Local actions", EventName: "conversation_created", Conditions: "[]", Actions: actions, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := autDB.Create(&f.rule).Error; err != nil {
		t.Fatal(err)
	}
	v := domain.AutomationRuleVersion{AccountID: 91, RuleID: f.rule.ID, Version: 1, EventName: f.rule.EventName, Conditions: "[]", Actions: actions, CreatedAt: now}
	if err := autDB.Create(&v).Error; err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *autRunnerFixture) startCommand() foundation.CommandEnvelope {
	return foundation.CommandEnvelope{CommandID: "start", CommandType: "aut.execution.start.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "rule", ID: strconv.Itoa(int(f.rule.ID))}, Input: map[string]any{"event_id": "fact-1", "event_name": f.rule.EventName, "conversation_id": f.conversation.ID, "target_version": 1}, IdempotencyKey: "start-local", CorrelationID: "local-correlation", CausationID: "fact-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "aut.execution.get.v1"}}
}
func (f *autRunnerFixture) call(command foundation.CommandEnvelope, code int) domain.AutomationRuleExecution {
	f.t.Helper()
	response := performContractRequest(f.aut.Handler(), "/contracts/commands/"+command.CommandType, command, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.aut.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		r.Header.Set(headerCorrelationID, command.CorrelationID)
	})
	if response.Code != code {
		f.t.Fatalf("command=%s code=%d body=%s", command.CommandType, response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			ID              uint   `json:"id"`
			Status          string `json:"status"`
			CancelRequested bool   `json:"cancel_requested"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		f.t.Fatal(err)
	}
	return domain.AutomationRuleExecution{ID: envelope.Data.ID, Status: envelope.Data.Status, CancelRequested: envelope.Data.CancelRequested}
}
func (f *autRunnerFixture) execution(id uint) domain.AutomationRuleExecution {
	f.t.Helper()
	var e domain.AutomationRuleExecution
	if err := f.aut.DB.First(&e, id).Error; err != nil {
		f.t.Fatal(err)
	}
	return e
}
func (f *autRunnerFixture) assertTarget(status, priority string, version int) {
	f.t.Helper()
	var c domain.Conversation
	if err := f.con.First(&c, f.conversation.ID).Error; err != nil {
		f.t.Fatal(err)
	}
	if c.Status != status || c.Priority != priority || c.ObjectVersion != version {
		f.t.Fatalf("target=%s/%s/v%d wanted %s/%s/v%d", c.Status, c.Priority, c.ObjectVersion, status, priority, version)
	}
}
func (f *autRunnerFixture) run() {
	f.t.Helper()
	ran, err := f.aut.RunAutomationOnce(context.Background())
	if err != nil || !ran {
		f.t.Fatalf("run=%v err=%v", ran, err)
	}
}
func (f *autRunnerFixture) cancel(id uint) domain.AutomationRuleExecution {
	c := f.startCommand()
	c.CommandID = "cancel"
	c.IdempotencyKey = "cancel-local"
	c.CommandType = "aut.execution.cancel.v1"
	c.Target = foundation.ContractTarget{Type: "execution", ID: strconv.Itoa(int(id))}
	c.Input = map[string]any{}
	return f.call(c, 200)
}

const localTwoActions = `[{"action_name":"change_priority","action_params":{"priority":"high"}},{"action_name":"resolve_conversation"}]`

func TestAUTRunnerChangesRealOwnerStateAndFreezesPlan(t *testing.T) {
	f := newAUTRunnerFixture(t, false, localTwoActions)
	start := f.startCommand()
	e := f.call(start, 200)
	if e.Status != "received" {
		t.Fatalf("status=%s", e.Status)
	}
	f.assertTarget("open", "medium", 1)
	// Mutating the rule and publishing a new version cannot replace the queued plan.
	f.aut.DB.Model(&domain.AutomationRule{}).Where("id = ?", f.rule.ID).Update("actions", `[{"action_name":"change_priority","action_params":{"priority":"low"}}]`)
	f.aut.DB.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: f.rule.ID, Version: 2, EventName: f.rule.EventName, Conditions: "[]", Actions: "[]", CreatedAt: time.Now().UTC()})
	f.run()
	f.assertTarget("open", "high", 2)
	f.run()
	f.assertTarget("resolved", "high", 3)
	stored := f.execution(e.ID)
	if stored.Status != "succeeded" || stored.RuleVersion != 1 || !strings.Contains(stored.ActionResults, `"object_version":3`) {
		t.Fatalf("execution=%+v", stored)
	}
	f.call(start, 200)
	if ran, err := f.aut.RunAutomationOnce(context.Background()); ran || err != nil {
		t.Fatalf("duplicate run=%v/%v", ran, err)
	}
	f.assertTarget("resolved", "high", 3)
	var ownerReceipts, ownerEvents, autEvents int64
	f.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&ownerReceipts)
	f.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&ownerEvents)
	f.aut.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&autEvents)
	if ownerReceipts != 2 || ownerEvents != 2 || autEvents != 1 {
		t.Fatalf("receipts/events=%d/%d/%d", ownerReceipts, ownerEvents, autEvents)
	}
	var events []domain.LocalChangeJournal
	f.con.Where("action = ?", "event_outbox").Find(&events)
	for _, row := range events {
		var event foundation.EventEnvelope
		json.Unmarshal([]byte(row.AfterState), &event)
		origin, _ := uintFilter(event.Payload, "origin_execution_id")
		depth, _ := uintFilter(event.Payload, "automation_depth")
		if origin != e.ID || depth != 1 {
			t.Fatalf("missing causal origin: %s", row.AfterState)
		}
	}
	if f.aut.DB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("AUT created foreign owner table")
	}
}
func TestAUTRunnerUnsupportedActionPreservesSupportedEffects(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":{"priority":"high"}},{"action_name":"send_webhook","action_params":{"content":"never send"}}]`)
	e := f.call(f.startCommand(), 200)
	if e.Status != "received" {
		t.Fatalf("status=%s", e.Status)
	}
	f.run()
	f.run()
	f.assertTarget("open", "high", 2)
	final := f.execution(e.ID)
	if final.Status != "partially_succeeded" || final.Error != "action_capability_executor_unavailable" {
		t.Fatalf("unsupported logical action=%+v", final)
	}
	if ran, err := f.aut.RunAutomationOnce(context.Background()); ran || err != nil {
		t.Fatalf("terminal ran=%v/%v", ran, err)
	}
}

func TestAUTRunnerCancellationBeforeAndDuringAction(t *testing.T) {
	t.Run("queued", func(t *testing.T) {
		f := newAUTRunnerFixture(t, false, localTwoActions)
		e := f.call(f.startCommand(), 200)
		if f.cancel(e.ID).Status != "cancelled" {
			t.Fatal("cancel failed")
		}
		if ran, err := f.aut.RunAutomationOnce(context.Background()); ran || err != nil {
			t.Fatalf("ran=%v/%v", ran, err)
		}
		f.assertTarget("open", "medium", 1)
	})
	t.Run("inflight", func(t *testing.T) {
		f := newAUTRunnerFixture(t, false, localTwoActions)
		e := f.call(f.startCommand(), 200)
		f.block.Store(true)
		done := make(chan error, 1)
		go func() { _, err := f.aut.RunAutomationOnce(context.Background()); done <- err }()
		select {
		case <-f.committed:
		case <-time.After(3 * time.Second):
			t.Fatal("owner did not commit")
		}
		response := f.cancel(e.ID)
		if !response.CancelRequested || response.Status != "running" {
			t.Fatalf("cancellation=%+v", response)
		}
		close(f.release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		f.assertTarget("open", "high", 2)
		if stored := f.execution(e.ID); stored.Status != "cancelled" || !strings.Contains(stored.ActionResults, `"status":"skipped"`) {
			t.Fatalf("execution=%+v", stored)
		}
	})
}
func TestAUTRunnerReconcilesLostOwnerResponseAfterRestart(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	e := f.call(f.startCommand(), 200)
	f.lost.Store(true)
	f.run()
	f.assertTarget("open", "urgent", 2)
	if stored := f.execution(e.ID); stored.Status != "waiting" || !strings.Contains(stored.ActionResults, "result_unknown") {
		t.Fatalf("unknown=%+v", stored)
	}
	past := time.Now().UTC().Add(-time.Minute)
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_retry_at", past)
	// Fresh Runtime has no in-memory receipt/runner state.
	restarted := Runtime{Config: f.aut.Config, DB: f.aut.DB}
	if ran, err := restarted.RunAutomationOnce(context.Background()); !ran || err != nil {
		t.Fatalf("restart=%v/%v", ran, err)
	}
	f.assertTarget("open", "urgent", 2)
	if f.execution(e.ID).Status != "succeeded" {
		t.Fatal("receipt reconciliation did not complete")
	}
	var count int64
	f.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&count)
	if count != 1 {
		t.Fatalf("owner receipts=%d", count)
	}
}
func TestAUTRunnerCheckpointRollbackRecoversWithoutRepeatingEffect(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":{"priority":"high"}}]`)
	e := f.call(f.startCommand(), 200)
	if err := f.aut.DB.Callback().Create().Before("gorm:create").Register("aut_runner_fail_outbox", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.Action == "event_outbox" {
			tx.AddError(errors.New("forced checkpoint rollback"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if ran, err := f.aut.RunAutomationOnce(context.Background()); !ran || err == nil {
		t.Fatalf("wanted checkpoint failure, got %v/%v", ran, err)
	}
	f.aut.DB.Callback().Create().Remove("aut_runner_fail_outbox")
	f.assertTarget("open", "high", 2)
	stored := f.execution(e.ID)
	if stored.Status != "running" || !strings.Contains(stored.ActionResults, "accepted") {
		t.Fatalf("checkpoint=%+v", stored)
	}
	past := time.Now().UTC().Add(-time.Minute)
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_lease_until", past)
	f.run()
	f.assertTarget("open", "high", 2)
	if f.execution(e.ID).Status != "succeeded" {
		t.Fatal("recovery failed")
	}
}
func TestAUTRunnerOwnerVersionRaceFailsWithoutOverwriting(t *testing.T) {
	f := newAUTRunnerFixture(t, false, localTwoActions)
	e := f.call(f.startCommand(), 200)
	f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"priority": "urgent", "object_version": 2})
	f.run()
	f.run()
	f.assertTarget("open", "urgent", 2)
	if f.execution(e.ID).Status != "failed" {
		t.Fatal("stale write not rejected")
	}
}
func TestAUTRunnerConcurrentPostgresWorkersApplyOnce(t *testing.T) {
	f := newAUTRunnerFixture(t, true, `[{"capability_id":"conversation.change_priority","capability_version":"v1","action_params":{"priority":"urgent"}}]`)
	e := f.call(f.startCommand(), 200)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := Runtime{Config: f.aut.Config, DB: f.aut.DB}
			_, err := r.RunAutomationOnce(context.Background())
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	f.assertTarget("open", "urgent", 2)
	if f.execution(e.ID).Status != "succeeded" {
		t.Fatal("runner did not finish")
	}
	var count int64
	f.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&count)
	if count != 1 {
		t.Fatal(fmt.Sprintf("owner command count=%d", count))
	}
}

func TestAUTRunnerQuarantinesPoisonCheckpointWithoutStarvation(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":{"priority":"high"}}]`)
	now := time.Now().UTC()
	poison := domain.AutomationRuleExecution{AccountID: 91, RuleID: f.rule.ID, ConversationID: f.conversation.ID, EventName: f.rule.EventName, Status: "running", RunnerContext: "{broken", ActionResults: "[]", TriggeredAt: now, CreatedAt: now}
	if err := f.aut.DB.Create(&poison).Error; err != nil {
		t.Fatal(err)
	}
	e := f.call(f.startCommand(), 200)
	f.run()
	if row := f.execution(poison.ID); row.Status != "manual_action_required" || row.Error != "invalid_runner_checkpoint" {
		t.Fatalf("poison=%+v", row)
	}
	f.run()
	f.assertTarget("open", "high", 2)
	if f.execution(e.ID).Status != "succeeded" {
		t.Fatal("valid work was starved")
	}
}
func TestAUTRunnerPartialFailurePreservesEarlierOwnerEffect(t *testing.T) {
	f := newAUTRunnerFixture(t, false, localTwoActions)
	e := f.call(f.startCommand(), 200)
	f.run()
	f.assertTarget("open", "high", 2)
	f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("object_version", 3)
	f.run()
	f.assertTarget("open", "high", 3)
	if row := f.execution(e.ID); row.Status != "partially_succeeded" || !strings.Contains(row.ActionResults, `"status":"succeeded"`) || !strings.Contains(row.ActionResults, `"status":"failed"`) {
		t.Fatalf("partial=%+v", row)
	}
}
func TestAUTRunnerEmittedOriginBlocksActualActionLoop(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":{"priority":"high"}}]`)
	f.rule.EventName = "conversation_updated"
	f.aut.DB.Model(&domain.AutomationRule{}).Where("id = ?", f.rule.ID).Update("event_name", f.rule.EventName)
	f.aut.DB.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", f.rule.ID).Update("event_name", f.rule.EventName)
	e := f.call(f.startCommand(), 200)
	f.run()
	f.assertTarget("open", "high", 2)
	var outbox domain.LocalChangeJournal
	f.con.Where("action = ?", "event_outbox").First(&outbox)
	var event foundation.EventEnvelope
	if err := json.Unmarshal([]byte(outbox.AfterState), &event); err != nil {
		t.Fatal(err)
	}
	c := f.startCommand()
	c.CommandID = "loop-start"
	c.IdempotencyKey = "loop-start"
	c.Input["event_id"] = event.EventID
	c.Input["target_version"] = 2
	c.Input["origin_execution_id"] = event.Payload["origin_execution_id"]
	c.Input["automation_depth"] = event.Payload["automation_depth"]
	blocked := f.call(c, 200)
	if blocked.Status != "loop_blocked" || blocked.ID == e.ID {
		t.Fatalf("loop=%+v", blocked)
	}
	if ran, err := f.aut.RunAutomationOnce(context.Background()); ran || err != nil {
		t.Fatalf("loop ran=%v/%v", ran, err)
	}
	f.assertTarget("open", "high", 2)
}
func TestAUTRunnerStaleLeaseCannotOverwriteRecoveredWorker(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":{"priority":"high"}}]`)
	e := f.call(f.startCommand(), 200)
	f.block.Store(true)
	done := make(chan error, 1)
	go func() { _, err := f.aut.RunAutomationOnce(context.Background()); done <- err }()
	select {
	case <-f.committed:
	case <-time.After(3 * time.Second):
		t.Fatal("owner never committed")
	}
	past := time.Now().UTC().Add(-time.Minute)
	if err := f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_lease_until", past).Error; err != nil {
		t.Fatal(err)
	}
	recovered := Runtime{Config: f.aut.Config, DB: f.aut.DB}
	recovered.Config.MaxTimeout = time.Second
	if ran, err := recovered.RunAutomationOnce(context.Background()); !ran || err != nil {
		t.Fatalf("recovery=%v/%v", ran, err)
	}
	close(f.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.assertTarget("open", "high", 2)
	if row := f.execution(e.ID); row.Status != "succeeded" {
		t.Fatalf("stale overwrite=%+v", row)
	}
	var count int64
	f.aut.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&count)
	if count != 1 {
		t.Fatalf("terminal events=%d", count)
	}
}

func TestAUTRunnerAuthenticationFailureCannotEraseUnknownCommittedEffect(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":{"priority":"high"}}]`)
	e := f.call(f.startCommand(), 200)
	f.lost.Store(true)
	f.run()
	f.assertTarget("open", "high", 2)
	retry := func() {
		past := time.Now().UTC().Add(-time.Minute)
		f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_retry_at", past)
	}
	original := f.aut.Config.Dependencies["CON"]
	wrong := original
	wrong.Token = "rotated-invalid"
	f.aut.Config.Dependencies["CON"] = wrong
	retry()
	f.run()
	if row := f.execution(e.ID); row.Status != "waiting" || !strings.Contains(row.ActionResults, "result_unknown") {
		t.Fatalf("unknown commit became false failure: %+v", row)
	}
	f.assertTarget("open", "high", 2)
	f.aut.Config.Dependencies["CON"] = original
	retry()
	f.run()
	if f.execution(e.ID).Status != "succeeded" {
		t.Fatal("valid receipt did not recover")
	}
	f.assertTarget("open", "high", 2)
}

func TestAUTRunnerQuarantinesCheckpointThatRemovesOwnerCAS(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":{"priority":"high"}}]`)
	e := f.call(f.startCommand(), 200)
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_target_version", 0)
	f.run()
	f.assertTarget("open", "medium", 1)
	if f.execution(e.ID).Status != "manual_action_required" {
		t.Fatal("zero-version checkpoint escaped quarantine")
	}
}

func TestAUTRunnerBackgroundLoopAppliesQueuedOwnerActions(t *testing.T) {
	f := newAUTRunnerFixture(t, false, localTwoActions)
	e := f.call(f.startCommand(), 200)
	f.aut.Config.EventPollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		defer close(done)
		f.aut.RunAutomation(ctx, func(err error) {
			select {
			case failures <- err:
			default:
			}
		})
	}()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-failures:
			t.Fatal(err)
		case <-deadline:
			t.Fatal("background runner did not finish")
		case <-ticker.C:
			if f.execution(e.ID).Status == "succeeded" {
				cancel()
				<-done
				f.assertTarget("resolved", "high", 3)
				return
			}
		}
	}
}
