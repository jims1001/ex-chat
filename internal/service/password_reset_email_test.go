package service

import (
	"bytes"
	"context"
	"errors"
	"html"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func passwordResetTestEmailService() (*EmailService, *MockEmailSender) {
	mock := NewMockEmailSender()
	s := &EmailService{fromEmail: "security@example.test"}
	s.SetSender(mock)
	return s, mock
}

func TestPasswordResetEmailConfiguration(t *testing.T) {
	valid := []string{
		"https://example.test",
		"https://EXAMPLE.test:8443/customer/app/",
		"https://example.test./customer",
		"https://192.0.2.1/customer",
		"https://[2001:db8::1]:8443/customer",
		"https://example.test/customer%20one",
		"http://localhost:3000",
		"http://LOCALHOST:3000/base/",
		"http://127.0.0.1:3000",
		"http://[::1]:3000/base",
	}
	invalid := []string{
		"", " ", "example.test", "/relative", "//example.test", "https:example.test", "https:///path",
		"http://example.test", "ftp://example.test", "javascript:alert(1)",
		"http://localhost.evil.test", "http://127.0.0.1.evil.test", "http://127.0.0.2", "http://localhost.",
		"https://user:password@example.test", "https://user@example.test", "https://example.test@evil.test",
		"https://example.test?redirect=https://evil.test", "https://example.test?", "https://example.test#fragment", "https://example.test#",
		"https://example.test\r\nBcc:evil@example.test", "https://example.test\n", " https://example.test", "https://example.test ",
		"https://example.test\\@evil.test", "https://example.test/<script>\\evil", "https://example.test/%5cevil",
		"https://example.test/%0d%0aevil", "https://example.test/%00evil", "https://example.test/%zz",
		"https://exa mple.test", "https://example.test%2fevil.test", "https://example.test%40evil.test", "https://exa_mple.test",
		"https://-example.test", "https://example-.test", "https://example..test", "https://.example.test", "https://example.test..",
		"https://example.test:", "https://example.test:0", "https://example.test:65536", "https://example.test:abc",
		"https://[example.test]", "https://[127.0.0.1]", "https://[::1", "https://2001:db8::1:443", "https://[fe80::1%25eth0]",
		"https://example.test/base/../other", "https://example.test/base/%2e%2e/other", "https://example.test/base/./other",
		"https://example.test/base/%252e%252e/other", "https://example.test/base%2fother", "https://example.test/base//other",
	}
	for _, base := range valid {
		t.Run("valid/"+base, func(t *testing.T) {
			t.Setenv("FRONTEND_URL", base)
			s, mock := passwordResetTestEmailService()
			require.NoError(t, s.CheckPasswordResetConfiguration())
			require.Empty(t, mock.GetSentEmails(), "preflight must not dispatch email")
		})
	}
	for _, base := range invalid {
		t.Run("invalid/"+base, func(t *testing.T) {
			t.Setenv("FRONTEND_URL", base)
			t.Setenv("APP_URL", "https://fallback.example.test")
			s, mock := passwordResetTestEmailService()
			require.ErrorIs(t, s.CheckPasswordResetConfiguration(), errPasswordResetFrontendURL)
			err := s.SendPasswordResetEmail(context.Background(), "user@example.test", "not-a-real-secret", time.Now().Add(time.Hour))
			require.ErrorIs(t, err, errPasswordResetFrontendURL)
			require.Empty(t, mock.GetSentEmails())
		})
	}
}

func TestPasswordResetEmailRequiresConfiguredSender(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	for _, s := range []*EmailService{nil, {}, {sender: NewMockEmailSender(), fromEmail: "security@example.test"}} {
		require.ErrorIs(t, s.CheckPasswordResetConfiguration(), ErrSMTPNotConfigured)
		require.ErrorIs(t, s.SendPasswordResetEmail(context.Background(), "user@example.test", "token", time.Now().Add(time.Hour)), ErrSMTPNotConfigured)
	}
	s, _ := passwordResetTestEmailService()
	for _, from := range []string{"", "invalid", "sender@example.test\r\nBcc: attacker@example.test"} {
		s.fromEmail = from
		require.Error(t, s.CheckPasswordResetConfiguration())
	}
	s.fromEmail = "ExChat Security <security@example.test>"
	require.NoError(t, s.CheckPasswordResetConfiguration())
	s.SetSender(nil)
	require.ErrorIs(t, s.CheckPasswordResetConfiguration(), ErrSMTPNotConfigured)
}

func TestPasswordResetEmailPreservesPrefixAndEscapesCredential(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test:8443/customer&team/'quoted'%20name/")
	s, mock := passwordResetTestEmailService()
	token := `secret+/?&next=https://evil.test#"><script>alert('x')</script>`
	expiry := time.Now().Add(time.Hour).In(time.FixedZone("other-zone", -7*60*60))
	require.NoError(t, s.SendPasswordResetEmail(context.Background(), "user@example.test", token, expiry))
	emails := mock.GetSentEmails()
	require.Len(t, emails, 1)
	email := emails[0]
	require.Equal(t, "security@example.test", email.From)
	require.Equal(t, "user@example.test", email.To)
	require.Equal(t, "Reset your ExChat password", email.Subject)
	var resetURL string
	for _, line := range strings.Split(email.TextBody, "\n") {
		if strings.HasPrefix(line, "https://") {
			resetURL = line
		}
	}
	require.NotEmpty(t, resetURL)
	u, err := url.Parse(resetURL)
	require.NoError(t, err)
	require.Equal(t, "https", u.Scheme)
	require.Equal(t, "example.test:8443", u.Host)
	require.Equal(t, "/customer&team/'quoted' name/app/auth/password/edit", u.Path)
	require.Equal(t, url.Values{"reset_password_token": {token}}, u.Query())
	require.Empty(t, u.Fragment)
	require.Contains(t, email.HTMLBody, `href="`+html.EscapeString(resetURL)+`"`)
	require.NotContains(t, email.HTMLBody, token)
	require.NotContains(t, email.HTMLBody, "<script>")
	require.Contains(t, email.HTMLBody, "&amp;team/")
	for _, body := range []string{email.HTMLBody, email.TextBody} {
		require.Contains(t, body, "can be used once")
		require.Contains(t, body, expiry.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	require.NotContains(t, email.Subject, token)
}

func TestPasswordResetEmailNormalizesDisplayNameSender(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	s, mock := passwordResetTestEmailService()
	s.SetFromEmail("ExChat Security <security@example.test>")
	require.NoError(t, s.CheckPasswordResetConfiguration())
	require.NoError(t, s.SendPasswordResetEmail(context.Background(), "user@example.test", "local-test-token", time.Now().Add(time.Hour)))
	emails := mock.GetSentEmails()
	require.Len(t, emails, 1)
	require.Equal(t, "security@example.test", emails[0].From)
}

func TestPasswordResetEmailRejectsInvalidInput(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	now := time.Now()
	for _, tc := range []struct {
		name      string
		recipient string
		token     string
		expiry    time.Time
	}{
		{"empty recipient", "", "token", now.Add(time.Hour)},
		{"invalid recipient", "not-an-email", "token", now.Add(time.Hour)},
		{"header injection", "user@example.test\r\nBcc:evil@example.test", "token", now.Add(time.Hour)},
		{"recipient list", "user@example.test, evil@example.test", "token", now.Add(time.Hour)},
		{"display name", "User <user@example.test>", "token", now.Add(time.Hour)},
		{"empty token", "user@example.test", "", now.Add(time.Hour)},
		{"whitespace token", "user@example.test", "  token  ", now.Add(time.Hour)},
		{"control token", "user@example.test", "token\nsecret", now.Add(time.Hour)},
		{"oversized token", "user@example.test", strings.Repeat("x", 1025), now.Add(time.Hour)},
		{"zero expiry", "user@example.test", "token", time.Time{}},
		{"expired", "user@example.test", "token", now.Add(-time.Second)},
		{"immediate expiry", "user@example.test", "token", now},
		{"excessive expiry", "user@example.test", "token", now.Add(3 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := passwordResetTestEmailService()
			require.Error(t, s.SendPasswordResetEmail(context.Background(), tc.recipient, tc.token, tc.expiry))
			require.Empty(t, mock.GetSentEmails())
		})
	}
	s, mock := passwordResetTestEmailService()
	require.Error(t, s.SendPasswordResetEmail(nil, "user@example.test", "token", now.Add(time.Hour)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, s.SendPasswordResetEmail(ctx, "user@example.test", "token", now.Add(time.Hour)), context.Canceled)
	require.Empty(t, mock.GetSentEmails())
	require.NoError(t, s.SendPasswordResetEmail(context.Background(), "user@example.test", "token", time.Now().Add(2*time.Hour)))
}

type passwordResetFailingSender struct {
	err error
}

func (s passwordResetFailingSender) Send(context.Context, string, string, string, string, string) error {
	return s.err
}

func TestPasswordResetEmailDoesNotPersistOrExposeSensitiveErrors(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.EmailLog{}))
	s, _ := passwordResetTestEmailService()
	s.db = db
	token := "raw-sensitive-reset-token-for-local-test"
	require.NoError(t, s.SendPasswordResetEmail(context.Background(), "user@example.test", token, time.Now().Add(time.Hour)))
	leakyErr := errors.New("failed to send body containing " + token)
	s.SetSender(passwordResetFailingSender{err: leakyErr})
	err = s.SendPasswordResetEmail(context.Background(), "user@example.test", token, time.Now().Add(time.Hour))
	require.EqualError(t, err, "password reset email delivery failed")
	require.NotContains(t, err.Error(), token)
	require.False(t, errors.Is(err, leakyErr), "callers must not unwrap sensitive sender errors")
	var count int64
	require.NoError(t, db.Model(&domain.EmailLog{}).Count(&count).Error)
	require.Zero(t, count, "neither successful nor failed reset emails enter persisted audit/retry storage")
}

func TestPasswordResetEmailRedactsBuiltInSenderLogs(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	s, mock := passwordResetTestEmailService()
	token := "secret-reset-token-that-must-not-be-logged"
	mock.SetFailError(errors.New("smtp echoed reset body " + token))
	err := s.SendPasswordResetEmail(context.Background(), "user@example.test", token, time.Now().Add(time.Hour))
	require.EqualError(t, err, "password reset email delivery failed")
	require.Contains(t, logs.String(), "email delivery failed")
	require.NotContains(t, logs.String(), token)
	require.NotContains(t, logs.String(), "smtp echoed reset body")
	mock.SetFailError(nil)
	require.NoError(t, s.SendPasswordResetEmail(context.Background(), "user@example.test", token, time.Now().Add(time.Hour)))
	require.NotContains(t, logs.String(), token)
	require.NotContains(t, logs.String(), "reset_password_token")
	underlying := errors.New("ordinary diagnostic")
	require.Equal(t, underlying.Error(), safeEmailSendError(context.Background(), underlying), "ordinary email logging is unchanged")
}
