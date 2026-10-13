package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"strings"
)

// emailRetryLifecycleContextKey is private to the owned retry dispatch path.
type emailRetryLifecycleContextKey struct{}

// smtpHelloConn remembers the standard client's plaintext HELO fallback. The
// old smtp.SendMail skips AUTH for a HELO-only server, but rejects missing AUTH
// after successful EHLO. Client does not export that distinction. Observe only
// the command prefix, without retaining or logging any protocol content.
type smtpHelloConn struct {
	net.Conn
	usedHELO bool
}

func (c *smtpHelloConn) Write(p []byte) (int, error) {
	if bytes.HasPrefix(p, []byte("HELO ")) {
		c.usedHELO = true
	}
	return c.Conn.Write(p)
}

// Owned retry mail keeps its existing TLS/auth policy and introduces no new
// timeout. Its lifecycle cancellation/deadlines interrupt every socket operation.
// The send stays synchronous; cleanup joins the cancellation callback.
func (s *SMTPEmailSender) sendLegacySMTPContext(ctx context.Context, from, to string, message []byte, implicitTLS bool) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Match smtp.SendMail's pre-dial validation on its non-implicit-TLS path.
	if !implicitTLS && (strings.ContainsAny(from, "\r\n") || strings.ContainsAny(to, "\r\n")) {
		return errors.New("smtp: A line must not contain CR or LF")
	}
	defer func() {
		if err != nil && ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	closed := make(chan struct{})
	stopClose := context.AfterFunc(ctx, func() { defer close(closed); _ = conn.Close() })
	defer func() {
		if !stopClose() {
			<-closed
		}
	}()
	helloConn := &smtpHelloConn{Conn: conn}
	var smtpConn net.Conn = helloConn
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
		if err = client.Hello("localhost"); err != nil {
			return err
		}
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err = client.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
				return err
			}
		}
	}
	if s.Username != "" && (implicitTLS || !helloConn.usedHELO) {
		if !implicitTLS {
			if supported, _ := client.Extension("AUTH"); !supported {
				return errors.New("smtp: server doesn't support AUTH")
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
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
