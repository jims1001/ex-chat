package domain

import "time"

// AgentInvitation is TEN-owned. Only a digest of the mailed bearer proof is
// stored. Public responses intentionally omit security and identity bindings.
type AgentInvitation struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	AccountID            uint       `gorm:"not null;uniqueIndex:idx_agent_invitation_mailbox" json:"account_id"`
	Email                string     `gorm:"size:255;not null;uniqueIndex:idx_agent_invitation_mailbox" json:"email"`
	Role                 string     `gorm:"size:50;not null" json:"role"`
	CustomRoleID         *uint      `json:"custom_role_id,omitempty"`
	RoleFingerprint      string     `gorm:"size:64" json:"-"`
	InviterID            uint       `gorm:"not null" json:"-"`
	InviterMembershipID  uint       `gorm:"not null" json:"-"`
	IntendedUserID       *uint      `json:"-"`
	TokenDigest          string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	DeliveryState        string     `gorm:"size:32;not null" json:"delivery_state"`
	ExpiresAt            time.Time  `json:"expires_at"`
	ConsumedAt           *time.Time `json:"consumed_at,omitempty"`
	AcceptedUserID       *uint      `json:"-"`
	AcceptedMembershipID *uint      `json:"-"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// InvitationIdentity is an IAM-owned create-once activation checkpoint, not a
// permission grant. It never authorizes login or tenant membership by itself.
type InvitationIdentity struct {
	ID           uint      `gorm:"primaryKey" json:"-"`
	OperationKey string    `gorm:"size:64;not null;uniqueIndex" json:"-"`
	UserID       uint      `gorm:"not null;uniqueIndex" json:"-"`
	Email        string    `gorm:"size:255;not null" json:"-"`
	CreatedAt    time.Time `json:"-"`
}
