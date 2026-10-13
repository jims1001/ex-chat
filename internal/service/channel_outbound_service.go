package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChannelOutboundService coordinates owner ports. Network requests and all
// cross-owner reads happen outside MSG transactions. No credentials enter MSG.
type ChannelOutboundService struct {
	owner         channel.TextOwner
	conversations *repository.ConversationRepository
	messages      *repository.MessageRepository
}

func NewChannelOutboundService(owner channel.TextOwner, conversations *repository.ConversationRepository, messages *repository.MessageRepository) *ChannelOutboundService {
	return &ChannelOutboundService{owner, conversations, messages}
}

func SupportedTextChannel(kind string) bool {
	return kind == "Channel::Whatsapp" || kind == "Channel::TwilioSms"
}

func (s *ChannelOutboundService) ready() error {
	if s == nil || s.messages == nil || s.conversations == nil || s.owner == nil {
		return channel.ErrNotConfigured
	}
	return s.owner.Ready()
}

func (s *ChannelOutboundService) prepare(ctx context.Context, msg *domain.Message, key string) (*domain.OutboundMessageAttempt, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !s.owner.OutboundEnabled() {
		return nil, channel.ErrNotConfigured
	}
	if msg == nil || msg.Private || msg.MessageType != domain.MessageTypeOutgoing || msg.ContentType != domain.ContentTypeText || msg.ContentAttributes != "" || msg.Deleted {
		return nil, channel.ErrUnsupported
	}
	if !utf8.ValidString(msg.Content) || strings.TrimSpace(msg.Content) == "" || len(msg.Content) > 16000 || len(key) > 200 || strings.TrimSpace(key) != key {
		return nil, channel.ErrInvalid
	}
	target, err := s.conversations.OutboundMessageTarget(ctx, msg.AccountID, msg.ConversationID)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if key != "" {
		id = uuid.NewSHA1(uuid.NameSpaceURL, []byte(channel.StableKey("exchat.text.intent.v1", fmt.Sprint(msg.AccountID), fmt.Sprint(msg.ConversationID), msg.SenderType, fmt.Sprint(msg.SenderID), key))).String()
	}
	binding, err := s.owner.PrepareText(ctx, channel.TextIntent{AttemptID: id, AccountID: msg.AccountID, InboxID: target.InboxID, RecipientID: target.RecipientID, Body: msg.Content})
	if err != nil {
		return nil, err
	}
	attempt := &domain.OutboundMessageAttempt{ID: id, AccountID: msg.AccountID, ConversationID: msg.ConversationID, InboxID: target.InboxID, ContactID: target.ContactID, ConnectionID: binding.ConnectionID, Provider: binding.Provider, ProviderAccountID: binding.ProviderAccountID, SenderID: binding.SenderID, RecipientID: target.RecipientID, ConfigFingerprint: binding.ConfigFingerprint, APIVersion: binding.APIVersion, CallbackURL: binding.CallbackURL, Body: msg.Content, ContentType: msg.ContentType, MessageType: msg.MessageType, SenderType: msg.SenderType, ActorID: msg.SenderID, Private: msg.Private}
	return attempt, nil
}

func (s *ChannelOutboundService) CreateText(ctx context.Context, msg *domain.Message, idempotencyKey string) (bool, error) {
	attempt, err := s.prepare(ctx, msg, idempotencyKey)
	if err != nil {
		return false, err
	}
	msg.Status = domain.MessageStatusQueued
	return s.messages.CreateOutboundMessage(ctx, msg, attempt)
}

func (s *ChannelOutboundService) RetryText(ctx context.Context, accountID, conversationID, messageID uint) (*domain.Message, bool, error) {
	if err := s.ready(); err != nil {
		return nil, false, err
	}
	msg, err := s.messages.FindByIDAndConversation(accountID, conversationID, messageID)
	if err != nil {
		return nil, false, err
	}
	if msg == nil {
		return nil, false, gorm.ErrRecordNotFound
	}
	attempt, err := s.prepare(ctx, msg, "")
	if err != nil {
		return nil, false, err
	}
	return s.messages.RetryOutboundMessage(ctx, accountID, conversationID, messageID, attempt)
}

// sendBinding rechecks owner routing at the dispatch boundary. Changes after
// this check cannot recall a provider request already in flight.
func (s *ChannelOutboundService) sendBinding(ctx context.Context, a *domain.OutboundMessageAttempt) (channel.PreparedText, error) {
	target, err := s.conversations.OutboundMessageTarget(ctx, a.AccountID, a.ConversationID)
	if err != nil {
		return nil, err
	}
	if target.InboxID != a.InboxID || target.ContactID != a.ContactID {
		return nil, channel.ErrInvalid
	}
	if target.RecipientID != a.RecipientID {
		return nil, channel.ErrInvalid
	}
	return s.owner.PrepareDispatch(ctx, outboundTextSnapshot(a))
}

// Only the persisted routing/body snapshot crosses into CHN. MSG never receives
// a credential, encrypted connection, provider request, or configuration key.
func outboundTextSnapshot(a *domain.OutboundMessageAttempt) channel.TextSnapshot {
	return channel.TextSnapshot{
		TextIntent:  channel.TextIntent{AttemptID: a.ID, AccountID: a.AccountID, InboxID: a.InboxID, RecipientID: a.RecipientID, Body: a.Body},
		TextBinding: channel.TextBinding{ConnectionID: a.ConnectionID, Provider: a.Provider, ProviderAccountID: a.ProviderAccountID, SenderID: a.SenderID, ConfigFingerprint: a.ConfigFingerprint, APIVersion: a.APIVersion, CallbackURL: a.CallbackURL},
	}
}

// DispatchOne performs at most one provider POST. An expired sending claim is
// recovered by MSG as unknown, never returned as another dispatchable item.
func (s *ChannelOutboundService) DispatchOne(ctx context.Context) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if !s.owner.OutboundEnabled() {
		return false, channel.ErrNotConfigured
	}
	a, err := s.messages.ClaimNextOutbound(ctx, time.Now().UTC(), time.Minute)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	prepared, err := s.sendBinding(ctx, a)
	if err != nil {
		// Missing/changed owners are a definite local cancellation, not a send.
		// A transient store error leaves the pre-dispatch claim to expire safely.
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, gorm.ErrInvalidData) || errors.Is(err, channel.ErrInvalid) || errors.Is(err, channel.ErrUnauthorized) {
			return true, s.messages.CancelClaimedOutbound(ctx, a.AccountID, a.ID, a.ClaimToken, time.Now().UTC())
		}
		return true, err
	}
	a, err = s.messages.StartOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, time.Now().UTC())
	if errors.Is(err, repository.ErrOutboundCancelled) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	result := prepared.Send(ctx)
	// Persist uncertainty even when the request's context was cancelled. This
	// bounded local write cannot send again, and restart recovery remains the fallback.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return true, s.messages.CompleteOutboundDispatch(persist, a.AccountID, a.ID, a.ClaimToken, result.ProviderID, result.Status, result.Disposition)
}

func (s *ChannelOutboundService) Run(ctx context.Context, report func(error)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		for i := 0; i < 20 && ctx.Err() == nil; i++ {
			worked, err := s.DispatchOne(ctx)
			if err != nil {
				if report != nil {
					report(err)
				}
				break
			}
			if !worked {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// ReconcileText uses only Twilio's documented GET-by-SID. It cannot turn a
// missing result into permission to resend. WhatsApp has no presumed lookup.
func (s *ChannelOutboundService) ReconcileText(ctx context.Context, accountID uint, attemptID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	a, err := s.messages.GetOutboundAttempt(ctx, accountID, attemptID)
	if err != nil {
		return err
	}
	if a.Provider != "twilio" || a.ProviderMessageID == "" {
		return channel.ErrUnsupported
	}
	result, err := s.owner.LookupText(ctx, outboundTextSnapshot(a), a.ProviderMessageID)
	if err != nil {
		return err
	}
	if result.Disposition != "accepted" || result.ProviderID != a.ProviderMessageID {
		return channel.ErrProvider
	}
	return s.messages.ApplyOutboundStatus(ctx, *a, result.ProviderID, result.Status)
}

// ReconcileMessage is a scoped, read-only provider lookup followed by a verified
// local status update. It does not create or adopt a send intent.
func (s *ChannelOutboundService) ReconcileMessage(ctx context.Context, accountID, conversationID, messageID uint) (*domain.Message, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	a, err := s.messages.LatestOutboundAttempt(ctx, accountID, conversationID, messageID)
	if err != nil {
		return nil, err
	}
	if err := s.ReconcileText(ctx, accountID, a.ID); err != nil {
		return nil, err
	}
	return s.messages.FindByIDAndConversation(accountID, conversationID, messageID)
}

func (s *ChannelOutboundService) IngestTwilioStatus(ctx context.Context, accountID uint, attemptID, signature string, raw []byte) error {
	if err := s.ready(); err != nil {
		return err
	}
	if _, err := uuid.Parse(attemptID); err != nil || signature == "" {
		return channel.ErrUnauthorized
	}
	event, params, err := channel.ParseTwilioStatus(raw)
	if err != nil {
		return err
	}
	a, err := s.messages.GetOutboundAttempt(ctx, accountID, attemptID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return channel.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if err := s.owner.VerifyTwilioStatus(ctx, outboundTextSnapshot(a), event, params, signature); err != nil {
		return err
	}
	return s.messages.ApplyOutboundStatus(ctx, *a, event.ProviderID, event.Status)
}

func (s *ChannelOutboundService) IngestWhatsAppStatus(ctx context.Context, accountID uint, signature string, raw []byte) error {
	if err := s.ready(); err != nil {
		return err
	}
	events, err := s.owner.ParseWhatsAppStatuses(accountID, signature, raw)
	if err != nil {
		return err
	}
	attempts := make([]*domain.OutboundMessageAttempt, len(events))
	// Preflight the whole status envelope before writing any owner state.
	for i, event := range events {
		// Meta's opaque echo could not be freshly verified from accessible official
		// docs. Only a persisted provider ID authorizes correlation in this release.
		a, err := s.messages.FindOutboundAttemptByProvider(ctx, accountID, event.Provider, event.ProviderAccountID, event.SenderID, event.RecipientID, event.ProviderID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return channel.ErrUnauthorized
		}
		if err != nil {
			return err
		}
		if event.AttemptID != "" && event.AttemptID != a.ID {
			return channel.ErrUnauthorized
		}
		if err := s.owner.CheckBinding(ctx, outboundTextSnapshot(a)); err != nil {
			return err
		}
		attempts[i] = a
	}
	for i, event := range events {
		if err := s.messages.ApplyOutboundStatus(ctx, *attempts[i], event.ProviderID, event.Status); err != nil {
			return err
		}
	}
	return nil
}

// ValidOutboundStatusPath rejects alternate encodings/query strings so the
// configured public URL is exactly the URL used by Twilio's signature SDK.
func ValidOutboundStatusPath(rawPath, rawQuery, attemptID string) bool {
	parsed, err := uuid.Parse(attemptID)
	return err == nil && parsed.String() == attemptID && parsed != uuid.Nil && rawQuery == "" && !strings.Contains(rawPath, "%") && url.PathEscape(attemptID) == attemptID
}
