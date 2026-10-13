package domain

import "time"

const (
	MessageStatusProviderAccepted = "provider_accepted"
	MessageStatusUnknown          = "unknown"
	MessageStatusCancelled        = "cancelled"
	OutboundStateQueued           = "queued"
	OutboundStateClaimed          = "claimed"
	OutboundStateSending          = "sending"
	OutboundStateUnknown          = "unknown"
	OutboundStateSettled          = "settled"
	OutboundStateCancelled        = "cancelled"
)

// OutboundMessageAttempt is MSG-owned durable delivery intent. Routing and text
// are immutable snapshots; credentials never belong in this record. Body is not
// part of public JSON or journal/event payloads.
type OutboundMessageAttempt struct {
	ID                string     `gorm:"size:36;primaryKey" json:"id"`
	AccountID         uint       `gorm:"not null;uniqueIndex:ux_outbound_message_sequence,priority:1;uniqueIndex:ux_outbound_provider_identity,priority:1;index" json:"account_id"`
	ConversationID    uint       `gorm:"not null;index" json:"conversation_id"`
	MessageID         uint       `gorm:"not null;uniqueIndex:ux_outbound_message_sequence,priority:2;index" json:"message_id"`
	InboxID           uint       `gorm:"not null" json:"inbox_id"`
	ContactID         uint       `gorm:"not null" json:"contact_id"`
	ConnectionID      uint       `gorm:"not null;index" json:"connection_id"`
	Sequence          int        `gorm:"not null;uniqueIndex:ux_outbound_message_sequence,priority:3" json:"sequence"`
	Provider          string     `gorm:"size:50;not null;uniqueIndex:ux_outbound_provider_identity,priority:2" json:"provider"`
	ProviderAccountID string     `gorm:"size:255;not null;uniqueIndex:ux_outbound_provider_identity,priority:3" json:"provider_account_id"`
	SenderID          string     `gorm:"size:255;not null;uniqueIndex:ux_outbound_provider_identity,priority:4" json:"sender_id"`
	RecipientID       string     `gorm:"size:255;not null" json:"recipient_id"`
	ConfigFingerprint string     `gorm:"size:128;not null" json:"-"`
	APIVersion        string     `gorm:"size:50;not null" json:"api_version"`
	CallbackURL       string     `gorm:"type:text;not null" json:"-"`
	Body              string     `gorm:"type:text;not null" json:"-"`
	ContentType       string     `gorm:"size:50;not null" json:"content_type"`
	MessageType       string     `gorm:"size:50;not null" json:"message_type"`
	SenderType        string     `gorm:"size:50;not null" json:"sender_type"`
	ActorID           uint       `gorm:"not null" json:"actor_id"`
	Private           bool       `gorm:"not null;default:false" json:"private"`
	State             string     `gorm:"size:30;not null;index:ix_outbound_claim,priority:1" json:"state"`
	Status            string     `gorm:"size:30;not null" json:"status"`
	ProviderMessageID string     `gorm:"size:255;index;uniqueIndex:ux_outbound_provider_identity,priority:5,where:provider_message_id <> ''" json:"provider_message_id"`
	ClaimToken        string     `gorm:"size:36" json:"-"`
	ClaimUntil        *time.Time `gorm:"index:ix_outbound_claim,priority:2" json:"-"`
	DispatchStartedAt *time.Time `json:"dispatch_started_at,omitempty"`
	RetrySafe         bool       `gorm:"not null;default:false" json:"retry_safe"`
	// FailureObserved retains a provider-authoritative failure before delivery.
	// Message/status retain sent progress; delivered/read supersede this diagnostic.
	FailureObserved bool      `gorm:"not null;default:false" json:"-"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
