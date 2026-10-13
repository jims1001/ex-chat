package billingportal

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

type billingServiceStore struct {
	mu       sync.Mutex
	row      *domain.BillingCustomer
	failBind bool
	binds    int
}

func (s *billingServiceStore) SelectCurrency(_ context.Context, a uint, c string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.row != nil && s.row.State != "selected" && s.row.Currency != c {
		return ErrConflict
	}
	if s.row == nil {
		s.row = &domain.BillingCustomer{ID: 1, AccountID: a, State: "selected"}
	}
	s.row.Currency = c
	return nil
}
func (s *billingServiceStore) Prepare(_ context.Context, _ uint, scope, id string, now time.Time, allowed []string) (*domain.BillingCustomer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.row == nil {
		return nil, ErrCurrencyRequired
	}
	permitted := false
	for _, currency := range allowed {
		if currency == s.row.Currency {
			permitted = true
		}
	}
	if !permitted {
		return nil, ErrInvalidRequest
	}
	if s.row.IntentID == nil {
		s.row.IntentID = &id
		s.row.IntentCreatedAt = &now
		s.row.ProviderScope = scope
		s.row.State = "creating"
	}
	out := *s.row
	return &out, nil
}
func (s *billingServiceStore) Find(context.Context, uint) (*domain.BillingCustomer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.row == nil {
		return nil, ErrCustomerRequired
	}
	out := *s.row
	return &out, nil
}
func (s *billingServiceStore) Bind(_ context.Context, _ *domain.BillingCustomer, c *Customer, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failBind {
		return errors.New("synthetic DB failure")
	}
	if s.row.CustomerID == nil {
		s.row.CustomerID = &c.ID
		s.row.State = "ready"
		s.binds++
	}
	return nil
}

type billingServiceProvider struct {
	mu           sync.Mutex
	scope        string
	requests     []CustomerRequest
	customer     *Customer
	unknown, bad bool
	portalKeys   []string
	portalURL    string
}

func (p *billingServiceProvider) Ready() bool   { return true }
func (p *billingServiceProvider) Scope() string { return p.scope }
func (p *billingServiceProvider) CreateCustomer(_ context.Context, r CustomerRequest) (*Customer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, r)
	if p.customer == nil {
		p.customer = &Customer{ID: "cus_local", BindingRef: r.BindingRef, Currency: r.Currency, Scope: p.scope}
	}
	if p.unknown {
		return nil, ErrUnavailable
	}
	out := *p.customer
	if p.bad {
		out.BindingRef = "wrong"
	}
	return &out, nil
}
func (p *billingServiceProvider) RetrieveCustomer(context.Context, string) (*Customer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := *p.customer
	if p.bad {
		out.Currency = "jpy"
	}
	return &out, nil
}
func (p *billingServiceProvider) CreatePortal(_ context.Context, r PortalRequest) (*Portal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.portalKeys = append(p.portalKeys, r.IdempotencyKey)
	raw := p.portalURL
	if raw == "" {
		raw = "https://billing.stripe.com/p/session/ephemeral"
	}
	return &Portal{URL: raw}, nil
}
func billingServiceFixture() (*Service, *billingServiceStore, *billingServiceProvider) {
	store := &billingServiceStore{}
	provider := &billingServiceProvider{scope: strings.Repeat("a", 64)}
	s := NewService(store, provider, Config{AllowedCurrencies: []string{"eur", "usd"}})
	s.now = func() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }
	return s, store, provider
}
func TestBillingCustomerServiceExplicitSelectionAndRecovery(t *testing.T) {
	ctx := context.Background()
	s, store, p := billingServiceFixture()
	_, err := s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrCurrencyRequired)
	require.Empty(t, p.requests)
	view, err := s.Selection(ctx, 91)
	require.NoError(t, err)
	require.True(t, view.Required)
	require.Equal(t, []string{"eur", "usd"}, view.Options)
	require.NoError(t, s.SelectCurrency(ctx, 91, "eur"))
	store.failBind = true
	_, err = s.Initialize(ctx, 91)
	require.Error(t, err)
	require.Nil(t, store.row.CustomerID)
	require.Len(t, p.requests, 1)
	store.failBind = false
	record, err := s.Initialize(ctx, 91)
	require.NoError(t, err)
	require.Equal(t, "ready", record.State)
	require.Equal(t, 1, store.binds)
	require.Len(t, p.requests, 2)
	require.Equal(t, p.requests[0], p.requests[1])
	_, err = s.Initialize(ctx, 91)
	require.NoError(t, err)
	require.Len(t, p.requests, 2)
	require.ErrorIs(t, s.SelectCurrency(ctx, 91, "usd"), ErrConflict)
	p.bad = true
	_, err = s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrInvalidResponse)
	require.Len(t, p.requests, 2)
	_, err = s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrInvalidResponse)
	require.Len(t, p.requests, 2, "mismatch must never create a replacement")
	p.bad = false
	p.scope = strings.Repeat("b", 64)
	_, err = s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrConflict)
	_, _, err = s.Checkout(ctx, 91)
	require.ErrorIs(t, err, ErrConflict)
	require.Empty(t, p.portalKeys)
	require.ErrorIs(t, s.CheckCompatibility(ctx, 91), ErrConflict)
}
func TestBillingCustomerServiceUnknownHorizonNeverMoves(t *testing.T) {
	ctx := context.Background()
	s, store, p := billingServiceFixture()
	require.NoError(t, s.SelectCurrency(ctx, 91, "eur"))
	p.unknown = true
	_, err := s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrUnavailable)
	first := *store.row.IntentCreatedAt
	s.now = func() time.Time { return first.Add(22 * time.Hour) }
	_, err = s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Equal(t, p.requests[0], p.requests[1])
	require.Equal(t, first, *store.row.IntentCreatedAt)
	s.now = func() time.Time { return first.Add(23*time.Hour + time.Second) }
	_, err = s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrConflict)
	require.Len(t, p.requests, 2)
	require.Nil(t, store.row.CustomerID)
	require.ErrorIs(t, s.CheckCompatibility(ctx, 91), ErrConflict)
	view, err := s.Selection(ctx, 91)
	require.NoError(t, err)
	require.Equal(t, "reconciliation_required", view.Status)
	require.False(t, view.Required)
	s.now = func() time.Time { return first.Add(-time.Second) }
	_, err = s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrConflict)
	require.Len(t, p.requests, 2)
}
func TestBillingCustomerServiceConcurrentIntentAndEphemeralPortal(t *testing.T) {
	ctx := context.Background()
	s, store, p := billingServiceFixture()
	require.NoError(t, s.SelectCurrency(ctx, 91, "eur"))
	var group sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() { defer group.Done(); _, err := s.Initialize(ctx, 91); errs <- err }()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for _, request := range p.requests {
		require.Equal(t, p.requests[0], request)
	}
	require.Equal(t, 1, store.binds)
	for i := 0; i < 2; i++ {
		portal, currency, err := s.Checkout(ctx, 91)
		require.NoError(t, err)
		require.Equal(t, "eur", currency)
		require.Contains(t, portal.URL, "billing.stripe.com")
	}
	require.NotEqual(t, p.portalKeys[0], p.portalKeys[1])
	p.portalURL = "https://evil.example/secret"
	_, _, err := s.Checkout(ctx, 91)
	require.ErrorIs(t, err, ErrInvalidResponse)
}
func TestBillingCustomerServiceConfigSnapshotAndRemovedCurrency(t *testing.T) {
	ctx := context.Background()
	store := &billingServiceStore{}
	p := &billingServiceProvider{scope: strings.Repeat("a", 64)}
	currencies := []string{"usd"}
	s := NewService(store, p, Config{AllowedCurrencies: currencies})
	currencies[0] = "eur"
	require.NoError(t, s.SelectCurrency(ctx, 91, "usd"))
	require.ErrorIs(t, s.SelectCurrency(ctx, 91, "eur"), ErrInvalidRequest)
	options := s.CurrencyOptions()
	options[0] = "jpy"
	require.Equal(t, []string{"usd"}, s.CurrencyOptions())
	removed := NewService(store, p, Config{AllowedCurrencies: []string{"eur"}})
	_, err := removed.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrInvalidRequest)
	require.Nil(t, store.row.IntentID)
	require.Empty(t, p.requests)
	require.NoError(t, removed.SelectCurrency(ctx, 91, "eur"))
	_, err = removed.Initialize(ctx, 91)
	require.NoError(t, err)
	old := *store.row
	_, err = s.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrInvalidRequest)
	require.Equal(t, old, *store.row)
	require.ErrorIs(t, s.SelectCurrency(ctx, 91, "usd"), ErrConflict)
	for _, bad := range [][]string{nil, {""}, {"USD"}, {"usd", "bad-value"}} {
		unconfigured := NewService(&billingServiceStore{}, p, Config{AllowedCurrencies: bad})
		require.Empty(t, unconfigured.CurrencyOptions())
		require.ErrorIs(t, unconfigured.SelectCurrency(ctx, 91, "usd"), ErrInvalidRequest)
	}
	var unavailable *Service
	_, err = unavailable.Initialize(ctx, 91)
	require.ErrorIs(t, err, ErrUnavailable)
	_, _, err = unavailable.Checkout(ctx, 91)
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, unavailable.SelectCurrency(ctx, 91, "usd"), ErrUnavailable)
}
