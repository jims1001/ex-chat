package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Keep the established monolith recursion threshold without importing its service.
const automationMaxDepth = 3

func automationExecutionView(e *domain.AutomationRuleExecution) map[string]any {
	view := map[string]any{"id": e.ID, "account_id": e.AccountID, "rule_id": e.RuleID, "rule_version": e.RuleVersion, "conversation_id": e.ConversationID, "message_id": e.MessageID, "event_name": e.EventName, "event_id": e.EventID, "target_version": e.TargetVersion, "automation_depth": e.AutomationDepth, "origin_execution_id": e.OriginExecutionID, "status": e.Status, "duration_ms": e.DurationMs, "triggered_at": e.TriggeredAt, "created_at": e.CreatedAt, "completed_at": e.CompletedAt, "cancel_requested": e.CancelRequested}
	switch e.Error {
	case "causal_predecessor_unverified", "target_version_stale", "rule_changed_before_dispatch", "rule_disabled_before_dispatch", "rule_deleted_before_dispatch", "target_unavailable", "fact_admission_rejected", "invalid_runner_checkpoint", "invalid_action_parameters", "owner_command_rejected", "automation_loop_guard", "invalid_frozen_definition", "condition_evaluator_unavailable", "conditions_not_matched", "condition_context_unavailable", "action_capability_executor_unavailable", "cancelled_by_request":
		view["reason_code"] = e.Error
	}
	if e.ActionResults != "" {
		var results []automationActionResult
		if json.Unmarshal([]byte(e.ActionResults), &results) == nil {
			view["action_results"] = results
		}
	}
	return view
}

func (r *Runtime) registerAutomationLifecycle() error {
	if err := r.Contracts.RegisterCommandPreflight("aut.execution.start.v1", r.prepareAutomationExecution); err != nil {
		return err
	}
	if err := r.Contracts.RegisterCommand("aut.execution.start.v1", r.startAutomationExecution); err != nil {
		return err
	}
	return r.Contracts.RegisterCommand("aut.execution.cancel.v1", r.cancelAutomationExecution)
}

func (r *Runtime) startAutomationExecution(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
	fail := func(kind, message string) (any, error) { return nil, &ContractFailure{Type: kind, Message: message} }
	ruleID, err := strconv.ParseUint(command.Target.ID, 10, 64)
	if command.Target.Type != "rule" || err != nil || ruleID == 0 {
		return fail("validation_failed", "rule target is required")
	}
	conversationID, err := uintFilter(command.Input, "conversation_id")
	if err != nil {
		return fail("validation_failed", "conversation_id is required")
	}
	targetVersion, err := uintFilter(command.Input, "target_version")
	if err != nil {
		return fail("validation_failed", "target_version is required")
	}
	eventID, ok := command.Input["event_id"].(string)
	if !ok || strings.TrimSpace(eventID) == "" || len(eventID) > 255 {
		return fail("validation_failed", "event_id is required")
	}
	eventName, ok := command.Input["event_name"].(string)
	if !ok {
		return fail("validation_failed", "event_name is required")
	}
	depth := uint(0)
	if raw, exists := command.Input["automation_depth"]; exists {
		// uintFilter intentionally rejects zero; allow only an actual numeric zero here.
		encoded, _ := json.Marshal(raw)
		if string(encoded) != "0" {
			depth, err = uintFilter(command.Input, "automation_depth")
			if err != nil {
				return fail("validation_failed", "automation_depth is invalid")
			}
		}
	}
	var origin *uint
	if _, exists := command.Input["origin_execution_id"]; exists {
		id, e := uintFilter(command.Input, "origin_execution_id")
		if e != nil {
			return fail("validation_failed", "origin_execution_id is invalid")
		}
		origin = &id
	}
	tx := Transaction(ctx, r.DB)
	// Serialize starts and rule changes through the owned rule, not target-module tables.
	var rule domain.AutomationRule
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", command.AccountID, ruleID).First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fail("resource_not_found", "rule was not found")
		}
		return fail("dependency_unavailable", "rule store is unavailable")
	}
	var version domain.AutomationRuleVersion
	q := tx.Where("account_id = ? AND rule_id = ?", command.AccountID, rule.ID)
	if _, exists := command.Input["rule_version"]; exists {
		v, e := uintFilter(command.Input, "rule_version")
		if e != nil {
			return fail("validation_failed", "rule_version is invalid")
		}
		q = q.Where("version = ?", v)
	}
	if err := q.Order("version DESC").First(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fail("resource_not_found", "rule version was not found")
		}
		return fail("dependency_unavailable", "rule version store is unavailable")
	}
	if prepared, ok := ctx.Value(automationPreparedRuleKey{}).(automationPreparedRule); ok && (prepared.RuleID != rule.ID || prepared.Version != version.Version) {
		return fail("state_conflict", "rule version changed during preparation")
	}
	if eventName != version.EventName {
		return fail("validation_failed", "event_name does not match frozen rule")
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s:%d:%d", command.AccountID, rule.ID, version.Version, eventID, conversationID, targetVersion)))
	key := hex.EncodeToString(hash[:])
	var existing domain.AutomationRuleExecution
	if err := tx.Where("account_id = ? AND execution_key = ?", command.AccountID, key).First(&existing).Error; err == nil {
		return automationExecutionView(&existing), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fail("dependency_unavailable", "execution store is unavailable")
	}
	if !rule.Active {
		return fail("state_conflict", "rule is disabled")
	}
	// Old frozen definitions may be queried/replayed, but cannot start a new run after an update.
	var latest domain.AutomationRuleVersion
	if err := tx.Where("account_id = ? AND rule_id = ?", command.AccountID, rule.ID).Order("version DESC").First(&latest).Error; err != nil {
		return fail("dependency_unavailable", "rule version store is unavailable")
	}
	if latest.Version != version.Version {
		return fail("state_conflict", "rule version is no longer active")
	}
	var messageID *uint
	verified, bridgeVerified := ctx.Value(automationVerifiedTargetKey{}).(automationVerifiedTarget)
	if bridgeVerified {
		if verified.AccountID != command.AccountID || verified.ConversationID != conversationID || verified.Version != targetVersion {
			return fail("validation_failed", "verified target does not match command")
		}
		messageID = verified.MessageID
		if eventName == "message_created" && messageID == nil {
			return fail("validation_failed", "message_id is required for message_created")
		}
	} else {
		return fail("dependency_unavailable", "automation target was not prepared before admission")
	}
	observation, _ := ctx.Value(automationConditionContextKey{}).(*automationConditionContext)
	if observation != nil {
		if observation.Conversation.ID != conversationID || observation.Conversation.AccountID != command.AccountID || observation.Conversation.ObjectVersion <= 0 || uint(observation.Conversation.ObjectVersion) != targetVersion {
			return fail("dependency_unavailable", "condition context does not match admission target")
		}
		if (eventName != "message_created" && observation.Message != nil) || (eventName == "message_created" && (observation.Message == nil || messageID == nil || observation.Message.ID != *messageID || observation.Message.AccountID != command.AccountID || observation.Message.ConversationID != conversationID)) {
			return fail("dependency_unavailable", "condition message context does not match admission event")
		}
	}

	blocked := depth >= automationMaxDepth
	if origin != nil {
		var parent domain.AutomationRuleExecution
		if err := tx.Where("account_id = ? AND id = ?", command.AccountID, *origin).First(&parent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fail("resource_not_found", "origin execution was not found")
			}
			return fail("dependency_unavailable", "origin execution store is unavailable")
		}
		if depth != parent.AutomationDepth+1 {
			return fail("validation_failed", "automation depth does not follow origin")
		}
		// Walk persisted ancestry; client-provided depth cannot erase a repeated causal path.
		seen := map[uint]bool{}
		for {
			if seen[parent.ID] || (parent.RuleID == rule.ID && parent.ConversationID == conversationID) {
				blocked = true
				break
			}
			seen[parent.ID] = true
			if parent.OriginExecutionID == nil {
				break
			}
			if len(seen) >= automationMaxDepth {
				blocked = true
				break
			}
			parentID := *parent.OriginExecutionID
			parent = domain.AutomationRuleExecution{}
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, parentID).First(&parent).Error; err != nil {
				return fail("state_conflict", "origin chain is incomplete")
			}
		}
	} else if depth > 0 {
		return fail("validation_failed", "origin execution is required for recursive triggers")
	}
	var previous int64
	if err := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND rule_id = ? AND conversation_id = ? AND target_version = ? AND execution_key IS NOT NULL AND status <> ?", command.AccountID, rule.ID, conversationID, targetVersion, "loop_blocked").Count(&previous).Error; err != nil {
		return fail("dependency_unavailable", "execution store is unavailable")
	}
	// Owner facts already have a stable receipt and immutable ancestry. Distinct
	// sibling message facts may legitimately share a version after a no-match;
	// their identity must not be collapsed by a target-version heuristic.
	blocked = blocked || (previous > 0 && verified.FactBatchKey == "")
	now := time.Now().UTC()
	execution := domain.AutomationRuleExecution{AccountID: command.AccountID, RuleID: rule.ID, ExecutionKey: &key, RuleVersion: version.Version, RunnerConditionSemantics: version.ConditionSemantics, EventID: eventID, TargetVersion: targetVersion, RunnerTargetVersion: targetVersion, FactBatchKey: verified.FactBatchKey, RunnerPredecessorID: verified.PredecessorID, OriginExecutionID: origin, AutomationDepth: depth, ConversationID: conversationID, MessageID: messageID, EventName: eventName, Status: "received", TriggeredAt: now, CreatedAt: now, ActionResults: "[]"}
	if err := tx.Create(&execution).Error; err != nil {
		return fail("state_conflict", "execution could not be created")
	}
	// Later conditional siblings must observe confirmed earlier effects. Their
	// definition and shared observation remain immutable while the decision waits.
	conditions, _, validConditions := parseAutomationProfileConditions(version.Conditions, version.ConditionSemantics)
	if !blocked && verified.FactBatchKey != "" && verified.PredecessorID != nil && (!validConditions || len(conditions) > 0) {
		if err := r.queueAutomationSiblingEvaluation(ctx, command, &execution, version, observation); err != nil {
			return nil, err
		}
		return automationExecutionView(&execution), nil
	}
	status, reason := "succeeded", ""
	if blocked {
		status, reason = "loop_blocked", "automation_loop_guard"
	} else {
		matched, conditionFailure := matchAutomationConditionsProfile(version.Conditions, version.ConditionSemantics, observation)
		if conditionFailure == "condition_context_unavailable" {
			return fail("dependency_unavailable", "condition context was not prepared before admission")
		}
		actions, validActions := parseAutomationActionDefinitions(version.Actions)
		if conditionFailure != "" {
			status, reason = "failed", "invalid_frozen_definition"
		} else if !matched {
			status, reason = "no_match", "conditions_not_matched"
		} else if !validActions {
			status, reason = "failed", "invalid_frozen_definition"
		} else if len(actions) > 0 {
			compiled, failure := compileAutomationContinuingActions(version.Actions)
			if failure != "" {
				status, reason = "failed", failure
			} else {
				if err := r.queueAutomationExecution(ctx, command, &execution, compiled, automationTouchValidActivity); err != nil {
					return nil, err
				}
				return automationExecutionView(&execution), nil
			}
		}
	}
	frozenActions, validFrozenActions := parseAutomationActionDefinitions(version.Actions)
	if validFrozenActions && len(frozenActions) > 0 {
		results := make([]map[string]any, len(frozenActions))
		for index := range frozenActions {
			actionStatus := "skipped"
			if reason == "action_capability_executor_unavailable" && index == 0 {
				actionStatus = "failed"
			}
			results[index] = map[string]any{"action_index": index, "status": actionStatus, "reason_code": reason}
		}
		encoded, _ := json.Marshal(results)
		execution.ActionResults = string(encoded)
	}
	// Unsupported definitions fail closed. Supported local actions are durably queued
	// above and dispatched outside the command transaction by the bounded runner.
	if err := r.finishAutomationExecution(ctx, command, &execution, status, reason); err != nil {
		return nil, err
	}
	return automationExecutionView(&execution), nil
}

func (r *Runtime) finishAutomationExecution(ctx context.Context, command foundation.CommandEnvelope, execution *domain.AutomationRuleExecution, status, reason string) error {
	now := time.Now().UTC()
	result := Transaction(ctx, r.DB).Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ? AND status IN ?", command.AccountID, execution.ID, []string{"received", "evaluating", "running", "waiting"}).Updates(map[string]any{"status": status, "error": reason, "action_results": execution.ActionResults, "completed_at": now, "duration_ms": now.Sub(execution.TriggeredAt).Milliseconds()})
	if result.Error != nil || result.RowsAffected != 1 {
		return &ContractFailure{Type: "state_conflict", Message: "execution is no longer cancellable or completable"}
	}
	execution.Status = status
	execution.Error = reason
	execution.CompletedAt = &now
	execution.DurationMs = now.Sub(execution.TriggeredAt).Milliseconds()
	eventType := "aut.automation.executed.v1"
	if status == "failed" || status == "partially_succeeded" || status == "manual_action_required" {
		eventType = "aut.action.failed.v1"
	}
	if status == "loop_blocked" {
		eventType = "aut.loop.blocked.v1"
	}
	return r.appendBusinessEvent(ctx, command, eventType, "AutomationExecution", strconv.FormatUint(uint64(execution.ID), 10), []string{"status"}, map[string]any{"execution_id": execution.ID, "rule_id": execution.RuleID, "rule_version": execution.RuleVersion, "conversation_id": execution.ConversationID, "status": status, "reason_code": reason})
}

func (r *Runtime) cancelAutomationExecution(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
	id, err := strconv.ParseUint(command.Target.ID, 10, 64)
	if command.Target.Type != "execution" || err != nil || id == 0 {
		return nil, &ContractFailure{Type: "validation_failed", Message: "execution target is required"}
	}
	var execution domain.AutomationRuleExecution
	err = Transaction(ctx, r.DB).Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", command.AccountID, id).First(&execution).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &ContractFailure{Type: "resource_not_found", Message: "execution was not found"}
	}
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "execution store is unavailable"}
	}
	if execution.Status == "cancelled" {
		return automationExecutionView(&execution), nil
	}
	if execution.RunnerContext != "" && (execution.Status == "running" || execution.Status == "waiting") {
		if err := Transaction(ctx, r.DB).Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ?", command.AccountID, execution.ID).Update("cancel_requested", true).Error; err != nil {
			return nil, err
		}
		execution.CancelRequested = true
		return automationExecutionView(&execution), nil
	}
	// Legacy or unsupported running/waiting rows have no recoverable runner context.
	if execution.Status != "received" && execution.Status != "evaluating" {
		return nil, &ContractFailure{Type: "state_conflict", Message: "execution cannot be safely cancelled in its current state"}
	}
	if execution.RunnerContext != "" {
		var results []automationActionResult
		if err := json.Unmarshal([]byte(execution.ActionResults), &results); err != nil {
			return nil, err
		}
		for i := range results {
			if results[i].Status == "pending" {
				results[i].Status = "skipped"
				results[i].Reason = "cancelled_by_request"
			}
		}
		raw, _ := json.Marshal(results)
		execution.ActionResults = string(raw)
	}
	if execution.RunnerEvaluation != "" && execution.RunnerContext == "" {
		// Evaluation has no external effects; revoke its read lease immediately.
		if err := Transaction(ctx, r.DB).Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ?", command.AccountID, execution.ID).Updates(map[string]any{"runner_lease": "", "runner_lease_until": nil, "runner_retry_at": nil}).Error; err != nil {
			return nil, err
		}
	}
	// Cancellation never claims to undo an action already committed in another module.
	if err := r.finishAutomationExecution(ctx, command, &execution, "cancelled", "cancelled_by_request"); err != nil {
		return nil, err
	}
	return automationExecutionView(&execution), nil
}
