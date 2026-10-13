package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/stretchr/testify/require"
)

const ordinarySMTPProviderEcho = "private-provider-echo-from-body-and-credentials"

type ordinarySMTPBehavior struct {
	rejectAt string
	code     int
	dropAt   string
	onData   func()
	badTLS   bool
}

type ordinarySMTPFixture struct {
	done     chan struct{}
	mu       sync.Mutex
	conn     net.Conn
	closed   bool
	commands []string
	body     string
	err      error
}

func newOrdinarySMTPFixture(t *testing.T, behavior ordinarySMTPBehavior) (*SMTPEmailSender, *ordinarySMTPFixture) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	require.NoError(t, listener.SetDeadline(time.Now().Add(5*time.Second)))
	f := &ordinarySMTPFixture{done: make(chan struct{})}
	t.Cleanup(func() {
		_ = listener.Close()
		f.mu.Lock()
		f.closed = true
		if f.conn != nil {
			_ = f.conn.Close()
		}
		f.mu.Unlock()
		f.wait(t)
	})
	go func() {
		defer close(f.done)
		conn, err := listener.Accept()
		if err != nil {
			f.err = err
			return
		}
		defer conn.Close()
		f.mu.Lock()
		f.conn = conn
		closed := f.closed
		f.mu.Unlock()
		if closed {
			return
		}
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			f.err = err
			return
		}
		f.err = f.serve(conn, behavior)
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return NewSMTPEmailSender(host, port, "", ""), f
}

func (f *ordinarySMTPFixture) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary loopback SMTP fixture did not stop")
	}
}

func (f *ordinarySMTPFixture) serve(conn net.Conn, behavior ordinarySMTPBehavior) error {
	write := func(response string) error { _, err := io.WriteString(conn, response+"\r\n"); return err }
	if behavior.dropAt == "banner" {
		return nil
	}
	if behavior.rejectAt == "banner" {
		return write(fmt.Sprintf("%d %s", behavior.code, ordinarySMTPProviderEcho))
	}
	if err := write("220 local ordinary SMTP ready"); err != nil {
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
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return errors.New("empty ordinary SMTP fixture command")
		}
		stage := strings.ToLower(fields[0])
		if stage == "data" && behavior.onData != nil {
			behavior.onData()
		}
		if behavior.dropAt == stage {
			return nil
		}
		if behavior.rejectAt == stage {
			return write(fmt.Sprintf("%d %s", behavior.code, ordinarySMTPProviderEcho))
		}
		switch stage {
		case "ehlo", "helo":
			if behavior.badTLS {
				err = write("250-loopback\r\n250 STARTTLS")
			} else {
				err = write("250-loopback\r\n250 AUTH PLAIN")
			}
		case "starttls":
			if err := write("220 begin TLS"); err != nil {
				return err
			}
			var clientHelloHeader [5]byte
			if _, err := io.ReadFull(conn, clientHelloHeader[:]); err != nil {
				return err
			}
			if err := write("this is not TLS"); err != nil {
				return err
			}
			// Read the ClientHello until the client closes. Closing immediately
			// with unread input could cause RST and hide the malformed TLS record.
			_, _ = io.Copy(io.Discard, conn)
			return nil
		case "auth":
			err = write("235 authenticated")
		case "mail", "rcpt":
			err = write("250 accepted")
		case "data":
			if err := write("354 send message"); err != nil {
				return err
			}
			var body strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				if line == ".\r\n" {
					break
				}
				if strings.HasPrefix(line, "..") {
					line = line[1:]
				}
				body.WriteString(line)
			}
			f.body = body.String()
			if behavior.dropAt == "data-ack" {
				return nil
			}
			if behavior.rejectAt == "data-ack" {
				return write(fmt.Sprintf("%d %s", behavior.code, ordinarySMTPProviderEcho))
			}
			err = write("250 queued")
		case "quit":
			return write("221 goodbye")
		default:
			return errors.New("unexpected ordinary SMTP fixture command")
		}
		if err != nil {
			return err
		}
	}
}

func TestOrdinarySMTPSubmissionEvidenceAndRedaction(t *testing.T) {
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	for _, tc := range []struct {
		name     string
		behavior ordinarySMTPBehavior
		want     OrdinarySMTPOutcome
		hook     bool
	}{
		{"accepted", ordinarySMTPBehavior{}, OrdinarySMTPAccepted, true},
		{"quit-lost", ordinarySMTPBehavior{dropAt: "quit"}, OrdinarySMTPAccepted, true},
		{"quit-rejected", ordinarySMTPBehavior{rejectAt: "quit", code: 550}, OrdinarySMTPAccepted, true},
		{"lost-ack-after-body", ordinarySMTPBehavior{dropAt: "data-ack"}, OrdinarySMTPUnknown, true},
		{"data-command-lost", ordinarySMTPBehavior{dropAt: "data"}, OrdinarySMTPUnknown, true},
		{"data-transient", ordinarySMTPBehavior{rejectAt: "data", code: 451}, OrdinarySMTPUnknown, true},
		{"data-permanent", ordinarySMTPBehavior{rejectAt: "data", code: 550}, OrdinarySMTPUnknown, true},
		{"body-transient", ordinarySMTPBehavior{rejectAt: "data-ack", code: 451}, OrdinarySMTPUnknown, true},
		{"body-permanent", ordinarySMTPBehavior{rejectAt: "data-ack", code: 550}, OrdinarySMTPUnknown, true},
		{"greeting-transient", ordinarySMTPBehavior{rejectAt: "banner", code: 421}, OrdinarySMTPPreSubmissionTransient, false},
		{"greeting-permanent", ordinarySMTPBehavior{rejectAt: "banner", code: 554}, OrdinarySMTPPermanentFailure, false},
		{"greeting-lost", ordinarySMTPBehavior{dropAt: "banner"}, OrdinarySMTPPreSubmissionTransient, false},
		{"mail-transient", ordinarySMTPBehavior{rejectAt: "mail", code: 450}, OrdinarySMTPPreSubmissionTransient, false},
		{"mail-permanent", ordinarySMTPBehavior{rejectAt: "mail", code: 550}, OrdinarySMTPPermanentFailure, false},
		{"recipient-transient", ordinarySMTPBehavior{rejectAt: "rcpt", code: 451}, OrdinarySMTPPreSubmissionTransient, false},
		{"recipient-permanent", ordinarySMTPBehavior{rejectAt: "rcpt", code: 550}, OrdinarySMTPPermanentFailure, false},
		{"auth-transient-stops", ordinarySMTPBehavior{rejectAt: "auth", code: 454}, OrdinarySMTPAuthenticationFailure, false},
		{"auth-permanent-stops", ordinarySMTPBehavior{rejectAt: "auth", code: 535}, OrdinarySMTPAuthenticationFailure, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hookCalls atomic.Int32
			var sawDurableMarker atomic.Bool
			tc.behavior.onData = func() { sawDurableMarker.Store(hookCalls.Load() == 1) }
			sender, fixture := newOrdinarySMTPFixture(t, tc.behavior)
			sender.Username, sender.Password = "disposable-local-user", "disposable-local-password"
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := sender.SendOrdinary(ctx, "from@example.test", "to@example.test", "ordinary", ordinarySMTPProviderEcho, ordinarySMTPProviderEcho, func() error {
				hookCalls.Add(1)
				return nil
			})
			require.Equal(t, tc.want, result.Outcome)
			fixture.wait(t)
			require.NoError(t, fixture.err)
			if tc.hook {
				require.EqualValues(t, 1, hookCalls.Load())
				require.True(t, sawDurableMarker.Load(), "DATA must follow the durable hook")
			} else {
				require.Zero(t, hookCalls.Load())
				require.NotContains(t, fixture.commands, "DATA")
			}
			if tc.behavior.dropAt == "data-ack" {
				require.Contains(t, fixture.body, ordinarySMTPProviderEcho, "the entire body was received even though the acknowledgement was lost")
			}
			require.NotContains(t, fmt.Sprintf("%+v", result), ordinarySMTPProviderEcho)
			require.Empty(t, logs.String(), "ordinary transport must not log payload, destination, or provider errors")
		})
	}
}

func TestOrdinarySMTPFailedDurableHookNeverSendsDATA(t *testing.T) {
	sender, fixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	hookCalls := 0
	result := sender.SendOrdinary(ctx, "from@example.test", "to@example.test", "ordinary", "hello", "hello", func() error {
		hookCalls++
		return errors.New(ordinarySMTPProviderEcho)
	})
	require.Equal(t, OrdinarySMTPCancelledBeforeSubmission, result.Outcome)
	require.Equal(t, 1, hookCalls)
	fixture.wait(t)
	require.ErrorIs(t, fixture.err, io.EOF)
	require.Contains(t, fixture.commands, "RCPT TO:<to@example.test>")
	require.NotContains(t, fixture.commands, "DATA")
	require.Empty(t, fixture.body)
}

func TestOrdinarySMTPCancellationAfterDurableHookIsUnknown(t *testing.T) {
	sender, fixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := sender.SendOrdinary(ctx, "from@example.test", "to@example.test", "ordinary", "hello", "hello", func() error {
		cancel()
		return nil
	})
	require.Equal(t, OrdinarySMTPUnknown, result.Outcome)
	fixture.wait(t)
	require.NotContains(t, fixture.commands, "DATA")
}

func TestOrdinarySMTPBoundedCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		for _, stage := range []string{"banner", "ehlo", "auth", "mail", "rcpt", "data", "data-ack", "quit", "starttls", "tls-handshake", "implicit-tls"} {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{stallAt: stage, startTLS: stage == "starttls" || stage == "tls-handshake"})
				if stage == "auth" {
					sender.Username, sender.Password = "disposable-local-user", "disposable-local-password"
				}
				ctx, cancel := context.WithCancel(context.Background())
				if mode == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
				}
				defer cancel()
				resultC := make(chan OrdinarySMTPResult, 1)
				go func() {
					if stage == "implicit-tls" {
						resultC <- sender.sendOrdinarySMTP(ctx, "from@example.test", "to@example.test", []byte("test"), true, func() error { return nil })
					} else {
						resultC <- sender.SendOrdinary(ctx, "from@example.test", "to@example.test", "ordinary", "hello", "hello", func() error { return nil })
					}
				}()
				select {
				case <-fixture.reached:
				case result := <-resultC:
					t.Fatalf("returned before %s: %s", stage, result.Outcome)
				case <-time.After(2 * time.Second):
					t.Fatalf("did not reach %s", stage)
				}
				if mode == "cancel" {
					cancel()
				}
				want := OrdinarySMTPCancelledBeforeSubmission
				if stage == "data" || stage == "data-ack" {
					want = OrdinarySMTPUnknown
				} else if stage == "quit" {
					want = OrdinarySMTPAccepted
				}
				select {
				case result := <-resultC:
					require.Equal(t, want, result.Outcome)
				case <-time.After(2 * time.Second):
					t.Fatal("ordinary SMTP did not stop after cancellation/deadline")
				}
			})
		}
	}
}

func TestOrdinarySMTPBoundedBodyWriteIsUnknown(t *testing.T) {
	sender, fixture := newConfirmationSMTPFixture(t, confirmationSMTPBehavior{stallAt: "data-write"})
	// Payload preparation is not the transport operation under deadline.
	body := []byte(strings.Repeat("x", 16<<20))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	result := sender.sendOrdinarySMTP(ctx, "from@example.test", "to@example.test", body, false, func() error { return nil })
	require.Equal(t, OrdinarySMTPUnknown, result.Outcome)
	require.Less(t, time.Since(start), 2*time.Second)
	select {
	case <-fixture.reached:
	default:
		t.Fatal("SMTP did not reach the DATA-write stall after the 354 response")
	}
}

func TestOrdinarySMTPRejectsInvalidHeadersBeforeNetwork(t *testing.T) {
	sender := NewSMTPEmailSender("127.0.0.1", "1", "", "")
	for _, field := range []string{"from", "to", "subject"} {
		for _, control := range []string{"\r\nBcc: injected@example.test", "\n", "\r", "\x00", "\t", "\x7f", "\u0085", string([]byte{0xff})} {
			t.Run(field+"/"+fmt.Sprintf("%x", control), func(t *testing.T) {
				from, to, subject := "from@example.test", "to@example.test", "ordinary"
				switch field {
				case "from":
					from += control
				case "to":
					to += control
				case "subject":
					subject += control
				}
				result := sender.SendOrdinary(context.Background(), from, to, subject, "hello", "hello", func() error {
					t.Fatal("invalid headers reached submission hook")
					return nil
				})
				require.Equal(t, OrdinarySMTPPermanentFailure, result.Outcome)
			})
		}
	}
	for _, address := range []string{"", "invalid", "a@example.test,b@example.test"} {
		result := sender.SendOrdinary(context.Background(), address, "to@example.test", "ordinary", "hello", "hello", func() error { return nil })
		require.Equal(t, OrdinarySMTPPermanentFailure, result.Outcome)
	}
	result := sender.SendOrdinary(context.Background(), "from@example.test", "to@example.test", "ordinary", "hello", "hello", nil)
	require.Equal(t, OrdinarySMTPPermanentFailure, result.Outcome)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = sender.SendOrdinary(ctx, "from@example.test", "to@example.test", "ordinary", "hello", "hello", func() error { return nil })
	require.Equal(t, OrdinarySMTPCancelledBeforeSubmission, result.Outcome)
	result = sender.SendOrdinary(nil, "from@example.test", "to@example.test", "ordinary", "hello", "hello", func() error { return nil })
	require.Equal(t, OrdinarySMTPCancelledBeforeSubmission, result.Outcome)
}

func TestOrdinarySMTPMIMEAndEnvelope(t *testing.T) {
	sender, fixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	subject := "更新, a subject"
	textBody := "A line\n.\nUnicode: 更新\n" + strings.Repeat("long ", 50)
	htmlBody := "<p>更新 &amp; message</p>"
	result := sender.SendOrdinary(ctx, `"Sender, Name" <from@example.test>`, `受信者 <to@example.test>`, subject, htmlBody, textBody, func() error { return nil })
	require.Equal(t, OrdinarySMTPAccepted, result.Outcome)
	fixture.wait(t)
	require.NoError(t, fixture.err)
	require.Contains(t, fixture.commands, "MAIL FROM:<from@example.test>")
	require.Contains(t, fixture.commands, "RCPT TO:<to@example.test>")
	message, err := mail.ReadMessage(strings.NewReader(fixture.body))
	require.NoError(t, err)
	decodedSubject, err := (&mime.WordDecoder{}).DecodeHeader(message.Header.Get("Subject"))
	require.NoError(t, err)
	require.Equal(t, subject, decodedSubject)
	from, err := message.Header.AddressList("From")
	require.NoError(t, err)
	require.Equal(t, "Sender, Name", from[0].Name)
	contentType, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/alternative", contentType)
	reader := multipart.NewReader(message.Body, parameters["boundary"])
	for _, want := range []string{textBody, htmlBody} {
		part, err := reader.NextRawPart()
		require.NoError(t, err)
		body, err := io.ReadAll(quotedprintable.NewReader(part))
		require.NoError(t, err)
		require.Equal(t, want, strings.ReplaceAll(string(body), "\r\n", "\n"))
	}
	_, err = reader.NextPart()
	require.ErrorIs(t, err, io.EOF)
}

func TestOrdinarySMTPRequiresTLSForRemoteHostPolicy(t *testing.T) {
	for _, host := range []string{"smtp.remote.example.test", "localhost.example.test", "localhost.", "10.0.0.1", "0.0.0.0"} {
		t.Run(host, func(t *testing.T) {
			sender, fixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(sender.Host, sender.Port))
			require.NoError(t, err)
			defer conn.Close()
			deadline, _ := ctx.Deadline()
			require.NoError(t, conn.SetDeadline(deadline))
			sender.Host = host
			sender.Username, sender.Password = "disposable-local-user", "disposable-local-password"
			result := sender.submitOrdinarySMTP(ctx, conn, "from@example.test", "to@example.test", []byte("hello"), false, func() error {
				t.Fatal("plaintext remote host reached submission hook")
				return nil
			})
			require.Equal(t, OrdinarySMTPPermanentFailure, result.Outcome)
			fixture.wait(t)
			require.Equal(t, []string{"EHLO localhost"}, fixture.commands)
			require.Empty(t, fixture.body)
		})
	}
}

func TestOrdinarySMTPDoesNotDowngradeFailedSTARTTLS(t *testing.T) {
	sender, fixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{badTLS: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := sender.SendOrdinary(ctx, "from@example.test", "to@example.test", "ordinary", "hello", "hello", func() error {
		t.Fatal("failed STARTTLS reached submission hook")
		return nil
	})
	require.Equal(t, OrdinarySMTPPermanentFailure, result.Outcome)
	fixture.wait(t)
	require.Equal(t, []string{"EHLO localhost", "STARTTLS"}, fixture.commands)
	require.Empty(t, fixture.body)
}
