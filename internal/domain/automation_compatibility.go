package domain

import "time"

// AUT-owned redacted read models. These intentionally never embed persisted rule
// models, raw definitions, action values, titles, arbitrary keys, or config.
const AutomationCompatibilitySchema = "aut.rule_compatibility.v1"
const AutomationCompatibilityTarget = "durable_local_v1"
const AutomationCompatibilityRevision = "phase17.inventory.v1"
const AutomationCompatibilityMaxBytes = 1 << 20

type AutomationCompatibilityIssue struct {
	Code               string                         `json:"code"`
	Category           string                         `json:"category"`
	Severity           string                         `json:"severity"`
	Source             string                         `json:"source"`
	Path               string                         `json:"path"`
	RuntimeEffect      string                         `json:"runtime_effect"`
	ReasonCode         string                         `json:"reason_code,omitempty"`
	ConditionIndex     *int                           `json:"condition_index,omitempty"`
	LogicalActionIndex *int                           `json:"logical_action_index,omitempty"`
	Details            AutomationCompatibilityDetails `json:"details"`
}
type AutomationCompatibilityDetails struct {
	Count         int    `json:"count,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Unit          string `json:"unit,omitempty"`
	KnownOperator string `json:"known_operator,omitempty"`
	KnownEvent    string `json:"known_event,omitempty"`
	KnownAction   string `json:"known_action,omitempty"`
	Owner         string `json:"owner,omitempty"`
}
type AutomationCompatibilitySource struct {
	Source string `json:"source"`
	Event  string `json:"event"`
	// Empty means unchanged legacy; otherwise numeric_custom.v1 or unknown.
	ConditionSemantics   string                         `json:"condition_semantics"`
	Complete             bool                           `json:"complete"`
	Conditions           int                            `json:"conditions"`
	LogicalActions       int                            `json:"logical_actions"`
	AttemptedSteps       int                            `json:"attempted_expanded_steps"`
	CompiledSteps        int                            `json:"compiled_steps"`
	FailedLogicalIndexes []int                          `json:"failed_logical_indexes"`
	RequiredOwners       []string                       `json:"required_owners"`
	AssessmentLevel      string                         `json:"assessment_level"`
	IssuesTruncated      bool                           `json:"issues_truncated"`
	IssuesOmitted        int                            `json:"issues_omitted"`
	Issues               []AutomationCompatibilityIssue `json:"issues"`
}
type AutomationCompatibilityRule struct {
	ID            string                          `json:"id"`
	AccountID     string                          `json:"account_id"`
	Active        bool                            `json:"active"`
	Event         string                          `json:"event"`
	LatestVersion *string                         `json:"latest_version"`
	UpdatedAt     time.Time                       `json:"updated_at"`
	Complete      bool                            `json:"complete"`
	Sources       []AutomationCompatibilitySource `json:"sources"`
}

// The input has no clients or runtime. Byte lengths let guarded SQL projections
// skip oversized blobs before materialization. Zero means the actual Go length.
type AutomationCompatibilityInput struct {
	Rule                                          AutomationRule
	Frozen                                        *AutomationRuleVersion
	Preview                                       *AutomationRuleVersion
	SavedConditionBytes, SavedActionBytes         int
	EffectiveConditionBytes, EffectiveActionBytes int
	Profile                                       AutomationCompatibilityProfile
}

// Facts are supplied by composition, never loaded from secrets or endpoints.
// Absent configuration means unknown; configured does not mean reachable.
type AutomationCompatibilityProfile struct {
	Assessed        bool
	Owners          map[string]bool
	RequestCapBytes map[string]int
}
