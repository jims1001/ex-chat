package service

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
)

// SentEmail records captured email payload for inspection
type SentEmail struct {
	From     string
	To       string
	Subject  string
	HTMLBody string
	TextBody string
	SentAt   time.Time
}

// EmailSender defines interface for sending email
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
	addr := fmt.Sprintf("%s:%s", s.Host, s.Port)
	boundary := fmt.Sprintf("boundary-%d", time.Now().UnixNano())

	logger.WithComponent("smtp").Info("sending email via smtp",
		"host", s.Host,
		"port", s.Port,
		"from", from,
		"to", to,
		"subject", subject,
	)

	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}

	headers := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", subject),
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

	// If port is 465, use direct TLS
	if s.Port == "465" {
		tlsConfig := &tls.Config{
			ServerName: s.Host,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			logger.WithComponent("smtp").Error("smtp tls dial failed",
				"addr", addr,
				"error", err.Error(),
			)
			return fmt.Errorf("tls dial failed: %w", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, s.Host)
		if err != nil {
			logger.WithComponent("smtp").Error("smtp new client failed",
				"addr", addr,
				"error", err.Error(),
			)
			return fmt.Errorf("smtp new client failed: %w", err)
		}
		defer client.Close()

		if auth != nil {
			if err = client.Auth(auth); err != nil {
				logger.WithComponent("smtp").Error("smtp client auth failed",
					"username", s.Username,
					"error", err.Error(),
				)
				return fmt.Errorf("smtp auth failed: %w", err)
			}
		}
		if err = client.Mail(from); err != nil {
			logger.WithComponent("smtp").Error("smtp mail from failed",
				"from", from,
				"error", err.Error(),
			)
			return fmt.Errorf("smtp mail from failed: %w", err)
		}
		if err = client.Rcpt(to); err != nil {
			logger.WithComponent("smtp").Error("smtp rcpt to failed",
				"to", to,
				"error", err.Error(),
			)
			return fmt.Errorf("smtp rcpt to failed: %w", err)
		}
		w, err := client.Data()
		if err != nil {
			logger.WithComponent("smtp").Error("smtp data failed", "error", err.Error())
			return fmt.Errorf("smtp data failed: %w", err)
		}
		_, err = w.Write(rawMsg)
		if err != nil {
			logger.WithComponent("smtp").Error("smtp write failed", "error", err.Error())
			return fmt.Errorf("smtp write failed: %w", err)
		}
		err = w.Close()
		if err != nil {
			logger.WithComponent("smtp").Error("smtp close data failed", "error", err.Error())
			return fmt.Errorf("smtp close data failed: %w", err)
		}

		logger.WithComponent("smtp").Info("email sent successfully via tls smtp",
			"to", to,
			"subject", subject,
		)

		return client.Quit()
	}

	if err := smtp.SendMail(addr, auth, from, []string{to}, rawMsg); err != nil {
		logger.WithComponent("smtp").Error("smtp sendmail failed",
			"addr", addr,
			"to", to,
			"error", err.Error(),
		)
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
			"error", err.Error(),
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
