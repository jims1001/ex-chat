package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestLegacySMTPAuthPolicyMatchesSendMail(t *testing.T) {
	for _, mode := range []string{"helo_only", "ehlo_without_auth", "ehlo_with_auth"} {
		t.Run(mode, func(t *testing.T) {
			var traces [][]string
			var failures []bool
			for _, contextual := range []bool{false, true} {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { _ = listener.Close() })
				trace := make(chan []string, 1)
				go func() {
					commands := []string{}
					defer func() { trace <- commands }()
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
					r := bufio.NewReader(conn)
					_, _ = io.WriteString(conn, "220 localhost\r\n")
					for {
						line, err := r.ReadString('\n')
						if err != nil {
							return
						}
						command := strings.Fields(line)[0]
						commands = append(commands, command)
						switch command {
						case "EHLO":
							if mode == "helo_only" {
								_, _ = io.WriteString(conn, "500 unsupported\r\n")
							} else if mode == "ehlo_with_auth" {
								_, _ = io.WriteString(conn, "250-localhost\r\n250 AUTH PLAIN\r\n")
							} else {
								_, _ = io.WriteString(conn, "250 localhost\r\n")
							}
						case "HELO", "MAIL", "RCPT":
							_, _ = io.WriteString(conn, "250 OK\r\n")
						case "AUTH":
							_, _ = io.WriteString(conn, "235 accepted\r\n")
						case "DATA":
							_, _ = io.WriteString(conn, "354 body\r\n")
							for {
								line, err = r.ReadString('\n')
								if err != nil {
									return
								}
								if line == ".\r\n" {
									break
								}
							}
							_, _ = io.WriteString(conn, "250 accepted\r\n")
						case "QUIT":
							_, _ = io.WriteString(conn, "221 bye\r\n")
							return
						default:
							return
						}
					}
				}()
				host, port, _ := net.SplitHostPort(listener.Addr().String())
				message := []byte("Subject: compatibility\r\n\r\nbody\r\n")
				if contextual {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					err = NewSMTPEmailSender(host, port, "test-user", "test-password").sendLegacySMTPContext(ctx, "from@example.test", "to@example.test", message, false)
					cancel()
				} else {
					err = smtp.SendMail(listener.Addr().String(), smtp.PlainAuth("", "test-user", "test-password", host), "from@example.test", []string{"to@example.test"}, message)
				}
				failures = append(failures, err != nil)
				select {
				case commands := <-trace:
					traces = append(traces, commands)
				case <-time.After(5 * time.Second):
					t.Fatal("SMTP peer did not exit")
				}
				_ = listener.Close()
			}
			require.Equal(t, mode == "ehlo_without_auth", failures[0])
			require.Equal(t, failures[0], failures[1])
			require.Equal(t, traces[0], traces[1], "legacy EHLO/HELO, AUTH and submission policy changed")
		})
	}
}

func TestOrdinarySMTPCancellationInterruptsBlockedSocket(t *testing.T) {
	for _, phase := range []string{"greeting", "implicit_tls_handshake", "starttls_handshake", "data_ack", "quit"} {
		t.Run(phase, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			ready := make(chan struct{})
			peerDone := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					peerDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(conn)
				if phase != "greeting" && phase != "implicit_tls_handshake" {
					_, _ = io.WriteString(conn, "220 lifecycle-test\r\n")
					for {
						line, readErr := r.ReadString('\n')
						if readErr != nil {
							peerDone <- readErr
							return
						}
						switch {
						case strings.HasPrefix(line, "EHLO "):
							if phase == "starttls_handshake" {
								_, _ = io.WriteString(conn, "250-localhost\r\n250 STARTTLS\r\n")
							} else {
								_, _ = io.WriteString(conn, "250 localhost\r\n")
							}
						case strings.HasPrefix(line, "STARTTLS"):
							_, _ = io.WriteString(conn, "220 ready\r\n")
							goto blocked
						case strings.HasPrefix(line, "MAIL "), strings.HasPrefix(line, "RCPT "):
							_, _ = io.WriteString(conn, "250 OK\r\n")
						case line == "DATA\r\n":
							_, _ = io.WriteString(conn, "354 body\r\n")
							for {
								line, readErr = r.ReadString('\n')
								if readErr != nil {
									peerDone <- readErr
									return
								}
								if line == ".\r\n" {
									break
								}
							}
							if phase == "data_ack" {
								goto blocked
							}
							_, _ = io.WriteString(conn, "250 accepted\r\n")
						case line == "QUIT\r\n":
							goto blocked
						default:
							peerDone <- fmt.Errorf("unexpected command %q", line)
							return
						}
					}
				}
			blocked:
				close(ready)
				// Cancellation must close the peer, not detach a blocked sender.
				_, err = io.Copy(io.Discard, r)
				peerDone <- err
			}()
			t.Cleanup(func() { cancel(); _ = listener.Close() })
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			s := NewSMTPEmailSender(host, port, "", "")
			done := make(chan error, 1)
			go func() {
				done <- s.sendLegacySMTPContext(ctx, "from@example.test", "to@example.test", []byte("Subject: lifecycle\r\n\r\nbody\r\n"), phase == "implicit_tls_handshake")
			}()
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("SMTP did not reach blocking phase")
			}
			cancel()
			select {
			case err = <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("SMTP did not join after cancellation")
			}
			select {
			case err = <-peerDone:
				require.True(t, err == nil || errors.Is(err, syscall.ECONNRESET), "expected closed socket, got %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("SMTP connection remained open")
			}
		})
	}
}

type cancellingRetrySender struct {
	cancel context.CancelFunc
	calls  int
}

func (s *cancellingRetrySender) Send(ctx context.Context, _, _, _, _, _ string) error {
	s.calls++
	s.cancel()
	return ctx.Err()
}

func TestEmailRetryCancellationPreservesUnattemptedRows(t *testing.T) {
	db := confirmationEmailTestDB(t)
	s, _ := confirmationTestEmailService()
	s.db = db
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender := &cancellingRetrySender{cancel: cancel}
	s.SetSender(sender)
	rows := []domain.EmailLog{}
	for i := 0; i < 3; i++ {
		rows = append(rows, domain.EmailLog{AccountID: 7, EmailType: "transcript", ToEmail: "to@example.test", FromEmail: "from@example.test", Status: "failed", DeliveryStatus: domain.EmailDeliveryStatusFailed, MaxRetries: 1})
	}
	require.NoError(t, db.Create(&rows).Error)
	count, err := s.RetryFailedEmails(ctx, 10)
	require.True(t, errors.Is(err, context.Canceled))
	require.Zero(t, count)
	require.Equal(t, 1, sender.calls)
	var saved []domain.EmailLog
	require.NoError(t, db.Order("id").Find(&saved).Error)
	require.Len(t, saved, 3)
	require.Equal(t, 1, saved[0].RetryCount, "already attempted row retains failure accounting")
	for _, row := range saved[1:] {
		require.Zero(t, row.RetryCount)
		require.Nil(t, row.LastAttemptAt)
		require.Empty(t, row.Error)
	}
}

func TestEmailRetrySMTPJoinsBlockedGreetingOnCancellation(t *testing.T) {
	db := confirmationEmailTestDB(t)
	s, _ := confirmationTestEmailService()
	s.db = db
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = listener.Close() })
	accepted := make(chan struct{})
	peerDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		close(accepted)
		_, err = io.Copy(io.Discard, conn)
		peerDone <- err
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	s.SetSender(NewSMTPEmailSender(host, port, "", ""))
	row := domain.EmailLog{AccountID: 7, EmailType: "transcript", ToEmail: "to@example.test", FromEmail: "from@example.test", Status: "failed", DeliveryStatus: domain.EmailDeliveryStatusFailed, MaxRetries: 3}
	require.NoError(t, db.Create(&row).Error)
	done := make(chan error, 1)
	go func() { _, err := s.RetryFailedEmails(ctx, 10); done <- err }()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not connect to SMTP")
	}
	cancel()
	select {
	case err = <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("retry remained blocked after cancellation")
	}
	select {
	case err = <-peerDone:
		require.True(t, err == nil || errors.Is(err, syscall.ECONNRESET))
	case <-time.After(5 * time.Second):
		t.Fatal("retry socket remained open")
	}
	require.NoError(t, db.First(&row, row.ID).Error)
	require.Equal(t, 1, row.RetryCount)
	require.Equal(t, "failed", row.Status)
	require.NotEmpty(t, row.Error)
}
