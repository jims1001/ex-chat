package service

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/google/uuid"
	"github.com/twilio/twilio-go/client"
	"gorm.io/gorm"
)

// ChannelIngressService orchestrates owner commands. It has no shared database
// transaction and makes no direct SQL calls across owner boundaries.
type ChannelIngressService struct {
	config        channel.Config
	provider      channel.Provider
	inboxes       *repository.InboxRepository
	contacts      *repository.ContactRepository
	conversations *repository.ConversationRepository
	messages      *repository.MessageRepository
}

func NewChannelIngressService(cfg channel.Config, provider channel.Provider, inboxes *repository.InboxRepository, contacts *repository.ContactRepository, conversations *repository.ConversationRepository, messages *repository.MessageRepository) *ChannelIngressService {
	if provider == nil {
		provider = channel.NewHTTPProvider(nil)
	}
	return &ChannelIngressService{config: cfg, provider: provider, inboxes: inboxes, contacts: contacts, conversations: conversations, messages: messages}
}

func (s *ChannelIngressService) Ready(provider string) error {
	if s == nil || s.inboxes == nil || s.contacts == nil || s.conversations == nil || s.messages == nil {
		return channel.ErrNotConfigured
	}
	return s.config.Validate(provider)
}

func (s *ChannelIngressService) VerifyWhatsApp(ctx context.Context, accountID uint, mode, token, challenge string) error {
	if err := s.Ready("whatsapp"); err != nil {
		return err
	}
	if accountID == 0 || mode != "subscribe" || challenge == "" || len(challenge) > 1024 || !channel.EqualSecret(s.config.WhatsAppVerifyToken, token) {
		return channel.ErrUnauthorized
	}
	connections, err := s.inboxes.ListWhatsAppConnections(ctx, accountID)
	if err != nil {
		return err
	}
	if len(connections) == 0 {
		return channel.ErrUnauthorized
	}
	for _, connection := range connections {
		if _, err := s.resolve(ctx, accountID, channel.TextEvent{Provider: connection.Provider, ProviderAccountID: connection.ProviderAccountID, RecipientID: connection.RecipientID}); err != nil {
			return err
		}
	}

	return nil
}

type WhatsAppSetup struct {
	Code          string `json:"code"`
	InboxName     string `json:"inbox_name"`
	WabaID        string `json:"waba_id"`
	PhoneNumberID string `json:"phone_number_id"`
}
type TwilioSetup struct {
	InboxName   string `json:"inbox_name"`
	AccountSID  string `json:"account_sid"`
	AuthToken   string `json:"auth_token"`
	PhoneNumber string `json:"phone_number"`
	Medium      string `json:"medium"`
}

type ChannelSetupResult struct {
	InboxID     uint   `json:"inbox_id"`
	AccountID   uint   `json:"account_id"`
	Provider    string `json:"channel_type"`
	RecipientID string `json:"recipient_id"`
	CallbackURL string `json:"callback_url"`
	Configured  bool   `json:"configured"`
}

func credentialScope(c *domain.ChannelConnection) string {
	return channel.StableKey(strconv.FormatUint(uint64(c.AccountID), 10), c.Provider, c.ProviderAccountID, c.RecipientID)
}

func (s *ChannelIngressService) saveConnection(ctx context.Context, accountID uint, name, provider, providerAccount, recipient, secret string) (*ChannelSetupResult, error) {
	key, err := s.config.Key()
	if err != nil {
		return nil, err
	}
	connection := &domain.ChannelConnection{AccountID: accountID, Provider: provider, ProviderAccountID: providerAccount, RecipientID: recipient, RecipientKey: channel.StableKey(provider, recipient)}
	connection.SecretCiphertext, err = channel.Seal(key, credentialScope(connection), secret)
	if err != nil {
		return nil, err
	}
	kind := "Channel::Whatsapp"
	if provider == "twilio" {
		kind = "Channel::TwilioSms"
	}
	inbox := &domain.Inbox{AccountID: accountID, Name: name, ChannelType: kind, WebsiteToken: uuid.NewString(), WebhookURL: s.config.Callback(accountID, provider)}
	if err := s.inboxes.CreateChannelConnection(ctx, inbox, connection); err != nil {
		return nil, err
	}
	return &ChannelSetupResult{InboxID: inbox.ID, AccountID: accountID, Provider: provider, RecipientID: recipient, CallbackURL: inbox.WebhookURL, Configured: true}, nil
}

func (s *ChannelIngressService) AuthorizeWhatsApp(ctx context.Context, accountID uint, req WhatsAppSetup) (*ChannelSetupResult, error) {
	if err := s.Ready("whatsapp"); err != nil {
		return nil, err
	}
	if accountID == 0 || req.Code == "" || req.PhoneNumberID == "" || req.WabaID == "" {
		return nil, channel.ErrInvalid
	}
	name := strings.TrimSpace(req.InboxName)
	if name == "" {
		name = "WhatsApp " + req.PhoneNumberID
	}
	if len(name) > 255 {
		return nil, channel.ErrInvalid
	}
	token, err := s.provider.ExchangeWhatsApp(ctx, s.config, req.Code, req.WabaID, req.PhoneNumberID)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, channel.ErrProvider
	}
	return s.saveConnection(ctx, accountID, name, "whatsapp", req.WabaID, req.PhoneNumberID, token)
}

func (s *ChannelIngressService) CreateTwilio(ctx context.Context, accountID uint, req TwilioSetup) (*ChannelSetupResult, error) {
	if err := s.Ready("twilio"); err != nil {
		return nil, err
	}
	if req.Medium != "" && req.Medium != "sms" {
		return nil, channel.ErrUnsupported
	}
	name := strings.TrimSpace(req.InboxName)
	if accountID == 0 || name == "" || len(name) > 255 {
		return nil, channel.ErrInvalid
	}
	if err := s.provider.VerifyTwilio(ctx, req.AccountSID, req.AuthToken, req.PhoneNumber); err != nil {
		return nil, err
	}
	return s.saveConnection(ctx, accountID, name, "twilio", req.AccountSID, req.PhoneNumber, req.AuthToken)
}

func (s *ChannelIngressService) IngestWhatsApp(ctx context.Context, accountID uint, signature string, raw []byte) (int, error) {
	if err := s.Ready("whatsapp"); err != nil {
		return 0, err
	}
	if accountID == 0 || !channel.VerifyWhatsAppSignature(s.config.WhatsAppAppSecret, signature, raw) {
		return 0, channel.ErrUnauthorized
	}
	events, err := channel.ParseWhatsApp(raw)
	if err != nil {
		return 0, err
	}
	connections := make([]*domain.ChannelConnection, len(events))
	// Preflight the entire envelope before any owner write, including mixed-
	// recipient envelopes. Never partially accept an unsupported/foreign batch.
	for i, event := range events {
		connections[i], err = s.resolve(ctx, accountID, event)
		if err != nil {
			return 0, err
		}
	}
	count := 0
	for i, event := range events {
		created, err := s.ingest(ctx, connections[i], event)
		if err != nil {
			return count, err
		}
		if created {
			count++
		}
	}
	return count, nil
}

func (s *ChannelIngressService) IngestTwilio(ctx context.Context, accountID uint, signature string, raw []byte) (bool, error) {
	if err := s.Ready("twilio"); err != nil {
		return false, err
	}
	if accountID == 0 || signature == "" {
		return false, channel.ErrUnauthorized
	}
	event, params, err := channel.ParseTwilio(raw)
	if err != nil {
		return false, err
	}
	connection, err := s.resolve(ctx, accountID, event)
	if err != nil {
		return false, err
	}
	key, err := s.config.Key()
	if err != nil {
		return false, err
	}
	token, err := channel.Open(key, credentialScope(connection), connection.SecretCiphertext)
	if err != nil {
		return false, err
	}
	validator := client.NewRequestValidator(token)
	if !validator.Validate(s.config.Callback(accountID, "twilio"), params, signature) {
		return false, channel.ErrUnauthorized
	}
	return s.ingest(ctx, connection, event)
}

func (s *ChannelIngressService) resolve(ctx context.Context, accountID uint, event channel.TextEvent) (*domain.ChannelConnection, error) {
	connection, err := s.inboxes.ResolveChannelConnection(ctx, accountID, event.Provider, event.ProviderAccountID, event.RecipientID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, channel.ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	key, err := s.config.Key()
	if err != nil {
		return nil, err
	}
	if _, err := channel.Open(key, credentialScope(connection), connection.SecretCiphertext); err != nil {
		return nil, err
	}
	return connection, nil
}

func (s *ChannelIngressService) ingest(ctx context.Context, connection *domain.ChannelConnection, event channel.TextEvent) (bool, error) {
	account := strconv.FormatUint(uint64(connection.AccountID), 10)
	inbox := strconv.FormatUint(uint64(connection.InboxID), 10)
	key := channel.StableKey(account, event.Provider, event.ProviderAccountID, event.RecipientID, event.ID)
	digest := channel.StableKey(event.Provider, event.ProviderAccountID, event.RecipientID, event.ID, event.From, event.Text)
	exists, err := s.messages.HasChannelMessage(ctx, key, digest, connection.AccountID)
	if err != nil || exists {
		return false, err
	}
	identityKey := channel.StableKey(account, inbox, event.From)
	contact, err := s.contacts.EnsureChannelContact(ctx, identityKey, connection.AccountID, connection.InboxID, event.From, event.Name)
	if err != nil {
		return false, err
	}
	sessionKey := channel.StableKey(account, inbox, strconv.FormatUint(uint64(contact.ID), 10))
	conversation, err := s.conversations.EnsureChannelConversation(ctx, sessionKey, connection.AccountID, connection.InboxID, contact.ID)
	if err != nil {
		return false, err
	}
	current, err := s.resolve(ctx, connection.AccountID, event)
	if err != nil {
		return false, err
	}
	if current.ID != connection.ID || current.InboxID != connection.InboxID {
		return false, channel.ErrUnauthorized
	}
	msg := &domain.Message{AccountID: connection.AccountID, ConversationID: conversation.ID, SenderType: domain.SenderTypeContact, SenderID: contact.ID, MessageType: domain.MessageTypeIncoming, ContentType: domain.ContentTypeText, Content: event.Text, Status: domain.MessageStatusSent, EchoID: event.ID}
	return s.messages.CreateChannelMessage(ctx, key, digest, msg)
}
