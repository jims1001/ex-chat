package modruntime

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// automationHistoryView deliberately does not embed the database model. In
// particular, preloaded rule/conversation associations and all private recovery
// data must never become part of the ordinary API compatibility projection.
type automationHistoryView struct {
	ID                 uint       `json:"id"`
	AccountID          uint       `json:"account_id"`
	RuleID             uint       `json:"rule_id"`
	RuleVersion        uint       `json:"rule_version"`
	EventID            string     `json:"event_id"`
	TargetVersion      uint       `json:"target_version"`
	OriginExecutionID  *uint      `json:"origin_execution_id,omitempty"`
	AutomationDepth    uint       `json:"automation_depth"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	CancelRequested    bool       `json:"cancel_requested"`
	ConversationID     uint       `json:"conversation_id"`
	MessageID          *uint      `json:"message_id,omitempty"`
	EventName          string     `json:"event_name"`
	Status             string     `json:"status"`
	RawStatus          string     `json:"raw_status"`
	ActionResults      string     `json:"action_results"`
	ActionResultsState string     `json:"action_results_state"`
	ResultSchema       string     `json:"result_schema"`
	DurationMs         int64      `json:"duration_ms"`
	TriggeredAt        time.Time  `json:"triggered_at"`
	Error              string     `json:"error,omitempty"`
	ReasonCode         string     `json:"reason_code,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// For durable rows, completed means confirmed success of the entire logical
// action, not just terminality or successful preparation. A queued public message
// is completed with outcome=queued, which makes no delivery assertion. For legacy
// rows, completed only reflects recorded historical status; outcome=legacy_success
// and action_results_state=legacy explicitly distinguish it from owner proof.
// Legacy success cannot verify capability/provider completion. OwnerSteps are raw
// allowlisted owner results; their status vocabulary is intentionally unchanged.
type automationHistoryAction struct {
	ActionName   string                  `json:"action_name"`
	LogicalIndex *int                    `json:"logical_action_index,omitempty"`
	Status       string                  `json:"status"`
	Error        string                  `json:"error,omitempty"`
	Outcome      string                  `json:"outcome"`
	Completed    bool                    `json:"completed"`
	OwnerSteps   []automationHistoryStep `json:"owner_steps"`
}

// Pointers preserve absence: an absent historical index must not become index 0.
// No map of arbitrary persisted JSON is ever copied to a public response.
type automationHistoryStep struct {
	Index         *int   `json:"action_index,omitempty"`
	LogicalIndex  *int   `json:"logical_action_index,omitempty"`
	Capability    string `json:"capability,omitempty"`
	Step          string `json:"step,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason_code,omitempty"`
	ObjectVersion int    `json:"object_version,omitempty"`
	ResourceID    uint   `json:"resource_id,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
}

type automationHistoryStoredResult struct {
	automationHistoryStep
	ActionName string `json:"action_name"`
	Error      string `json:"error"`
}

// AutomationHistoryCompatibilityView is a pure, opt-in compat_v1 read codec.
// It neither repairs checkpoints nor calls owners. action_results remains a JSON
// STRING containing logical legacy-shaped entries plus typed progress details.
//
// action_results_state is one of:
//
//	legacy: persisted monolith names/status; legacy_success is not owner proof
//	logical: exact frozen version + matching plan/results (or early no-effect rows)
//	empty: the exact frozen definition and persisted results are both empty
//	missing / malformed: absent/null or invalid result JSON
//	version_missing / version_mismatch / definition_invalid / plan_mismatch /
//	results_mismatch: insufficient evidence for a logical interpretation
//	unresolved: matching identities but an unknown/inconsistent result prevents
//	            asserting logical completion
//
// Diagnostic states always include an unresolved, completed=false entry rather
// than silently replacing bad/missing evidence with a successful empty array.
// Unknown code-like statuses/reasons/outcomes survive only in typed fields.
// Free-form errors, action parameters, private messages, dispatches, evaluation
// context, associations and arbitrary JSON keys are deliberately excluded.
func AutomationHistoryCompatibilityView(e domain.AutomationRuleExecution, version *domain.AutomationRuleVersion) any {
	v := automationHistoryView{
		ID: e.ID, AccountID: e.AccountID, RuleID: e.RuleID, RuleVersion: e.RuleVersion,
		EventID: e.EventID, TargetVersion: e.TargetVersion, OriginExecutionID: e.OriginExecutionID,
		AutomationDepth: e.AutomationDepth, CompletedAt: e.CompletedAt, CancelRequested: e.CancelRequested,
		ConversationID: e.ConversationID, MessageID: e.MessageID, EventName: e.EventName,
		Status: automationHistoryStatus(e.Status), RawStatus: e.Status, ResultSchema: "compat_v1",
		DurationMs: e.DurationMs, TriggeredAt: e.TriggeredAt, CreatedAt: e.CreatedAt,
		Error: automationHistoryCode(e.Error), ReasonCode: automationHistoryCode(e.Error),
	}
	results, state := automationHistoryDecode(e.ActionResults)
	var entries []automationHistoryAction
	if state == "" {
		entries, state = automationHistoryActions(e, version, results)
	}
	if entries == nil {
		steps := make([]automationHistoryStep, len(results))
		for i := range results {
			steps[i] = automationHistoryPublicStep(results[i].automationHistoryStep)
		}
		entries = []automationHistoryAction{{Status: "unresolved", Outcome: "unresolved", OwnerSteps: steps}}
	}
	encoded, _ := json.Marshal(entries) // Only concrete JSON-safe scalar fields.
	v.ActionResults, v.ActionResultsState = string(encoded), state
	return v
}

func automationHistoryStatus(status string) string {
	switch status {
	case "succeeded":
		return "success"
	case "partially_succeeded":
		return "partial_failed"
	case "loop_blocked":
		return "depth_exceeded"
	}
	return status
}

// Codes have a bounded, non-free-text grammar. This keeps future enum members
// readable without exposing legacy error messages or accepting arbitrary text.
func automationHistoryCode(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':') {
			return ""
		}
	}
	return value
}

func automationHistoryPublicStep(step automationHistoryStep) automationHistoryStep {
	step.Status = automationHistoryCode(step.Status)
	if step.Status == "" {
		step.Status = "unresolved"
	}
	step.Capability = automationHistoryCode(step.Capability)
	step.Step = automationHistoryCode(step.Step)
	step.Reason = automationHistoryCode(step.Reason)
	step.Outcome = automationHistoryCode(step.Outcome)
	return step
}

func automationHistoryDecode(raw string) ([]automationHistoryStoredResult, string) {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "null" {
		return nil, "missing"
	}
	var items []json.RawMessage
	if json.Unmarshal([]byte(raw), &items) != nil || items == nil {
		return nil, "malformed"
	}
	results := make([]automationHistoryStoredResult, 0, len(items))
	state := ""
	for _, item := range items {
		var result automationHistoryStoredResult
		if !automationHistoryDistinctFields(item) || json.Unmarshal(item, &result) != nil || result.Status == "" || result.Index != nil && *result.Index < 0 || result.LogicalIndex != nil && *result.LogicalIndex < 0 || result.ObjectVersion < 0 {
			state = "malformed"
			continue
		}
		results = append(results, result)
	}
	return results, state
}

// Duplicate keys in an identity-bearing row are ambiguous, even if Go's JSON
// decoder would choose the last one. Unknown fields are ignored, not published.
func automationHistoryDistinctFields(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}
	_, err := decoder.Token()
	return err == nil
}

func automationHistoryActions(e domain.AutomationRuleExecution, version *domain.AutomationRuleVersion, results []automationHistoryStoredResult) ([]automationHistoryAction, string) {
	if e.RuleVersion == 0 && e.ExecutionKey == nil && e.RunnerContext == "" && e.RunnerEvaluation == "" {
		entries := make([]automationHistoryAction, 0, len(results))
		for _, result := range results {
			if automationHistoryCode(result.ActionName) == "" || automationHistoryCode(result.Status) == "" || result.Index != nil || result.LogicalIndex != nil || result.Capability != "" || result.Step != "" || result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "" {
				return nil, "results_mismatch"
			}
			status := automationHistoryStatus(automationHistoryCode(result.Status))
			entry := automationHistoryAction{ActionName: automationHistoryCode(result.ActionName), Status: status, Error: automationHistoryCode(result.Error), Outcome: "unresolved", OwnerSteps: []automationHistoryStep{}}
			// Legacy status is a recorded logical result; it cannot prove a message
			// delivery or recover the original position in an unavailable rule.
			if status == "success" {
				entry.Completed, entry.Outcome = true, "legacy_success"
			} else if status == "failed" {
				entry.Outcome = "failed"
			}
			entries = append(entries, entry)
		}
		return entries, "legacy"
	}
	if version == nil {
		return nil, "version_missing"
	}
	if e.RuleVersion == 0 || version.AccountID != e.AccountID || version.RuleID != e.RuleID || version.Version != e.RuleVersion || version.EventName != e.EventName {
		return nil, "version_mismatch"
	}
	definitions, valid := parseAutomationActionDefinitions(version.Actions)
	if !valid || len(definitions) > 100 {
		return nil, "definition_invalid"
	}
	base := e.TargetVersion
	if e.RunnerEvaluation != "" {
		evaluation, ok := decodeAutomationSiblingEvaluation(e)
		if !ok || evaluation.Actions != version.Actions || evaluation.Conditions != version.Conditions {
			return nil, "plan_mismatch"
		}
		if evaluation.Decision == "pending" {
			if e.Status == "evaluating" && e.CompletedAt == nil || e.Status == "cancelled" && e.Error == "cancelled_by_request" && e.CompletedAt != nil {
				entries := make([]automationHistoryAction, len(definitions))
				for i, definition := range definitions {
					index := i
					entry := automationHistoryAction{ActionName: automationHistoryName(definition), LogicalIndex: &index, Status: "pending", Outcome: "not_evaluated", OwnerSteps: []automationHistoryStep{}}
					if e.Status == "cancelled" {
						entry.Status, entry.Outcome, entry.Error = "skipped", "not_executed", "cancelled_by_request"
					}
					entries[i] = entry
				}
				return entries, "logical"
			}
			return nil, "unresolved"
		}
		base = evaluation.Version
	}
	if len(definitions) == 0 && len(results) == 0 && e.RunnerContext == "" {
		return []automationHistoryAction{}, "empty"
	}
	if e.RunnerContext == "" {
		return automationHistoryEarlyResults(e, definitions, results)
	}
	var runner automationRunnerContext
	if json.Unmarshal([]byte(e.RunnerContext), &runner) != nil {
		return nil, "plan_mismatch"
	}
	compiled, failure := compileAutomationRunnerActions(version.Actions, runner.FailurePolicy)
	if failure != "" || len(compiled) == 0 || !reflect.DeepEqual(compiled, runner.Actions) || !validAutomationRunnerPlan(runner) {
		return nil, "plan_mismatch"
	}
	if len(results) != len(compiled) {
		return nil, "results_mismatch"
	}
	if e.RunnerNextAction < 0 || e.RunnerNextAction > len(results) {
		return nil, "results_mismatch"
	}
	// The continuing compiler also gives old strict plans a logical partition.
	// Strip only the added indexes, then require an exact match to the persisted
	// strict plan before using that partition. No name/capability heuristics.
	logicalPlan, failure := compileAutomationContinuingActions(version.Actions)
	if failure != "" || len(logicalPlan) != len(compiled) {
		return nil, "plan_mismatch"
	}
	for i := range compiled {
		logical := logicalPlan[i]
		if runner.FailurePolicy == "" {
			logical.LogicalIndex = nil
		}
		if !reflect.DeepEqual(logical, compiled[i]) {
			return nil, "plan_mismatch"
		}
		result := results[i]
		if result.Index == nil || result.ActionName != "" || !automationResultIdentity(compiled[i], automationHistoryResult(result), i) {
			return nil, "results_mismatch"
		}
	}
	// An unconditional causal sibling has no deferred evaluation. Its original
	// admission version can predate its predecessor's effects. Use only a frozen
	// dispatch's expected version or a successful lookup's unchanged version to
	// recover that historical base; never read the predecessor or a live owner.
	if e.RunnerPredecessorID != nil && e.RunnerEvaluation == "" {
		for i, result := range results {
			if i >= e.RunnerNextAction {
				break
			}
			if compiled[i].Owner != "CUS" && len(runner.Dispatches) == len(compiled) && !automationEmptyDispatch(runner.Dispatches[i]) {
				base = uint(runner.Dispatches[i].Target.ExpectedVersion)
				break
			}
			if result.Status == "succeeded" && compiled[i].Owner == "CUS" && result.ObjectVersion > 0 {
				base = uint(result.ObjectVersion)
				break
			}
		}
		if base < e.TargetVersion {
			return nil, "results_mismatch"
		}
	}
	entries := make([]automationHistoryAction, len(definitions))
	state := "logical"
	// Verify the successful receipt versions as a sequence, starting with the
	// admitted (or validated deferred-evaluation) target, never a live owner.
	proof := make([]bool, len(results))
	blocked := false
	for i, result := range results {
		proof[i] = true
		if result.Status == "succeeded" {
			next, ok := automationResultAdvances(compiled[i], automationHistoryResult(result), base, runner.ActivityPolicy)
			proof[i] = ok && !blocked && base > 0 && i < e.RunnerNextAction
			if proof[i] {
				base = next
			} else {
				blocked = true
			}
		} else if result.Status != "failed" && result.Status != "skipped" {
			blocked = true
		}
	}
	for i, definition := range definitions {
		index := i
		entries[i] = automationHistoryAction{ActionName: automationHistoryName(definition), LogicalIndex: &index, OwnerSteps: []automationHistoryStep{}}
		var actions []automationLocalAction
		var group []automationHistoryStoredResult
		verified := true
		for step, a := range logicalPlan {
			if *a.LogicalIndex == i {
				entries[i].OwnerSteps = append(entries[i].OwnerSteps, automationHistoryPublicStep(results[step].automationHistoryStep))
				actions, group = append(actions, a), append(group, results[step])
				verified = verified && proof[step]
			}
		}
		if verified {
			automationHistoryAggregate(&entries[i], actions, group)
		} else {
			entries[i].Status, entries[i].Outcome = "unresolved", "unresolved"
		}
		if entries[i].Status == "unresolved" {
			state = "unresolved"
		}
	}
	return entries, state
}

func automationHistoryName(definition automationActionDefinition) string {
	if definition.ActionName != "" {
		return automationHistoryCode(definition.ActionName)
	}
	return automationHistoryCode(definition.CapabilityID)
}

func automationHistoryResult(result automationHistoryStoredResult) automationActionResult {
	index := -1
	if result.Index != nil {
		index = *result.Index
	}
	return automationActionResult{Index: index, LogicalIndex: result.LogicalIndex, Capability: result.Capability, Step: result.Step, Status: result.Status, Reason: result.Reason, ObjectVersion: result.ObjectVersion, ResourceID: result.ResourceID, Outcome: result.Outcome}
}

func automationHistoryEarlyResults(e domain.AutomationRuleExecution, definitions []automationActionDefinition, results []automationHistoryStoredResult) ([]automationHistoryAction, string) {
	if len(results) == 0 || len(results) != len(definitions) || e.CompletedAt == nil {
		return nil, "results_mismatch"
	}
	switch e.Status {
	case "no_match":
		if e.Error != "conditions_not_matched" {
			return nil, "results_mismatch"
		}
	case "loop_blocked":
		if e.Error != "automation_loop_guard" {
			return nil, "results_mismatch"
		}
	case "failed":
		switch e.Error {
		case "invalid_frozen_definition", "condition_evaluator_unavailable", "condition_context_unavailable", "action_capability_executor_unavailable", "invalid_action_parameters", "target_version_stale", "rule_changed_before_dispatch", "rule_disabled_before_dispatch", "rule_deleted_before_dispatch", "target_unavailable":
		default:
			return nil, "results_mismatch"
		}
	default:
		return nil, "results_mismatch"
	}
	entries := make([]automationHistoryAction, len(results))
	for i, result := range results {
		status := "skipped"
		if e.Error == "action_capability_executor_unavailable" && i == 0 {
			status = "failed"
		}
		if result.Index == nil || *result.Index != i || result.LogicalIndex != nil || result.Capability != "" || result.Step != "" || result.ActionName != "" || result.Status != status || result.Reason != e.Error || result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "" {
			return nil, "results_mismatch"
		}
		index := i
		entries[i] = automationHistoryAction{ActionName: automationHistoryName(definitions[i]), LogicalIndex: &index, Status: status, Error: automationHistoryCode(result.Reason), Outcome: "not_executed", OwnerSteps: []automationHistoryStep{}}
	}
	return entries, "logical"
}

func automationHistoryAggregate(entry *automationHistoryAction, actions []automationLocalAction, results []automationHistoryStoredResult) {
	entry.Status, entry.Outcome = "unresolved", "unresolved"
	if len(results) == 0 {
		return
	}
	succeeded, effects, failed, skipped, pending, accepted, unknown := 0, 0, 0, 0, 0, 0, 0
	for i, result := range results {
		switch result.Status {
		case "succeeded":
			// Receipt transitions/outcomes were checked across the complete plan.
			// Within this group, success must still be a contiguous prefix.
			if result.Reason != "" || failed > 0 || skipped > 0 || pending > 0 || accepted > 0 || unknown > 0 {
				return
			}
			succeeded++
			if actions[i].Command != "con.automation.message.prepare.v1" && actions[i].Owner != "CUS" {
				effects++
			}
		case "failed":
			failed++
			if entry.Error == "" {
				entry.Error = automationHistoryCode(result.Reason)
			}
		case "skipped":
			skipped++
		case "pending":
			pending++
		case "accepted":
			accepted++
		case "result_unknown":
			unknown++
		default:
			return
		}
		if result.Status != "succeeded" && (result.ObjectVersion != 0 || result.ResourceID != 0 || result.Outcome != "") {
			return
		}
	}
	if entry.Error == "" {
		for _, result := range results {
			if reason := automationHistoryCode(result.Reason); reason != "" {
				entry.Error = reason
				break
			}
		}
	}
	entry.Outcome = "not_completed"
	if succeeded > 0 {
		entry.Outcome = "partial_progress"
	}
	if effects > 0 {
		entry.Outcome = "partial"
	}
	switch {
	case succeeded == len(results):
		entry.Status, entry.Completed, entry.Outcome = "success", true, "completed"
		if outcome := results[len(results)-1].Outcome; outcome != "" {
			entry.Outcome = automationHistoryCode(outcome)
		}
	case unknown > 0:
		entry.Status = "result_unknown"
	case accepted > 0:
		entry.Status = "running"
	case failed > 0:
		entry.Status = "failed"
		if effects > 0 {
			entry.Status = "partial_failed"
		}
	case skipped > 0 && pending == 0:
		entry.Status = "skipped"
		if succeeded == 0 {
			entry.Outcome = "not_executed"
		}
	case pending > 0:
		entry.Status = "pending"
	}
}
