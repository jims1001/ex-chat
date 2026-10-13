// Package payment isolates provider communication from durable purchase and
// entitlement state. A successful Checkout redirect is never proof of payment.
package payment

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUnavailable    = errors.New("payment provider unavailable")
	ErrInvalidWebhook = errors.New("invalid payment webhook")
	ErrInvalidRequest = errors.New("invalid payment request")
	ErrMismatch       = errors.New("payment details mismatch")
)

// Provider returns authenticated provider data. Callers must still match it to
// a durable intent, apply their paid/completed policy, and enforce idempotency.
type Provider interface {
	Ready() bool
	CreateCheckout(context.Context, CheckoutRequest) (*Session, error)
	RetrieveCheckout(context.Context, string) (*Session, error)
	VerifyWebhook([]byte, string, time.Time) (*Event, error)
}

type CheckoutRequest struct {
	IntentID       string
	AmountMinor    int64
	Currency       string
	Product        string
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string
}

type Session struct {
	ID            string
	URL           string
	ReferenceID   string
	Currency      string
	Product       string
	PaymentStatus string
	Status        string
	Mode          string
	AmountMinor   int64
	Quantity      int64
	Livemode      bool
	Created       int64
}

// Event is a verified notification, not payment authority. The application must
// retrieve SessionID with the provider before fulfilling a durable intent.
// Digest covers the exact body, allowing durable event-ID/body conflict checks.
type Event struct {
	ID        string
	Type      string
	SessionID string
	Digest    string
	Livemode  bool
}
