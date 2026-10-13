package service

import (
	"context"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
)

// formatRFC2047IfNeeded encodes a string to RFC 2047 MIME encoded-word syntax if it contains non-ASCII characters
func formatRFC2047IfNeeded(s string) string {
	hasNonASCII := false
	for _, r := range s {
		if r > unicode.MaxASCII {
			hasNonASCII = true
			break
		}
	}
	if !hasNonASCII {
		return s
	}
	return mime.BEncoding.Encode("UTF-8", s)
}

// formatAddressHeader formats an email address header (From, To) with RFC 2047 display name encoding if needed
func formatAddressHeader(rawAddr string) string {
	rawAddr = strings.TrimSpace(rawAddr)
	if rawAddr == "" {
		return ""
	}
	parsed, err := mail.ParseAddress(rawAddr)
	if err == nil {
		if parsed.Name != "" {
			return fmt.Sprintf("%s <%s>", formatRFC2047IfNeeded(parsed.Name), parsed.Address)
		}
		return parsed.Address
	}
	// Fallback to simple angle bracket parsing if mail.ParseAddress fails
	if start := strings.Index(rawAddr, "<"); start > 0 && strings.HasSuffix(rawAddr, ">") {
		name := strings.TrimSpace(rawAddr[:start])
		addr := strings.TrimSpace(rawAddr[start+1 : len(rawAddr)-1])
		return fmt.Sprintf("%s <%s>", formatRFC2047IfNeeded(name), addr)
	}
	return rawAddr
}

// SentEmail records captured email payload for inspection
type SentEmail struct {
	From     string
	To       string
	Subject  string
	HTMLBody string
	TextBody string
	SentAt   time.Time
}

// EmailSender defines interface for sending email. Implementations used for
// synchronous credential mail must honor context cancellation and deadlines.
// The built-in SMTP sender enforces these on its socket. Custom implementations
// that ignore context can block their caller and are unsupported for this use;
// EmailService does not detach them into background goroutines.
type EmailSender interface {
	Send(ctx context.Context, from, to, subject, htmlBody, textBody string) error
}

// SMTPEmailSender implements EmailSender via standard SMTP
type SMTPEmailSender struct {
	Host     string
	Port     string
	Username string
	Password string
}

func NewSMTPEmailSender(host, port, username, password string) *SMTPEmailSender {
	return &SMTPEmailSender{
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
	}
}

func (s *SMTPEmailSender) Send(ctx context.Context, from, to, subject, htmlBody, textBody string) error {
	boundary := fmt.Sprintf("boundary-%d", time.Now().UnixNano())

	logger.WithComponent("smtp").Info("sending email via smtp",
		"host", s.Host,
		"port", s.Port,
		"from", from,
		"to", to,
		"subject", subject,
	)

	headers := []string{
		fmt.Sprintf("From: %s", formatAddressHeader(from)),
		fmt.Sprintf("To: %s", formatAddressHeader(to)),
		fmt.Sprintf("Subject: %s", formatRFC2047IfNeeded(subject)),
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"", boundary),
		"",
	}

	var msgBuilder strings.Builder
	msgBuilder.WriteString(strings.Join(headers, "\r\n"))
	msgBuilder.WriteString("\r\n")

	// Plain text part
	msgBuilder.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msgBuilder.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	msgBuilder.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	msgBuilder.WriteString(textBody)
	msgBuilder.WriteString("\r\n\r\n")

	// HTML part
	msgBuilder.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	msgBuilder.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	msgBuilder.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	msgBuilder.WriteString(htmlBody)
	msgBuilder.WriteString("\r\n\r\n")

	// End boundary
	msgBuilder.WriteString(fmt.Sprintf("--%s--\r\n", boundary))

	rawMsg := []byte(msgBuilder.String())

	// Confirmation, password reset and invitations carry bearer credentials.
	// Their shared marker selects the bounded, verified-TLS transport as well
	// as error redaction. Ordinary mail retains its existing transport below.
	if ctx != nil && ctx.Value(sensitiveEmailContextKey{}) == true {
		return s.sendSensitiveSMTP(ctx, from, to, rawMsg, s.Port == "465")
	}

	// Preserve the historical synchronous ordinary-mail cancellation contract.
	// Only the owned retry lifecycle opts into interruptible ordinary SMTP.
	transportCtx := context.Background()
	if ctx != nil && ctx.Value(emailRetryLifecycleContextKey{}) == true {
		transportCtx = ctx
	}
	if err := s.sendLegacySMTPContext(transportCtx, from, to, rawMsg, s.Port == "465"); err != nil {
		logger.WithComponent("smtp").Error("smtp sendmail failed", "host", s.Host, "to", to, "error", safeEmailSendError(ctx, err))
		return err
	}

	logger.WithComponent("smtp").Info("email sent successfully via smtp sendmail",
		"to", to,
		"subject", subject,
	)

	return nil
}

// MockEmailSender is a mock/in-memory email sender for development, test, and fallback environments
type MockEmailSender struct {
	mu         sync.RWMutex
	sentEmails []SentEmail
	failErr    error
}

func NewMockEmailSender() *MockEmailSender {
	return &MockEmailSender{
		sentEmails: make([]SentEmail, 0),
	}
}

func (m *MockEmailSender) Send(ctx context.Context, from, to, subject, htmlBody, textBody string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failErr != nil {
		err := m.failErr
		logger.WithComponent("mock_smtp").Warn("mock email delivery simulated failure",
			"to", to,
			"error", safeEmailSendError(ctx, err),
		)
		return err
	}

	m.sentEmails = append(m.sentEmails, SentEmail{
		From:     from,
		To:       to,
		Subject:  subject,
		HTMLBody: htmlBody,
		TextBody: textBody,
		SentAt:   time.Now(),
	})

	logger.WithComponent("mock_smtp").Debug("mock email captured successfully",
		"from", from,
		"to", to,
		"subject", subject,
	)

	return nil
}

func (m *MockEmailSender) GetSentEmails() []SentEmail {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SentEmail, len(m.sentEmails))
	copy(result, m.sentEmails)
	return result
}

func (m *MockEmailSender) SetFailError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failErr = err
}

func (m *MockEmailSender) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentEmails = make([]SentEmail, 0)
	m.failErr = nil
}
