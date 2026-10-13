package middleware

import (
	"net/url"
	"strings"
	"testing"
)

func TestChannelCredentialQueryRedaction(t *testing.T) {
	raw := "hub.mode=subscribe&hub.verify_token=secret-verification&access_token=secret-access&auth_token=secret-auth&client_secret=secret-client&hub.challenge=123"
	got := safeRequestQuery("/api/v1/accounts/91/whatsapp/callback", raw)
	if strings.Contains(got, "secret-") {
		t.Fatal("credential in log query", got)
	}
	values, _ := url.ParseQuery(got)
	if values.Get("hub.challenge") != "123" || values.Get("hub.verify_token") != "[REDACTED]" {
		t.Fatal(got)
	}
}
