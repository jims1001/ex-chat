package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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

// Exercise each public credential-mail service, so a forgotten context marker
// cannot silently put either message back on ordinary SMTP's unbounded path.
func sendSensitiveTestMail(ctx context.Context, s *EmailService, kind string) (string, error) {
	if kind == "reset" {
		err := s.SendPasswordResetEmail(ctx, "user@example.test", confirmationTestEmailToken, time.Now().Add(time.Hour))
		if err != nil {
			return "delivery_unknown", err
		}
		// This is only a test adapter; reset's public contract remains error-only.
		return "submitted", nil
	}
	return s.SendAgentInvitationEmail(ctx, "user@example.test", confirmationTestEmailToken, 42, time.Now().Add(time.Hour))
}

func requireSensitiveMailFailure(t *testing.T, kind, state string, err error) {
	t.Helper()
	require.Equal(t, "delivery_unknown", state)
	if kind == "reset" {
		require.EqualError(t, err, "password reset email delivery failed")
	} else {
		require.EqualError(t, err, "agent invitation email delivery outcome is unknown")
	}
}

func waitSensitiveSMTPFixture(t *testing.T, fixture *confirmationSMTPFixture) {
	t.Helper()
	select {
	case <-fixture.done:
	case <-time.After(2 * time.Second):
		t.Fatal("loopback SMTP fixture did not finish")
	}
}

func TestSensitiveSMTPAcceptedSubmission(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	for _, kind := range []string{"reset", "invitation"} {
		for _, authenticated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/auth=%v", kind, authenticated), func(t *testing.T) {
				sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{})
				if authenticated {
					sender.Username, sender.Password = "local-test-user", "local-test-password"
				}
				s := confirmationSMTPService(t, sender)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				state, err := sendSensitiveTestMail(ctx, s, kind)
				require.NoError(t, err)
				require.Equal(t, "submitted", state)
				waitSensitiveSMTPFixture(t, fixture)
				require.NoError(t, fixture.err)
				require.Equal(t, "QUIT", fixture.commands[len(fixture.commands)-1])
				require.Contains(t, fixture.body, confirmationTestEmailToken)
				require.Equal(t, 1, strings.Count(strings.Join(fixture.commands, "\n"), "MAIL FROM:"))
			})
		}
	}
}

func TestSensitiveSMTPStopsAtEveryProtocolStage(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	for _, kind := range []string{"reset", "invitation"} {
		for _, mode := range []string{"cancel", "deadline"} {
			for _, stage := range []string{"banner", "ehlo", "auth", "mail", "rcpt", "data", "data-ack", "quit", "starttls", "tls-handshake"} {
				t.Run(kind+"/"+mode+"/"+stage, func(t *testing.T) {
					sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{stallAt: stage, startTLS: stage == "starttls" || stage == "tls-handshake"})
					if stage == "auth" {
						sender.Username, sender.Password = "local-test-user", "local-test-password"
					}
					s := confirmationSMTPService(t, sender)
					ctx, cancel := context.WithCancel(context.Background())
					if mode == "deadline" {
						cancel()
						ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
					}
					defer cancel()
					type result struct {
						state string
						err   error
					}
					resultC := make(chan result, 1)
					done := make(chan struct{})
					go func() {
						defer close(done)
						state, err := sendSensitiveTestMail(ctx, s, kind)
						resultC <- result{state, err}
					}()
					t.Cleanup(func() {
						cancel()
						fixture.mu.Lock()
						if fixture.conn != nil {
							_ = fixture.conn.Close()
						}
						fixture.mu.Unlock()
						select {
						case <-done:
						case <-time.After(2 * time.Second):
							t.Error("SMTP caller did not stop during cleanup")
						}
					})
					select {
					case <-fixture.reached:
					case got := <-resultC:
						t.Fatalf("submission did not reach %s: %s / %v", stage, got.state, got.err)
					case <-time.After(2 * time.Second):
						cancel()
						t.Fatal("SMTP did not reach expected stage")
					}
					if mode == "cancel" {
						cancel()
					}
					select {
					case got := <-resultC:
						requireSensitiveMailFailure(t, kind, got.state, got.err)
					case <-time.After(2 * time.Second):
						t.Fatal("sensitive SMTP did not stop after cancellation/deadline")
					}
				})
			}
		}
	}
}

func TestSensitiveSMTPNoDowngradeOrProviderErrorLeak(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	for _, kind := range []string{"reset", "invitation"} {
		for _, stage := range []string{"mail", "rcpt", "data", "data-ack", "quit", "failed-starttls"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				behavior := confirmationSMTPBehavior{rejectAt: stage, startTLS: stage == "failed-starttls"}
				sender, fixture := newConfirmationSMTPFixture(t, behavior)
				s := confirmationSMTPService(t, sender)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				state, err := sendSensitiveTestMail(ctx, s, kind)
				requireSensitiveMailFailure(t, kind, state, err)
				waitSensitiveSMTPFixture(t, fixture)
				require.NoError(t, fixture.err)
				require.NotContains(t, logs.String(), confirmationTestEmailToken)
				require.NotContains(t, logs.String(), "provider echoed")
				if stage == "failed-starttls" {
					require.Equal(t, []string{"EHLO localhost", "STARTTLS"}, fixture.commands)
					require.Empty(t, fixture.body)
				}
			})
		}
	}
	// Direct SMTP callers using the sensitive marker receive a static error too.
	sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{rejectAt: "data-ack"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, sensitiveEmailContextKey{}, true)
	err := sender.Send(ctx, "from@example.test", "to@example.test", "credential", confirmationTestEmailToken, confirmationTestEmailToken)
	require.ErrorIs(t, err, errSensitiveSMTPOutcome)
	waitSensitiveSMTPFixture(t, fixture)
	require.NotContains(t, logs.String(), confirmationTestEmailToken)
}

func TestSensitiveSMTPRemotePolicyRejectsBeforeAuthOrBody(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(fmt.Sprintf("auth=%v", authenticated), func(t *testing.T) {
			sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", fixture.listener.Addr().String())
			require.NoError(t, err)
			defer conn.Close()
			deadline, _ := ctx.Deadline()
			require.NoError(t, conn.SetDeadline(deadline))
			sender.Host = "remote.example.test"
			if authenticated {
				sender.Username, sender.Password = "secret-local-username", "secret-local-password"
			}
			err = sender.submitSensitiveSMTP(ctx, conn, "from@example.test", "to@example.test", []byte(confirmationTestEmailToken), false)
			require.ErrorIs(t, err, errSensitiveSMTPOutcome)
			waitSensitiveSMTPFixture(t, fixture)
			require.ErrorIs(t, fixture.err, io.EOF)
			require.Equal(t, []string{"EHLO localhost"}, fixture.commands)
			require.Empty(t, fixture.body)
		})
	}
}

type sensitiveContextSender struct {
	check func(context.Context) error
	calls int
}

func (s *sensitiveContextSender) Send(ctx context.Context, _, _, _, _, _ string) error {
	s.calls++
	return s.check(ctx)
}

func TestSensitiveEmailDeadlineAndNoRetryContract(t *testing.T) {
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://example.test")
	for _, kind := range []string{"reset", "invitation"} {
		for _, outcome := range []string{"success", "error", "late-success"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				t.Cleanup(func() { _ = sqlDB.Close() })
				require.NoError(t, db.AutoMigrate(&domain.EmailLog{}))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				before := time.Now()
				sender := &sensitiveContextSender{check: func(ctx context.Context) error {
					require.Equal(t, true, ctx.Value(sensitiveEmailContextKey{}))
					deadline, ok := ctx.Deadline()
					require.True(t, ok)
					require.True(t, deadline.After(before))
					require.LessOrEqual(t, time.Until(deadline), 30*time.Second)
					if outcome == "error" {
						return errors.New("provider echoed " + confirmationTestEmailToken)
					}
					if outcome == "late-success" {
						cancel()
					}
					return nil
				}}
				s := &EmailService{db: db, fromEmail: "from@example.test"}
				s.SetSender(sender)
				state, err := sendSensitiveTestMail(ctx, s, kind)
				if outcome == "success" {
					require.NoError(t, err)
					require.Equal(t, "submitted", state)
				} else {
					requireSensitiveMailFailure(t, kind, state, err)
				}
				var count int64
				require.NoError(t, db.Model(&domain.EmailLog{}).Count(&count).Error)
				require.Zero(t, count)
				retried, err := s.RetryFailedEmails(context.Background(), 10)
				require.NoError(t, err)
				require.Zero(t, retried)
				require.Equal(t, 1, sender.calls, "no credential mail may be automatically retried")
			})
		}
	}
}

func TestSensitiveSMTPKeepsOrdinaryTransportUnchanged(t *testing.T) {
	sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Ordinary SMTP historically ignores caller cancellation. A completed local
	// submission with that context proves it did not enter the new sensitive path.
	require.NoError(t, sender.Send(ctx, "from@example.test", "to@example.test", "ordinary", "hello", "hello"))
	waitSensitiveSMTPFixture(t, fixture)
	require.NoError(t, fixture.err)
	require.Contains(t, fixture.body, "hello")
	require.Equal(t, "QUIT", fixture.commands[len(fixture.commands)-1])
}
