package modruntime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Each compiled step has exactly one owner. Cross-owner actions are ordered
// durable steps, never a shared transaction or synchronous owner dependency cycle.
type automationLocalAction struct {
	LogicalIndex  *int   `json:"logical_action_index,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	Capability    string `json:"capability"`
	Command       string `json:"command"`
	Field         string `json:"field"`
	Value         string `json:"value"`
	Owner         string `json:"owner,omitempty"`
	Operation     string `json:"operation,omitempty"`
	Private       bool   `json:"private,omitempty"`
}
type automationRunnerContext struct {
	ActivityPolicy string                   `json:"activity_policy,omitempty"`
	FailurePolicy  string                   `json:"failure_policy,omitempty"`
	Actor          foundation.ContractActor `json:"actor"`
	CorrelationID  string                   `json:"correlation_id"`
	CausationID    string                   `json:"causation_id"`
	RequestedAt    time.Time                `json:"requested_at"`
	Actions        []automationLocalAction  `json:"actions"`
	// Private, immutable command envelopes are committed before sending. Their
	// expected version, label IDs, timestamps and content survive unknown results.
	Dispatches []foundation.CommandEnvelope `json:"dispatches,omitempty"`
}
type automationActionResult struct {
	LogicalIndex  *int   `json:"logical_action_index,omitempty"`
	Index         int    `json:"action_index"`
	Capability    string `json:"capability,omitempty"`
	Step          string `json:"step,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason_code,omitempty"`
	ObjectVersion int    `json:"object_version,omitempty"`
	ResourceID    uint   `json:"resource_id,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
}

func (r *Runtime) queueAutomationExecution(ctx context.Context, command foundation.CommandEnvelope, e *domain.AutomationRuleExecution, actions []automationLocalAction, activityPolicy ...string) error {
	if len(activityPolicy) > 1 || (len(activityPolicy) == 1 && !validAutomationActivityPolicy(activityPolicy[0])) {
		return errors.New("invalid frozen automation activity policy")
	}
	runner := automationRunnerContext{Actor: foundation.ContractActor{Type: "System", ID: "automation:" + strconv.Itoa(int(e.RuleID)), AuthSource: "module"}, CorrelationID: command.CorrelationID, CausationID: command.CommandID, RequestedAt: command.RequestedAt, Actions: actions, Dispatches: make([]foundation.CommandEnvelope, len(actions))}
	if len(activityPolicy) == 1 {
		runner.ActivityPolicy = activityPolicy[0]
	}
	if len(actions) > 0 && actions[0].LogicalIndex != nil {
		runner.FailurePolicy = automationContinueDefinite
	}
	encoded, err := json.Marshal(runner)
	if err != nil {
		return err
	}
	e.RunnerContext = string(encoded)
	e.RunnerTargetVersion = e.TargetVersion
	results := make([]automationActionResult, len(actions))
	for i, a := range actions {
		results[i] = automationActionResult{LogicalIndex: a.LogicalIndex, Index: i, Capability: a.Capability, Step: automationActionStep(a), Status: "pending"}
	}
	encoded, err = json.Marshal(results)
	if err != nil {
		return err
	}
	e.ActionResults = string(encoded)
	return Transaction(ctx, r.DB).Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ?", e.AccountID, e.ID).Updates(map[string]any{"runner_context": e.RunnerContext, "runner_target_version": e.RunnerTargetVersion, "action_results": e.ActionResults}).Error
}

// RunAutomation executes at most one durable action per pass. No network command
// is sent inside an AUT transaction. Leases and stable owner command receipts allow
// recovery after a lost response, process crash, or local checkpoint rollback.
func (r Runtime) RunAutomation(ctx context.Context, onError func(error)) {
	if r.Config.ModuleID != "AUT" || !r.Config.EnableBusiness {
		return
	}
	interval := r.Config.EventPollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := r.RunAutomationOnce(ctx); err != nil && onError != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r Runtime) RunAutomationOnce(ctx context.Context) (bool, error) {
	if r.Config.ModuleID != "AUT" {
		return false, nil
	}
	now := time.Now().UTC()
	lease := foundation.GenerateUUID()
	ttl := 2*r.Config.MaxTimeout + 30*time.Second
	var execution domain.AutomationRuleExecution
	var runner automationRunnerContext
	var results []automationActionResult
	claimed, quarantined := false, false
	evaluating := false
	var evaluation automationSiblingEvaluation
	var projected conversationView
	var evaluationVersion uint
	reconciling := false
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Where("(runner_context <> ? OR COALESCE(fact_batch_key, '') <> '') AND status IN ? AND (runner_lease_until IS NULL OR runner_lease_until < ?) AND (runner_retry_at IS NULL OR runner_retry_at <= ?)", "", []string{"received", "evaluating", "running", "waiting"}, now, now).
			Where(`(COALESCE(fact_batch_key, '') = '' OR NOT EXISTS (
    SELECT 1 FROM automation_rule_executions AS prior
    WHERE prior.account_id = automation_rule_executions.account_id
    AND prior.fact_batch_key = automation_rule_executions.fact_batch_key
    AND prior.id < automation_rule_executions.id
    AND prior.status IN ?))`, []string{"received", "evaluating", "running", "waiting"}).Order("id ASC")
		if tx.Dialector.Name() == "postgres" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := q.First(&execution).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		if execution.RunnerEvaluation != "" && execution.RunnerContext == "" {
			var valid bool
			evaluation, valid = decodeAutomationSiblingEvaluation(execution)
			if !valid || evaluation.Decision != "pending" {
				quarantined = true
				return r.finishAutomationExecution(context.WithValue(ctx, transactionKey{}, tx), foundation.CommandEnvelope{AccountID: execution.AccountID, CommandID: "aut-evaluation-quarantine", CorrelationID: "aut-evaluation-quarantine"}, &execution, "manual_action_required", "invalid_runner_checkpoint")
			}
			version, ready, err := automationPredecessorVersion(tx, execution)
			if errors.Is(err, errAutomationCausalCheckpoint) {
				quarantined = true
				return r.finishAutomationExecution(context.WithValue(ctx, transactionKey{}, tx), evaluation.Metadata.command(execution), &execution, "manual_action_required", "causal_predecessor_unverified")
			}
			if err != nil || !ready {
				return err
			}
			projected, err = projectAutomationSiblingConversation(tx, execution, evaluation.Initial.Conversation)
			if err != nil {
				return err
			}
			evaluationVersion = version
			until := now.Add(ttl)
			update := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ? AND (runner_lease_until IS NULL OR runner_lease_until < ?)", execution.AccountID, execution.ID, now).Updates(map[string]any{"runner_lease": lease, "runner_lease_until": until, "runner_retry_at": nil})
			if update.Error != nil {
				return update.Error
			}
			claimed, evaluating = update.RowsAffected == 1, true
			return nil
		}
		if json.Unmarshal([]byte(execution.RunnerContext), &runner) != nil || json.Unmarshal([]byte(execution.ActionResults), &results) != nil || len(runner.Actions) == 0 || len(results) != len(runner.Actions) || execution.RunnerNextAction < 0 || execution.RunnerNextAction >= len(results) || !validAutomationCheckpoint(execution, runner, results) {

			// Do not let one corrupt oldest row starve every later execution. It may
			// already have an unknown owner outcome, so require manual action rather
			// than pretending that no side effect occurred.
			quarantined = true
			command := foundation.CommandEnvelope{CommandID: fmt.Sprintf("aut-quarantine:%d", execution.ID), AccountID: execution.AccountID, Actor: foundation.ContractActor{Type: "System", ID: "automation-runner", AuthSource: "module"}, CorrelationID: fmt.Sprintf("aut-quarantine:%d", execution.ID)}
			return r.finishAutomationExecution(context.WithValue(ctx, transactionKey{}, tx), command, &execution, "manual_action_required", "invalid_runner_checkpoint")
		}
		version, ready, causalErr := automationPredecessorVersion(tx, execution)
		if causalErr == nil && ready && execution.RunnerEvaluation != "" {
			evaluation, valid := decodeAutomationSiblingEvaluation(execution)
			if !valid || evaluation.Version != version {
				causalErr = errAutomationCausalCheckpoint
			}
		}
		if causalErr == nil && ready && !(execution.RunnerNextAction == 0 && results[0].Status == "pending") {
			proved, valid := automationPrefixVersion(runner, results, execution.RunnerNextAction, version)
			if !valid || execution.RunnerTargetVersion != proved {
				causalErr = errAutomationCausalCheckpoint
			}
		}
		if errors.Is(causalErr, errAutomationCausalCheckpoint) {
			quarantined = true
			return r.finishAutomationExecution(context.WithValue(ctx, transactionKey{}, tx), runner.command(execution), &execution, "manual_action_required", "causal_predecessor_unverified")
		}
		if causalErr != nil {
			return causalErr
		}
		if !ready {
			return nil
		}
		if execution.RunnerNextAction == 0 && results[0].Status == "pending" {
			execution.RunnerTargetVersion = version
		}

		index := execution.RunnerNextAction
		reconciling = results[index].Status == "accepted" || results[index].Status == "result_unknown"
		// An accepted/unknown action must first reconcile its stable command even if a
		// cancellation arrived. Cancellation only prevents starting the next action.
		if execution.CancelRequested && results[index].Status == "pending" {
			for i := index; i < len(results); i++ {
				results[i].Status = "skipped"
				results[i].Reason = "cancelled_by_request"
			}
			raw, _ := json.Marshal(results)
			execution.ActionResults = string(raw)
			return r.finishAutomationExecution(context.WithValue(ctx, transactionKey{}, tx), runner.command(execution), &execution, "cancelled", "cancelled_by_request")
		}
		// Freeze the complete command before dispatch, including values obtained
		// from earlier owners and dynamic snooze deadlines. Never rebuild retries.
		if len(runner.Dispatches) == 0 {
			runner.Dispatches = make([]foundation.CommandEnvelope, len(runner.Actions))
			// Expand the phase9 status/priority codec without losing a confirmed
			// prefix. Its commands contain no dynamic values.
			migrated := execution
			migrated.RunnerTargetVersion = version
			for previous := 0; previous < index; previous++ {
				runner.Dispatches[previous] = automationDispatch(runner, migrated, results, previous, int(r.Config.MaxTimeout.Milliseconds()), runner.RequestedAt)
				migrated.RunnerTargetVersion = uint(results[previous].ObjectVersion)
			}
		}
		if runner.Actions[index].FailureReason == "" && automationEmptyDispatch(runner.Dispatches[index]) {
			runner.Dispatches[index] = automationDispatch(runner, execution, results, index, int(r.Config.MaxTimeout.Milliseconds()), now)
		}
		if !validAutomationDispatch(runner, execution, results, index, execution.RunnerTargetVersion) {
			return errors.New("invalid frozen automation dispatch")
		}
		frozen, err := json.Marshal(runner)
		if err != nil {
			return err
		}
		execution.RunnerContext = string(frozen)
		until := now.Add(ttl)
		if runner.Actions[index].FailureReason == "" {
			results[index].Status = "accepted"
		}
		results[index].Reason = ""
		raw, _ := json.Marshal(results)
		update := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ? AND (runner_lease_until IS NULL OR runner_lease_until < ?)", execution.AccountID, execution.ID, now).Updates(map[string]any{"status": "running", "runner_lease": lease, "runner_lease_until": until, "runner_retry_at": nil, "runner_target_version": execution.RunnerTargetVersion, "runner_context": execution.RunnerContext, "action_results": string(raw)})
		if update.Error != nil {
			return update.Error
		}
		claimed = update.RowsAffected == 1
		return nil
	})
	if err != nil || !claimed {
		return claimed || quarantined, err
	}
	if evaluating {
		return true, r.completeAutomationSiblingEvaluation(ctx, execution, evaluation, projected, evaluationVersion, lease)
	}
	index := execution.RunnerNextAction
	action := runner.Actions[index]
	command := runner.Dispatches[index]
	dependency, ok := r.Config.Dependencies[automationActionOwner(action)]
	updatedResult := automationActionResult{LogicalIndex: action.LogicalIndex, Index: index, Capability: action.Capability, Step: automationActionStep(action), Status: "succeeded"}
	var actionErr error
	if action.FailureReason != "" {
		actionErr = &ContractFailure{Type: "validation_failed", Message: "frozen logical action is invalid"}
	} else if !ok {
		actionErr = &ContractFailure{Type: "dependency_unavailable", Message: "action owner is not configured"}
	} else {
		client := newModuleClient("AUT", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		switch automationActionOwner(action) {
		case "CUS":
			var label labelView
			actionErr = client.command(ctx, command, &label)
			if actionErr == nil {
				if label.AccountID != execution.AccountID || (label.ID == 0 && action.Operation != "remove") {
					actionErr = &ContractFailure{Type: "dependency_unavailable", Message: "label owner result is invalid"}
				} else {
					updatedResult.ObjectVersion = int(execution.RunnerTargetVersion)
					updatedResult.ResourceID = label.ID
					updatedResult.Outcome = "resolved"
					if label.ID == 0 {
						updatedResult.Outcome = "missing"
					}
				}
			}
		case "MSG":
			var message messageView
			actionErr = client.command(ctx, command, &message)
			expectedStatus := "queued"
			if action.Private {
				expectedStatus = "saved"
			}
			if actionErr == nil {
				if message.AccountID != execution.AccountID || message.ConversationID != execution.ConversationID || message.ID == 0 || message.Content != action.Value || message.Private != action.Private || message.Status != expectedStatus || message.MessageType != domain.MessageTypeOutgoing || message.SenderID != 0 || message.SenderType != domain.SenderTypeUser || message.Deleted {
					actionErr = &ContractFailure{Type: "dependency_unavailable", Message: "message owner result is invalid"}
				} else {
					updatedResult.ObjectVersion = int(execution.RunnerTargetVersion)
					updatedResult.ResourceID = message.ID
					updatedResult.Outcome = expectedStatus
				}
			}
		default:
			var updated automationConversationActivityView
			actionErr = client.command(ctx, command, &updated)
			if updated.ActivityOnly {
				updatedResult.Outcome = automationActivityTouched
			}
			updatedResult.ObjectVersion = updated.ObjectVersion
			if actionErr == nil && !automationConversationResultMatches(action, command, execution, updated.conversationView) {
				actionErr = &ContractFailure{Type: "dependency_unavailable", Message: "CON result did not confirm owned effect"}
			}
		}
	}
	if actionErr == nil {
		if _, valid := automationResultAdvances(action, updatedResult, execution.RunnerTargetVersion, runner.ActivityPolicy); !valid {
			actionErr = &ContractFailure{Type: "dependency_unavailable", Message: "owner result did not confirm version proof"}
		}
	}
	// A cancelled context or unknown transport result never fabricates failure; retry
	// the same owner idempotency key to reconcile its committed receipt.
	err = r.DB.Transaction(func(tx *gorm.DB) error {
		var current domain.AutomationRuleExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ? AND runner_lease = ?", execution.AccountID, execution.ID, lease).First(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		if json.Unmarshal([]byte(current.ActionResults), &results) != nil {
			return errors.New("automation result checkpoint is invalid")
		}
		permanent := false
		if actionErr != nil {
			var f *ContractFailure
			if errors.As(actionErr, &f) {
				switch f.Type {
				case "validation_failed", "resource_not_found", "state_conflict", "authentication_failed", "permission_denied":
					permanent = true
				}
			}
		}
		// A rejection before receipt lookup (for example rotated authentication)
		// cannot disprove a previous commit whose response was lost. Reconciliation
		// errors retain an unknown result until the original receipt can be read.
		if reconciling {
			permanent = false
		}
		status, reason := "received", ""
		updates := map[string]any{"runner_lease": "", "runner_lease_until": nil, "runner_retry_at": nil}
		if actionErr != nil && !permanent {
			results[index].Status = "result_unknown"
			results[index].Reason = "owner_result_unknown"
			status = "waiting"
			retry := time.Now().UTC().Add(time.Second)
			updates["runner_retry_at"] = retry
		} else if actionErr != nil && runner.FailurePolicy == automationContinueDefinite {
			results[index].Status, results[index].Reason = "failed", "owner_command_rejected"
			if action.FailureReason != "" {
				results[index].Reason = action.FailureReason
			}
			next := index + 1
			for next < len(results) && automationSameLogicalAction(action, runner.Actions[next]) {
				results[next].Status, results[next].Reason = "skipped", "dependency_failed"
				next++
			}
			updates["runner_next_action"] = next
			if current.CancelRequested && next < len(results) {
				status, reason = "cancelled", "cancelled_by_request"
				for i := next; i < len(results); i++ {
					results[i].Status, results[i].Reason = "skipped", reason
				}
			} else if next == len(results) {
				status, reason = automationCompletedStatus(results)
			}
		} else if actionErr != nil {
			results[index].Status = "failed"
			results[index].Reason = "owner_command_rejected"
			status, reason = "failed", "owner_command_rejected"
			if index > 0 {
				status = "partially_succeeded"
			}
			for i := index + 1; i < len(results); i++ {
				results[i].Status = "skipped"
				results[i].Reason = "stopped_after_failure"
			}
		} else {
			results[index] = updatedResult
			updates["runner_next_action"] = index + 1
			updates["runner_target_version"] = updatedResult.ObjectVersion
			if current.CancelRequested && index+1 < len(results) {
				status, reason = "cancelled", "cancelled_by_request"
				for i := index + 1; i < len(results); i++ {
					results[i].Status = "skipped"
					results[i].Reason = reason
				}
			} else if index+1 == len(results) {
				status, reason = automationCompletedStatus(results)
			}
		}
		raw, _ := json.Marshal(results)
		current.ActionResults = string(raw)
		updates["action_results"] = current.ActionResults
		if status == "received" || status == "waiting" {
			updates["status"] = status
			return tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ? AND runner_lease = ?", current.AccountID, current.ID, lease).Updates(updates).Error
		}
		if err := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ? AND runner_lease = ?", current.AccountID, current.ID, lease).Updates(updates).Error; err != nil {
			return err
		}
		return r.finishAutomationExecution(context.WithValue(ctx, transactionKey{}, tx), runner.command(current), &current, status, reason)
	})
	return true, err
}

// Revalidate persisted dispatch data as well as JSON shape. Corruption must never
// expand the static capability allowlist or silently remove the owner CAS guard.
func validAutomationCheckpoint(e domain.AutomationRuleExecution, c automationRunnerContext, results []automationActionResult) bool {
	if e.RunnerEvaluation != "" {
		evaluation, valid := decodeAutomationSiblingEvaluation(e)
		if !valid || evaluation.Decision != "matched" {
			return false
		}
	}
	if !validAutomationRunnerIdentity(e, c) {
		return false
	}
	if !validAutomationRunnerPlan(c) || len(results) != len(c.Actions) || e.RunnerNextAction < 0 || e.RunnerNextAction >= len(results) || (len(c.Dispatches) != 0 && len(c.Dispatches) != len(c.Actions)) {
		return false
	}
	if c.FailurePolicy == automationContinueDefinite {
		return validAutomationContinuingCheckpoint(e, c, results, false, automationCheckpointBase(e, c, results))
	}
	for i, a := range c.Actions {
		result := results[i]
		if !automationResultIdentity(a, result, i) {
			return false
		}
		if i < e.RunnerNextAction {
			if result.Status != "succeeded" || result.ObjectVersion <= 0 {
				return false
			}
			before := uint(result.ObjectVersion) - 1
			if i > 0 {
				before = uint(results[i-1].ObjectVersion)
			} else if len(c.Dispatches) > 0 && c.Dispatches[i].CommandID != "" {
				before = uint(c.Dispatches[i].Target.ExpectedVersion)
				if a.Owner == "CUS" {
					before = uint(result.ObjectVersion)
				}
			}
			if _, valid := automationResultAdvances(a, result, before, c.ActivityPolicy); !valid {
				return false
			}
			if !validAutomationDispatch(c, e, results, i, before) {
				return false
			}
		} else {
			if result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "" {
				return false
			}
			if i > e.RunnerNextAction && result.Status != "pending" {
				return false
			}
			if i == e.RunnerNextAction && result.Status != "pending" && result.Status != "accepted" && result.Status != "result_unknown" {
				return false
			}
			if len(c.Dispatches) > 0 {
				if i > e.RunnerNextAction && !automationEmptyDispatch(c.Dispatches[i]) {
					return false
				}
				if i == e.RunnerNextAction {
					if (result.Status == "accepted" || result.Status == "result_unknown") && automationEmptyDispatch(c.Dispatches[i]) {
						return false
					}
					if !automationEmptyDispatch(c.Dispatches[i]) && !validAutomationDispatch(c, e, results, i, e.RunnerTargetVersion) {
						return false
					}
				}
			} else if a.Command != "con.status.change.v1" && a.Command != "con.priority.change.v1" {
				return false
			}
		}
	}
	if e.RunnerNextAction > 0 && uint(results[e.RunnerNextAction-1].ObjectVersion) != e.RunnerTargetVersion {
		return false
	}
	return true
}

func validAutomationRunnerIdentity(e domain.AutomationRuleExecution, c automationRunnerContext) bool {
	if !validAutomationActivityPolicy(c.ActivityPolicy) || (c.FailurePolicy != "" && c.FailurePolicy != automationContinueDefinite) {
		return false
	}
	if e.ExecutionKey == nil || len(*e.ExecutionKey) != 64 || e.RunnerTargetVersion == 0 || e.AccountID == 0 || e.ConversationID == 0 || e.RuleID == 0 || e.RuleVersion == 0 || e.TargetVersion == 0 || e.AutomationDepth >= automationMaxDepth {
		return false
	}
	if _, err := hex.DecodeString(*e.ExecutionKey); err != nil {
		return false
	}
	if c.Actor.Type != "System" || c.Actor.AuthSource != "module" || c.Actor.ID != "automation:"+strconv.Itoa(int(e.RuleID)) || c.CorrelationID == "" || c.CausationID == "" || c.RequestedAt.IsZero() {
		return false
	}
	return true
}

func (c automationRunnerContext) command(e domain.AutomationRuleExecution) foundation.CommandEnvelope {
	return foundation.CommandEnvelope{CommandID: c.CausationID, CommandType: "aut.execution.start.v1", AccountID: e.AccountID, Actor: c.Actor, CorrelationID: c.CorrelationID, CausationID: c.CausationID, RequestedAt: c.RequestedAt}
}
