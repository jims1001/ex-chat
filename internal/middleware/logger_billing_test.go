package middleware

import "testing"

func TestBillingPortalQueryCredentialsNeverLogged(t *testing.T) {
	for _, prefix := range []string{"/api/v1/accounts/91/", "/enterprise/api/v1/accounts/91/"} {
		for _, route := range []string{"subscription", "checkout", "select_billing_currency"} {
			for _, suffix := range []string{"", "/"} {
				if got := safeRequestQuery(prefix+route+suffix, "unknown=https%3A%2F%2Fbilling.stripe.com%2Fp%2Fsession%2Fsecret-sentinel"); got != "" {
					t.Fatalf("query secret retained on %s", route)
				}
			}
		}
	}
	if safeRequestQuery("/api/search", "q=hello") != "q=hello" {
		t.Fatal("unrelated query changed")
	}
}
