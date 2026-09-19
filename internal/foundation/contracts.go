package foundation

import (
	"errors"
	"time"
)

const ModuleContractVersion = "v1"

type ContractActor struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	AuthSource string `json:"auth_source"`
}

type ContractTarget struct {
	Type            string `json:"type"`
	ID              string `json:"id"`
	ExpectedVersion uint64 `json:"expected_version,omitempty"`
}

type TimeoutPolicy struct {
	TimeoutMS    int    `json:"timeout_ms"`
	OnTimeout    string `json:"on_timeout"`
	StatusQuery  string `json:"status_query"`
	Compensation string `json:"compensation,omitempty"`
}

type CommandEnvelope struct {
	CommandID      string         `json:"command_id"`
	CommandType    string         `json:"command_type"`
	AccountID      uint           `json:"account_id"`
	Actor          ContractActor  `json:"actor"`
	Target         ContractTarget `json:"target"`
	Input          map[string]any `json:"input"`
	IdempotencyKey string         `json:"idempotency_key"`
	CorrelationID  string         `json:"correlation_id"`
	CausationID    string         `json:"causation_id"`
	RequestedAt    time.Time      `json:"requested_at"`
	TimeoutPolicy  TimeoutPolicy  `json:"timeout_policy"`
}

type QueryEnvelope struct {
	QueryType   string         `json:"query_type"`
	AccountID   uint           `json:"account_id"`
	Actor       ContractActor  `json:"actor"`
	Filter      map[string]any `json:"filter"`
	Sort        []string       `json:"sort"`
	Page        string         `json:"page,omitempty"`
	Cursor      string         `json:"cursor,omitempty"`
	Fields      []string       `json:"fields"`
	Consistency string         `json:"consistency"`
	RequestedAt time.Time      `json:"requested_at"`
}

type EventEnvelope struct {
	EventID            string         `json:"event_id"`
	EventType          string         `json:"event_type"`
	EventVersion       string         `json:"event_version"`
	SourceModule       string         `json:"source_module"`
	AccountID          uint           `json:"account_id"`
	AggregateType      string         `json:"aggregate_type"`
	AggregateID        string         `json:"aggregate_id"`
	AggregateVersion   uint64         `json:"aggregate_version"`
	OccurredAt         time.Time      `json:"occurred_at"`
	PublishedAt        time.Time      `json:"published_at"`
	Actor              ContractActor  `json:"actor"`
	CorrelationID      string         `json:"correlation_id"`
	CausationID        string         `json:"causation_id"`
	ChangedFields      []string       `json:"changed_fields"`
	Payload            map[string]any `json:"payload"`
	DataClassification string         `json:"data_classification"`
}

type ProcessSignal struct {
	SignalID      string         `json:"signal_id"`
	SignalType    string         `json:"signal_type"`
	Version       string         `json:"version"`
	SourceModule  string         `json:"source_module"`
	AccountID     uint           `json:"account_id"`
	ProcessID     string         `json:"process_id"`
	StepID        string         `json:"step_id"`
	Status        string         `json:"status"`
	CorrelationID string         `json:"correlation_id"`
	CausationID   string         `json:"causation_id"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Payload       map[string]any `json:"payload"`
}

type CapabilityCall struct {
	Capability string           `json:"capability"`
	Command    *CommandEnvelope `json:"command,omitempty"`
	Query      *QueryEnvelope   `json:"query,omitempty"`
}

type ContractError struct {
	Type          string         `json:"type"`
	Message       string         `json:"message"`
	Retryable     bool           `json:"retryable"`
	RetryAfterMS  int            `json:"retry_after_ms,omitempty"`
	CorrelationID string         `json:"correlation_id"`
	Details       map[string]any `json:"details,omitempty"`
}

var ContractErrorTypes = []string{
	"authentication_failed", "permission_denied", "resource_not_found", "validation_failed",
	"state_conflict", "feature_unavailable", "quota_exceeded", "dependency_unavailable",
	"rate_limited", "result_unknown", "permanent_external_failure",
}

var ContractErrorRetryPolicy = map[string]string{
	"authentication_failed": "after_identity_repair", "permission_denied": "never",
	"resource_not_found": "never", "validation_failed": "after_input_change",
	"state_conflict": "after_query", "feature_unavailable": "after_condition_change",
	"quota_exceeded": "after_quota_recovery", "dependency_unavailable": "bounded_backoff",
	"rate_limited": "after_retry_after", "result_unknown": "query_before_retry",
	"permanent_external_failure": "never",
}

func validActor(actor ContractActor) bool {
	return actor.Type != "" && actor.ID != "" && actor.AuthSource != ""
}

func (c CommandEnvelope) Validate() error {
	if c.CommandID == "" || c.CommandType == "" || c.AccountID == 0 || !validActor(c.Actor) || c.Target.Type == "" || c.Target.ID == "" || c.Input == nil || c.IdempotencyKey == "" || c.CorrelationID == "" || c.CausationID == "" || c.RequestedAt.IsZero() || c.TimeoutPolicy.TimeoutMS <= 0 || (c.TimeoutPolicy.OnTimeout != "query" && c.TimeoutPolicy.OnTimeout != "compensate_then_query") || c.TimeoutPolicy.StatusQuery == "" {
		return errors.New("invalid command contract")
	}
	return nil
}

func (c CommandEnvelope) ValidateWithQueries(queries map[string]bool) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if !queries[c.TimeoutPolicy.StatusQuery] {
		return errors.New("timeout status_query is not registered")
	}
	return nil
}

func (q QueryEnvelope) Validate() error {
	if q.QueryType == "" || q.AccountID == 0 || !validActor(q.Actor) || q.Filter == nil || len(q.Sort) == 0 || (q.Page == "" && q.Cursor == "") || len(q.Fields) == 0 || q.Consistency == "" || q.RequestedAt.IsZero() {
		return errors.New("invalid query contract")
	}
	return nil
}

func (e EventEnvelope) Validate() error {
	if e.EventID == "" || e.EventType == "" || e.EventVersion == "" || e.SourceModule == "" || e.AccountID == 0 || e.AggregateType == "" || e.AggregateID == "" || e.AggregateVersion == 0 || e.OccurredAt.IsZero() || e.PublishedAt.IsZero() || !validActor(e.Actor) || e.CorrelationID == "" || e.CausationID == "" || e.ChangedFields == nil || e.Payload == nil || e.DataClassification == "" {
		return errors.New("invalid event contract")
	}
	return nil
}

func (s ProcessSignal) Validate() error {
	if s.SignalID == "" || s.SignalType == "" || s.Version == "" || s.SourceModule == "" || s.AccountID == 0 || s.ProcessID == "" || s.StepID == "" || s.Status == "" || s.CorrelationID == "" || s.CausationID == "" || s.OccurredAt.IsZero() || s.Payload == nil {
		return errors.New("invalid process signal contract")
	}
	return nil
}

func (c CapabilityCall) Validate() error {
	if c.Capability == "" || (c.Command == nil) == (c.Query == nil) {
		return errors.New("invalid capability call contract")
	}
	if c.Command != nil {
		return c.Command.Validate()
	}
	return c.Query.Validate()
}

func (e ContractError) Validate() error {
	policy, ok := ContractErrorRetryPolicy[e.Type]
	if !ok || e.Message == "" || e.CorrelationID == "" {
		return errors.New("invalid error contract")
	}
	if e.Retryable != (policy != "never") {
		return errors.New("error retryability contradicts policy")
	}
	if e.Type == "rate_limited" && e.RetryAfterMS <= 0 {
		return errors.New("rate_limited requires retry_after_ms")
	}
	if e.Type != "rate_limited" && e.RetryAfterMS != 0 {
		return errors.New("retry_after_ms is only valid for rate_limited")
	}
	return nil
}
