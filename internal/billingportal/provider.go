// Package billingportal binds an application tenant to a private billing
// customer and opens an operator-configured, non-subscription-management portal.
// It does not expose payment, subscription, refund, or configuration mutations.
package billingportal

import (
	"context"
	"errors"
)

// Errors are deliberately static. Provider responses, URLs, credentials, and
// transport errors must never become caller-visible error text.
var (
	ErrUnavailable         = errors.New("billing provider unavailable")
	ErrInvalidRequest      = errors.New("invalid billing request")
	ErrInvalidCustomer     = errors.New("invalid billing customer")
	ErrUnsafeConfiguration = errors.New("unsafe billing portal configuration")
	ErrInvalidResponse     = errors.New("invalid billing provider response")
)

type Provider interface {
	Ready() bool
	Scope() string
	CreateCustomer(context.Context, CustomerRequest) (*Customer, error)
	RetrieveCustomer(context.Context, string) (*Customer, error)
	CreatePortal(context.Context, PortalRequest) (*Portal, error)
}

type CustomerRequest struct {
	BindingRef     string
	Currency       string
	IdempotencyKey string
}

type Customer struct {
	ID         string
	BindingRef string
	Currency   string
	Scope      string
	Livemode   bool
}

type PortalRequest struct {
	CustomerID     string
	BindingRef     string
	Currency       string
	IdempotencyKey string
}

type Portal struct {
	URL string
}
