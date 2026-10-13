package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// InboxTranscriptSMTPRepository reads only CHN-owned current Inbox data. It must
// honor ctx and scope the read to both identifiers; the adapter independently
// checks the returned identity before inspecting the current configuration.
type InboxTranscriptSMTPRepository interface {
	FindInboxSMTP(ctx context.Context, accountID, inboxID uint) (*domain.Inbox, error)
}

type inboxTranscriptSMTP struct {
	repo         InboxTranscriptSMTPRepository
	fallbackFrom string
}

// NewInboxTranscriptSMTP creates the CHN local owner adapter. fallbackFrom must
// be the legacy EmailService effective fallback From used at admission. No SMTP
// sender or credentials leave this adapter, and it has no global fallback.
func NewInboxTranscriptSMTP(repo InboxTranscriptSMTPRepository, fallbackFrom string) domain.InboxTranscriptSMTP {
	return &inboxTranscriptSMTP{repo: repo, fallbackFrom: fallbackFrom}
}

// ManagedTranscriptInboxBinding reports actual legacy Inbox selection. The bool
// is true even when the selected Inbox cannot yield a usable proof; (nil,true)
// must fail managed admission, never fall through to a direct or global send.
// Global, mock, custom and absent selections return (nil,false).
func (s *EmailService) ManagedTranscriptInboxBinding(conv *domain.Conversation) (*domain.InboxSMTPBinding, bool) {
	if s == nil || conv == nil {
		return nil, false
	}
	sender, from, selected := selectInboxTranscriptSender(conv.Inbox, s.fromEmail)
	if !selected {
		return nil, false
	}
	if conv.AccountID == 0 || conv.InboxID == 0 || conv.Inbox.AccountID != conv.AccountID || conv.Inbox.ID != conv.InboxID {
		return nil, true
	}
	binding, ok := inboxSMTPBinding(conv.Inbox, sender, from)
	if !ok {
		return nil, true
	}
	return &binding, true
}

// The fingerprint binds the transport policy as well as the selected route.
// Changing any policy behavior requires a new version and re-admission.
const inboxSMTPTransportPolicy = "ordinary_smtp_v1;tls_min_1_2;implicit_port_465;starttls_required_except_loopback;auth_plain"

func inboxSMTPBinding(inbox *domain.Inbox, sender *SMTPEmailSender, from string) (domain.InboxSMTPBinding, bool) {
	if inbox == nil || inbox.ID == 0 || inbox.AccountID == 0 || inbox.CreatedAt.IsZero() || !inboxSMTPSupported(inbox.ProviderConfig, sender, from) {
		return domain.InboxSMTPBinding{}, false
	}
	binding := domain.InboxSMTPBinding{
		Kind:      domain.InboxSMTPBindingKind,
		AccountID: strconv.FormatUint(uint64(inbox.AccountID), 10),
		InboxID:   strconv.FormatUint(uint64(inbox.ID), 10),
		CreatedAt: inbox.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	// An ordered JSON array gives exact boundaries without delimiter ambiguity.
	identity, _ := json.Marshal([]string{binding.Kind, binding.AccountID, binding.InboxID, binding.CreatedAt, sender.Host, sender.Port, sender.Username, from, inboxSMTPTransportPolicy})
	digest := sha256.Sum256(identity)
	binding.Fingerprint = domain.InboxSMTPFingerprintPrefix + hex.EncodeToString(digest[:])
	return binding, domain.ValidateInboxSMTPBinding(binding) == nil
}

func inboxSMTPSupported(providerConfig string, sender *SMTPEmailSender, from string) bool {
	if sender == nil || !ordinarySMTPValidHost(sender.Host) || !ordinarySMTPValidHeader(sender.Username) || !ordinarySMTPValidHeader(from) {
		return false
	}
	port, err := strconv.Atoi(sender.Port)
	if err != nil || port < 1 || port > 65535 || strings.TrimSpace(sender.Port) != sender.Port {
		return false
	}
	if parsed, err := mail.ParseAddress(from); err != nil || parsed.Address != from {
		return false
	}
	// An authenticated route that lost its credential cannot be submitted.
	if sender.Username != "" && sender.Password == "" {
		return false
	}
	var config map[string]json.RawMessage
	if json.Unmarshal([]byte(providerConfig), &config) != nil {
		return false
	}
	// The existing sender only supports PLAIN. Explicit alternative modes cannot
	// silently acquire managed provenance; the legacy direct path is unchanged.
	// Match JSON field casing like the shared legacy struct decoder does.
	for key, raw := range config {
		switch strings.ToLower(key) {
		case "smtp_authentication", "smtp_auth_type", "smtp_auth_mode", "smtp_auth":
			if string(raw) != "null" {
				var value string
				if json.Unmarshal(raw, &value) != nil || (value != "" && value != "plain") {
					return false
				}
			}
		case "smtp_mode", "smtp_transport", "smtp_tls", "smtp_ssl", "smtp_enable_starttls_auto":
			// No transport-policy override is implemented by this fixed policy.
			if string(raw) != "null" && string(raw) != "\"\"" {
				return false
			}
		case "smtp_oauth", "smtp_oauth2", "smtp_oauth_enabled", "smtp_oauth2_enabled", "oauth_enabled":
			if string(raw) != "null" && string(raw) != "\"\"" && string(raw) != "false" {
				return false
			}
		}
	}
	return true
}

func (s *inboxTranscriptSMTP) resolve(ctx context.Context, binding domain.InboxSMTPBinding, from string) (*SMTPEmailSender, bool) {
	if s == nil || s.repo == nil || domain.ValidateInboxSMTPBinding(binding) != nil || ordinarySMTPContextDone(ctx) {
		return nil, false
	}
	accountID, _ := strconv.ParseUint(binding.AccountID, 10, strconv.IntSize)
	inboxID, _ := strconv.ParseUint(binding.InboxID, 10, strconv.IntSize)
	inbox, err := s.repo.FindInboxSMTP(ctx, uint(accountID), uint(inboxID))
	if err != nil || inbox == nil || inbox.AccountID != uint(accountID) || inbox.ID != uint(inboxID) || ordinarySMTPContextDone(ctx) {
		return nil, false
	}
	sender, currentFrom, selected := selectInboxTranscriptSender(inbox, s.fallbackFrom)
	if !selected || currentFrom != from {
		return nil, false
	}
	current, valid := inboxSMTPBinding(inbox, sender, currentFrom)
	if !valid || current != binding {
		return nil, false
	}
	return sender, true
}

func (s *inboxTranscriptSMTP) SendInboxTranscript(ctx context.Context, binding domain.InboxSMTPBinding, from, to, subject, htmlBody, textBody string, beforeSubmit func() error) domain.OrdinarySMTPResult {
	permanent := domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPPermanentFailure}
	cancelled := domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPCancelledBeforeSubmission}
	if ctx == nil || ordinarySMTPContextDone(ctx) {
		return cancelled
	}
	if beforeSubmit == nil {
		return permanent
	}
	ctx, cancel := context.WithTimeout(ctx, ordinaryEmailSendTimeout)
	defer cancel()
	sender, valid := s.resolve(ctx, binding, from)
	if !valid {
		if ordinarySMTPContextDone(ctx) {
			return cancelled
		}
		return permanent
	}
	invalidated := false
	result := sender.SendOrdinary(ctx, from, to, subject, htmlBody, textBody, func() error {
		// CHN's last read immediately precedes MSG's durable marker. No database
		// transaction is claimed across owners, and a later mutation cannot recall
		// DATA. Password rotation does not change the route fingerprint.
		if _, valid := s.resolve(ctx, binding, from); !valid {
			invalidated = !ordinarySMTPContextDone(ctx)
			return errors.New("inbox smtp route unavailable")
		}
		return beforeSubmit()
	})
	if invalidated {
		return permanent
	}
	return result
}
