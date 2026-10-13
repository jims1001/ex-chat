package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/billingportal"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Both backends run the same contract against physically isolated owners and
// independent BIL connection pools. PostgreSQL is opt-in and uses disposable,
// randomly named schemas only; no production or provider calls are made.
func billingCustomerFixture(t *testing.T, backend string) (*gorm.DB, *gorm.DB, *gorm.DB, *gorm.DB) {
	t.Helper()
	config := func() *gorm.Config {
		return &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), DisableForeignKeyConstraintWhenMigrating: true}
	}
	var open func(string) *gorm.DB
	if backend == "postgres" {
		dsn := os.Getenv("PAYMENT_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("PAYMENT_TEST_POSTGRES_DSN is not configured")
		}
		base, err := gorm.Open(postgres.Open(dsn), config())
		if err != nil {
			t.Fatal(err)
		}
		baseSQL, err := base.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = baseSQL.Close() })
		schemas := map[string]string{}
		open = func(owner string) *gorm.DB {
			schema, exists := schemas[owner]
			if !exists {
				schema = "billingcustomer_" + owner + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
					t.Fatal(err)
				}
				schemas[owner] = schema
				t.Cleanup(func() {
					if err := base.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
						t.Error(err)
					}
				})
			}
			db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), config())
			if err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			pool.SetMaxOpenConns(4)
			t.Cleanup(func() { _ = pool.Close() })
			return db
		}
	} else {
		dir := t.TempDir()
		open = func(owner string) *gorm.DB {
			dsn := filepath.Join(dir, owner+".db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
			db, err := gorm.Open(sqlite.Open(dsn), config())
			if err != nil {
				t.Fatal(err)
			}
			pool, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			pool.SetMaxOpenConns(4)
			t.Cleanup(func() { _ = pool.Close() })
			return db
		}
	}
	bil, bil2, ten, ent := open("bil"), open("bil"), open("ten"), open("ent")
	if err := bil.AutoMigrate(&domain.BillingCustomer{}, &domain.SubscriptionPlan{}, &domain.AccountSubscription{}, &domain.AccountBilling{}, &domain.LocalChangeJournal{}); err != nil {
		t.Fatal(err)
	}
	if err := ten.AutoMigrate(&domain.Account{}); err != nil {
		t.Fatal(err)
	}
	if err := ent.AutoMigrate(&domain.AccountLimit{}, &domain.LocalChangeJournal{}); err != nil {
		t.Fatal(err)
	}
	return bil, bil2, ten, ent
}

func billingCustomerRequireCount(t *testing.T, db *gorm.DB, model any, expected int64) {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%T count=%d, want %d", model, count, expected)
	}
}
func billingCustomerRead(t *testing.T, db *gorm.DB, account uint) domain.BillingCustomer {
	t.Helper()
	var row domain.BillingCustomer
	if err := db.Where("account_id = ?", account).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}
func billingCustomerPrepare(t *testing.T, repo *BillingCustomerRepository, account uint, currency, scope string) *domain.BillingCustomer {
	t.Helper()
	if err := repo.SelectCurrency(context.Background(), account, currency); err != nil {
		t.Fatal(err)
	}
	row, err := repo.Prepare(context.Background(), account, scope, uuid.NewString(), time.Now().UTC(), []string{currency})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestBillingCustomerRepositorySQLite(t *testing.T) { billingCustomerContract(t, "sqlite") }
func TestBillingCustomerRepositoryPostgres(t *testing.T) {
	if os.Getenv("PAYMENT_TEST_POSTGRES_DSN") == "" {
		t.Skip("PAYMENT_TEST_POSTGRES_DSN is not configured")
	}
	billingCustomerContract(t, "postgres")
}

func billingCustomerContract(t *testing.T, backend string) {
	billingCustomerBindingContract(t, backend)
	t.Run("ExplicitCurrencyAndOwnerIsolation", func(t *testing.T) {
		bil, _, ten, ent := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		ctx := context.Background()
		for _, currency := range []string{"", "USD", "Usd", "us", "usdd", " usd", "usd ", "u$d", "€", "åbc"} {
			if err := repo.SelectCurrency(ctx, 7, currency); !errors.Is(err, billingportal.ErrInvalidRequest) {
				t.Fatalf("currency %q: %v", currency, err)
			}
		}
		if err := repo.SelectCurrency(ctx, 0, "usd"); !errors.Is(err, billingportal.ErrInvalidRequest) {
			t.Fatal(err)
		}
		if _, err := repo.Prepare(ctx, 7, "scope-a", uuid.NewString(), time.Now(), []string{"usd"}); !errors.Is(err, billingportal.ErrCurrencyRequired) {
			t.Fatal(err)
		}
		billingCustomerRequireCount(t, bil, &domain.BillingCustomer{}, 0)
		if err := repo.SelectCurrency(ctx, 7, "usd"); err != nil {
			t.Fatal(err)
		}
		if err := repo.SelectCurrency(ctx, 7, "eur"); err != nil {
			t.Fatal(err)
		}
		row := billingCustomerRead(t, bil, 7)
		if row.Currency != "eur" || row.State != "selected" || row.IntentID != nil || row.CustomerID != nil || row.ProviderScope != "" {
			t.Fatalf("selection=%+v", row)
		}
		for _, allowed := range [][]string{nil, {}, {"usd"}, {"EUR"}} {
			if _, err := repo.Prepare(ctx, 7, "scope-a", uuid.NewString(), time.Now(), allowed); !errors.Is(err, billingportal.ErrInvalidRequest) {
				t.Fatalf("allowlist=%v err=%v", allowed, err)
			}
			if after := billingCustomerRead(t, bil, 7); !reflect.DeepEqual(row, after) {
				t.Fatalf("invalid allowlist changed row: before=%+v after=%+v", row, after)
			}
		}
		billingCustomerRequireCount(t, bil, &domain.AccountSubscription{}, 0)
		billingCustomerRequireCount(t, bil, &domain.AccountBilling{}, 0)
		billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 0)
		billingCustomerRequireCount(t, ten, &domain.Account{}, 0)
		billingCustomerRequireCount(t, ent, &domain.AccountLimit{}, 0)
		billingCustomerRequireCount(t, ent, &domain.LocalChangeJournal{}, 0)
		if bil.Migrator().HasTable(&domain.Account{}) || bil.Migrator().HasTable(&domain.AccountLimit{}) || ten.Migrator().HasTable(&domain.BillingCustomer{}) || ent.Migrator().HasTable(&domain.BillingCustomer{}) {
			t.Fatal("owner model leaked")
		}
	})

	t.Run("CompatibilityGuards", func(t *testing.T) {
		for _, kind := range []string{"free", "paid", "unknown-plan", "missing-plan", "legacy-customer", "past-due", "canceling", "canceled", "mispriced-free"} {
			t.Run(kind, func(t *testing.T) {
				bil, _, _, _ := billingCustomerFixture(t, backend)
				repo := NewBillingCustomerRepository(bil)
				ctx := context.Background()
				plan := domain.SubscriptionPlan{Name: "Test", Slug: "free", PriceCents: 0}
				if kind == "paid" {
					plan.Slug = "pro"
					plan.PriceCents = 9900
				}
				if kind == "unknown-plan" {
					plan.Slug = "unknown"
				}
				if kind == "mispriced-free" {
					plan.PriceCents = 1
				}
				if err := bil.Create(&plan).Error; err != nil {
					t.Fatal(err)
				}
				sub := domain.AccountSubscription{AccountID: 21, PlanID: plan.ID, Status: "active", BillingCurrency: "cad"}
				switch kind {
				case "missing-plan":
					sub.PlanID = plan.ID + 999
				case "legacy-customer":
					sub.StripeCustomerID = "cus_legacy"
				case "past-due":
					sub.Status = "past_due"
				case "canceling":
					sub.CancelAtPeriodEnd = true
				case "canceled":
					sub.Status = "canceled"
				}
				if err := bil.Create(&sub).Error; err != nil {
					t.Fatal(err)
				}
				if err := bil.First(&sub, sub.ID).Error; err != nil {
					t.Fatal(err)
				}
				before, _ := json.Marshal(sub)
				err := repo.SelectCurrency(ctx, 21, "usd")
				if kind == "free" {
					if err != nil {
						t.Fatal(err)
					}
					if _, err := repo.Prepare(ctx, 21, "scope-a", uuid.NewString(), time.Now(), []string{"usd"}); err != nil {
						t.Fatal(err)
					}
				} else {
					if !errors.Is(err, billingportal.ErrConflict) {
						t.Fatalf("select=%v", err)
					}
					if _, err := repo.Prepare(ctx, 21, "scope-a", uuid.NewString(), time.Now(), []string{"usd"}); !errors.Is(err, billingportal.ErrConflict) {
						t.Fatalf("prepare=%v", err)
					}
					if _, err := repo.Find(ctx, 21); !errors.Is(err, billingportal.ErrConflict) {
						t.Fatalf("find=%v", err)
					}
					billingCustomerRequireCount(t, bil, &domain.BillingCustomer{}, 0)
				}
				var after domain.AccountSubscription
				if err := bil.First(&after, sub.ID).Error; err != nil {
					t.Fatal(err)
				}
				afterJSON, _ := json.Marshal(after)
				if string(before) != string(afterJSON) {
					t.Fatalf("subscription changed: %s => %s", before, afterJSON)
				}
				billingCustomerRequireCount(t, bil, &domain.SubscriptionPlan{}, 1)
				billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 0)
			})
		}
	})

	t.Run("MalformedRows", func(t *testing.T) {
		cases := map[string]func(*domain.BillingCustomer){
			"unknown-state":      func(r *domain.BillingCustomer) { r.State = "unknown" },
			"upper-currency":     func(r *domain.BillingCustomer) { r.Currency = "USD" },
			"selected-intent":    func(r *domain.BillingCustomer) { v := uuid.NewString(); r.IntentID = &v },
			"selected-scope":     func(r *domain.BillingCustomer) { r.ProviderScope = "scope-a" },
			"selected-customer":  func(r *domain.BillingCustomer) { v := "cus_wrong"; r.CustomerID = &v },
			"creating-no-intent": func(r *domain.BillingCustomer) { r.State = "creating" },
			"ready-no-customer": func(r *domain.BillingCustomer) {
				r.State = "ready"
				v := uuid.NewString()
				r.IntentID = &v
				n := time.Now()
				r.IntentCreatedAt = &n
				r.ProviderScope = "scope-a"
			},
		}
		for name, mutate := range cases {
			t.Run(name, func(t *testing.T) {
				bil, _, _, _ := billingCustomerFixture(t, backend)
				row := domain.BillingCustomer{AccountID: 25, Currency: "usd", State: "selected"}
				mutate(&row)
				if err := bil.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
				before := billingCustomerRead(t, bil, 25)
				repo := NewBillingCustomerRepository(bil)
				ctx := context.Background()
				if err := repo.SelectCurrency(ctx, 25, "eur"); !errors.Is(err, billingportal.ErrConflict) {
					t.Fatalf("select=%v", err)
				}
				if _, err := repo.Prepare(ctx, 25, "scope-a", uuid.NewString(), time.Now(), []string{"usd", "eur"}); !errors.Is(err, billingportal.ErrConflict) {
					t.Fatalf("prepare=%v", err)
				}
				if _, err := repo.Find(ctx, 25); !errors.Is(err, billingportal.ErrConflict) {
					t.Fatalf("find=%v", err)
				}
				if after := billingCustomerRead(t, bil, 25); !reflect.DeepEqual(before, after) {
					t.Fatalf("malformed row repaired: %+v", after)
				}
			})
		}
	})

	t.Run("PrepareScopeAndIntentImmutable", func(t *testing.T) {
		bil, bil2, _, _ := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		ctx := context.Background()
		first := billingCustomerPrepare(t, repo, 30, "usd", "scope-a")
		again, err := NewBillingCustomerRepository(bil2).Prepare(ctx, 30, "scope-a", uuid.NewString(), time.Now().Add(time.Hour), []string{"usd"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("intent changed: %+v => %+v", first, again)
		}
		if _, err := repo.Prepare(ctx, 30, "scope-b", uuid.NewString(), time.Now(), []string{"usd"}); !errors.Is(err, billingportal.ErrConflict) {
			t.Fatalf("scope=%v", err)
		}
		if err := repo.SelectCurrency(ctx, 30, "eur"); !errors.Is(err, billingportal.ErrConflict) {
			t.Fatalf("currency=%v", err)
		}
		if err := repo.SelectCurrency(ctx, 30, "usd"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Prepare(ctx, 30, "scope-a", uuid.NewString(), time.Now(), nil); !errors.Is(err, billingportal.ErrInvalidRequest) {
			t.Fatalf("empty allowlist=%v", err)
		}
		if after := billingCustomerRead(t, bil, 30); !reflect.DeepEqual(*first, after) {
			t.Fatal("immutable intent was reset")
		}
		// Competing process handles cannot replace the first intent or its clock.
		results := make(chan *domain.BillingCustomer, 8)
		errs := make(chan error, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				db := bil
				if n%2 == 1 {
					db = bil2
				}
				row, err := NewBillingCustomerRepository(db).Prepare(ctx, 30, "scope-a", uuid.NewString(), time.Now(), []string{"usd"})
				results <- row
				errs <- err
			}(i)
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		for row := range results {
			if !reflect.DeepEqual(first, row) {
				t.Fatalf("concurrent intent changed: %+v", row)
			}
		}
		if err := repo.SelectCurrency(ctx, 31, "usd"); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Prepare(ctx, 31, "scope-a", *first.IntentID, time.Now(), []string{"usd"}); !errors.Is(err, billingportal.ErrConflict) {
			t.Fatalf("duplicate global intent=%v", err)
		}
		if row := billingCustomerRead(t, bil, 31); row.State != "selected" || row.IntentID != nil || row.ProviderScope != "" {
			t.Fatalf("duplicate intent partially committed: %+v", row)
		}
	})

	t.Run("SelectorPrepareRaceAcrossPools", func(t *testing.T) {
		bil, bil2, _, _ := billingCustomerFixture(t, backend)
		ctx := context.Background()
		repo := NewBillingCustomerRepository(bil)
		repo2 := NewBillingCustomerRepository(bil2)
		for account := uint(50); account < 58; account++ {
			if err := repo.SelectCurrency(ctx, account, "usd"); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			var selected error
			var prepared *domain.BillingCustomer
			var prepareErr error
			go func() { defer wg.Done(); <-start; selected = repo2.SelectCurrency(ctx, account, "eur") }()
			go func() {
				defer wg.Done()
				<-start
				prepared, prepareErr = repo.Prepare(ctx, account, "scope-a", uuid.NewString(), time.Now(), []string{"usd"})
			}()
			close(start)
			wg.Wait()
			row := billingCustomerRead(t, bil, account)
			switch {
			case prepareErr == nil:
				if prepared.Currency != "usd" || row.Currency != "usd" || row.State != "creating" || !errors.Is(selected, billingportal.ErrConflict) {
					t.Fatalf("prepare won but changed: %+v select=%v", row, selected)
				}
			case errors.Is(prepareErr, billingportal.ErrInvalidRequest):
				if selected != nil || row.Currency != "eur" || row.State != "selected" || row.IntentID != nil {
					t.Fatalf("selector won but locked: %+v select=%v", row, selected)
				}
			default:
				t.Fatalf("unexpected prepare failure: %v, select %v", prepareErr, selected)
			}
		}
	})

	t.Run("AllowedCurrencyUnderRowLock", func(t *testing.T) {
		bil, bil2, _, _ := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		ctx := context.Background()
		if err := repo.SelectCurrency(ctx, 60, "usd"); err != nil {
			t.Fatal(err)
		}
		tx := bil2.Begin()
		if tx.Error != nil {
			t.Fatal(tx.Error)
		}
		defer tx.Rollback()
		if err := tx.Model(&domain.BillingCustomer{}).Where("account_id = ?", 60).Update("currency", "eur").Error; err != nil {
			t.Fatal(err)
		}
		reached := make(chan struct{})
		var once sync.Once
		const hook = "billing-customer-test-before-lock"
		if err := bil.Callback().Update().Before("gorm:update").Register(hook, func(db *gorm.DB) {
			if db.Statement.Table == "billing_customers" {
				once.Do(func() { close(reached) })
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer bil.Callback().Update().Remove(hook)
		result := make(chan error, 1)
		go func() {
			_, err := repo.Prepare(ctx, 60, "scope-a", uuid.NewString(), time.Now(), []string{"usd"})
			result <- err
		}()
		select {
		case <-reached:
		case <-time.After(10 * time.Second):
			t.Fatal("prepare never attempted lock")
		}
		if err := tx.Commit().Error; err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, billingportal.ErrInvalidRequest) {
				t.Fatalf("stale allowlist result=%v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("prepare did not finish")
		}
		row := billingCustomerRead(t, bil, 60)
		if row.Currency != "eur" || row.State != "selected" || row.IntentID != nil || row.IntentCreatedAt != nil || row.ProviderScope != "" {
			t.Fatalf("disallowed currency locked: %+v", row)
		}
	})

	t.Run("ReadMissingGuardPrecedence", func(t *testing.T) {
		bil, _, _, _ := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		if _, err := repo.Find(context.Background(), 90); !errors.Is(err, billingportal.ErrCustomerRequired) {
			t.Fatalf("missing=%v", err)
		}
		billingCustomerRequireCount(t, bil, &domain.BillingCustomer{}, 0)
		billingCustomerRequireCount(t, bil, &domain.AccountSubscription{}, 0)
		billingCustomerRequireCount(t, bil, &domain.SubscriptionPlan{}, 0)
	})
}

func billingCustomerResponse(row *domain.BillingCustomer, id string) *billingportal.Customer {
	return &billingportal.Customer{ID: id, BindingRef: *row.IntentID, Currency: row.Currency, Scope: row.ProviderScope}
}
func billingCustomerBindingContract(t *testing.T, backend string) {
	t.Run("ImmutableBindingAndSafeJournal", func(t *testing.T) {
		bil, bil2, _, _ := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		row := billingCustomerPrepare(t, repo, 101, "usd", "private-scope-a")
		ctx := context.Background()
		customer := billingCustomerResponse(row, "cus_private101")
		for name, mutate := range map[string]func(*domain.BillingCustomer){
			"id": func(v *domain.BillingCustomer) { v.ID++ }, "account": func(v *domain.BillingCustomer) { v.AccountID++ }, "currency": func(v *domain.BillingCustomer) { v.Currency = "eur" },
			"scope": func(v *domain.BillingCustomer) { v.ProviderScope = "scope-b" }, "intent": func(v *domain.BillingCustomer) { s := uuid.NewString(); v.IntentID = &s },
			"first-timestamp": func(v *domain.BillingCustomer) { s := v.IntentCreatedAt.Add(time.Second); v.IntentCreatedAt = &s },
		} {
			invalid := *row
			mutate(&invalid)
			if err := repo.Bind(ctx, &invalid, customer, time.Now()); !errors.Is(err, billingportal.ErrConflict) {
				t.Fatalf("tampered %s accepted: %v", name, err)
			}
		}
		for _, field := range []string{"id", "binding", "currency", "scope"} {
			bad := *customer
			switch field {
			case "id":
				bad.ID = "https://evil.test/cus_leak"
			case "binding":
				bad.BindingRef = uuid.NewString()
			case "currency":
				bad.Currency = "eur"
			case "scope":
				bad.Scope = "scope-b"
			}
			if err := repo.Bind(ctx, row, &bad, time.Now()); !errors.Is(err, billingportal.ErrConflict) {
				t.Fatalf("bad customer %s: %v", field, err)
			}
		}
		if err := repo.Bind(ctx, row, customer, time.Now()); err != nil {
			t.Fatal(err)
		}
		ready := billingCustomerRead(t, bil, 101)
		if ready.State != "ready" || ready.CustomerID == nil || *ready.CustomerID != customer.ID || !billingCustomerSameIntent(&ready, row) {
			t.Fatalf("binding=%+v", ready)
		}
		if err := NewBillingCustomerRepository(bil2).Bind(ctx, row, customer, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("stale creating replay=%v", err)
		}
		if err := repo.Bind(ctx, &ready, customer, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("ready replay=%v", err)
		}
		other := *customer
		other.ID = "cus_another"
		if err := repo.Bind(ctx, row, &other, time.Now()); !errors.Is(err, billingportal.ErrConflict) {
			t.Fatalf("rebind=%v", err)
		}
		if after := billingCustomerRead(t, bil, 101); !reflect.DeepEqual(ready, after) {
			t.Fatal("replay changed persisted identity")
		}
		billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 1)
		var journal domain.LocalChangeJournal
		if err := bil.First(&journal).Error; err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(journal)
		for _, secret := range []string{customer.ID, customer.Scope, customer.BindingRef, "billing.stripe.com"} {
			if strings.Contains(string(payload), secret) {
				t.Fatalf("journal leaked %q: %s", secret, payload)
			}
		}
		if journal.SourceModule != "BIL" || journal.ObjectType != "BillingCustomer" || journal.AccountID != row.AccountID || journal.ObjectID != row.ID || journal.BeforeState != `{"currency":"usd","state":"creating"}` || journal.AfterState != `{"currency":"usd","state":"ready"}` {
			t.Fatalf("journal=%s", payload)
		}
	})

	t.Run("GlobalCustomerUniqueness", func(t *testing.T) {
		bil, bil2, _, _ := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		ctx := context.Background()
		first := billingCustomerPrepare(t, repo, 111, "usd", "scope-a")
		second := billingCustomerPrepare(t, NewBillingCustomerRepository(bil2), 112, "usd", "scope-b")
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				db := bil
				if n%2 == 1 {
					db = bil2
				}
				errs <- NewBillingCustomerRepository(db).Bind(ctx, first, billingCustomerResponse(first, "cus_shared"), time.Now())
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := NewBillingCustomerRepository(bil2).Bind(ctx, second, billingCustomerResponse(second, "cus_shared"), time.Now()); !errors.Is(err, billingportal.ErrConflict) {
			t.Fatalf("cross-scope duplicate=%v", err)
		}
		after := billingCustomerRead(t, bil, 112)
		if !reflect.DeepEqual(*second, after) {
			t.Fatal("global uniqueness failure partially committed")
		}
		billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 1)
	})

	t.Run("BindRechecksCompatibility", func(t *testing.T) {
		bil, _, _, _ := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		row := billingCustomerPrepare(t, repo, 120, "usd", "scope-a")
		plan := domain.SubscriptionPlan{Name: "Paid", Slug: "pro", PriceCents: 100}
		if err := bil.Create(&plan).Error; err != nil {
			t.Fatal(err)
		}
		sub := domain.AccountSubscription{AccountID: 120, PlanID: plan.ID, Status: "active"}
		if err := bil.Create(&sub).Error; err != nil {
			t.Fatal(err)
		}
		if err := repo.Bind(context.Background(), row, billingCustomerResponse(row, "cus_conflict"), time.Now()); !errors.Is(err, billingportal.ErrConflict) {
			t.Fatalf("late paid guard=%v", err)
		}
		if after := billingCustomerRead(t, bil, 120); !reflect.DeepEqual(*row, after) {
			t.Fatal("late compatibility failure committed")
		}
		billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 0)
	})

	t.Run("JournalFailureRollback", func(t *testing.T) {
		bil, bil2, _, _ := billingCustomerFixture(t, backend)
		repo := NewBillingCustomerRepository(bil)
		row := billingCustomerPrepare(t, repo, 130, "usd", "scope-a")
		const hook = "billing-customer-test-journal-failure"
		if err := bil.Callback().Create().Before("gorm:create").Register(hook, func(db *gorm.DB) {
			if db.Statement.Table == "local_change_journals" {
				db.AddError(errors.New("synthetic journal failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer bil.Callback().Create().Remove(hook)
		customer := billingCustomerResponse(row, "cus_rollback")
		if err := repo.Bind(context.Background(), row, customer, time.Now()); err == nil {
			t.Fatal("journal fault accepted")
		}
		if after := billingCustomerRead(t, bil2, 130); !reflect.DeepEqual(*row, after) {
			t.Fatalf("partial bind commit: %+v", after)
		}
		billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 0)
		if err := NewBillingCustomerRepository(bil2).Bind(context.Background(), row, customer, time.Now()); err != nil {
			t.Fatal(err)
		}
		billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 1)
	})

	t.Run("ServiceRecoveryAndPortalNoStorage", func(t *testing.T) {
		bil, bil2, ten, ent := billingCustomerFixture(t, backend)
		ctx := context.Background()
		fake := &billingCustomerProviderFake{scope: "private-scope-a", customers: map[string]billingportal.Customer{}}
		account := domain.Account{ID: 150, Name: "Tenant", CustomAttributes: `{"billing_currency":"jpy","private":"preserve"}`}
		if err := ten.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		limit := domain.AccountLimit{AccountID: 150, Credits: 4321, Currency: "EUR"}
		if err := ent.Create(&limit).Error; err != nil {
			t.Fatal(err)
		}
		plan := domain.SubscriptionPlan{Name: "Free", Slug: "free"}
		if err := bil.Create(&plan).Error; err != nil {
			t.Fatal(err)
		}
		sub := domain.AccountSubscription{AccountID: 150, PlanID: plan.ID, Status: "active", BillingCurrency: "cad"}
		if err := bil.Create(&sub).Error; err != nil {
			t.Fatal(err)
		}
		if err := ten.First(&account, account.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := ent.First(&limit, limit.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := bil.First(&sub, sub.ID).Error; err != nil {
			t.Fatal(err)
		}
		svc := billingportal.NewService(NewBillingCustomerRepository(bil), fake, billingportal.Config{AllowedCurrencies: []string{"usd", "eur"}})
		if _, err := svc.Initialize(ctx, 150); !errors.Is(err, billingportal.ErrCurrencyRequired) {
			t.Fatalf("legacy fields inferred currency: %v", err)
		}
		if len(fake.createRequests) != 0 {
			t.Fatal("provider called without explicit selection")
		}
		if err := svc.SelectCurrency(ctx, 150, "usd"); err != nil {
			t.Fatal(err)
		}
		const hook = "billing-customer-test-service-fault"
		if err := bil.Callback().Create().Before("gorm:create").Register(hook, func(db *gorm.DB) {
			if db.Statement.Table == "local_change_journals" {
				db.AddError(errors.New("synthetic post-provider DB failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer bil.Callback().Create().Remove(hook)
		if _, err := svc.Initialize(ctx, 150); err == nil {
			t.Fatal("post-provider DB fault accepted")
		}
		creating := billingCustomerRead(t, bil2, 150)
		if creating.State != "creating" || creating.CustomerID != nil || creating.IntentID == nil || creating.IntentCreatedAt == nil {
			t.Fatalf("lost ambiguity intent: %+v", creating)
		}
		billingCustomerRequireCount(t, bil, &domain.LocalChangeJournal{}, 0)
		restarted := billingportal.NewService(NewBillingCustomerRepository(bil2), fake, billingportal.Config{AllowedCurrencies: []string{"usd", "eur"}})
		ready, err := restarted.Initialize(ctx, 150)
		if err != nil {
			t.Fatal(err)
		}
		if len(fake.createRequests) != 2 || fake.createRequests[0] != fake.createRequests[1] || fake.createRequests[0].IdempotencyKey != "billing-customer-"+*creating.IntentID || ready.CustomerID == nil || !billingCustomerSameIntent(&creating, ready) {
			t.Fatalf("restart did not reuse exact identity: requests=%+v row=%+v", fake.createRequests, ready)
		}
		afterInit := billingCustomerRead(t, bil, 150)
		again, err := restarted.Initialize(ctx, 150)
		if err != nil {
			t.Fatal(err)
		}
		if len(fake.createRequests) != 2 || fake.retrievals != 1 || again.CustomerID == nil || *again.CustomerID != *ready.CustomerID {
			t.Fatal("ready replay recreated customer")
		}
		for i := 0; i < 2; i++ {
			portal, currency, err := restarted.Checkout(ctx, 150)
			if err != nil || portal == nil || currency != "usd" {
				t.Fatalf("portal=%+v currency=%s err=%v", portal, currency, err)
			}
		}
		if len(fake.portalRequests) != 2 || fake.portalRequests[0].IdempotencyKey == fake.portalRequests[1].IdempotencyKey || fake.portalRequests[0].CustomerID != *ready.CustomerID || fake.portalRequests[1].CustomerID != *ready.CustomerID {
			t.Fatalf("portal keys/customer=%+v", fake.portalRequests)
		}
		if after := billingCustomerRead(t, bil, 150); !reflect.DeepEqual(afterInit, after) {
			t.Fatal("portal persisted customer changes")
		}
		var journals []domain.LocalChangeJournal
		if err := bil.Find(&journals).Error; err != nil {
			t.Fatal(err)
		}
		blob, _ := json.Marshal(journals)
		if len(journals) != 1 || strings.Contains(string(blob), "billing.stripe.com") || strings.Contains(string(blob), *ready.CustomerID) || strings.Contains(string(blob), fake.scope) {
			t.Fatalf("unsafe/duplicate journals=%s", blob)
		}
		billingCustomerRequireCount(t, bil, &domain.AccountBilling{}, 0)
		billingCustomerRequireCount(t, ent, &domain.LocalChangeJournal{}, 0)
		var afterAccount domain.Account
		var afterLimit domain.AccountLimit
		var afterSub domain.AccountSubscription
		if err := ten.First(&afterAccount, 150).Error; err != nil {
			t.Fatal(err)
		}
		if err := ent.First(&afterLimit, limit.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := bil.First(&afterSub, sub.ID).Error; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(account, afterAccount) || !reflect.DeepEqual(limit, afterLimit) || !reflect.DeepEqual(sub, afterSub) {
			t.Fatalf("owner state mutated: account=%+v limit=%+v sub=%+v", afterAccount, afterLimit, afterSub)
		}
	})
}

type billingCustomerProviderFake struct {
	scope          string
	customers      map[string]billingportal.Customer
	createRequests []billingportal.CustomerRequest
	portalRequests []billingportal.PortalRequest
	retrievals     int
}

func (f *billingCustomerProviderFake) Ready() bool   { return true }
func (f *billingCustomerProviderFake) Scope() string { return f.scope }
func (f *billingCustomerProviderFake) CreateCustomer(_ context.Context, request billingportal.CustomerRequest) (*billingportal.Customer, error) {
	f.createRequests = append(f.createRequests, request)
	if old, ok := f.customers[request.IdempotencyKey]; ok {
		return &old, nil
	}
	customer := billingportal.Customer{ID: "cus_" + strings.ReplaceAll(request.BindingRef, "-", ""), BindingRef: request.BindingRef, Currency: request.Currency, Scope: f.scope}
	f.customers[request.IdempotencyKey] = customer
	return &customer, nil
}
func (f *billingCustomerProviderFake) RetrieveCustomer(_ context.Context, id string) (*billingportal.Customer, error) {
	f.retrievals++
	for _, customer := range f.customers {
		if customer.ID == id {
			return &customer, nil
		}
	}
	return nil, billingportal.ErrInvalidCustomer
}
func (f *billingCustomerProviderFake) CreatePortal(_ context.Context, request billingportal.PortalRequest) (*billingportal.Portal, error) {
	f.portalRequests = append(f.portalRequests, request)
	return &billingportal.Portal{URL: fmt.Sprintf("https://billing.stripe.com/p/session-%d", len(f.portalRequests))}, nil
}
