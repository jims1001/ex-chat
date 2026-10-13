package modruntime

import (
	"encoding/json"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

var errAutomationCausalCheckpoint = errors.New("automation predecessor checkpoint cannot prove owner causality")

// automationPredecessorVersion reconstructs the version from this fact's ordered
// durable checkpoints. It never reads mutable CON state. Terminal no-op, failed,
// and cancelled runs carry the preceding version; only confirmed owner effects
// advance it. An unknown/quarantined outcome cannot authorize a later rule.
func automationPredecessorVersion(tx *gorm.DB, execution domain.AutomationRuleExecution) (uint, bool, error) {
	if execution.FactBatchKey == "" {
		if execution.RunnerPredecessorID != nil {
			return 0, false, errAutomationCausalCheckpoint
		}
		return execution.TargetVersion, true, nil
	}
	var receipt domain.LocalChangeJournal
	if err := tx.Where("change_id = ? AND source_module = ? AND account_id = ? AND event_id = ? AND action = ?", execution.FactBatchKey, "AUT", execution.AccountID, execution.EventID, "automation_fact_received").First(&receipt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, errAutomationCausalCheckpoint
		}
		return 0, false, err
	}
	if err := validateAutomationFactBatch(tx, receipt); err != nil {
		return 0, false, errAutomationCausalCheckpoint
	}
	var event foundation.EventEnvelope
	if json.Unmarshal([]byte(receipt.AfterState), &event) != nil || event.Validate() != nil || automationFactReceipt(event) != execution.FactBatchKey || event.AccountID != execution.AccountID || event.EventID != execution.EventID {
		return 0, false, errAutomationCausalCheckpoint
	}
	targetID, targetErr := uintFilter(event.Payload, "conversation_id")
	targetVersion, versionErr := uintFilter(event.Payload, "object_version")
	if targetErr != nil || versionErr != nil || targetID != execution.ConversationID || targetVersion != execution.TargetVersion {
		return 0, false, errAutomationCausalCheckpoint
	}
	names, namesErr := automationFactEventNames(event)
	if namesErr != nil || !names[execution.EventName] || !validAutomationRetryExecution(event, execution) {
		return 0, false, errAutomationCausalCheckpoint
	}
	var preceding []domain.AutomationRuleExecution
	if err := tx.Where("account_id = ? AND fact_batch_key = ? AND id < ?", execution.AccountID, execution.FactBatchKey, execution.ID).Order("id ASC").Find(&preceding).Error; err != nil {
		return 0, false, err
	}
	version := execution.TargetVersion
	var previousID, previousRule uint
	for _, p := range preceding {
		if !names[p.EventName] || !validAutomationRetryExecution(event, p) || p.ConversationID != execution.ConversationID || p.EventID != execution.EventID || p.TargetVersion != execution.TargetVersion || p.AutomationDepth != execution.AutomationDepth || !automationOptionalIDEqual(p.OriginExecutionID, execution.OriginExecutionID) || !automationOptionalIDEqual(p.MessageID, execution.MessageID) || p.RuleID <= previousRule || !automationPredecessorMatches(p.RunnerPredecessorID, previousID) {
			return 0, false, errAutomationCausalCheckpoint
		}
		var ready bool
		var err error
		version, ready, err = automationTerminalExecutionVersion(p, version)
		if err != nil || !ready {
			return 0, ready, err
		}
		previousID, previousRule = p.ID, p.RuleID
	}
	if execution.RuleID <= previousRule || !automationPredecessorMatches(execution.RunnerPredecessorID, previousID) {
		return 0, false, errAutomationCausalCheckpoint
	}
	return version, true, nil
}

func automationPredecessorMatches(id *uint, previous uint) bool {
	return (previous == 0 && id == nil) || (previous != 0 && id != nil && *id == previous)
}

func automationOptionalIDEqual(a, b *uint) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// A condition miss is decided before dispatch and can never contain an owner
// receipt prefix. Treat a restored claimed/mutated no-match as corrupt.
func validAutomationNoMatch(e domain.AutomationRuleExecution) bool {
	if e.Status != "no_match" || e.Error != "conditions_not_matched" || e.CompletedAt == nil || e.ExecutionKey == nil || e.TargetVersion == 0 || e.RunnerContext != "" || e.RunnerNextAction != 0 || e.RunnerTargetVersion != e.TargetVersion || e.RunnerLease != "" || e.RunnerLeaseUntil != nil || e.RunnerRetryAt != nil || e.CancelRequested {
		return false
	}
	if e.RunnerEvaluation != "" {
		evaluation, valid := decodeAutomationSiblingEvaluation(e)
		if !valid || evaluation.Decision != "no_match" {
			return false
		}
	}
	var results []automationActionResult
	if decodeStrictSnapshotJSON([]byte(e.ActionResults), &results) != nil || results == nil {
		return false
	}
	for i, result := range results {
		if result.LogicalIndex != nil || result.Index != i || result.Status != "skipped" || result.Reason != "conditions_not_matched" || result.Capability != "" || result.Step != "" || result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "" {
			return false
		}
	}
	return true
}

// A terminal checkpoint carries only its confirmed owner receipt prefix. Unknown,
// active, and quarantined checkpoints cannot authorize another event or action.
func automationTerminalExecutionVersion(p domain.AutomationRuleExecution, version uint) (uint, bool, error) {
	switch p.Status {
	case "received", "evaluating", "running", "waiting":
		return 0, false, nil
	case "succeeded", "failed", "partially_succeeded", "cancelled", "loop_blocked", "no_match":
	default:
		return 0, false, errAutomationCausalCheckpoint
	}
	if p.RunnerEvaluation != "" {
		evaluation, valid := decodeAutomationSiblingEvaluation(p)
		if !valid || (evaluation.Decision == "pending" && p.Status != "cancelled") || (evaluation.Decision != "pending" && evaluation.Version != version) {
			return 0, false, errAutomationCausalCheckpoint
		}
	}
	if p.Status == "no_match" && !validAutomationNoMatch(p) {
		return 0, false, errAutomationCausalCheckpoint
	}
	if p.CompletedAt == nil {
		return 0, false, errAutomationCausalCheckpoint
	}
	if p.RunnerContext == "" {
		if p.RunnerNextAction != 0 || p.RunnerTargetVersion != p.TargetVersion {
			return 0, false, errAutomationCausalCheckpoint
		}
	} else {
		var runner automationRunnerContext
		var results []automationActionResult
		if json.Unmarshal([]byte(p.RunnerContext), &runner) != nil || json.Unmarshal([]byte(p.ActionResults), &results) != nil || len(results) == 0 || len(results) != len(runner.Actions) || !validAutomationRunnerPlan(runner) || !validAutomationRunnerIdentity(p, runner) || p.RunnerNextAction < 0 || p.RunnerNextAction > len(results) {
			return 0, false, errAutomationCausalCheckpoint
		}
		if runner.FailurePolicy == automationContinueDefinite {
			if !validAutomationContinuingCheckpoint(p, runner, results, true, version) {
				return 0, false, errAutomationCausalCheckpoint
			}
			next, valid := automationPrefixVersion(runner, results, p.RunnerNextAction, version)
			if !valid {
				return 0, false, errAutomationCausalCheckpoint
			}
			return next, true, nil
		}
		for i, result := range results {
			if !validAutomationLocalAction(runner.Actions[i]) || result.Index != i || result.Capability != runner.Actions[i].Capability || result.Step != automationActionStep(runner.Actions[i]) {
				return 0, false, errAutomationCausalCheckpoint
			}
			if i < p.RunnerNextAction {
				next, valid := automationResultAdvances(runner.Actions[i], result, version, runner.ActivityPolicy)
				if !valid || !validAutomationDispatch(runner, p, results, i, version) {
					return 0, false, errAutomationCausalCheckpoint
				}
				version = next
			} else if (result.Status != "skipped" && result.Status != "failed") || result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "" {
				return 0, false, errAutomationCausalCheckpoint
			}
		}
		if p.RunnerTargetVersion != version && (p.RunnerNextAction != 0 || p.RunnerTargetVersion != p.TargetVersion) {
			return 0, false, errAutomationCausalCheckpoint
		}
	}
	return version, true, nil
}
