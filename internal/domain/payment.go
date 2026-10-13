package domain

import "time"

// PaymentCheckoutIntent is BIL-owned. It preserves the server-priced purchase,
// provider binding and durable fulfillment work across failures and restarts.
// AmountMinor is money; CreditUnits is deliberately a separate integer quantity.
type PaymentCheckoutIntent struct {
	ID             uint      `gorm:"primaryKey" json:"-"`
	IntentID       string    `gorm:"size:64;uniqueIndex;not null" json:"intent_id"`
	AccountID      uint      `gorm:"uniqueIndex:payment_request;not null" json:"account_id"`
	RequestKeyHash string    `gorm:"size:64;uniqueIndex:payment_request;not null" json:"-"`
	RequestHash    string    `gorm:"size:64;not null" json:"-"`
	Kind           string    `gorm:"size:32;not null" json:"kind"`
	Product        string    `gorm:"size:128;not null" json:"product"`
	AmountMinor    int64     `gorm:"not null" json:"amount_minor"`
	Currency       string    `gorm:"size:3;not null" json:"currency"`
	CreditUnits    int64     `json:"credits"`
	SessionID      *string   `gorm:"size:255;uniqueIndex" json:"session_id,omitempty"`
	CheckoutURL    string    `gorm:"type:text" json:"redirect_url,omitempty"`
	SuccessURL     string    `gorm:"type:text" json:"-"`
	CancelURL      string    `gorm:"type:text" json:"-"`
	Status         string    `gorm:"size:32;index;not null" json:"status"`
	BillingID      uint      `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"-"`
}

// PaymentWebhookReceipt belongs only to BIL. No raw webhook or personal/payment
// data is retained; a payload hash detects reuse of an event ID with other data.
type PaymentWebhookReceipt struct {
	ID        uint   `gorm:"primaryKey"`
	EventID   string `gorm:"size:255;uniqueIndex;not null"`
	Digest    string `gorm:"size:64;not null"`
	IntentID  string `gorm:"size:64;index"`
	Status    string `gorm:"size:32;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}
