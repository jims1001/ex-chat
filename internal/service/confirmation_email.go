package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

const confirmationEmailMaxLifetime = 24 * time.Hour

var errConfirmationFrontendURL = errors.New("confirmation requires a valid, secure FRONTEND_URL or APP_URL")

// CheckConfirmationConfiguration is a side-effect-free preflight. It checks
// delivery readiness, not whether signup or login requires email confirmation.
func (s *EmailService) CheckConfirmationConfiguration() error {
	// Invitations already use this strict transport check: nil and typed-nil
	// senders, incomplete SMTP configurations, and implicit mocks cannot send.
	if !s.agentInvitationSenderConfigured() {
		return ErrSMTPNotConfigured
	}
	if !validPasswordResetAddress(s.fromEmail, false) {
		return errors.New("confirmation sender address is invalid")
	}
	_, err := confirmationFrontendURL()
	return err
}

// SendConfirmationEmail passes a one-use credential directly to the configured
// transport. Its bodies and provider errors never enter EmailLog or the retry
// queue. Empty state means validation prevented dispatch; delivery_unknown means
// a send was attempted and must not be retried automatically. Provider acceptance
// is only submitted, not proof of mailbox delivery.
func (s *EmailService) SendConfirmationEmail(ctx context.Context, user *domain.User, rawToken string, expiresAt time.Time) (deliveryState string, err error) {
	if err := s.CheckConfirmationConfiguration(); err != nil {
		return "", err
	}
	if user == nil || !validPasswordResetAddress(user.Email, true) {
		return "", errors.New("confirmation recipient address is invalid")
	}
	if !validConfirmationEmailToken(rawToken) {
		return "", errors.New("confirmation token is invalid")
	}
	now := time.Now()
	if !expiresAt.After(now) || expiresAt.After(now.Add(confirmationEmailMaxLifetime)) {
		return "", errors.New("confirmation expiry is invalid")
	}
	if ctx == nil {
		return "", errors.New("confirmation email requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fromAddress, err := mail.ParseAddress(s.fromEmail)
	if err != nil {
		return "", errors.New("confirmation sender address is invalid")
	}
	// Preserve the existing client route and query parameter. This URL comes
	// only from deployment configuration, never from a request's Host header.
	baseURL, err := confirmationFrontendURL()
	if err != nil {
		return "", err
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/auth/confirmation"
	baseURL.RawPath = ""
	baseURL.RawQuery = url.Values{"confirmation_token": {rawToken}}.Encode()
	confirmationURL := baseURL.String()
	expiry := expiresAt.UTC().Format("2006-01-02 15:04:05 UTC")
	subject := "欢迎加入 ExChat - 请验证您的电子邮箱"
	textBody := fmt.Sprintf("欢迎加入 ExChat！\n\n尊敬的 %s：\n请访问以下链接完成电子邮箱验证：\n%s\n\n此链接仅可使用一次，有效期至 %s。\n如果这不是您的操作，请忽略此邮件。\n", user.Name, confirmationURL, expiry)
	htmlBody := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"></head><body><h2>欢迎加入 ExChat</h2><p>尊敬的 %s：</p><p><a href="%s">验证我的邮箱</a></p><p>此链接仅可使用一次，有效期至 %s。</p><p>如果这不是您的操作，请忽略此邮件。</p></body></html>`, html.EscapeString(user.Name), html.EscapeString(confirmationURL), html.EscapeString(expiry))
	// A custom injected sender must honor this context. Built-in SMTP also
	// enforces it on the socket; we never detach a send into a goroutine.
	ctx, cancel := context.WithTimeout(ctx, sensitiveEmailSendTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, sensitiveEmailContextKey{}, true)
	if err := s.sender.Send(ctx, fromAddress.Address, user.Email, subject, htmlBody, textBody); err != nil {
		// Providers can echo the bearer credential in errors, including after
		// accepting a message. Never wrap, persist, or log their original error.
		return "delivery_unknown", errors.New("confirmation email delivery outcome is unknown")
	}
	if ctx.Err() != nil {
		return "delivery_unknown", errors.New("confirmation email delivery outcome is unknown")
	}
	if _, mock := s.sender.(*MockEmailSender); mock {
		return "captured_mock", nil
	}
	return "submitted", nil
}

func validConfirmationEmailToken(token string) bool {
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

func confirmationFrontendURL() (*url.URL, error) {
	raw := os.Getenv("FRONTEND_URL")
	if raw == "" {
		raw = os.Getenv("APP_URL")
	}
	if raw == "" {
		raw = "http://localhost:3000"
	}
	// A nonempty but invalid explicit setting must fail closed, never fall back.
	return secureEmailFrontendURL(raw, errConfirmationFrontendURL)
}
