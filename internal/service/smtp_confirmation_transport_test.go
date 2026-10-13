package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/stretchr/testify/require"
)

// Every server in these tests listens only on an ephemeral loopback port.
// Each connection and goroutine is closed and joined during cleanup.
type confirmationSMTPFixture struct {
	listener net.Listener
	mu       sync.Mutex
	conn     net.Conn
	release  chan struct{}
	reached  chan struct{}
	done     chan struct{}
	commands []string
	body     string
	err      error
}

type confirmationSMTPBehavior struct {
	stallAt  string
	rejectAt string
	startTLS bool
}

func newConfirmationSMTPFixture(t *testing.T, behavior confirmationSMTPBehavior) (*SMTPEmailSender, *confirmationSMTPFixture) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fixture := &confirmationSMTPFixture{listener: listener, release: make(chan struct{}), reached: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(fixture.done)
		conn, err := listener.Accept()
		if err != nil {
			fixture.err = err
			return
		}
		fixture.mu.Lock()
		fixture.conn = conn
		fixture.mu.Unlock()
		defer conn.Close()
		fixture.err = fixture.serve(conn, behavior)
	}()
	t.Cleanup(func() {
		close(fixture.release)
		_ = listener.Close()
		fixture.mu.Lock()
		if fixture.conn != nil {
			_ = fixture.conn.Close()
		}
		fixture.mu.Unlock()
		select {
		case <-fixture.done:
		case <-time.After(2 * time.Second):
			t.Error("loopback SMTP fixture did not stop")
		}
	})
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return NewSMTPEmailSender(host, port, "", ""), fixture
}

func (f *confirmationSMTPFixture) serve(conn net.Conn, behavior confirmationSMTPBehavior) error {
	write := func(response string) error { _, err := io.WriteString(conn, response+"\r\n"); return err }
	// Deliberately stop reading and writing. Cleanup releases the fake server;
	// only the client's deadline or cancellation can unblock client submission.
	stall := func(stage string) bool {
		if behavior.stallAt != stage {
			return false
		}
		close(f.reached)
		<-f.release
		return true
	}
	if behavior.stallAt == "implicit-tls" {
		// Implicit TLS must begin before reading any plaintext SMTP banner.
		var header [5]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return err
		}
		if header[0] != 22 || header[1] != 3 {
			return errors.New("implicit SMTP did not begin with a TLS handshake")
		}
		f.commands = append(f.commands, "TLS ClientHello")
		stall("implicit-tls")
		return nil
	}
	if stall("banner") {
		return nil
	}
	if err := write("220 loopback SMTP ready"); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		f.commands = append(f.commands, line)
		stage := strings.ToLower(strings.Fields(line)[0])
		if stall(stage) {
			return nil
		}
		if behavior.rejectAt == stage {
			return write("550 provider echoed credential " + confirmationTestEmailToken)
		}
		switch stage {
		case "ehlo", "helo":
			if behavior.startTLS {
				err = write("250-loopback\r\n250 STARTTLS")
			} else {
				err = write("250-loopback\r\n250 AUTH PLAIN")
			}
		case "starttls":
			if err = write("220 begin TLS"); err != nil {
				return err
			}
			if stall("tls-handshake") {
				return nil
			}
			// No fixture disables certificate verification. A server claiming
			// STARTTLS but speaking plaintext must be rejected, never downgraded.
			_, err = io.WriteString(conn, "this is not TLS\r\n")
			return err
		case "auth":
			err = write("235 authenticated")
		case "mail", "rcpt":
			err = write("250 accepted")
		case "data":
			if err = write("354 send message"); err != nil {
				return err
			}
			if stall("data-write") {
				return nil
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
			f.body = body.String()
			if stall("data-ack") {
				return nil
			}
			if behavior.rejectAt == "data-ack" {
				return write("550 provider echoed credential " + confirmationTestEmailToken)
			}
			err = write("250 queued")
		case "quit":
			return write("221 goodbye")
		default:
			return fmt.Errorf("unexpected loopback SMTP command: %s", stage)
		}
		if err != nil {
			return err
		}
	}
}

func confirmationSMTPService(t *testing.T, sender *SMTPEmailSender) *EmailService {
	t.Helper()
	t.Setenv("FRONTEND_URL", "https://example.test")
	s := &EmailService{fromEmail: "confirm@example.test"}
	s.SetSender(sender)
	return s
}

func TestConfirmationSMTPAcceptedSubmission(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(fmt.Sprintf("auth=%v", authenticated), func(t *testing.T) {
			sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{})
			if authenticated {
				sender.Username = "loopback-test-user"
				sender.Password = "loopback-test-password"
			}
			s := confirmationSMTPService(t, sender)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			state, err := s.SendConfirmationEmail(ctx, confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
			require.NoError(t, err)
			require.Equal(t, "submitted", state)
			select {
			case <-fixture.done:
			case <-time.After(time.Second):
				t.Fatal("SMTP submission did not finish")
			}
			require.NoError(t, fixture.err)
			require.Contains(t, fixture.commands, "MAIL FROM:<confirm@example.test>")
			require.Contains(t, fixture.commands, "RCPT TO:<user@example.test>")
			require.Equal(t, "QUIT", fixture.commands[len(fixture.commands)-1])
			require.Contains(t, fixture.body, "confirmation_token="+confirmationTestEmailToken)
			require.Contains(t, fixture.body, "Content-Type: multipart/alternative")
		})
	}
}

func TestConfirmationSMTPCancellationClosesEveryProtocolStage(t *testing.T) {
	for _, stage := range []string{"banner", "ehlo", "auth", "mail", "rcpt", "data", "data-ack", "quit", "starttls", "tls-handshake"} {
		t.Run(stage, func(t *testing.T) {
			behavior := confirmationSMTPBehavior{stallAt: stage, startTLS: stage == "starttls" || stage == "tls-handshake"}
			sender, fixture := newConfirmationSMTPFixture(t, behavior)
			if stage == "auth" {
				sender.Username = "loopback-test-user"
				sender.Password = "loopback-test-password"
			}
			s := confirmationSMTPService(t, sender)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				state string
				err   error
			}
			resultC := make(chan result, 1)
			go func() {
				state, err := s.SendConfirmationEmail(ctx, confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
				resultC <- result{state, err}
			}()
			select {
			case <-fixture.reached:
			case got := <-resultC:
				t.Fatalf("submission returned before %s: %s / %v", stage, got.state, got.err)
			case <-time.After(2 * time.Second):
				cancel()
				<-resultC
				t.Fatalf("SMTP did not reach %s", stage)
			}
			cancel()
			select {
			case got := <-resultC:
				require.Equal(t, "delivery_unknown", got.state)
				require.EqualError(t, got.err, "confirmation email delivery outcome is unknown")
			case <-time.After(2 * time.Second):
				t.Fatal("caller cancellation did not stop synchronous SMTP")
			}
		})
	}
}

func TestConfirmationSMTPDeadlineBoundsBannerDataAndQuit(t *testing.T) {
	for _, stage := range []string{"banner", "data", "data-ack", "quit", "tls-handshake"} {
		t.Run(stage, func(t *testing.T) {
			sender, _ := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{stallAt: stage, startTLS: stage == "tls-handshake"})
			s := confirmationSMTPService(t, sender)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			state, err := s.SendConfirmationEmail(ctx, confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
			require.Less(t, time.Since(start), 2*time.Second)
			require.Equal(t, "delivery_unknown", state)
			require.EqualError(t, err, "confirmation email delivery outcome is unknown")
		})
	}
}

func TestConfirmationSMTPBoundsLargeDataWrite(t *testing.T) {
	sender, _ := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{stallAt: "data-write"})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := sender.sendSensitiveSMTP(ctx, "from@example.test", "to@example.test", []byte(strings.Repeat("x", 16<<20)), false)
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
	require.NotContains(t, err.Error(), strings.Repeat("x", 64))
}

func TestConfirmationSMTPBoundsImplicitTLSHandshake(t *testing.T) {
	sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{stallAt: "implicit-tls"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	// Production selects this branch for port 465; an ephemeral test port
	// avoids binding a well-known port or needing a real SMTP service.
	err := sender.sendSensitiveSMTP(ctx, "from@example.test", "to@example.test", []byte("test"), true)
	require.Error(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
	select {
	case <-fixture.reached:
		require.Contains(t, fixture.commands, "TLS ClientHello")
	default:
		t.Fatal("implicit TLS handshake was not observed")
	}
}

func TestConfirmationSMTPDoesNotDowngradeFailedSTARTTLS(t *testing.T) {
	sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{startTLS: true})
	s := confirmationSMTPService(t, sender)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := s.SendConfirmationEmail(ctx, confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
	require.Equal(t, "delivery_unknown", state)
	require.EqualError(t, err, "confirmation email delivery outcome is unknown")
	<-fixture.done
	require.Contains(t, fixture.commands, "STARTTLS")
	require.Empty(t, fixture.body)
	for _, command := range fixture.commands {
		require.NotContains(t, command, "MAIL FROM")
	}
}

func TestConfirmationSMTPRejectsCanceledContextBeforeConnecting(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		}
		cancel()
		sender := NewSMTPEmailSender("127.0.0.1", "1", "", "")
		ctx = context.WithValue(ctx, sensitiveEmailContextKey{}, true)
		err := sender.Send(ctx, "from@example.test", "to@example.test", "test", "test", "test")
		require.ErrorIs(t, err, want)
	}
}

func TestConfirmationSMTPNeverLeaksProviderEchoOrClaimsRejectedMailWasSubmitted(t *testing.T) {
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	for _, stage := range []string{"mail", "rcpt", "data", "data-ack", "quit"} {
		t.Run(stage, func(t *testing.T) {
			sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{rejectAt: stage})
			s := confirmationSMTPService(t, sender)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			state, err := s.SendConfirmationEmail(ctx, confirmationTestEmailUser(), confirmationTestEmailToken, time.Now().Add(time.Hour))
			require.Equal(t, "delivery_unknown", state)
			require.EqualError(t, err, "confirmation email delivery outcome is unknown")
			require.NotContains(t, err.Error(), confirmationTestEmailToken)
			require.NotContains(t, logs.String(), confirmationTestEmailToken)
			require.NotContains(t, logs.String(), "provider echoed")
			<-fixture.done
			require.NoError(t, fixture.err)
		})
	}
	// The transport itself also removes provider errors for direct callers.
	sender, _ := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{rejectAt: "data-ack"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := sender.sendSensitiveSMTP(ctx, "from@example.test", "to@example.test", []byte(confirmationTestEmailToken), false)
	require.ErrorIs(t, err, errSensitiveSMTPOutcome)
	require.False(t, errors.Is(err, context.DeadlineExceeded))
	require.NotContains(t, err.Error(), confirmationTestEmailToken)
}

func TestConfirmationSMTPPlaintextDevelopmentException(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "127.0.0.1", "127.0.0.2", "127.255.255.255", "::1", "::ffff:127.0.0.1"} {
		require.True(t, sensitiveSMTPAllowsPlaintext(host), host)
	}
	for _, host := range []string{"", "smtp.example.test", "localhost.example.test", "localhost.", "localhost.localdomain", "127.0.0.1.example.test", "127.000.000.001", " localhost", "localhost ", "localhost\n", "0.0.0.0", "::", "192.0.2.1", "10.0.0.1", "::ffff:192.0.2.1", "[::1]", "::1%lo", "2130706433"} {
		require.False(t, sensitiveSMTPAllowsPlaintext(host), host)
	}
}

func TestConfirmationSMTPRequiresTLSBeforeCredentialsForRemotePolicyHost(t *testing.T) {
	for _, host := range []string{"smtp.production.example.test", "localhost.example.test", "localhost.", "192.0.2.1", "0.0.0.0"} {
		t.Run(host, func(t *testing.T) {
			sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			// Dial only the fixture's verified loopback address. Then exercise
			// the production protocol path with a nonloopback policy hostname;
			// no production option can override where sendSensitiveSMTP dials.
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", fixture.listener.Addr().String())
			require.NoError(t, err)
			defer conn.Close()
			deadline, _ := ctx.Deadline()
			require.NoError(t, conn.SetDeadline(deadline))
			sender.Host = host
			require.Empty(t, sender.Username, "unauthenticated delivery still requires encryption")
			err = sender.submitSensitiveSMTP(ctx, conn, "from@example.test", "to@example.test", []byte(confirmationTestEmailToken), false)
			require.ErrorIs(t, err, errSensitiveSMTPOutcome)
			select {
			case <-fixture.done:
			case <-time.After(time.Second):
				t.Fatal("rejected plaintext connection was not closed")
			}
			require.ErrorIs(t, fixture.err, io.EOF)
			require.Equal(t, []string{"EHLO localhost"}, fixture.commands, "no AUTH, MAIL, RCPT, or DATA may be sent before TLS")
			require.Empty(t, fixture.body)
			require.NotContains(t, strings.Join(fixture.commands, "\n"), confirmationTestEmailToken)
		})
	}
}
