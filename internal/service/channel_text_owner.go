package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/twilio/twilio-go/client"
)

// channelTextOwner owns all outbound credential resolution, provider transport
// and provider signature verification within CHN. No MSG store is available.
type channelTextOwner struct {
	config    channel.Config
	transport channel.TextTransport
	inboxes   *repository.InboxRepository
}

func NewChannelTextOwner(cfg channel.Config, transport channel.TextTransport, inboxes *repository.InboxRepository) channel.TextOwner {
	if transport == nil {
		transport = channel.NewHTTPTextTransport(nil)
	}
	return &channelTextOwner{cfg, transport, inboxes}
}

func (s *channelTextOwner) Ready() error {
	if s == nil || s.inboxes == nil || s.transport == nil {
		return channel.ErrNotConfigured
	}
	return nil
}

func (s *channelTextOwner) OutboundEnabled() bool { return s.config.OutboundEnabled }

func (s *channelTextOwner) statusURL(accountID uint, attemptID string) string {
	return strings.TrimRight(s.config.PublicBaseURL, "/") + fmt.Sprintf("/api/v1/accounts/%d/twilio/status/%s", accountID, attemptID)
}

func (s *channelTextOwner) fingerprint(c *domain.ChannelConnection) string {
	return channel.StableKey(fmt.Sprint(c.ID), fmt.Sprint(c.AccountID), fmt.Sprint(c.InboxID), c.Provider, c.ProviderAccountID, c.RecipientID, c.RecipientKey, c.SecretCiphertext, s.config.PublicBaseURL, s.config.WhatsAppAPIVersion, s.config.WhatsAppAppID)
}

func (s *channelTextOwner) PrepareText(ctx context.Context, intent channel.TextIntent) (channel.TextBinding, error) {
	connection, err := s.inboxes.OutboundConnection(ctx, intent.AccountID, intent.InboxID)
	if err != nil {
		return channel.TextBinding{}, err
	}
	if err := s.config.Validate(connection.Provider); err != nil {
		return channel.TextBinding{}, err
	}
	limit := 4096
	if connection.Provider == "twilio" {
		limit = 1600
	}
	if utf8.RuneCountInString(intent.Body) > limit {
		return channel.TextBinding{}, channel.ErrInvalid
	}
	binding := channel.TextBinding{ConnectionID: connection.ID, Provider: connection.Provider, ProviderAccountID: connection.ProviderAccountID, SenderID: connection.RecipientID, ConfigFingerprint: s.fingerprint(connection), APIVersion: s.config.WhatsAppAPIVersion}
	if connection.Provider == "twilio" {
		binding.APIVersion = "2010-04-01"
		binding.CallbackURL = s.statusURL(intent.AccountID, intent.AttemptID)
	} else {
		binding.CallbackURL = s.config.Callback(intent.AccountID, "whatsapp")
	}
	// Preserve admission's decrypt-as-configuration-check without exporting the
	// encrypted connection or plaintext to the MSG coordinator.
	key, err := s.config.Key()
	if err != nil {
		return channel.TextBinding{}, err
	}
	if _, err := channel.Open(key, credentialScope(connection), connection.SecretCiphertext); err != nil {
		return channel.TextBinding{}, err
	}
	return binding, nil
}

func (s *channelTextOwner) connectionBinding(ctx context.Context, a channel.TextSnapshot) (*domain.ChannelConnection, error) {
	c, err := s.inboxes.OutboundConnection(ctx, a.AccountID, a.InboxID)
	if err != nil {
		return nil, err
	}
	if c.ID != a.ConnectionID || c.Provider != a.Provider || c.ProviderAccountID != a.ProviderAccountID || c.RecipientID != a.SenderID || s.fingerprint(c) != a.ConfigFingerprint {
		return nil, channel.ErrUnauthorized
	}
	if err := s.config.Validate(c.Provider); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *channelTextOwner) request(a channel.TextSnapshot, c *domain.ChannelConnection) (channel.TextRequest, error) {
	key, err := s.config.Key()
	if err != nil {
		return channel.TextRequest{}, err
	}
	secret, err := channel.Open(key, credentialScope(c), c.SecretCiphertext)
	if err != nil {
		return channel.TextRequest{}, err
	}
	return channel.TextRequest{AttemptID: a.AttemptID, Provider: a.Provider, ProviderAccountID: a.ProviderAccountID, SenderID: a.SenderID, RecipientID: a.RecipientID, Body: a.Body, CallbackURL: a.CallbackURL, APIVersion: a.APIVersion, Credential: secret}, nil
}

// preparedChannelText keeps its resolved request in a closure, so callers cannot
// inspect or format credential-bearing fields. Send does no later revalidation.
type preparedChannelText func(context.Context) channel.Result

func (p preparedChannelText) Send(ctx context.Context) channel.Result {
	return p(ctx)
}

func (s *channelTextOwner) PrepareDispatch(ctx context.Context, a channel.TextSnapshot) (channel.PreparedText, error) {
	c, err := s.connectionBinding(ctx, a)
	if err != nil {
		return nil, err
	}
	req, err := s.request(a, c)
	if err != nil {
		return nil, err
	}
	transport := s.transport
	return preparedChannelText(func(ctx context.Context) channel.Result {
		return transport.SendText(ctx, req)
	}), nil
}

func (s *channelTextOwner) LookupText(ctx context.Context, a channel.TextSnapshot, providerID string) (channel.Result, error) {
	c, err := s.connectionBinding(ctx, a)
	if err != nil {
		return channel.Result{}, err
	}
	req, err := s.request(a, c)
	if err != nil {
		return channel.Result{}, err
	}
	return s.transport.LookupText(ctx, req, providerID), nil
}

func (s *channelTextOwner) VerifyTwilioStatus(ctx context.Context, a channel.TextSnapshot, event channel.StatusEvent, params map[string]string, signature string) error {
	if a.Provider != "twilio" || event.ProviderAccountID != a.ProviderAccountID || event.SenderID != a.SenderID || event.RecipientID != a.RecipientID || a.CallbackURL != s.statusURL(a.AccountID, a.AttemptID) {
		return channel.ErrUnauthorized
	}
	c, err := s.connectionBinding(ctx, a)
	if err != nil {
		return err
	}
	req, err := s.request(a, c)
	if err != nil {
		return err
	}
	validator := client.NewRequestValidator(req.Credential)
	if !validator.Validate(a.CallbackURL, params, signature) {
		return channel.ErrUnauthorized
	}
	return nil
}

func (s *channelTextOwner) ParseWhatsAppStatuses(accountID uint, signature string, raw []byte) ([]channel.StatusEvent, error) {
	if s.config.Validate("whatsapp") != nil {
		return nil, channel.ErrNotConfigured
	}
	if accountID == 0 || !channel.VerifyWhatsAppSignature(s.config.WhatsAppAppSecret, signature, raw) {
		return nil, channel.ErrUnauthorized
	}
	return channel.ParseWhatsAppStatuses(raw)
}

func (s *channelTextOwner) CheckBinding(ctx context.Context, a channel.TextSnapshot) error {
	_, err := s.connectionBinding(ctx, a)
	return err
}
