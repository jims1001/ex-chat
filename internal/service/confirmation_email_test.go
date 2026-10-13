package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

const confirmationTestEmailToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func confirmationTestEmailService() (*EmailService, *MockEmailSender) {
	mock := NewMockEmailSender()
	s := &EmailService{fromEmail: "confirm@example.test"}
	s.SetSender(mock)
	return s, mock
}

func confirmationTestEmailUser() *domain.User {
	return &domain.User{ID: 42, Name: "张三 <script>alert('x')</script>", Email: "user@example.test"}
}

func TestConfirmationEmailConfiguration(t *testing.T) {
	valid := []string{
		"https://example.test", "https://EXAMPLE.test:8443/customer/app/",
		"https://example.test./customer", "https://192.0.2.1/customer",
		"https://[2001:db8::1]:8443/customer", "https://example.test/customer%20one",
		"http://localhost:3000", "http://LOCALHOST:3000/base/",
		"http://127.0.0.1:3000", "http://[::1]:3000/base",
	}
	invalid := []string{
		" ", "example.test", "/relative", "//example.test", "https:example.test", "https:///path",
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
	for _, setting := range []string{"FRONTEND_URL", "APP_URL"} {
		for _, base := range valid {
			t.Run(setting+"/valid/"+base, func(t *testing.T) {
				t.Setenv("FRONTEND_URL", "")
				t.Setenv("APP_URL", "")
				t.Setenv(setting, base)
				s, mock := confirmationTestEmailService()
				require.NoError(t, s.CheckConfirmationConfiguration())
				require.Empty(t, mock.GetSentEmails(), "preflight must not dispatch email")
			})
		}
		for _, base := range invalid {
			t.Run(setting+"/invalid/"+base, func(t *testing.T) {
				t.Setenv("FRONTEND_URL", "")
				t.Setenv("APP_URL", "https://fallback.example.test")
				t.Setenv(setting, base)
				s, mock := confirmationTestEmailService()
				require.ErrorIs(t, s.CheckConfirmationConfiguration(), errConfirmationFrontendURL)
				state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
				require.ErrorIs(t, err, errConfirmationFrontendURL)
				require.EqualError(t, err, errConfirmationFrontendURL.Error())
				require.Empty(t, state)
				require.Empty(t, mock.GetSentEmails())
			})
		}
	}
}

func TestConfirmationEmailPreservesConfigurationFallbacks(t *testing.T) {
	for _, tc := range []struct{ name, frontend, app, expected string }{
		{"frontend wins", "https://frontend.example.test/prefix/", "https://app.example.test", "https://frontend.example.test/prefix/auth/confirmation"},
		{"valid frontend ignores invalid app", "https://frontend.example.test", "http://untrusted.example.test", "https://frontend.example.test/auth/confirmation"},
		{"app fallback", "", "https://app.example.test/prefix/", "https://app.example.test/prefix/auth/confirmation"},
		{"local fallback", "", "", "http://localhost:3000/auth/confirmation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FRONTEND_URL", tc.frontend)
			t.Setenv("APP_URL", tc.app)
			s, mock := confirmationTestEmailService()
			state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
			require.NoError(t, err)
			require.Equal(t, "captured_mock", state)
			emails := mock.GetSentEmails()
			require.Len(t, emails, 1)
			require.Contains(t, emails[0].TextBody, tc.expected+"?confirmation_token="+confirmationTestEmailToken)
		})
	}
	t.Run("unset environment", func(t *testing.T) {
		t.Setenv("FRONTEND_URL", "restore-on-cleanup")
		t.Setenv("APP_URL", "restore-on-cleanup")
		require.NoError(t, os.Unsetenv("FRONTEND_URL"))
		require.NoError(t, os.Unsetenv("APP_URL"))
		s, mock := confirmationTestEmailService()
		require.NoError(t, s.CheckConfirmationConfiguration())
		state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
		require.NoError(t, err)
		require.Equal(t, "captured_mock", state)
		require.Contains(t, mock.GetSentEmails()[0].TextBody, "http://localhost:3000/auth/confirmation?confirmation_token=")
		s.SetSender(nil)
		require.ErrorIs(t, s.CheckConfirmationConfiguration(), ErrSMTPNotConfigured, "a fallback URL must not imply configured delivery")
	})
}

func TestConfirmationEmailRequiresConfiguredSender(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	var nilMock *MockEmailSender
	var nilSMTP *SMTPEmailSender
	var nilCustom *confirmationRecordingEmailSender
	for _, s := range []*EmailService{
		nil, {},
		{sender: NewMockEmailSender(), fromEmail: "confirm@example.test"},
		{sender: NewMockEmailSender(), configured: true, fromEmail: "confirm@example.test"},
		{sender: nilMock, allowMock: true, fromEmail: "confirm@example.test"},
		{sender: nilSMTP, configured: true, fromEmail: "confirm@example.test"},
		{sender: nilCustom, configured: true, fromEmail: "confirm@example.test"},
		{sender: &confirmationRecordingEmailSender{}, allowMock: true, fromEmail: "confirm@example.test"},
	} {
		require.ErrorIs(t, s.CheckConfirmationConfiguration(), ErrSMTPNotConfigured)
		state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
		require.ErrorIs(t, err, ErrSMTPNotConfigured)
		require.Empty(t, state)
	}
	s, mock := confirmationTestEmailService()
	for _, from := range []string{"", "invalid", "sender@example.test\r\nBcc: attacker@example.test", " sender@example.test", "sender@example.test\x00", "first@example.test, second@example.test"} {
		s.fromEmail = from
		require.Error(t, s.CheckConfirmationConfiguration())
		state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
		require.EqualError(t, err, "confirmation sender address is invalid")
		require.Empty(t, state)
	}
	require.Empty(t, mock.GetSentEmails())
	s.fromEmail = "ExChat Confirmation <confirm@example.test>"
	require.NoError(t, s.CheckConfirmationConfiguration())
	s.SetSender(nil)
	require.ErrorIs(t, s.CheckConfirmationConfiguration(), ErrSMTPNotConfigured)
}

func TestConfirmationEmailRejectsIncompleteSMTPWithoutNetwork(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	for _, tc := range []struct{ host, port string }{
		{"", "587"}, {"smtp.example.test\r\n", "587"}, {"smtp://example.test", "587"},
		{"smtp.example.test", ""}, {"smtp.example.test", "0"}, {"smtp.example.test", "65536"}, {"smtp.example.test", "invalid"},
	} {
		s := &EmailService{fromEmail: "confirm@example.test"}
		s.SetSender(NewSMTPEmailSender(tc.host, tc.port, "", ""))
		require.ErrorIs(t, s.CheckConfirmationConfiguration(), ErrSMTPNotConfigured)
	}
	// Only inspect local configuration; never invoke this real transport.
	s := &EmailService{fromEmail: "confirm@example.test"}
	s.SetSender(NewSMTPEmailSender("smtp.example.test", "587", "", ""))
	require.NoError(t, s.CheckConfirmationConfiguration())
}

func TestConfirmationEmailExplicitMockConfiguration(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	t.Setenv("SMTP_ADDRESS", "")
	t.Setenv("ENV", "production")
	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_MOCK_EMAIL", "false")
	t.Setenv("MAILER_SENDER_EMAIL", "confirm@example.test")
	s := NewEmailService(nil)
	require.ErrorIs(t, s.CheckConfirmationConfiguration(), ErrSMTPNotConfigured)
	for _, setting := range []string{"ALLOW_MOCK_EMAIL", "ENV", "APP_ENV"} {
		t.Run(setting, func(t *testing.T) {
			value := "test"
			if setting == "ALLOW_MOCK_EMAIL" {
				value = "true"
			}
			t.Setenv(setting, value)
			s := NewEmailService(nil)
			require.NoError(t, s.CheckConfirmationConfiguration())
			state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
			require.NoError(t, err)
			require.Equal(t, "captured_mock", state)
			s.SetAllowMock(false)
			require.ErrorIs(t, s.CheckConfirmationConfiguration(), ErrSMTPNotConfigured)
		})
	}
	s.SetAllowMock(true)
	require.NoError(t, s.CheckConfirmationConfiguration())
}

func TestConfirmationEmailPreservesTrustedURLAndEscapesHTML(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test:8443/customer&team/'quoted'%20name/")
	t.Setenv("APP_URL", "https://untrusted.example.test")
	s, mock := confirmationTestEmailService()
	s.SetFromEmail("ExChat Confirmation <confirm@example.test>")
	expiry := time.Now().Add(time.Hour).In(time.FixedZone("other-zone", -7*60*60))
	state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, expiry)
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
	emails := mock.GetSentEmails()
	require.Len(t, emails, 1)
	email := emails[0]
	require.Equal(t, "confirm@example.test", email.From)
	require.Equal(t, "user@example.test", email.To)
	require.Contains(t, email.Subject, "ExChat")
	var confirmationURL string
	for _, line := range strings.Split(email.TextBody, "\n") {
		if strings.HasPrefix(line, "https://") {
			confirmationURL = line
		}
	}
	require.NotEmpty(t, confirmationURL)
	u, err := url.Parse(confirmationURL)
	require.NoError(t, err)
	require.Equal(t, "https", u.Scheme)
	require.Equal(t, "example.test:8443", u.Host)
	require.Equal(t, "/customer&team/'quoted' name/auth/confirmation", u.Path)
	require.Equal(t, url.Values{"confirmation_token": {confirmationTestEmailToken}}, u.Query())
	require.Empty(t, u.Fragment)
	require.NotContains(t, email.Subject, confirmationTestEmailToken)
	require.Contains(t, email.HTMLBody, `href="`+html.EscapeString(confirmationURL)+`"`)
	require.Contains(t, email.HTMLBody, "&amp;team/")
	require.Contains(t, email.HTMLBody, html.EscapeString(confirmationTestEmailUser().Name))
	require.NotContains(t, email.HTMLBody, "<script>")
	for _, body := range []string{email.HTMLBody, email.TextBody} {
		require.Contains(t, body, "仅可使用一次")
		require.Contains(t, body, expiry.UTC().Format("2006-01-02 15:04:05 UTC"))
		require.NotContains(t, body, "untrusted.example.test")
	}
}

func TestConfirmationEmailRejectsInvalidInput(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	now := time.Now()
	for _, tc := range []struct {
		name      string
		recipient string
		token     string
		expiry    time.Time
	}{
		{"empty recipient", "", confirmationTestEmailToken, now.Add(time.Hour)},
		{"invalid recipient", "not-an-email", confirmationTestEmailToken, now.Add(time.Hour)},
		{"header injection", "user@example.test\r\nBcc:evil@example.test", confirmationTestEmailToken, now.Add(time.Hour)},
		{"recipient list", "user@example.test, evil@example.test", confirmationTestEmailToken, now.Add(time.Hour)},
		{"display name", "User <user@example.test>", confirmationTestEmailToken, now.Add(time.Hour)},
		{"recipient whitespace", " user@example.test", confirmationTestEmailToken, now.Add(time.Hour)},
		{"recipient control", "user@example.test\x00", confirmationTestEmailToken, now.Add(time.Hour)},
		{"empty token", "user@example.test", "", now.Add(time.Hour)},
		{"whitespace token", "user@example.test", "  " + confirmationTestEmailToken + "  ", now.Add(time.Hour)},
		{"control token", "user@example.test", strings.Repeat("a", 63) + "\n", now.Add(time.Hour)},
		{"short token", "user@example.test", strings.Repeat("a", 63), now.Add(time.Hour)},
		{"long token", "user@example.test", strings.Repeat("a", 65), now.Add(time.Hour)},
		{"uppercase token", "user@example.test", strings.ToUpper(confirmationTestEmailToken), now.Add(time.Hour)},
		{"nonhex token", "user@example.test", strings.Repeat("z", 64), now.Add(time.Hour)},
		{"unicode token", "user@example.test", strings.Repeat("a", 62) + "é", now.Add(time.Hour)},
		{"legacy token", "user@example.test", "sample_token_abc_123", now.Add(time.Hour)},
		{"zero expiry", "user@example.test", confirmationTestEmailToken, time.Time{}},
		{"expired", "user@example.test", confirmationTestEmailToken, now.Add(-time.Second)},
		{"immediate expiry", "user@example.test", confirmationTestEmailToken, now},
		{"excessive expiry", "user@example.test", confirmationTestEmailToken, now.Add(25 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := confirmationTestEmailService()
			state, err := s.SendConfirmationEmail(context.Background(), &domain.User{Email: tc.recipient}, tc.token, tc.expiry)
			require.Error(t, err)
			require.Empty(t, state)
			require.Empty(t, mock.GetSentEmails())
		})
	}
	s, mock := confirmationTestEmailService()
	state, err := s.SendConfirmationEmail(context.Background(), nil, confirmationTestEmailToken, now.Add(time.Hour))
	require.EqualError(t, err, "confirmation recipient address is invalid")
	require.Empty(t, state)
	state, err = s.SendConfirmationEmail(nil, confirmationTestEmailUser(), confirmationTestEmailToken, now.Add(time.Hour))
	require.EqualError(t, err, "confirmation email requires a context")
	require.Empty(t, state)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, err = s.SendConfirmationEmail(ctx, confirmationTestEmailUser(), confirmationTestEmailToken, now.Add(time.Hour))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, state)
	ctx, cancel = context.WithDeadline(context.Background(), now.Add(-time.Second))
	defer cancel()
	state, err = s.SendConfirmationEmail(ctx, confirmationTestEmailUser(), confirmationTestEmailToken, now.Add(time.Hour))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, state)
	require.Empty(t, mock.GetSentEmails())
	state, err = s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
}

// This transport double is local-only; no test sends to a real provider.
type confirmationRecordingEmailSender struct {
	err       error
	calls     int
	sensitive bool
}

func (s *confirmationRecordingEmailSender) Send(ctx context.Context, _, _, _, _, _ string) error {
	s.calls++
	s.sensitive = ctx.Value(sensitiveEmailContextKey{}) == true
	return s.err
}

func confirmationEmailTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.EmailLog{}))
	return db
}

func TestConfirmationEmailDoesNotPersistOrExposeSensitiveErrors(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	db := confirmationEmailTestDB(t)
	s, mock := confirmationTestEmailService()
	s.db = db
	state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
	require.Len(t, mock.GetSentEmails(), 1)
	sender := &confirmationRecordingEmailSender{}
	s.SetSender(sender)
	state, err = s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "submitted", state, "provider acceptance is not confirmed mailbox delivery")
	require.True(t, sender.sensitive)
	leakyErr := errors.New("provider accepted body but connection lost: " + confirmationTestEmailToken)
	sender.err = leakyErr
	state, err = s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
	require.Equal(t, "delivery_unknown", state)
	require.EqualError(t, err, "confirmation email delivery outcome is unknown")
	require.NotContains(t, err.Error(), confirmationTestEmailToken)
	require.False(t, errors.Is(err, leakyErr), "callers must not unwrap sensitive provider errors")
	require.Equal(t, 2, sender.calls, "ambiguous sends must not be retried automatically")
	s.SetSender(nil)
	state, err = s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
	require.ErrorIs(t, err, ErrSMTPNotConfigured)
	require.Empty(t, state)
	var count int64
	require.NoError(t, db.Model(&domain.EmailLog{}).Count(&count).Error)
	require.Zero(t, count, "no successful, failed, or rejected confirmation may enter audit/retry storage")
}

func TestConfirmationEmailRedactsBuiltInSenderLogs(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	s, mock := confirmationTestEmailService()
	mock.SetFailError(errors.New("smtp echoed confirmation body " + confirmationTestEmailToken))
	state, err := s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
	require.Equal(t, "delivery_unknown", state)
	require.EqualError(t, err, "confirmation email delivery outcome is unknown")
	require.Contains(t, logs.String(), "email delivery failed")
	require.NotContains(t, logs.String(), confirmationTestEmailToken)
	require.NotContains(t, logs.String(), "smtp echoed confirmation body")
	require.Empty(t, mock.GetSentEmails())
	mock.SetFailError(nil)
	state, err = s.SendConfirmationEmail(context.Background(), confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
	require.NotContains(t, logs.String(), confirmationTestEmailToken)
	require.NotContains(t, logs.String(), "confirmation_token")
}

func TestConfirmationEmailLegacyLogsAreNeverRetried(t *testing.T) {
	db := confirmationEmailTestDB(t)
	s, mock := confirmationTestEmailService()
	s.db = db
	past := time.Now().Add(-time.Minute)
	legacy := domain.EmailLog{AccountID: 7, EmailType: "confirmation", ToEmail: "legacy@example.test", FromEmail: "confirm@example.test", Subject: "legacy", ContentHTML: "<p>" + confirmationTestEmailToken + "</p>", ContentText: confirmationTestEmailToken, Status: "failed", DeliveryStatus: domain.EmailDeliveryStatusFailed, MaxRetries: 3, NextRetryAt: &past}
	require.NoError(t, db.Create(&legacy).Error)
	updated, sent, err := s.RetryFailedEmail(context.Background(), legacy.AccountID, legacy.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.Nil(t, updated)
	require.False(t, sent)
	require.Empty(t, mock.GetSentEmails())
	count, err := s.RetryFailedEmails(context.Background(), 10)
	require.NoError(t, err)
	require.Zero(t, count)
	require.Empty(t, mock.GetSentEmails())
	ordinary := domain.EmailLog{AccountID: 7, EmailType: "transcript", ToEmail: "ordinary@example.test", FromEmail: "support@example.test", Subject: "transcript", ContentText: "ordinary content", Status: "failed", DeliveryStatus: domain.EmailDeliveryStatusFailed, MaxRetries: 3, NextRetryAt: &past}
	require.NoError(t, db.Create(&ordinary).Error)
	count, err = s.RetryFailedEmails(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, count, "excluded legacy rows must not consume the batch limit")
	emails := mock.GetSentEmails()
	require.Len(t, emails, 1)
	require.Equal(t, "ordinary@example.test", emails[0].To)
	require.NotContains(t, emails[0].HTMLBody+emails[0].TextBody, confirmationTestEmailToken)
	var unchanged domain.EmailLog
	require.NoError(t, db.First(&unchanged, legacy.ID).Error)
	require.Equal(t, "failed", unchanged.Status)
	require.Zero(t, unchanged.RetryCount)
	require.Nil(t, unchanged.LastAttemptAt)
	ordinary.ID = 0
	ordinary.ToEmail = "exact@example.test"
	require.NoError(t, db.Create(&ordinary).Error)
	updated, sent, err = s.RetryFailedEmail(context.Background(), ordinary.AccountID, ordinary.ID)
	require.NoError(t, err)
	require.True(t, sent)
	require.Equal(t, "sent", updated.Status)
	require.Len(t, mock.GetSentEmails(), 2)
}

// The confirmation exclusion must not change retry eligibility for older
// ordinary logs whose email_type column was left NULL.
func TestConfirmationRetryExclusionPreservesLegacyNullType(t *testing.T) {
	for _, exact := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact=%t", exact), func(t *testing.T) {
			db := confirmationEmailTestDB(t)
			s, mock := confirmationTestEmailService()
			s.db = db
			row := domain.EmailLog{AccountID: 7, EmailType: "transcript", ToEmail: "ordinary@example.test", FromEmail: "sender@example.test", Subject: "ordinary", ContentText: "ordinary body", Status: "failed", DeliveryStatus: "failed", MaxRetries: 3}
			require.NoError(t, db.Create(&row).Error)
			require.NoError(t, db.Model(&row).UpdateColumn("email_type", nil).Error)
			if exact {
				_, sent, err := s.RetryFailedEmail(context.Background(), row.AccountID, row.ID)
				require.NoError(t, err)
				require.True(t, sent)
			} else {
				count, err := s.RetryFailedEmails(context.Background(), 1)
				require.NoError(t, err)
				require.Equal(t, 1, count)
			}
			require.Len(t, mock.GetSentEmails(), 1)
		})
	}
}
