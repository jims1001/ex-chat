package payment

import (
	"os"
	"strings"
)

// ConfigFromEnvironment uses a deliberately small, operator-only return URL
// allowlist. Request bodies cannot override a URL or the provider API endpoint.
func ConfigFromEnvironment() Config {
	var allowed []string
	for _, u := range strings.Split(os.Getenv("PAYMENT_ALLOWED_RETURN_URLS"), ",") {
		if u = strings.TrimSpace(u); u != "" {
			allowed = append(allowed, u)
		}
	}
	return Config{SuccessURL: os.Getenv("PAYMENT_SUCCESS_URL"), CancelURL: os.Getenv("PAYMENT_CANCEL_URL"), AllowedReturnURLs: allowed}
}
