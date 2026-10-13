package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

func restoreAUTOwnerFixture(t *testing.T, source *autRunnerFixture, pg bool) *autRunnerFixture {
	t.Helper()
	createDB := newModuleTestDB
	if pg {
		createDB = postgresModuleDB
	}
	targetAUT, targetCON := createDB(t, "AUT"), createDB(t, "CON")
	for owner, pair := range map[string][2]*gorm.DB{"AUT": {source.aut.DB, targetAUT}, "CON": {source.con, targetCON}} {
		path := filepath.Join(t.TempDir(), strings.ToLower(owner)+".snapshot.json")
		if err := CreateSnapshot(pair[0], owner, "owner-restore", path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private snapshot mode: %v %v", info, err)
		}
		if err := RestoreSnapshot(context.Background(), pair[1], owner, "owner-restore", path, nil); err != nil {
			t.Fatalf("restore %s: %v", owner, err)
		}
	}
	cfg := autRunnerCONConfig()
	con := Runtime{Config: cfg, DB: targetCON, Contracts: NewContractRegistry("CON")}
	if err := con.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(con.Handler())
	t.Cleanup(server.Close)
	aut := Runtime{Config: source.aut.Config, DB: targetAUT, Contracts: NewContractRegistry("AUT")}
	aut.Config.Dependencies = map[string]DependencyConfig{"CON": {URL: server.URL, Token: con.Config.AuthToken}}
	if err := aut.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	if targetAUT.Migrator().HasTable(&domain.Conversation{}) || targetCON.Migrator().HasTable(&domain.AutomationRuleExecution{}) {
		t.Fatal("restore crossed owner storage boundary")
	}
	return &autRunnerFixture{t: t, aut: aut, con: targetCON, rule: source.rule, conversation: source.conversation}
}

func TestAUTSnapshotRestoresIndependentOwnersAndResumesReceipts(t *testing.T) {
	for _, state := range []string{"pending", "partial", "unknown", "accepted", "cancel_unknown", "terminal_partial"} {
		t.Run(state, func(t *testing.T) { testAUTSnapshotRestart(t, false, state) })
	}
}

func TestPostgresAUTSnapshotRestoresIndependentOwnersAndResumesReceipts(t *testing.T) {
	for _, state := range []string{"pending", "partial", "unknown", "accepted", "cancel_unknown", "terminal_partial"} {
		t.Run(state, func(t *testing.T) { testAUTSnapshotRestart(t, true, state) })
	}
}

func testAUTSnapshotRestart(t *testing.T, pg bool, state string) {
	source := newAUTRunnerFixture(t, pg, localTwoActions)
	start := source.startCommand()
	e := source.call(start, 200)
	if state != "pending" {
		source.lost.Store(state == "unknown" || state == "accepted" || state == "cancel_unknown")
		source.run()
		if source.lost.Load() {
			t.Fatal("lost-response fixture did not observe a successful owner command")
		}
	}
	if state == "cancel_unknown" {
		source.cancel(e.ID)
	}
	if state == "accepted" {
		stored := source.execution(e.ID)
		var results []automationActionResult
		if err := json.Unmarshal([]byte(stored.ActionResults), &results); err != nil {
			t.Fatal(err)
		}
		results[0].Status = "accepted"
		results[0].Reason = ""
		raw, _ := json.Marshal(results)
		expired := time.Now().UTC().Add(-time.Minute)
		if err := source.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Updates(map[string]any{"status": "running", "runner_lease": "crashed-runner-lease", "runner_lease_until": expired, "action_results": string(raw)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if state == "unknown" || state == "accepted" || state == "cancel_unknown" {
		if err := source.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_retry_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if state == "terminal_partial" {
		// An unrelated owner update rejects action two after action one committed.
		if err := source.con.Model(&domain.Conversation{}).Where("id = ?", source.conversation.ID).Update("object_version", 3).Error; err != nil {
			t.Fatal(err)
		}
		source.run()
		if source.execution(e.ID).Status != "partially_succeeded" {
			t.Fatal("partial terminal fixture was not established")
		}
	}
	// Neither restoring nor starting the process may substitute the current rule
	// for the immutable version and private frozen dispatch plan.
	if err := source.aut.DB.Model(&domain.AutomationRule{}).Where("id = ?", source.rule.ID).Update("actions", `[{"action_name":"change_priority","action_params":{"priority":"low"}}]`).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.aut.DB.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: source.rule.ID, Version: 2, EventName: source.rule.EventName, Conditions: "[]", Actions: "[]", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	before := source.execution(e.ID)
	assertAUTSnapshotFixtureState(t, source, before, state)
	restored := restoreAUTOwnerFixture(t, source, pg)
	after := restored.execution(e.ID)
	assertAUTSnapshotEqual(t, before, after)
	if state != "terminal_partial" {
		restored.run()
		if state == "unknown" || state == "accepted" || state == "cancel_unknown" {
			restored.assertTarget("open", "high", 2)
		}
		if state == "pending" || state == "unknown" || state == "accepted" {
			restored.run()
		}
	}
	wantStatus, wantTargetStatus, wantVersion, wantReceipts := "succeeded", "resolved", 3, int64(2)
	if state == "cancel_unknown" {
		wantStatus, wantTargetStatus, wantVersion, wantReceipts = "cancelled", "open", 2, 1
	}
	if state == "terminal_partial" {
		wantStatus, wantTargetStatus, wantVersion, wantReceipts = "partially_succeeded", "open", 3, 1
	}
	if got := restored.execution(e.ID); got.Status != wantStatus || got.RuleVersion != 1 || got.ExecutionKey == nil || *got.ExecutionKey != *before.ExecutionKey {
		t.Fatalf("restored execution: %+v", got)
	}
	restored.assertTarget(wantTargetStatus, "high", wantVersion)
	var receipts, events int64
	if err := restored.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if err := restored.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if receipts != wantReceipts || events != wantReceipts {
		t.Fatalf("duplicate real owner effects: receipts=%d events=%d want=%d", receipts, events, wantReceipts)
	}
	// A replay with a different transport identity retains semantic deduplication.
	start.CommandID, start.IdempotencyKey = "replay-after-restore", "replay-after-restore"
	start.Input["rule_version"] = 1
	if got := restored.call(start, 200); got.ID != e.ID {
		t.Fatalf("semantic replay created execution %d, expected %d", got.ID, e.ID)
	}
	if ran, err := restored.aut.RunAutomationOnce(context.Background()); ran || err != nil {
		t.Fatalf("terminal replay dispatched: %v %v", ran, err)
	}
	// Fresh logical work after restore must allocate a new execution ID, rather
	// than colliding with the explicit IDs imported by the snapshot.
	start.CommandID, start.IdempotencyKey = "fresh-after-restore", "fresh-after-restore"
	start.Input["event_id"], start.Input["target_version"], start.Input["rule_version"] = "fresh-fact", wantVersion, 2
	if fresh := restored.call(start, 200); fresh.ID <= e.ID || fresh.Status != "succeeded" {
		t.Fatalf("fresh restored execution=%+v", fresh)
	}
	assertAUTSnapshotEqual(t, before, source.execution(e.ID))
}

func assertAUTSnapshotFixtureState(t *testing.T, source *autRunnerFixture, got domain.AutomationRuleExecution, state string) {
	t.Helper()
	wantStatus, wantPriority := "received", "high"
	wantNext, wantRunnerVersion, wantOwnerVersion, wantReceipts := 0, uint(1), 2, int64(1)
	wantResults := []string{"result_unknown", "pending"}
	switch state {
	case "pending":
		wantPriority, wantOwnerVersion, wantReceipts = "medium", 1, 0
		wantResults = []string{"pending", "pending"}
	case "partial":
		wantNext, wantRunnerVersion = 1, 2
		wantResults = []string{"succeeded", "pending"}
	case "unknown", "cancel_unknown":
		wantStatus = "waiting"
	case "accepted":
		wantStatus = "running"
		wantResults = []string{"accepted", "pending"}
	case "terminal_partial":
		wantStatus, wantNext, wantRunnerVersion, wantOwnerVersion = "partially_succeeded", 2, 2, 3
		wantResults = []string{"succeeded", "failed"}
	default:
		t.Fatalf("unknown snapshot fixture state %q", state)
	}
	var results []automationActionResult
	if err := json.Unmarshal([]byte(got.ActionResults), &results); err != nil {
		t.Fatal(err)
	}
	statuses := make([]string, len(results))
	for i, result := range results {
		statuses[i] = result.Status
	}
	if got.Status != wantStatus || got.RunnerNextAction != wantNext || got.RunnerTargetVersion != wantRunnerVersion || got.CancelRequested != (state == "cancel_unknown") || !reflect.DeepEqual(statuses, wantResults) {
		t.Fatalf("snapshot fixture %s was not established: execution=%+v expected=%s/%v/index%d/runner-version%d", state, got, wantStatus, wantResults, wantNext, wantRunnerVersion)
	}
	source.assertTarget("open", wantPriority, wantOwnerVersion)
	for _, action := range []string{"command_receipt", "event_outbox"} {
		var count int64
		if err := source.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", action).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != wantReceipts {
			t.Fatalf("snapshot fixture %s has %s=%d, want %d", state, action, count, wantReceipts)
		}
	}
}

func assertAUTSnapshotEqual(t *testing.T, want, got domain.AutomationRuleExecution) {
	t.Helper()
	a, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{want})
	if err != nil {
		t.Fatal(err)
	}
	b, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{got})
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil || !reflect.DeepEqual(left, right) {
		t.Fatalf("snapshot lost durable state\nwant=%s\ngot=%s", a, b)
	}
	public, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"execution_key", "runner_context", "runner_next_action", "runner_target_version", "runner_lease", "runner_lease_until", "runner_retry_at", "fact_batch_key", "runner_predecessor_id"} {
		if strings.Contains(string(public), `"`+field+`"`) {
			t.Fatalf("private field leaked: %s", field)
		}
	}
	if got.RunnerLease != "" && strings.Contains(string(public), got.RunnerLease) {
		t.Fatal("private lease leaked")
	}
	if got.ExecutionKey != nil && strings.Contains(string(public), *got.ExecutionKey) {
		t.Fatal("private semantic key leaked")
	}
}

func TestAUTSnapshotPreservesActiveLeaseAndRetrySchedule(t *testing.T) {
	source := newAUTRunnerFixture(t, false, localTwoActions)
	e := source.call(source.startCommand(), 200)
	future := time.Now().UTC().Add(time.Hour)
	if err := source.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Updates(map[string]any{"runner_lease": "lease-still-owned", "runner_lease_until": future, "runner_retry_at": future}).Error; err != nil {
		t.Fatal(err)
	}
	restored := restoreAUTOwnerFixture(t, source, false)
	assertAUTSnapshotEqual(t, source.execution(e.ID), restored.execution(e.ID))
	if ran, err := restored.aut.RunAutomationOnce(context.Background()); ran || err != nil {
		t.Fatalf("premature restored dispatch: %v %v", ran, err)
	}
	restored.assertTarget("open", "medium", 1)
}

func TestAUTSnapshotRejectsInvalidCheckpointAtomically(t *testing.T) {
	source := newAUTRunnerFixture(t, false, localTwoActions)
	e := source.call(source.startCommand(), 200)
	path := filepath.Join(t.TempDir(), "aut.json")
	if err := CreateSnapshot(source.aut.DB, "AUT", "reject", path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*automationExecutionSnapshotTable){
		"foreign_association": func(v *automationExecutionSnapshotTable) {
			v.Rows[0].Conversation = &domain.Conversation{ID: 999, AccountID: 91, Status: "resolved"}
		},
		"nested_rule": func(v *automationExecutionSnapshotTable) {
			v.Rows[0].Rule = &domain.AutomationRule{ID: 999, AccountID: 91, Name: "unexpected nested rule"}
		},
		"wrong_initial_guard": func(v *automationExecutionSnapshotTable) { v.Rows[0].Private.RunnerTargetVersion = 900 },
		"unsupported_codec":   func(v *automationExecutionSnapshotTable) { v.Codec = "aut.execution.v99" },
		"missing_private":     func(v *automationExecutionSnapshotTable) { v.Rows[0].Private = nil },
		"missing_key":         func(v *automationExecutionSnapshotTable) { v.Rows[0].Private.ExecutionKey = nil },
		"wrong_key": func(v *automationExecutionSnapshotTable) {
			key := strings.Repeat("0", 64)
			v.Rows[0].Private.ExecutionKey = &key
		},
		"missing_plan":      func(v *automationExecutionSnapshotTable) { v.Rows[0].Private.RunnerContext = "" },
		"invalid_plan_json": func(v *automationExecutionSnapshotTable) { v.Rows[0].Private.RunnerContext = "{" },
		"unsupported_action": func(v *automationExecutionSnapshotTable) {
			v.Rows[0].Private.RunnerContext = strings.ReplaceAll(v.Rows[0].Private.RunnerContext, "con.priority.change.v1", "msg.message.send.v1")
		},
		"changed_frozen_plan": func(v *automationExecutionSnapshotTable) {
			v.Rows[0].Private.RunnerContext = strings.ReplaceAll(v.Rows[0].Private.RunnerContext, `"value":"high"`, `"value":"low"`)
		},
		"negative_checkpoint":  func(v *automationExecutionSnapshotTable) { v.Rows[0].Private.RunnerNextAction = -1 },
		"past_last_checkpoint": func(v *automationExecutionSnapshotTable) { v.Rows[0].Private.RunnerNextAction = 2 },
		"missing_target_guard": func(v *automationExecutionSnapshotTable) { v.Rows[0].Private.RunnerTargetVersion = 0 },
		"invalid_result_json":  func(v *automationExecutionSnapshotTable) { v.Rows[0].ActionResults = "null" },
		"missing_predecessor": func(v *automationExecutionSnapshotTable) {
			missing := uint(1000)
			v.Rows[0].Private.RunnerPredecessorID = &missing
			v.Rows[0].Private.FactBatchKey = "batch"
		},
		"self_predecessor": func(v *automationExecutionSnapshotTable) {
			v.Rows[0].Private.RunnerPredecessorID = &v.Rows[0].ID
			v.Rows[0].Private.FactBatchKey = "batch"
		},
		"missing_origin": func(v *automationExecutionSnapshotTable) {
			missing := uint(1000)
			v.Rows[0].OriginExecutionID = &missing
			v.Rows[0].AutomationDepth = 1
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var file snapshotFile
			if err := json.Unmarshal(original, &file); err != nil {
				t.Fatal(err)
			}
			var table automationExecutionSnapshotTable
			if err := json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table); err != nil {
				t.Fatal(err)
			}
			mutate(&table)
			raw, _ := json.Marshal(table)
			file.Snapshot.Tables["automation_rule_executions"] = raw
			snapshot, _ := json.Marshal(file.Snapshot)
			file.Checksum = sha256Bytes(snapshot) // checksum-valid semantic corruption
			candidate, _ := json.Marshal(file)
			candidatePath := filepath.Join(t.TempDir(), "invalid.json")
			if err := os.WriteFile(candidatePath, candidate, 0600); err != nil {
				t.Fatal(err)
			}
			target := newModuleTestDB(t, "AUT")
			sentinel := domain.AutomationRule{ID: 700, AccountID: 91, Name: "keep", EventName: "conversation_created", Actions: "[]", Conditions: "[]"}
			if err := target.Create(&sentinel).Error; err != nil {
				t.Fatal(err)
			}
			rebuilt := false
			err := RestoreSnapshot(context.Background(), target, "AUT", "reject", candidatePath, func(context.Context, *gorm.DB) error { rebuilt = true; return nil })
			if err == nil || rebuilt {
				t.Fatalf("invalid checkpoint accepted or rebuild ran: %v %v", err, rebuilt)
			}
			var got domain.AutomationRule
			if err := target.First(&got, sentinel.ID).Error; err != nil || got.Name != "keep" {
				t.Fatalf("failed restore changed owned rows: %+v %v", got, err)
			}
			var rows, markers int64
			target.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Count(&rows)
			target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "recovery_complete").Count(&markers)
			if rows != 0 || markers != 0 {
				t.Fatalf("failed restore committed rows=%d markers=%d", rows, markers)
			}
		})
	}
}

func TestAUTSnapshotRejectsUnversionedAndUnknownPrivateCodecFields(t *testing.T) {
	for _, raw := range []string{`[]`, `null`, `{"codec":"aut.execution.v1","rows":null}`, `{"codec":"aut.execution.v1","rows":[],"new_unsupported_field":true}`, `{"codec":"aut.execution.v1","rows":[]} {}`} {
		var rows []domain.AutomationRuleExecution
		if err := unmarshalAutomationSnapshot([]byte(raw), &rows); err == nil {
			t.Fatalf("unsupported snapshot accepted: %s", raw)
		}
	}
}

func TestPostgresAUTSnapshotSequenceRestartAndRollback(t *testing.T) {
	source := newAUTRunnerFixture(t, true, localTwoActions)
	source.call(source.startCommand(), 200)
	// IDs above the initial sequence state expose a restore collision immediately.
	legacy := domain.AutomationRuleExecution{ID: 500, AccountID: 91, RuleID: source.rule.ID, ConversationID: source.conversation.ID, EventName: source.rule.EventName, Status: "success", TriggeredAt: time.Now().UTC()}
	if err := source.aut.DB.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sequence.json")
	if err := CreateSnapshot(source.aut.DB, "AUT", "sequence", path); err != nil {
		t.Fatal(err)
	}
	target := postgresModuleDB(t, "AUT")
	original := legacy
	original.ID = 0
	if err := target.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	if err := RestoreSnapshot(context.Background(), target, "AUT", "sequence", path, func(context.Context, *gorm.DB) error { return errors.New("rebuild failed") }); err == nil {
		t.Fatal("failed rebuild accepted")
	}
	next := legacy
	next.ID = 0
	if err := target.Create(&next).Error; err != nil || next.ID != original.ID+1 {
		t.Fatalf("sequence restart escaped failed transaction: id=%d err=%v", next.ID, err)
	}
	if err := RestoreSnapshot(context.Background(), target, "AUT", "sequence", path, nil); err != nil {
		t.Fatal(err)
	}
	next.ID = 0
	if err := target.Create(&next).Error; err != nil || next.ID != 501 {
		t.Fatalf("restored sequence collision: id=%d err=%v", next.ID, err)
	}
	rule := domain.AutomationRule{AccountID: 91, Name: "new", EventName: "conversation_created", Actions: "[]", Conditions: "[]"}
	if err := target.Create(&rule).Error; err != nil || rule.ID <= source.rule.ID {
		t.Fatalf("rule sequence collision: id=%d err=%v", rule.ID, err)
	}
	// LocalChangeJournal IDs must also advance so recovery marker, receipts and
	// runner completion events can be written immediately after restore.
	marker := domain.LocalChangeJournal{ChangeID: "after-restore", SourceModule: "AUT", ObjectType: "test", Action: "test", Result: "applied", Status: "delivered"}
	if err := target.Create(&marker).Error; err != nil {
		t.Fatal(err)
	}
}

func TestAUTSnapshotRestoresFactBatchCausalCheckpoints(t *testing.T) {
	for _, state := range []string{"pending", "partial", "unknown_successor"} {
		t.Run(state, func(t *testing.T) { testAUTSnapshotCausalRestart(t, false, state) })
	}
}

func TestPostgresAUTSnapshotRestoresFactBatchCausalCheckpoints(t *testing.T) {
	for _, state := range []string{"pending", "partial", "unknown_successor"} {
		t.Run(state, func(t *testing.T) { testAUTSnapshotCausalRestart(t, true, state) })
	}
}

func testAUTSnapshotCausalRestart(t *testing.T, pg bool, state string) {
	source := newAUTRunnerFixture(t, pg, `[{"action_name":"resolve_conversation"}]`)
	// Include terminal no-op and unsupported definitions between real actions.
	for _, actions := range []string{`[]`, `[{"action_name":"send_webhook","action_params":["never"]}]`, `[{"action_name":"open_conversation"}]`} {
		now := time.Now().UTC()
		rule := domain.AutomationRule{AccountID: 91, Name: "Restored causal rule", EventName: source.rule.EventName, Actions: actions, Conditions: "[]", Active: true, CreatedAt: now, UpdatedAt: now}
		if err := source.aut.DB.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
		if err := source.aut.DB.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: rule.ID, Version: 1, EventName: rule.EventName, Conditions: "[]", Actions: actions, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	event := bridgeFact(source, "conversation_created")
	if err := source.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if state != "pending" {
		source.run()
	}
	if state == "unknown_successor" {
		source.run() // First finish the explicit unsupported action
		source.lost.Store(true)
		source.run()
		if err := source.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("status = ?", "waiting").Update("runner_retry_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
	}
	var before []domain.AutomationRuleExecution
	if err := source.aut.DB.Order("id ASC").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	if len(before) != 4 || before[0].FactBatchKey == "" || before[3].RunnerPredecessorID == nil {
		t.Fatalf("missing actual batch chain: %+v", before)
	}
	restored := restoreAUTOwnerFixture(t, source, pg)
	for _, e := range before {
		assertAUTSnapshotEqual(t, e, restored.execution(e.ID))
	}
	passes := 2
	if state == "unknown_successor" {
		passes = 1
	}
	if state == "pending" {
		passes = 3
	}
	for i := 0; i < passes; i++ {
		restored.run()
	}
	restored.assertTarget("open", "medium", 3)
	for i, want := range []string{"succeeded", "succeeded", "failed", "succeeded"} {
		if got := restored.execution(before[i].ID); got.Status != want || got.FactBatchKey != before[i].FactBatchKey {
			t.Fatalf("lost restored chain row: %+v", got)
		}
	}
	if err := restored.aut.AcceptAutomationFact(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if ran, err := restored.aut.RunAutomationOnce(context.Background()); ran || err != nil {
		t.Fatalf("restored fact replay dispatched: %v %v", ran, err)
	}
	var executions, receipts, events int64
	restored.aut.DB.Model(&domain.AutomationRuleExecution{}).Count(&executions)
	restored.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts)
	restored.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events)
	if executions != 4 || receipts != 2 || events != 2 {
		t.Fatalf("restored duplicate facts/effects: %d %d %d", executions, receipts, events)
	}
}

func TestAUTSnapshotRequiresAllDurableFieldsIncludingCancellation(t *testing.T) {
	source := newAUTRunnerFixture(t, false, localTwoActions)
	e := source.call(source.startCommand(), 200)
	source.lost.Store(true)
	source.run()
	source.cancel(e.ID)
	raw, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{source.execution(e.ID)})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []bool{false, true} {
		for _, null := range []bool{false, true} {
			for _, field := range []string{"cancel_requested", "runner_target_version", "runner_context", "runner_next_action", "runner_lease", "runner_retry_at", "fact_batch_key", "status", "account_id", "event_id", "action_results"} {
				var table map[string]any
				if err := json.Unmarshal(raw, &table); err != nil {
					t.Fatal(err)
				}
				row := table["rows"].([]any)[0].(map[string]any)
				if private {
					row = row["private"].(map[string]any)
				}
				if _, exists := row[field]; !exists {
					continue
				}
				// Pointer-valued null retries are valid state; omission still is not.
				if null && field == "runner_retry_at" {
					continue
				}
				if null {
					row[field] = nil
				} else {
					delete(row, field)
				}
				candidate, _ := json.Marshal(table)
				var executions []domain.AutomationRuleExecution
				if err := unmarshalAutomationSnapshot(candidate, &executions); err == nil {
					t.Fatalf("accepted missing/null checkpoint field: private=%v null=%v field=%s", private, null, field)
				}
			}
		}
	}
}

func TestAUTSnapshotTemporaryFileCannotExposePrivateState(t *testing.T) {
	source := newAUTRunnerFixture(t, false, localTwoActions)
	source.call(source.startCommand(), 200)
	dir := t.TempDir()
	for _, symlink := range []bool{false, true} {
		path := filepath.Join(dir, "snapshot.json")
		predictable := path + ".tmp"
		victim := filepath.Join(dir, "public-file")
		_ = os.Remove(predictable)
		if err := os.WriteFile(victim, []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		if symlink {
			if err := os.Symlink(victim, predictable); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(predictable, []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old-public-snapshot"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := CreateSnapshot(source.aut.DB, "AUT", "private", path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private snapshot permissions: %v %v", info, err)
		}
		untouched, err := os.ReadFile(predictable)
		if err != nil || string(untouched) != "keep" {
			t.Fatalf("predictable temporary path was followed: %s %v", untouched, err)
		}
	}
}

func TestAUTSnapshotRetainsTerminalKeysAndCausalOrigin(t *testing.T) {
	source := newAUTRunnerFixture(t, false, "[]")
	start := source.startCommand()
	parent := source.call(start, 200)
	now := time.Now().UTC()
	rule := domain.AutomationRule{AccountID: 91, Name: "Child no-op", EventName: source.rule.EventName, Actions: "[]", Conditions: "[]", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := source.aut.DB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.aut.DB.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: rule.ID, Version: 1, EventName: rule.EventName, Conditions: "[]", Actions: "[]", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	source.rule = rule
	start = source.startCommand()
	start.CommandID, start.IdempotencyKey = "child-noop", "child-noop"
	start.Input["origin_execution_id"], start.Input["automation_depth"] = parent.ID, 1
	child := source.call(start, 200)
	restored := restoreAUTOwnerFixture(t, source, false)
	for _, id := range []uint{parent.ID, child.ID} {
		before := source.execution(id)
		assertAUTSnapshotEqual(t, before, restored.execution(id))
		raw, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{before})
		if err != nil {
			t.Fatal(err)
		}
		var table automationExecutionSnapshotTable
		if err := json.Unmarshal(raw, &table); err != nil {
			t.Fatal(err)
		}
		table.Rows[0].Private.ExecutionKey = nil
		raw, _ = json.Marshal(table)
		var decoded []domain.AutomationRuleExecution
		if err := unmarshalAutomationSnapshot(raw, &decoded); err == nil {
			t.Fatal("terminal semantic identity was silently discarded")
		}
	}
	start.CommandID, start.IdempotencyKey = "child-replay", "child-replay"
	if got := restored.call(start, 200); got.ID != child.ID {
		t.Fatalf("causal replay duplicated: %+v", got)
	}
}

func TestAUTSnapshotRejectsDiscontinuousOwnerReceiptPrefix(t *testing.T) {
	source := newAUTRunnerFixture(t, false, localTwoActions)
	e := source.call(source.startCommand(), 200)
	source.run()
	var stored domain.AutomationRuleExecution
	if err := source.aut.DB.First(&stored, e.ID).Error; err != nil {
		t.Fatal(err)
	}
	var results []automationActionResult
	if err := json.Unmarshal([]byte(stored.ActionResults), &results); err != nil {
		t.Fatal(err)
	}
	results[0].ObjectVersion = 900
	raw, _ := json.Marshal(results)
	if err := source.aut.DB.Model(&stored).Updates(map[string]any{"runner_target_version": 900, "action_results": string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "forged-prefix.json")
	if err := CreateSnapshot(source.aut.DB, "AUT", "forged-prefix", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "AUT")
	if err := RestoreSnapshot(context.Background(), target, "AUT", "forged-prefix", path, nil); err == nil {
		t.Fatal("coordinated forged checkpoint and receipt version accepted")
	}
	var count int64
	if err := target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "recovery_complete").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid prefix marked ready: %d %v", count, err)
	}
}

func TestOwnerSnapshotDoesNotImportNestedForeignAssociations(t *testing.T) {
	source, target := newModuleTestDB(t, "CON"), newModuleTestDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: "open", Priority: "medium"}
	if err := source.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "flat-owner.json")
	if err := CreateSnapshot(source, "CON", "flat-owner", path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file snapshotFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	var rows []domain.Conversation
	if err := json.Unmarshal(file.Snapshot.Tables["conversations"], &rows); err != nil {
		t.Fatal(err)
	}
	rows[0].Contact = &domain.Contact{ID: 21, AccountID: 91, Name: "must not write foreign contact"}
	file.Snapshot.Tables["conversations"], _ = json.Marshal(rows)
	payload, _ := json.Marshal(file.Snapshot)
	file.Checksum = sha256Bytes(payload)
	raw, _ = json.Marshal(file)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSnapshot(context.Background(), target, "CON", "flat-owner", path, nil); err != nil {
		t.Fatal(err)
	}
	var got domain.Conversation
	if err := target.First(&got, conversation.ID).Error; err != nil || got.ContactID != 21 || got.Status != "open" {
		t.Fatalf("owned scalar state changed: %+v %v", got, err)
	}
	if target.Migrator().HasTable(&domain.Contact{}) {
		t.Fatal("nested association escaped owner inventory")
	}
}

func TestAUTSnapshotRejectsIncompleteFactBatchAtomically(t *testing.T) {
	for _, mutation := range []string{"missing_link", "skipped_link", "missing_receipt", "corrupt_receipt"} {
		t.Run(mutation, func(t *testing.T) {
			source := newAUTRunnerFixture(t, false, localTwoActions)
			for i := 0; i < 2; i++ {
				now := time.Now().UTC()
				rule := domain.AutomationRule{AccountID: 91, Name: "Queued batch rule", EventName: source.rule.EventName, Actions: localTwoActions, Conditions: "[]", Active: true, CreatedAt: now, UpdatedAt: now}
				if err := source.aut.DB.Create(&rule).Error; err != nil {
					t.Fatal(err)
				}
				if err := source.aut.DB.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: rule.ID, Version: 1, EventName: rule.EventName, Conditions: "[]", Actions: localTwoActions, CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			event := bridgeFact(source, "conversation_created")
			if err := source.aut.AcceptAutomationFact(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			var rows []domain.AutomationRuleExecution
			if err := source.aut.DB.Order("id ASC").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "missing_link":
				if err := source.aut.DB.Model(&rows[1]).Update("runner_predecessor_id", nil).Error; err != nil {
					t.Fatal(err)
				}
			case "skipped_link":
				if err := source.aut.DB.Model(&rows[2]).Update("runner_predecessor_id", rows[0].ID).Error; err != nil {
					t.Fatal(err)
				}
			case "missing_receipt":
				if err := source.aut.DB.Where("change_id = ?", rows[0].FactBatchKey).Delete(&domain.LocalChangeJournal{}).Error; err != nil {
					t.Fatal(err)
				}
			case "corrupt_receipt":
				if err := source.aut.DB.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", rows[0].FactBatchKey).Update("after_state", "{}").Error; err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "invalid-batch.json")
			if err := CreateSnapshot(source.aut.DB, "AUT", "invalid-batch", path); err != nil {
				t.Fatal(err)
			}
			target := newModuleTestDB(t, "AUT")
			if err := RestoreSnapshot(context.Background(), target, "AUT", "invalid-batch", path, nil); err == nil {
				t.Fatal("malformed causal batch was restored")
			}
			var count int64
			target.Model(&domain.LocalChangeJournal{}).Count(&count)
			if count != 0 {
				t.Fatalf("failed batch restore committed %d journal entries", count)
			}
		})
	}
}
