package modruntime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

func cascadeSetupPlan(t *testing.T, e domain.AutomationRuleExecution) (automationRunnerContext, []automationActionResult) {
	t.Helper()
	var p automationRunnerContext
	var results []automationActionResult
	if err := json.Unmarshal([]byte(e.RunnerContext), &p); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(e.ActionResults), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(p.Actions) || len(p.Dispatches) != len(p.Actions) {
		t.Fatalf("invalid setup plan lengths: %+v", e)
	}
	return p, results
}

// Advance confirmed action indexes, not owner message counts.
// Only a verified unknown action may reconcile once; every other non-progress
// outcome fails rather than silently turning setup into an unbounded retry loop.
func advanceCascadeSetup(t *testing.T, f *automationActionFixture, id uint, want int) int {
	t.Helper()
	initial := f.execution(id).RunnerNextAction
	if want < initial {
		t.Fatalf("setup already overshot: index=%d want=%d", initial, want)
	}
	maxCalls := 2 * (want - initial)
	calls, recovered := 0, 0
	retried := map[int]bool{}
	for {
		e := f.execution(id)
		plan, results := cascadeSetupPlan(t, e)
		if e.CancelRequested || e.RunnerLease != "" || e.RunnerLeaseUntil != nil || e.Error != "" {
			t.Fatalf("unexpected setup lifecycle: %+v", e)
		}
		if e.RunnerNextAction < initial || e.RunnerNextAction > want || want > len(results) {
			t.Fatalf("setup index escaped checkpoint: %+v want=%d", e, want)
		}
		for i := 0; i < e.RunnerNextAction; i++ {
			if results[i].Status != "succeeded" {
				t.Fatalf("unconfirmed setup prefix: %+v", e)
			}
		}
		if e.RunnerNextAction == want {
			status := "received"
			if want == len(results) {
				status = "succeeded"
			}
			if e.Status != status || e.RunnerRetryAt != nil {
				t.Fatalf("wrong reached setup checkpoint: %+v", e)
			}
			for i := want; i < len(results); i++ {
				if results[i].Status != "pending" || !automationEmptyDispatch(plan.Dispatches[i]) {
					t.Fatalf("setup ran beyond requested checkpoint: %+v", e)
				}
			}
			return recovered
		}
		if calls >= maxCalls || !validAutomationCheckpoint(e, plan, results) {
			t.Fatalf("setup bound/validity failed: calls=%d max=%d execution=%+v", calls, maxCalls, e)
		}
		index := e.RunnerNextAction
		reconciling := e.Status == "waiting"
		var frozen []byte
		if reconciling {
			if results[index].Status != "result_unknown" || e.RunnerRetryAt == nil || automationEmptyDispatch(plan.Dispatches[index]) || retried[index] {
				t.Fatalf("unsupported/repeated setup recovery: %+v", e)
			}
			retried[index] = true
			recovered++
			frozen, _ = json.Marshal(plan.Dispatches[index])
			t.Logf("setup recovery execution=%d index=%d owner=%s command=%s retry=%s recovery=1", id, index, automationActionOwner(plan.Actions[index]), plan.Dispatches[index].CommandID, e.RunnerRetryAt.Format(time.RFC3339Nano))
			update := f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ? AND status = ? AND runner_next_action = ? AND runner_lease = ?", id, "waiting", index, "").Update("runner_retry_at", time.Now().UTC().Add(-time.Second))
			if update.Error != nil || update.RowsAffected != 1 {
				t.Fatalf("setup retry eligibility update: rows=%d err=%v", update.RowsAffected, update.Error)
			}
		} else if e.Status != "received" || results[index].Status != "pending" || e.RunnerRetryAt != nil {
			t.Fatalf("unexpected setup state: %+v", e)
		}
		ran, err := f.aut.RunAutomationOnce(context.Background())
		calls++
		if err != nil || !ran {
			t.Fatalf("eligible setup run=%v err=%v execution=%+v", ran, err, f.execution(id))
		}
		after := f.execution(id)
		afterPlan, afterResults := cascadeSetupPlan(t, after)
		if reconciling {
			replay, _ := json.Marshal(afterPlan.Dispatches[index])
			if string(replay) != string(frozen) || after.RunnerNextAction != index+1 || afterResults[index].Status != "succeeded" {
				t.Fatalf("setup reconciliation did not confirm exactly one frozen action: %+v", after)
			}
		} else if after.RunnerNextAction == index {
			if after.Status != "waiting" || afterResults[index].Status != "result_unknown" {
				t.Fatalf("setup did not advance or establish an unknown action: %+v", after)
			}
		} else if after.RunnerNextAction != index+1 || afterResults[index].Status != "succeeded" {
			t.Fatalf("unexpected setup advancement: %+v", after)
		}
	}
}

func cascadeSetupCount(t *testing.T, db *gorm.DB, model any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	q := db.Model(model)
	if where != "" {
		q = q.Where(where, args...)
	}
	if err := q.Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func assertCascadeSetupCheckpoint(t *testing.T, f *automationActionFixture, roots []domain.AutomationRuleExecution, firstMessages, totalMessages int) {
	t.Helper()
	if len(roots) != 2 {
		t.Fatalf("setup roots=%d", len(roots))
	}
	rows := []domain.AutomationRuleExecution{f.execution(roots[0].ID), f.execution(roots[1].ID)}
	if rows[0].RunnerPredecessorID != nil || rows[1].RunnerPredecessorID == nil || *rows[1].RunnerPredecessorID != rows[0].ID || rows[0].FactBatchKey == "" || rows[0].FactBatchKey != rows[1].FactBatchKey {
		t.Fatalf("setup causal identities changed: %+v", rows)
	}
	proof := map[string]any{"same_batch": true, "linked_predecessor": true}
	for n, row := range rows {
		wantIndex, wantVersion, wantStatus := 2*firstMessages, uint(firstMessages+1), "succeeded"
		if n == 1 {
			wantIndex, wantVersion, wantStatus = 2*(totalMessages-firstMessages), uint(totalMessages+1), "received"
		}
		plan, results := cascadeSetupPlan(t, row)
		wantLength := wantIndex
		if n == 1 {
			wantLength++
		}
		if row.Status != wantStatus || row.RunnerNextAction != wantIndex || row.RunnerTargetVersion != wantVersion || len(results) != wantLength || row.CancelRequested || row.RunnerLease != "" || row.RunnerLeaseUntil != nil || row.RunnerRetryAt != nil || row.Error != "" {
			t.Fatalf("setup checkpoint not equivalent: %+v", row)
		}
		if row.AccountID != 91 || row.ConversationID != f.conversation.ID || row.TargetVersion != 1 || row.OriginExecutionID != nil || row.AutomationDepth != 0 || (row.CompletedAt != nil) != (n == 0) {
			t.Fatalf("setup identity/completion changed: %+v", row)
		}
		statuses := make([]string, len(results))
		for i := range results {
			statuses[i] = results[i].Status
			if i < wantIndex {
				command := "con.automation.message.prepare.v1"
				if i%2 == 1 {
					command = "msg.automation.message.create.v1"
				}
				if results[i].Status != "succeeded" || results[i].ObjectVersion <= 0 || plan.Actions[i].Command != command || automationEmptyDispatch(plan.Dispatches[i]) {
					t.Fatalf("setup prefix is not confirmed: root=%d index=%d result=%+v", n, i, results[i])
				}
			} else if results[i].Status != "pending" || results[i].ObjectVersion != 0 || plan.Actions[i].Command != "con.priority.change.v1" || plan.Actions[i].Value != "urgent" || !automationEmptyDispatch(plan.Dispatches[i]) {
				t.Fatalf("setup did not stop before final priority: root=%d index=%d", n, i)
			}
		}
		key := "first"
		if n == 1 {
			key = "second"
		}
		proof[key] = map[string]any{"status": row.Status, "index": row.RunnerNextAction, "runner_version": row.RunnerTargetVersion, "results": statuses}
	}
	f.assertTarget("open", "medium", totalMessages+1)
	counts := map[string]int64{
		"messages":             cascadeSetupCount(t, f.owners["MSG"].DB, &domain.Message{}, ""),
		"msg_events":           cascadeSetupCount(t, f.owners["MSG"].DB, &domain.LocalChangeJournal{}, "action = ?", "event_outbox"),
		"msg_receipts":         cascadeSetupCount(t, f.owners["MSG"].DB, &domain.LocalChangeJournal{}, "action = ?", "command_receipt"),
		"con_receipts":         cascadeSetupCount(t, f.con, &domain.LocalChangeJournal{}, "action = ?", "command_receipt"),
		"con_prepare_receipts": cascadeSetupCount(t, f.con, &domain.LocalChangeJournal{}, "action = ? AND object_type = ?", "command_receipt", "con.automation.message.prepare.v1"),
	}
	for name, n := range counts {
		if n != int64(totalMessages) {
			t.Fatalf("setup %s=%d want=%d", name, n, totalMessages)
		}
	}
	proof["counts"], proof["owner"] = counts, map[string]any{"status": "open", "priority": "medium", "version": totalMessages + 1}
	raw, _ := json.Marshal(proof)
	t.Logf("SETUP_PROOF %s", raw)
}

func assertCascadeFinalUnknown(t *testing.T, f *automationActionFixture, id uint) {
	t.Helper()
	row := f.execution(id)
	plan, results := cascadeSetupPlan(t, row)
	if f.lost["CON"].Load() || row.Status != "waiting" || row.RunnerNextAction != 90 || row.RunnerTargetVersion != 106 || row.RunnerLease != "" || row.RunnerLeaseUntil != nil || row.RunnerRetryAt == nil || row.CancelRequested || len(results) != 91 || results[90].Status != "result_unknown" || automationEmptyDispatch(plan.Dispatches[90]) {
		t.Fatalf("final intentional lost-response checkpoint missing: %+v", row)
	}
	for i := 0; i < 90; i++ {
		if results[i].Status != "succeeded" {
			t.Fatalf("final fault lost earlier effect at %d", i)
		}
	}
	f.assertTarget("open", "urgent", 107)
	if n := cascadeSetupCount(t, f.con, &domain.LocalChangeJournal{}, "action = ?", "command_receipt"); n != 106 {
		t.Fatalf("final CON receipts=%d want=106", n)
	}
}

func TestAutomationCascadeSetupCheckpoint(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "healthy"
		if lost {
			name = "lost_last_msg"
		}
		t.Run(name, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"first"}]`)
			cascadeRule(t, f, "conversation_created", "[]", `[{"action_name":"send_message","action_params":"last"},{"action_name":"change_priority","action_params":"urgent"}]`)
			fact := bridgeFact(f.autRunnerFixture, "conversation_created")
			if err := f.aut.AcceptAutomationFact(context.Background(), fact); err != nil {
				t.Fatal(err)
			}
			roots := causalExecutions(t, f.autRunnerFixture, fact.EventID)
			if len(roots) != 2 {
				t.Fatalf("roots=%d", len(roots))
			}
			if n := advanceCascadeSetup(t, f, roots[0].ID, 2); n != 0 {
				t.Fatalf("unexpected first-root recovery=%d", n)
			}
			if n := advanceCascadeSetup(t, f, roots[1].ID, 1); n != 0 {
				t.Fatalf("unexpected prepare recovery=%d", n)
			}
			var frozen []byte
			if lost {
				f.lost["MSG"].Store(true)
				f.run()
				unknown := f.execution(roots[1].ID)
				plan, results := cascadeSetupPlan(t, unknown)
				if f.lost["MSG"].Load() || unknown.Status != "waiting" || unknown.RunnerNextAction != 1 || unknown.RunnerTargetVersion != 3 || len(results) != 3 || results[1].Status != "result_unknown" || results[2].Status != "pending" {
					t.Fatalf("last-MSG lost response not established: %+v", unknown)
				}
				frozen, _ = json.Marshal(plan.Dispatches[1])
				for _, count := range []int64{cascadeSetupCount(t, f.owners["MSG"].DB, &domain.Message{}, ""), cascadeSetupCount(t, f.owners["MSG"].DB, &domain.LocalChangeJournal{}, "action = ?", "command_receipt"), cascadeSetupCount(t, f.owners["MSG"].DB, &domain.LocalChangeJournal{}, "action = ?", "event_outbox")} {
					if count != 2 {
						t.Fatalf("lost response preceded real owner commit: count=%d", count)
					}
				}
			}
			wantRecoveries := 0
			if lost {
				wantRecoveries = 1
			}
			if n := advanceCascadeSetup(t, f, roots[1].ID, 2); n != wantRecoveries {
				t.Fatalf("recoveries=%d want=%d", n, wantRecoveries)
			}
			if lost {
				plan, _ := cascadeSetupPlan(t, f.execution(roots[1].ID))
				replay, _ := json.Marshal(plan.Dispatches[1])
				captures := failureRecoveryCaptures(f, "MSG")
				if string(replay) != string(frozen) || len(captures) != 3 || captures[1] != captures[2] {
					t.Fatalf("setup replay changed its command or send count: captures=%d", len(captures))
				}
			}
			assertCascadeSetupCheckpoint(t, f, roots, 1, 2)
		})
	}
}
