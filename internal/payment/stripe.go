package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	stripeAPIURL       = "https://api.stripe.com"
	maxProviderBody    = 1 << 20
	maxSignatureHeader = 8192
	webhookTolerance   = 5 * time.Minute
	checkoutExpansion  = "line_items.data.price.product"
)

// StripeProvider intentionally has no public endpoint/client override. Tests in
// this package use a local fake server; production always uses api.stripe.com.
type StripeProvider struct {
	secretKey     string
	webhookSecret string
	baseURL       string
	client        *http.Client
	livemode      bool
	configured    bool
}

var _ Provider = (*StripeProvider)(nil)

func NewStripeProvider(secretKey, webhookSecret string) *StripeProvider {
	live, valid := stripeKeyMode(secretKey)
	return &StripeProvider{
		secretKey: secretKey, webhookSecret: webhookSecret,
		baseURL: stripeAPIURL, livemode: live,
		configured: valid && strings.HasPrefix(webhookSecret, "whsec_") && len(webhookSecret) > len("whsec_") && safeToken(webhookSecret, 512),
		client:     &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect},
	}
}

func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func (p *StripeProvider) Ready() bool { return p != nil && p.configured && p.client != nil }

func stripeKeyMode(key string) (bool, bool) {
	for _, prefix := range []string{"sk_test_", "sk_live_", "rk_test_", "rk_live_"} {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) && safeToken(key, 512) {
			return strings.Contains(prefix, "_live_"), true
		}
	}
	return false, false
}

// CreateCheckout creates one explicit one-time purchase and never starts
// recurring billing. API parameters:
// https://docs.stripe.com/api/checkout/sessions/create
func (p *StripeProvider) CreateCheckout(ctx context.Context, in CheckoutRequest) (*Session, error) {
	if !p.Ready() {
		return nil, ErrUnavailable
	}
	if !validText(in.IntentID, 200) || !validText(in.Product, 200) || !safeToken(in.IdempotencyKey, 255) ||
		in.AmountMinor <= 0 || !validCurrency(in.Currency) || !validReturnURL(in.SuccessURL) || !validReturnURL(in.CancelURL) {
		return nil, ErrInvalidRequest
	}
	form := url.Values{
		"mode": {"payment"}, "client_reference_id": {in.IntentID},
		"success_url": {in.SuccessURL}, "cancel_url": {in.CancelURL},
		"line_items[0][quantity]":                                         {"1"},
		"line_items[0][price_data][currency]":                             {in.Currency},
		"line_items[0][price_data][unit_amount]":                          {strconv.FormatInt(in.AmountMinor, 10)},
		"line_items[0][price_data][product_data][name]":                   {in.Product},
		"line_items[0][price_data][product_data][metadata][product_code]": {in.Product},
		"metadata[intent_id]":                                             {in.IntentID}, "metadata[product_code]": {in.Product},
		"allow_promotion_codes": {"false"}, "automatic_tax[enabled]": {"false"},
		"adaptive_pricing[enabled]": {"false"}, "expand[]": {checkoutExpansion},
	}
	var raw stripeSession
	if err := p.request(ctx, http.MethodPost, "/v1/checkout/sessions", form, in.IdempotencyKey, &raw); err != nil {
		return nil, err
	}
	session, err := p.normalizeSession(raw)
	if err != nil {
		return nil, err
	}
	if session.ReferenceID != in.IntentID || session.AmountMinor != in.AmountMinor || session.Currency != in.Currency ||
		session.Product != in.Product || session.Status != "open" || !validCheckoutURL(session.URL) {
		return nil, ErrMismatch
	}
	return session, nil
}

// RetrieveCheckout expands the purchased price and product, rather than trusting
// session metadata to describe what was bought. Only one complete line is valid.
// https://docs.stripe.com/api/checkout/sessions/retrieve
// https://docs.stripe.com/expand#lists
func (p *StripeProvider) RetrieveCheckout(ctx context.Context, id string) (*Session, error) {
	if !p.Ready() {
		return nil, ErrUnavailable
	}
	if !stripeID(id, "cs_") {
		return nil, ErrInvalidRequest
	}
	var raw stripeSession
	if err := p.request(ctx, http.MethodGet, "/v1/checkout/sessions/"+id, url.Values{"expand[]": {checkoutExpansion}}, "", &raw); err != nil {
		return nil, err
	}
	session, err := p.normalizeSession(raw)
	if err != nil {
		return nil, err
	}
	if session.ID != id {
		return nil, ErrMismatch
	}
	return session, nil
}

func (p *StripeProvider) request(ctx context.Context, method, path string, values url.Values, idempotencyKey string, out any) error {
	if ctx == nil {
		return ErrInvalidRequest
	}
	endpoint := p.baseURL + path
	var body io.Reader
	if method == http.MethodGet {
		endpoint += "?" + values.Encode()
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	// Enforce these protections even if a package-local test replaces the client.
	client := *p.client
	client.CheckRedirect = noRedirect
	if client.Timeout <= 0 || client.Timeout > 15*time.Second {
		client.Timeout = 15 * time.Second
	}
	res, err := client.Do(req)
	if err != nil {
		return ErrUnavailable // Never expose headers, credentials, or raw errors.
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxProviderBody+1))
	if err != nil || len(data) > maxProviderBody || !uniqueJSON(data) || json.Unmarshal(data, out) != nil {
		return ErrUnavailable
	}
	return nil
}

type stripeSession struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	URL           string `json:"url"`
	ReferenceID   string `json:"client_reference_id"`
	Currency      string `json:"currency"`
	AmountTotal   *int64 `json:"amount_total"`
	PaymentStatus string `json:"payment_status"`
	Status        string `json:"status"`
	Mode          string `json:"mode"`
	Livemode      *bool  `json:"livemode"`
	Created       int64  `json:"created"`
	LineItems     *struct {
		Object  string `json:"object"`
		HasMore *bool  `json:"has_more"`
		Data    []struct {
			Currency    string `json:"currency"`
			AmountTotal *int64 `json:"amount_total"`
			Quantity    int64  `json:"quantity"`
			Price       *struct {
				ID         string          `json:"id"`
				Object     string          `json:"object"`
				Currency   string          `json:"currency"`
				UnitAmount *int64          `json:"unit_amount"`
				Type       string          `json:"type"`
				Recurring  json.RawMessage `json:"recurring"`
				Livemode   *bool           `json:"livemode"`
				Product    *struct {
					ID       string            `json:"id"`
					Object   string            `json:"object"`
					Livemode *bool             `json:"livemode"`
					Metadata map[string]string `json:"metadata"`
				} `json:"product"`
			} `json:"price"`
		} `json:"data"`
	} `json:"line_items"`
}

func (p *StripeProvider) normalizeSession(raw stripeSession) (*Session, error) {
	if !stripeID(raw.ID, "cs_") || raw.Object != "checkout.session" || !validText(raw.ReferenceID, 200) ||
		raw.Mode != "payment" || raw.Livemode == nil || *raw.Livemode != p.livemode ||
		raw.AmountTotal == nil || *raw.AmountTotal <= 0 || !validCurrency(raw.Currency) || raw.Created <= 0 ||
		(raw.Status != "open" && raw.Status != "complete" && raw.Status != "expired") ||
		(raw.PaymentStatus != "paid" && raw.PaymentStatus != "unpaid" && raw.PaymentStatus != "no_payment_required") ||
		(raw.URL != "" && !validCheckoutURL(raw.URL)) || raw.LineItems == nil ||
		raw.LineItems.Object != "list" || raw.LineItems.HasMore == nil || *raw.LineItems.HasMore || len(raw.LineItems.Data) != 1 {
		return nil, ErrMismatch
	}
	line := raw.LineItems.Data[0]
	if line.Quantity != 1 || line.Currency != raw.Currency || line.AmountTotal == nil || *line.AmountTotal != *raw.AmountTotal || line.Price == nil {
		return nil, ErrMismatch
	}
	price := line.Price
	if !stripeID(price.ID, "price_") || price.Object != "price" || price.Type != "one_time" || (len(price.Recurring) != 0 && !bytes.Equal(bytes.TrimSpace(price.Recurring), []byte("null"))) ||
		price.Currency != raw.Currency || price.UnitAmount == nil || *price.UnitAmount != *raw.AmountTotal ||
		price.Livemode == nil || *price.Livemode != p.livemode || price.Product == nil {
		return nil, ErrMismatch
	}
	product := price.Product
	if !stripeID(product.ID, "prod_") || product.Object != "product" || product.Livemode == nil || *product.Livemode != p.livemode || !validText(product.Metadata["product_code"], 200) {
		return nil, ErrMismatch
	}
	return &Session{
		ID: raw.ID, URL: raw.URL, ReferenceID: raw.ReferenceID, Currency: raw.Currency,
		Product: product.Metadata["product_code"], PaymentStatus: raw.PaymentStatus,
		Status: raw.Status, Mode: raw.Mode, AmountMinor: *raw.AmountTotal,
		Quantity: line.Quantity, Livemode: *raw.Livemode, Created: raw.Created,
	}, nil
}

// VerifyWebhook follows Stripe's manual verification algorithm over unmodified
// bytes, accepts current v1 signatures only, and bounds both past/future skew.
// https://docs.stripe.com/webhooks#verify-manually
// https://docs.stripe.com/webhooks/signature
func (p *StripeProvider) VerifyWebhook(body []byte, header string, now time.Time) (*Event, error) {
	if !p.Ready() {
		return nil, ErrUnavailable
	}
	if len(body) == 0 || len(body) > maxProviderBody || len(header) == 0 || len(header) > maxSignatureHeader {
		return nil, ErrInvalidWebhook
	}
	var timestamp string
	var signatures [][]byte
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, ErrInvalidWebhook
		}
		switch key {
		case "t":
			if timestamp != "" || value == "" {
				return nil, ErrInvalidWebhook
			}
			timestamp = value
		case "v1":
			sig, err := hex.DecodeString(value)
			if err == nil && len(sig) == sha256.Size {
				signatures = append(signatures, sig)
			}
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds <= 0 || strconv.FormatInt(seconds, 10) != timestamp || len(signatures) == 0 {
		return nil, ErrInvalidWebhook
	}
	signedAt := time.Unix(seconds, 0)
	if signedAt.Before(now.Add(-webhookTolerance)) || signedAt.After(now.Add(webhookTolerance)) {
		return nil, ErrInvalidWebhook
	}
	mac := hmac.New(sha256.New, []byte(p.webhookSecret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	valid := false
	for _, sig := range signatures {
		if hmac.Equal(expected, sig) {
			valid = true
		}
	}
	if !valid || !uniqueJSON(body) {
		return nil, ErrInvalidWebhook
	}
	var raw struct {
		ID       string `json:"id"`
		Object   string `json:"object"`
		Type     string `json:"type"`
		Created  int64  `json:"created"`
		Livemode *bool  `json:"livemode"`
		Data     *struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &raw) != nil || !stripeID(raw.ID, "evt_") || raw.Object != "event" ||
		!validText(raw.Type, 255) || raw.Created <= 0 || raw.Livemode == nil || *raw.Livemode != p.livemode ||
		raw.Data == nil || len(raw.Data.Object) == 0 || bytes.TrimSpace(raw.Data.Object)[0] != '{' {
		return nil, ErrInvalidWebhook
	}
	digest := sha256.Sum256(body)
	event := &Event{ID: raw.ID, Type: raw.Type, Livemode: *raw.Livemode, Digest: hex.EncodeToString(digest[:])}
	if strings.HasPrefix(raw.Type, "checkout.session.") {
		var session struct {
			ID       string `json:"id"`
			Object   string `json:"object"`
			Livemode *bool  `json:"livemode"`
		}
		if json.Unmarshal(raw.Data.Object, &session) != nil || !stripeID(session.ID, "cs_") || session.Object != "checkout.session" ||
			session.Livemode == nil || *session.Livemode != p.livemode {
			return nil, ErrInvalidWebhook
		}
		event.SessionID = session.ID
	}
	return event, nil
}

func validCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

func validText(s string, max int) bool {
	if len(s) == 0 || len(s) > max || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func safeToken(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' && c != '.' && c != ':' {
			return false
		}
	}
	return true
}

func stripeID(s, prefix string) bool {
	if !strings.HasPrefix(s, prefix) || len(s) <= len(prefix) || len(s) > 255 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func validCheckoutURL(s string) bool {
	parsed, err := url.Parse(s)
	return err == nil && len(s) <= 16384 && parsed.Scheme == "https" && parsed.Host == "checkout.stripe.com" && parsed.User == nil && parsed.Path != ""
}

func validReturnURL(s string) bool {
	parsed, err := url.Parse(s)
	if err != nil || len(s) > 4096 || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(parsed.Hostname())
	return parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))
}

// uniqueJSON rejects duplicate object keys (including escaped aliases), trailing
// values and excessive nesting instead of relying on last-key-wins decoding.
func uniqueJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 64 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] || !value(depth+1) {
					return false
				}
				seen[name] = true
			}
			close, err := decoder.Token()
			return err == nil && close == json.Delim('}')
		case '[':
			for decoder.More() {
				if !value(depth + 1) {
					return false
				}
			}
			close, err := decoder.Token()
			return err == nil && close == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
