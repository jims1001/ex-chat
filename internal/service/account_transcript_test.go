package service

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountTranscriptSenderRequiresOriginalGlobalProvenance(t *testing.T) {
	for _, name := range []string{"global", "sender_alias", "injected_same_object", "injected_matching_smtp", "mock", "mutated_host", "mutated_port", "mutated_user", "mutated_password", "changed_from", "fallback_from", "normalized_host", "normalized_port", "inbox_same_identity", "disabled_inbox"} {
		t.Run(name, func(t *testing.T) {
			for k, v := range map[string]string{"SMTP_ADDRESS": "127.0.0.1", "SMTP_PORT": "2525", "SMTP_USERNAME": "user", "SMTP_PASSWORD": "synthetic-test-password", "MAILER_SENDER_EMAIL": "sender@example.test", "SMTP_FROM_EMAIL": ""} {
				t.Setenv(k, v)
			}
			if name == "sender_alias" {
				t.Setenv("MAILER_SENDER_EMAIL", "")
				t.Setenv("SMTP_FROM_EMAIL", "sender@example.test")
			}
			if name == "fallback_from" {
				t.Setenv("MAILER_SENDER_EMAIL", "")
			}
			if name == "normalized_host" {
				t.Setenv("SMTP_ADDRESS", " 127.0.0.1 ")
			}
			if name == "normalized_port" {
				t.Setenv("SMTP_PORT", " 2525 ")
			}
			s := NewEmailService(nil)
			conv := &domain.Conversation{}
			switch name {
			case "injected_same_object":
				s.SetSender(s.GetSender())
			case "injected_matching_smtp":
				s.SetSender(NewSMTPEmailSender("127.0.0.1", "2525", "user", "synthetic-test-password"))
			case "mock":
				s.SetSender(NewMockEmailSender())
			case "mutated_host":
				s.GetSender().(*SMTPEmailSender).Host = "localhost"
			case "mutated_port":
				s.GetSender().(*SMTPEmailSender).Port = "2526"
			case "mutated_user":
				s.GetSender().(*SMTPEmailSender).Username = "other"
			case "mutated_password":
				s.GetSender().(*SMTPEmailSender).Password = "different-synthetic-password"
			case "changed_from":
				s.SetFromEmail("other@example.test")
			case "inbox_same_identity":
				conv.Inbox = &domain.Inbox{ProviderConfig: `{"smtp_address":"127.0.0.1","smtp_port":"2525","smtp_username":"sender@example.test"}`}
			case "disabled_inbox":
				conv.Inbox = &domain.Inbox{ProviderConfig: `{"smtp_address":"127.0.0.1","smtp_port":"2525","smtp_enabled":false}`}
			}
			_, eligible := s.ManagedTranscriptSender(conv)
			require.Equal(t, name == "global" || name == "sender_alias" || name == "disabled_inbox", eligible)
		})
	}
}
