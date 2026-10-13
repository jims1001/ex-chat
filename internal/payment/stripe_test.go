package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testSecret = "sk_test_only_a_local_fixture"
const testWebhookSecret = "whsec_only_a_local_fixture"
const testSessionID = "cs_test_fixture"
const testIntentID = "intent_fixture"
const testProduct = "wallet_topup_v1"

func fakeStripe(t *testing.T, handler http.HandlerFunc) *StripeProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := NewStripeProvider(testSecret, testWebhookSecret)
	p.baseURL = server.URL
	p.client = server.Client()
	return p
}

func fixtureSession() map[string]any {
	return map[string]any{
		"id": testSessionID, "object": "checkout.session", "client_reference_id": testIntentID,
		"url":          "https://checkout.stripe.com/c/pay/" + testSessionID + "#fixture",
		"amount_total": int64(2500), "currency": "usd", "mode": "payment", "livemode": false,
		"payment_status": "unpaid", "status": "open", "created": int64(1700000000),
		"metadata": map[string]any{"product_code": "untrusted_session_metadata"},
		"line_items": map[string]any{"object": "list", "has_more": false, "data": []any{
			map[string]any{"quantity": int64(1), "amount_total": int64(2500), "currency": "usd", "price": map[string]any{
				"id": "price_fixture", "object": "price", "type": "one_time", "recurring": nil,
				"unit_amount": int64(2500), "currency": "usd", "livemode": false,
				"product": map[string]any{"id": "prod_fixture", "object": "product", "livemode": false,
					"metadata": map[string]any{"product_code": testProduct}},
			}},
		}},
	}
}

func fixtureLine(raw map[string]any) map[string]any {
	return raw["line_items"].(map[string]any)["data"].([]any)[0].(map[string]any)
}

func fixturePrice(raw map[string]any) map[string]any {
	return fixtureLine(raw)["price"].(map[string]any)
}

func fixtureProduct(raw map[string]any) map[string]any {
	return fixturePrice(raw)["product"].(map[string]any)
}

func fixtureRequest() CheckoutRequest {
	return CheckoutRequest{IntentID: testIntentID, AmountMinor: 2500, Currency: "usd", Product: testProduct,
		SuccessURL: "https://app.example/billing/success?session_id={CHECKOUT_SESSION_ID}",
		CancelURL:  "https://app.example/billing/cancel", IdempotencyKey: "checkout:intent_fixture"}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestStripeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		key, webhook string
		ready, live  bool
	}{
		{testSecret, testWebhookSecret, true, false},
		{"sk_live_only_fixture", testWebhookSecret, true, true},
		{"rk_test_only_fixture", testWebhookSecret, true, false},
		{"rk_live_only_fixture", testWebhookSecret, true, true},
		{"", testWebhookSecret, false, false}, {testSecret, "", false, false},
		{"pk_test_public", testWebhookSecret, false, false},
		{"sk_test_", testWebhookSecret, false, false}, {testSecret, "whsec_", false, false},
		{"sk_test_fixture\nInjected", testWebhookSecret, false, false},
		{testSecret, "whsec_ contains spaces", false, false},
	} {
		t.Run(tc.key+tc.webhook, func(t *testing.T) {
			p := NewStripeProvider(tc.key, tc.webhook)
			if p.Ready() != tc.ready || (tc.ready && p.livemode != tc.live) {
				t.Fatalf("Ready=%v, live=%v", p.Ready(), p.livemode)
			}
			if p.baseURL != "https://api.stripe.com" || p.client.Timeout != 15*time.Second || p.client.CheckRedirect == nil {
				t.Fatal("unsafe transport defaults")
			}
		})
	}
	var nilProvider *StripeProvider
	if nilProvider.Ready() {
		t.Fatal("nil provider ready")
	}
	for _, p := range []*StripeProvider{nilProvider, NewStripeProvider("", "")} {
		if _, err := p.CreateCheckout(context.Background(), fixtureRequest()); err != ErrUnavailable {
			t.Fatalf("create: %v", err)
		}
		if _, err := p.RetrieveCheckout(context.Background(), testSessionID); err != ErrUnavailable {
			t.Fatalf("retrieve: %v", err)
		}
		if _, err := p.VerifyWebhook(nil, "", time.Now()); err != ErrUnavailable {
			t.Fatalf("webhook: %v", err)
		}
	}
}

func TestStripeCreateAndRetrieveCheckout(t *testing.T) {
	var calls atomic.Int32
	in := fixtureRequest()
	p := fakeStripe(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+testSecret || r.Header.Get("Accept") != "application/json" {
			t.Error("missing authentication/content type")
		}
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/v1/checkout/sessions" || r.Header.Get("Idempotency-Key") != in.IdempotencyKey {
				t.Error("incorrect create target or idempotency key")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			want := url.Values{
				"mode": {"payment"}, "client_reference_id": {in.IntentID}, "success_url": {in.SuccessURL}, "cancel_url": {in.CancelURL},
				"line_items[0][quantity]": {"1"}, "line_items[0][price_data][currency]": {"usd"}, "line_items[0][price_data][unit_amount]": {"2500"},
				"line_items[0][price_data][product_data][name]": {testProduct}, "line_items[0][price_data][product_data][metadata][product_code]": {testProduct},
				"metadata[intent_id]": {in.IntentID}, "metadata[product_code]": {testProduct}, "expand[]": {checkoutExpansion},
				"allow_promotion_codes": {"false"}, "automatic_tax[enabled]": {"false"}, "adaptive_pricing[enabled]": {"false"},
			}
			if r.PostForm.Encode() != want.Encode() {
				t.Errorf("unexpected form: %s", r.PostForm.Encode())
			}
			writeJSON(t, w, fixtureSession())
		case http.MethodGet:
			if r.URL.Path != "/v1/checkout/sessions/"+testSessionID || r.URL.Query().Get("expand[]") != checkoutExpansion || r.Header.Get("Idempotency-Key") != "" {
				t.Error("incorrect retrieve request")
			}
			raw := fixtureSession()
			raw["status"], raw["payment_status"], raw["url"] = "complete", "paid", nil
			writeJSON(t, w, raw)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	created, err := p.CreateCheckout(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if created.Product != testProduct || created.ReferenceID != testIntentID || created.AmountMinor != 2500 || created.Quantity != 1 || created.Status != "open" || created.URL == "" {
		t.Fatalf("wrong create result: %+v", created)
	}
	paid, err := p.RetrieveCheckout(context.Background(), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if paid.Product != testProduct || paid.PaymentStatus != "paid" || paid.Status != "complete" || paid.URL != "" || paid.Mode != "payment" || paid.Created != 1700000000 || paid.Livemode {
		t.Fatalf("wrong retrieved result: %+v", paid)
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected network calls")
	}
}

func TestStripeRejectsInvalidRequestsWithoutNetwork(t *testing.T) {
	p := fakeStripe(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached provider") })
	for _, mutate := range []func(*CheckoutRequest){
		func(r *CheckoutRequest) { r.IntentID = "" }, func(r *CheckoutRequest) { r.Product = "" },
		func(r *CheckoutRequest) { r.Product = "bad\nproduct" }, func(r *CheckoutRequest) { r.Currency = "USD" },
		func(r *CheckoutRequest) { r.Currency = "us" }, func(r *CheckoutRequest) { r.AmountMinor = 0 },
		func(r *CheckoutRequest) { r.AmountMinor = -1 }, func(r *CheckoutRequest) { r.IdempotencyKey = "" },
		func(r *CheckoutRequest) { r.IdempotencyKey = "bad\r\nX:" },
		func(r *CheckoutRequest) { r.SuccessURL = "javascript:alert(1)" },
		func(r *CheckoutRequest) { r.CancelURL = "//app.example" },
		func(r *CheckoutRequest) { r.CancelURL = "https://user:password@app.example" },
		func(r *CheckoutRequest) { r.CancelURL = "http://app.example" },
		func(r *CheckoutRequest) { r.SuccessURL = "https://app.example/#fragment" },
	} {
		r := fixtureRequest()
		mutate(&r)
		if _, err := p.CreateCheckout(context.Background(), r); err != ErrInvalidRequest {
			t.Fatalf("invalid request accepted: %v", err)
		}
	}
	for _, id := range []string{"", "cs_", "cs_test_../secret", "cs_test_foo?expand=secret", "cs_test_foo/bar", "cs_test_foo#x", "cs_test_%2fsecret", "wrong_foo", strings.Repeat("a", 256)} {
		if _, err := p.RetrieveCheckout(context.Background(), id); err != ErrInvalidRequest {
			t.Fatalf("ID %q: %v", id, err)
		}
	}
	if _, err := p.CreateCheckout(nil, fixtureRequest()); err != ErrInvalidRequest {
		t.Fatalf("nil context: %v", err)
	}
}

func TestStripeRejectsSessionMismatches(t *testing.T) {
	tests := map[string]func(map[string]any){
		"missing session mode":   func(s map[string]any) { delete(s, "livemode") },
		"wrong session mode":     func(s map[string]any) { s["livemode"] = true },
		"subscription":           func(s map[string]any) { s["mode"] = "subscription" },
		"missing reference":      func(s map[string]any) { s["client_reference_id"] = nil },
		"missing amount":         func(s map[string]any) { delete(s, "amount_total") },
		"negative amount":        func(s map[string]any) { s["amount_total"] = -1 },
		"missing created":        func(s map[string]any) { delete(s, "created") },
		"invalid currency":       func(s map[string]any) { s["currency"] = "USD" },
		"unknown status":         func(s map[string]any) { s["status"] = "succeeded" },
		"unknown payment status": func(s map[string]any) { s["payment_status"] = "succeeded" },
		"wrong object":           func(s map[string]any) { s["object"] = "payment_intent" },
		"wrong session ID":       func(s map[string]any) { s["id"] = "cs_test_other" },
		"unsafe URL":             func(s map[string]any) { s["url"] = "https://checkout.stripe.com.evil.example/pay" },
		"missing lines":          func(s map[string]any) { delete(s, "line_items") },
		"multiple lines": func(s map[string]any) {
			s["line_items"].(map[string]any)["data"] = []any{fixtureLine(s), fixtureLine(s)}
		},
		"empty lines":            func(s map[string]any) { s["line_items"].(map[string]any)["data"] = []any{} },
		"paginated lines":        func(s map[string]any) { s["line_items"].(map[string]any)["has_more"] = true },
		"unknown pagination":     func(s map[string]any) { delete(s["line_items"].(map[string]any), "has_more") },
		"wrong quantity":         func(s map[string]any) { fixtureLine(s)["quantity"] = 2 },
		"wrong line currency":    func(s map[string]any) { fixtureLine(s)["currency"] = "eur" },
		"wrong line amount":      func(s map[string]any) { fixtureLine(s)["amount_total"] = 2000 },
		"missing price":          func(s map[string]any) { fixtureLine(s)["price"] = nil },
		"missing price identity": func(s map[string]any) { delete(fixturePrice(s), "id") },
		"wrong price object":     func(s map[string]any) { fixturePrice(s)["object"] = "product" },
		"recurring type":         func(s map[string]any) { fixturePrice(s)["type"] = "recurring" },
		"recurring details":      func(s map[string]any) { fixturePrice(s)["recurring"] = map[string]any{"interval": "month"} },
		"wrong price amount":     func(s map[string]any) { fixturePrice(s)["unit_amount"] = 2400 },
		"wrong price currency":   func(s map[string]any) { fixturePrice(s)["currency"] = "eur" },
		"wrong price mode":       func(s map[string]any) { fixturePrice(s)["livemode"] = true },
		"missing product":        func(s map[string]any) { fixturePrice(s)["product"] = nil },
		"wrong product object":   func(s map[string]any) { fixtureProduct(s)["object"] = "price" },
		"wrong product mode":     func(s map[string]any) { fixtureProduct(s)["livemode"] = true },
		"missing product code":   func(s map[string]any) { fixtureProduct(s)["metadata"] = map[string]any{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			raw := fixtureSession()
			mutate(raw)
			p := fakeStripe(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, raw) })
			if got, err := p.RetrieveCheckout(context.Background(), testSessionID); err != ErrMismatch || got != nil {
				t.Fatalf("mismatch returned %+v, %v", got, err)
			}
		})
	}
}

func TestStripeCreateMatchesIntent(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"different reference": func(s map[string]any) { s["client_reference_id"] = "another_intent" },
		"different product": func(s map[string]any) {
			fixtureProduct(s)["metadata"].(map[string]any)["product_code"] = "another_product"
		},
		"different amount": func(s map[string]any) {
			s["amount_total"] = 1200
			fixtureLine(s)["amount_total"] = 1200
			fixturePrice(s)["unit_amount"] = 1200
		},
		"different currency": func(s map[string]any) {
			s["currency"] = "eur"
			fixtureLine(s)["currency"] = "eur"
			fixturePrice(s)["currency"] = "eur"
		},
		"complete": func(s map[string]any) { s["status"] = "complete" },
		"no URL":   func(s map[string]any) { s["url"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			raw := fixtureSession()
			mutate(raw)
			p := fakeStripe(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, raw) })
			if got, err := p.CreateCheckout(context.Background(), fixtureRequest()); err != ErrMismatch || got != nil {
				t.Fatalf("create mismatch returned %+v, %v", got, err)
			}
		})
	}
}

func TestStripeTransportFailsClosed(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"provider error": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"message":"secret credential data"}}`)
		},
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) },
		"oversize": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", maxProviderBody+1))
		},
		"truncated": func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"id":`) },
		"trailing JSON": func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, fixtureSession())
			_, _ = io.WriteString(w, `{}`)
		},
		"duplicate fields": func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"id":"first","id":"second"}`) },
		"unexpanded product": func(w http.ResponseWriter, r *http.Request) {
			raw := fixtureSession()
			fixturePrice(raw)["product"] = "prod_fixture"
			writeJSON(t, w, raw)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := fakeStripe(t, handler)
			if got, err := p.RetrieveCheckout(context.Background(), testSessionID); got != nil || err != ErrUnavailable {
				t.Fatalf("unsafe error: %+v, %v", got, err)
			}
		})
	}
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		t.Error("redirect followed with possible credentials")
	}))
	defer target.Close()
	p := fakeStripe(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	if _, err := p.CreateCheckout(context.Background(), fixtureRequest()); err != ErrUnavailable {
		t.Fatalf("redirect error: %v", err)
	}
	if redirected.Load() != 0 {
		t.Fatal("redirect followed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.RetrieveCheckout(ctx, testSessionID); err != ErrUnavailable {
		t.Fatalf("cancelled request: %v", err)
	}
}

func TestStripeURLValidation(t *testing.T) {
	for _, s := range []string{"https://checkout.stripe.com/c/pay/cs_test_fixture", "https://checkout.stripe.com/c/pay/cs_test_fixture#valid"} {
		if !validCheckoutURL(s) {
			t.Fatalf("valid URL rejected: %s", s)
		}
	}
	for _, s := range []string{"", "https://checkout.stripe.com", "http://checkout.stripe.com/pay", "https://checkout.stripe.com:443/pay", "https://checkout.stripe.com.evil.example/pay", "https://checkout.stripe.com@evil.example/pay", "https://evil.example@checkout.stripe.com/pay", "javascript:alert(1)", "//checkout.stripe.com/pay", "https://checkout.stripe.com./pay", "https://checkout.stripe.com%2fevil.example/pay"} {
		if validCheckoutURL(s) {
			t.Fatalf("unsafe URL accepted: %s", s)
		}
	}
	for _, s := range []string{"http://localhost:3000/success", "http://127.0.0.1:3000/success", "http://[::1]:3000/success", "https://app.example/success?session_id={CHECKOUT_SESSION_ID}"} {
		if !validReturnURL(s) {
			t.Fatalf("valid return URL rejected: %s", s)
		}
	}
}

func webhookFixture() map[string]any {
	return map[string]any{"id": "evt_fixture", "object": "event", "type": "checkout.session.completed", "created": int64(1700000000), "livemode": false,
		"data": map[string]any{"object": map[string]any{"id": testSessionID, "object": "checkout.session", "livemode": false}}}
}

func encoded(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func signed(body []byte, seconds int64) string {
	ts := fmt.Sprint(seconds)
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	_, _ = mac.Write([]byte(ts + "."))
	_, _ = mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestStripeVerifyWebhook(t *testing.T) {
	p := NewStripeProvider(testSecret, testWebhookSecret)
	now := time.Unix(1700000010, 0)
	body := encoded(t, webhookFixture())
	for _, when := range []time.Time{now, now.Add(-5 * time.Minute), now.Add(5 * time.Minute)} {
		event, err := p.VerifyWebhook(body, signed(body, when.Unix()), now)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		if event.ID != "evt_fixture" || event.Type != "checkout.session.completed" || event.SessionID != testSessionID || event.Livemode || event.Digest != hex.EncodeToString(digest[:]) {
			t.Fatalf("wrong event: %+v", event)
		}
	}
	for _, header := range []string{signed(body, now.Unix()) + ",v1=" + strings.Repeat("0", 64), "v0=ignored," + signed(body, now.Unix()), "v1=malformed," + signed(body, now.Unix()), "v1=" + strings.Repeat("0", 64) + ", " + signed(body, now.Unix())} {
		if _, err := p.VerifyWebhook(body, header, now); err != nil {
			t.Fatalf("rotation/unknown version rejected: %v", err)
		}
	}
	unknown := webhookFixture()
	unknown["type"] = "invoice.created"
	unknown["data"] = map[string]any{"object": map[string]any{"id": "in_fixture", "object": "invoice"}}
	unknownBody := encoded(t, unknown)
	event, err := p.VerifyWebhook(unknownBody, signed(unknownBody, now.Unix()), now)
	if err != nil || event.SessionID != "" || event.Type != "invoice.created" {
		t.Fatalf("unknown event: %+v, %v", event, err)
	}
	live := webhookFixture()
	live["livemode"] = true
	live["data"].(map[string]any)["object"].(map[string]any)["livemode"] = true
	liveBody := encoded(t, live)
	liveProvider := NewStripeProvider("sk_live_only_fixture", testWebhookSecret)
	if _, err := liveProvider.VerifyWebhook(liveBody, signed(liveBody, now.Unix()), now); err != nil {
		t.Fatal(err)
	}
}

func TestStripeRejectsInvalidSignatures(t *testing.T) {
	p := NewStripeProvider(testSecret, testWebhookSecret)
	now := time.Unix(1700000010, 0)
	body := encoded(t, webhookFixture())
	for name, header := range map[string]string{
		"missing": "", "wrong": "t=1700000010,v1=" + strings.Repeat("0", 64),
		"old":                 signed(body, now.Add(-5*time.Minute-time.Second).Unix()),
		"future":              signed(body, now.Add(5*time.Minute+time.Second).Unix()),
		"v0 only":             strings.ReplaceAll(signed(body, now.Unix()), "v1=", "v0="),
		"duplicate timestamp": signed(body, now.Unix()) + ",t=1700000010",
		"no timestamp":        "v1=" + strings.Repeat("0", 64), "malformed timestamp": "t=x,v1=" + strings.Repeat("0", 64),
		"huge timestamp":   "t=9223372036854775807,v1=" + strings.Repeat("0", 64),
		"signed timestamp": "t=+1700000010,v1=" + strings.Repeat("0", 64),
		"short signature":  "t=1700000010,v1=00", "malformed pair": signed(body, now.Unix()) + ",broken",
		"oversize header": strings.Repeat(" ", maxSignatureHeader+1),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := p.VerifyWebhook(body, header, now); got != nil || err != ErrInvalidWebhook {
				t.Fatalf("accepted: %+v, %v", got, err)
			}
		})
	}
	for name, altered := range map[string][]byte{"altered body": append(append([]byte{}, body...), ' '), "empty body": nil, "oversize body": []byte(strings.Repeat("x", maxProviderBody+1))} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.VerifyWebhook(altered, signed(body, now.Unix()), now); err != ErrInvalidWebhook {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestStripeRejectsSignedMalformedEvents(t *testing.T) {
	p := NewStripeProvider(testSecret, testWebhookSecret)
	now := time.Unix(1700000010, 0)
	for name, mutate := range map[string]func(map[string]any){
		"missing ID": func(e map[string]any) { delete(e, "id") }, "wrong ID": func(e map[string]any) { e["id"] = "pi_fixture" },
		"missing type": func(e map[string]any) { delete(e, "type") }, "missing created": func(e map[string]any) { delete(e, "created") },
		"wrong object": func(e map[string]any) { e["object"] = "checkout.session" }, "missing mode": func(e map[string]any) { delete(e, "livemode") },
		"wrong mode": func(e map[string]any) { e["livemode"] = true }, "null mode": func(e map[string]any) { e["livemode"] = nil },
		"missing data": func(e map[string]any) { delete(e, "data") }, "null data": func(e map[string]any) { e["data"] = nil },
		"empty data": func(e map[string]any) { e["data"] = map[string]any{} }, "null object": func(e map[string]any) { e["data"].(map[string]any)["object"] = nil },
		"string object": func(e map[string]any) { e["data"].(map[string]any)["object"] = "cs_fixture" }, "array object": func(e map[string]any) { e["data"].(map[string]any)["object"] = []any{} },
		"wrong session ID":     func(e map[string]any) { e["data"].(map[string]any)["object"].(map[string]any)["id"] = "pi_fixture" },
		"missing session mode": func(e map[string]any) { delete(e["data"].(map[string]any)["object"].(map[string]any), "livemode") },
		"wrong session mode":   func(e map[string]any) { e["data"].(map[string]any)["object"].(map[string]any)["livemode"] = true },
		"wrong session object": func(e map[string]any) {
			e["data"].(map[string]any)["object"].(map[string]any)["object"] = "payment_intent"
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := webhookFixture()
			mutate(raw)
			body := encoded(t, raw)
			if got, err := p.VerifyWebhook(body, signed(body, now.Unix()), now); got != nil || err != ErrInvalidWebhook {
				t.Fatalf("accepted: %+v, %v", got, err)
			}
		})
	}
	for _, body := range [][]byte{[]byte("null"), []byte("{}{}"), []byte(`{"id":"evt_one","id":"evt_two"}`), []byte(`{"id":"evt_one","\u0069d":"evt_two"}`), []byte(`{"id":`), []byte(strings.Repeat("[", 66) + strings.Repeat("]", 66))} {
		if _, err := p.VerifyWebhook(body, signed(body, now.Unix()), now); err != ErrInvalidWebhook {
			t.Fatalf("invalid JSON accepted: %v", err)
		}
	}
}

func TestUniqueJSON(t *testing.T) {
	for _, s := range []string{`{}`, ` {"a":[1,null,{"b":true}]} `, `[]`, `"string"`, `123`, `null`} {
		if !uniqueJSON([]byte(s)) {
			t.Fatalf("valid JSON rejected: %s", s)
		}
	}
	for _, s := range []string{"", `{`, `{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{"a":1,"\u0061":2}`, `[] []`, `[1,]`, `{"a":}`, `true false`, `}`} {
		if uniqueJSON([]byte(s)) {
			t.Fatalf("invalid JSON accepted: %s", s)
		}
	}
}

func FuzzStripeWebhook(f *testing.F) {
	f.Add([]byte(`{"id":"evt_fixture"}`), "t=1,v1=00")
	f.Add([]byte(`null`), "")
	p := NewStripeProvider(testSecret, testWebhookSecret)
	now := time.Unix(1700000010, 0)
	f.Fuzz(func(t *testing.T, body []byte, header string) {
		_, err := p.VerifyWebhook(body, header, now)
		if err != nil && !errors.Is(err, ErrInvalidWebhook) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
