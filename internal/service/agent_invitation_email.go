package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const agentInvitationEmailMaxLifetime = 24 * time.Hour

var errAgentInvitationFrontendURL = errors.New("agent invitation requires a valid, secure FRONTEND_URL")
var errAgentInvitationsDisabled = errors.New("agent invitations are unavailable: AGENT_INVITATIONS_ENABLED must be true after deploying the supported invitation page")

// CheckAgentInvitationConfiguration is a side-effect-free preflight. An
// implicitly available mock must never masquerade as configured delivery.
func (s *EmailService) CheckAgentInvitationConfiguration() error {
	// This backend does not ship the invitation client page. Deployment must
	// explicitly attest that the matching browser flow is ready before issuing
	// bearer links. SMTP and a valid frontend origin alone are insufficient.
	if os.Getenv("AGENT_INVITATIONS_ENABLED") != "true" {
		return errAgentInvitationsDisabled
	}
	if !s.agentInvitationSenderConfigured() {
		return ErrSMTPNotConfigured
	}
	if !validPasswordResetAddress(s.fromEmail, false) {
		return errors.New("agent invitation sender address is invalid")
	}
	_, err := agentInvitationFrontendURL()
	return err
}

func (s *EmailService) agentInvitationSenderConfigured() bool {
	if s == nil || s.sender == nil {
		return false
	}
	// A typed nil also implements EmailSender but cannot dispatch a message.
	value := reflect.ValueOf(s.sender)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return false
		}
	}
	switch sender := s.sender.(type) {
	case *MockEmailSender:
		return s.allowMock
	case *SMTPEmailSender:
		port, err := strconv.Atoi(sender.Port)
		return s.configured && validPasswordResetHostname(strings.ToLower(sender.Host)) &&
			err == nil && port >= 1 && port <= 65535
	default:
		// Explicit dependency injection through SetSender configures other
		// implementations; allowMock alone cannot enable an arbitrary sender.
		return s.configured
	}
}

// SendAgentInvitationEmail sends a one-use credential directly to the sender.
// It deliberately bypasses EmailLog and the ordinary failed-email retry queue:
// neither message bodies nor provider errors are safe to persist. A successful
// real send means submitted to the provider, never confirmed mailbox delivery.
// An empty state means validation prevented dispatch; delivery_unknown means a
// send was attempted and its outcome is uncertain. Callers must not retry it
// automatically or assume that an error means the message was not accepted.
func (s *EmailService) SendAgentInvitationEmail(ctx context.Context, recipient, token string, accountID uint, expiresAt time.Time) (deliveryState string, err error) {
	if err := s.CheckAgentInvitationConfiguration(); err != nil {
		return "", err
	}
	if !validPasswordResetAddress(recipient, true) {
		return "", errors.New("agent invitation recipient address is invalid")
	}
	if !validAgentInvitationEmailToken(token) {
		return "", errors.New("agent invitation token is invalid")
	}
	if accountID == 0 {
		return "", errors.New("agent invitation account is invalid")
	}
	now := time.Now()
	if !expiresAt.After(now) || expiresAt.After(now.Add(agentInvitationEmailMaxLifetime)) {
		return "", errors.New("agent invitation expiry is invalid")
	}
	if ctx == nil {
		return "", errors.New("agent invitation email requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fromAddress, err := mail.ParseAddress(s.fromEmail)
	if err != nil {
		return "", errors.New("agent invitation sender address is invalid")
	}
	// FRONTEND_URL is deployment configuration, never an incoming Host header.
	baseURL, err := agentInvitationFrontendURL()
	if err != nil {
		return "", err
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/app/auth/invitation"
	baseURL.RawPath = ""
	// The browser's fragment is not sent to web servers or in Referer headers.
	baseURL.Fragment = url.Values{
		"token":      {token},
		"account_id": {strconv.FormatUint(uint64(accountID), 10)},
	}.Encode()
	invitationURL := baseURL.String()
	expiry := expiresAt.UTC().Format("2006-01-02 15:04:05 UTC")
	subject := "Your ExChat invitation"
	textBody := fmt.Sprintf("You have been invited to join an ExChat account.\n\nOpen this invitation:\n%s\n\nIf you already have an ExChat user account, sign in to accept the invitation. If you are new to ExChat, activate your user account using this link.\nThis link can be used once and expires at %s.\nIf you did not expect this invitation, you can ignore this email.\n", invitationURL, expiry)
	htmlBody := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"></head><body><h2>Your ExChat invitation</h2><p>You have been invited to join an ExChat account.</p><p><a href="%s">Open invitation</a></p><p>If you already have an ExChat user account, sign in to accept the invitation. If you are new to ExChat, activate your user account using this link.</p><p>This link can be used once and expires at %s.</p><p>If you did not expect this invitation, you can ignore this email.</p></body></html>`, html.EscapeString(invitationURL), html.EscapeString(expiry))
	// Custom senders must honor this context; built-in SMTP additionally
	// enforces its deadline and cancellation on the owned connection.
	ctx, cancel := context.WithTimeout(ctx, sensitiveEmailSendTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, sensitiveEmailContextKey{}, true)
	if err := s.sender.Send(ctx, fromAddress.Address, recipient, subject, htmlBody, textBody); err != nil {
		// The provider may echo the credential, including after accepting the
		// message. Never wrap or log its error, nor claim definitive failure.
		return "delivery_unknown", errors.New("agent invitation email delivery outcome is unknown")
	}
	// A late nil return is not a trustworthy submission acknowledgement.
	if ctx.Err() != nil {
		return "delivery_unknown", errors.New("agent invitation email delivery outcome is unknown")
	}
	if _, mock := s.sender.(*MockEmailSender); mock {
		return "captured_mock", nil
	}
	return "submitted", nil
}

func validAgentInvitationEmailToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, ch := range token {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func agentInvitationFrontendURL() (*url.URL, error) {
	u, err := passwordResetFrontendURL()
	if err != nil {
		return nil, errAgentInvitationFrontendURL
	}
	return u, nil
}
