package payment

import (
	"context"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

type configStore struct{}

func (configStore) Prepare(context.Context, *domain.PaymentCheckoutIntent) (*domain.PaymentCheckoutIntent, error) {
	return nil, ErrUnavailable
}
func (configStore) BindSession(context.Context, string, *Session) error { return nil }
func (configStore) FindSession(context.Context, string) (*domain.PaymentCheckoutIntent, error) {
	return nil, ErrUnavailable
}
func (configStore) RecordEvent(context.Context, *Event, *Session, time.Time) (*domain.PaymentCheckoutIntent, error) {
	return nil, nil
}
func (configStore) MarkFulfilled(context.Context, string) error { return nil }
func (configStore) Pending(context.Context, int) ([]domain.PaymentCheckoutIntent, error) {
	return nil, nil
}

type configGrant struct{}

func (configGrant) ApplyPaymentGrant(context.Context, Grant) error { return nil }
func TestPaymentReturnURLConfiguration(t *testing.T) {
	provider := NewStripeProvider("sk_test_localonly", "whsec_localonly")
	for _, tc := range []struct {
		url   string
		valid bool
	}{{"https://app.test/success", true}, {"http://app.test/success", false}, {"https://app.test@evil.test/success", false}, {"https://evil.test/success", false}, {"//app.test/success", false}, {"https://app.test/success#fragment", false}, {"https://app.test/success?redirect=https://evil.test", false}} {
		config := Config{SuccessURL: tc.url, CancelURL: "https://app.test/cancel", AllowedReturnURLs: []string{"https://app.test/success", "https://app.test/cancel"}}
		svc := NewService(configStore{}, configGrant{}, provider, config)
		if svc.Ready() != tc.valid {
			t.Errorf("ready %q=%v", tc.url, svc.Ready())
		}
	}
	t.Setenv("PAYMENT_SUCCESS_URL", "https://app.test/success")
	t.Setenv("PAYMENT_CANCEL_URL", "https://app.test/cancel")
	t.Setenv("PAYMENT_ALLOWED_RETURN_URLS", "https://app.test/success, https://app.test/cancel, ")
	config := ConfigFromEnvironment()
	if len(config.AllowedReturnURLs) != 2 || !NewService(configStore{}, configGrant{}, provider, config).Ready() {
		t.Fatalf("config=%+v", config)
	}
}
