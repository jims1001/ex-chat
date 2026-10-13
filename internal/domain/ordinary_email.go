package domain

import "time"

// OrdinaryEmailIntent is MSG's opt-in global SMTP queue with a private, bounded
// Inbox route for authenticated transcript producers. Only new, explicitly typed
// commands create it; old EmailLogs are never adopted.
// Payload is frozen until owner anonymization. Public JSON never exposes it.
type OrdinaryEmailIntent struct {
	// Nullable identity keeps ordinary contract producers unchanged. Only the
	// Account and Widget bridges assign these fields with the existing receipt.
	RequestScopeHash   *string `gorm:"size:64;uniqueIndex" json:"-"`
	RequestInputHash   string  `gorm:"size:64" json:"-"`
	TranscriptMessages int     `gorm:"not null;default:0" json:"-"`
	ID                 uint    `gorm:"primaryKey" json:"id"`
	AccountID          uint    `gorm:"not null;index" json:"account_id"`
	EmailLogID         uint    `gorm:"not null;uniqueIndex" json:"email_log_id"`
	EmailType          string  `gorm:"size:32;not null" json:"email_type"`
	Payload            string  `gorm:"type:text;not null" json:"-"`
	PayloadHash        string  `gorm:"size:64;not null" json:"-"`
	TransportKey       string  `gorm:"size:64;not null" json:"-"`
	// Empty route columns preserve the existing global TransportKey identity.
	// Inbox routes contain identity only; CHN retains all connection credentials.
	RouteKind        string     `gorm:"size:32" json:"-"`
	RouteAccountID   string     `gorm:"size:20" json:"-"`
	RouteInboxID     string     `gorm:"size:20" json:"-"`
	RouteCreatedAt   string     `gorm:"size:40" json:"-"`
	RouteFingerprint string     `gorm:"size:96" json:"-"`
	CommandID        string     `gorm:"size:255" json:"-"`
	CorrelationID    string     `gorm:"size:255" json:"-"`
	State            string     `gorm:"size:32;not null;index" json:"state"`
	AttemptCount     int        `gorm:"not null" json:"attempt_count"`
	MaxAttempts      int        `gorm:"not null" json:"max_attempts"`
	LatestAttemptID  string     `gorm:"size:36" json:"latest_attempt_id"`
	LeaseToken       string     `gorm:"size:36" json:"-"`
	LeaseUntil       *time.Time `json:"-"`
	NextAttemptAt    *time.Time `gorm:"index" json:"next_attempt_at"`
	ErrorCode        string     `gorm:"size:64" json:"error_code"`
	Anonymized       bool       `gorm:"not null;default:false" json:"anonymized"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// OrdinaryEmailAttempt preserves the durable pre-submission boundary. A process
// lost after SubmittingAt may have sent the message and is never retried.
type OrdinaryEmailAttempt struct {
	ID           string     `gorm:"size:36;primaryKey" json:"id"`
	IntentID     uint       `gorm:"not null;uniqueIndex:idx_ordinary_attempt_sequence" json:"intent_id"`
	AccountID    uint       `gorm:"not null;index" json:"account_id"`
	Sequence     int        `gorm:"not null;uniqueIndex:idx_ordinary_attempt_sequence" json:"sequence"`
	State        string     `gorm:"size:32;not null" json:"state"`
	LeaseToken   string     `gorm:"size:36;not null" json:"-"`
	LeaseUntil   time.Time  `json:"-"`
	SubmittingAt *time.Time `json:"submitting_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	ErrorCode    string     `gorm:"size:64" json:"error_code"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
