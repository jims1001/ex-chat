package domain

import "time"

// ChannelConnection is a CHN-owned, verified provider recipient mapping. Secrets
// never enter Inbox.ProviderConfig or a JSON/API representation.
type ChannelConnection struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	AccountID         uint       `gorm:"index;not null" json:"account_id"`
	InboxID           uint       `gorm:"uniqueIndex;not null" json:"inbox_id"`
	Provider          string     `gorm:"size:20;index;not null" json:"provider"`
	ProviderAccountID string     `gorm:"size:100;not null" json:"provider_account_id"`
	RecipientID       string     `gorm:"size:100;index;not null" json:"recipient_id"`
	RecipientKey      string     `gorm:"size:64;uniqueIndex;not null" json:"recipient_key"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	SecretCiphertext  string     `gorm:"type:text;not null" json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ChannelContactIdentity serializes CUS channel identity creation across workers.
// ContactInbox remains the authoritative public identity relation.
type ChannelContactIdentity struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	InboxID   uint      `gorm:"index;not null" json:"inbox_id"`
	ContactID uint      `json:"contact_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ChannelConversationSession serializes CON session creation across workers.
type ChannelConversationSession struct {
	Key            string    `gorm:"primaryKey;size:64" json:"key"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	InboxID        uint      `gorm:"index;not null" json:"inbox_id"`
	ContactID      uint      `gorm:"index;not null" json:"contact_id"`
	ConversationID uint      `json:"conversation_id"`
	CreatedAt      time.Time `json:"created_at"`
}

// ChannelMessageReceipt commits with the MSG message and journal. Retain it
// after message deletion: redelivery must never recreate deleted content.
type ChannelMessageReceipt struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	MessageID uint      `json:"message_id"`
	Digest    string    `gorm:"size:64;not null" json:"digest"`
	CreatedAt time.Time `json:"created_at"`
}
