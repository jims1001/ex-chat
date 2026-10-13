package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

const ordinaryEmailSendTimeout = 30 * time.Second

// OrdinarySMTPOutcome describes submission evidence, never mailbox delivery.
// Only a proven pre-submission transient failure is eligible for automatic retry.
type OrdinarySMTPOutcome = domain.OrdinarySMTPOutcome

const (
	OrdinarySMTPAccepted                  = domain.OrdinarySMTPAccepted
	OrdinarySMTPPreSubmissionTransient    = domain.OrdinarySMTPPreSubmissionTransient
	OrdinarySMTPPermanentFailure          = domain.OrdinarySMTPPermanentFailure
	OrdinarySMTPAuthenticationFailure     = domain.OrdinarySMTPAuthenticationFailure
	OrdinarySMTPCancelledBeforeSubmission = domain.OrdinarySMTPCancelledBeforeSubmission
	OrdinarySMTPUnknown                   = domain.OrdinarySMTPUnknown
)

// OrdinarySMTPResult deliberately carries no server response, address, body,
// credential, or wrapped error. These values are safe to persist as queue state.
type OrdinarySMTPResult = domain.OrdinarySMTPResult

// SendOrdinary sends only ordinary mail through a bounded SMTP session. The
// caller must durably record the submitting state in beforeSubmission. It is
// called exactly once immediately before DATA, after the envelope is accepted;
// an error prevents DATA. A nil hook is rejected before opening a connection.
// The hook must honor the caller's context and must not detach its durable write.
// From authorization and immutable queue payload validation belong to the worker.
// Credential mail must continue to use the separate sensitive transport.
func (s *SMTPEmailSender) SendOrdinary(ctx context.Context, from, to, subject, htmlBody, textBody string, beforeSubmission func() error) OrdinarySMTPResult {
	if ctx == nil || ordinarySMTPContextDone(ctx) {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPCancelledBeforeSubmission}
	}
	ctx, cancel := context.WithTimeout(ctx, ordinaryEmailSendTimeout)
	defer cancel()
	if s == nil || beforeSubmission == nil || !ordinarySMTPValidHost(s.Host) {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPPermanentFailure}
	}
	port, err := strconv.Atoi(s.Port)
	if err != nil || port < 1 || port > 65535 {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPPermanentFailure}
	}
	envelopeFrom, envelopeTo, message, err := ordinarySMTPMessage(from, to, subject, htmlBody, textBody)
	if err != nil {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPPermanentFailure}
	}
	return s.sendOrdinarySMTP(ctx, envelopeFrom, envelopeTo, message, port == 465, beforeSubmission)
}

// Keeping TLS mode separate permits loopback fixtures to exercise implicit TLS
// on an ephemeral port. The public entry point chooses it only from port 465.
func (s *SMTPEmailSender) sendOrdinarySMTP(ctx context.Context, from, to string, message []byte, implicitTLS bool, beforeSubmission func() error) OrdinarySMTPResult {
	if ordinarySMTPContextDone(ctx) {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPCancelledBeforeSubmission}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
	if err != nil {
		return ordinarySMTPBeforeSubmissionResult(ctx, err)
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > ordinaryEmailSendTimeout {
		deadline = time.Now().Add(ordinaryEmailSendTimeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return ordinarySMTPBeforeSubmissionResult(ctx, err)
	}
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	return s.submitOrdinarySMTP(ctx, conn, from, to, message, implicitTLS, beforeSubmission)
}

// Production always supplies an owned connection dialed to the configured host
// with a total deadline. Tests can separately exercise remote-host TLS policy
// against a loopback server without changing DNS or weakening TLS verification.
func (s *SMTPEmailSender) submitOrdinarySMTP(ctx context.Context, conn net.Conn, from, to string, message []byte, implicitTLS bool, beforeSubmission func() error) OrdinarySMTPResult {
	if ordinarySMTPContextDone(ctx) {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPCancelledBeforeSubmission}
	}
	var smtpConn net.Conn = conn
	if implicitTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return ordinarySMTPBeforeSubmissionResult(ctx, err)
		}
		smtpConn = tlsConn
	}
	client, err := smtp.NewClient(smtpConn, s.Host)
	if err != nil {
		return ordinarySMTPBeforeSubmissionResult(ctx, err)
	}
	defer client.Close()
	if err := client.Hello("localhost"); err != nil {
		return ordinarySMTPBeforeSubmissionResult(ctx, err)
	}
	if !implicitTLS {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return ordinarySMTPBeforeSubmissionResult(ctx, err)
			}
		} else if !sensitiveSMTPAllowsPlaintext(s.Host) {
			return OrdinarySMTPResult{Outcome: OrdinarySMTPPermanentFailure}
		}
	}
	if s.Username != "" {
		if supported, _ := client.Extension("AUTH"); !supported {
			return OrdinarySMTPResult{Outcome: OrdinarySMTPAuthenticationFailure}
		}
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			if ordinarySMTPContextDone(ctx) {
				return OrdinarySMTPResult{Outcome: OrdinarySMTPCancelledBeforeSubmission}
			}
			// Even a temporary AUTH rejection stops automatic retries. A worker
			// must not hammer a provider with invalid or suspended credentials.
			return OrdinarySMTPResult{Outcome: OrdinarySMTPAuthenticationFailure}
		}
	}
	if err := client.Mail(from); err != nil {
		return ordinarySMTPBeforeSubmissionResult(ctx, err)
	}
	if err := client.Rcpt(to); err != nil {
		return ordinarySMTPBeforeSubmissionResult(ctx, err)
	}
	if ordinarySMTPContextDone(ctx) {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPCancelledBeforeSubmission}
	}
	if beforeSubmission == nil {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPPermanentFailure}
	}
	if err := beforeSubmission(); err != nil {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPCancelledBeforeSubmission}
	}
	// The durable marker may now exist. Conservatively prohibit automatic
	// retry after this point, even if cancellation prevents writing DATA.
	unknown := OrdinarySMTPResult{Outcome: OrdinarySMTPUnknown}
	if ordinarySMTPContextDone(ctx) {
		return unknown
	}
	writer, err := client.Data()
	if err != nil {
		return unknown
	}
	if _, err := writer.Write(message); err != nil {
		return unknown
	}
	// Close reads the final DATA reply. Only its 250 response proves acceptance;
	// a lost response, rejected body, or late acknowledgement remains unknown.
	if err := writer.Close(); err != nil || ordinarySMTPContextDone(ctx) {
		return unknown
	}
	// Acceptance cannot be undone by a lost QUIT response or later cancellation.
	// This intentionally differs from the unchanged credential-mail contract.
	_ = client.Quit()
	return OrdinarySMTPResult{Outcome: OrdinarySMTPAccepted}
}

func ordinarySMTPContextDone(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func ordinarySMTPBeforeSubmissionResult(ctx context.Context, err error) OrdinarySMTPResult {
	if ordinarySMTPContextDone(ctx) {
		return OrdinarySMTPResult{Outcome: OrdinarySMTPCancelledBeforeSubmission}
	}
	permanent := OrdinarySMTPResult{Outcome: OrdinarySMTPPermanentFailure}
	transient := OrdinarySMTPResult{Outcome: OrdinarySMTPPreSubmissionTransient}
	var protocolErr *textproto.Error
	if errors.As(err, &protocolErr) {
		if protocolErr.Code >= 400 && protocolErr.Code < 500 {
			return transient
		}
		return permanent
	}
	// Certificate and protocol failures are configuration failures, including
	// TLS verification errors wrapped in net.OpError.
	var unknownCA x509.UnknownAuthorityError
	var wrongHost x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	var recordErr tls.RecordHeaderError
	if errors.As(err, &unknownCA) || errors.As(err, &wrongHost) || errors.As(err, &invalidCert) || errors.As(err, &recordErr) {
		return permanent
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return permanent
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return transient
	}
	return permanent
}

func ordinarySMTPValidHeader(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func ordinarySMTPValidHost(host string) bool {
	return host != "" && strings.TrimSpace(host) == host && ordinarySMTPValidHeader(host)
}

func ordinarySMTPMessage(from, to, subject, htmlBody, textBody string) (string, string, []byte, error) {
	invalid := errors.New("invalid ordinary email headers")
	if !ordinarySMTPValidHeader(from) || !ordinarySMTPValidHeader(to) || !ordinarySMTPValidHeader(subject) {
		return "", "", nil, invalid
	}
	fromAddress, err := mail.ParseAddress(from)
	if err != nil {
		return "", "", nil, invalid
	}
	toAddress, err := mail.ParseAddress(to)
	if err != nil {
		return "", "", nil, invalid
	}
	var message bytes.Buffer
	parts := multipart.NewWriter(&message)
	message.WriteString("From: " + fromAddress.String() + "\r\n")
	message.WriteString("To: " + toAddress.String() + "\r\n")
	message.WriteString("Subject: " + formatRFC2047IfNeeded(subject) + "\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: multipart/alternative; boundary=\"" + parts.Boundary() + "\"\r\n\r\n")
	for _, part := range []struct{ contentType, body string }{{"text/plain", textBody}, {"text/html", htmlBody}} {
		headers := textproto.MIMEHeader{}
		headers.Set("Content-Type", part.contentType+"; charset=UTF-8")
		headers.Set("Content-Transfer-Encoding", "quoted-printable")
		writer, err := parts.CreatePart(headers)
		if err != nil {
			return "", "", nil, invalid
		}
		encoded := quotedprintable.NewWriter(writer)
		if _, err := encoded.Write([]byte(part.body)); err != nil {
			return "", "", nil, invalid
		}
		if err := encoded.Close(); err != nil {
			return "", "", nil, invalid
		}
	}
	if err := parts.Close(); err != nil {
		return "", "", nil, invalid
	}
	return fromAddress.Address, toAddress.Address, message.Bytes(), nil
}
