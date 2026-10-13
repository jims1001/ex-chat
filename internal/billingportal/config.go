package billingportal

import (
	"os"
	"strings"
)

func ConfigFromEnvironment() Config {
	raw := os.Getenv("BILLING_ALLOWED_CURRENCIES")
	if raw == "" {
		return Config{}
	}
	currencies := strings.Split(raw, ",")
	for i := range currencies {
		currencies[i] = strings.TrimSpace(currencies[i])
	}
	return Config{AllowedCurrencies: currencies}
}

// Credentials, explicit mode and installation scope are independent of top-up
// and webhook readiness. No currency, portal config, URL or locale is inferred.
func ProviderFromEnvironment() Provider {
	mode := os.Getenv("BILLING_STRIPE_MODE")
	if mode != "test" && mode != "live" {
		return nil
	}
	p, err := NewStripeProvider(StripeConfig{SecretKey: os.Getenv("BILLING_STRIPE_SECRET_KEY"), InstallationID: os.Getenv("BILLING_INSTALLATION_ID"), LiveMode: mode == "live", ConfigurationID: os.Getenv("BILLING_PORTAL_CONFIGURATION_ID"), ReturnURL: os.Getenv("BILLING_PORTAL_RETURN_URL"), AllowedReturnURLs: strings.Split(os.Getenv("BILLING_ALLOWED_RETURN_URLS"), ",")}, nil)
	if err != nil {
		return nil
	}
	return p
}
