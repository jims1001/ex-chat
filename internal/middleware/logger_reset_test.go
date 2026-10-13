package middleware

import (
	"strings"
	"testing"
)

func TestRequestLogRedactsResetCredentials(t *testing.T) {
	for _, raw := range []string{"reset_password_token=secret-value&x=1", "reset%5Fpassword%5Ftoken=secret-value", "reset_password_token=secret-value&reset_password_token=other-value", "reset_password_token=secret-value%ZZ"} {
		got := safeRequestQuery("/app/auth/password/edit", raw)
		if strings.Contains(got, "secret-value") || strings.Contains(got, "other-value") {
			t.Fatal("reset credential leaked into log query")
		}
	}
	if strings.Contains(safeRequestQuery("/auth/password", "token=raw-secret"), "raw-secret") {
		t.Fatal("API token leaked")
	}
	if got := safeRequestQuery("/api/search", "q=hello"); got != "q=hello" {
		t.Fatalf("unrelated query changed: %s", got)
	}
}
