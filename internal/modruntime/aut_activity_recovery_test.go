package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const activityRecoveryRemoveAgent = `{"action_name":"remove_assigned_agent"}`
const activityRecoveryRemoveTeam = `{"action_name":"remove_assigned_team"}`

func activityRecoveryInitial(t *testing.T, f *automationActionFixture) domain.Conversation {
	t.Helper()
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"last_activity_at": old, "updated_at": old}).Error)
	return f.conversationState()
}

func activityRecoveryReceipt(t *testing.T, f *automationActionFixture, commandID string) domain.LocalChangeJournal {
	t.Helper()
	var receipt domain.LocalChangeJournal
	require.NoError(t, f.con.Where("action = ? AND command_id = ?", "command_receipt", commandID).First(&receipt).Error)
	return receipt
}

func activityRecoveryRewriteCodec(t *testing.T, path, codec string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var file snapshotFile
	require.NoError(t, json.Unmarshal(raw, &file))
	var table automationExecutionSnapshotTable
	require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
	require.Equal(t, "aut.execution.v5", table.Codec)
	table.Codec = codec
	file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
	require.NoError(t, err)
	snapshot, err := json.Marshal(file.Snapshot)
	require.NoError(t, err)
	file.Checksum = sha256Bytes(snapshot)
	raw, err = json.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
}

// The owner commits once even when the response is lost. Cancellation must first
// reconcile that exact touch, then prevent the later independent owner action.
func TestAUTActivityRecoveryLostResponseReplayAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		name := "continue"
		if cancel {
			name = "cancel_after_commit"
		}
		t.Run(name, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`,`+failureRecoveryResolve+`]`)
			initial := activityRecoveryInitial(t, f)
			e := f.call(f.startCommand(), http.StatusOK)
			f.lost["CON"].Store(true)
			f.run()
			unknown := f.execution(e.ID)
			require.Equal(t, "waiting", unknown.Status)
			failureRecoveryStatuses(t, unknown, "result_unknown", "pending")
			plan, _ := failureRecoveryPlan(t, unknown)
			require.Equal(t, automationTouchValidActivity, plan.ActivityPolicy)
			require.Equal(t, automationTouchValidActivity, plan.Dispatches[0].Input["activity_policy"])
			require.Equal(t, uint64(1), plan.Dispatches[0].Target.ExpectedVersion)
			committed := f.conversationState()
			require.Equal(t, 2, committed.ObjectVersion)
			require.True(t, committed.LastActivityAt.After(initial.LastActivityAt))
			require.Equal(t, initial.Status, committed.Status)
			require.Equal(t, initial.Priority, committed.Priority)
			receipt := activityRecoveryReceipt(t, f, plan.Dispatches[0].CommandID)
			var proof map[string]any
			require.NoError(t, json.Unmarshal([]byte(receipt.AfterState), &proof))
			require.Equal(t, true, proof["activity_only"])
			require.Equal(t, float64(2), proof["object_version"])
			require.Empty(t, cascadeOwnerEvents(t, f, "CON"))
			frozenCaptures := failureRecoveryCaptures(f, "CON")
			require.Len(t, frozenCaptures, 1)
			if cancel {
				cancelled := f.cancel(e.ID)
				require.True(t, cancelled.CancelRequested)
				require.Equal(t, "waiting", cancelled.Status)
			}
			before := f.execution(e.ID)
			f = failureRecoveryRestore(t, f)
			assertAUTSnapshotEqual(t, before, f.execution(e.ID))
			siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
			f.run()
			recovered := f.execution(e.ID)
			plan, results := failureRecoveryPlan(t, recovered)
			require.Equal(t, "activity_touched", results[0].Outcome)
			require.Equal(t, 2, results[0].ObjectVersion)
			require.Equal(t, committed.ObjectVersion, f.conversationState().ObjectVersion)
			require.True(t, committed.LastActivityAt.Equal(f.conversationState().LastActivityAt))
			require.Equal(t, frozenCaptures[0], failureRecoveryCaptures(f, "CON")[0])
			require.Equal(t, receipt.AfterState, activityRecoveryReceipt(t, f, plan.Dispatches[0].CommandID).AfterState)
			failureRecoveryReceipts(t, f, "CON", 1)
			if cancel {
				require.Equal(t, "cancelled", recovered.Status)
				failureRecoveryStatuses(t, recovered, "succeeded", "skipped")
				require.Equal(t, "cancelled_by_request", results[1].Reason)
				require.True(t, automationEmptyDispatch(plan.Dispatches[1]))
				require.Empty(t, cascadeOwnerEvents(t, f, "CON"))
			} else {
				f.run()
				final := f.execution(e.ID)
				require.Equal(t, "succeeded", final.Status)
				plan, _ = failureRecoveryPlan(t, final)
				require.Equal(t, uint64(2), plan.Dispatches[1].Target.ExpectedVersion)
				f.assertTarget("resolved", "medium", 3)
				require.Len(t, cascadeOwnerEvents(t, f, "CON"), 1)
				failureRecoveryReceipts(t, f, "CON", 2)
			}
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTActivityRecoveryCompletionCheckpointFailure(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`]`)
	initial := activityRecoveryInitial(t, f)
	e := f.call(f.startCommand(), http.StatusOK)
	require.NoError(t, f.aut.DB.Callback().Update().Before("gorm:update").Register("reject_activity_completion", func(tx *gorm.DB) {
		if values, ok := tx.Statement.Dest.(map[string]any); ok {
			if _, exists := values["runner_next_action"]; exists {
				tx.AddError(errors.New("activity completion checkpoint unavailable"))
			}
		}
	}))
	ran, err := f.aut.RunAutomationOnce(context.Background())
	require.True(t, ran)
	require.Error(t, err)
	require.NoError(t, f.aut.DB.Callback().Update().Remove("reject_activity_completion"))
	accepted := f.execution(e.ID)
	plan, results := failureRecoveryPlan(t, accepted)
	require.Equal(t, "accepted", results[0].Status)
	require.Equal(t, uint(1), accepted.RunnerTargetVersion)
	committed := f.conversationState()
	require.Equal(t, 2, committed.ObjectVersion)
	require.True(t, committed.LastActivityAt.After(initial.LastActivityAt))
	receipt := activityRecoveryReceipt(t, f, plan.Dispatches[0].CommandID)
	f = failureRecoveryRestore(t, f)
	assertAUTSnapshotEqual(t, accepted, f.execution(e.ID))
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_lease_until", time.Now().UTC().Add(-time.Minute)).Error)
	f.run()
	final := f.execution(e.ID)
	require.Equal(t, "succeeded", final.Status)
	_, results = failureRecoveryPlan(t, final)
	require.Equal(t, "activity_touched", results[0].Outcome)
	require.Equal(t, uint(2), final.RunnerTargetVersion)
	require.True(t, committed.LastActivityAt.Equal(f.conversationState().LastActivityAt))
	require.Equal(t, receipt.AfterState, activityRecoveryReceipt(t, f, plan.Dispatches[0].CommandID).AfterState)
	failureRecoveryReceipts(t, f, "CON", 1)
	require.Empty(t, cascadeOwnerEvents(t, f, "CON"))
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

// Label resolution proves the frozen label identity, not conversation activity.
// Only the succeeding CON step may touch an already-satisfied label operation.
func TestAUTActivityRecoveryLabelBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, actions, outcome string
		attached               bool
	}{
		{"duplicate_attached", `[{"action_name":"add_label","action_params":" vip "}]`, "resolved", true},
		{"missing_removal", `[{"action_name":"remove_label","action_params":" never-created "}]`, "missing", false},
	} {
		for _, interruption := range []string{"lost_cus", "cancel_after_cus", "lost_con"} {
			t.Run(tc.name+"/"+interruption, func(t *testing.T) {
				f := newAutomationActionFixture(t, false, tc.actions)
				var label domain.Label
				if tc.attached {
					label = domain.Label{AccountID: 91, Title: "vip"}
					require.NoError(t, f.owners["CUS"].DB.Create(&label).Error)
					require.NoError(t, f.con.Create(&domain.ConversationLabel{ConversationID: f.conversation.ID, LabelID: label.ID}).Error)
				}
				initial := activityRecoveryInitial(t, f)
				e := f.call(f.startCommand(), http.StatusOK)
				if interruption == "lost_cus" {
					f.lost["CUS"].Store(true)
				}
				f.run()
				f.assertTarget("open", "medium", 1)
				require.True(t, initial.LastActivityAt.Equal(f.conversationState().LastActivityAt))
				failureRecoveryReceipts(t, f, "CON", 0)
				failureRecoveryReceipts(t, f, "CUS", 1)
				if interruption == "lost_cus" {
					unknown := f.execution(e.ID)
					require.Equal(t, "waiting", unknown.Status)
					failureRecoveryStatuses(t, unknown, "result_unknown", "pending")
					plan, _ := failureRecoveryPlan(t, unknown)
					require.True(t, automationEmptyDispatch(plan.Dispatches[1]))
					frozen := failureRecoveryCaptures(f, "CUS")
					require.Len(t, frozen, 1)
					f = failureRecoveryRestore(t, f)
					siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
					f.run()
					require.Equal(t, frozen[0], failureRecoveryCaptures(f, "CUS")[0])
					failureRecoveryReceipts(t, f, "CUS", 1)
				}
				resolved := f.execution(e.ID)
				require.Equal(t, "received", resolved.Status)
				failureRecoveryStatuses(t, resolved, "succeeded", "pending")
				plan, results := failureRecoveryPlan(t, resolved)
				require.Equal(t, tc.outcome, results[0].Outcome)
				require.Equal(t, label.ID, results[0].ResourceID)
				require.Equal(t, 1, results[0].ObjectVersion)
				require.Equal(t, uint(1), resolved.RunnerTargetVersion)
				require.True(t, automationEmptyDispatch(plan.Dispatches[1]))
				f.assertTarget("open", "medium", 1)
				require.True(t, initial.LastActivityAt.Equal(f.conversationState().LastActivityAt))
				if interruption == "cancel_after_cus" {
					f = failureRecoveryRestore(t, f)
					cancelled := f.cancel(e.ID)
					require.Equal(t, "cancelled", cancelled.Status)
					final := f.execution(e.ID)
					failureRecoveryStatuses(t, final, "succeeded", "skipped")
					plan, results = failureRecoveryPlan(t, final)
					require.Equal(t, "cancelled_by_request", results[1].Reason)
					require.True(t, automationEmptyDispatch(plan.Dispatches[1]))
					f.assertTarget("open", "medium", 1)
					require.True(t, initial.LastActivityAt.Equal(f.conversationState().LastActivityAt))
					failureRecoveryReceipts(t, f, "CON", 0)
					require.Empty(t, failureRecoveryCaptures(f, "CON"))
				} else {
					if interruption == "lost_con" {
						f.lost["CON"].Store(true)
					}
					f.run()
					committed := f.conversationState()
					require.Equal(t, 2, committed.ObjectVersion)
					require.True(t, committed.LastActivityAt.After(initial.LastActivityAt))
					plan, _ = failureRecoveryPlan(t, f.execution(e.ID))
					require.Equal(t, automationTouchValidActivity, plan.Dispatches[1].Input["activity_policy"])
					require.Equal(t, uint64(1), plan.Dispatches[1].Target.ExpectedVersion)
					if !tc.attached {
						require.Equal(t, "0", plan.Dispatches[1].Input["label_id"])
						require.Equal(t, "never-created", plan.Dispatches[1].Input["missing_label"])
					} else {
						require.NotContains(t, plan.Dispatches[1].Input, "missing_label")
					}
					receipt := activityRecoveryReceipt(t, f, plan.Dispatches[1].CommandID)
					if interruption == "lost_con" {
						unknown := f.execution(e.ID)
						require.Equal(t, "waiting", unknown.Status)
						failureRecoveryStatuses(t, unknown, "succeeded", "result_unknown")
						frozen := failureRecoveryCaptures(f, "CON")
						require.Len(t, frozen, 1)
						f = failureRecoveryRestore(t, f)
						siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
						f.run()
						require.Equal(t, frozen[0], failureRecoveryCaptures(f, "CON")[0])
						require.Equal(t, receipt.AfterState, activityRecoveryReceipt(t, f, plan.Dispatches[1].CommandID).AfterState)
						require.True(t, committed.LastActivityAt.Equal(f.conversationState().LastActivityAt))
					}
					final := f.execution(e.ID)
					require.Equal(t, "succeeded", final.Status)
					failureRecoveryStatuses(t, final, "succeeded", "succeeded")
					_, results = failureRecoveryPlan(t, final)
					require.Equal(t, "activity_touched", results[1].Outcome)
					require.Equal(t, 2, results[1].ObjectVersion)
					f.assertTarget("open", "medium", 2)
					failureRecoveryReceipts(t, f, "CON", 1)
				}
				wantLabels := int64(0)
				if tc.attached {
					wantLabels = 1
				}
				require.Equal(t, wantLabels, failureRecoveryCount(t, f.con, &domain.ConversationLabel{}, ""))
				require.Equal(t, wantLabels, failureRecoveryCount(t, f.owners["CUS"].DB, &domain.Label{}, ""))
				failureRecoveryReceipts(t, f, "CUS", 1)
				require.Empty(t, cascadeOwnerEvents(t, f, "CON"))
				siblingRecoveryIdle(t, f.autRunnerFixture)
			})
		}
	}
}

func TestAUTActivityRecoveryRejectsSiblingPolicyMismatch(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`]`)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryOpen, `[`+activityRecoveryRemoveTeam+`]`)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	f.run()
	f.run()
	matched := f.execution(rows[1].ID)
	decision := siblingRecoveryDecision(t, matched, "matched")
	require.NoError(t, validateAutomationSnapshotExecution(matched))
	for _, side := range []string{"runner", "evaluation"} {
		t.Run(side, func(t *testing.T) {
			copy := matched
			if side == "runner" {
				plan, _ := failureRecoveryPlan(t, copy)
				plan.ActivityPolicy = ""
				raw, err := json.Marshal(plan)
				require.NoError(t, err)
				copy.RunnerContext = string(raw)
			} else {
				checkpoint := decision
				checkpoint.Metadata.ActivityPolicy = ""
				raw, err := json.Marshal(checkpoint)
				require.NoError(t, err)
				copy.RunnerEvaluation = string(raw)
			}
			_, valid := decodeAutomationSiblingEvaluation(copy)
			require.False(t, valid, "both individually supported policies must still agree")
			require.Error(t, validateAutomationSnapshotExecution(copy))
		})
	}
	decision.Metadata.ActivityPolicy = ""
	raw, err := json.Marshal(decision)
	require.NoError(t, err)
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", matched.ID).Update("runner_evaluation", string(raw)).Error)
	f.run()
	quarantined := f.execution(matched.ID)
	require.Equal(t, "manual_action_required", quarantined.Status)
	f.assertTarget("open", "medium", 2)
	failureRecoveryReceipts(t, f, "CON", 1)
	require.Len(t, failureRecoveryCaptures(f, "CON"), 1)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTActivityRecoverySnapshotsFreezeNewDeferredPolicy(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`]`)
	activityRecoveryInitial(t, f)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryOpen, `[`+activityRecoveryRemoveTeam+`]`)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	require.Len(t, rows, 2)
	firstPlan, _ := failureRecoveryPlan(t, rows[0])
	require.Equal(t, automationTouchValidActivity, firstPlan.ActivityPolicy)
	pending := siblingRecoveryDecision(t, rows[1], "pending")
	require.Equal(t, automationTouchValidActivity, pending.Metadata.ActivityPolicy)
	encoded, err := marshalAutomationSnapshot(rows)
	require.NoError(t, err)
	var table automationExecutionSnapshotTable
	require.NoError(t, json.Unmarshal(encoded, &table))
	require.Equal(t, "aut.execution.v5", table.Codec)
	f = failureRecoveryRestore(t, f)
	for _, row := range rows {
		assertAUTSnapshotEqual(t, row, f.execution(row.ID))
	}
	f.run()
	f.assertTarget("open", "medium", 2)
	f.run()
	matched := f.execution(rows[1].ID)
	decision := siblingRecoveryDecision(t, matched, "matched")
	require.Equal(t, uint(2), decision.Version)
	plan, _ := failureRecoveryPlan(t, matched)
	require.Equal(t, automationTouchValidActivity, plan.ActivityPolicy)
	// Restore the decision checkpoint into a fresh AUT store independently of
	// owners, using a distinct import identity from the initial all-owner restore.
	path := filepath.Join(t.TempDir(), "matched-activity.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "activity-matched", path))
	target := newModuleTestDB(t, "AUT")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "activity-matched", path, nil))
	f.aut.DB, f.owners["AUT"].DB = target, target
	assertAUTSnapshotEqual(t, matched, f.execution(matched.ID))
	f.run()
	final := f.execution(rows[1].ID)
	require.Equal(t, "succeeded", final.Status)
	plan, results := failureRecoveryPlan(t, final)
	require.Equal(t, uint64(2), plan.Dispatches[0].Target.ExpectedVersion)
	require.Equal(t, "activity_touched", results[0].Outcome)
	f.assertTarget("open", "medium", 3)
	failureRecoveryReceipts(t, f, "CON", 2)
	require.Empty(t, cascadeOwnerEvents(t, f, "CON"))
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTActivityRecoveryLegacyV3PendingRunnerAndSiblingKeepNoop(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`]`)
	initial := activityRecoveryInitial(t, f)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryOpen, `[`+activityRecoveryRemoveTeam+`]`)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	require.Len(t, rows, 2)
	first := f.execution(rows[0].ID)
	plan, _ := failureRecoveryPlan(t, first)
	actions, reason := compileAutomationContinuingActions(f.rule.Actions)
	require.Empty(t, reason)
	// An omitted optional policy models a v3 queue, preserving continuation
	// semantics without silently opting its old no-ops into activity changes.
	require.NoError(t, f.aut.queueAutomationExecution(context.Background(), plan.command(first), &first, actions))
	pending := siblingRecoveryDecision(t, rows[1], "pending")
	pending.Metadata.ActivityPolicy = ""
	raw, err := json.Marshal(pending)
	require.NoError(t, err)
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[1].ID).Update("runner_evaluation", string(raw)).Error)
	beforeFirst, beforeSibling := f.execution(rows[0].ID), f.execution(rows[1].ID)
	path := filepath.Join(t.TempDir(), "legacy-v3-activity.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "legacy-activity", path))
	activityRecoveryRewriteCodec(t, path, "aut.execution.v3")
	target := newModuleTestDB(t, "AUT")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "legacy-activity", path, nil))
	f.aut.DB, f.owners["AUT"].DB = target, target
	assertAUTSnapshotEqual(t, beforeFirst, f.execution(beforeFirst.ID))
	assertAUTSnapshotEqual(t, beforeSibling, f.execution(beforeSibling.ID))
	f.run()
	f.assertTarget("open", "medium", 1)
	f.run()
	decision := siblingRecoveryDecision(t, f.execution(rows[1].ID), "matched")
	require.Empty(t, decision.Metadata.ActivityPolicy)
	require.Equal(t, uint(1), decision.Version)
	f.run()
	for _, row := range rows {
		final := f.execution(row.ID)
		require.Equal(t, "succeeded", final.Status)
		plan, results := failureRecoveryPlan(t, final)
		require.Empty(t, plan.ActivityPolicy)
		require.Equal(t, automationContinueDefinite, plan.FailurePolicy)
		require.NotContains(t, plan.Dispatches[0].Input, "activity_policy")
		require.Empty(t, results[0].Outcome)
		require.Equal(t, 1, results[0].ObjectVersion)
		receipt := activityRecoveryReceipt(t, f, plan.Dispatches[0].CommandID)
		var old map[string]any
		require.NoError(t, json.Unmarshal([]byte(receipt.AfterState), &old))
		require.NotContains(t, old, "activity_only")
		callAutomationOwner(t, f.owners["CON"], plan.Dispatches[0], http.StatusOK)
		require.Equal(t, receipt.AfterState, activityRecoveryReceipt(t, f, plan.Dispatches[0].CommandID).AfterState)
	}
	current := f.conversationState()
	require.Equal(t, initial.ObjectVersion, current.ObjectVersion)
	require.True(t, initial.LastActivityAt.Equal(current.LastActivityAt))
	require.True(t, initial.UpdatedAt.Equal(current.UpdatedAt))
	failureRecoveryReceipts(t, f, "CON", 2)
	require.Empty(t, cascadeOwnerEvents(t, f, "CON"))
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTActivityRecoveryLegacyCodecsRejectNewPolicy(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`]`)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryOpen, `[`+activityRecoveryRemoveTeam+`]`)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	for _, which := range []string{"runner", "deferred"} {
		for _, codec := range []string{"aut.execution.v1", "aut.execution.v2", "aut.execution.v3"} {
			t.Run(which+"/"+codec, func(t *testing.T) {
				row := rows[0]
				if which == "deferred" {
					row = rows[1]
				}
				raw, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{row})
				require.NoError(t, err)
				var table automationExecutionSnapshotTable
				require.NoError(t, json.Unmarshal(raw, &table))
				table.Codec = codec
				raw, err = json.Marshal(table)
				require.NoError(t, err)
				var decoded []domain.AutomationRuleExecution
				require.Error(t, unmarshalAutomationSnapshot(raw, &decoded), "old codecs cannot claim new activity semantics")
				require.Nil(t, decoded)
			})
		}
	}
}

func TestAUTActivityRecoveryManualInterleaveDoesNotAdoptLiveVersion(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`,`+activityRecoveryRemoveTeam+`,`+failureRecoveryResolve+`]`)
	activityRecoveryInitial(t, f)
	e := f.call(f.startCommand(), http.StatusOK)
	f.lost["CON"].Store(true)
	f.run()
	f.assertTarget("open", "medium", 2)
	require.NoError(t, repository.NewConversationRepository(f.con).UpdateStatus(91, f.conversation.ID, "pending", nil))
	manual := f.conversationState()
	require.Equal(t, 3, manual.ObjectVersion)
	siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
	f.run()
	reconciled := f.execution(e.ID)
	require.Equal(t, uint(2), reconciled.RunnerTargetVersion)
	failureRecoveryStatuses(t, reconciled, "succeeded", "pending", "pending")
	final := f.drain(e.ID)
	require.Equal(t, "partially_succeeded", final.Status)
	failureRecoveryStatuses(t, final, "succeeded", "failed", "failed")
	plan, results := failureRecoveryPlan(t, final)
	require.Equal(t, "activity_touched", results[0].Outcome)
	require.Equal(t, uint64(2), plan.Dispatches[1].Target.ExpectedVersion)
	require.Equal(t, uint64(2), plan.Dispatches[2].Target.ExpectedVersion)
	require.Equal(t, uint(2), final.RunnerTargetVersion)
	f.assertTarget("pending", "medium", 3)
	require.True(t, manual.LastActivityAt.Equal(f.conversationState().LastActivityAt))
	failureRecoveryReceipts(t, f, "CON", 1)
	restored := failureRecoveryRestore(t, f)
	assertAUTSnapshotEqual(t, final, restored.execution(e.ID))
	siblingRecoveryIdle(t, restored.autRunnerFixture)
}

func TestAUTActivityRecoveryRejectsCorruptPolicyAndProof(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`,`+failureRecoveryResolve+`]`)
	e := f.call(f.startCommand(), http.StatusOK)
	f.run()
	valid := f.execution(e.ID)
	plan, results := failureRecoveryPlan(t, valid)
	require.True(t, validAutomationCheckpoint(valid, plan, results))
	require.NoError(t, validateAutomationSnapshotExecution(valid))
	cases := map[string]func(*domain.AutomationRuleExecution, *automationRunnerContext, []automationActionResult){
		"unknown_policy": func(_ *domain.AutomationRuleExecution, plan *automationRunnerContext, _ []automationActionResult) {
			plan.ActivityPolicy = "touch_valid.v99"
		},
		"missing_dispatch_policy": func(_ *domain.AutomationRuleExecution, plan *automationRunnerContext, _ []automationActionResult) {
			delete(plan.Dispatches[0].Input, "activity_policy")
		},
		"changed_dispatch_policy": func(_ *domain.AutomationRuleExecution, plan *automationRunnerContext, _ []automationActionResult) {
			plan.Dispatches[0].Input["activity_policy"] = "touch_valid.v99"
		},
		"legacy_claims_touch": func(_ *domain.AutomationRuleExecution, plan *automationRunnerContext, _ []automationActionResult) {
			plan.ActivityPolicy = ""
			delete(plan.Dispatches[0].Input, "activity_policy")
		},
		"touch_without_version_advance": func(e *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			e.RunnerTargetVersion = 1
			results[0].ObjectVersion = 1
		},
		"touch_with_extra_version": func(e *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			e.RunnerTargetVersion = 3
			results[0].ObjectVersion = 3
		},
		"unknown_outcome": func(_ *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			results[0].Outcome = "invented_activity"
		},
		"touch_claims_resource": func(_ *domain.AutomationRuleExecution, _ *automationRunnerContext, results []automationActionResult) {
			results[0].ResourceID = 999
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
	// A malformed durable policy is quarantined before any remaining dispatch.
	plan.ActivityPolicy = "touch_valid.v99"
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", e.ID).Update("runner_context", string(raw)).Error)
	f.run()
	quarantined := f.execution(e.ID)
	require.Equal(t, "manual_action_required", quarantined.Status)
	require.Equal(t, "invalid_runner_checkpoint", quarantined.Error)
	f.assertTarget("open", "medium", 2)
	failureRecoveryReceipts(t, f, "CON", 1)
	require.Len(t, failureRecoveryCaptures(f, "CON"), 1)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

// An activity-only version increment contributes causal progress, but cannot
// invent a missing child event that blocks a later real message/change event.
func TestAUTActivityRecoveryCascadeCompletenessAndHistory(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[`+activityRecoveryRemoveAgent+`,{"action_name":"private_note","action_params":"activity recovery note"},`+activityRecoveryRemoveTeam+`,`+failureRecoveryPriority+`]`)
	activityRecoveryInitial(t, f)
	cascadeRule(t, f, "message_created", "[]", `[]`)
	e := f.call(f.startCommand(), http.StatusOK)
	final := f.drain(e.ID)
	require.Equal(t, "succeeded", final.Status)
	f.assertTarget("open", "high", 5)
	plan, results := failureRecoveryPlan(t, final)
	require.Equal(t, "activity_touched", results[0].Outcome)
	require.Equal(t, "activity_touched", results[3].Outcome)
	version, children, err := automationCascadeBatch(f.aut.DB, final)
	require.NoError(t, err)
	require.Equal(t, uint(5), version)
	require.Len(t, children, 2)
	require.Equal(t, plan.Dispatches[2].CommandID, children[0].command.CommandID)
	require.Equal(t, plan.Dispatches[4].CommandID, children[1].command.CommandID)
	messageEvents := cascadeOwnerEvents(t, f, "MSG")
	conversationEvents := cascadeOwnerEvents(t, f, "CON")
	require.Len(t, messageEvents, 1)
	require.Len(t, conversationEvents, 1)
	// Real predecessor delivery still matters. Excluding phantom activity facts
	// must not accidentally exclude or reorder an actual earlier message fact.
	_, err = automationCascadeTarget(f.aut.DB, conversationEvents[0])
	require.ErrorIs(t, err, errAutomationCascadePending)
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), messageEvents[0]))
	child := bridgeExecution(t, f.autRunnerFixture)
	require.Equal(t, uint(5), child.TargetVersion)
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), conversationEvents[0]))
	require.Zero(t, failureRecoveryCount(t, f.aut.DB, &domain.LocalChangeJournal{}, "action = ?", "automation_fact_quarantine"))
	var ruleVersion domain.AutomationRuleVersion
	require.NoError(t, f.aut.DB.Where("rule_id = ? AND version = ?", final.RuleID, final.RuleVersion).First(&ruleVersion).Error)
	view, entries := historyProjection(t, final, &ruleVersion)
	require.Equal(t, "logical", view.ActionResultsState)
	require.Len(t, entries, 4)
	for _, index := range []int{0, 2} {
		require.Equal(t, "success", entries[index].Status)
		require.True(t, entries[index].Completed)
		require.Equal(t, "activity_touched", entries[index].Outcome)
	}
	require.NotContains(t, view.ActionResults, "activity recovery note")
	require.NotContains(t, view.ActionResults, "activity_policy")
	restored := failureRecoveryRestore(t, f)
	assertAUTSnapshotEqual(t, final, restored.execution(e.ID))
	_, restoredEntries := historyProjection(t, restored.execution(e.ID), &ruleVersion)
	require.Equal(t, entries, restoredEntries)
	siblingRecoveryIdle(t, restored.autRunnerFixture)
}

// Gated by DB_DSN; the parent task runs this through the native PostgreSQL
// wrapper. Fresh worker values contend on leases and recover one lost response.
func TestPostgresAUTActivityRecoveryConcurrentWorkers(t *testing.T) {
	f := newAutomationActionFixture(t, true, `[`+activityRecoveryRemoveAgent+`,`+activityRecoveryRemoveTeam+`,`+activityRecoveryRemoveAgent+`]`)
	initial := activityRecoveryInitial(t, f)
	e := f.call(f.startCommand(), http.StatusOK)
	f.lost["CON"].Store(true)
	for round := 0; round < 12; round++ {
		current := f.execution(e.ID)
		if current.Status == "succeeded" {
			break
		}
		if current.RunnerRetryAt != nil {
			siblingRecoveryRetryNow(t, f.autRunnerFixture, e.ID)
		}
		start, done := make(chan struct{}), make(chan error, 8)
		for worker := 0; worker < 8; worker++ {
			instance := Runtime{Config: f.aut.Config, DB: f.aut.DB, Contracts: f.aut.Contracts}
			go func() { <-start; _, err := instance.RunAutomationOnce(context.Background()); done <- err }()
		}
		close(start)
		for worker := 0; worker < 8; worker++ {
			require.NoError(t, <-done)
		}
	}
	final := f.execution(e.ID)
	require.Equal(t, "succeeded", final.Status)
	failureRecoveryStatuses(t, final, "succeeded", "succeeded", "succeeded")
	f.assertTarget("open", "medium", 4)
	require.True(t, f.conversationState().LastActivityAt.After(initial.LastActivityAt))
	plan, results := failureRecoveryPlan(t, final)
	for i, result := range results {
		require.Equal(t, "activity_touched", result.Outcome)
		require.Equal(t, i+2, result.ObjectVersion)
		require.Equal(t, uint64(i+1), plan.Dispatches[i].Target.ExpectedVersion)
		require.Equal(t, automationTouchValidActivity, plan.Dispatches[i].Input["activity_policy"])
	}
	captures := failureRecoveryCaptures(f, "CON")
	require.GreaterOrEqual(t, len(captures), 4)
	seen := map[string]string{}
	for _, raw := range captures {
		var command foundation.CommandEnvelope
		require.NoError(t, json.Unmarshal([]byte(raw), &command))
		if previous, exists := seen[command.CommandID]; exists {
			require.Equal(t, previous, raw, "recovery must replay the exact frozen activity command")
		}
		seen[command.CommandID] = raw
	}
	require.Len(t, seen, 3)
	failureRecoveryReceipts(t, f, "CON", 3)
	require.Empty(t, cascadeOwnerEvents(t, f, "CON"))
	require.Equal(t, int64(1), failureRecoveryCount(t, f.aut.DB, &domain.LocalChangeJournal{}, "action = ?", "event_outbox"))
	siblingRecoveryIdle(t, f.autRunnerFixture)
}
