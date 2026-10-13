package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const failureRecoveryForeignAgent = `{"action_name":"assign_agent","action_params":[8]}`
const failureRecoveryPriority = `{"action_name":"change_priority","action_params":["high"]}`
const failureRecoveryResolve = `{"action_name":"resolve_conversation"}`

func failureRecoveryPlan(t *testing.T, e domain.AutomationRuleExecution) (automationRunnerContext, []automationActionResult) {
	t.Helper()
	var plan automationRunnerContext
	var results []automationActionResult
	require.NoError(t, json.Unmarshal([]byte(e.RunnerContext), &plan))
	require.NoError(t, json.Unmarshal([]byte(e.ActionResults), &results))
	require.Len(t, results, len(plan.Actions))
	return plan, results
}

func failureRecoveryStatuses(t *testing.T, e domain.AutomationRuleExecution, statuses ...string) {
	t.Helper()
	_, results := failureRecoveryPlan(t, e)
	got := make([]string, len(results))
	for i, result := range results {
		got[i] = result.Status
		if result.Status != "succeeded" {
			require.Zero(t, result.ObjectVersion, "unconfirmed step %d cannot prove a version", i)
			require.Zero(t, result.ResourceID)
			require.Empty(t, result.Outcome)
		}
	}
	require.Equal(t, statuses, got, "execution=%+v", e)
}

func failureRecoveryCount(t *testing.T, db *gorm.DB, model any, where string, values ...any) int64 {
	t.Helper()
	var count int64
	q := db.Model(model)
	if where != "" {
		q = q.Where(where, values...)
	}
	require.NoError(t, q.Count(&count).Error)
	return count
}

func failureRecoveryReceipts(t *testing.T, f *automationActionFixture, owner string, want int64) {
	t.Helper()
	require.Equal(t, want, failureRecoveryCount(t, f.owners[owner].DB, &domain.LocalChangeJournal{}, "action = ?", "command_receipt"), owner)
}

func failureRecoveryCaptures(f *automationActionFixture, owner string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.captures[owner]...)
}

func failureRecoveryRestore(t *testing.T, source *automationActionFixture) *automationActionFixture {
	t.Helper()
	restored := newAutomationActionFixture(t, false, source.rule.Actions)
	for _, owner := range []string{"IAM", "TEN", "CUS", "CON", "MSG", "AUT"} {
		path := filepath.Join(t.TempDir(), owner+".json")
		require.NoError(t, CreateSnapshot(source.owners[owner].DB, owner, "failure-recovery", path))
		require.NoError(t, RestoreSnapshot(context.Background(), restored.owners[owner].DB, owner, "failure-recovery", path, nil), owner)
	}
	return restored
}

// These are actual owner rejections: the configured foreign account member is
// rejected by TEN/CON and cannot create a CON receipt or advance its version.
func TestAUTFailureRecoveryContinuesIndependentActions(t *testing.T) {
	for _, raw := range []string{
		`[` + failureRecoveryForeignAgent + `,` + failureRecoveryPriority + `,` + failureRecoveryResolve + `]`,
		`[` + failureRecoveryPriority + `,` + failureRecoveryForeignAgent + `,` + failureRecoveryResolve + `]`,
	} {
		t.Run(raw, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, raw)
			e := f.call(f.startCommand(), http.StatusOK)
			require.Equal(t, "received", e.Status)
			final := f.drain(e.ID)
			require.Equal(t, "partially_succeeded", final.Status)
			require.Equal(t, "owner_command_rejected", final.Error)
			require.NotNil(t, final.CompletedAt)
			f.assertTarget("resolved", "high", 3)
			plan, results := failureRecoveryPlan(t, final)
			require.Equal(t, "continue_definite.v1", plan.FailurePolicy)
			version := uint64(1)
			failures := 0
			for i, result := range results {
				require.NotNil(t, plan.Actions[i].LogicalIndex)
				require.Equal(t, i, *plan.Actions[i].LogicalIndex)
				require.Equal(t, plan.Actions[i].LogicalIndex, result.LogicalIndex)
				require.Equal(t, version, plan.Dispatches[i].Target.ExpectedVersion)
				if result.Status == "failed" {
					failures++
					require.Equal(t, "owner_command_rejected", result.Reason)
					require.Zero(t, result.ObjectVersion)
				} else {
					require.Equal(t, "succeeded", result.Status)
					version++
					require.Equal(t, int(version), result.ObjectVersion)
				}
			}
			require.Equal(t, 1, failures)
			require.Equal(t, uint(3), final.RunnerTargetVersion)
			failureRecoveryReceipts(t, f, "CON", 2)
			require.Equal(t, int64(1), failureRecoveryCount(t, f.aut.DB, &domain.LocalChangeJournal{}, "action = ?", "event_outbox"))
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTFailureRecoveryDefinitionFailureIsAnOrderedStep(t *testing.T) {
	cases := map[string]struct {
		action string
		reason string
	}{
		"unsupported":         {`{"action_name":"send_webhook","action_params":"https://example.invalid/never"}`, "action_capability_executor_unavailable"},
		"invalid_priority":    {`{"action_name":"change_priority","action_params":"not-a-priority"}`, "invalid_action_parameters"},
		"empty_message":       {`{"action_name":"send_message"}`, "invalid_action_parameters"},
		"fractional_member":   {`{"action_name":"assign_agent","action_params":[1.5]}`, "invalid_action_parameters"},
		"unsupported_version": {`{"capability_id":"conversation.change_priority","capability_version":"v99","action_params":"low"}`, "invalid_action_parameters"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[`+failureRecoveryPriority+`,`+tc.action+`,`+failureRecoveryResolve+`]`)
			e := f.call(f.startCommand(), http.StatusOK)
			require.Equal(t, "received", e.Status)
			f.run()
			f.assertTarget("open", "high", 2)
			f.run()
			checkpoint := f.execution(e.ID)
			require.Equal(t, "received", checkpoint.Status, "one invalid definition must leave later actions runnable")
			failureRecoveryStatuses(t, checkpoint, "succeeded", "failed", "pending")
			plan, results := failureRecoveryPlan(t, checkpoint)
			require.Equal(t, tc.reason, plan.Actions[1].FailureReason)
			require.Equal(t, tc.reason, results[1].Reason)
			require.True(t, automationEmptyDispatch(plan.Dispatches[1]), "definition failure must never reach an owner")
			require.Equal(t, uint(2), checkpoint.RunnerTargetVersion)
			failureRecoveryReceipts(t, f, "CON", 1)
			f.run()
			final := f.execution(e.ID)
			require.Equal(t, "partially_succeeded", final.Status)
			failureRecoveryStatuses(t, final, "succeeded", "failed", "succeeded")
			f.assertTarget("resolved", "high", 3)
			failureRecoveryReceipts(t, f, "CON", 2)
			require.Zero(t, failureRecoveryCount(t, f.owners["MSG"].DB, &domain.Message{}, ""))
		})
	}
}

// Inject storage failures inside the real owner's transaction. Each rejection
// rolls back any attempted local effect, while prior successful owner receipts
// remain available to prove the next independent action's CAS version.
func TestAUTFailureRecoveryMultiStepFailuresSkipOnlyTheirLogicalGroup(t *testing.T) {
	cases := []struct {
		name      string
		action    string
		failIndex int
		version   int
		statuses  []string
		setup     func(*testing.T, *automationActionFixture)
	}{
		{"label_resolve", `{"action_name":"add_labels","action_params":["vip","blue"]}`, 1, 3, []string{"succeeded", "failed", "skipped", "skipped", "skipped", "succeeded"}, func(t *testing.T, f *automationActionFixture) {
			require.NoError(t, f.owners["CUS"].DB.Callback().Create().Before("gorm:create").Register("reject_failure_label", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*domain.Label); ok {
					tx.AddError(errors.New("label storage rejected"))
				}
			}))
		}},
		{"label_apply", `{"action_name":"add_labels","action_params":["vip","blue"]}`, 2, 3, []string{"succeeded", "succeeded", "failed", "skipped", "skipped", "succeeded"}, func(t *testing.T, f *automationActionFixture) {
			require.NoError(t, f.owners["CUS"].DB.Where("account_id = ?", 91).Delete(&domain.Label{}).Error)
		}},
		{"message_prepare", `{"action_name":"send_message","action_params":"must not be created"}`, 1, 3, []string{"succeeded", "failed", "skipped", "succeeded"}, func(t *testing.T, f *automationActionFixture) {
			require.NoError(t, f.con.Callback().Create().Before("gorm:create").Register("reject_message_grant", func(tx *gorm.DB) {
				if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.Action == "automation_message_authorization" {
					tx.AddError(errors.New("grant storage rejected"))
				}
			}))
		}},
		{"message_create", `{"action_name":"private_note","action_params":"must not be saved"}`, 2, 4, []string{"succeeded", "succeeded", "failed", "succeeded"}, func(t *testing.T, f *automationActionFixture) {
			require.NoError(t, f.owners["MSG"].DB.Callback().Create().Before("gorm:create").Register("reject_failure_message", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*domain.Message); ok {
					tx.AddError(errors.New("message storage rejected"))
				}
			}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[`+failureRecoveryPriority+`,`+tc.action+`,`+failureRecoveryResolve+`]`)
			e := f.call(f.startCommand(), http.StatusOK)
			for step := 0; step < tc.failIndex; step++ {
				f.run()
			}
			tc.setup(t, f)
			f.run()
			checkpoint := f.execution(e.ID)
			require.Equal(t, "received", checkpoint.Status)
			require.Equal(t, len(tc.statuses)-1, checkpoint.RunnerNextAction)
			require.Equal(t, uint(tc.version-1), checkpoint.RunnerTargetVersion)
			plan, results := failureRecoveryPlan(t, checkpoint)
			for i := 1; i < len(results)-1; i++ {
				require.NotNil(t, plan.Actions[i].LogicalIndex)
				require.Equal(t, 1, *plan.Actions[i].LogicalIndex)
				require.Equal(t, plan.Actions[i].LogicalIndex, results[i].LogicalIndex)
				if i > tc.failIndex {
					require.Equal(t, "skipped", results[i].Status)
					require.Equal(t, "dependency_failed", results[i].Reason)
					require.True(t, automationEmptyDispatch(plan.Dispatches[i]))
				}
			}
			f.run()
			final := f.execution(e.ID)
			require.Equal(t, "partially_succeeded", final.Status)
			failureRecoveryStatuses(t, final, tc.statuses...)
			f.assertTarget("resolved", "high", tc.version)
			plan, _ = failureRecoveryPlan(t, final)
			require.Equal(t, uint64(tc.version-1), plan.Dispatches[len(plan.Actions)-1].Target.ExpectedVersion)
			require.Zero(t, failureRecoveryCount(t, f.con, &domain.ConversationLabel{}, ""))
			require.Zero(t, failureRecoveryCount(t, f.owners["MSG"].DB, &domain.Message{}, ""))
			require.Zero(t, failureRecoveryCount(t, f.owners["CUS"].DB, &domain.Label{}, "title = ?", "blue"))
			failureRecoveryReceipts(t, f, "CON", int64(tc.version-1))
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTFailureRecoveryUnknownOwnerNeverUnlocksLaterAction(t *testing.T) {
	cases := []struct {
		name, action, owner string
		index               int
	}{
		{"label_resolve", `{"action_name":"add_label","action_params":"vip"}`, "CUS", 1},
		{"label_apply", `{"action_name":"add_label","action_params":"vip"}`, "CON", 2},
		{"message_prepare", `{"action_name":"send_message","action_params":"exactly once"}`, "CON", 1},
		{"message_create", `{"action_name":"send_message","action_params":"exactly once"}`, "MSG", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[`+failureRecoveryForeignAgent+`,`+tc.action+`,`+failureRecoveryResolve+`]`)
			e := f.call(f.startCommand(), http.StatusOK)
			for step := 0; step < tc.index; step++ {
				f.run()
			}
			f.lost[tc.owner].Store(true)
			f.run()
			unknown := f.execution(e.ID)
			require.Equal(t, "waiting", unknown.Status)
			require.Equal(t, tc.index, unknown.RunnerNextAction)
			require.Nil(t, unknown.CompletedAt)
			plan, results := failureRecoveryPlan(t, unknown)
			require.Equal(t, "result_unknown", results[tc.index].Status)
			require.Equal(t, "pending", results[len(results)-1].Status)
			require.True(t, automationEmptyDispatch(plan.Dispatches[len(plan.Actions)-1]))
			frozen, err := json.Marshal(plan.Dispatches[tc.index])
			require.NoError(t, err)
			committed := f.conversationState()
			require.Equal(t, "open", committed.Status)
			siblingRecoveryIdle(t, f.autRunnerFixture)

			// Authentication failure during reconciliation cannot establish that
			// the original owner command did not commit.
			original := f.aut.Config.Dependencies[tc.owner]
			wrong := original
			wrong.Token = "invalid-rotated-token"
			f.aut.Config.Dependencies[tc.owner] = wrong
			siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
			f.run()
			stillUnknown := f.execution(e.ID)
			require.Equal(t, "waiting", stillUnknown.Status)
			require.Equal(t, tc.index, stillUnknown.RunnerNextAction)
			require.Equal(t, unknown.RunnerTargetVersion, stillUnknown.RunnerTargetVersion)
			stillPlan, stillResults := failureRecoveryPlan(t, stillUnknown)
			require.Equal(t, "result_unknown", stillResults[tc.index].Status)
			require.Equal(t, "pending", stillResults[len(stillResults)-1].Status)
			retried, err := json.Marshal(stillPlan.Dispatches[tc.index])
			require.NoError(t, err)
			require.Equal(t, string(frozen), string(retried))
			f.assertTarget(committed.Status, committed.Priority, committed.ObjectVersion)

			f.aut.Config.Dependencies[tc.owner] = original
			siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
			// Reconstruct the worker without retaining transient runner state.
			f.aut = Runtime{Config: f.aut.Config, DB: f.aut.DB, Contracts: f.aut.Contracts}
			f.run()
			captures := failureRecoveryCaptures(f, tc.owner)
			require.GreaterOrEqual(t, len(captures), 3)
			require.Equal(t, captures[len(captures)-3], captures[len(captures)-2])
			require.Equal(t, captures[len(captures)-2], captures[len(captures)-1])
			final := f.drain(e.ID)
			require.Equal(t, "partially_succeeded", final.Status)
			failureRecoveryStatuses(t, final, "failed", "succeeded", "succeeded", "succeeded")
			f.assertTarget("resolved", "medium", 3)
			failureRecoveryReceipts(t, f, "CON", 2)
			if tc.owner == "MSG" || strings.Contains(tc.name, "message") {
				require.Equal(t, int64(1), failureRecoveryCount(t, f.owners["MSG"].DB, &domain.Message{}, ""))
				failureRecoveryReceipts(t, f, "MSG", 1)
			} else {
				require.Equal(t, int64(1), failureRecoveryCount(t, f.owners["CUS"].DB, &domain.Label{}, ""))
				failureRecoveryReceipts(t, f, "CUS", 1)
			}
		})
	}
}

func TestAUTFailureRecoveryMissingDependencyRemainsUnknown(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+failureRecoveryForeignAgent+`,{"action_name":"add_label","action_params":"vip"},`+failureRecoveryResolve+`]`)
	e := f.call(f.startCommand(), http.StatusOK)
	f.run()
	dependency := f.aut.Config.Dependencies["CUS"]
	delete(f.aut.Config.Dependencies, "CUS")
	f.run()
	unknown := f.execution(e.ID)
	require.Equal(t, "waiting", unknown.Status)
	failureRecoveryStatuses(t, unknown, "failed", "result_unknown", "pending", "pending")
	failureRecoveryReceipts(t, f, "CON", 0)
	failureRecoveryReceipts(t, f, "CUS", 0)
	f.assertTarget("open", "medium", 1)
	f.aut.Config.Dependencies["CUS"] = dependency
	siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
	final := f.drain(e.ID)
	require.Equal(t, "partially_succeeded", final.Status)
	f.assertTarget("resolved", "medium", 3)
}

func TestAUTFailureRecoveryCancellationStopsContinuationButReconcilesUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "between_actions"
		if unknown {
			name = "unknown_committed_action"
		}
		t.Run(name, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[`+failureRecoveryForeignAgent+`,`+failureRecoveryPriority+`,`+failureRecoveryResolve+`]`)
			e := f.call(f.startCommand(), http.StatusOK)
			f.run()
			if unknown {
				f.lost["CON"].Store(true)
				f.run()
				f.assertTarget("open", "high", 2)
			}
			cancelled := f.cancel(e.ID)
			if unknown {
				require.True(t, cancelled.CancelRequested)
				require.Equal(t, "waiting", cancelled.Status)
				f = failureRecoveryRestore(t, f)
				siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
				f.run()
			}
			stored := f.execution(e.ID)
			require.Equal(t, "cancelled", stored.Status)
			if unknown {
				failureRecoveryStatuses(t, stored, "failed", "succeeded", "skipped")
				f.assertTarget("open", "high", 2)
				failureRecoveryReceipts(t, f, "CON", 1)
			} else {
				failureRecoveryStatuses(t, stored, "failed", "skipped", "skipped")
				f.assertTarget("open", "medium", 1)
				failureRecoveryReceipts(t, f, "CON", 0)
			}
			_, results := failureRecoveryPlan(t, stored)
			require.Equal(t, "cancelled_by_request", results[len(results)-1].Reason)
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTFailureRecoveryManualConflictDoesNotAdoptLiveVersion(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+failureRecoveryPriority+`,`+failureRecoveryResolve+`,{"action_name":"change_priority","action_params":"urgent"}]`)
	e := f.call(f.startCommand(), http.StatusOK)
	f.run()
	f.assertTarget("open", "high", 2)
	require.NoError(t, repository.NewConversationRepository(f.con).UpdateStatus(91, f.conversation.ID, "pending", nil))
	f.run()
	checkpoint := f.execution(e.ID)
	require.Equal(t, "received", checkpoint.Status)
	require.Equal(t, uint(2), checkpoint.RunnerTargetVersion)
	failureRecoveryStatuses(t, checkpoint, "succeeded", "failed", "pending")
	f.run()
	final := f.execution(e.ID)
	require.Equal(t, "partially_succeeded", final.Status)
	failureRecoveryStatuses(t, final, "succeeded", "failed", "failed")
	plan, _ := failureRecoveryPlan(t, final)
	require.Equal(t, uint64(2), plan.Dispatches[1].Target.ExpectedVersion)
	require.Equal(t, uint64(2), plan.Dispatches[2].Target.ExpectedVersion)
	require.Equal(t, uint(2), final.RunnerTargetVersion)
	f.assertTarget("pending", "high", 3)
	failureRecoveryReceipts(t, f, "CON", 1)
	restored := failureRecoveryRestore(t, f)
	assertAUTSnapshotEqual(t, final, restored.execution(e.ID))
	restored.assertTarget("pending", "high", 3)
	siblingRecoveryIdle(t, restored.autRunnerFixture)
}

func TestAUTFailureRecoverySnapshotsResumeFrozenMixedResults(t *testing.T) {
	for _, state := range []string{"failed", "unknown", "accepted", "terminal_partial"} {
		t.Run(state, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[`+failureRecoveryForeignAgent+`,{"action_name":"private_note","action_params":"frozen recovery note"},`+failureRecoveryResolve+`]`)
			e := f.call(f.startCommand(), http.StatusOK)
			f.run()
			if state == "unknown" {
				f.lost["CON"].Store(true)
				f.run()
			}
			if state == "accepted" {
				// The owner commits its preparation, but AUT's completion
				// checkpoint rolls back. Recovery must use the accepted envelope.
				require.NoError(t, f.aut.DB.Callback().Update().Before("gorm:update").Register("fail_completion_checkpoint", func(tx *gorm.DB) {
					if values, ok := tx.Statement.Dest.(map[string]any); ok {
						if _, exists := values["runner_next_action"]; exists {
							tx.AddError(errors.New("completion checkpoint unavailable"))
						}
					}
				}))
				ran, err := f.aut.RunAutomationOnce(context.Background())
				require.True(t, ran)
				require.Error(t, err)
				require.NoError(t, f.aut.DB.Callback().Update().Remove("fail_completion_checkpoint"))
				_, results := failureRecoveryPlan(t, f.execution(e.ID))
				require.Equal(t, "accepted", results[1].Status)
				f.assertTarget("open", "medium", 2)
				require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_lease_until", time.Now().UTC().Add(-time.Minute)).Error)
			}
			if state == "terminal_partial" {
				f.drain(e.ID)
			}
			// A later rule version cannot replace either the failed prefix or
			// the still-pending private note and its immutable message contents.
			require.NoError(t, f.aut.DB.Model(&domain.AutomationRule{}).Where("id = ?", f.rule.ID).Update("actions", `[{"action_name":"change_priority","action_params":"low"}]`).Error)
			require.NoError(t, f.aut.DB.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: f.rule.ID, Version: 2, EventName: f.rule.EventName, Conditions: "[]", Actions: `[]`, CreatedAt: time.Now().UTC()}).Error)
			before := f.execution(e.ID)
			encoded, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{before})
			require.NoError(t, err)
			var table automationExecutionSnapshotTable
			require.NoError(t, json.Unmarshal(encoded, &table))
			require.Equal(t, "aut.execution.v5", table.Codec)
			restored := failureRecoveryRestore(t, f)
			assertAUTSnapshotEqual(t, before, restored.execution(e.ID))
			if state == "unknown" {
				siblingRecoveryRetryNow(t, restored.autRunnerFixture, e.ID)
			}
			final := restored.drain(e.ID)
			require.Equal(t, "partially_succeeded", final.Status)
			require.Equal(t, uint(1), final.RuleVersion)
			failureRecoveryStatuses(t, final, "failed", "succeeded", "succeeded", "succeeded")
			restored.assertTarget("resolved", "medium", 3)
			failureRecoveryReceipts(t, restored, "CON", 2)
			failureRecoveryReceipts(t, restored, "MSG", 1)
			var messages []domain.Message
			require.NoError(t, restored.owners["MSG"].DB.Find(&messages).Error)
			require.Len(t, messages, 1)
			require.Equal(t, "frozen recovery note", messages[0].Content)
			require.True(t, messages[0].Private)
			require.Equal(t, "saved", messages[0].Status)
			require.Equal(t, int64(1), failureRecoveryCount(t, restored.aut.DB, &domain.LocalChangeJournal{}, "action = ?", "event_outbox"))
			assertAUTSnapshotEqual(t, before, f.execution(e.ID))
			siblingRecoveryIdle(t, restored.autRunnerFixture)
		})
	}
}

func TestAUTFailureRecoverySnapshotPreservesSkippedGroup(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"add_labels","action_params":["vip","blue"]},`+failureRecoveryResolve+`]`)
	e := f.call(f.startCommand(), http.StatusOK)
	f.run()
	require.NoError(t, f.owners["CUS"].DB.Where("account_id = ?", 91).Delete(&domain.Label{}).Error)
	f.run()
	before := f.execution(e.ID)
	require.Equal(t, "received", before.Status)
	failureRecoveryStatuses(t, before, "succeeded", "failed", "skipped", "skipped", "pending")
	restored := failureRecoveryRestore(t, f)
	assertAUTSnapshotEqual(t, before, restored.execution(e.ID))
	restored.run()
	final := restored.execution(e.ID)
	require.Equal(t, "partially_succeeded", final.Status)
	failureRecoveryStatuses(t, final, "succeeded", "failed", "skipped", "skipped", "succeeded")
	restored.assertTarget("resolved", "medium", 2)
	failureRecoveryReceipts(t, restored, "CUS", 1)
	failureRecoveryReceipts(t, restored, "CON", 1)
	require.Zero(t, failureRecoveryCount(t, restored.owners["CUS"].DB, &domain.Label{}, "title = ?", "blue"))
}

func TestAUTFailureRecoveryLegacySnapshotsKeepStopPolicy(t *testing.T) {
	for _, codec := range []string{"aut.execution.v1", "aut.execution.v2"} {
		t.Run(codec, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[`+failureRecoveryForeignAgent+`,`+failureRecoveryPriority+`]`)
			start := f.startCommand()
			e := f.call(start, http.StatusOK)
			legacy := f.execution(e.ID)
			actions, reason := compileAutomationActions(f.rule.Actions)
			require.Empty(t, reason)
			require.NoError(t, f.aut.queueAutomationExecution(context.Background(), start, &legacy, actions))
			plan, _ := failureRecoveryPlan(t, f.execution(e.ID))
			require.Empty(t, plan.FailurePolicy)
			for _, action := range plan.Actions {
				require.Nil(t, action.LogicalIndex)
			}
			path := filepath.Join(t.TempDir(), "legacy.json")
			require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "legacy-failure", path))
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var file snapshotFile
			require.NoError(t, json.Unmarshal(raw, &file))
			var table automationExecutionSnapshotTable
			require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
			table.Codec = codec
			file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
			require.NoError(t, err)
			snapshot, err := json.Marshal(file.Snapshot)
			require.NoError(t, err)
			file.Checksum = sha256Bytes(snapshot)
			raw, err = json.Marshal(file)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			target := newModuleTestDB(t, "AUT")
			require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "legacy-failure", path, nil))
			f.aut.DB = target
			f.owners["AUT"].DB = target
			f.run()
			final := f.execution(e.ID)
			require.Equal(t, "failed", final.Status)
			failureRecoveryStatuses(t, final, "failed", "skipped")
			plan, results := failureRecoveryPlan(t, final)
			require.Empty(t, plan.FailurePolicy)
			require.Equal(t, "stopped_after_failure", results[1].Reason)
			require.True(t, automationEmptyDispatch(plan.Dispatches[1]))
			f.assertTarget("open", "medium", 1)
			failureRecoveryReceipts(t, f, "CON", 0)
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTFailureRecoveryCheckpointRejectsInventedProgress(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"add_labels","action_params":["vip","blue"]},`+failureRecoveryResolve+`]`)
	e := f.call(f.startCommand(), http.StatusOK)
	f.run()
	require.NoError(t, f.owners["CUS"].DB.Where("account_id = ?", 91).Delete(&domain.Label{}).Error)
	f.run()
	valid := f.execution(e.ID)
	plan, results := failureRecoveryPlan(t, valid)
	require.True(t, validAutomationCheckpoint(valid, plan, results))
	require.NoError(t, validateAutomationSnapshotExecution(valid))
	cases := map[string]func(*domain.AutomationRuleExecution, *automationRunnerContext, []automationActionResult){
		"failed_version": func(_ *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			results[1].ObjectVersion = 2
		},
		"skipped_version": func(_ *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			results[2].ObjectVersion = 2
		},
		"failed_resource": func(_ *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			results[1].ResourceID = 999
		},
		"unproven_target": func(e *domain.AutomationRuleExecution, _ *automationRunnerContext, _ []automationActionResult) {
			e.RunnerTargetVersion++
		},
		"skipped_dispatched": func(_ *domain.AutomationRuleExecution, plan *automationRunnerContext, _ []automationActionResult) {
			plan.Dispatches[2] = plan.Dispatches[1]
		},
		"wrong_result_group": func(_ *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			v := 1
			results[2].LogicalIndex = &v
		},
		"group_dependency_broken": func(_ *domain.AutomationRuleExecution, plan *automationRunnerContext, _ []automationActionResult) {
			v := 1
			plan.Actions[1].LogicalIndex = &v
		},
		"skip_without_failure": func(_ *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			results[1].Status = "skipped"
			results[1].Reason = "dependency_failed"
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			copy := valid
			plan, results := failureRecoveryPlan(t, copy)
			edit(&copy, &plan, results)
			raw, err := json.Marshal(plan)
			require.NoError(t, err)
			copy.RunnerContext = string(raw)
			raw, err = json.Marshal(results)
			require.NoError(t, err)
			copy.ActionResults = string(raw)
			require.False(t, validAutomationCheckpoint(copy, plan, results))
			require.Error(t, validateAutomationSnapshotExecution(copy))
		})
	}
}

// Exercise cancellation while a first-dispatch rejection is in flight. Its
// completion must notice the durable cancellation before starting a later group.
func TestAUTFailureRecoveryCancellationWinsInflightRejection(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+failureRecoveryForeignAgent+`,`+failureRecoveryPriority+`]`)
	e := f.call(f.startCommand(), http.StatusOK)
	read, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	siblingRecoveryHook(f, "CON", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		recorder := httptest.NewRecorder()
		next.ServeHTTP(recorder, r)
		if strings.Contains(r.URL.Path, "/commands/") && recorder.Code == http.StatusNotFound {
			once.Do(func() { close(read); <-release })
		}
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	})
	go func() { _, err := f.aut.RunAutomationOnce(context.Background()); done <- err }()
	siblingRecoveryWaitRead(t, read)
	cancelled := f.cancel(e.ID)
	require.True(t, cancelled.CancelRequested)
	close(release)
	require.NoError(t, <-done)
	stored := f.execution(e.ID)
	require.Equal(t, "cancelled", stored.Status)
	failureRecoveryStatuses(t, stored, "failed", "skipped")
	f.assertTarget("open", "medium", 1)
	failureRecoveryReceipts(t, f, "CON", 0)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

// Gated by DB_DSN and intended for the repository's native PostgreSQL wrapper.
// Independent workers contend on durable leases while each owner loses one
// committed response and two definite failures leave subsequent groups runnable.
func TestPostgresAUTFailureRecoveryConcurrentWorkers(t *testing.T) {
	f := newAutomationActionFixture(t, true, `[`+failureRecoveryForeignAgent+`,{"action_name":"add_labels","action_params":["vip","blue"]},{"action_name":"private_note","action_params":"one retained note"},{"action_name":"change_priority","action_params":"urgent"}]`)
	var rejected atomic.Int32
	require.NoError(t, f.owners["CUS"].DB.Callback().Create().Before("gorm:create").Register("reject_blue_label", func(tx *gorm.DB) {
		if label, ok := tx.Statement.Dest.(*domain.Label); ok && label.Title == "blue" {
			rejected.Add(1)
			tx.AddError(errors.New("blue label rejected"))
		}
	}))
	e := f.call(f.startCommand(), http.StatusOK)
	for _, owner := range []string{"CUS", "CON", "MSG"} {
		f.lost[owner].Store(true)
	}
	for round := 0; round < 30; round++ {
		current := f.execution(e.ID)
		if current.Status == "partially_succeeded" {
			break
		}
		if current.RunnerRetryAt != nil {
			siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
		}
		start, done := make(chan struct{}), make(chan error, 6)
		for worker := 0; worker < 6; worker++ {
			instance := Runtime{Config: f.aut.Config, DB: f.aut.DB}
			go func() { <-start; _, err := instance.RunAutomationOnce(context.Background()); done <- err }()
		}
		close(start)
		for worker := 0; worker < 6; worker++ {
			require.NoError(t, <-done)
		}
	}
	final := f.execution(e.ID)
	require.Equal(t, "partially_succeeded", final.Status)
	failureRecoveryStatuses(t, final, "failed", "succeeded", "succeeded", "failed", "skipped", "succeeded", "succeeded", "succeeded")
	require.Equal(t, int32(1), rejected.Load(), "the definite rejection must not redispatch after its checkpoint")
	f.assertTarget("open", "urgent", 4)
	failureRecoveryReceipts(t, f, "CUS", 1)
	failureRecoveryReceipts(t, f, "CON", 3)
	failureRecoveryReceipts(t, f, "MSG", 1)
	require.Equal(t, int64(1), failureRecoveryCount(t, f.owners["CUS"].DB, &domain.Label{}, ""))
	require.Equal(t, int64(1), failureRecoveryCount(t, f.con, &domain.ConversationLabel{}, ""))
	require.Equal(t, int64(1), failureRecoveryCount(t, f.owners["MSG"].DB, &domain.Message{}, ""))
	require.Equal(t, int64(1), failureRecoveryCount(t, f.aut.DB, &domain.LocalChangeJournal{}, "action = ?", "event_outbox"))
	plan, _ := failureRecoveryPlan(t, final)
	for _, owner := range []string{"CUS", "CON", "MSG"} {
		seen := map[string]string{}
		for _, raw := range failureRecoveryCaptures(f, owner) {
			var command foundation.CommandEnvelope
			require.NoError(t, json.Unmarshal([]byte(raw), &command))
			if previous, exists := seen[command.CommandID]; exists {
				require.Equal(t, previous, raw, "a recovered owner command must be byte-for-byte frozen")
			}
			seen[command.CommandID] = raw
		}
	}
	require.True(t, automationEmptyDispatch(plan.Dispatches[4]))
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTFailureRecoveryAggregateCountsPreparationButNotSkippedSteps(t *testing.T) {
	t.Run("only_failed_definitions", func(t *testing.T) {
		f := newAutomationActionFixture(t, false, `[{"action_name":"send_webhook"},{"action_name":"send_message"}]`)
		e := f.call(f.startCommand(), http.StatusOK)
		final := f.drain(e.ID)
		require.Equal(t, "failed", final.Status)
		failureRecoveryStatuses(t, final, "failed", "failed")
		f.assertTarget("open", "medium", 1)
		failureRecoveryReceipts(t, f, "CON", 0)
	})
	t.Run("failed_prepare_skipped_create", func(t *testing.T) {
		f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"never prepared"},`+failureRecoveryForeignAgent+`]`)
		require.NoError(t, f.con.Callback().Create().Before("gorm:create").Register("reject_all_grants", func(tx *gorm.DB) {
			if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.Action == "automation_message_authorization" {
				tx.AddError(errors.New("grant storage rejected"))
			}
		}))
		e := f.call(f.startCommand(), http.StatusOK)
		final := f.drain(e.ID)
		require.Equal(t, "failed", final.Status)
		failureRecoveryStatuses(t, final, "failed", "skipped", "failed")
		f.assertTarget("open", "medium", 1)
		failureRecoveryReceipts(t, f, "CON", 0)
		failureRecoveryReceipts(t, f, "MSG", 0)
	})
	t.Run("retained_label_resolution", func(t *testing.T) {
		f := newAutomationActionFixture(t, false, `[{"action_name":"add_label","action_params":"retained label"}]`)
		e := f.call(f.startCommand(), http.StatusOK)
		f.run()
		dependency := f.owners["CON"].Config.Dependencies["CUS"]
		dependency.Token = "invalid-customer-token"
		f.owners["CON"].Config.Dependencies["CUS"] = dependency
		f.run()
		final := f.execution(e.ID)
		require.Equal(t, "partially_succeeded", final.Status)
		failureRecoveryStatuses(t, final, "succeeded", "failed")
		f.assertTarget("open", "medium", 1)
		require.Equal(t, int64(1), failureRecoveryCount(t, f.owners["CUS"].DB, &domain.Label{}, ""))
		require.Zero(t, failureRecoveryCount(t, f.con, &domain.ConversationLabel{}, ""))
		failureRecoveryReceipts(t, f, "CUS", 1)
		failureRecoveryReceipts(t, f, "CON", 0)
	})
	t.Run("existing_label_resolution_is_successful_owner_progress", func(t *testing.T) {
		f := newAutomationActionFixture(t, false, `[{"action_name":"add_label","action_params":"existing label"}]`)
		label := domain.Label{AccountID: 91, Title: "existing label"}
		require.NoError(t, f.owners["CUS"].DB.Create(&label).Error)
		e := f.call(f.startCommand(), http.StatusOK)
		f.run()
		dependency := f.owners["CON"].Config.Dependencies["CUS"]
		dependency.Token = "invalid-customer-token"
		f.owners["CON"].Config.Dependencies["CUS"] = dependency
		f.run()
		final := f.execution(e.ID)
		require.Equal(t, "partially_succeeded", final.Status)
		failureRecoveryStatuses(t, final, "succeeded", "failed")
		_, results := failureRecoveryPlan(t, final)
		require.Equal(t, label.ID, results[0].ResourceID)
		require.Equal(t, "resolved", results[0].Outcome)
		require.Equal(t, 1, results[0].ObjectVersion)
		require.Equal(t, uint(1), final.RunnerTargetVersion)
		f.assertTarget("open", "medium", 1)
		require.Equal(t, int64(1), failureRecoveryCount(t, f.owners["CUS"].DB, &domain.Label{}, ""))
		require.Zero(t, failureRecoveryCount(t, f.con, &domain.ConversationLabel{}, ""))
		require.Zero(t, failureRecoveryCount(t, f.con, &domain.LocalChangeJournal{}, "action = ?", "event_outbox"))
		failureRecoveryReceipts(t, f, "CUS", 1)
		failureRecoveryReceipts(t, f, "CON", 0)
	})

	t.Run("retained_message_preparation", func(t *testing.T) {
		f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"prepared only"}]`)
		require.NoError(t, f.owners["MSG"].DB.Callback().Create().Before("gorm:create").Register("reject_all_messages", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*domain.Message); ok {
				tx.AddError(errors.New("message storage rejected"))
			}
		}))
		e := f.call(f.startCommand(), http.StatusOK)
		final := f.drain(e.ID)
		require.Equal(t, "partially_succeeded", final.Status)
		failureRecoveryStatuses(t, final, "succeeded", "failed")
		f.assertTarget("open", "medium", 2)
		require.Equal(t, int64(1), failureRecoveryCount(t, f.con, &domain.LocalChangeJournal{}, "action = ?", "automation_message_authorization"))
		require.Zero(t, failureRecoveryCount(t, f.owners["MSG"].DB, &domain.Message{}, ""))
		failureRecoveryReceipts(t, f, "CON", 1)
		failureRecoveryReceipts(t, f, "MSG", 0)
	})
}

func TestAUTFailureRecoveryLegacyV2PendingSiblingKeepsWholePlanRejection(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+failureRecoveryResolve+`]`)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryResolved, `[{"action_name":"send_webhook","action_params":"https://example.invalid/never"},{"action_name":"change_priority","action_params":"urgent"}]`)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	require.Len(t, rows, 2)

	// Rebuild the pre-upgrade rows explicitly. The predecessor uses the old
	// strict compiler and the deferred sibling has no opt-in policy marker.
	first := f.execution(rows[0].ID)
	firstPlan, _ := failureRecoveryPlan(t, first)
	legacyActions, reason := compileAutomationActions(f.rule.Actions)
	require.Empty(t, reason)
	require.NoError(t, f.aut.queueAutomationExecution(context.Background(), firstPlan.command(first), &first, legacyActions))
	pending := siblingRecoveryDecision(t, f.execution(rows[1].ID), "pending")
	pending.Metadata.FailurePolicy = ""
	pending.Metadata.ActivityPolicy = ""
	raw, err := json.Marshal(pending)
	require.NoError(t, err)
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[1].ID).Update("runner_evaluation", string(raw)).Error)
	before := f.execution(rows[1].ID)
	require.Empty(t, before.RunnerContext)

	path := filepath.Join(t.TempDir(), "legacy-v2-pending-sibling.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "legacy-pending-sibling", path))
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	var file snapshotFile
	require.NoError(t, json.Unmarshal(raw, &file))
	var table automationExecutionSnapshotTable
	require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
	table.Codec = "aut.execution.v2"
	file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
	require.NoError(t, err)
	snapshot, err := json.Marshal(file.Snapshot)
	require.NoError(t, err)
	file.Checksum = sha256Bytes(snapshot)
	raw, err = json.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	target := newModuleTestDB(t, "AUT")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "legacy-pending-sibling", path, nil))
	f.aut.DB, f.owners["AUT"].DB = target, target
	assertAUTSnapshotEqual(t, before, f.execution(before.ID))
	firstPlan, _ = failureRecoveryPlan(t, f.execution(first.ID))
	require.Empty(t, firstPlan.FailurePolicy)
	require.Empty(t, siblingRecoveryDecision(t, f.execution(before.ID), "pending").Metadata.FailurePolicy)

	f.run() // The old predecessor confirms CON version 2.
	f.assertTarget("resolved", "medium", 2)
	f.run() // Conditions match, but the old whole-plan compiler rejects it.
	final := f.execution(before.ID)
	require.Equal(t, "failed", final.Status)
	require.Equal(t, "action_capability_executor_unavailable", final.Error)
	require.NotNil(t, final.CompletedAt)
	require.Empty(t, final.RunnerContext, "legacy rejection must never become a continuing owner plan")
	decision := siblingRecoveryDecision(t, final, "matched")
	require.Empty(t, decision.Metadata.FailurePolicy)
	require.Equal(t, uint(2), decision.Version)
	var results []automationActionResult
	require.NoError(t, json.Unmarshal([]byte(final.ActionResults), &results))
	require.Len(t, results, 2)
	require.Equal(t, "failed", results[0].Status)
	require.Equal(t, "skipped", results[1].Status)
	for _, result := range results {
		require.Nil(t, result.LogicalIndex)
		require.Equal(t, "action_capability_executor_unavailable", result.Reason)
		require.Zero(t, result.ObjectVersion)
	}
	f.assertTarget("resolved", "medium", 2)
	failureRecoveryReceipts(t, f, "CON", 1)
	require.Len(t, failureRecoveryCaptures(f, "CON"), 1, "the later supported priority action must not be sent")
	siblingRecoveryIdle(t, f.autRunnerFixture)
	require.NoError(t, validateAutomationSnapshotExecution(final))
}
