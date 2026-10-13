// Package channel implements the bounded provider protocols used by text ingress.
package channel

import (
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var (
	ErrNotConfigured = errors.New("channel integration is not configured")
	ErrInvalid       = errors.New("invalid channel request")
	ErrUnauthorized  = errors.New("channel authentication failed")
	ErrProvider      = errors.New("channel provider unavailable or rejected request")
	ErrUnsupported   = errors.New("channel event is unsupported")
)

type Config struct {
	// Outbound is opt-in and only dispatches newly captured durable intents.
	OutboundEnabled     bool
	PublicBaseURL       string
	EncryptionKey       string
	WhatsAppAppID       string
	WhatsAppAppSecret   string
	WhatsAppVerifyToken string
	WhatsAppAPIVersion  string
	WhatsAppRedirectURI string
}

func ConfigFromEnvironment() Config {
	return Config{OutboundEnabled: os.Getenv("CHANNEL_OUTBOUND_ENABLED") == "true", PublicBaseURL: os.Getenv("CHANNEL_PUBLIC_BASE_URL"), EncryptionKey: os.Getenv("CHANNEL_CREDENTIAL_KEY"), WhatsAppAppID: os.Getenv("WHATSAPP_APP_ID"), WhatsAppAppSecret: os.Getenv("WHATSAPP_APP_SECRET"), WhatsAppVerifyToken: os.Getenv("WHATSAPP_VERIFY_TOKEN"), WhatsAppAPIVersion: os.Getenv("WHATSAPP_API_VERSION"), WhatsAppRedirectURI: os.Getenv("WHATSAPP_REDIRECT_URI")}
}

var numericID = regexp.MustCompile(`^[0-9]{1,100}$`)
var accountSID = regexp.MustCompile(`^AC[0-9a-fA-F]{32}$`)
var messageSID = regexp.MustCompile(`^(SM|MM)[0-9a-fA-F]{32}$`)
var phoneNumber = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
var apiVersion = regexp.MustCompile(`^v[0-9]{1,3}\.0$`)

func (c Config) Key() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(c.EncryptionKey)
	if err != nil || len(key) != 32 {
		return nil, ErrNotConfigured
	}
	return key, nil
}

func (c Config) Validate(provider string) error {
	if _, err := c.Key(); err != nil {
		return err
	}
	u, err := url.Parse(c.PublicBaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.TrimSpace(c.PublicBaseURL) != c.PublicBaseURL {
		return ErrNotConfigured
	}
	if provider == "whatsapp" && (!numericID.MatchString(c.WhatsAppAppID) || len(c.WhatsAppAppSecret) < 16 || len(c.WhatsAppVerifyToken) < 16 || !apiVersion.MatchString(c.WhatsAppAPIVersion)) {
		return ErrNotConfigured
	}
	if provider == "whatsapp" && c.WhatsAppRedirectURI != "" {
		redirect, err := url.Parse(c.WhatsAppRedirectURI)
		if err != nil || redirect.Scheme != "https" || redirect.Hostname() == "" || redirect.User != nil || redirect.Fragment != "" {
			return ErrNotConfigured
		}
	}
	return nil
}

func (c Config) Callback(accountID uint, provider string) string {
	return strings.TrimRight(c.PublicBaseURL, "/") + "/api/v1/accounts/" + uintString(accountID) + "/" + provider + "/callback"
}
