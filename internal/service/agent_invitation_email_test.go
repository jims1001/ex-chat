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

const agentInvitationTestEmailToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func agentInvitationTestEmailService() (*EmailService, *MockEmailSender) {
	mock := NewMockEmailSender()
	s := &EmailService{fromEmail: "invites@example.test"}
	s.SetSender(mock)
	return s, mock
}

func TestAgentInvitationEmailRequiresExplicitClientReadiness(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://example.test")
	t.Setenv("SMTP_ADDRESS", "smtp.example.test")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("MAILER_SENDER_EMAIL", "invites@example.test")
	for _, enabled := range []string{"", "false", "1", "TRUE", "True", "true ", " true", "true\n", "yes", "invalid-sensitive-value"} {
		t.Run("flag/"+enabled, func(t *testing.T) {
			t.Setenv("AGENT_INVITATIONS_ENABLED", enabled)
			// Even a configured SMTP provider is insufficient. Only inspect its
			// local configuration; no test invokes a real network sender.
			smtpService := NewEmailService(nil)
			require.True(t, smtpService.IsConfigured())
			require.ErrorIs(t, smtpService.CheckAgentInvitationConfiguration(), errAgentInvitationsDisabled)
			for _, useMock := range []bool{false, true} {
				s, mock := agentInvitationTestEmailService()
				localSender := &agentInvitationRecordingEmailSender{}
				if !useMock {
					s.SetSender(localSender)
				}
				require.ErrorIs(t, s.CheckAgentInvitationConfiguration(), errAgentInvitationsDisabled)
				state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
				require.ErrorIs(t, err, errAgentInvitationsDisabled)
				require.Contains(t, err.Error(), "agent invitations are unavailable")
				require.Contains(t, err.Error(), "supported invitation page")
				require.NotContains(t, err.Error(), "invalid-sensitive-value")
				require.Empty(t, state)
				require.Empty(t, mock.GetSentEmails())
				require.Zero(t, localSender.calls)
			}
		})
	}
	t.Run("unset", func(t *testing.T) {
		t.Setenv("AGENT_INVITATIONS_ENABLED", "restore-on-cleanup")
		require.NoError(t, os.Unsetenv("AGENT_INVITATIONS_ENABLED"))
		s, mock := agentInvitationTestEmailService()
		state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
		require.ErrorIs(t, err, errAgentInvitationsDisabled)
		require.Empty(t, state)
		require.Empty(t, mock.GetSentEmails())
	})
}

func TestAgentInvitationEmailConfiguration(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	valid := []string{
		"https://example.test", "https://EXAMPLE.test:8443/customer/app/",
		"https://example.test./customer", "https://192.0.2.1/customer",
		"https://[2001:db8::1]:8443/customer", "https://example.test/customer%20one",
		"http://localhost:3000", "http://LOCALHOST:3000/base/",
		"http://127.0.0.1:3000", "http://[::1]:3000/base",
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
			s, mock := agentInvitationTestEmailService()
			require.NoError(t, s.CheckAgentInvitationConfiguration())
			require.Empty(t, mock.GetSentEmails(), "preflight must not dispatch email")
		})
	}
	for _, base := range invalid {
		t.Run("invalid/"+base, func(t *testing.T) {
			t.Setenv("FRONTEND_URL", base)
			t.Setenv("APP_URL", "https://fallback.example.test")
			s, mock := agentInvitationTestEmailService()
			require.ErrorIs(t, s.CheckAgentInvitationConfiguration(), errAgentInvitationFrontendURL)
			state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
			require.ErrorIs(t, err, errAgentInvitationFrontendURL)
			require.Empty(t, state)
			require.Empty(t, mock.GetSentEmails())
		})
	}
}

func TestAgentInvitationEmailRequiresConfiguredSender(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test")
	var nilMock *MockEmailSender
	var nilSMTP *SMTPEmailSender
	var nilCustom *agentInvitationRecordingEmailSender
	for _, s := range []*EmailService{
		nil, {},
		{sender: NewMockEmailSender(), fromEmail: "invites@example.test"},
		{sender: NewMockEmailSender(), configured: true, fromEmail: "invites@example.test"},
		{sender: nilMock, allowMock: true, fromEmail: "invites@example.test"},
		{sender: nilSMTP, configured: true, fromEmail: "invites@example.test"},
		{sender: nilCustom, configured: true, fromEmail: "invites@example.test"},
		{sender: &agentInvitationRecordingEmailSender{}, allowMock: true, fromEmail: "invites@example.test"},
	} {
		require.ErrorIs(t, s.CheckAgentInvitationConfiguration(), ErrSMTPNotConfigured)
		state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
		require.ErrorIs(t, err, ErrSMTPNotConfigured)
		require.Empty(t, state)
	}
	s, mock := agentInvitationTestEmailService()
	for _, from := range []string{"", "invalid", "sender@example.test\r\nBcc: attacker@example.test", " sender@example.test", "sender@example.test\x00"} {
		s.fromEmail = from
		require.Error(t, s.CheckAgentInvitationConfiguration())
		state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
		require.Error(t, err)
		require.Empty(t, state)
	}
	require.Empty(t, mock.GetSentEmails())
	s.fromEmail = "ExChat Invitations <invites@example.test>"
	require.NoError(t, s.CheckAgentInvitationConfiguration())
	s.SetSender(nil)
	require.ErrorIs(t, s.CheckAgentInvitationConfiguration(), ErrSMTPNotConfigured)
}

func TestAgentInvitationEmailRejectsIncompleteSMTPWithoutNetwork(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test")
	for _, tc := range []struct{ host, port string }{
		{"", "587"}, {"smtp.example.test\r\n", "587"}, {"smtp://example.test", "587"},
		{"smtp.example.test", ""}, {"smtp.example.test", "0"}, {"smtp.example.test", "65536"}, {"smtp.example.test", "invalid"},
	} {
		s := &EmailService{fromEmail: "invites@example.test"}
		s.SetSender(NewSMTPEmailSender(tc.host, tc.port, "", ""))
		require.ErrorIs(t, s.CheckAgentInvitationConfiguration(), ErrSMTPNotConfigured)
	}
	// Configuration inspection is local and must never contact this hostname.
	s := &EmailService{fromEmail: "invites@example.test"}
	s.SetSender(NewSMTPEmailSender("smtp.example.test", "587", "", ""))
	require.NoError(t, s.CheckAgentInvitationConfiguration())
}

func TestAgentInvitationEmailExplicitMockConfiguration(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test")
	t.Setenv("SMTP_ADDRESS", "")
	t.Setenv("ENV", "production")
	t.Setenv("APP_ENV", "production")
	t.Setenv("ALLOW_MOCK_EMAIL", "false")
	t.Setenv("MAILER_SENDER_EMAIL", "invites@example.test")
	s := NewEmailService(nil)
	require.ErrorIs(t, s.CheckAgentInvitationConfiguration(), ErrSMTPNotConfigured)
	t.Setenv("ALLOW_MOCK_EMAIL", "true")
	s = NewEmailService(nil)
	require.NoError(t, s.CheckAgentInvitationConfiguration())
	state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
	s.SetAllowMock(false)
	require.ErrorIs(t, s.CheckAgentInvitationConfiguration(), ErrSMTPNotConfigured)
}

func TestAgentInvitationEmailUsesTrustedFragmentAndHonestMockOutcome(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test:8443/customer&team/'quoted'%20name/")
	t.Setenv("APP_URL", "https://untrusted.example.test")
	s, mock := agentInvitationTestEmailService()
	s.SetFromEmail("ExChat Invitations <invites@example.test>")
	expiry := time.Now().Add(time.Hour).In(time.FixedZone("other-zone", -7*60*60))
	state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, expiry)
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
	emails := mock.GetSentEmails()
	require.Len(t, emails, 1)
	email := emails[0]
	require.Equal(t, "invites@example.test", email.From)
	require.Equal(t, "user@example.test", email.To)
	require.Equal(t, "Your ExChat invitation", email.Subject)
	var invitationURL string
	for _, line := range strings.Split(email.TextBody, "\n") {
		if strings.HasPrefix(line, "https://") {
			invitationURL = line
		}
	}
	require.NotEmpty(t, invitationURL)
	u, err := url.Parse(invitationURL)
	require.NoError(t, err)
	require.Equal(t, "https", u.Scheme)
	require.Equal(t, "example.test:8443", u.Host)
	require.Equal(t, "/customer&team/'quoted' name/app/auth/invitation", u.Path)
	require.Empty(t, u.RawQuery)
	fragment, err := url.ParseQuery(u.Fragment)
	require.NoError(t, err)
	require.Equal(t, url.Values{"token": {agentInvitationTestEmailToken}, "account_id": {"42"}}, fragment)
	require.NotContains(t, u.RequestURI(), agentInvitationTestEmailToken, "credential must not reach a web server access log")
	require.NotContains(t, email.Subject, agentInvitationTestEmailToken)
	require.Contains(t, email.HTMLBody, `href="`+html.EscapeString(invitationURL)+`"`)
	require.Contains(t, email.HTMLBody, "&amp;team/")
	for _, body := range []string{email.HTMLBody, email.TextBody} {
		require.Contains(t, body, "sign in to accept")
		require.Contains(t, body, "activate your user account")
		require.Contains(t, body, "can be used once")
		require.Contains(t, body, expiry.UTC().Format("2006-01-02 15:04:05 UTC"))
		require.NotContains(t, body, "untrusted.example.test")
	}
}

func TestAgentInvitationEmailRejectsInvalidInput(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test")
	now := time.Now()
	for _, tc := range []struct {
		name      string
		recipient string
		token     string
		accountID uint
		expiry    time.Time
	}{
		{"empty recipient", "", agentInvitationTestEmailToken, 42, now.Add(time.Hour)},
		{"invalid recipient", "not-an-email", agentInvitationTestEmailToken, 42, now.Add(time.Hour)},
		{"header injection", "user@example.test\r\nBcc:evil@example.test", agentInvitationTestEmailToken, 42, now.Add(time.Hour)},
		{"recipient list", "user@example.test, evil@example.test", agentInvitationTestEmailToken, 42, now.Add(time.Hour)},
		{"display name", "User <user@example.test>", agentInvitationTestEmailToken, 42, now.Add(time.Hour)},
		{"empty token", "user@example.test", "", 42, now.Add(time.Hour)},
		{"short token", "user@example.test", strings.Repeat("a", 63), 42, now.Add(time.Hour)},
		{"long token", "user@example.test", strings.Repeat("a", 65), 42, now.Add(time.Hour)},
		{"oversized token", "user@example.test", strings.Repeat("a", 1025), 42, now.Add(time.Hour)},
		{"uppercase token", "user@example.test", strings.Repeat("A", 64), 42, now.Add(time.Hour)},
		{"nonhex token", "user@example.test", strings.Repeat("g", 64), 42, now.Add(time.Hour)},
		{"whitespace token", "user@example.test", " " + strings.Repeat("a", 63), 42, now.Add(time.Hour)},
		{"control token", "user@example.test", strings.Repeat("a", 63) + "\n", 42, now.Add(time.Hour)},
		{"url injection token", "user@example.test", strings.Repeat("a", 50) + "&role=admin1234", 42, now.Add(time.Hour)},
		{"zero account", "user@example.test", agentInvitationTestEmailToken, 0, now.Add(time.Hour)},
		{"zero expiry", "user@example.test", agentInvitationTestEmailToken, 42, time.Time{}},
		{"expired", "user@example.test", agentInvitationTestEmailToken, 42, now.Add(-time.Second)},
		{"immediate expiry", "user@example.test", agentInvitationTestEmailToken, 42, now},
		{"excessive expiry", "user@example.test", agentInvitationTestEmailToken, 42, now.Add(25 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := agentInvitationTestEmailService()
			state, err := s.SendAgentInvitationEmail(context.Background(), tc.recipient, tc.token, tc.accountID, tc.expiry)
			require.Error(t, err)
			require.Empty(t, state)
			require.Empty(t, mock.GetSentEmails())
		})
	}
	s, mock := agentInvitationTestEmailService()
	state, err := s.SendAgentInvitationEmail(nil, "user@example.test", agentInvitationTestEmailToken, 42, now.Add(time.Hour))
	require.Error(t, err)
	require.Empty(t, state)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, err = s.SendAgentInvitationEmail(ctx, "user@example.test", agentInvitationTestEmailToken, 42, now.Add(time.Hour))
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, state)
	require.Empty(t, mock.GetSentEmails())
	state, err = s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
}

// This is a local-only transport double, never a real provider implementation.
type agentInvitationRecordingEmailSender struct {
	err       error
	calls     int
	sensitive bool
}

func (s *agentInvitationRecordingEmailSender) Send(ctx context.Context, _, _, _, _, _ string) error {
	s.calls++
	s.sensitive = ctx.Value(sensitiveEmailContextKey{}) == true
	return s.err
}

func TestAgentInvitationEmailDoesNotPersistOrExposeSensitiveErrors(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.EmailLog{}))
	s, mock := agentInvitationTestEmailService()
	s.db = db
	state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
	require.Len(t, mock.GetSentEmails(), 1)
	sender := &agentInvitationRecordingEmailSender{}
	s.SetSender(sender)
	state, err = s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "submitted", state, "provider acceptance must not be called delivered")
	require.True(t, sender.sensitive)
	leakyErr := errors.New("provider accepted body but connection lost: " + agentInvitationTestEmailToken)
	sender.err = leakyErr
	state, err = s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
	require.Equal(t, "delivery_unknown", state)
	require.EqualError(t, err, "agent invitation email delivery outcome is unknown")
	require.NotContains(t, err.Error(), agentInvitationTestEmailToken)
	require.False(t, errors.Is(err, leakyErr), "callers must not unwrap sensitive sender errors")
	require.Equal(t, 2, sender.calls, "an ambiguous result must not automatically be retried")
	var count int64
	require.NoError(t, db.Model(&domain.EmailLog{}).Count(&count).Error)
	require.Zero(t, count, "neither successful nor failed invitation emails enter persisted audit/retry storage")
}

func TestAgentInvitationEmailRedactsBuiltInSenderLogs(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test")
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	s, mock := agentInvitationTestEmailService()
	mock.SetFailError(errors.New("smtp echoed invitation body " + agentInvitationTestEmailToken))
	state, err := s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
	require.Equal(t, "delivery_unknown", state)
	require.EqualError(t, err, "agent invitation email delivery outcome is unknown")
	require.Contains(t, logs.String(), "email delivery failed")
	require.NotContains(t, logs.String(), agentInvitationTestEmailToken)
	require.NotContains(t, logs.String(), "smtp echoed invitation body")
	require.Empty(t, mock.GetSentEmails())
	mock.SetFailError(nil)
	state, err = s.SendAgentInvitationEmail(context.Background(), "user@example.test", agentInvitationTestEmailToken, 42, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, "captured_mock", state)
	require.NotContains(t, logs.String(), agentInvitationTestEmailToken)
	require.NotContains(t, logs.String(), "/app/auth/invitation")
}
