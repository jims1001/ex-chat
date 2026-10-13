package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/payment"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Explicit opt-in only: this creates isolated disposable schemas in the supplied
// test database. It must never be pointed at a production database.
func TestVerifiedPaymentPostgresConcurrency(t *testing.T) {
	dsn := os.Getenv("PAYMENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PAYMENT_TEST_POSTGRES_DSN is not configured")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	baseSQL, _ := base.DB()
	baseSQL.SetMaxOpenConns(2)
	newOwner := func(owner string) *gorm.DB {
		schema := "payment_" + owner + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
			t.Fatal(err)
		}
		ownerDB, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := ownerDB.DB()
		sqlDB.SetMaxOpenConns(8)
		t.Cleanup(func() {
			_ = sqlDB.Close()
			if err := base.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
				t.Error(err)
			}
		})
		return ownerDB
	}
	// Keep base open until cleanup removes only the test's own generated schemas.
	t.Cleanup(func() { _ = baseSQL.Close() })
	bil := newOwner("bil")
	ent := newOwner("ent")
	if err := bil.AutoMigrate(&domain.PaymentCheckoutIntent{}, &domain.PaymentWebhookReceipt{}, &domain.AccountBilling{}, &domain.LocalChangeJournal{}); err != nil {
		t.Fatal(err)
	}
	if err := ent.AutoMigrate(&domain.AccountLimit{}, &domain.LocalChangeJournal{}); err != nil {
		t.Fatal(err)
	}
	bilPort := NewPaymentBILRepository(bil)
	entPort := NewPaymentENTRepository(ent)
	ctx := context.Background()
	var sessions []payment.Session
	for i, units := range []int64{500, 1000} {
		amount, _ := payment.TopupPrice(units)
		intentID := uuid.NewString()
		draft := &domain.PaymentCheckoutIntent{IntentID: intentID, AccountID: 91, RequestKeyHash: fmt.Sprintf("request_%d", i), RequestHash: "test-only", Kind: "topup", Product: fmt.Sprintf("credits_%d", units), AmountMinor: amount, Currency: "usd", CreditUnits: units, Status: "creating"}
		if _, err := bilPort.Prepare(ctx, draft); err != nil {
			t.Fatal(err)
		}
		session := payment.Session{ID: fmt.Sprintf("cs_test_purchase_%d", i), ReferenceID: intentID, AmountMinor: amount, Currency: "usd", Product: draft.Product, Quantity: 1, Mode: "payment", PaymentStatus: "paid", Status: "complete"}
		if err := bilPort.BindSession(ctx, intentID, &session); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			session := sessions[n%2]
			event := payment.Event{ID: fmt.Sprintf("evt_purchase_%d_delivery_%d", n%2, n%4), SessionID: session.ID, Type: "checkout.session.completed", Digest: fmt.Sprintf("digest_%d", n%4)}
			intent, err := bilPort.RecordEvent(ctx, &event, &session, time.Now().UTC())
			if err != nil {
				errs <- err
				return
			}
			if intent == nil {
				return
			}
			grant := payment.Grant{IntentID: intent.IntentID, Sequence: intent.ID, AccountID: intent.AccountID, Kind: intent.Kind, CreditUnits: intent.CreditUnits}
			if err := entPort.ApplyPaymentGrant(ctx, grant); err != nil {
				errs <- err
				return
			}
			if err := bilPort.MarkFulfilled(ctx, intent.IntentID); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	if creditsPayment(t, ent, 91) != 2500 {
		t.Fatal("concurrent events lost or duplicated credit units")
	}
	if countPayment(t, bil, &domain.AccountBilling{}) != 2 || countPayment(t, ent, &domain.LocalChangeJournal{}) != 2 {
		t.Fatal("concurrent events duplicated durable effects")
	}
	pending, err := bilPort.Pending(ctx, 100)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if bil.Migrator().HasTable(&domain.AccountLimit{}) || ent.Migrator().HasTable(&domain.AccountBilling{}) {
		t.Fatal("owner boundary violation")
	}
}
