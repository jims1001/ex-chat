package billingportal

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
)

var (
	ErrCurrencyRequired = errors.New("explicit billing currency selection required")
	ErrConflict         = errors.New("billing customer requires reconciliation")
	ErrCustomerRequired = errors.New("billing customer initialization required")
)

// Store is an exclusively BIL-owned port. Prepare validates the supplied
// immutable configuration snapshot under the same row lock as selection.
type Store interface {
	SelectCurrency(context.Context, uint, string) error
	Prepare(context.Context, uint, string, string, time.Time, []string) (*domain.BillingCustomer, error)
	Find(context.Context, uint) (*domain.BillingCustomer, error)
	Bind(context.Context, *domain.BillingCustomer, *Customer, time.Time) error
}
type Config struct{ AllowedCurrencies []string }
type Service struct {
	store      Store
	provider   Provider
	currencies []string
	now        func() time.Time
}

func NewService(store Store, provider Provider, cfg Config) *Service {
	// Invalid operator lists fail closed; no default, case inference, locale
	// mapping, or mutable caller slice becomes billing authority.
	var currencies []string
	seen := map[string]bool{}
	for _, currency := range cfg.AllowedCurrencies {
		if !currencyCode(currency) {
			currencies = nil
			break
		}
		if !seen[currency] {
			currencies = append(currencies, currency)
			seen[currency] = true
		}
	}
	return &Service{store: store, provider: provider, currencies: currencies, now: func() time.Time { return time.Now().UTC() }}
}
func currencyCode(v string) bool {
	return len(v) == 3 && strings.Trim(v, "abcdefghijklmnopqrstuvwxyz") == ""
}
func (s *Service) CurrencyOptions() []string {
	if s == nil {
		return []string{}
	}
	return append([]string{}, s.currencies...)
}
func (s *Service) allowed(currency string) bool {
	if !currencyCode(currency) {
		return false
	}
	for _, v := range s.currencies {
		if currency == v {
			return true
		}
	}
	return false
}
func (s *Service) SelectCurrency(ctx context.Context, account uint, currency string) error {
	if s == nil || s.store == nil {
		return ErrUnavailable
	}
	if account == 0 || !s.allowed(currency) {
		return ErrInvalidRequest
	}
	return s.store.SelectCurrency(ctx, account, currency)
}
func (s *Service) ready() bool {
	return s != nil && s.store != nil && s.provider != nil && s.provider.Ready() && s.provider.Scope() != ""
}
func (s *Service) Initialize(ctx context.Context, account uint) (*domain.BillingCustomer, error) {
	if !s.ready() {
		return nil, ErrUnavailable
	}
	if account == 0 {
		return nil, ErrInvalidRequest
	}
	record, err := s.store.Prepare(ctx, account, s.provider.Scope(), uuid.NewString(), s.now(), s.CurrencyOptions())
	if err != nil {
		return nil, err
	}
	if record == nil || record.IntentID == nil || record.ProviderScope != s.provider.Scope() || !s.allowed(record.Currency) {
		return nil, ErrConflict
	}
	var customer *Customer
	if record.CustomerID != nil {
		customer, err = s.provider.RetrieveCustomer(ctx, *record.CustomerID)
	} else {
		// Stripe may prune idempotency entries after 24 hours. Never reuse an
		// ambiguous create beyond the original safe horizon or after clock rollback.
		if expiredIntent(record, s.now()) {
			return nil, ErrConflict
		}
		customer, err = s.provider.CreateCustomer(ctx, CustomerRequest{BindingRef: *record.IntentID, Currency: record.Currency, IdempotencyKey: "billing-customer-" + *record.IntentID})
	}
	if err != nil {
		return nil, err
	}
	if !customerMatches(record, customer) {
		return nil, ErrInvalidResponse
	}
	if record.CustomerID == nil {
		if err = s.store.Bind(ctx, record, customer, s.now()); err != nil {
			return nil, err
		}
		record.CustomerID = &customer.ID
		record.State = "ready"
	}
	return record, nil
}
func expiredIntent(record *domain.BillingCustomer, now time.Time) bool {
	return record.IntentCreatedAt == nil || now.Before(*record.IntentCreatedAt) || now.Sub(*record.IntentCreatedAt) > 23*time.Hour
}
func customerMatches(record *domain.BillingCustomer, customer *Customer) bool {
	return record != nil && record.IntentID != nil && customer != nil && strings.HasPrefix(customer.ID, "cus_") && len(customer.ID) > 4 && customer.BindingRef == *record.IntentID && customer.Currency == record.Currency && customer.Scope == record.ProviderScope && (record.CustomerID == nil || *record.CustomerID == customer.ID)
}
func (s *Service) Checkout(ctx context.Context, account uint) (*Portal, string, error) {
	if !s.ready() {
		return nil, "", ErrUnavailable
	}
	if account == 0 {
		return nil, "", ErrInvalidRequest
	}
	record, err := s.store.Find(ctx, account)
	if err != nil {
		return nil, "", err
	}
	if record == nil || record.CustomerID == nil || record.IntentID == nil || record.State != "ready" {
		return nil, "", ErrCustomerRequired
	}
	if record.ProviderScope != s.provider.Scope() || !s.allowed(record.Currency) {
		return nil, "", ErrConflict
	}
	// A later explicit request gets a fresh operation key. Neither the URL nor
	// a portal-session replay record ever enters durable storage or a journal.
	portal, err := s.provider.CreatePortal(ctx, PortalRequest{CustomerID: *record.CustomerID, BindingRef: *record.IntentID, Currency: record.Currency, IdempotencyKey: "billing-portal-" + uuid.NewString()})
	if err != nil {
		return nil, "", err
	}
	if portal == nil || !portalRedirectAllowed(portal.URL) {
		return nil, "", ErrInvalidResponse
	}
	return portal, record.Currency, nil
}
func portalRedirectAllowed(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "billing.stripe.com" && parsed.User == nil && parsed.Fragment == "" && strings.HasPrefix(parsed.Path, "/p/") && !strings.ContainsAny(raw, "\r\n\\")
}

// Selection is additive: historical subscription/TEN currencies are neither
// overwritten nor interpreted as explicit selection for customer operations.
type Selection struct {
	Currency, Status string
	Required         bool
	Options          []string
}

func (s *Service) Selection(ctx context.Context, account uint) (Selection, error) {
	result := Selection{Status: "unavailable", Required: true, Options: s.CurrencyOptions()}
	if s == nil || s.store == nil {
		return result, nil
	}
	record, err := s.store.Find(ctx, account)
	if errors.Is(err, ErrCustomerRequired) {
		result.Status = "currency_required"
		return result, nil
	}
	if errors.Is(err, ErrConflict) {
		result.Status = "reconciliation_required"
		result.Required = false
		return result, nil
	}
	if err != nil {
		return Selection{}, err
	}
	result.Currency = record.Currency
	result.Status = record.State
	result.Required = false
	if s.recordConflict(record) {
		result.Status = "reconciliation_required"
	} else if record.State == "selected" && !s.allowed(record.Currency) {
		result.Required = true
	}
	return result, nil
}
func (s *Service) recordConflict(record *domain.BillingCustomer) bool {
	if record == nil {
		return true
	}
	if record.State == "selected" {
		return false
	}
	return !s.allowed(record.Currency) || (record.State == "creating" && expiredIntent(record, s.now())) || (s.provider != nil && s.provider.Ready() && record.ProviderScope != s.provider.Scope())
}
func (s *Service) CheckCompatibility(ctx context.Context, account uint) error {
	if s == nil || s.store == nil {
		return nil
	}
	record, err := s.store.Find(ctx, account)
	if errors.Is(err, ErrCustomerRequired) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.recordConflict(record) {
		return ErrConflict
	}
	return nil
}
