package domain

import "time"

// Delivery status constants for EmailLog
const (
	EmailDeliveryStatusPending   = "pending"
	EmailDeliveryStatusSent      = "sent"
	EmailDeliveryStatusDelivered = "delivered"
	EmailDeliveryStatusBounced   = "bounced"
	EmailDeliveryStatusFailed    = "failed"
)

// EmailLog records all outgoing email dispatches (transcripts, confirmation emails, notification digests, etc.)
type EmailLog struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	AccountID      uint       `gorm:"index;not null" json:"account_id"`
	ConversationID *uint      `gorm:"index" json:"conversation_id,omitempty"`
	UserID         *uint      `gorm:"index" json:"user_id,omitempty"`
	EmailType      string     `gorm:"size:50;default:'transcript'" json:"email_type"` // transcript, confirmation, reset_password, notification
	ToEmail        string     `gorm:"size:255;not null;index" json:"to_email"`
	FromEmail      string     `gorm:"size:255;not null" json:"from_email"`
	Subject        string     `gorm:"size:512;not null" json:"subject"`
	ContentHTML    string     `gorm:"type:text" json:"content_html"`
	ContentText    string     `gorm:"type:text" json:"content_text"`
	Status         string     `gorm:"size:50;not null;default:'sent'" json:"status"`          // sent, failed, bounced
	DeliveryStatus string     `gorm:"size:50;not null;default:'sent'" json:"delivery_status"` // pending, sent, delivered, bounced, failed
	BounceReason   string     `gorm:"type:text" json:"bounce_reason,omitempty"`
	RetryCount     int        `gorm:"default:0" json:"retry_count"`
	MaxRetries     int        `gorm:"default:3" json:"max_retries"`
	NextRetryAt    *time.Time `json:"next_retry_at,omitempty"`
	LastAttemptAt  *time.Time `json:"last_attempt_at,omitempty"`
	Error          string     `gorm:"type:text" json:"error,omitempty"`
	CreatedAt      time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
