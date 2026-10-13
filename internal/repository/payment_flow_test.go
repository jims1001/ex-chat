package repository

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/payment"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Scripted provider is an injected port fake; the Stripe adapter's separate
// protocol tests use a local HTTP server, never a payment network.
type paymentFake struct {
	ready                bool
	requests             []payment.CheckoutRequest
	session              payment.Session
	event                payment.Event
	createErr, verifyErr error
}

func (f *paymentFake) Ready() bool { return f.ready }
func (f *paymentFake) CreateCheckout(_ context.Context, r payment.CheckoutRequest) (*payment.Session, error) {
	f.requests = append(f.requests, r)
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.session = payment.Session{ID: "cs_test_" + r.IntentID, URL: "https://checkout.stripe.com/c/pay/" + r.IntentID, ReferenceID: r.IntentID, AmountMinor: r.AmountMinor, Currency: r.Currency, Product: r.Product, Quantity: 1, Mode: "payment", Status: "open", PaymentStatus: "unpaid"}
	result := f.session
	return &result, nil
}
func (f *paymentFake) RetrieveCheckout(_ context.Context, _ string) (*payment.Session, error) {
	result := f.session
	return &result, nil
}
func (f *paymentFake) VerifyWebhook(_ []byte, _ string, _ time.Time) (*payment.Event, error) {
	if f.verifyErr != nil {
		return nil, f.verifyErr
	}
	result := f.event
	return &result, nil
}
func (f *paymentFake) paid(id string) {
	f.session.Status = "complete"
	f.session.PaymentStatus = "paid"
	f.event = payment.Event{ID: id, Type: "checkout.session.completed", SessionID: f.session.ID, Digest: "digest-" + id}
}

func paymentDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}
func paymentFixture(t *testing.T) (*payment.Service, *paymentFake, *gorm.DB, *gorm.DB) {
	t.Helper()
	bil := paymentDB(t, "bil")
	ent := paymentDB(t, "ent")
	if err := bil.AutoMigrate(&domain.PaymentCheckoutIntent{}, &domain.PaymentWebhookReceipt{}, &domain.AccountBilling{}, &domain.LocalChangeJournal{}); err != nil {
		t.Fatal(err)
	}
	if err := ent.AutoMigrate(&domain.AccountLimit{}, &domain.LocalChangeJournal{}); err != nil {
		t.Fatal(err)
	}
	fake := &paymentFake{ready: true}
	svc := payment.NewService(NewPaymentBILRepository(bil), NewPaymentENTRepository(ent), fake, payment.Config{SuccessURL: "https://app.example.test/billing/success", CancelURL: "https://app.example.test/billing/cancel", AllowedReturnURLs: []string{"https://app.example.test/billing/success", "https://app.example.test/billing/cancel"}})
	return svc, fake, bil, ent
}
func countPayment(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}
func creditsPayment(t *testing.T, db *gorm.DB, id uint) float64 {
	t.Helper()
	var limit domain.AccountLimit
	if err := db.Where("account_id = ?", id).First(&limit).Error; err != nil {
		t.Fatal(err)
	}
	return limit.Credits
}

func TestVerifiedPaymentTopup(t *testing.T) {
	ctx := context.Background()
	svc, fake, bil, ent := paymentFixture(t)
	intent, err := svc.CheckoutTopup(ctx, 11, 2500, "usd", "checkout-request-1")
	if err != nil {
		t.Fatal(err)
	}
	if intent.AmountMinor != 4500 || intent.CreditUnits != 2500 || intent.Status != "pending" {
		t.Fatalf("wrong intent: %+v", intent)
	}
	if countPayment(t, bil, &domain.AccountBilling{}) != 0 || countPayment(t, ent, &domain.AccountLimit{}) != 0 {
		t.Fatal("checkout created a charge or credits before callback")
	}
	again, err := svc.CheckoutTopup(ctx, 11, 2500, "usd", "checkout-request-1")
	if err != nil || again.IntentID != intent.IntentID || len(fake.requests) != 1 {
		t.Fatalf("checkout replay: %+v %v", again, err)
	}
	if _, err := svc.CheckoutTopup(ctx, 11, 500, "usd", "checkout-request-1"); !errors.Is(err, payment.ErrConflict) {
		t.Fatalf("key reuse=%v", err)
	}
	fake.paid("evt_one")
	if err := svc.HandleWebhook(ctx, []byte("signed"), "sig"); err != nil {
		t.Fatal(err)
	}
	if creditsPayment(t, ent, 11) != 3500 {
		t.Fatal("price money confused with credit units")
	}
	if countPayment(t, bil, &domain.AccountBilling{}) != 1 {
		t.Fatal("missing billing record")
	}
	var bill domain.AccountBilling
	bil.First(&bill)
	if bill.Amount != 45 || bill.ActionType != "topup" {
		t.Fatalf("billing=%+v", bill)
	}
	for _, id := range []string{"evt_one", "evt_second_same_session"} {
		fake.paid(id)
		if err := svc.HandleWebhook(ctx, nil, "sig"); err != nil {
			t.Fatal(err)
		}
	}
	fake.event = payment.Event{ID: "evt_failed_late", Type: "checkout.session.async_payment_failed", SessionID: fake.session.ID, Digest: "failed"}
	if err := svc.HandleWebhook(ctx, nil, "sig"); err != nil {
		t.Fatal(err)
	}
	if creditsPayment(t, ent, 11) != 3500 || countPayment(t, bil, &domain.AccountBilling{}) != 1 {
		t.Fatal("duplicate/late event changed balance")
	}
	if bil.Migrator().HasTable(&domain.AccountLimit{}) || ent.Migrator().HasTable(&domain.AccountBilling{}) || ent.Migrator().HasTable(&domain.PaymentCheckoutIntent{}) {
		t.Fatal("owner table leak")
	}
}

func TestVerifiedPaymentFailureRecovery(t *testing.T) {
	ctx := context.Background()
	svc, fake, bil, ent := paymentFixture(t)
	if _, err := svc.CheckoutTopup(ctx, 5, 1000, "usd", "recovery-request-1"); err != nil {
		t.Fatal(err)
	}
	fake.paid("evt_recovery")
	if err := ent.Migrator().DropTable(&domain.AccountLimit{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleWebhook(ctx, nil, "sig"); err == nil {
		t.Fatal("ENT failure acknowledged as success")
	}
	if countPayment(t, bil, &domain.AccountBilling{}) != 1 || countPayment(t, ent, &domain.LocalChangeJournal{}) != 0 {
		t.Fatal("non-atomic failed grant")
	}
	if err := ent.AutoMigrate(&domain.AccountLimit{}); err != nil {
		t.Fatal(err)
	}
	// New objects simulate a process restart; no volatile dedup state survives.
	restarted := payment.NewService(NewPaymentBILRepository(bil), NewPaymentENTRepository(ent), nil, payment.Config{})
	if err := restarted.RetryPending(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if creditsPayment(t, ent, 5) != 2000 {
		t.Fatal("pending grant did not recover")
	}
	if err := svc.HandleWebhook(ctx, nil, "sig"); err != nil {
		t.Fatal(err)
	}
	if creditsPayment(t, ent, 5) != 2000 || countPayment(t, bil, &domain.AccountBilling{}) != 1 {
		t.Fatal("retry duplicated fulfillment")
	}
}

func TestVerifiedPaymentRejectsUnpaidAndMismatched(t *testing.T) {
	for _, tc := range []string{"unpaid", "amount", "currency", "product", "quantity", "reference", "session", "mode", "signature", "livemode"} {
		t.Run(tc, func(t *testing.T) {
			ctx := context.Background()
			svc, fake, bil, ent := paymentFixture(t)
			if _, err := svc.CheckoutTopup(ctx, 9, 500, "usd", "mismatch-request"); err != nil {
				t.Fatal(err)
			}
			fake.paid("evt_mismatch")
			switch tc {
			case "unpaid":
				fake.session.PaymentStatus = "unpaid"
			case "amount":
				fake.session.AmountMinor++
			case "currency":
				fake.session.Currency = "eur"
			case "product":
				fake.session.Product = "credits_10000"
			case "quantity":
				fake.session.Quantity = 2
			case "reference":
				fake.session.ReferenceID = "foreign-account"
			case "session":
				fake.session.ID = "cs_test_foreign"
			case "mode":
				fake.session.Mode = "subscription"
			case "signature":
				fake.verifyErr = payment.ErrInvalidWebhook
			case "livemode":
				fake.session.Livemode = true
			}
			err := svc.HandleWebhook(ctx, nil, "sig")
			if tc != "unpaid" && err == nil {
				t.Fatal("mismatch accepted")
			}
			if countPayment(t, bil, &domain.AccountBilling{}) != 0 || countPayment(t, ent, &domain.AccountLimit{}) != 0 {
				t.Fatal("unverified grant")
			}
		})
	}
}

func TestVerifiedPaymentUnavailableAndCatalog(t *testing.T) {
	ctx := context.Background()
	svc, fake, bil, _ := paymentFixture(t)
	for _, credits := range []int64{-1, 0, 1, 499, 9999999999} {
		if _, err := svc.CheckoutTopup(ctx, 1, credits, "usd", "request-key-1"); !errors.Is(err, payment.ErrInvalidRequest) {
			t.Fatalf("credits %d err %v", credits, err)
		}
	}
	if _, err := svc.CheckoutTopup(ctx, 1, 500, "eur", "request-key-1"); !errors.Is(err, payment.ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := svc.CheckoutTopup(ctx, 1, 500, "usd", ""); !errors.Is(err, payment.ErrInvalidRequest) {
		t.Fatal(err)
	}
	fake.ready = false
	if _, err := svc.CheckoutTopup(ctx, 1, 500, "usd", "request-key-1"); !errors.Is(err, payment.ErrUnavailable) {
		t.Fatal(err)
	}
	if countPayment(t, bil, &domain.PaymentCheckoutIntent{}) != 0 {
		t.Fatal("invalid/unconfigured checkout persisted")
	}
}

func TestVerifiedPaymentProviderAmbiguityReusesKey(t *testing.T) {
	ctx := context.Background()
	svc, fake, bil, _ := paymentFixture(t)
	fake.createErr = payment.ErrUnavailable
	if _, err := svc.CheckoutTopup(ctx, 7, 500, "usd", "provider-request"); err == nil {
		t.Fatal("provider failure accepted")
	}
	if countPayment(t, bil, &domain.PaymentCheckoutIntent{}) != 1 {
		t.Fatal("lost pending intent")
	}
	fake.createErr = nil
	if _, err := svc.CheckoutTopup(ctx, 7, 500, "usd", "provider-request"); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 || fake.requests[0].IdempotencyKey != fake.requests[1].IdempotencyKey {
		t.Fatal("provider retry changed idempotency key")
	}
}

func TestVerifiedPaymentJournalFailureRollsBackBilling(t *testing.T) {
	ctx := context.Background()
	svc, fake, bil, ent := paymentFixture(t)
	_, err := svc.CheckoutTopup(ctx, 8, 1000, "usd", "rollback-request")
	if err != nil {
		t.Fatal(err)
	}
	fake.paid("evt_rollback")
	if err := bil.Migrator().DropTable(&domain.LocalChangeJournal{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleWebhook(ctx, nil, "sig"); err == nil {
		t.Fatal("journal failure hidden")
	}
	if countPayment(t, bil, &domain.AccountBilling{}) != 0 || countPayment(t, bil, &domain.PaymentWebhookReceipt{}) != 0 || countPayment(t, ent, &domain.AccountLimit{}) != 0 {
		t.Fatal("partial write escaped rollback")
	}
}

func TestVerifiedPaymentGrantReplayAfterAcknowledgementFailure(t *testing.T) {
	ctx := context.Background()
	svc, fake, bil, ent := paymentFixture(t)
	_, err := svc.CheckoutTopup(ctx, 2, 500, "usd", "ack-failure-request")
	if err != nil {
		t.Fatal(err)
	}
	fake.paid("evt_ack")
	// Fail only final BIL acknowledgement after ENT has committed its grant.
	if err := bil.Callback().Update().Before("gorm:update").Register("test:fail_ack", func(db *gorm.DB) {
		if db.Statement.Table == "payment_checkout_intents" {
			if values, ok := db.Statement.Dest.(map[string]any); ok && values["status"] == "fulfilled" {
				db.AddError(fmt.Errorf("injected acknowledgement failure"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleWebhook(ctx, nil, "sig"); err == nil {
		t.Fatal("ack failure hidden")
	}
	if creditsPayment(t, ent, 2) != 1500 {
		t.Fatal("grant did not commit")
	}
	if err := bil.Callback().Update().Remove("test:fail_ack"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RetryPending(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if creditsPayment(t, ent, 2) != 1500 || countPayment(t, bil, &domain.AccountBilling{}) != 1 {
		t.Fatal("ack retry duplicated effect")
	}
}
