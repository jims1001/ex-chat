package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const passwordResetEmailMaxLifetime = 2 * time.Hour

var errPasswordResetFrontendURL = errors.New("password reset requires a valid, secure FRONTEND_URL")

type sensitiveEmailContextKey struct{}

// SMTP failures may contain server-echoed message content. Credential messages
// mark their context to select bounded SMTP and redact errors in built-in senders.
func safeEmailSendError(ctx context.Context, err error) string {
	if ctx != nil && ctx.Value(sensitiveEmailContextKey{}) == true {
		return "email delivery failed"
	}
	return err.Error()
}

// CheckPasswordResetConfiguration can run before looking up an account, so
// deployment configuration failures do not disclose whether an account exists.
func (s *EmailService) CheckPasswordResetConfiguration() error {
	if s == nil || !s.IsConfigured() {
		return ErrSMTPNotConfigured
	}
	if !validPasswordResetAddress(s.fromEmail, false) {
		return errors.New("password reset sender address is invalid")
	}
	_, err := passwordResetFrontendURL()
	return err
}

// SendPasswordResetEmail delivers a short-lived credential directly to the
// configured sender. Unlike ordinary email, its bodies and delivery errors must
// never be stored in EmailLog or the ordinary failed-email retry queue.
func (s *EmailService) SendPasswordResetEmail(ctx context.Context, recipient, token string, expiresAt time.Time) error {
	if err := s.CheckPasswordResetConfiguration(); err != nil {
		return err
	}
	if !validPasswordResetAddress(recipient, true) {
		return errors.New("password reset recipient address is invalid")
	}
	if token == "" || len(token) > 1024 || strings.TrimSpace(token) != token || containsPasswordResetControl(token) {
		return errors.New("password reset token is invalid")
	}
	now := time.Now()
	if !expiresAt.After(now) || expiresAt.After(now.Add(passwordResetEmailMaxLifetime)) {
		return errors.New("password reset expiry is invalid")
	}
	if ctx == nil {
		return errors.New("password reset email requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// SMTP envelope senders require a bare mailbox, even when the configured
	// sender includes an optional display name for ordinary email headers.
	fromAddress, err := mail.ParseAddress(s.fromEmail)
	if err != nil {
		return errors.New("password reset sender address is invalid")
	}

	// Never derive a credential-bearing URL from an incoming request's Host.
	baseURL, err := passwordResetFrontendURL()
	if err != nil {
		return err
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/app/auth/password/edit"
	baseURL.RawPath = ""
	baseURL.RawQuery = url.Values{"reset_password_token": {token}}.Encode()
	resetURL := baseURL.String()
	expiry := expiresAt.UTC().Format("2006-01-02 15:04:05 UTC")
	subject := "Reset your ExChat password"
	textBody := fmt.Sprintf("Use this link to reset your ExChat password:\n\n%s\n\nThis link can be used once and expires at %s.\nIf you did not request a password reset, you can ignore this email.\n", resetURL, expiry)
	htmlBody := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"></head><body><h2>Reset your ExChat password</h2><p><a href="%s">Reset password</a></p><p>This link can be used once and expires at %s.</p><p>If you did not request a password reset, you can ignore this email.</p></body></html>`, html.EscapeString(resetURL), html.EscapeString(expiry))
	// Custom senders must honor this context; built-in SMTP additionally
	// enforces its deadline and cancellation on the owned connection.
	ctx, cancel := context.WithTimeout(ctx, sensitiveEmailSendTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, sensitiveEmailContextKey{}, true)
	if err := s.sender.Send(ctx, fromAddress.Address, recipient, subject, htmlBody, textBody); err != nil {
		// Some senders include their input in errors. Do not wrap or log the
		// original error, because callers may log or return this error.
		return errors.New("password reset email delivery failed")
	}
	// A late nil return is not a trustworthy submission acknowledgement.
	if ctx.Err() != nil {
		return errors.New("password reset email delivery failed")
	}
	return nil
}

func validPasswordResetAddress(value string, bare bool) bool {
	if value == "" || strings.TrimSpace(value) != value || containsPasswordResetControl(value) {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && (!bare || address.Address == value)
}

func containsPasswordResetControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

func passwordResetFrontendURL() (*url.URL, error) {
	return secureEmailFrontendURL(os.Getenv("FRONTEND_URL"), errPasswordResetFrontendURL)
}

// secureEmailFrontendURL validates deployment-owned URLs for credential-bearing
// links. Callers select their own fallback policy and static validation error.
func secureEmailFrontendURL(raw string, invalidURL error) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || containsPasswordResetControl(raw) || strings.ContainsAny(raw, "\\?#") {
		return nil, invalidURL
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.User != nil || u.Host == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, invalidURL
	}
	hostname := strings.ToLower(u.Hostname())
	if !validPasswordResetHostname(hostname) {
		return nil, invalidURL
	}
	if strings.HasPrefix(u.Host, "[") {
		addr, err := netip.ParseAddr(hostname)
		if err != nil || !addr.Is6() {
			return nil, invalidURL
		}
	} else if strings.Contains(hostname, ":") {
		return nil, invalidURL
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (hostname != "localhost" && hostname != "127.0.0.1" && hostname != "::1")) {
		return nil, invalidURL
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, invalidURL
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, invalidURL
		}
	}
	// Reject ambiguous path forms rather than silently normalizing a configured
	// application prefix. Decoded percent signs can hide double-encoded paths.
	escapedPath := strings.ToLower(u.EscapedPath())
	if containsPasswordResetControl(u.Path) || strings.ContainsAny(u.Path, "\\%") || strings.Contains(u.Path, "//") || strings.Contains(escapedPath, "%2f") {
		return nil, invalidURL
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, invalidURL
		}
	}
	return u, nil
}

func validPasswordResetHostname(hostname string) bool {
	if addr, err := netip.ParseAddr(hostname); err == nil {
		return addr.Zone() == ""
	}
	if len(hostname) == 0 || len(hostname) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(hostname, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return false
			}
		}
	}
	return true
}
