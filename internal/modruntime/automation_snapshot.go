package modruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// Public execution JSON deliberately excludes the durable dispatch state. Keep
// it in an explicit, versioned owner-only codec instead of changing API tags.
// Unsupported/legacy execution-table codecs fail closed: an old public-JSON
// snapshot cannot prove whether an omitted checkpoint already caused an effect.
const automationSnapshotCodec = "aut.execution.v5"

type automationExecutionSnapshot struct {
	domain.AutomationRuleExecution
	Private *automationExecutionPrivateSnapshot `json:"private"`
}

type automationExecutionPrivateSnapshot struct {
	RunnerConditionSemantics string     `json:"runner_condition_semantics"`
	CancelRequested          bool       `json:"cancel_requested"`
	ExecutionKey             *string    `json:"execution_key"`
	RunnerContext            string     `json:"runner_context"`
	RunnerEvaluation         string     `json:"runner_evaluation"`
	RunnerNextAction         int        `json:"runner_next_action"`
	RunnerTargetVersion      uint       `json:"runner_target_version"`
	RunnerLease              string     `json:"runner_lease"`
	RunnerLeaseUntil         *time.Time `json:"runner_lease_until"`
	RunnerRetryAt            *time.Time `json:"runner_retry_at"`
	FactBatchKey             string     `json:"fact_batch_key"`
	RunnerPredecessorID      *uint      `json:"runner_predecessor_id"`
}

type automationExecutionSnapshotTable struct {
	Codec string                        `json:"codec"`
	Rows  []automationExecutionSnapshot `json:"rows"`
}

func marshalAutomationSnapshot(executions []domain.AutomationRuleExecution) ([]byte, error) {
	table := automationExecutionSnapshotTable{Codec: automationSnapshotCodec, Rows: make([]automationExecutionSnapshot, len(executions))}
	for i, e := range executions {
		table.Rows[i] = automationExecutionSnapshot{AutomationRuleExecution: e, Private: &automationExecutionPrivateSnapshot{
			RunnerConditionSemantics: e.RunnerConditionSemantics, CancelRequested: e.CancelRequested, ExecutionKey: e.ExecutionKey, RunnerContext: e.RunnerContext, RunnerEvaluation: e.RunnerEvaluation, RunnerNextAction: e.RunnerNextAction, RunnerTargetVersion: e.RunnerTargetVersion,
			RunnerLease: e.RunnerLease, RunnerLeaseUntil: e.RunnerLeaseUntil, RunnerRetryAt: e.RunnerRetryAt,
			FactBatchKey: e.FactBatchKey, RunnerPredecessorID: e.RunnerPredecessorID,
		}}
	}
	return json.Marshal(table)
}

func decodeStrictSnapshotJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("snapshot contains trailing data")
	}
	return nil
}

func unmarshalAutomationSnapshot(raw []byte, executions *[]domain.AutomationRuleExecution) error {
	var table automationExecutionSnapshotTable
	if err := decodeStrictSnapshotJSON(raw, &table); err != nil {
		return err
	}
	if (table.Codec != automationSnapshotCodec && table.Codec != "aut.execution.v4" && table.Codec != "aut.execution.v3" && table.Codec != "aut.execution.v2" && table.Codec != "aut.execution.v1") || table.Rows == nil {
		return errors.New("unsupported automation snapshot codec")
	}
	// Require emitted scalar fields even when false/zero; missing dispatch or
	// cancellation metadata is not a supported schema migration.
	var shape struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	for _, row := range shape.Rows {
		if err := requireAutomationSnapshotFields(row, reflect.TypeOf(domain.AutomationRuleExecution{})); err != nil {
			return err
		}
		var private map[string]json.RawMessage
		if err := json.Unmarshal(row["private"], &private); err != nil {
			return err
		}
		if table.Codec != automationSnapshotCodec {
			if raw, exists := private["runner_condition_semantics"]; exists && string(raw) != `""` {
				return errors.New("old snapshot cannot contain condition semantics")
			}
			private["runner_condition_semantics"] = json.RawMessage(`""`)
		}
		if table.Codec == "aut.execution.v1" {
			// V1 runs retain their admission decision; never opt old queued work
			// into new evaluation semantics, or infer missing decision state.
			if raw, exists := private["runner_evaluation"]; exists && string(raw) != `""` {
				return errors.New("v1 snapshot cannot contain sibling evaluation")
			}
			private["runner_evaluation"] = json.RawMessage(`""`)
		}
		if err := requireAutomationSnapshotFields(private, reflect.TypeOf(automationExecutionPrivateSnapshot{})); err != nil {
			return err
		}
	}
	rows := make([]domain.AutomationRuleExecution, len(table.Rows))
	for i, row := range table.Rows {
		if row.Private == nil {
			return errors.New("automation private checkpoint is required")
		}
		e, p := row.AutomationRuleExecution, row.Private
		if e.CancelRequested != p.CancelRequested {
			return errors.New("automation cancellation checkpoint mismatch")
		}
		e.ExecutionKey, e.RunnerContext, e.RunnerNextAction, e.RunnerTargetVersion = p.ExecutionKey, p.RunnerContext, p.RunnerNextAction, p.RunnerTargetVersion
		e.RunnerEvaluation = p.RunnerEvaluation
		e.RunnerConditionSemantics = p.RunnerConditionSemantics
		if table.Codec != automationSnapshotCodec {
			var runner automationRunnerContext
			var evaluation automationSiblingEvaluation
			if (e.RunnerContext != "" && json.Unmarshal([]byte(e.RunnerContext), &runner) != nil) || (e.RunnerEvaluation != "" && json.Unmarshal([]byte(e.RunnerEvaluation), &evaluation) != nil) {
				return errors.New("invalid old snapshot runner metadata")
			}
			if p.RunnerConditionSemantics != "" || evaluation.ConditionSemantics != "" || len(evaluation.Initial.NumericCatalog) > 0 || (evaluation.Observation != nil && len(evaluation.Observation.NumericCatalog) > 0) || (evaluation.Codec != "" && evaluation.Codec != "aut.sibling_conditions.v1") {
				return errors.New("old snapshot cannot contain numeric condition metadata")
			}
			if table.Codec != "aut.execution.v4" && (runner.ActivityPolicy != "" || evaluation.Metadata.ActivityPolicy != "") {
				return errors.New("old snapshot cannot contain activity policy")
			}
			if table.Codec != "aut.execution.v4" && table.Codec != "aut.execution.v3" && (runner.FailurePolicy != "" || evaluation.Metadata.FailurePolicy != "") {
				return errors.New("old snapshot cannot contain continuation policy")
			}
		}
		e.RunnerLease, e.RunnerLeaseUntil, e.RunnerRetryAt = p.RunnerLease, p.RunnerLeaseUntil, p.RunnerRetryAt
		e.FactBatchKey, e.RunnerPredecessorID = p.FactBatchKey, p.RunnerPredecessorID
		if err := validateAutomationSnapshotExecution(e); err != nil {
			return err
		}
		rows[i] = e
	}
	*executions = rows
	return nil
}

func requireAutomationSnapshotFields(fields map[string]json.RawMessage, typ reflect.Type) error {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" || strings.Contains(tag, "omitempty") {
			continue
		}
		name := strings.Split(tag, ",")[0]
		value, ok := fields[name]
		if !ok || (field.Type.Kind() != reflect.Pointer && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return errors.New("automation checkpoint field is missing or null")
		}
	}
	return nil
}

func validateAutomationSnapshotExecution(e domain.AutomationRuleExecution) error {
	invalid := errors.New("invalid automation snapshot checkpoint")
	if e.RunnerConditionSemantics != "" && e.RunnerConditionSemantics != domain.AutomationNumericCustomV1 {
		return invalid
	}
	if e.ID == 0 || e.AccountID == 0 || e.RuleID == 0 || e.ConversationID == 0 || e.Rule != nil || e.Conversation != nil {
		return invalid
	}
	if (e.RunnerLease == "") != (e.RunnerLeaseUntil == nil) {
		return invalid
	}
	switch e.Status {
	case "received", "evaluating", "running", "waiting", "succeeded", "failed", "partially_succeeded", "cancelled", "loop_blocked", "manual_action_required", "no_match", "success", "partial_failed", "depth_exceeded":
	default:
		return invalid
	}
	if e.Status == "no_match" && !validAutomationNoMatch(e) {
		return invalid
	}
	active := e.Status == "received" || e.Status == "running" || e.Status == "waiting" || e.Status == "evaluating"
	if e.ExecutionKey == nil {
		// Legacy audit rows remain readable, but must never become dispatchable.
		if e.RunnerConditionSemantics != "" || e.RunnerEvaluation != "" || active || e.RuleVersion != 0 || e.TargetVersion != 0 || e.EventID != "" || e.RunnerContext != "" || e.RunnerNextAction != 0 || e.RunnerTargetVersion != 0 || e.RunnerLease != "" || e.RunnerLeaseUntil != nil || e.RunnerRetryAt != nil || e.RunnerPredecessorID != nil || e.FactBatchKey != "" {
			return invalid
		}
		return nil
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s:%d:%d", e.AccountID, e.RuleID, e.RuleVersion, e.EventID, e.ConversationID, e.TargetVersion)))
	_, keyErr := hex.DecodeString(*e.ExecutionKey)
	if len(*e.ExecutionKey) != 64 || keyErr != nil || (e.RunnerContext != "" && *e.ExecutionKey != hex.EncodeToString(key[:])) || e.RuleVersion == 0 || e.TargetVersion == 0 || e.EventID == "" {
		return invalid
	}
	if e.RunnerEvaluation != "" {
		c, valid := decodeAutomationSiblingEvaluation(e)
		if !valid {
			return invalid
		}
		if c.Decision == "pending" {
			if e.Status == "evaluating" && e.CompletedAt != nil {
				return invalid
			}
			if e.Status == "cancelled" && (e.CompletedAt == nil || e.RunnerLease != "" || e.RunnerRetryAt != nil) {
				return invalid
			}
			return nil
		}
	}
	if e.RunnerContext == "" {
		if active || e.RunnerNextAction != 0 || (e.RunnerTargetVersion != 0 && e.RunnerTargetVersion != e.TargetVersion) || e.RunnerLease != "" || e.RunnerLeaseUntil != nil || e.RunnerRetryAt != nil {
			return invalid
		}
		return nil
	}
	var runner automationRunnerContext
	var results []automationActionResult
	if decodeStrictSnapshotJSON([]byte(e.RunnerContext), &runner) != nil || decodeStrictSnapshotJSON([]byte(e.ActionResults), &results) != nil || len(runner.Actions) == 0 || len(runner.Actions) > automationMaxSteps || !validAutomationRunnerIdentity(e, runner) || !validAutomationRunnerPlan(runner) || len(results) != len(runner.Actions) || e.RunnerNextAction < 0 || e.RunnerNextAction > len(results) {
		return invalid
	}
	if !active && runner.FailurePolicy == automationContinueDefinite && (e.Status == "succeeded" || e.Status == "failed" || e.Status == "partially_succeeded" || e.Status == "cancelled") {
		if e.CompletedAt == nil || !validAutomationContinuingCheckpoint(e, runner, results, true, automationCheckpointBase(e, runner, results)) {
			return invalid
		}
	}
	if active {
		if e.RunnerNextAction == len(results) || !validAutomationCheckpoint(e, runner, results) || e.CompletedAt != nil {
			return invalid
		}
		// The rolling target must be exactly the last confirmed owner receipt, never
		// a value reconstructed from live CON state during restore.
		if runner.FailurePolicy == "" && e.RunnerNextAction > 0 && (results[e.RunnerNextAction-1].ObjectVersion <= 0 || uint(results[e.RunnerNextAction-1].ObjectVersion) != e.RunnerTargetVersion) {
			return invalid
		}
	}
	return nil
}

// Validate owned frozen definitions and causal links after row insertion inside
// the restore transaction, before a readiness marker or sequence restart. Do not
// query CON: owner snapshots are independent, not a cross-owner consistent cut.
func validateRestoredAutomationSnapshot(tx *gorm.DB) error {
	var executions []domain.AutomationRuleExecution
	if err := tx.Order("id ASC").Find(&executions).Error; err != nil {
		return err
	}
	byID := make(map[uint]domain.AutomationRuleExecution, len(executions))
	for _, e := range executions {
		byID[e.ID] = e
	}
	batchLast := make(map[string]domain.AutomationRuleExecution)
	for _, e := range executions {
		if e.Status == "no_match" && !validAutomationNoMatch(e) {
			return errors.New("automation snapshot no-match contains effects or invalid state")
		}
		if e.ExecutionKey == nil {
			continue
		}
		var frozenProfile domain.AutomationRuleVersion
		if err := tx.Where("account_id = ? AND rule_id = ? AND version = ?", e.AccountID, e.RuleID, e.RuleVersion).First(&frozenProfile).Error; err != nil || frozenProfile.ConditionSemantics != e.RunnerConditionSemantics || (frozenProfile.ConditionSemantics != "" && frozenProfile.ConditionSemantics != domain.AutomationNumericCustomV1) {
			return errors.New("automation snapshot frozen condition profile mismatch")
		}
		if e.FactBatchKey != "" {
			previous := batchLast[e.FactBatchKey]
			if !automationPredecessorMatches(e.RunnerPredecessorID, previous.ID) {
				return errors.New("automation snapshot predecessor chain is incomplete")
			}
			batchLast[e.FactBatchKey] = e

		}

		base, ready, predecessorErr := automationPredecessorVersion(tx, e)
		if predecessorErr != nil {
			return errors.New("automation snapshot predecessor checkpoint is unverified")
		}
		if e.RunnerEvaluation != "" {
			c, valid := decodeAutomationSiblingEvaluation(e)
			if !valid {
				return errors.New("automation snapshot evaluation checkpoint is invalid")
			}
			var version domain.AutomationRuleVersion
			if err := tx.Where("account_id = ? AND rule_id = ? AND version = ?", e.AccountID, e.RuleID, e.RuleVersion).First(&version).Error; err != nil || version.ConditionSemantics != c.ConditionSemantics || version.Conditions != c.Conditions || version.Actions != c.Actions || version.EventName != e.EventName {
				return errors.New("automation snapshot frozen evaluation mismatch")
			}
			if c.Decision != "pending" {
				if !ready || c.Version != base {
					return errors.New("automation snapshot evaluated before proven predecessor")
				}
				if c.Observation != nil {
					projected, err := projectAutomationSiblingConversation(tx, e, c.Initial.Conversation)
					observed := c.Observation.Conversation
					if err != nil || projected.Status != observed.Status || projected.Priority != observed.Priority || !automationOptionalIDEqual(projected.AssigneeID, observed.AssigneeID) || !automationOptionalIDEqual(projected.TeamID, observed.TeamID) {
						return errors.New("automation snapshot sibling projection mismatch")
					}
				}
			}
		}
		if e.RunnerContext != "" {
			var version domain.AutomationRuleVersion
			if err := tx.Where("account_id = ? AND rule_id = ? AND version = ?", e.AccountID, e.RuleID, e.RuleVersion).First(&version).Error; err != nil {
				return errors.New("automation snapshot frozen version is missing")
			}
			var runner automationRunnerContext
			if err := decodeStrictSnapshotJSON([]byte(e.RunnerContext), &runner); err != nil {
				return err
			}
			var results []automationActionResult
			if err := decodeStrictSnapshotJSON([]byte(e.ActionResults), &results); err != nil {
				return err
			}
			untouched := e.RunnerNextAction == 0 && (results[0].Status == "pending" || results[0].Status == "skipped")
			if !ready {
				if !untouched || e.RunnerTargetVersion != e.TargetVersion {
					return errors.New("automation snapshot progressed before its predecessor")
				}
			} else {
				for i, result := range results {
					if i < e.RunnerNextAction {
						next, valid := automationCheckpointStep(runner, results, i, base)
						if !valid || !validAutomationDispatch(runner, e, results, i, base) {
							return errors.New("automation snapshot owner receipt prefix is discontinuous")
						}
						base = next
					} else if result.ObjectVersion != 0 {
						return errors.New("automation snapshot unconfirmed owner version")
					}
				}
				if e.RunnerTargetVersion != base && !(untouched && e.RunnerTargetVersion == e.TargetVersion) {
					return errors.New("automation snapshot target checkpoint is unverified")
				}
			}
			actions, failure := compileAutomationRunnerActions(version.Actions, runner.FailurePolicy)
			if failure != "" || !reflect.DeepEqual(actions, runner.Actions) || version.EventName != e.EventName {
				return errors.New("automation snapshot frozen plan mismatch")
			}
		}
		if e.RunnerPredecessorID != nil {
			p, ok := byID[*e.RunnerPredecessorID]
			// Strictly descending IDs make every chain finite and acyclic.
			if !ok || p.ID >= e.ID || p.RuleID >= e.RuleID || p.AccountID != e.AccountID || p.ConversationID != e.ConversationID || p.EventID != e.EventID || p.TargetVersion != e.TargetVersion || p.AutomationDepth != e.AutomationDepth || !automationOptionalIDEqual(p.OriginExecutionID, e.OriginExecutionID) || !automationOptionalIDEqual(p.MessageID, e.MessageID) || e.FactBatchKey == "" || p.FactBatchKey != e.FactBatchKey {
				return errors.New("automation snapshot predecessor is invalid")
			}
		}
		if e.OriginExecutionID != nil {
			origin, ok := byID[*e.OriginExecutionID]
			if !ok || origin.ID >= e.ID || origin.AccountID != e.AccountID || e.AutomationDepth != origin.AutomationDepth+1 {
				return errors.New("automation snapshot origin is invalid")
			}
		} else if e.AutomationDepth != 0 {
			return errors.New("automation snapshot origin is missing")
		}
	}
	return validateAutomationCascadeReceipts(tx)
}

// The execution-table codec also bounds rule/profile metadata in the same AUT
// snapshot, including snapshots containing only prepared rules and no runs.
func validateAutomationSnapshotProfiles(tables map[string]json.RawMessage) error {
	var envelope struct {
		Codec string `json:"codec"`
	}
	if err := json.Unmarshal(tables["automation_rule_executions"], &envelope); err != nil {
		return errors.New("automation snapshot codec is invalid")
	}
	for _, table := range []string{"automation_rules", "automation_rule_versions"} {
		var rows []struct {
			ConditionSemantics string `json:"condition_semantics"`
		}
		if err := json.Unmarshal(tables[table], &rows); err != nil {
			return errors.New("automation snapshot profile metadata is invalid")
		}
		for _, row := range rows {
			if row.ConditionSemantics != "" && (envelope.Codec != automationSnapshotCodec || row.ConditionSemantics != domain.AutomationNumericCustomV1) {
				return errors.New("automation snapshot rule profile is unsupported by its codec")
			}
		}
	}
	return nil
}
