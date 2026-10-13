package billingportal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	stripeAPIURL    = "https://api.stripe.com"
	operationLimit  = 15 * time.Second
	maxResponseBody = 1 << 20
	maxJSONDepth    = 32
	maxJSONTokens   = 100000
)

// StripeConfig is operator-controlled. Configuration and return destinations
// cannot be overridden by customer or portal requests.
type StripeConfig struct {
	SecretKey         string
	InstallationID    string
	ConfigurationID   string
	ReturnURL         string
	AllowedReturnURLs []string
	LiveMode          bool
}

// StripeProvider has no endpoint override. A non-nil transport is intended only
// for synthetic tests; production uses HTTPS against api.stripe.com.
type StripeProvider struct {
	secretKey        string
	installationHash string
	scope            string
	configurationID  string
	returnURL        string
	livemode         bool
	portalConfigured bool
	client           *http.Client
}

var _ Provider = (*StripeProvider)(nil)

// NewStripeProvider validates customer credentials independently of portal
// configuration: missing portal settings must not prevent customer binding.
func NewStripeProvider(cfg StripeConfig, transport http.RoundTripper) (*StripeProvider, error) {
	live, valid := stripeKeyMode(cfg.SecretKey)
	if !valid || live != cfg.LiveMode || !safeToken(cfg.InstallationID, 255) {
		return nil, ErrUnavailable
	}
	mode := "test"
	if live {
		mode = "live"
	}
	credentialHash := digest("ex-chat/billingportal/credential/v1", cfg.SecretKey)
	p := &StripeProvider{
		secretKey:        cfg.SecretKey,
		installationHash: digest("ex-chat/billingportal/installation/v1", cfg.InstallationID),
		// Credential rotation (including an account switch) conservatively changes
		// scope. Existing bindings require explicit reconciliation by the operator.
		scope:            digest("ex-chat/billingportal/scope/v1", cfg.InstallationID, mode, credentialHash),
		configurationID:  cfg.ConfigurationID,
		returnURL:        cfg.ReturnURL,
		livemode:         live,
		portalConfigured: validPortalConfig(cfg),
		client: &http.Client{
			Transport: transport, Timeout: operationLimit,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	return p, nil
}

func (p *StripeProvider) Ready() bool {
	return p != nil && p.client != nil && p.scope != "" && p.installationHash != "" && p.secretKey != ""
}

func (p *StripeProvider) Scope() string {
	if !p.Ready() {
		return ""
	}
	return p.scope
}

// CreateCustomer sends only opaque ownership metadata. In particular it does
// not send a name, email, or Stripe's financial currency/balance parameters.
// https://docs.stripe.com/api/customers/create
func (p *StripeProvider) CreateCustomer(ctx context.Context, in CustomerRequest) (*Customer, error) {
	if !p.Ready() {
		return nil, ErrUnavailable
	}
	if ctx == nil || !safeToken(in.BindingRef, 255) || !validCurrency(in.Currency) || !safeToken(in.IdempotencyKey, 255) {
		return nil, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	form := url.Values{
		"metadata[exchat_installation]": {p.installationHash},
		"metadata[exchat_binding]":      {in.BindingRef},
		"metadata[exchat_currency]":     {in.Currency},
	}
	raw, err := p.request(ctx, http.MethodPost, "/v1/customers", form, in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	customer, err := p.customer(raw)
	if err != nil {
		return nil, err
	}
	if customer.BindingRef != in.BindingRef || customer.Currency != in.Currency {
		return nil, ErrInvalidCustomer
	}
	return customer, nil
}

// RetrieveCustomer checks the complete ownership metadata and mode. Callers
// still compare the returned binding and currency to their authoritative row.
// https://docs.stripe.com/api/customers/retrieve
func (p *StripeProvider) RetrieveCustomer(ctx context.Context, id string) (*Customer, error) {
	if !p.Ready() {
		return nil, ErrUnavailable
	}
	if ctx == nil || !stripeID(id, "cus_") {
		return nil, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	return p.retrieveCustomer(ctx, id)
}

func (p *StripeProvider) retrieveCustomer(ctx context.Context, id string) (*Customer, error) {
	raw, err := p.request(ctx, http.MethodGet, "/v1/customers/"+id, nil, "")
	if err != nil {
		return nil, err
	}
	customer, err := p.customer(raw)
	if err != nil {
		return nil, err
	}
	if customer.ID != id {
		return nil, ErrInvalidCustomer
	}
	return customer, nil
}

// CreatePortal always revalidates the customer and the explicitly selected
// configuration. It neither writes configurations nor uses the Stripe default.
// https://docs.stripe.com/api/customer_portal/configurations/retrieve
// https://docs.stripe.com/api/customer_portal/sessions/create
func (p *StripeProvider) CreatePortal(ctx context.Context, in PortalRequest) (*Portal, error) {
	if !p.Ready() {
		return nil, ErrUnavailable
	}
	if ctx == nil || !stripeID(in.CustomerID, "cus_") || !safeToken(in.BindingRef, 255) ||
		!validCurrency(in.Currency) || !safeToken(in.IdempotencyKey, 255) {
		return nil, ErrInvalidRequest
	}
	if !p.portalConfigured {
		return nil, ErrUnsafeConfiguration
	}
	// One deadline spans all three requests, rather than a fresh 15s per step.
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	customer, err := p.retrieveCustomer(ctx, in.CustomerID)
	if err != nil {
		return nil, err
	}
	if customer.BindingRef != in.BindingRef || customer.Currency != in.Currency {
		return nil, ErrInvalidCustomer
	}
	configuration, err := p.request(ctx, http.MethodGet, "/v1/billing_portal/configurations/"+p.configurationID, nil, "")
	if err != nil {
		return nil, err
	}
	if !p.safeConfiguration(configuration) {
		return nil, ErrUnsafeConfiguration
	}
	form := url.Values{
		"customer": {in.CustomerID}, "configuration": {p.configurationID}, "return_url": {p.returnURL},
	}
	raw, err := p.request(ctx, http.MethodPost, "/v1/billing_portal/sessions", form, in.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	portalURL := stringField(raw, "url")
	if !stripeID(stringField(raw, "id"), "bps_") || stringField(raw, "object") != "billing_portal.session" ||
		!boolEquals(raw, "livemode", p.livemode) || stringField(raw, "customer") != in.CustomerID ||
		stringField(raw, "configuration") != p.configurationID || stringField(raw, "return_url") != p.returnURL ||
		!absentOrNull(raw, "flow") || !absentOrNull(raw, "on_behalf_of") || !absentOrNull(raw, "customer_account") ||
		!validPortalURL(portalURL) {
		return nil, ErrInvalidResponse
	}
	return &Portal{URL: portalURL}, nil
}

func (p *StripeProvider) customer(raw jsonObject) (*Customer, error) {
	id := stringField(raw, "id")
	metadata, ok := objectField(raw, "metadata")
	binding := stringField(metadata, "exchat_binding")
	currency := stringField(metadata, "exchat_currency")
	if !stripeID(id, "cus_") || stringField(raw, "object") != "customer" ||
		!absentOrFalse(raw, "deleted") || !boolEquals(raw, "livemode", p.livemode) || !ok ||
		stringField(metadata, "exchat_installation") != p.installationHash || !safeToken(binding, 255) || !validCurrency(currency) {
		return nil, ErrInvalidCustomer
	}
	// A new Stripe customer often has no financial currency yet. The app's
	// explicit metadata currency remains authoritative; an assigned one must agree.
	if !absentOrNull(raw, "currency") && stringField(raw, "currency") != currency {
		return nil, ErrInvalidCustomer
	}
	return &Customer{ID: id, BindingRef: binding, Currency: currency, Scope: p.scope, Livemode: p.livemode}, nil
}

func (p *StripeProvider) safeConfiguration(raw jsonObject) bool {
	if stringField(raw, "id") != p.configurationID || stringField(raw, "object") != "billing_portal.configuration" ||
		!boolEquals(raw, "active", true) || !boolEquals(raw, "livemode", p.livemode) {
		return false
	}
	features, ok := objectField(raw, "features")
	if !ok {
		return false
	}
	for _, key := range []string{"subscription_update", "subscription_cancel"} {
		feature, ok := objectField(features, key)
		if !ok || !boolEquals(feature, "enabled", false) {
			return false
		}
	}
	return true
}

func (p *StripeProvider) request(ctx context.Context, method, path string, form url.Values, key string) (jsonObject, error) {
	if ctx == nil || !allowedEndpoint(method, path) {
		return nil, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, stripeAPIURL+path, body)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBody+1))
	if err != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if len(data) > maxResponseBody || !validJSON(data) {
		return nil, ErrInvalidResponse
	}
	var raw jsonObject
	if json.Unmarshal(data, &raw) != nil || raw == nil {
		return nil, ErrInvalidResponse
	}
	return raw, nil
}

func allowedEndpoint(method, path string) bool {
	if method == http.MethodPost {
		return path == "/v1/customers" || path == "/v1/billing_portal/sessions"
	}
	if method != http.MethodGet {
		return false
	}
	const customers = "/v1/customers/"
	const configurations = "/v1/billing_portal/configurations/"
	return (strings.HasPrefix(path, customers) && stripeID(strings.TrimPrefix(path, customers), "cus_")) ||
		(strings.HasPrefix(path, configurations) && stripeID(strings.TrimPrefix(path, configurations), "bpc_"))
}

func stripeKeyMode(key string) (bool, bool) {
	for _, prefix := range []string{"sk_test_", "rk_test_", "sk_live_", "rk_live_"} {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) && asciiIdentifier(key, 512) {
			return strings.Contains(prefix, "_live_"), true
		}
	}
	return false, false
}

func digest(domain string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(domain))
	for _, part := range parts {
		// Inputs cannot contain NUL; explicit domain and separators avoid collisions.
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func safeToken(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == ':' || c == '.') {
			return false
		}
	}
	return true
}

func asciiIdentifier(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func stripeID(s, prefix string) bool {
	return strings.HasPrefix(s, prefix) && len(s) > len(prefix) && asciiIdentifier(s, 255)
}

func validCurrency(s string) bool {
	return len(s) == 3 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z' && s[2] >= 'a' && s[2] <= 'z'
}

func validHTTPSURL(s string) bool {
	if len(s) == 0 || len(s) > 8192 || !utf8.ValidString(s) || strings.ContainsAny(s, "\\#") {
		return false
	}
	for _, c := range s {
		if c <= 32 || c == 127 {
			return false
		}
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Hostname() != "" && u.User == nil && u.Opaque == "" && u.Fragment == ""
}

func validPortalURL(s string) bool {
	if !validHTTPSURL(s) {
		return false
	}
	u, _ := url.Parse(s)
	return u.Host == "billing.stripe.com"
}

func validPortalConfig(cfg StripeConfig) bool {
	if !stripeID(cfg.ConfigurationID, "bpc_") || !validHTTPSURL(cfg.ReturnURL) || len(cfg.AllowedReturnURLs) == 0 || len(cfg.AllowedReturnURLs) > 64 {
		return false
	}
	allowed := false
	for _, candidate := range cfg.AllowedReturnURLs {
		if !validHTTPSURL(candidate) {
			return false
		}
		if candidate == cfg.ReturnURL {
			allowed = true
		}
	}
	return allowed
}

// Decode exact keys through raw maps rather than struct field matching, which
// otherwise accepts case-insensitive aliases for security-critical fields.
type jsonObject map[string]json.RawMessage

func stringField(raw jsonObject, key string) string {
	var result string
	if json.Unmarshal(raw[key], &result) != nil {
		return ""
	}
	return result
}

func objectField(raw jsonObject, key string) (jsonObject, bool) {
	var result jsonObject
	if json.Unmarshal(raw[key], &result) != nil || result == nil {
		return nil, false
	}
	return result, true
}

func boolEquals(raw jsonObject, key string, expected bool) bool {
	var result *bool
	return json.Unmarshal(raw[key], &result) == nil && result != nil && *result == expected
}

func absentOrNull(raw jsonObject, key string) bool {
	value, ok := raw[key]
	return !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func absentOrFalse(raw jsonObject, key string) bool {
	_, ok := raw[key]
	return !ok || boolEquals(raw, key, false)
}

// Validate every field, including unknown nested values, before decoding. The
// byte, token, and depth budgets bound both memory use and recursive work.
func validJSON(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tokens := 0
	var value func(int) bool
	value = func(depth int) bool {
		if depth > maxJSONDepth || tokens >= maxJSONTokens {
			return false
		}
		tokens++
		token, err := dec.Token()
		if err != nil {
			return false
		}
		delim, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return true
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for dec.More() {
				if tokens >= maxJSONTokens {
					return false
				}
				tokens++
				key, err := dec.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return false
				}
				if _, exists := seen[name]; exists {
					return false
				}
				seen[name] = struct{}{}
				if !value(depth + 1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for dec.More() {
				if !value(depth + 1) {
					return false
				}
			}
			end, err := dec.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}
