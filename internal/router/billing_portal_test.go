package router_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/billingportal"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/stretchr/testify/require"
)

type routerBillingProvider struct {
	customer         *billingportal.Customer
	creates, portals int
	portalKeys       []string
	fail             bool
}

func (*routerBillingProvider) Ready() bool   { return true }
func (*routerBillingProvider) Scope() string { return strings.Repeat("a", 64) }
func (p *routerBillingProvider) CreateCustomer(_ context.Context, r billingportal.CustomerRequest) (*billingportal.Customer, error) {
	p.creates++
	p.customer = &billingportal.Customer{ID: "cus_local", BindingRef: r.BindingRef, Currency: r.Currency, Scope: p.Scope()}
	return p.customer, nil
}
func (p *routerBillingProvider) RetrieveCustomer(context.Context, string) (*billingportal.Customer, error) {
	return p.customer, nil
}
func (p *routerBillingProvider) CreatePortal(_ context.Context, r billingportal.PortalRequest) (*billingportal.Portal, error) {
	p.portals++
	p.portalKeys = append(p.portalKeys, r.IdempotencyKey)
	if p.fail {
		return nil, billingportal.ErrUnavailable
	}
	if p.customer == nil || r.CustomerID != p.customer.ID || r.BindingRef != p.customer.BindingRef || r.Currency != p.customer.Currency {
		return nil, billingportal.ErrInvalidCustomer
	}
	return &billingportal.Portal{URL: "https://billing.stripe.com/p/session/local-credential"}, nil
}

func TestBillingCustomerPortalRoutesAndIsolation(t *testing.T) {
	t.Setenv("BILLING_STRIPE_MODE", "")
	f := newCompatibilityRouterFixture(t)
	provider := &routerBillingProvider{}
	f.r = router.SetupRouterWithOptions(f.cfg, f.db, ws.NewHub(), router.Options{BillingPortalProvider: provider, BillingPortalConfig: &billingportal.Config{AllowedCurrencies: []string{"eur", "usd"}}})
	call := func(path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		f.r.ServeHTTP(out, req)
		return out
	}
	prefixes := []string{fmt.Sprintf("/api/v1/accounts/%d", f.account.ID), fmt.Sprintf("/enterprise/api/v1/accounts/%d", f.account.ID)}
	for _, prefix := range prefixes {
		for _, route := range []string{"/subscription", "/checkout", "/select_billing_currency"} {
			require.Equal(t, 401, call(prefix+route, `{}`, "").Code)
			require.Equal(t, 403, call(prefix+route, `{}`, f.agentToken).Code)
			require.Equal(t, 403, call(prefix+route, `{}`, f.otherToken).Code)
		}
		out := call(prefix+"/subscription", `{}`, f.adminToken)
		require.Equal(t, 422, out.Code, out.Body.String())
		require.Contains(t, out.Body.String(), `"currency_selection_required":true`)
		require.Contains(t, out.Body.String(), `"currency_options":["eur","usd"]`)
		require.Equal(t, 409, call(prefix+"/checkout", `{}`, f.adminToken).Code)
	}
	for _, body := range []string{`null`, `[]`, `{} {}`, `{"plan_id":1,"plan_slug":"free"}`, `{"plan_slug":"free","plan_slug":"pro"}`, `{"plan_id":0}`, `{"plan_slug":null}`, `{"provider":"stripe"}`, `{"amount":1}`, `{"currency":"eur"}`, `{"customer":"cus_other"}`, `{"return_url":"https://evil.example"}`, `{"plan_slug":"` + strings.Repeat("x", 4096) + `"}`} {
		require.Equal(t, 400, call(prefixes[0]+"/subscription", body, f.adminToken).Code)
	}
	require.Equal(t, 503, call(prefixes[0]+"/subscription", `{"plan_slug":"pro"}`, f.adminToken).Code)
	for _, model := range []any{&domain.SubscriptionPlan{}, &domain.AccountSubscription{}, &domain.BillingCustomer{}} {
		var count int64
		require.NoError(t, f.db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
	for _, tc := range []struct {
		body string
		code int
	}{{`{"currency":"gbp"}`, 422}, {`{"currency":""}`, 400}, {`{"currency":"   "}`, 422}, {`{"currency":null}`, 400}, {`{"currency":123}`, 400}, {`{"currency":"KWD"}`, 400}, {`{"currency":"eur","currency":"usd"}`, 400}} {
		require.Equal(t, tc.code, call(prefixes[0]+"/select_billing_currency", tc.body, f.adminToken).Code)
	}
	var count int64
	require.NoError(t, f.db.Model(&domain.BillingCustomer{}).Count(&count).Error)
	require.Zero(t, count)
	require.Zero(t, provider.creates)
	require.Zero(t, provider.portals)
	require.Equal(t, 200, call(prefixes[0]+"/select_billing_currency", `{"currency":" EUR "}`, f.adminToken).Code)
	for _, prefix := range prefixes {
		out := call(prefix+"/subscription", "", f.adminToken)
		require.Equal(t, 200, out.Code, out.Body.String())
		require.Contains(t, out.Body.String(), `"billing_customer_status":"ready"`)
		require.NotContains(t, out.Body.String(), "active")
		require.NotContains(t, out.Body.String(), "period")
		require.NotContains(t, out.Body.String(), "cus_local")
		require.NotContains(t, out.Body.String(), "suggested_currency")
		require.Equal(t, 409, call(prefix+"/select_billing_currency", `{"currency":"USD"}`, f.adminToken).Code)
		for _, body := range []string{`{"customer":"cus_local"}`, `{"return_url":"https://evil.example"}`, `{"configuration":"bpc_other"}`, `{"flow_data":{}}`, `{"plan_id":1}`, `{"currency":"eur"}`} {
			require.Equal(t, 400, call(prefix+"/checkout", body, f.adminToken).Code)
		}
		out = call(prefix+"/checkout", `{}`, f.adminToken)
		require.Equal(t, 200, out.Code, out.Body.String())
		require.Contains(t, out.Body.String(), `"currency":"eur"`)
		require.Contains(t, out.Body.String(), "https://billing.stripe.com/")
		require.Equal(t, "no-store", out.Header().Get("Cache-Control"))
	}
	require.Equal(t, 1, provider.creates)
	require.Equal(t, 2, provider.portals)
	require.NotEqual(t, provider.portalKeys[0], provider.portalKeys[1])
	role := domain.CustomRole{AccountID: f.account.ID, Name: "Billing only", Permissions: fmt.Sprintf("[%q]", domain.PermissionBillingManage)}
	require.NoError(t, f.db.Create(&role).Error)
	_, token := f.principal(f.account.ID, domain.RoleAgent, &role.ID)
	for _, prefix := range prefixes {
		require.Equal(t, 200, call(prefix+"/subscription", `{}`, token).Code)
	}
	require.NoError(t, f.db.Model(&role).Update("permissions", "[]").Error)
	for _, prefix := range prefixes {
		require.Equal(t, 403, call(prefix+"/subscription", `{}`, token).Code)
	}
	provider.fail = true
	out := call(prefixes[0]+"/checkout", `{}`, f.adminToken)
	require.Equal(t, 503, out.Code)
	require.NotContains(t, out.Body.String(), "redirect_url")
	require.Equal(t, 400, call(prefixes[0]+"/checkout?redirect_url=secret", `{}`, f.adminToken).Code)
	for _, model := range []any{&domain.AccountBilling{}, &domain.AccountSubscription{}, &domain.AccountLimit{}, &domain.PaymentCheckoutIntent{}} {
		require.NoError(t, f.db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
	var journals []domain.LocalChangeJournal
	require.NoError(t, f.db.Where("source_module = ?", "BIL").Find(&journals).Error)
	for _, journal := range journals {
		require.NotContains(t, fmt.Sprintf("%+v", journal), "local-credential")
	}
}

func TestBillingSelectionReadKeepsHistoricalCurrencyAndFreeBranch(t *testing.T) {
	t.Setenv("BILLING_STRIPE_MODE", "")
	f := newCompatibilityRouterFixture(t)
	provider := &routerBillingProvider{}
	f.r = router.SetupRouterWithOptions(f.cfg, f.db, ws.NewHub(), router.Options{BillingPortalProvider: provider, BillingPortalConfig: &billingportal.Config{AllowedCurrencies: []string{"eur"}}})
	free := domain.SubscriptionPlan{Name: "Free", Slug: "free", PriceCents: 0}
	require.NoError(t, f.db.Create(&free).Error)
	sub := domain.AccountSubscription{AccountID: f.account.ID, PlanID: free.ID, Status: "active", BillingCurrency: "usd"}
	require.NoError(t, f.db.Create(&sub).Error)
	before := sub
	require.NoError(t, f.db.Model(&domain.Account{}).Where("id = ?", f.account.ID).Update("custom_attributes", `{"billing_currency":"aud","unrelated":true}`).Error)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+f.adminToken)
		out := httptest.NewRecorder()
		f.r.ServeHTTP(out, req)
		return out
	}
	for _, prefix := range []string{fmt.Sprintf("/api/v1/accounts/%d", f.account.ID), fmt.Sprintf("/enterprise/api/v1/accounts/%d", f.account.ID)} {
		out := call(http.MethodPost, prefix+"/select_billing_currency", `{"currency":"eur"}`)
		require.Equal(t, 200, out.Code, out.Body.String())
		out = call(http.MethodGet, prefix+"/subscription", "")
		require.Equal(t, 200, out.Code, out.Body.String())
		require.Contains(t, out.Body.String(), `"selected_billing_currency":"eur"`)
		require.Contains(t, out.Body.String(), `"billing_currency":"usd"`)
		require.Contains(t, out.Body.String(), `"billing_customer_status":"selected"`)
	}
	var after domain.AccountSubscription
	require.NoError(t, f.db.First(&after, sub.ID).Error)
	before.CreatedAt = before.CreatedAt.UTC()
	before.UpdatedAt = before.UpdatedAt.UTC()
	after.CreatedAt = after.CreatedAt.UTC()
	after.UpdatedAt = after.UpdatedAt.UTC()
	require.Equal(t, before, after)
	var account domain.Account
	require.NoError(t, f.db.First(&account, f.account.ID).Error)
	require.Equal(t, `{"billing_currency":"aud","unrelated":true}`, account.CustomAttributes)
	prefix := fmt.Sprintf("/api/v1/accounts/%d", f.account.ID)
	out := call(http.MethodPost, prefix+"/subscription", `{"plan_slug":"free"}`)
	require.Equal(t, 200, out.Code, out.Body.String())
	require.Zero(t, provider.creates)
	paid := domain.SubscriptionPlan{Name: "Paid", Slug: "paid-custom", PriceCents: 100}
	require.NoError(t, f.db.Create(&paid).Error)
	require.NoError(t, f.db.Model(&domain.AccountSubscription{}).Where("id = ?", sub.ID).Update("plan_id", paid.ID).Error)
	for _, route := range []string{"/subscription", "/select_billing_currency"} {
		body := `{}`
		if route == "/select_billing_currency" {
			body = `{"currency":"eur"}`
		}
		out = call(http.MethodPost, prefix+route, body)
		require.Equal(t, 409, out.Code, out.Body.String())
	}
	out = call(http.MethodPost, prefix+"/subscription", `{"plan_slug":"free"}`)
	require.Equal(t, 409, out.Code, out.Body.String())
	out = call(http.MethodGet, prefix+"/subscription", "")
	require.Equal(t, 200, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"billing_customer_status":"reconciliation_required"`)
	require.Zero(t, provider.creates)
}
