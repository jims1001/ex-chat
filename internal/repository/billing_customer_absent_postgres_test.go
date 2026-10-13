package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/billingportal"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// This exercises the PostgreSQL READ COMMITTED gap in which Prepare's initial
// write sees no row, but its following SELECT sees a newly committed selection.
// Callback barriers control the ordering without production hooks. A server
// lock observation, rather than an elapsed sleep, proves the competing selector
// waits on the exact initializing transaction.
func TestBillingCustomerInitiallyAbsentPostgres(t *testing.T) {
	if os.Getenv("PAYMENT_TEST_POSTGRES_DSN") == "" {
		t.Skip("PAYMENT_TEST_POSTGRES_DSN is not configured")
	}

	t.Run("allowed_currency_lock", func(t *testing.T) {
		bil, selectorDB, ten, ent := billingCustomerFixture(t, "postgres")
		const account uint = 521
		billingCustomerAbsentSeedOwners(t, bil, ten, ent, account)
		ownersBefore := billingCustomerAbsentOwnerSnapshot(t, bil, ten, ent)
		provider := &billingCustomerAbsentProvider{}
		service := billingportal.NewService(NewBillingCustomerRepository(bil), provider, billingportal.Config{AllowedCurrencies: []string{"usd"}})
		h := billingCustomerAbsentHarness(t, bil, true)
		initialized := h.initialize(service, account)

		write := billingCustomerAbsentAwait(t, h.ctx, h.write.reached, "initial UPDATE")
		if write.err != nil || write.rows != 0 || write.pid == 0 {
			t.Fatalf("initial UPDATE must see no row: %+v", write)
		}
		if err := NewBillingCustomerRepository(selectorDB).SelectCurrency(h.ctx, account, "usd"); err != nil {
			t.Fatalf("insert selection in UPDATE/SELECT gap: %v", err)
		}
		h.write.unblock()
		read := billingCustomerAbsentAwait(t, h.ctx, h.read.reached, "initial customer SELECT")
		if read.err != nil || read.rows != 1 || read.pid != write.pid {
			t.Fatalf("SELECT must see the committed row in the same transaction: write=%+v read=%+v", write, read)
		}

		// A separate open pool and pinned connection prevent backend-PID guesses.
		// This selector represents an older caller whose currency options include
		// EUR; the initializer's current options contain only USD.
		selectorPID, selected := h.selectCurrency(selectorDB, account, "eur")
		pid := billingCustomerAbsentAwait(t, h.ctx, selectorPID, "selector backend PID")
		if pid.err != nil || pid.pid == 0 || pid.pid == read.pid {
			t.Fatalf("selector needs its own backend: %+v; initializer=%d", pid, read.pid)
		}
		billingCustomerAbsentWaitBlocked(t, h.ctx, ten, read.pid, pid.pid, selected)
		provider.assertCalls(t, 0)
		h.read.unblock()

		result := billingCustomerAbsentAwait(t, h.ctx, initialized, "initialization result")
		if result.err != nil || result.row == nil {
			t.Fatalf("initialization: row=%+v err=%v", result.row, result.err)
		}
		if err := billingCustomerAbsentAwait(t, h.ctx, selected, "selector result"); !errors.Is(err, billingportal.ErrConflict) {
			t.Fatalf("selector must conflict after initialization locks currency: %v", err)
		}
		row, err := NewBillingCustomerRepository(selectorDB).Find(h.ctx, account)
		if err != nil || row == nil || row.Currency != "usd" || row.State != "ready" || row.IntentID == nil || row.CustomerID == nil {
			t.Fatalf("durable USD binding: row=%+v err=%v", row, err)
		}
		if result.row.Currency != row.Currency || result.row.IntentID == nil || *result.row.IntentID != *row.IntentID || result.row.CustomerID == nil || *result.row.CustomerID != *row.CustomerID {
			t.Fatalf("returned binding differs from persisted binding: returned=%+v persisted=%+v", result.row, row)
		}
		provider.assertBinding(t, row)
		if n := countPayment(t, bil, &domain.BillingCustomer{}); n != 1 {
			t.Fatalf("customer rows=%d, want 1", n)
		}
		if n := countPayment(t, bil, &domain.LocalChangeJournal{}); n != 1 {
			t.Fatalf("binding journals=%d, want 1", n)
		}
		if after := billingCustomerAbsentOwnerSnapshot(t, bil, ten, ent); after != ownersBefore {
			t.Fatalf("customer initialization changed financial, subscription, TEN, or ENT records\nbefore=%s\nafter=%s", ownersBefore, after)
		}
	})

	t.Run("removed_currency_editable", func(t *testing.T) {
		bil, selectorDB, ten, ent := billingCustomerFixture(t, "postgres")
		const account uint = 522
		billingCustomerAbsentSeedOwners(t, bil, ten, ent, account)
		ownersBefore := billingCustomerAbsentOwnerSnapshot(t, bil, ten, ent)
		provider := &billingCustomerAbsentProvider{}
		service := billingportal.NewService(NewBillingCustomerRepository(bil), provider, billingportal.Config{AllowedCurrencies: []string{"usd"}})
		h := billingCustomerAbsentHarness(t, bil, false)
		initialized := h.initialize(service, account)
		write := billingCustomerAbsentAwait(t, h.ctx, h.write.reached, "initial UPDATE")
		if write.err != nil || write.rows != 0 || write.pid == 0 {
			t.Fatalf("initial UPDATE must see no row: %+v", write)
		}
		if err := NewBillingCustomerRepository(selectorDB).SelectCurrency(h.ctx, account, "eur"); err != nil {
			t.Fatalf("insert now-removed selection in UPDATE/SELECT gap: %v", err)
		}
		journalsBefore := countPayment(t, selectorDB, &domain.LocalChangeJournal{})
		h.write.unblock()
		result := billingCustomerAbsentAwait(t, h.ctx, initialized, "rejected initialization")
		if !errors.Is(result.err, billingportal.ErrInvalidRequest) || result.row != nil {
			t.Fatalf("removed currency must be rejected: row=%+v err=%v", result.row, result.err)
		}
		row, err := NewBillingCustomerRepository(selectorDB).Find(h.ctx, account)
		if err != nil || row == nil || row.Currency != "eur" || row.State != "selected" || row.IntentID != nil || row.IntentCreatedAt != nil || row.ProviderScope != "" || row.CustomerID != nil {
			t.Fatalf("rejection must leave selection editable with no intent/binding: row=%+v err=%v", row, err)
		}
		provider.assertCalls(t, 0)
		if n := countPayment(t, bil, &domain.LocalChangeJournal{}); n != journalsBefore {
			t.Fatalf("rejection wrote a journal: before=%d after=%d", journalsBefore, n)
		}
		if after := billingCustomerAbsentOwnerSnapshot(t, bil, ten, ent); after != ownersBefore {
			t.Fatalf("rejected initialization changed other owner records\nbefore=%s\nafter=%s", ownersBefore, after)
		}

		// Successful editing on a separate connection also proves the failed
		// transaction released its row lock and did not leave an immutable intent.
		selectorService := billingportal.NewService(NewBillingCustomerRepository(selectorDB), provider, billingportal.Config{AllowedCurrencies: []string{"usd"}})
		if err := selectorService.SelectCurrency(h.ctx, account, "usd"); err != nil {
			t.Fatalf("selection remains editable after rejection: %v", err)
		}
		row, err = service.Initialize(h.ctx, account)
		if err != nil || row == nil || row.State != "ready" || row.Currency != "usd" {
			t.Fatalf("allowed-currency retry: row=%+v err=%v", row, err)
		}
		provider.assertBinding(t, row)
		if n := countPayment(t, bil, &domain.BillingCustomer{}); n != 1 {
			t.Fatalf("retry customer rows=%d, want 1", n)
		}
		if n := countPayment(t, bil, &domain.LocalChangeJournal{}); n != journalsBefore+1 {
			t.Fatalf("retry journals=%d, want %d", n, journalsBefore+1)
		}
		if after := billingCustomerAbsentOwnerSnapshot(t, bil, ten, ent); after != ownersBefore {
			t.Fatalf("allowed retry changed financial, subscription, TEN, or ENT records\nbefore=%s\nafter=%s", ownersBefore, after)
		}
	})
}

type billingCustomerAbsentObservation struct {
	pid  int
	rows int64
	err  error
}

type billingCustomerAbsentBarrier struct {
	reached chan billingCustomerAbsentObservation
	release chan struct{}
	once    sync.Once
	closed  sync.Once
}

func (b *billingCustomerAbsentBarrier) unblock() { b.closed.Do(func() { close(b.release) }) }
func (b *billingCustomerAbsentBarrier) hold(tx *gorm.DB) {
	b.once.Do(func() {
		observation := billingCustomerAbsentObservation{rows: tx.RowsAffected, err: tx.Error}
		if observation.err == nil {
			observation.err = tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SELECT pg_backend_pid()").Scan(&observation.pid)
		}
		b.reached <- observation
		select {
		case <-b.release:
		case <-tx.Statement.Context.Done():
			tx.AddError(tx.Statement.Context.Err())
		}
	})
}

type billingCustomerAbsentRun struct {
	ctx     context.Context
	write   *billingCustomerAbsentBarrier
	read    *billingCustomerAbsentBarrier
	workers []<-chan struct{}
}

func billingCustomerAbsentHarness(t *testing.T, db *gorm.DB, pauseRead bool) *billingCustomerAbsentRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	barrier := func() *billingCustomerAbsentBarrier {
		return &billingCustomerAbsentBarrier{reached: make(chan billingCustomerAbsentObservation, 1), release: make(chan struct{})}
	}
	h := &billingCustomerAbsentRun{ctx: ctx, write: barrier(), read: barrier()}
	var wrote atomic.Bool
	const updateCallback = "test:billing_customer_absent_update"
	const queryCallback = "test:billing_customer_absent_query"
	if err := db.Callback().Update().After("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_customers" {
			wrote.Store(true)
			h.write.hold(tx)
		}
	}); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := db.Callback().Query().After("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
		if pauseRead && wrote.Load() && tx.Statement.Table == "billing_customers" {
			h.read.hold(tx)
		}
	}); err != nil {
		cancel()
		_ = db.Callback().Update().Remove(updateCallback)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Release barriers and cancel SQL before removing callbacks or closing
		// fixture pools. Every result channel is buffered, including failure paths.
		h.write.unblock()
		h.read.unblock()
		cancel()
		for _, done := range h.workers {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-done:
			case <-timer.C:
				t.Error("billing customer concurrency worker did not exit after cancellation")
			}
			timer.Stop()
		}
		if err := db.Callback().Query().Remove(queryCallback); err != nil {
			t.Error(err)
		}
		if err := db.Callback().Update().Remove(updateCallback); err != nil {
			t.Error(err)
		}
	})
	return h
}

type billingCustomerAbsentResult struct {
	row *domain.BillingCustomer
	err error
}

func (h *billingCustomerAbsentRun) initialize(service *billingportal.Service, account uint) <-chan billingCustomerAbsentResult {
	result := make(chan billingCustomerAbsentResult, 1)
	done := make(chan struct{})
	h.workers = append(h.workers, done)
	go func() {
		defer close(done)
		row, err := service.Initialize(h.ctx, account)
		result <- billingCustomerAbsentResult{row: row, err: err}
	}()
	return result
}

func (h *billingCustomerAbsentRun) selectCurrency(db *gorm.DB, account uint, currency string) (<-chan billingCustomerAbsentObservation, <-chan error) {
	pid := make(chan billingCustomerAbsentObservation, 1)
	result := make(chan error, 1)
	done := make(chan struct{})
	h.workers = append(h.workers, done)
	go func() {
		defer close(done)
		announced := false
		err := db.WithContext(h.ctx).Connection(func(connection *gorm.DB) error {
			observation := billingCustomerAbsentObservation{}
			observation.err = connection.Statement.ConnPool.QueryRowContext(h.ctx, "SELECT pg_backend_pid()").Scan(&observation.pid)
			pid <- observation
			announced = true
			if observation.err != nil {
				return observation.err
			}
			return NewBillingCustomerRepository(connection).SelectCurrency(h.ctx, account, currency)
		})
		if !announced {
			pid <- billingCustomerAbsentObservation{err: err}
		}
		result <- err
	}()
	return pid, result
}

func billingCustomerAbsentAwait[T any](t *testing.T, ctx context.Context, result <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", description, ctx.Err())
		var zero T
		return zero
	}
}

func billingCustomerAbsentWaitBlocked(t *testing.T, ctx context.Context, observer *gorm.DB, initializingPID, selectorPID int, selected <-chan error) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-selected:
			t.Fatalf("selector completed before initializing SELECT was released (err=%v); absent-row currency lock was bypassed", err)
		default:
		}
		var blocked bool
		if err := observer.WithContext(ctx).Raw("SELECT ? = ANY(pg_blocking_pids(?))", initializingPID, selectorPID).Scan(&blocked).Error; err != nil {
			t.Fatalf("observe PostgreSQL blockers: %v", err)
		}
		if blocked {
			t.Logf("pg_blocking_pids proves selector backend %d waits on initializer backend %d", selectorPID, initializingPID)
			return
		}
		select {
		case err := <-selected:
			t.Fatalf("selector completed before initializing SELECT was released (err=%v); absent-row currency lock was bypassed", err)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("selector never acquired a server-observable wait on initializer: %v", ctx.Err())
		}
	}
}

// This fake implements only the injected provider port. It cannot make network
// calls, and unexpected retrieve/portal operations are counted and rejected.
type billingCustomerAbsentProvider struct {
	mu        sync.Mutex
	creates   []billingportal.CustomerRequest
	retrieves int
	portals   int
}

func (*billingCustomerAbsentProvider) Ready() bool   { return true }
func (*billingCustomerAbsentProvider) Scope() string { return strings.Repeat("a", 64) }
func (p *billingCustomerAbsentProvider) CreateCustomer(ctx context.Context, request billingportal.CustomerRequest) (*billingportal.Customer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.creates = append(p.creates, request)
	p.mu.Unlock()
	return &billingportal.Customer{ID: "cus_absent_" + request.BindingRef, BindingRef: request.BindingRef, Currency: request.Currency, Scope: p.Scope()}, nil
}
func (p *billingCustomerAbsentProvider) RetrieveCustomer(context.Context, string) (*billingportal.Customer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retrieves++
	return nil, billingportal.ErrInvalidRequest
}
func (p *billingCustomerAbsentProvider) CreatePortal(context.Context, billingportal.PortalRequest) (*billingportal.Portal, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.portals++
	return nil, billingportal.ErrInvalidRequest
}
func (p *billingCustomerAbsentProvider) assertCalls(t *testing.T, creates int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.creates) != creates || p.retrieves != 0 || p.portals != 0 {
		t.Fatalf("provider calls: creates=%d retrieves=%d portals=%d; want %d,0,0", len(p.creates), p.retrieves, p.portals, creates)
	}
}
func (p *billingCustomerAbsentProvider) assertBinding(t *testing.T, row *domain.BillingCustomer) {
	t.Helper()
	p.assertCalls(t, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	request := p.creates[0]
	if row == nil || row.IntentID == nil || row.CustomerID == nil || request.Currency != "usd" || request.Currency != row.Currency || request.BindingRef != *row.IntentID || request.IdempotencyKey != "billing-customer-"+*row.IntentID || *row.CustomerID != "cus_absent_"+request.BindingRef || row.ProviderScope != p.Scope() {
		t.Fatalf("provider request did not use the one durable, allowed USD identity: request=%+v row=%+v", request, row)
	}
}

func billingCustomerAbsentSeedOwners(t *testing.T, bil, ten, ent *gorm.DB, account uint) {
	t.Helper()
	plan := domain.SubscriptionPlan{Name: "Existing free plan", Slug: "free", PriceCents: 0}
	if err := bil.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct {
		db    *gorm.DB
		model any
	}{
		{bil, &domain.AccountSubscription{AccountID: account, PlanID: plan.ID, Status: "active", BillingCurrency: "eur"}},
		{bil, &domain.AccountBilling{AccountID: account, ActionType: "existing", Amount: 17, Currency: "EUR", Description: "unchanged sentinel"}},
		{ten, &domain.Account{ID: account, Name: "Existing tenant", CustomAttributes: `{"billing_currency":"eur"}`}},
		{ent, &domain.AccountLimit{AccountID: account, Credits: 321, Currency: "EUR", AgentLimit: 7}},
	} {
		if err := seed.db.Create(seed.model).Error; err != nil {
			t.Fatal(err)
		}
	}
	if n := countPayment(t, bil, &domain.BillingCustomer{}); n != 0 {
		t.Fatalf("customer fixture must start absent: %d rows", n)
	}
}

func billingCustomerAbsentOwnerSnapshot(t *testing.T, bil, ten, ent *gorm.DB) string {
	t.Helper()
	snapshot := make(map[string][]map[string]any)
	for _, owner := range []struct {
		name   string
		db     *gorm.DB
		tables []string
	}{
		{"bil", bil, []string{"account_billings", "account_subscriptions", "subscription_plans"}},
		{"ten", ten, []string{"accounts"}},
		{"ent", ent, []string{"account_limits", "local_change_journals"}},
	} {
		for _, table := range owner.tables {
			var rows []map[string]any
			if err := owner.db.Table(table).Order("id").Find(&rows).Error; err != nil {
				t.Fatalf("snapshot %s.%s: %v", owner.name, table, err)
			}
			snapshot[fmt.Sprintf("%s.%s", owner.name, table)] = rows
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
