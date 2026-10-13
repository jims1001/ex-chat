package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

const outboundTestSID = "SM33333333333333333333333333333333"

type outboundMock struct {
	mu           sync.Mutex
	calls        []channel.TextRequest
	lookups      int
	result       channel.Result
	lookup       channel.Result
	beforeReturn func(channel.TextRequest)
}

func (m *outboundMock) SendText(_ context.Context, r channel.TextRequest) channel.Result {
	m.mu.Lock()
	m.calls = append(m.calls, r)
	callback, result := m.beforeReturn, m.result
	m.mu.Unlock()
	if callback != nil {
		callback(r)
	}
	return result
}
func (m *outboundMock) LookupText(_ context.Context, r channel.TextRequest, id string) channel.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookups++
	return m.lookup
}

func outboundServiceFixture(t *testing.T, provider string) (channelFixture, *ChannelOutboundService, *outboundMock, domain.Conversation) {
	t.Helper()
	f := newChannelFixture(t)
	ctx := context.Background()
	if provider == "twilio" {
		setupTwilio(t, f)
		raw := channelTwilioForm("SM22222222222222222222222222222222", "inbound fixture")
		_, err := f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, raw), raw)
		require.NoError(t, err)
	} else {
		setupWhatsApp(t, f)
		raw := channelWhatsAppBody("wamid.inbound", "inbound fixture")
		_, err := f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(raw), raw)
		require.NoError(t, err)
	}
	var conv domain.Conversation
	require.NoError(t, f.dbs["CON"].First(&conv).Error)
	cfg := channelTestConfig()
	cfg.OutboundEnabled = true
	mock := &outboundMock{result: channel.Result{ProviderID: outboundTestSID, Status: domain.MessageStatusProviderAccepted, Disposition: "accepted"}}
	if provider == "whatsapp" {
		mock.result.ProviderID = "wamid.outbound"
	}
	out := NewChannelOutboundService(NewChannelTextOwner(cfg, mock, f.s.inboxes), f.s.conversations, f.s.messages)
	return f, out, mock, conv
}
func outboundServiceMessage(conv domain.Conversation) domain.Message {
	return domain.Message{AccountID: conv.AccountID, ConversationID: conv.ID, SenderType: domain.SenderTypeUser, SenderID: 17, MessageType: domain.MessageTypeOutgoing, ContentType: domain.ContentTypeText, Content: "private outbound body sentinel"}
}
func outboundServiceAttempt(t *testing.T, f channelFixture, id uint) domain.OutboundMessageAttempt {
	t.Helper()
	var a domain.OutboundMessageAttempt
	require.NoError(t, f.dbs["MSG"].Where("message_id = ?", id).Order("sequence DESC").First(&a).Error)
	return a
}
func outboundStatusForm(status, id string) []byte {
	return []byte(url.Values{"AccountSid": {channelTestSID}, "MessageSid": {id}, "From": {"+14155550123"}, "To": {"+14155550999"}, "MessageStatus": {status}, "Extra": {"covered"}}.Encode())
}
func outboundStatusSignature(address string, raw []byte) string {
	params, _ := url.ParseQuery(string(raw))
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data := address
	for _, k := range keys {
		data += k + params.Get(k)
	}
	mac := hmac.New(sha1.New, []byte(channelTestToken))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
func outboundVisible(t *testing.T, f channelFixture, id uint) domain.Message {
	t.Helper()
	var msg domain.Message
	require.NoError(t, f.dbs["MSG"].First(&msg, id).Error)
	return msg
}

func TestOutboundServiceExplicitIntentIdempotencyAndAcceptance(t *testing.T) {
	f, s, mock, conv := outboundServiceFixture(t, "twilio")
	ctx := context.Background()
	msg := outboundServiceMessage(conv)
	original := msg
	created, err := s.CreateText(ctx, &msg, "stable-client-command")
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "queued", msg.Status)
	require.Empty(t, mock.calls)
	a := outboundServiceAttempt(t, f, msg.ID)
	require.Equal(t, "+14155550999", a.RecipientID)
	require.NotContains(t, a.ConfigFingerprint, channelTestToken)
	replay := original
	created, err = s.CreateText(ctx, &replay, "stable-client-command")
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, msg.ID, replay.ID)
	changed := original
	changed.Content = "other content"
	_, err = s.CreateText(ctx, &changed, "stable-client-command")
	require.ErrorIs(t, err, repository.ErrOutboundConflict)
	worked, err := s.DispatchOne(ctx)
	require.NoError(t, err)
	require.True(t, worked)
	require.Len(t, mock.calls, 1)
	require.Equal(t, "provider_accepted", outboundVisible(t, f, msg.ID).Status)
	// Provider acceptance cannot complete either first-response or reply timeline.
	timeline, err := s.messages.ListSLATimeline(conv.AccountID, conv.ID)
	require.NoError(t, err)
	for _, item := range timeline {
		require.NotEqual(t, msg.ID, item.ID)
	}
	replay = original
	created, err = s.CreateText(ctx, &replay, "stable-client-command")
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "provider_accepted", replay.Status)
	worked, err = s.DispatchOne(ctx)
	require.NoError(t, err)
	require.False(t, worked)
	require.Len(t, mock.calls, 1)
	form := outboundStatusForm("sent", outboundTestSID)
	require.NoError(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, form), form))
	require.Equal(t, "sent", outboundVisible(t, f, msg.ID).Status)
	timeline, err = s.messages.ListSLATimeline(conv.AccountID, conv.ID)
	require.NoError(t, err)
	found := false
	for _, item := range timeline {
		if item.ID == msg.ID {
			found = true
			require.True(t, item.CreatedAt.Equal(msg.CreatedAt))
		}
	}
	require.True(t, found)
}

func TestOutboundServiceTwilioLostResponseVerifiedCallbackAndRestart(t *testing.T) {
	f, s, mock, conv := outboundServiceFixture(t, "twilio")
	ctx := context.Background()
	msg := outboundServiceMessage(conv)
	_, err := s.CreateText(ctx, &msg, "")
	require.NoError(t, err)
	a := outboundServiceAttempt(t, f, msg.ID)
	mock.result = channel.Result{Status: "unknown", Disposition: "unknown"}
	mock.beforeReturn = func(r channel.TextRequest) {
		form := outboundStatusForm("delivered", outboundTestSID)
		require.NoError(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(r.CallbackURL, form), form))
	}
	_, err = s.DispatchOne(ctx)
	require.NoError(t, err)
	require.Equal(t, "delivered", outboundVisible(t, f, msg.ID).Status)
	// A lost HTTP result cannot overwrite a verified callback received mid-flight.
	current := outboundServiceAttempt(t, f, msg.ID)
	require.Equal(t, outboundTestSID, current.ProviderMessageID)
	require.Equal(t, "delivered", current.Status)
	restarted := NewChannelOutboundService(s.owner, f.s.conversations, repository.NewMessageRepositoryWithPorts(f.dbs["MSG"], f.s.conversations, nil, f.s.contacts))
	worked, err := restarted.DispatchOne(ctx)
	require.NoError(t, err)
	require.False(t, worked)
	require.Len(t, mock.calls, 1)
	for _, status := range []string{"queued", "sent", "failed", "delivered"} {
		form := outboundStatusForm(status, outboundTestSID)
		require.NoError(t, restarted.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, form), form))
	}
	require.Equal(t, "delivered", outboundVisible(t, f, msg.ID).Status)
}

func TestOutboundServiceUnknownNeverResendsAndSignedBindings(t *testing.T) {
	f, s, mock, conv := outboundServiceFixture(t, "twilio")
	ctx := context.Background()
	msg := outboundServiceMessage(conv)
	_, err := s.CreateText(ctx, &msg, "")
	require.NoError(t, err)
	a := outboundServiceAttempt(t, f, msg.ID)
	mock.result = channel.Result{Status: "unknown", Disposition: "unknown"}
	_, err = s.DispatchOne(ctx)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		worked, err := s.DispatchOne(ctx)
		require.NoError(t, err)
		require.False(t, worked)
	}
	_, _, err = s.RetryText(ctx, 91, conv.ID, msg.ID)
	require.Error(t, err)
	require.Len(t, mock.calls, 1)
	form := outboundStatusForm("delivered", outboundTestSID)
	for _, tc := range []struct {
		account uint
		raw     []byte
		sig     string
	}{
		{91, form, "invalid"}, {92, form, outboundStatusSignature(a.CallbackURL, form)},
		{91, append(append([]byte{}, form...), []byte("&Extra=duplicate")...), outboundStatusSignature(a.CallbackURL, form)},
		{91, []byte(strings.Replace(string(form), "To=%2B14155550999", "To=%2B14155550888", 1)), "invalid"},
	} {
		require.Error(t, s.IngestTwilioStatus(ctx, tc.account, a.ID, tc.sig, tc.raw))
		require.Equal(t, "unknown", outboundVisible(t, f, msg.ID).Status)
	}
	for _, field := range []string{"To", "From", "AccountSid"} {
		vals, _ := url.ParseQuery(string(form))
		if field == "AccountSid" {
			vals.Set(field, "AC99999999999999999999999999999999")
		} else {
			vals.Set(field, "+14155550888")
		}
		raw := []byte(vals.Encode())
		require.Error(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, raw), raw))
	}
	require.NoError(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, form), form))
	require.Equal(t, "delivered", outboundVisible(t, f, msg.ID).Status)
	conflict := outboundStatusForm("read", "SM44444444444444444444444444444444")
	require.Error(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, conflict), conflict))
}

func TestOutboundServiceNoHistoricalDrainAndLocalInvalidation(t *testing.T) {
	for _, mutate := range []string{"body", "delete", "recipient", "connection", "block", "conversation"} {
		t.Run(mutate, func(t *testing.T) {
			f, s, mock, conv := outboundServiceFixture(t, "twilio")
			ctx := context.Background()
			historical := outboundServiceMessage(conv)
			historical.Status = domain.MessageStatusQueued
			require.NoError(t, s.messages.Create(&historical))
			worked, err := s.DispatchOne(ctx)
			require.NoError(t, err)
			require.False(t, worked)
			msg := outboundServiceMessage(conv)
			_, err = s.CreateText(ctx, &msg, "")
			require.NoError(t, err)
			switch mutate {
			case "body":
				_, err = s.messages.UpdateContent(91, conv.ID, msg.ID, "edited")
			case "delete":
				_, err = s.messages.DeleteMessage(91, conv.ID, msg.ID)
			case "recipient":
				err = f.dbs["CUS"].Model(&domain.ContactInbox{}).Where("inbox_id = ?", conv.InboxID).Update("source_id", "+14155550888").Error
			case "connection":
				err = f.dbs["CHN"].Model(&domain.ChannelConnection{}).Where("inbox_id = ?", conv.InboxID).Update("recipient_id", "+14155550888").Error
			case "block":
				err = f.dbs["CUS"].Model(&domain.Contact{}).Where("id = ?", conv.ContactID).Update("blocked", true).Error
			case "conversation":
				err = f.dbs["CON"].Delete(&domain.Conversation{}, conv.ID).Error
			}
			require.NoError(t, err)
			worked, err = s.DispatchOne(ctx)
			require.NoError(t, err)
			require.True(t, worked)
			require.Empty(t, mock.calls)
			require.Equal(t, "cancelled", outboundServiceAttempt(t, f, msg.ID).State)
			worked, err = s.DispatchOne(ctx)
			require.NoError(t, err)
			require.False(t, worked)
		})
	}
}

func TestOutboundServiceRejectRetryAndKnownSIDReconcile(t *testing.T) {
	f, s, mock, conv := outboundServiceFixture(t, "twilio")
	ctx := context.Background()
	msg := outboundServiceMessage(conv)
	_, err := s.CreateText(ctx, &msg, "")
	require.NoError(t, err)
	mock.result = channel.Result{Status: "failed", Disposition: "rejected"}
	_, err = s.DispatchOne(ctx)
	require.NoError(t, err)
	require.Equal(t, "failed", outboundVisible(t, f, msg.ID).Status)
	_, accepted, err := s.RetryText(ctx, 91, conv.ID, msg.ID)
	require.NoError(t, err)
	require.True(t, accepted)
	a := outboundServiceAttempt(t, f, msg.ID)
	require.Equal(t, 2, a.Sequence)
	mock.result = channel.Result{ProviderID: outboundTestSID, Status: "provider_accepted", Disposition: "accepted"}
	_, err = s.DispatchOne(ctx)
	require.NoError(t, err)
	mock.lookup = channel.Result{ProviderID: outboundTestSID, Status: "delivered", Disposition: "accepted"}
	require.NoError(t, s.ReconcileText(ctx, 91, a.ID))
	require.Equal(t, "delivered", outboundVisible(t, f, msg.ID).Status)
	require.Equal(t, 1, mock.lookups)
	require.Len(t, mock.calls, 2)
	mock.lookup = channel.Result{Status: "unknown", Disposition: "unknown"}
	require.Error(t, s.ReconcileText(ctx, 91, a.ID))
	require.Equal(t, "delivered", outboundVisible(t, f, msg.ID).Status)
}

func TestOutboundServiceWhatsAppConfirmedIDAndUnknownCorrelationGate(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			f, s, mock, conv := outboundServiceFixture(t, "whatsapp")
			ctx := context.Background()
			msg := outboundServiceMessage(conv)
			_, err := s.CreateText(ctx, &msg, "")
			require.NoError(t, err)
			a := outboundServiceAttempt(t, f, msg.ID)
			if unknown {
				mock.result = channel.Result{Status: "unknown", Disposition: "unknown"}
			}
			_, err = s.DispatchOne(ctx)
			require.NoError(t, err)
			raw := []byte(fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"789","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"456"},"statuses":[{"id":"wamid.outbound","recipient_id":"14155550999","status":"read","timestamp":"1780000000","biz_opaque_callback_data":%q}]}}]}]}`, a.ID))
			err = s.IngestWhatsAppStatus(ctx, 91, channelWhatsAppSignature(raw), raw)
			if unknown {
				require.Error(t, err)
				require.Equal(t, "unknown", outboundVisible(t, f, msg.ID).Status)
			} else {
				require.NoError(t, err)
				require.Equal(t, "read", outboundVisible(t, f, msg.ID).Status)
			}
			require.Error(t, s.ReconcileText(ctx, 91, a.ID))
			require.Zero(t, mock.lookups)
			require.Len(t, mock.calls, 1)
		})
	}
}

func TestOutboundServiceDisabledUnsupportedAndConcurrentWorkers(t *testing.T) {
	f, s, mock, conv := outboundServiceFixture(t, "twilio")
	ctx := context.Background()
	msg := outboundServiceMessage(conv)
	s.owner.(*channelTextOwner).config.OutboundEnabled = false
	_, err := s.CreateText(ctx, &msg, "")
	require.ErrorIs(t, err, channel.ErrNotConfigured)
	require.Zero(t, msg.ID)
	s.owner.(*channelTextOwner).config.OutboundEnabled = true
	for _, kind := range []string{"image", "video", "file"} {
		invalid := msg
		invalid.ContentType = kind
		_, err := s.CreateText(ctx, &invalid, "")
		require.ErrorIs(t, err, channel.ErrUnsupported)
	}
	_, err = s.CreateText(ctx, &msg, "")
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.DispatchOne(ctx) }()
	}
	wg.Wait()
	require.Len(t, mock.calls, 1)
	require.Equal(t, "provider_accepted", outboundVisible(t, f, msg.ID).Status)
	// Disabling worker dispatch still permits valid status callbacks for in-flight sends.
	a := outboundServiceAttempt(t, f, msg.ID)
	s.owner.(*channelTextOwner).config.OutboundEnabled = false
	raw := outboundStatusForm("delivered", outboundTestSID)
	require.NoError(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, raw), raw))
	before := time.Now()
	runCtx, cancel := context.WithCancel(ctx)
	cancel()
	s.Run(runCtx, nil)
	require.Less(t, time.Since(before), time.Second)
}

func TestOutboundServiceOwnerIdentityCannotUseForeignTenantOrInbox(t *testing.T) {
	for _, wrong := range []string{"tenant", "inbox", "contact", "ambiguous-source", "generic-phone"} {
		t.Run(wrong, func(t *testing.T) {
			f, s, mock, conv := outboundServiceFixture(t, "twilio")
			msg := outboundServiceMessage(conv)
			switch wrong {
			case "tenant":
				msg.AccountID = 92
			case "inbox":
				require.NoError(t, f.dbs["CON"].Model(&domain.Conversation{}).Where("id = ?", conv.ID).Update("inbox_id", conv.InboxID+100).Error)
			case "contact":
				require.NoError(t, f.dbs["CUS"].Model(&domain.Contact{}).Where("id = ?", conv.ContactID).Update("account_id", 92).Error)
			case "ambiguous-source":
				other := domain.Contact{AccountID: 91, Name: "Other"}
				require.NoError(t, f.dbs["CUS"].Create(&other).Error)
				require.NoError(t, f.dbs["CUS"].Create(&domain.ContactInbox{ContactID: other.ID, InboxID: conv.InboxID, SourceID: "+14155550999"}).Error)
			case "generic-phone":
				require.NoError(t, f.dbs["CUS"].Where("inbox_id = ?", conv.InboxID).Delete(&domain.ContactInbox{}).Error)
			}
			_, err := s.CreateText(context.Background(), &msg, "owner-boundary")
			require.Error(t, err)
			require.Zero(t, msg.ID)
			require.Empty(t, mock.calls)
			var count int64
			require.NoError(t, f.dbs["MSG"].Model(&domain.OutboundMessageAttempt{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestOutboundServiceCallbackStoreOutageRemainsRetryable(t *testing.T) {
	f, s, _, conv := outboundServiceFixture(t, "twilio")
	msg := outboundServiceMessage(conv)
	_, err := s.CreateText(context.Background(), &msg, "")
	require.NoError(t, err)
	a := outboundServiceAttempt(t, f, msg.ID)
	pool, err := f.dbs["MSG"].DB()
	require.NoError(t, err)
	require.NoError(t, pool.Close())
	raw := outboundStatusForm("sent", outboundTestSID)
	err = s.IngestTwilioStatus(context.Background(), 91, a.ID, outboundStatusSignature(a.CallbackURL, raw), raw)
	require.Error(t, err)
	require.False(t, errors.Is(err, channel.ErrUnauthorized), "a storage outage must surface as retryable service failure, not permanent authentication rejection")
}
