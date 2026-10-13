package billingportal

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// All credentials, customers, destinations, and transport responses in this
// suite are synthetic. No test invokes the default transport or any Stripe API.
const (
	testKey        = "sk_test_syntheticOnlyNeverReal"
	testBinding    = "7d00bbd6-29c3-4842-a190-b0cd26a63008"
	testCustomerID = "cus_synthetic123"
	testConfigID   = "bpc_synthetic123"
	testReturnURL  = "https://app.example.test/settings/billing"
	testPortalURL  = "https://billing.stripe.com/p/session?secret=syntheticOnly"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testConfig() StripeConfig {
	return StripeConfig{SecretKey: testKey, InstallationID: "test-installation-001", ConfigurationID: testConfigID,
		ReturnURL: testReturnURL, AllowedReturnURLs: []string{testReturnURL}}
}

func testProvider(t *testing.T, fn roundTripFunc) *StripeProvider {
	t.Helper()
	p, err := NewStripeProvider(testConfig(), fn)
	if err != nil || !p.Ready() {
		t.Fatalf("provider not ready: %v", err)
	}
	return p
}

func customerRequest() CustomerRequest {
	return CustomerRequest{BindingRef: testBinding, Currency: "usd", IdempotencyKey: "billing-customer-" + testBinding}
}

func portalRequest() PortalRequest {
	return PortalRequest{CustomerID: testCustomerID, BindingRef: testBinding, Currency: "usd", IdempotencyKey: "billing-portal-" + testBinding}
}

func customerFixture(p *StripeProvider) map[string]any {
	return map[string]any{"id": testCustomerID, "object": "customer", "livemode": p.livemode,
		"metadata": map[string]any{"exchat_installation": p.installationHash, "exchat_binding": testBinding, "exchat_currency": "usd"}}
}

func configurationFixture() map[string]any {
	return map[string]any{"id": testConfigID, "object": "billing_portal.configuration", "active": true, "livemode": false,
		"features": map[string]any{"subscription_update": map[string]any{"enabled": false}, "subscription_cancel": map[string]any{"enabled": false}}}
}

func portalFixture() map[string]any {
	return map[string]any{"id": "bps_synthetic123", "object": "billing_portal.session", "livemode": false,
		"configuration": testConfigID, "customer": testCustomerID, "return_url": testReturnURL,
		"flow": nil, "on_behalf_of": nil, "customer_account": nil, "url": testPortalURL}
}

func jsonResponse(value any) *http.Response {
	data, err := json.Marshal(value)
	if err != nil {
		panic("invalid synthetic fixture")
	}
	return bodyResponse(http.StatusOK, string(data))
}

func bodyResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func assertRequest(t *testing.T, r *http.Request, method, path, key string, form url.Values) {
	t.Helper()
	if r.Method != method || r.URL.Scheme != "https" || r.URL.Host != "api.stripe.com" || r.URL.Path != path || r.URL.RawQuery != "" || r.URL.User != nil {
		t.Fatalf("unexpected request destination or method")
	}
	if r.Header.Get("Authorization") != "Bearer "+testKey || r.Header.Get("Accept") != "application/json" || r.Header.Get("Idempotency-Key") != key {
		t.Fatal("unexpected request headers")
	}
	if strings.Contains(r.URL.String(), testKey) || r.Header.Get("Stripe-Account") != "" {
		t.Fatal("credential/account leaked into request")
	}
	deadline, ok := r.Context().Deadline()
	if !ok || time.Until(deadline) > operationLimit || time.Until(deadline) <= 0 {
		t.Fatal("request must have a bounded deadline")
	}
	if method == http.MethodPost {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatal("wrong content type")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		got, err := url.ParseQuery(string(data))
		if err != nil || !reflect.DeepEqual(got, form) {
			t.Fatalf("unexpected form: %v", got)
		}
	} else if r.Body != nil || r.Header.Get("Content-Type") != "" {
		t.Fatal("GET must have no body or form content type")
	}
}

func TestStripeScopeAndReadiness(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected call"); return nil, nil })
	base, err := NewStripeProvider(testConfig(), transport)
	if err != nil || !base.Ready() || len(base.Scope()) != 64 {
		t.Fatal("expected ready provider and 64-hex scope")
	}
	if _, err := hex.DecodeString(base.Scope()); err != nil || base.Scope() != strings.ToLower(base.Scope()) {
		t.Fatal("scope must be lower hex")
	}
	for _, tc := range []struct {
		name string
		edit func(*StripeConfig)
		same bool
	}{
		{"deterministic", func(*StripeConfig) {}, true},
		{"portal-independent", func(c *StripeConfig) { c.ConfigurationID = ""; c.ReturnURL = ""; c.AllowedReturnURLs = nil }, true},
		{"installation", func(c *StripeConfig) { c.InstallationID += "-other" }, false},
		{"credential-rotation", func(c *StripeConfig) { c.SecretKey += "Rotated" }, false},
		{"account-switch", func(c *StripeConfig) { c.SecretKey = "rk_test_otherSyntheticAccount" }, false},
		{"mode", func(c *StripeConfig) { c.SecretKey = "sk_live_syntheticOnly"; c.LiveMode = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.edit(&cfg)
			p, err := NewStripeProvider(cfg, transport)
			if err != nil || !p.Ready() || (p.Scope() == base.Scope()) != tc.same {
				t.Fatalf("scope/readiness mismatch: %v", err)
			}
		})
	}
	for _, prefix := range []string{"sk_test_", "rk_test_", "sk_live_", "rk_live_"} {
		cfg := testConfig()
		cfg.SecretKey = prefix + "synthetic"
		cfg.LiveMode = strings.Contains(prefix, "live")
		if p, err := NewStripeProvider(cfg, transport); err != nil || !p.Ready() {
			t.Fatalf("valid key mode rejected: %v", err)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*StripeConfig)
	}{
		{"missing-key", func(c *StripeConfig) { c.SecretKey = "" }},
		{"publishable-key", func(c *StripeConfig) { c.SecretKey = "pk_test_synthetic" }},
		{"bare-prefix", func(c *StripeConfig) { c.SecretKey = "sk_test_" }},
		{"header-injection", func(c *StripeConfig) { c.SecretKey = "sk_test_synthetic\r\nInjected: yes" }},
		{"invalid-key", func(c *StripeConfig) { c.SecretKey = "sk_test_synthetic/other" }},
		{"long-key", func(c *StripeConfig) { c.SecretKey = "sk_test_" + strings.Repeat("a", 513) }},
		{"wrong-mode", func(c *StripeConfig) { c.LiveMode = true }},
		{"missing-installation", func(c *StripeConfig) { c.InstallationID = "" }},
		{"unsafe-installation", func(c *StripeConfig) { c.InstallationID = "x\x00y" }},
		{"long-installation", func(c *StripeConfig) { c.InstallationID = strings.Repeat("x", 256) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.edit(&cfg)
			p, err := NewStripeProvider(cfg, transport)
			if p != nil || err != ErrUnavailable {
				t.Fatalf("expected static unavailable: %v", err)
			}
		})
	}
	var absent *StripeProvider
	if absent.Ready() || absent.Scope() != "" {
		t.Fatal("nil provider reported ready")
	}
	if _, err := absent.CreateCustomer(context.Background(), customerRequest()); err != ErrUnavailable {
		t.Fatal(err)
	}
	if _, err := absent.RetrieveCustomer(context.Background(), testCustomerID); err != ErrUnavailable {
		t.Fatal(err)
	}
	if _, err := absent.CreatePortal(context.Background(), portalRequest()); err != ErrUnavailable {
		t.Fatal(err)
	}
}

func TestStripeCreateCustomerExactMetadataAndCurrency(t *testing.T) {
	for _, tc := range []struct {
		name     string
		present  bool
		currency any
	}{
		{"absent", false, nil}, {"null", true, nil}, {"matching", true, "usd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var p *StripeProvider
			p = testProvider(t, func(r *http.Request) (*http.Response, error) {
				calls++
				assertRequest(t, r, http.MethodPost, "/v1/customers", customerRequest().IdempotencyKey, url.Values{
					"metadata[exchat_installation]": {p.installationHash}, "metadata[exchat_binding]": {testBinding}, "metadata[exchat_currency]": {"usd"},
				})
				if len(p.installationHash) != 64 || p.installationHash == testConfig().InstallationID {
					t.Fatal("installation metadata must be opaque")
				}
				fixture := customerFixture(p)
				if tc.present {
					fixture["currency"] = tc.currency
				}
				return jsonResponse(fixture), nil
			})
			got, err := p.CreateCustomer(context.Background(), customerRequest())
			want := &Customer{ID: testCustomerID, BindingRef: testBinding, Currency: "usd", Scope: p.Scope(), Livemode: false}
			if err != nil || !reflect.DeepEqual(got, want) || calls != 1 {
				t.Fatalf("customer mismatch: %v, calls %d", err, calls)
			}
		})
	}
}

func TestStripeRejectsInvalidCustomer(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
	}{
		{"id", func(m map[string]any) { m["id"] = "cus_" }},
		{"wrong-id", func(m map[string]any) { m["id"] = "cus_another" }},
		{"path-id", func(m map[string]any) { m["id"] = "cus_x/../../charges" }},
		{"object", func(m map[string]any) { m["object"] = "account" }},
		{"missing-mode", func(m map[string]any) { delete(m, "livemode") }},
		{"mode", func(m map[string]any) { m["livemode"] = true }},
		{"null-mode", func(m map[string]any) { m["livemode"] = nil }},
		{"string-mode", func(m map[string]any) { m["livemode"] = "false" }},
		{"case-mode", func(m map[string]any) { delete(m, "livemode"); m["LiveMode"] = false }},
		{"deleted", func(m map[string]any) { m["deleted"] = true }},
		{"null-deleted", func(m map[string]any) { m["deleted"] = nil }},
		{"string-deleted", func(m map[string]any) { m["deleted"] = "false" }},
		{"metadata-null", func(m map[string]any) { m["metadata"] = nil }},
		{"installation", func(m map[string]any) { m["metadata"].(map[string]any)["exchat_installation"] = "wrong" }},
		{"missing-binding", func(m map[string]any) { delete(m["metadata"].(map[string]any), "exchat_binding") }},
		{"unsafe-binding", func(m map[string]any) { m["metadata"].(map[string]any)["exchat_binding"] = "person@example.test" }},
		{"metadata-currency", func(m map[string]any) { m["metadata"].(map[string]any)["exchat_currency"] = "USD" }},
		{"missing-currency", func(m map[string]any) { delete(m["metadata"].(map[string]any), "exchat_currency") }},
		{"actual-currency-mismatch", func(m map[string]any) { m["currency"] = "eur" }},
		{"actual-currency-empty", func(m map[string]any) { m["currency"] = "" }},
		{"actual-currency-case", func(m map[string]any) { m["currency"] = "USD" }},
		{"actual-currency-bool", func(m map[string]any) { m["currency"] = false }},
		{"actual-currency-object", func(m map[string]any) { m["currency"] = map[string]any{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p *StripeProvider
			p = testProvider(t, func(r *http.Request) (*http.Response, error) {
				assertRequest(t, r, http.MethodGet, "/v1/customers/"+testCustomerID, "", nil)
				f := customerFixture(p)
				tc.edit(f)
				return jsonResponse(f), nil
			})
			got, err := p.RetrieveCustomer(context.Background(), testCustomerID)
			if got != nil || err != ErrInvalidCustomer {
				t.Fatalf("accepted invalid customer: %v", err)
			}
		})
	}
	for _, field := range []string{"exchat_binding", "exchat_currency"} {
		t.Run("create-mismatch-"+field, func(t *testing.T) {
			var p *StripeProvider
			p = testProvider(t, func(*http.Request) (*http.Response, error) {
				f := customerFixture(p)
				value := "other_binding"
				if field == "exchat_currency" {
					value = "eur"
				}
				f["metadata"].(map[string]any)[field] = value
				return jsonResponse(f), nil
			})
			if c, e := p.CreateCustomer(context.Background(), customerRequest()); c != nil || e != ErrInvalidCustomer {
				t.Fatalf("accepted mismatched creation: %v", e)
			}
		})
	}
}

func TestStripePortalExactSequenceAndImmutableConfiguration(t *testing.T) {
	cfg := testConfig()
	calls := 0
	var deadline time.Time
	var p *StripeProvider
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		d, _ := r.Context().Deadline()
		if calls == 1 {
			deadline = d
		} else if !deadline.Equal(d) {
			t.Fatal("portal operation deadline reset between requests")
		}
		switch calls {
		case 1:
			assertRequest(t, r, http.MethodGet, "/v1/customers/"+testCustomerID, "", nil)
			return jsonResponse(customerFixture(p)), nil
		case 2:
			assertRequest(t, r, http.MethodGet, "/v1/billing_portal/configurations/"+testConfigID, "", nil)
			return jsonResponse(configurationFixture()), nil
		case 3:
			assertRequest(t, r, http.MethodPost, "/v1/billing_portal/sessions", portalRequest().IdempotencyKey, url.Values{"customer": {testCustomerID}, "configuration": {testConfigID}, "return_url": {testReturnURL}})
			return jsonResponse(portalFixture()), nil
		default:
			t.Fatal("unexpected extra request")
			return nil, nil
		}
	})
	var err error
	p, err = NewStripeProvider(cfg, transport)
	if err != nil {
		t.Fatal(err)
	}
	// Caller mutations after construction cannot change the approved destination.
	cfg.AllowedReturnURLs[0] = "https://evil.example.test"
	cfg.ReturnURL = "https://evil.example.test"
	cfg.ConfigurationID = "bpc_other"
	cfg.SecretKey = "sk_test_other"
	got, err := p.CreatePortal(context.Background(), portalRequest())
	if err != nil || got == nil || got.URL != testPortalURL || calls != 3 {
		t.Fatalf("portal failed: %v, calls %d", err, calls)
	}
}

func TestStripePortalCustomerMismatchStopsBeforeConfiguration(t *testing.T) {
	for _, field := range []string{"exchat_binding", "exchat_currency"} {
		t.Run(field, func(t *testing.T) {
			calls := 0
			var p *StripeProvider
			p = testProvider(t, func(*http.Request) (*http.Response, error) {
				calls++
				f := customerFixture(p)
				value := "another_binding"
				if field == "exchat_currency" {
					value = "eur"
				}
				f["metadata"].(map[string]any)[field] = value
				return jsonResponse(f), nil
			})
			got, err := p.CreatePortal(context.Background(), portalRequest())
			if got != nil || err != ErrInvalidCustomer || calls != 1 {
				t.Fatalf("expected one validation call: %v, %d", err, calls)
			}
		})
	}
}

func TestStripeUnsafeOperatorConfigMakesNoRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*StripeConfig)
	}{
		{"missing-configuration", func(c *StripeConfig) { c.ConfigurationID = "" }},
		{"invalid-configuration", func(c *StripeConfig) { c.ConfigurationID = "bpc_x?expand[]=customer" }},
		{"missing-return", func(c *StripeConfig) { c.ReturnURL = "" }},
		{"http-return", func(c *StripeConfig) {
			c.ReturnURL = "http://app.example.test"
			c.AllowedReturnURLs = []string{c.ReturnURL}
		}},
		{"relative-return", func(c *StripeConfig) { c.ReturnURL = "/billing"; c.AllowedReturnURLs = []string{c.ReturnURL} }},
		{"userinfo-return", func(c *StripeConfig) {
			c.ReturnURL = "https://user@app.example.test/billing"
			c.AllowedReturnURLs = []string{c.ReturnURL}
		}},
		{"fragment-return", func(c *StripeConfig) { c.ReturnURL = testReturnURL + "#"; c.AllowedReturnURLs = []string{c.ReturnURL} }},
		{"backslash-return", func(c *StripeConfig) {
			c.ReturnURL = "https://app.example.test\\@evil.example.test"
			c.AllowedReturnURLs = []string{c.ReturnURL}
		}},
		{"missing-allowlist", func(c *StripeConfig) { c.AllowedReturnURLs = nil }},
		{"wildcard-allowlist", func(c *StripeConfig) { c.AllowedReturnURLs = []string{"https://*.example.test/settings/billing"} }},
		{"prefix-allowlist", func(c *StripeConfig) { c.AllowedReturnURLs = []string{"https://app.example.test"} }},
		{"case-allowlist", func(c *StripeConfig) { c.AllowedReturnURLs = []string{"https://APP.example.test/settings/billing"} }},
		{"unsafe-allowlist-entry", func(c *StripeConfig) { c.AllowedReturnURLs = append(c.AllowedReturnURLs, "http://other.example.test") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			tc.edit(&cfg)
			calls := 0
			p, err := NewStripeProvider(cfg, roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") }))
			if err != nil || !p.Ready() {
				t.Fatalf("portal settings must not affect customer readiness: %v", err)
			}
			if got, err := p.CreatePortal(context.Background(), portalRequest()); got != nil || err != ErrUnsafeConfiguration || calls != 0 {
				t.Fatalf("unsafe config performed calls: %v, %d", err, calls)
			}
		})
	}
}

func TestStripeUnsafeRemoteConfigStopsBeforeSession(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
	}{
		{"wrong-id", func(m map[string]any) { m["id"] = "bpc_other" }},
		{"object", func(m map[string]any) { m["object"] = "other" }},
		{"inactive", func(m map[string]any) { m["active"] = false }},
		{"missing-active", func(m map[string]any) { delete(m, "active") }},
		{"null-active", func(m map[string]any) { m["active"] = nil }},
		{"mode", func(m map[string]any) { m["livemode"] = true }},
		{"missing-mode", func(m map[string]any) { delete(m, "livemode") }},
		{"missing-features", func(m map[string]any) { delete(m, "features") }},
		{"null-features", func(m map[string]any) { m["features"] = nil }},
	}
	for _, feature := range []string{"subscription_update", "subscription_cancel"} {
		for _, state := range []string{"missing-feature", "null-feature", "missing-enabled", "null-enabled", "enabled", "wrong-type", "case-enabled"} {
			feature, state := feature, state
			cases = append(cases, struct {
				name string
				edit func(map[string]any)
			}{feature + "/" + state, func(m map[string]any) {
				features := m["features"].(map[string]any)
				f := features[feature].(map[string]any)
				switch state {
				case "missing-feature":
					delete(features, feature)
				case "null-feature":
					features[feature] = nil
				case "missing-enabled":
					delete(f, "enabled")
				case "null-enabled":
					f["enabled"] = nil
				case "enabled":
					f["enabled"] = true
				case "wrong-type":
					f["enabled"] = "false"
				case "case-enabled":
					delete(f, "enabled")
					f["Enabled"] = false
				}
			}})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var p *StripeProvider
			p = testProvider(t, func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return jsonResponse(customerFixture(p)), nil
				}
				if calls != 2 {
					t.Fatal("unsafe configuration created session")
				}
				f := configurationFixture()
				tc.edit(f)
				return jsonResponse(f), nil
			})
			if got, err := p.CreatePortal(context.Background(), portalRequest()); got != nil || err != ErrUnsafeConfiguration || calls != 2 {
				t.Fatalf("expected unsafe config: %v, calls %d", err, calls)
			}
		})
	}
}

func TestStripeRejectsUnsafePortalResponse(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
	}{
		{"id", func(m map[string]any) { m["id"] = "bps_" }},
		{"object", func(m map[string]any) { m["object"] = "checkout.session" }},
		{"mode", func(m map[string]any) { m["livemode"] = true }},
		{"missing-mode", func(m map[string]any) { delete(m, "livemode") }},
		{"null-mode", func(m map[string]any) { m["livemode"] = nil }},
		{"customer", func(m map[string]any) { m["customer"] = "cus_other" }},
		{"missing-customer", func(m map[string]any) { delete(m, "customer") }},
		{"configuration", func(m map[string]any) { m["configuration"] = "bpc_other" }},
		{"expanded-configuration", func(m map[string]any) { m["configuration"] = map[string]any{"id": testConfigID} }},
		{"return-url", func(m map[string]any) { m["return_url"] = "https://evil.example.test" }},
		{"missing-return", func(m map[string]any) { delete(m, "return_url") }},
	}
	for _, field := range []string{"flow", "on_behalf_of", "customer_account"} {
		for _, value := range []any{"", false, map[string]any{}, "acct_other"} {
			field, value := field, value
			cases = append(cases, struct {
				name string
				edit func(map[string]any)
			}{fmt.Sprintf("%s/%T", field, value), func(m map[string]any) { m[field] = value }})
		}
	}
	for _, badURL := range []string{"", "http://billing.stripe.com/p/session", "https://billing.stripe.com.evil.test/p/session", "https://evil.test/p/session", "https://user@billing.stripe.com/p/session", "https://billing.stripe.com:443/p/session", "https://billing.stripe.com/p/session#", "https://billing.stripe.com/p/session#frag", "https://billing.stripe.com./p/session", "https://BILLING.stripe.com/p/session", "//billing.stripe.com/p/session", "https://billing.stripe.com\\@evil.test/p/session", "https://billing.stripe.com/p/\nsession"} {
		badURL := badURL
		cases = append(cases, struct {
			name string
			edit func(map[string]any)
		}{"url/" + badURL, func(m map[string]any) { m["url"] = badURL }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var p *StripeProvider
			p = testProvider(t, func(*http.Request) (*http.Response, error) {
				calls++
				switch calls {
				case 1:
					return jsonResponse(customerFixture(p)), nil
				case 2:
					return jsonResponse(configurationFixture()), nil
				case 3:
					f := portalFixture()
					tc.edit(f)
					return jsonResponse(f), nil
				default:
					t.Fatal("extra call")
					return nil, nil
				}
			})
			if got, err := p.CreatePortal(context.Background(), portalRequest()); got != nil || err != ErrInvalidResponse || calls != 3 {
				t.Fatalf("unsafe portal accepted: %v, calls %d", err, calls)
			}
		})
	}
}

func TestStripeInputValidationMakesNoRequests(t *testing.T) {
	p := testProvider(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid input caused request"); return nil, nil })
	for _, bad := range []string{"", "USD", "us", "usdd", "u$d", "éa", "usd\n"} {
		c := customerRequest()
		c.Currency = bad
		if _, err := p.CreateCustomer(context.Background(), c); err != ErrInvalidRequest {
			t.Fatalf("bad create currency: %v", err)
		}
		b := portalRequest()
		b.Currency = bad
		if _, err := p.CreatePortal(context.Background(), b); err != ErrInvalidRequest {
			t.Fatalf("bad portal currency: %v", err)
		}
	}
	for _, bad := range []string{"", "unsafe ref", "person@example.test", "x\ny", "x/y", strings.Repeat("a", 256)} {
		for _, field := range []string{"binding", "key"} {
			c := customerRequest()
			b := portalRequest()
			if field == "binding" {
				c.BindingRef = bad
				b.BindingRef = bad
			} else {
				c.IdempotencyKey = bad
				b.IdempotencyKey = bad
			}
			if _, err := p.CreateCustomer(context.Background(), c); err != ErrInvalidRequest {
				t.Fatalf("bad create token: %v", err)
			}
			if _, err := p.CreatePortal(context.Background(), b); err != ErrInvalidRequest {
				t.Fatalf("bad portal token: %v", err)
			}
		}
	}
	for _, bad := range []string{"", "cus_", "cus_../other", "cus_x?expand[]=x", "cus_x#fragment", "acct_x", "cus_x-y", strings.Repeat("a", 256)} {
		if _, err := p.RetrieveCustomer(context.Background(), bad); err != ErrInvalidRequest {
			t.Fatalf("bad retrieve id: %v", err)
		}
		b := portalRequest()
		b.CustomerID = bad
		if _, err := p.CreatePortal(context.Background(), b); err != ErrInvalidRequest {
			t.Fatalf("bad portal id: %v", err)
		}
	}
	if _, err := p.CreateCustomer(nil, customerRequest()); err != ErrInvalidRequest {
		t.Fatal(err)
	}
	if _, err := p.RetrieveCustomer(nil, testCustomerID); err != ErrInvalidRequest {
		t.Fatal(err)
	}
	if _, err := p.CreatePortal(nil, portalRequest()); err != ErrInvalidRequest {
		t.Fatal(err)
	}
}

func TestStripeMalformedJSONIsBoundedAndRejected(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `true`, `{`, `{}`, `{} {}`, `{"x":1,"x":2}`, `{"x":1,"\u0078":2}`,
		`{"unknown":{"nested":1,"nested":2}}`, `{"unknown":[{"nested":1,"nested":2}]}`,
		`{"unknown":` + strings.Repeat("[", maxJSONDepth+2) + `0` + strings.Repeat("]", maxJSONDepth+2) + `}`,
		`{"unknown":[` + strings.Repeat("0,", maxJSONTokens) + `0]}`,
		`{"unknown":"` + strings.Repeat("x", maxResponseBody) + `"}`,
		"{\"unknown\":\"\xff\"}",
	} {
		t.Run(fmt.Sprintf("length-%d", len(body)), func(t *testing.T) {
			p := testProvider(t, func(*http.Request) (*http.Response, error) { return bodyResponse(200, body), nil })
			got, err := p.RetrieveCustomer(context.Background(), testCustomerID)
			if got != nil || (err != ErrInvalidResponse && err != ErrInvalidCustomer) {
				t.Fatalf("malformed response accepted: %v", err)
			}
		})
	}
	for _, duplicate := range []string{`"livemode":true,`, `"\u006civemode":true,`} {
		var p *StripeProvider
		p = testProvider(t, func(*http.Request) (*http.Response, error) {
			raw, _ := json.Marshal(customerFixture(p))
			return bodyResponse(200, "{"+duplicate+string(raw[1:])), nil
		})
		if _, err := p.RetrieveCustomer(context.Background(), testCustomerID); err != ErrInvalidResponse {
			t.Fatalf("duplicate critical field accepted: %v", err)
		}
	}
}

type trackingBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *trackingBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}
func (b *trackingBody) Close() error { b.closed = true; return nil }

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("response body leaked " + testKey + " " + testPortalURL)
}

func TestStripeResponseBodyLimitAndClose(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader(strings.Repeat("x", maxResponseBody*2))}
	p := testProvider(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
	})
	if _, err := p.RetrieveCustomer(context.Background(), testCustomerID); err != ErrInvalidResponse {
		t.Fatal(err)
	}
	if body.read != maxResponseBody+1 || !body.closed {
		t.Fatalf("unbounded/unclosed response: read=%d closed=%t", body.read, body.closed)
	}
	body = &trackingBody{Reader: errorReader{}}
	if _, err := p.RetrieveCustomer(context.Background(), testCustomerID); err != ErrUnavailable {
		t.Fatalf("unsafe body error: %v", err)
	}
	if !body.closed {
		t.Fatal("failed body not closed")
	}
}

func TestStripeTransportStatusAndRedirectErrorsAreStatic(t *testing.T) {
	p := testProvider(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport leaked " + testKey + " " + testPortalURL)
	})
	if _, err := p.RetrieveCustomer(context.Background(), testCustomerID); err != ErrUnavailable {
		t.Fatalf("raw transport error: %v", err)
	}
	for _, status := range []int{301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			body := &trackingBody{Reader: strings.NewReader(testKey + testPortalURL)}
			p := testProvider(t, func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://evil.example.test/steal"}}, Body: body}, nil
			})
			if _, err := p.RetrieveCustomer(context.Background(), testCustomerID); err != ErrUnavailable {
				t.Fatalf("raw status error: %v", err)
			}
			if calls != 1 || !body.closed {
				t.Fatalf("redirect or unclosed error: calls %d", calls)
			}
		})
	}
}

func TestStripeContextDeadlinesAndCancellation(t *testing.T) {
	for _, op := range []string{"create", "retrieve", "portal"} {
		t.Run(op, func(t *testing.T) {
			calls := 0
			p := testProvider(t, func(r *http.Request) (*http.Response, error) {
				calls++
				<-r.Context().Done()
				return nil, errors.New("cancelled " + testKey)
			})
			invoke := func(ctx context.Context) error {
				switch op {
				case "create":
					_, err := p.CreateCustomer(ctx, customerRequest())
					return err
				case "retrieve":
					_, err := p.RetrieveCustomer(ctx, testCustomerID)
					return err
				default:
					_, err := p.CreatePortal(ctx, portalRequest())
					return err
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := invoke(ctx); err != ErrUnavailable || calls != 0 {
				t.Fatalf("pre-cancelled request called transport: %v %d", err, calls)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			started := time.Now()
			if err := invoke(ctx); err != ErrUnavailable || calls != 1 || time.Since(started) > time.Second {
				t.Fatalf("deadline failure: %v %d", err, calls)
			}
		})
	}
}

func TestStripeEndpointAllowlist(t *testing.T) {
	p := testProvider(t, func(*http.Request) (*http.Response, error) { t.Fatal("forbidden endpoint called"); return nil, nil })
	for _, path := range []string{"/v1/charges", "/v1/payment_intents", "/v1/subscriptions", "/v1/refunds", "/v1/prices", "/v1/billing_portal/configurations", "/v1/billing_portal/configurations/bpc_x", "/v1/customers/cus_x"} {
		if _, err := p.request(context.Background(), http.MethodPost, path, nil, "x"); err != ErrInvalidRequest {
			t.Fatalf("forbidden mutation accepted: %s %v", path, err)
		}
	}
	for _, path := range []string{"/v1/customers", "/v1/accounts/acct_x", "/v1/customers/cus_x?expand[]=x", "/v1/billing_portal/configurations", "/v1/billing_portal/configurations/bpc_x/other"} {
		if _, err := p.request(context.Background(), http.MethodGet, path, nil, ""); err != ErrInvalidRequest {
			t.Fatalf("forbidden read accepted: %s %v", path, err)
		}
	}
	if _, err := p.request(context.Background(), http.MethodDelete, "/v1/customers/cus_x", nil, ""); err != ErrInvalidRequest {
		t.Fatal(err)
	}
}

func TestStripeCustomerBindingWorksWithoutPortalConfiguration(t *testing.T) {
	cfg := testConfig()
	cfg.ConfigurationID = ""
	cfg.ReturnURL = ""
	cfg.AllowedReturnURLs = nil
	var p *StripeProvider
	var err error
	calls := 0
	p, err = NewStripeProvider(cfg, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/customers" {
			t.Fatal("unexpected endpoint")
		}
		return jsonResponse(customerFixture(p)), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if customer, err := p.CreateCustomer(context.Background(), customerRequest()); err != nil || customer == nil || calls != 1 {
		t.Fatalf("portal settings blocked customer binding: %v", err)
	}
}

func TestStripePortalFailuresStopAtTheirStage(t *testing.T) {
	for stage := 1; stage <= 3; stage++ {
		for _, failure := range []string{"transport", "status", "malformed"} {
			t.Run(fmt.Sprintf("%d/%s", stage, failure), func(t *testing.T) {
				calls := 0
				var p *StripeProvider
				p = testProvider(t, func(*http.Request) (*http.Response, error) {
					calls++
					if calls == stage {
						switch failure {
						case "transport":
							return nil, errors.New("private upstream " + testKey + " " + testPortalURL)
						case "status":
							return bodyResponse(500, testKey+testPortalURL), nil
						default:
							return bodyResponse(200, `{"private":"`+testKey+`","private":"`+testPortalURL+`"}`), nil
						}
					}
					if calls == 1 {
						return jsonResponse(customerFixture(p)), nil
					}
					if calls == 2 {
						return jsonResponse(configurationFixture()), nil
					}
					t.Fatal("continued after failed validation")
					return nil, nil
				})
				want := ErrUnavailable
				if failure == "malformed" {
					want = ErrInvalidResponse
				}
				if got, err := p.CreatePortal(context.Background(), portalRequest()); got != nil || err != want || calls != stage {
					t.Fatalf("wrong failure boundary: %v, calls %d", err, calls)
				}
			})
		}
	}
}

func TestStripeCancellationBetweenPortalStepsPreventsSession(t *testing.T) {
	for _, cancelAt := range []int{1, 2} {
		t.Run(fmt.Sprint(cancelAt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var p *StripeProvider
			p = testProvider(t, func(*http.Request) (*http.Response, error) {
				calls++
				var response *http.Response
				if calls == 1 {
					response = jsonResponse(customerFixture(p))
				} else if calls == 2 {
					response = jsonResponse(configurationFixture())
				} else {
					t.Fatal("created a session after cancellation")
				}
				if calls == cancelAt {
					cancel()
				}
				return response, nil
			})
			if got, err := p.CreatePortal(ctx, portalRequest()); got != nil || err != ErrUnavailable || calls != cancelAt {
				t.Fatalf("operation ignored cancellation: %v, calls %d", err, calls)
			}
		})
	}
}

func TestStripeLiveModeSyntheticPortal(t *testing.T) {
	cfg := testConfig()
	cfg.SecretKey = "rk_live_syntheticNeverReal"
	cfg.LiveMode = true
	calls := 0
	var p *StripeProvider
	var err error
	p, err = NewStripeProvider(cfg, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "api.stripe.com" || r.Header.Get("Authorization") != "Bearer "+cfg.SecretKey {
			t.Fatal("unexpected request")
		}
		switch calls {
		case 1:
			customer := customerFixture(p)
			customer["currency"] = nil
			customer["deleted"] = false
			return jsonResponse(customer), nil
		case 2:
			configuration := configurationFixture()
			configuration["livemode"] = true
			return jsonResponse(configuration), nil
		case 3:
			session := portalFixture()
			session["livemode"] = true
			delete(session, "flow")
			delete(session, "on_behalf_of")
			delete(session, "customer_account")
			return jsonResponse(session), nil
		default:
			t.Fatal("unexpected call")
			return nil, nil
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.CreatePortal(context.Background(), portalRequest()); err != nil || got == nil || got.URL != testPortalURL || calls != 3 {
		t.Fatalf("live mode validation failed: %v, calls %d", err, calls)
	}
}

func TestStripeExactKeysCannotBeOverriddenByCaseAliases(t *testing.T) {
	var p *StripeProvider
	p = testProvider(t, func(*http.Request) (*http.Response, error) {
		fixture := customerFixture(p)
		fixture["LiveMode"] = true
		fixture["ID"] = "cus_wrong"
		fixture["Metadata"] = map[string]any{"exchat_binding": "wrong"}
		return jsonResponse(fixture), nil
	})
	got, err := p.RetrieveCustomer(context.Background(), testCustomerID)
	if err != nil || got == nil || got.ID != testCustomerID || got.Livemode || got.BindingRef != testBinding {
		t.Fatalf("case aliases changed exact-key interpretation: %v", err)
	}
}
