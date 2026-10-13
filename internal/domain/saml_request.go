package domain

import "time"

// SAMLAuthnRequest is IAM-owned durable correlation state. Relay/browser secrets
// are stored only as SHA-256 digests; no assertion XML or identity is retained.
// It is a storage model, never an HTTP response or an authentication credential.
type SAMLAuthnRequest struct {
	ID                  uint       `gorm:"primaryKey" json:"-"`
	RequestID           string     `gorm:"size:256;not null;uniqueIndex" json:"-"`
	AccountID           uint       `gorm:"not null;index" json:"-"`
	ConfigVersion       uint64     `gorm:"not null" json:"-"`
	ConfigDigest        string     `gorm:"size:64;not null" json:"-"`
	RelayDigest         string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	BrowserSecretDigest string     `gorm:"size:64;not null" json:"-"`
	CreatedAt           time.Time  `gorm:"not null" json:"-"`
	ExpiresAt           time.Time  `gorm:"not null;index" json:"-"`
	ConsumedAt          *time.Time `json:"-"`
}

// SAMLReplay records two independent uniqueness constraints. Each digest binds
// the exact trusted issuer and a separately verified assertion/response ID.
// Issuer-wide scope intentionally survives account/configuration changes. This
// is conservative across SPs, avoiding replay holes during configuration rotation.
// RetainUntil is a minimum retention boundary, not permission for request reuse.
// The repository does not perform automatic deletion of security state.
type SAMLReplay struct {
	ID              uint      `gorm:"primaryKey" json:"-"`
	AssertionDigest string    `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ResponseDigest  string    `gorm:"size:64;not null;uniqueIndex" json:"-"`
	CreatedAt       time.Time `gorm:"not null" json:"-"`
	RetainUntil     time.Time `gorm:"not null;index" json:"-"`
}
