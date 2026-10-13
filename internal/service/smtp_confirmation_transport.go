package service

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"net/smtp"
	"strings"
	"time"
)

const sensitiveEmailSendTimeout = 30 * time.Second

var errSensitiveSMTPOutcome = errors.New("sensitive SMTP delivery outcome is unknown")

// sendSensitiveSMTP uses implicit TLS on port 465 and STARTTLS elsewhere,
// and bounds the whole submission, not each individual step. Verified TLS is
// mandatory except for an explicit loopback host used during local development.
// The caller alone selects implicitTLS from its configured port. Keeping that
// selection separate also lets local tests exercise TLS on an ephemeral port.
// No background goroutine sends mail: cancellation only closes the socket that
// this synchronous call owns. Never expose a server response, which may echo the
// credential, even when called directly rather than through EmailService.
func (s *SMTPEmailSender) sendSensitiveSMTP(ctx context.Context, from, to string, message []byte, implicitTLS bool) (err error) {
	if ctx == nil {
		return errSensitiveSMTPOutcome
	}
	ctx, cancel := context.WithTimeout(ctx, sensitiveEmailSendTimeout)
	defer cancel()
	defer func() {
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = errSensitiveSMTPOutcome
			}
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline() // WithTimeout above always supplies a deadline.
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	return s.submitSensitiveSMTP(ctx, conn, from, to, message, implicitTLS)
}

// submitSensitiveSMTP receives an already bounded connection, allowing the
// network destination to be tested independently from the configured TLS host.
// Production always passes a connection dialed to s.Host by sendSensitiveSMTP.
func (s *SMTPEmailSender) submitSensitiveSMTP(ctx context.Context, conn net.Conn, from, to string, message []byte, implicitTLS bool) (err error) {
	var smtpConn net.Conn = conn
	if implicitTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.Host})
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		smtpConn = tlsConn
	}
	client, err := smtp.NewClient(smtpConn, s.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if !implicitTLS {
		// Hello surfaces protocol errors that Extension would otherwise hide.
		if err = client.Hello("localhost"); err != nil {
			return err
		}
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err = client.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
				return err
			}
		} else if !sensitiveSMTPAllowsPlaintext(s.Host) {
			// Reject before AUTH, MAIL, RCPT, or DATA: even without SMTP
			// credentials the body contains a bearer credential.
			return errSensitiveSMTPOutcome
		}
	}
	if s.Username != "" {
		if !implicitTLS {
			if supported, _ := client.Extension("AUTH"); !supported {
				return errSensitiveSMTPOutcome
			}
		}
		if err = client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err = client.Mail(from); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		return err
	}
	// Close reads the final DATA acknowledgement. A successful write alone
	// says nothing about whether the provider accepted the message.
	if err = writer.Close(); err != nil {
		return err
	}
	// Keep the existing conservative submission contract: DATA acceptance
	// alone does not make a proof active. If QUIT fails or the context expires,
	// the caller must treat the outcome as unknown even though mail may already
	// be queued. Never retry that secret-bearing message automatically.
	if err = client.Quit(); err != nil {
		return err
	}
	// Do not report a late response after the caller stopped waiting as success.
	return ctx.Err()
}

// sensitiveSMTPAllowsPlaintext is a local-development exception only. Never
// resolve arbitrary DNS names to decide trust: aliases, localhost suffixes, and
// wildcard/listen addresses require verified TLS like every remote host.
func sensitiveSMTPAllowsPlaintext(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Zone() == "" && addr.IsLoopback()
}
