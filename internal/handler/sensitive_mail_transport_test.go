package handler_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This fixture uses only an ephemeral loopback listener. It accepts additional
// connections so an accidental automatic retry is observable, and joins its
// server goroutine and closes every accepted connection during cleanup.
type sensitiveMailSMTPFixture struct {
	listener net.Listener
	mu       sync.Mutex
	conn     net.Conn
	closed   bool
	attempts int
	commands []string
	bodies   []string
	reached  chan struct{}
	finished chan error
	done     chan struct{}
}

func newSensitiveMailSMTPFixture(t *testing.T, behavior string) (*service.SMTPEmailSender, *sensitiveMailSMTPFixture) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	f := &sensitiveMailSMTPFixture{listener: listener, reached: make(chan struct{}, 8), finished: make(chan error, 8), done: make(chan struct{})}
	go func() {
		defer close(f.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			if f.closed {
				f.mu.Unlock()
				_ = conn.Close()
				return
			}
			f.conn = conn
			f.attempts++
			f.mu.Unlock()
			// A test failure must not leave a fixture blocked indefinitely.
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			err = f.serve(conn, behavior)
			_ = conn.Close()
			f.finished <- err
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		f.mu.Lock()
		f.closed = true
		if f.conn != nil {
			_ = f.conn.Close()
		}
		f.mu.Unlock()
		select {
		case <-f.done:
		case <-time.After(2 * time.Second):
			t.Error("loopback SMTP fixture did not stop")
		}
	})
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return service.NewSMTPEmailSender(host, port, "", ""), f
}

func (f *sensitiveMailSMTPFixture) serve(conn net.Conn, behavior string) error {
	write := func(line string) error { _, err := io.WriteString(conn, line+"\r\n"); return err }
	reader := bufio.NewReader(conn)
	stall := func() error {
		f.reached <- struct{}{}
		// The server deliberately omits its reply. The client must close its
		// connection on deadline/cancellation, including after DATA acceptance.
		_, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("stalled SMTP connection was not closed by client: %v", err)
	}
	if err := write("220 loopback SMTP ready"); err != nil {
		return err
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return errors.New("empty SMTP command")
		}
		command := strings.ToLower(fields[0])
		f.mu.Lock()
		f.commands = append(f.commands, command)
		f.mu.Unlock()
		switch command {
		case "ehlo", "helo":
			err = write("250 loopback")
		case "mail", "rcpt":
			err = write("250 accepted")
		case "data":
			if err = write("354 send message"); err != nil {
				return err
			}
			var body strings.Builder
			for {
				line, err = reader.ReadString('\n')
				if err != nil {
					return err
				}
				if line == ".\r\n" {
					break
				}
				body.WriteString(line)
			}
			f.mu.Lock()
			f.bodies = append(f.bodies, body.String())
			f.mu.Unlock()
			switch behavior {
			case "lost-data-ack":
				return nil
			case "cancel-data-ack", "deadline-data-ack":
				return stall()
			case "provider-echo":
				token := regexp.MustCompile(`[0-9a-f]{64}`).FindString(body.String())
				return write("550 provider-echo-sentinel " + token)
			}
			err = write("250 queued")
		case "quit":
			if behavior == "cancel-quit" || behavior == "deadline-quit" {
				return stall()
			}
			return write("221 goodbye")
		default:
			return fmt.Errorf("unexpected SMTP command %q", command)
		}
		if err != nil {
			return err
		}
	}
}

func (f *sensitiveMailSMTPFixture) snapshot() (int, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts, append([]string(nil), f.commands...), append([]string(nil), f.bodies...)
}

type sensitiveMailHTTPFixture struct {
	kind       string
	router     http.Handler
	db         *gorm.DB
	emails     *service.EmailService
	resetUser  *domain.User
	invitation *invitationHTTPFixture
}

func setupSensitiveMailHTTP(t *testing.T, kind string) *sensitiveMailHTTPFixture {
	t.Helper()
	f := &sensitiveMailHTTPFixture{kind: kind}
	// Pin all sender configuration to test data even if the shell has mail
	// credentials. Each test replaces the sender before invoking a handler.
	t.Setenv("MAILER_SENDER_EMAIL", "transport@support.example.test")
	if kind == "reset" {
		f.router, f.db, f.resetUser, f.emails, _, _ = setupPasswordReset(t)
	} else {
		f.invitation = setupInvitationHTTP(t)
		f.router, f.db, f.emails = f.invitation.router, f.invitation.db, f.invitation.emails
	}
	return f
}

func (f *sensitiveMailHTTPFixture) request(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
	path, recipient, token := "/auth/password", "reset@example.test", ""
	if f.invitation != nil {
		path = fmt.Sprintf("/api/v1/accounts/%d/agents", f.invitation.account.ID)
		recipient, token = "new-transport@invite.test", f.invitation.adminToken
	}
	body, err := json.Marshal(map[string]string{"email": recipient, "role": domain.RoleAgent})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func (f *sensitiveMailHTTPFixture) rawToken(t *testing.T, body string) string {
	t.Helper()
	if f.kind == "reset" {
		return resetTokenFromEmail(t, service.SentEmail{TextBody: body})
	}
	return invitationTokenFromEmail(t, service.SentEmail{TextBody: body})
}

func (f *sensitiveMailHTTPFixture) assertOutcome(t *testing.T, result *httptest.ResponseRecorder, raw string, accepted bool) {
	t.Helper()
	require.Len(t, raw, 64)
	require.NotContains(t, result.Body.String(), raw)
	require.NotContains(t, result.Body.String(), "provider-echo-sentinel")
	if f.kind == "reset" {
		require.Equal(t, http.StatusOK, result.Code, result.Body.String())
		unknown := performMFARequest(t, f.router, http.MethodPost, "/auth/password", map[string]string{"email": "absent@example.test"}, "")
		require.Equal(t, unknown.Body.String(), result.Body.String(), "delivery must not change the anti-enumeration response")
		var row domain.PasswordResetToken
		require.NoError(t, f.db.First(&row).Error)
		digest, err := repository.PasswordResetTokenDigest(raw)
		require.NoError(t, err)
		require.Equal(t, digest, row.Token)
		require.NotEqual(t, raw, row.Token)
		require.Equal(t, accepted, row.IssuedAt != nil)
		require.False(t, row.Used)
		reset := performMFARequest(t, f.router, http.MethodPut, "/auth/password", map[string]string{"token": raw, "password": "NewTransportPassword123!"}, "")
		var user domain.User
		require.NoError(t, f.db.First(&user, f.resetUser.ID).Error)
		if accepted {
			require.Equal(t, http.StatusOK, reset.Code, reset.Body.String())
			require.True(t, auth.CheckPasswordHash("NewTransportPassword123!", user.PasswordHash))
		} else {
			require.Equal(t, http.StatusBadRequest, reset.Code, reset.Body.String())
			require.Equal(t, f.resetUser.PasswordHash, user.PasswordHash)
		}
		return
	}
	var row domain.AgentInvitation
	require.NoError(t, f.db.First(&row).Error)
	digest, err := repository.AgentInvitationTokenDigest(raw)
	require.NoError(t, err)
	require.Equal(t, digest, row.TokenDigest)
	require.NotEqual(t, raw, row.TokenDigest)
	require.NotContains(t, result.Body.String(), row.TokenDigest)
	require.Nil(t, row.ConsumedAt)
	preview := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/preview", map[string]string{"token": raw}, "")
	activation := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "")
	var identities, memberships int64
	require.NoError(t, f.db.Model(&domain.User{}).Where("email = ?", "new-transport@invite.test").Count(&identities).Error)
	require.NoError(t, f.db.Model(&domain.AccountUser{}).Where("account_id = ?", row.AccountID).Count(&memberships).Error)
	if accepted {
		require.Equal(t, http.StatusAccepted, result.Code, result.Body.String())
		require.Equal(t, "submitted", row.DeliveryState)
		require.Contains(t, result.Body.String(), `"delivery_state":"submitted"`)
		require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
		require.Equal(t, http.StatusOK, activation.Code, activation.Body.String())
		require.EqualValues(t, 1, identities)
		require.EqualValues(t, 2, memberships)
	} else {
		require.Equal(t, http.StatusBadGateway, result.Code, result.Body.String())
		require.Equal(t, "delivery_unknown", row.DeliveryState)
		require.Contains(t, result.Body.String(), `"delivery_state":"delivery_unknown"`)
		require.Equal(t, http.StatusBadRequest, preview.Code, preview.Body.String())
		require.Equal(t, http.StatusBadRequest, activation.Code, activation.Body.String())
		require.Zero(t, identities)
		require.EqualValues(t, 1, memberships, "only the original administrator retains membership")
	}
}

func (f *sensitiveMailHTTPFixture) assertNoRetry(t *testing.T) {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Model(&domain.EmailLog{}).Count(&count).Error)
	require.Zero(t, count, "credential bodies and provider errors must never enter EmailLog")
	retried, err := f.emails.RetryFailedEmails(context.Background(), 10)
	require.NoError(t, err)
	require.Zero(t, retried, "there must be no queued secret-bearing email to retry")
}

func captureSensitiveMailLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	logger.SetOutput(logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	return logs
}

func TestSensitiveMailHTTPRealSMTPBoundedLifecycle(t *testing.T) {
	for _, kind := range []string{"reset", "invitation"} {
		for _, behavior := range []string{"success", "lost-data-ack", "deadline-data-ack", "cancel-data-ack", "deadline-quit", "cancel-quit", "provider-echo"} {
			t.Run(kind+"/"+behavior, func(t *testing.T) {
				f := setupSensitiveMailHTTP(t, kind)
				sender, smtp := newSensitiveMailSMTPFixture(t, behavior)
				f.emails.SetSender(sender)
				logs := captureSensitiveMailLogs(t)
				timeout := 2 * time.Second
				if strings.HasPrefix(behavior, "deadline-") {
					timeout = 600 * time.Millisecond
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				request, result := f.request(t, ctx), httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); f.router.ServeHTTP(result, request) }()
				t.Cleanup(func() {
					cancel()
					smtp.mu.Lock()
					if smtp.conn != nil {
						_ = smtp.conn.Close()
					}
					smtp.mu.Unlock()
					select {
					case <-done:
					case <-time.After(2 * time.Second):
						t.Error("HTTP handler goroutine did not stop")
					}
				})
				if strings.HasPrefix(behavior, "cancel-") || strings.HasPrefix(behavior, "deadline-") {
					select {
					case <-smtp.reached:
					case <-time.After(time.Second):
						t.Fatal("SMTP did not reach the selected blocking stage")
					}
					if strings.HasPrefix(behavior, "cancel-") {
						cancel()
					}
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("HTTP handler remained blocked after SMTP deadline/cancellation")
				}
				select {
				case err := <-smtp.finished:
					require.NoError(t, err)
				case <-time.After(time.Second):
					t.Fatal("SMTP connection was not closed after handler completion")
				}
				attempts, commands, bodies := smtp.snapshot()
				require.Equal(t, 1, attempts)
				require.Len(t, bodies, 1)
				require.Contains(t, commands, "data")
				if behavior == "success" || strings.HasSuffix(behavior, "quit") {
					require.Contains(t, commands, "quit", "test must exercise the stage after DATA was accepted")
				}
				raw := f.rawToken(t, bodies[0])
				f.assertOutcome(t, result, raw, behavior == "success")
				f.assertNoRetry(t)
				afterRetry, _, _ := smtp.snapshot()
				require.Equal(t, attempts, afterRetry)
				require.NotContains(t, logs.String(), raw)
				require.NotContains(t, logs.String(), "provider-echo-sentinel")
			})
		}
	}
}

type sensitiveMailLateSender struct {
	cancel  context.CancelFunc
	calls   int
	text    string
	bounded bool
}

func (s *sensitiveMailLateSender) Send(ctx context.Context, _, _, _, _, text string) error {
	s.calls++
	s.text = text
	deadline, ok := ctx.Deadline()
	s.bounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 30*time.Second
	if s.cancel != nil {
		s.cancel()
	}
	<-ctx.Done()
	// An injected sender can incorrectly report success after its caller has
	// stopped waiting. The service must reject that result before activation.
	return nil
}

func TestSensitiveMailHTTPRejectsLateSenderSuccess(t *testing.T) {
	for _, kind := range []string{"reset", "invitation"} {
		for _, stoppedBy := range []string{"cancellation", "deadline"} {
			t.Run(kind+"/"+stoppedBy, func(t *testing.T) {
				f := setupSensitiveMailHTTP(t, kind)
				logs := captureSensitiveMailLogs(t)
				var ctx context.Context
				var cancel context.CancelFunc
				sender := &sensitiveMailLateSender{}
				if stoppedBy == "cancellation" {
					ctx, cancel = context.WithCancel(context.Background())
					sender.cancel = cancel
				} else {
					ctx, cancel = context.WithTimeout(context.Background(), 500*time.Millisecond)
				}
				defer cancel()
				f.emails.SetSender(sender)
				result := httptest.NewRecorder()
				f.router.ServeHTTP(result, f.request(t, ctx))
				require.Error(t, ctx.Err())
				require.True(t, sender.bounded, "injected senders also receive the bounded service context")
				require.Equal(t, 1, sender.calls)
				raw := f.rawToken(t, sender.text)
				f.assertOutcome(t, result, raw, false)
				f.assertNoRetry(t)
				require.Equal(t, 1, sender.calls)
				require.NotContains(t, logs.String(), raw)
			})
		}
	}
}
