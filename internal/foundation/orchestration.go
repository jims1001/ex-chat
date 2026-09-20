package foundation

import (
	"fmt"
	"strings"
	"time"
)

var ProcessStates = map[string]bool{"created": true, "running": true, "waiting": true, "compensating": true, "completed": true, "partially_completed": true, "failed": true, "cancelled": true, "manual_action_required": true}
var ProcessFailurePolicies = map[string]bool{"continue": true, "stop": true, "compensate": true, "manual": true}
var ProcessTypes = map[string]bool{"inbound_message": true, "outbound_message": true, "automatic_assignment": true, "automation_action": true, "ai_answer": true, "campaign_delivery": true, "account_deletion": true, "import_migration": true}
var ProcessStepStatuses = map[string]bool{"accepted": true, "completed": true, "failed": true, "timed_out": true, "result_unknown": true, "compensated": true, "skipped": true}
var ProcessTerminalStates = map[string]bool{"completed": true, "partially_completed": true, "failed": true, "cancelled": true}
var ProcessStepOrders = map[string][]string{
	"inbound_message":      {"channel", "contact", "conversation", "message", "routing"},
	"outbound_message":     {"message", "provider", "status"},
	"automatic_assignment": {"candidate", "assignment", "notification"},
	"automation_action":    {"execution", "target_action"},
	"ai_answer":            {"generation", "reply", "usage"},
	"campaign_delivery":    {"campaign", "message"},
	"account_deletion":     {"schedule", "access", "channels", "contacts", "media", "conversations", "messages", "tickets", "knowledge", "plugins", "notifications", "campaigns", "ai_index", "reporting", "search", "realtime", "audit", "complete"},
	"import_migration":     {"job", "contacts", "conversations", "messages", "reconcile"},
}
var ProcessReferenceKeys = map[string]map[string]bool{
	"inbound_message": {"callback_receipt_id": true, "connection_id": true}, "outbound_message": {"conversation_id": true, "message_id": true},
	"automatic_assignment": {"conversation_id": true, "inbox_id": true}, "automation_action": {"event_id": true, "rule_id": true},
	"ai_answer": {"conversation_id": true, "assistant_id": true}, "campaign_delivery": {"campaign_id": true},
	"account_deletion": {"account_id": true}, "import_migration": {"job_id": true},
}

var ProcessStateTransitions = map[string]map[string]bool{
	"created":                {"running": true, "cancelled": true},
	"running":                {"waiting": true, "compensating": true, "completed": true, "partially_completed": true, "failed": true, "cancelled": true, "manual_action_required": true},
	"waiting":                {"running": true, "compensating": true, "failed": true, "cancelled": true, "manual_action_required": true},
	"compensating":           {"failed": true, "partially_completed": true, "manual_action_required": true},
	"manual_action_required": {"running": true, "compensating": true, "failed": true, "cancelled": true},
}

type ProcessStepDefinition struct {
	ID, Module, Command, ResultQuery, FailurePolicy, Compensation string
	TimeoutMS                                                     int
	Irreversible                                                  bool
	Preconditions                                                 []string
}

func (s ProcessStepDefinition) Validate(commands, queries map[string]bool) error {
	if s.ID == "" || s.Module == "" || !commands[s.Command] || !queries[s.ResultQuery] || s.TimeoutMS <= 0 || !ProcessFailurePolicies[s.FailurePolicy] {
		return fmt.Errorf("invalid process step %s", s.ID)
	}
	if s.FailurePolicy == "compensate" && !commands[s.Compensation] {
		return fmt.Errorf("step %s requires registered compensation", s.ID)
	}
	if s.Irreversible && len(s.Preconditions) == 0 {
		return fmt.Errorf("irreversible step %s requires preconditions", s.ID)
	}
	if strings.Contains(strings.ToLower(s.Compensation), "delete_internal") {
		return fmt.Errorf("step %s uses fake rollback", s.ID)
	}
	return nil
}

func (s ProcessStepDefinition) IdempotencyKey(processID, processVersion string) (string, error) {
	if processID == "" || processVersion == "" || s.ID == "" {
		return "", fmt.Errorf("idempotency key requires process, version and step")
	}
	return processID + ":" + processVersion + ":" + s.ID, nil
}

type ProcessStepResult struct {
	StepID, CommandID, IdempotencyKey, Status, CorrelationID, ChangeID, AuditID string
	Attempt                                                                     uint
	StartedAt, FinishedAt                                                       time.Time
}

func (r ProcessStepResult) Validate() error {
	if r.StepID == "" || r.CommandID == "" || r.IdempotencyKey == "" || !ProcessStepStatuses[r.Status] || r.CorrelationID == "" || r.ChangeID == "" || r.AuditID == "" || r.Attempt == 0 || r.StartedAt.IsZero() || r.FinishedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
		return fmt.Errorf("invalid process step result")
	}
	return nil
}

type ProcessIntent struct {
	ProcessID, ProcessType, ProcessVersion, CorrelationID, CausationID string
	AccountID                                                          uint
	Actor                                                              ContractActor
	State                                                              string
	Version                                                            uint64
	References                                                         map[string]string
	CreatedAt                                                          time.Time
}

func (p ProcessIntent) Validate() error {
	if p.ProcessID == "" || !ProcessTypes[p.ProcessType] || p.ProcessVersion != "v1" || p.CorrelationID == "" || p.CausationID == "" || p.AccountID == 0 || !validActor(p.Actor) || p.State != "created" || p.Version != 1 || len(p.References) == 0 || p.CreatedAt.IsZero() {
		return fmt.Errorf("invalid process intent")
	}
	allowedKeys := ProcessReferenceKeys[p.ProcessType]
	for key, value := range p.References {
		if !allowedKeys[key] || !validStableReference(value) {
			return fmt.Errorf("process references must contain scalar stable ids")
		}
	}
	return nil
}

func validStableReference(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	hasDigit := false
	for _, char := range value {
		if char >= '0' && char <= '9' {
			hasDigit = true
		}
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != ':' && char != '.' {
			return false
		}
	}
	return hasDigit
}

type ProcessCheckpoint struct {
	ProcessID, ProcessType, ProcessVersion, State, CurrentStepID, CorrelationID string
	AccountID                                                                   uint
	Version, ExpectedVersion                                                    uint64
	StepOrder                                                                   []string
	CompletedSteps                                                              []string
	StepResults                                                                 map[string]ProcessStepResult
	UpdatedAt                                                                   time.Time
}

func (p ProcessCheckpoint) Validate() error {
	if p.ProcessID == "" || !ProcessTypes[p.ProcessType] || p.ProcessVersion != "v1" || !ProcessStates[p.State] || p.AccountID == 0 || p.ExpectedVersion == 0 || p.Version != p.ExpectedVersion+1 || p.CorrelationID == "" || len(p.StepOrder) == 0 || p.CompletedSteps == nil || p.StepResults == nil || p.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid process checkpoint")
	}
	approvedOrder := ProcessStepOrders[p.ProcessType]
	if len(approvedOrder) != len(p.StepOrder) {
		return fmt.Errorf("checkpoint step order differs from process definition")
	}
	for index := range approvedOrder {
		if approvedOrder[index] != p.StepOrder[index] {
			return fmt.Errorf("checkpoint step order differs from process definition")
		}
	}
	knownSteps := map[string]bool{}
	for _, stepID := range p.StepOrder {
		if stepID == "" || knownSteps[stepID] {
			return fmt.Errorf("invalid checkpoint step order")
		}
		knownSteps[stepID] = true
	}
	completed := map[string]bool{}
	for _, stepID := range p.CompletedSteps {
		result, ok := p.StepResults[stepID]
		if !knownSteps[stepID] || completed[stepID] || !ok || (result.Status != "completed" && result.Status != "compensated" && result.Status != "skipped") {
			return fmt.Errorf("invalid completed step")
		}
		completed[stepID] = true
	}
	if !ProcessTerminalStates[p.State] && (p.CurrentStepID == "" || !knownSteps[p.CurrentStepID] || completed[p.CurrentStepID]) {
		return fmt.Errorf("resumable process requires current step")
	}
	if p.State == "completed" && len(completed) != len(p.StepOrder) {
		return fmt.Errorf("completed process has unfinished steps")
	}
	for stepID, result := range p.StepResults {
		prefix := p.ProcessID + ":" + p.ProcessVersion + ":" + stepID
		if !knownSteps[stepID] || stepID != result.StepID || result.CorrelationID != p.CorrelationID || result.IdempotencyKey != prefix || result.Validate() != nil {
			return fmt.Errorf("checkpoint contains invalid step result")
		}
		resultCompleted := result.Status == "completed" || result.Status == "compensated" || result.Status == "skipped"
		if resultCompleted != completed[stepID] {
			return fmt.Errorf("step result and completed steps disagree")
		}
	}
	return nil
}

func ValidateProcessTransition(from, to string) error {
	if !ProcessStates[from] || !ProcessStates[to] || !ProcessStateTransitions[from][to] {
		return fmt.Errorf("invalid process transition %s -> %s", from, to)
	}
	return nil
}
