package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
)

var ErrConflict = errors.New("payment request conflicts with a previous request")

// Store is the BIL-owned persistence port. It never exposes a raw owner DB.
type Store interface {
	Prepare(context.Context, *domain.PaymentCheckoutIntent) (*domain.PaymentCheckoutIntent, error)
	BindSession(context.Context, string, *Session) error
	FindSession(context.Context, string) (*domain.PaymentCheckoutIntent, error)
	RecordEvent(context.Context, *Event, *Session, time.Time) (*domain.PaymentCheckoutIntent, error)
	MarkFulfilled(context.Context, string) error
	Pending(context.Context, int) ([]domain.PaymentCheckoutIntent, error)
}

type Grant struct {
	IntentID    string
	Sequence    uint
	AccountID   uint
	Kind        string
	CreditUnits int64
}

// Entitlements is an ENT-owned idempotent grant port. Money never enters it.
type Entitlements interface {
	ApplyPaymentGrant(context.Context, Grant) error
}

type Config struct {
	SuccessURL string
	CancelURL  string
	// These are exact allowed URLs, not suffix/domain pattern matches.
	AllowedReturnURLs []string
}

type Service struct {
	store    Store
	ent      Entitlements
	provider Provider
	config   Config
	now      func() time.Time
}

func NewService(store Store, ent Entitlements, provider Provider, config Config) *Service {
	return &Service{store: store, ent: ent, provider: provider, config: config, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Ready() bool {
	return s != nil && s.store != nil && s.ent != nil && s.provider != nil && s.provider.Ready() && s.allowedURL(s.config.SuccessURL) && s.allowedURL(s.config.CancelURL)
}
func (s *Service) allowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\\") {
		return false
	}
	for _, allowed := range s.config.AllowedReturnURLs {
		if raw == allowed {
			return true
		}
	}
	return false
}

// TopupPrice is the complete supported USD catalog. Amounts are integer cents;
// arbitrary credit quantities and relabeling the same price as another currency
// are rejected. This also keeps the advertised 2500-credit price consistent.
func TopupPrice(credits int64) (int64, bool) {
	prices := map[int64]int64{500: 1000, 1000: 2000, 2500: 4500, 5000: 8000, 10000: 15000}
	price, ok := prices[credits]
	return price, ok
}

func (s *Service) CheckoutTopup(ctx context.Context, accountID uint, credits int64, currency, requestKey string) (*domain.PaymentCheckoutIntent, error) {
	amount, ok := TopupPrice(credits)
	if !ok || accountID == 0 || strings.ToLower(currency) != "usd" {
		return nil, ErrInvalidRequest
	}
	return s.checkout(ctx, &domain.PaymentCheckoutIntent{AccountID: accountID, Kind: "topup", Product: fmt.Sprintf("credits_%d", credits), CreditUnits: credits, AmountMinor: amount, Currency: "usd"}, requestKey)
}

func hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (s *Service) checkout(ctx context.Context, draft *domain.PaymentCheckoutIntent, key string) (*domain.PaymentCheckoutIntent, error) {
	if !s.Ready() {
		return nil, ErrUnavailable
	}
	if len(key) < 8 || len(key) > 128 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n") {
		return nil, ErrInvalidRequest
	}
	draft.IntentID = uuid.NewString()
	draft.RequestKeyHash = hash(key)
	draft.SuccessURL = s.config.SuccessURL
	draft.CancelURL = s.config.CancelURL
	draft.RequestHash = hash(fmt.Sprintf("%s:%s:%d:%s:%d:%s:%s", draft.Kind, draft.Product, draft.AmountMinor, draft.Currency, draft.CreditUnits, draft.SuccessURL, draft.CancelURL))
	draft.Status = "creating"
	draft.CreatedAt = s.now()
	intent, err := s.store.Prepare(ctx, draft)
	if err != nil {
		return nil, err
	}
	if intent.SessionID != nil {
		return intent, nil
	}
	// Stripe only guarantees idempotency retention for 24 hours. Never recreate an
	// ambiguous old request with the same key after the safe retry window.
	if s.now().Sub(intent.CreatedAt) > 23*time.Hour {
		return nil, ErrConflict
	}
	session, err := s.provider.CreateCheckout(ctx, CheckoutRequest{IntentID: intent.IntentID, AmountMinor: intent.AmountMinor, Currency: intent.Currency, Product: intent.Product, SuccessURL: intent.SuccessURL, CancelURL: intent.CancelURL, IdempotencyKey: "checkout-" + intent.IntentID})
	if err != nil {
		return nil, err
	}
	if session == nil || session.ID == "" || session.ReferenceID != intent.IntentID || session.AmountMinor != intent.AmountMinor || session.Currency != intent.Currency || session.Mode != "payment" {
		return nil, ErrMismatch
	}
	if err = s.store.BindSession(ctx, intent.IntentID, session); err != nil {
		return nil, err
	}
	intent.SessionID = &session.ID
	intent.CheckoutURL = session.URL
	intent.Status = "pending"
	return intent, nil
}

func (s *Service) HandleWebhook(ctx context.Context, body []byte, signature string) error {
	if !s.Ready() {
		return ErrUnavailable
	}
	event, err := s.provider.VerifyWebhook(body, signature, s.now())
	if err != nil {
		return err
	}
	switch event.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		intent, err := s.store.FindSession(ctx, event.SessionID)
		if err != nil {
			return err
		}
		// The durable binding is required. Neither account IDs nor entitlements are
		// ever selected from webhook metadata.
		if intent == nil {
			return ErrMismatch
		}
		session, err := s.provider.RetrieveCheckout(ctx, event.SessionID)
		if err != nil {
			return err
		}
		if session == nil || session.ID != event.SessionID || session.ReferenceID != intent.IntentID || session.Product != intent.Product || session.AmountMinor != intent.AmountMinor || session.Currency != intent.Currency || session.Quantity != 1 || session.Mode != "payment" || session.Livemode != event.Livemode {
			return ErrMismatch
		}
		paid, err := s.store.RecordEvent(ctx, event, session, s.now())
		if err != nil {
			return err
		}
		if paid != nil {
			return s.fulfill(ctx, paid)
		}
		return nil
	default:
		// Failed/expired/unknown events are durably acknowledged but cannot grant or
		// revoke anything, including when they arrive after a successful payment.
		_, err = s.store.RecordEvent(ctx, event, nil, s.now())
		return err
	}
}
func (s *Service) fulfill(ctx context.Context, intent *domain.PaymentCheckoutIntent) error {
	if err := s.ent.ApplyPaymentGrant(ctx, Grant{IntentID: intent.IntentID, Sequence: intent.ID, AccountID: intent.AccountID, Kind: intent.Kind, CreditUnits: intent.CreditUnits}); err != nil {
		return err
	}
	return s.store.MarkFulfilled(ctx, intent.IntentID)
}

// RetryPending replays only already-verified, BIL-committed fulfillment work.
// Call on worker startup and periodically with a process-lifetime context.
func (s *Service) RetryPending(ctx context.Context, limit int) error {
	if s == nil || s.store == nil || s.ent == nil {
		return ErrUnavailable
	}
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	intents, err := s.store.Pending(ctx, limit)
	if err != nil {
		return err
	}
	var errs []error
	for i := range intents {
		if err := s.fulfill(ctx, &intents[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RunPending retries persisted fulfillment until its owning process is stopped.
// Failure is reported to the caller but never deletes or marks work successful.
// The webhook also returns non-2xx on fulfillment failure, allowing provider retry.
func (s *Service) RunPending(ctx context.Context, onError func(error)) {
	retry := func() {
		if err := s.RetryPending(ctx, 100); err != nil && onError != nil {
			onError(err)
		}
	}
	retry()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			retry()
		}
	}
}
