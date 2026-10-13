package domain

import "time"

// BillingCustomer is BIL-owned explicit selection and private customer
// provenance. It is neither a subscription nor a financial balance. API
// handlers return dedicated views, never this private persistence model.
type BillingCustomer struct {
	ID              uint    `gorm:"primaryKey"`
	AccountID       uint    `gorm:"uniqueIndex;not null"`
	Currency        string  `gorm:"size:3;not null"`
	State           string  `gorm:"size:16;not null"`
	IntentID        *string `gorm:"size:64;uniqueIndex"`
	IntentCreatedAt *time.Time
	ProviderScope   string  `gorm:"size:64"`
	CustomerID      *string `gorm:"size:255;uniqueIndex"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
