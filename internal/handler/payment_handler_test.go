package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/payment"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type handlerPaymentProvider struct {
	session payment.Session
	event   payment.Event
	calls   int
}

func (p *handlerPaymentProvider) Ready() bool { return true }
func (p *handlerPaymentProvider) CreateCheckout(_ context.Context, r payment.CheckoutRequest) (*payment.Session, error) {
	p.calls++
	p.session = payment.Session{ID: "cs_test_local", URL: "https://checkout.stripe.com/c/pay/cs_test_local", ReferenceID: r.IntentID, AmountMinor: r.AmountMinor, Currency: r.Currency, Product: r.Product, Quantity: 1, Status: "open", PaymentStatus: "unpaid", Mode: "payment"}
	x := p.session
	return &x, nil
}
func (p *handlerPaymentProvider) RetrieveCheckout(_ context.Context, _ string) (*payment.Session, error) {
	x := p.session
	return &x, nil
}
func (p *handlerPaymentProvider) VerifyWebhook(_ []byte, s string, _ time.Time) (*payment.Event, error) {
	if s != "test-signed" {
		return nil, payment.ErrInvalidWebhook
	}
	x := p.event
	return &x, nil
}

func TestPaymentHandlerFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthEnterpriseHandler{}
	r := gin.New()
	r.POST("/:account_id/checkout", h.Checkout)
	r.POST("/:account_id/topup", h.TopupCheckout)
	r.POST("/webhook", h.HandleSubscriptionWebhook)
	for _, tc := range []struct {
		path, body string
		code       int
	}{{"/1/checkout", "", 503}, {"/1/topup", `{"credits":500}`, 503}, {"/1/topup", `{"credits":0}`, 422}, {"/1/topup", `{"credits":999}`, 422}, {"/1/topup", `{"credits":500,"currency":"eur"}`, 422}, {"/0/topup", `{"credits":500}`, 400}, {"/webhook", `{"type":"checkout.session.completed"}`, 503}} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%s code=%d body=%s", tc.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "redirect_url") {
			t.Fatal("invented checkout URL")
		}
	}
}
func TestPaymentHandlerCheckoutAndVerifiedCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "payments.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err := db.AutoMigrate(&domain.PaymentCheckoutIntent{}, &domain.PaymentWebhookReceipt{}, &domain.AccountBilling{}, &domain.AccountLimit{}, &domain.LocalChangeJournal{}, &domain.SubscriptionPlan{}, &domain.AccountSubscription{}); err != nil {
		t.Fatal(err)
	}
	provider := &handlerPaymentProvider{}
	h := &AuthEnterpriseHandler{bilRepo: repository.NewChannelAuthEnterpriseRepository(db)}
	h.SetPaymentService(payment.NewService(repository.NewPaymentBILRepository(db), repository.NewPaymentENTRepository(db), provider, payment.Config{SuccessURL: "https://app.test/success", CancelURL: "https://app.test/cancel", AllowedReturnURLs: []string{"https://app.test/success", "https://app.test/cancel"}}))
	r := gin.New()
	r.POST("/:account_id/topup", h.TopupCheckout)
	r.POST("/:account_id/subscription", h.CreateOrUpdateSubscription)
	r.POST("/webhook", h.HandleSubscriptionWebhook)
	send := func(path, body, key, sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Stripe-Signature", sig)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := send("/1/subscription", `{"plan_slug":"pro"}`, "", ""); w.Code != 503 {
		t.Fatalf("paid plan activated: %d %s", w.Code, w.Body.String())
	}
	var paid int64
	db.Model(&domain.AccountSubscription{}).Where("plan_id > ?", 1).Count(&paid)
	if paid != 0 {
		t.Fatal("paid sub persisted")
	}
	if w := send("/1/subscription", `{"plan_slug":"free"}`, "", ""); w.Code != 200 {
		t.Fatalf("free plan: %d %s", w.Code, w.Body.String())
	}
	if w := send("/1/topup", `{"credits":500}`, "", ""); w.Code != 422 {
		t.Fatal(w.Body.String())
	}
	w := send("/1/topup", `{"credits":500,"amount":0.01,"account_id":999,"success_url":"https://evil.test"}`, "one-time-key", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["amount_minor"] != float64(1000) || body["status"] != "pending" || provider.calls != 1 {
		t.Fatalf("checkout=%s", w.Body.String())
	}
	var n int64
	db.Model(&domain.AccountBilling{}).Count(&n)
	if n != 0 {
		t.Fatal("billed before verified callback")
	}
	provider.session.Status = "complete"
	provider.session.PaymentStatus = "paid"
	provider.event = payment.Event{ID: "evt_test", Type: "checkout.session.completed", SessionID: provider.session.ID, Digest: "test-digest"}
	if w := send("/webhook", `{}`, "", ""); w.Code != 400 {
		t.Fatalf("unsigned webhook=%s", w.Body.String())
	}
	if w := send("/webhook", `{}`, "", "test-signed"); w.Code != 200 {
		t.Fatalf("verified callback=%s", w.Body.String())
	}
	if w := send("/webhook", `{}`, "", "test-signed"); w.Code != 200 {
		t.Fatalf("duplicate callback=%s", w.Body.String())
	}
	var limits []domain.AccountLimit
	if err := db.Find(&limits).Error; err != nil {
		t.Fatal(err)
	}
	if len(limits) != 1 || limits[0].AccountID != 1 || limits[0].Credits != 1500 {
		t.Fatal(fmt.Sprintf("grants=%+v", limits))
	}
	db.Model(&domain.AccountBilling{}).Count(&n)
	if n != 1 {
		t.Fatalf("billing count=%d", n)
	}
	// Request body cap is enforced before provider parsing or database effects.
	if w := send("/webhook", strings.Repeat("x", (256<<10)+1), "", "test-signed"); w.Code != 400 {
		t.Fatal("oversized callback accepted")
	}
}
