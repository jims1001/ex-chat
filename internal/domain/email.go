package domain

import "time"

// EmailLog records all outgoing email dispatches (transcripts, notification digests, etc.)
type EmailLog struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	ConversationID *uint     `gorm:"index" json:"conversation_id,omitempty"`
	ToEmail        string    `gorm:"size:255;not null;index" json:"to_email"`
	FromEmail      string    `gorm:"size:255;not null" json:"from_email"`
	Subject        string    `gorm:"size:512;not null" json:"subject"`
	ContentHTML    string    `gorm:"type:text" json:"content_html"`
	ContentText    string    `gorm:"type:text" json:"content_text"`
	Status         string    `gorm:"size:50;not null;default:'sent'" json:"status"` // sent, failed
	Error          string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}
