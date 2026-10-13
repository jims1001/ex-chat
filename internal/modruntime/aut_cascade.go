package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errAutomationCascadePending = errors.New("automation causal family is not terminal")
var errAutomationCascadeUnverified = errors.New("automation causal family cannot prove the event")

type automationCascadeFact struct {
	execution domain.AutomationRuleExecution
	action    automationLocalAction
	result    automationActionResult
	command   foundation.CommandEnvelope
}

func automationFactEventNames(event foundation.EventEnvelope) (map[string]bool, error) {
	names := map[string]bool{}
	switch event.SourceModule + ":" + event.EventType {
	case "CON:con.conversation.created.v1":
		names["conversation_created"] = true
	case "CON:con.conversation.changed.v1", "CON:con.conversation_status.changed.v1":
		names["conversation_updated"] = true
	case "MSG:msg.message.created.v1":
		names["message_created"] = true
	case "MSG:msg.message_status.changed.v1":
		if err := validateAutomationRetryFact(event); err != nil {
			return nil, err
		}
		names["message_created"] = true
	default:
		return nil, errors.New("unsupported automation fact")
	}
	before, hasBefore := event.Payload["status_before"]
	after, hasAfter := event.Payload["status_after"]
	if !hasBefore && !hasAfter {
		return names, nil
	}
	validStatus := func(v any) bool {
		s, ok := v.(string)
		return ok && (s == "open" || s == "pending" || s == "resolved" || s == "snoozed")
	}
	changed := false
	for _, field := range event.ChangedFields {
		changed = changed || field == "status"
	}
	if !hasBefore || !hasAfter || !validStatus(before) || !validStatus(after) || before == after || !changed || names["conversation_created"] {
		return nil, errors.New("invalid automation status transition")
	}
	names["conversation_updated"] = true
	if after == "open" {
		names["conversation_opened"] = true
	}
	if after == "resolved" {
		names["conversation_resolved"] = true
	}
	return names, nil
}

func automationCascadeClaim(event foundation.EventEnvelope) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", event.AccountID, event.SourceModule, event.CausationID)))
	return "aut-cascade-" + hex.EncodeToString(sum[:])[:48]
}

// Only AUT-owned immutable execution checkpoints and intake receipts establish
// this order. No CON/MSG table, live version, or journal sequence is consulted.
// Children are visited breadth-first in parent rule/action order. A later sibling
// waits for preceding batches, including facts with no matching rule.
func automationCascadeTarget(tx *gorm.DB, event foundation.EventEnvelope) (uint, error) {
	origin, originErr := uintFilter(event.Payload, "origin_execution_id")
	_, hasOrigin := event.Payload["origin_execution_id"]
	_, hasDepth := event.Payload["automation_depth"]
	if !hasOrigin && !hasDepth {
		return uintFilter(event.Payload, "object_version")
	}
	depth, depthErr := uintFilter(event.Payload, "automation_depth")
	conv, convErr := uintFilter(event.Payload, "conversation_id")
	if originErr != nil || depthErr != nil || convErr != nil || depth > automationMaxDepth {
		return 0, errAutomationCascadeUnverified
	}
	var root domain.AutomationRuleExecution
	if err := tx.Where("account_id = ? AND id = ?", event.AccountID, origin).First(&root).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errAutomationCascadeUnverified
		}
		return 0, err
	}
	if root.ConversationID != conv || depth != root.AutomationDepth+1 {
		return 0, errAutomationCascadeUnverified
	}
	seen := map[uint]bool{}
	for root.OriginExecutionID != nil {
		if seen[root.ID] || len(seen) >= automationMaxDepth {
			return 0, errAutomationCascadeUnverified
		}
		seen[root.ID] = true
		var parent domain.AutomationRuleExecution
		if err := tx.Where("account_id = ? AND id = ?", event.AccountID, *root.OriginExecutionID).First(&parent).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return 0, errAutomationCascadeUnverified
			}
			return 0, err
		}
		if parent.ID >= root.ID || parent.ConversationID != conv || parent.AutomationDepth+1 != root.AutomationDepth {
			return 0, errAutomationCascadeUnverified
		}
		root = parent
	}
	if root.AutomationDepth != 0 {
		return 0, errAutomationCascadeUnverified
	}
	version, children, err := automationCascadeBatch(tx, root)
	if err != nil {
		return 0, err
	}
	// The complete sibling set comes from the frozen successful dispatches, not
	// from the current owner source page. Missing delivery is a wait, never a skip.
	for level := uint(1); len(children) > 0 && level <= automationMaxDepth; level++ {
		nextGeneration := []automationCascadeFact{}
		for _, fact := range children {
			if fact.command.CommandID == event.CausationID && automationActionOwner(fact.action) == event.SourceModule {
				if !automationCascadeEventMatches(fact, event) {
					return 0, errAutomationCascadeUnverified
				}
				return version, nil
			}
			expectedKey := automationCascadeClaim(foundation.EventEnvelope{AccountID: event.AccountID, SourceModule: automationActionOwner(fact.action), CausationID: fact.command.CommandID})
			var claim domain.LocalChangeJournal
			if err := tx.Where("source_module = ? AND account_id = ? AND action = ? AND change_id = ?", "AUT", event.AccountID, "automation_cascade_received", expectedKey).First(&claim).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return 0, errAutomationCascadePending
				}
				return 0, err
			}
			var prior foundation.EventEnvelope
			if json.Unmarshal([]byte(claim.AfterState), &prior) != nil || prior.EventID != claim.EventID || !automationCascadeEventMatches(fact, prior) {
				return 0, errAutomationCascadeUnverified
			}
			var receipt domain.LocalChangeJournal
			if err := tx.Where("source_module = ? AND account_id = ? AND action = ? AND change_id = ?", "AUT", event.AccountID, "automation_fact_received", automationFactReceipt(prior)).First(&receipt).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return 0, errAutomationCascadeUnverified
				}
				return 0, err
			}
			var admitted foundation.EventEnvelope
			if json.Unmarshal([]byte(receipt.AfterState), &admitted) != nil {
				return 0, errAutomationCascadeUnverified
			}
			target, err := uintFilter(admitted.Payload, "object_version")
			if err != nil || target != version || receipt.BeforeState != claim.AfterState || !automationCascadeAdmissionMatches(prior, admitted, version) {
				return 0, errAutomationCascadeUnverified
			}
			var rows []domain.AutomationRuleExecution
			if err := tx.Where("account_id = ? AND fact_batch_key = ?", event.AccountID, receipt.ChangeID).Order("id ASC").Find(&rows).Error; err != nil {
				return 0, err
			}
			if len(rows) > 0 {
				var descendants []automationCascadeFact
				version, descendants, err = automationCascadeBatch(tx, rows[0])
				if err != nil {
					return 0, err
				}
				nextGeneration = append(nextGeneration, descendants...)
			}
		}
		children = nextGeneration
	}
	return 0, errAutomationCascadeUnverified
}

func automationCascadeBatch(tx *gorm.DB, anchor domain.AutomationRuleExecution) (uint, []automationCascadeFact, error) {
	rows := []domain.AutomationRuleExecution{anchor}
	if anchor.FactBatchKey != "" {
		if err := tx.Where("account_id = ? AND fact_batch_key = ?", anchor.AccountID, anchor.FactBatchKey).Order("id ASC").Find(&rows).Error; err != nil {
			return 0, nil, err
		}
	}
	if len(rows) == 0 {
		return 0, nil, errAutomationCascadeUnverified
	}
	if anchor.FactBatchKey != "" {
		var receipt domain.LocalChangeJournal
		if err := tx.Where("source_module = ? AND account_id = ? AND change_id = ?", "AUT", anchor.AccountID, anchor.FactBatchKey).First(&receipt).Error; err != nil {
			return 0, nil, err
		}
		if err := validateAutomationFactBatch(tx, receipt); err != nil {
			return 0, nil, err
		}
	}
	version := rows[0].TargetVersion
	children := []automationCascadeFact{}
	for _, e := range rows {
		if e.AccountID != anchor.AccountID || e.ConversationID != anchor.ConversationID || e.TargetVersion != anchor.TargetVersion || e.FactBatchKey != anchor.FactBatchKey {
			return 0, nil, errAutomationCascadeUnverified
		}
		base, ready, err := automationPredecessorVersion(tx, e)
		if err != nil {
			if errors.Is(err, errAutomationCausalCheckpoint) {
				err = errAutomationCascadeUnverified
			}
			return 0, nil, err
		}
		if !ready {
			return 0, nil, errAutomationCascadePending
		}
		if base != version {
			return 0, nil, errAutomationCascadeUnverified
		}
		after, ready, err := automationTerminalExecutionVersion(e, base)
		if err != nil {
			return 0, nil, errAutomationCascadeUnverified
		}
		if !ready {
			return 0, nil, errAutomationCascadePending
		}
		if e.RunnerContext != "" {
			var runner automationRunnerContext
			var results []automationActionResult
			if json.Unmarshal([]byte(e.RunnerContext), &runner) != nil || json.Unmarshal([]byte(e.ActionResults), &results) != nil {
				return 0, nil, errAutomationCascadeUnverified
			}
			for i := 0; i < e.RunnerNextAction; i++ {
				a, result := runner.Actions[i], results[i]
				emits := result.Status == "succeeded" && (a.Command == "msg.automation.message.create.v1" || a.Command == "con.status.change.v1" || a.Command == "con.priority.change.v1" || (a.Command == "con.automation.apply.v1" && result.Outcome != automationActivityTouched && uint(result.ObjectVersion) > base))
				if emits {
					var command foundation.CommandEnvelope
					if len(runner.Dispatches) > 0 {
						command = runner.Dispatches[i]
					} else {
						copy := e
						copy.RunnerTargetVersion = base
						command = automationDispatch(runner, copy, results, i, 1000, runner.RequestedAt)
					}
					children = append(children, automationCascadeFact{e, a, result, command})
				}
				if result.Status == "succeeded" {
					base = uint(result.ObjectVersion)
				}
			}
		}
		version = after
	}
	return version, children, nil
}

func automationCascadeEventMatches(f automationCascadeFact, event foundation.EventEnvelope) bool {
	conv, e1 := uintFilter(event.Payload, "conversation_id")
	origin, e2 := uintFilter(event.Payload, "origin_execution_id")
	depth, e3 := uintFilter(event.Payload, "automation_depth")
	version, e4 := uintFilter(event.Payload, "object_version")
	if event.Validate() != nil || e1 != nil || e2 != nil || e3 != nil || e4 != nil || event.AccountID != f.execution.AccountID || conv != f.execution.ConversationID || origin != f.execution.ID || depth != f.execution.AutomationDepth+1 || version != uint(f.result.ObjectVersion) || event.CausationID != f.command.CommandID || event.SourceModule != automationActionOwner(f.action) || !automationJSONEqual(event.Actor, f.command.Actor) || event.CorrelationID != f.command.CorrelationID {
		return false
	}
	if event.SourceModule == "CON" {
		expected := "con.conversation.changed.v1"
		if f.action.Command == "con.status.change.v1" {
			expected = "con.conversation_status.changed.v1"
		}
		return event.EventType == expected && event.AggregateType == "Conversation" && event.AggregateID == strconv.FormatUint(uint64(conv), 10)
	}
	id, err := uintFilter(event.Payload, "message_id")
	private, ok := event.Payload["private"].(bool)
	hash := sha256.Sum256([]byte(f.action.Value))
	return err == nil && id == f.result.ResourceID && event.EventType == "msg.message.created.v1" && event.AggregateType == "Message" && event.AggregateID == strconv.FormatUint(uint64(id), 10) && ok && private == f.action.Private && event.Payload["message_type"] == domain.MessageTypeOutgoing && event.Payload["local_outcome"] == f.result.Outcome && event.Payload["content_hash"] == hex.EncodeToString(hash[:]) && event.Payload["conversation_activity_prepared"] == f.command.Input["prepare_command_id"]
}

func (r *Runtime) recordAutomationCascadeQuarantine(ctx context.Context, event foundation.EventEnvelope) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	row := domain.LocalChangeJournal{ChangeID: "bad-" + automationFactReceipt(event), SourceModule: "AUT", AccountID: event.AccountID, Action: "automation_fact_quarantine", EventID: event.EventID, SourceType: event.SourceModule, AfterState: string(raw), Reason: "causal_origin_unverified", Status: "manual_action_required", Result: "rejected", OccurredAt: event.OccurredAt}
	// The caller returns the proof error. Keep a durable diagnostic without claiming
	// delivery or suppressing a future operator repair of the original checkpoint.
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func automationCascadeAdmissionMatches(owner, admitted foundation.EventEnvelope, version uint) bool {
	copyPayload := make(map[string]any, len(owner.Payload)+1)
	for key, value := range owner.Payload {
		copyPayload[key] = value
	}
	copyPayload["origin_object_version"] = copyPayload["object_version"]
	copyPayload["object_version"] = version
	owner.Payload = copyPayload
	return automationJSONEqual(owner, admitted)
}

func validateAutomationCascadeReceipts(tx *gorm.DB) error {
	var batches []domain.LocalChangeJournal
	if err := tx.Where("source_module = ? AND action = ?", "AUT", "automation_fact_received").Find(&batches).Error; err != nil {
		return err
	}
	for _, receipt := range batches {
		if receipt.SchemaVersion == "aut.fact.v2" || receipt.SchemaVersion == "aut.fact.v3" {
			var admitted foundation.EventEnvelope
			if json.Unmarshal([]byte(receipt.AfterState), &admitted) != nil || admitted.Validate() != nil || admitted.AccountID != receipt.AccountID || admitted.EventID != receipt.EventID || automationFactReceipt(admitted) != receipt.ChangeID {
				return errAutomationCascadeUnverified
			}
			if automationRetryFact(admitted) {
				if _, err := automationFactEventNames(admitted); err != nil {
					return errAutomationCascadeUnverified
				}
			}
			_, cascade := admitted.Payload["origin_execution_id"]
			if cascade && (receipt.BeforeState == "" || receipt.CommandID != admitted.CausationID) {
				return errAutomationCascadeUnverified
			}
			if !cascade && receipt.BeforeState != "" {
				return errAutomationCascadeUnverified
			}
		}
		if err := validateAutomationFactBatch(tx, receipt); err != nil {
			return err
		}
	}
	var receipts []domain.LocalChangeJournal
	if err := tx.Where("source_module = ? AND action = ? AND before_state <> ?", "AUT", "automation_fact_received", "").Find(&receipts).Error; err != nil {
		return err
	}
	for _, receipt := range receipts {
		var owner, admitted foundation.EventEnvelope
		if json.Unmarshal([]byte(receipt.BeforeState), &owner) != nil || json.Unmarshal([]byte(receipt.AfterState), &admitted) != nil || owner.AccountID != receipt.AccountID || owner.EventID != receipt.EventID || automationFactReceipt(owner) != receipt.ChangeID {
			return errAutomationCascadeUnverified
		}
		version, err := automationCascadeTarget(tx, owner)
		if err != nil {
			return err
		}
		if !automationCascadeAdmissionMatches(owner, admitted, version) {
			return errAutomationCascadeUnverified
		}
		var claim domain.LocalChangeJournal
		if err := tx.Where("source_module = ? AND account_id = ? AND action = ? AND change_id = ?", "AUT", owner.AccountID, "automation_cascade_received", automationCascadeClaim(owner)).First(&claim).Error; err != nil {
			return err
		}
		if claim.EventID != owner.EventID || claim.AfterState != receipt.BeforeState {
			return errAutomationCascadeUnverified
		}
	}
	var claims []domain.LocalChangeJournal
	if err := tx.Where("source_module = ? AND action = ?", "AUT", "automation_cascade_received").Find(&claims).Error; err != nil {
		return err
	}
	for _, claim := range claims {
		var owner foundation.EventEnvelope
		if json.Unmarshal([]byte(claim.AfterState), &owner) != nil || owner.AccountID != claim.AccountID || owner.EventID != claim.EventID || claim.ChangeID != automationCascadeClaim(owner) || claim.CommandID != owner.CausationID || claim.SourceType != owner.SourceModule {
			return errAutomationCascadeUnverified
		}
		var receipt domain.LocalChangeJournal
		if err := tx.Where("source_module = ? AND account_id = ? AND action = ? AND change_id = ?", "AUT", owner.AccountID, "automation_fact_received", automationFactReceipt(owner)).First(&receipt).Error; err != nil {
			return err
		}
		if receipt.BeforeState != claim.AfterState {
			return errAutomationCascadeUnverified
		}
	}
	return nil
}

type automationFactBatchManifest struct {
	Codec         string `json:"codec"`
	ExecutionIDs  []uint `json:"execution_ids"`
	EvaluationIDs []uint `json:"evaluation_ids,omitempty"`
}

func freezeAutomationFactBatch(tx *gorm.DB, receipt domain.LocalChangeJournal) error {
	ids := []uint{}
	if err := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND fact_batch_key = ?", receipt.AccountID, receipt.ChangeID).Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return err
	}
	evaluationIDs := []uint{}
	if err := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND fact_batch_key = ? AND COALESCE(runner_evaluation, '') <> ''", receipt.AccountID, receipt.ChangeID).Order("id ASC").Pluck("id", &evaluationIDs).Error; err != nil {
		return err
	}
	raw, err := json.Marshal(automationFactBatchManifest{Codec: "aut.fact_batch.v2", ExecutionIDs: ids, EvaluationIDs: evaluationIDs})
	if err != nil {
		return err
	}
	return tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND change_id = ?", "AUT", receipt.AccountID, receipt.ChangeID).Update("diff", string(raw)).Error
}
func validateAutomationFactBatch(tx *gorm.DB, receipt domain.LocalChangeJournal) error {
	if receipt.SchemaVersion != "aut.fact.v2" && receipt.SchemaVersion != "aut.fact.v3" {
		return nil
	} // Phase9/10 intake compatibility.
	var manifest automationFactBatchManifest
	if decodeStrictSnapshotJSON([]byte(receipt.Diff), &manifest) != nil || (manifest.Codec != "aut.fact_batch.v1" && manifest.Codec != "aut.fact_batch.v2") || manifest.ExecutionIDs == nil {
		return errAutomationCascadeUnverified
	}
	if (receipt.SchemaVersion == "aut.fact.v3") != (manifest.Codec == "aut.fact_batch.v2") {
		return errAutomationCascadeUnverified
	}
	evaluationIDs := []uint{}
	if err := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND fact_batch_key = ? AND COALESCE(runner_evaluation, '') <> ''", receipt.AccountID, receipt.ChangeID).Order("id ASC").Pluck("id", &evaluationIDs).Error; err != nil {
		return err
	}
	if len(evaluationIDs) != len(manifest.EvaluationIDs) || (len(evaluationIDs) > 0 && !automationJSONEqual(evaluationIDs, manifest.EvaluationIDs)) {
		return errAutomationCascadeUnverified
	}
	ids := []uint{}
	if err := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND fact_batch_key = ?", receipt.AccountID, receipt.ChangeID).Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return err
	}
	if !automationJSONEqual(ids, manifest.ExecutionIDs) {
		return errAutomationCascadeUnverified
	}
	return nil
}

func automationCascadeIdentityConflict(tx *gorm.DB, event foundation.EventEnvelope, original string) error {
	var claim domain.LocalChangeJournal
	err := tx.Where("source_module = ? AND account_id = ? AND action = ? AND change_id = ?", "AUT", event.AccountID, "automation_cascade_received", automationCascadeClaim(event)).First(&claim).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if claim.EventID != event.EventID || claim.AfterState != original || claim.CommandID != event.CausationID || claim.SourceType != event.SourceModule {
		return errAutomationCascadeUnverified
	}
	return nil
}
