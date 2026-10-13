package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The same cases run against independent SQLite files and PostgreSQL schemas.
// Only CHN receives its connection store, configuration and synthetic transport.
func TestChannelTextOwnerIsolation(t *testing.T) {
	runChannelTextOwnerIsolation(t, func(t *testing.T, owner string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		return db
	})
}

func TestPostgresChannelTextOwnerIsolation(t *testing.T) {
	runChannelTextOwnerIsolation(t, reportWindowTestPostgres(t))
}

type textOwnerProbe struct {
	channel.TextOwner
	afterPrepare func(channel.PreparedText)
	afterBinding func()
	bindings     int
}

func (p *textOwnerProbe) PrepareDispatch(ctx context.Context, snapshot channel.TextSnapshot) (channel.PreparedText, error) {
	prepared, err := p.TextOwner.PrepareDispatch(ctx, snapshot)
	if err == nil && p.afterPrepare != nil {
		p.afterPrepare(prepared)
	}
	return prepared, err
}

func (p *textOwnerProbe) CheckBinding(ctx context.Context, snapshot channel.TextSnapshot) error {
	err := p.TextOwner.CheckBinding(ctx, snapshot)
	p.bindings++
	if err == nil && p.afterBinding != nil {
		p.afterBinding()
	}
	return err
}

func runChannelTextOwnerIsolation(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	fixture := func(t *testing.T, provider string) (channelFixture, *ChannelOutboundService, *outboundMock, domain.Conversation) {
		t.Helper()
		dbs := map[string]*gorm.DB{}
		for _, name := range []string{"CHN", "CUS", "CON", "MSG", "OPS"} {
			db := open(t, name)
			require.NoError(t, database.AutoMigrateModule(db, name))
			dbs[name] = db
		}
		f := channelFixture{dbs: dbs, p: &channelTestProvider{}}
		f.s = newChannelTestService(dbs, f.p)
		if provider == "twilio" {
			setupTwilio(t, f)
			raw := channelTwilioForm("SM22222222222222222222222222222222", "inbound fixture")
			_, err := f.s.IngestTwilio(context.Background(), 91, channelTwilioSignature(91, raw), raw)
			require.NoError(t, err)
		} else {
			setupWhatsApp(t, f)
			raw := channelWhatsAppBody("wamid.inbound", "inbound fixture")
			_, err := f.s.IngestWhatsApp(context.Background(), 91, channelWhatsAppSignature(raw), raw)
			require.NoError(t, err)
		}
		var conv domain.Conversation
		require.NoError(t, dbs["CON"].First(&conv).Error)
		cfg := channelTestConfig()
		cfg.OutboundEnabled = true
		transport := &outboundMock{result: channel.Result{ProviderID: outboundTestSID, Status: "provider_accepted", Disposition: "accepted"}}
		if provider == "whatsapp" {
			transport.result.ProviderID = "wamid.outbound"
		}
		return f, NewChannelOutboundService(NewChannelTextOwner(cfg, transport, f.s.inboxes), f.s.conversations, f.s.messages), transport, conv
	}
	ctx := context.Background()
	for _, provider := range []string{"twilio", "whatsapp"} {
		t.Run(provider+"_separate_stores_and_opaque_capability", func(t *testing.T) {
			f, s, transport, conv := fixture(t, provider)
			msg := outboundServiceMessage(conv)
			_, err := s.CreateText(ctx, &msg, "bounded-intent")
			require.NoError(t, err)
			a := outboundServiceAttempt(t, f, msg.ID)
			var connection domain.ChannelConnection
			require.NoError(t, f.dbs["CHN"].First(&connection).Error)
			secret := channelTestToken
			if provider == "whatsapp" {
				secret = "test-only-whatsapp-token"
			}
			for _, name := range []string{"MSG", "OPS"} {
				require.False(t, f.dbs[name].Migrator().HasTable(&domain.ChannelConnection{}))
				tables, err := f.dbs[name].Migrator().GetTables()
				require.NoError(t, err)
				for _, table := range tables {
					var rows []map[string]any
					require.NoError(t, f.dbs[name].Table(table).Find(&rows).Error)
					encoded, err := json.Marshal(rows)
					require.NoError(t, err)
					for _, excluded := range []string{secret, connection.SecretCiphertext, channelTestConfig().EncryptionKey, channelTestConfig().WhatsAppAppSecret} {
						require.NotContains(t, string(encoded), excluded, name+"/"+table)
					}
				}
			}
			require.False(t, f.dbs["CHN"].Migrator().HasTable(&domain.OutboundMessageAttempt{}))
			encoded, err := json.Marshal(outboundTextSnapshot(&a))
			require.NoError(t, err)
			require.NotContains(t, string(encoded), secret)
			require.NotContains(t, string(encoded), connection.SecretCiphertext)
			require.Equal(t, channel.StableKey(fmt.Sprint(connection.ID), fmt.Sprint(connection.AccountID), fmt.Sprint(connection.InboxID), connection.Provider, connection.ProviderAccountID, connection.RecipientID, connection.RecipientKey, connection.SecretCiphertext, channelTestConfig().PublicBaseURL, channelTestConfig().WhatsAppAPIVersion, channelTestConfig().WhatsAppAppID), a.ConfigFingerprint)
			probe := &textOwnerProbe{TextOwner: s.owner}
			probe.afterPrepare = func(prepared channel.PreparedText) {
				require.Empty(t, transport.calls)
				current := outboundServiceAttempt(t, f, msg.ID)
				require.Nil(t, current.DispatchStartedAt)
				typ := reflect.TypeOf(prepared)
				require.Equal(t, reflect.Func, typ.Kind())
				require.Equal(t, 1, typ.NumMethod())
				require.Equal(t, "Send", typ.Method(0).Name)
				for _, formatted := range []string{fmt.Sprintf("%+v", prepared), fmt.Sprintf("%#v", prepared)} {
					require.NotContains(t, formatted, secret)
					require.NotContains(t, formatted, connection.SecretCiphertext)
				}
				_, err := json.Marshal(prepared)
				require.Error(t, err, "opaque capability must not serialize a request")
			}
			s.owner = probe
			transport.beforeReturn = func(request channel.TextRequest) {
				require.Equal(t, secret, request.Credential)
				require.Equal(t, a.Body, request.Body)
				current := outboundServiceAttempt(t, f, msg.ID)
				require.NotNil(t, current.DispatchStartedAt, "MSG must commit the dispatch fence before provider I/O")
			}
			worked, err := s.DispatchOne(ctx)
			require.NoError(t, err)
			require.True(t, worked)
			require.Len(t, transport.calls, 1)
			require.Equal(t, domain.OutboundStateSettled, outboundServiceAttempt(t, f, msg.ID).State)
			require.Equal(t, "provider_accepted", outboundVisible(t, f, msg.ID).Status)
		})
	}
	t.Run("dispatch_fence_cancels_prepared_capability", func(t *testing.T) {
		f, s, transport, conv := fixture(t, "twilio")
		msg := outboundServiceMessage(conv)
		_, err := s.CreateText(ctx, &msg, "")
		require.NoError(t, err)
		s.owner = &textOwnerProbe{TextOwner: s.owner, afterPrepare: func(channel.PreparedText) {
			require.NoError(t, f.dbs["MSG"].Model(&domain.Message{}).Where("id = ?", msg.ID).Update("deleted", true).Error)
		}}
		worked, err := s.DispatchOne(ctx)
		require.NoError(t, err)
		require.True(t, worked)
		require.Empty(t, transport.calls)
		a := outboundServiceAttempt(t, f, msg.ID)
		require.Equal(t, "cancelled", a.State)
		require.Nil(t, a.DispatchStartedAt)
	})
	t.Run("rotation_after_preparation_does_not_revalidate", func(t *testing.T) {
		f, s, transport, conv := fixture(t, "twilio")
		msg := outboundServiceMessage(conv)
		_, err := s.CreateText(ctx, &msg, "")
		require.NoError(t, err)
		reads := 0
		probe := &textOwnerProbe{TextOwner: s.owner}
		probe.afterPrepare = func(channel.PreparedText) {
			require.NoError(t, f.dbs["CHN"].Model(&domain.ChannelConnection{}).Where("inbox_id = ?", conv.InboxID).Update("revoked_at", time.Now().UTC()).Error)
			require.NoError(t, f.dbs["CUS"].Model(&domain.ContactInbox{}).Where("inbox_id = ?", conv.InboxID).Update("source_id", "+14155550888").Error)
			for _, owner := range []string{"CHN", "CON", "CUS"} {
				db := f.dbs[owner]
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("forbid_post_prepare_read", func(tx *gorm.DB) { reads++; tx.AddError(errors.New("unexpected post-preparation owner read")) }))
				t.Cleanup(func() { db.Callback().Query().Remove("forbid_post_prepare_read") })
			}
		}
		s.owner = probe
		worked, err := s.DispatchOne(ctx)
		require.NoError(t, err)
		require.True(t, worked)
		require.Zero(t, reads)
		require.Len(t, transport.calls, 1)
		require.Equal(t, channelTestToken, transport.calls[0].Credential)
		require.Equal(t, "+14155550999", transport.calls[0].RecipientID)
	})
	t.Run("decrypt_failure_retains_retryable_claim", func(t *testing.T) {
		f, s, transport, conv := fixture(t, "twilio")
		msg := outboundServiceMessage(conv)
		_, err := s.CreateText(ctx, &msg, "")
		require.NoError(t, err)
		cfg := channelTestConfig()
		cfg.OutboundEnabled = true
		cfg.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
		s.owner = NewChannelTextOwner(cfg, transport, f.s.inboxes)
		worked, err := s.DispatchOne(ctx)
		require.ErrorIs(t, err, channel.ErrNotConfigured)
		require.True(t, worked)
		require.Empty(t, transport.calls)
		a := outboundServiceAttempt(t, f, msg.ID)
		require.Equal(t, "claimed", a.State)
		require.Equal(t, "queued", a.Status)
		require.Nil(t, a.DispatchStartedAt)
		require.NotEmpty(t, a.ClaimToken)
	})
	t.Run("rotation_before_preparation_cancels_attempt", func(t *testing.T) {
		f, s, transport, conv := fixture(t, "twilio")
		msg := outboundServiceMessage(conv)
		_, err := s.CreateText(ctx, &msg, "")
		require.NoError(t, err)
		require.NoError(t, f.dbs["CHN"].Model(&domain.ChannelConnection{}).Where("inbox_id = ?", conv.InboxID).Update("secret_ciphertext", "changed ciphertext").Error)
		worked, err := s.DispatchOne(ctx)
		require.NoError(t, err)
		require.True(t, worked)
		require.Empty(t, transport.calls)
		require.Equal(t, "cancelled", outboundServiceAttempt(t, f, msg.ID).State)
	})
	t.Run("twilio_lookup_and_callback_remain_owner_verified", func(t *testing.T) {
		f, s, transport, conv := fixture(t, "twilio")
		msg := outboundServiceMessage(conv)
		_, err := s.CreateText(ctx, &msg, "")
		require.NoError(t, err)
		_, err = s.DispatchOne(ctx)
		require.NoError(t, err)
		a := outboundServiceAttempt(t, f, msg.ID)
		transport.lookup = channel.Result{ProviderID: outboundTestSID, Status: "sent", Disposition: "accepted"}
		require.NoError(t, s.ReconcileText(ctx, 91, a.ID))
		require.Equal(t, 1, transport.lookups)
		form := outboundStatusForm("delivered", outboundTestSID)
		require.ErrorIs(t, s.IngestTwilioStatus(ctx, 91, a.ID, "invalid", form), channel.ErrUnauthorized)
		require.Equal(t, "sent", outboundVisible(t, f, msg.ID).Status)
		require.NoError(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, form), form))
		require.Equal(t, "delivered", outboundVisible(t, f, msg.ID).Status)
		require.NoError(t, f.dbs["CHN"].Model(&domain.ChannelConnection{}).Where("inbox_id = ?", conv.InboxID).Update("secret_ciphertext", "rotated").Error)
		require.ErrorIs(t, s.ReconcileText(ctx, 91, a.ID), channel.ErrUnauthorized)
		require.ErrorIs(t, s.IngestTwilioStatus(ctx, 91, a.ID, outboundStatusSignature(a.CallbackURL, form), form), channel.ErrUnauthorized)
		require.Equal(t, 1, transport.lookups)
		require.Len(t, transport.calls, 1)
	})
	t.Run("whatsapp_envelope_preflights_all_bindings", func(t *testing.T) {
		f, s, transport, conv := fixture(t, "whatsapp")
		first, second := outboundServiceMessage(conv), outboundServiceMessage(conv)
		_, err := s.CreateText(ctx, &first, "first")
		require.NoError(t, err)
		_, err = s.DispatchOne(ctx)
		require.NoError(t, err)
		transport.result.ProviderID = "wamid.second"
		_, err = s.CreateText(ctx, &second, "second")
		require.NoError(t, err)
		_, err = s.DispatchOne(ctx)
		require.NoError(t, err)
		probe := &textOwnerProbe{TextOwner: s.owner}
		probe.afterBinding = func() {
			if probe.bindings == 1 {
				require.NoError(t, f.dbs["CHN"].Model(&domain.ChannelConnection{}).Where("inbox_id = ?", conv.InboxID).Update("secret_ciphertext", "rotated").Error)
			}
		}
		s.owner = probe
		raw := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"789","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"456"},"statuses":[{"id":"wamid.outbound","recipient_id":"14155550999","status":"read","timestamp":"1780000000"},{"id":"wamid.second","recipient_id":"14155550999","status":"read","timestamp":"1780000000"}]}}]}]}`)
		require.ErrorIs(t, s.IngestWhatsAppStatus(ctx, 91, channelWhatsAppSignature(raw), raw), channel.ErrUnauthorized)
		require.Equal(t, 2, probe.bindings)
		require.Equal(t, "provider_accepted", outboundVisible(t, f, first.ID).Status)
		require.Equal(t, "provider_accepted", outboundVisible(t, f, second.ID).Status)
	})
	t.Run("whatsapp_binding_does_not_decrypt", func(t *testing.T) {
		f, s, _, conv := fixture(t, "whatsapp")
		msg := outboundServiceMessage(conv)
		_, err := s.CreateText(ctx, &msg, "")
		require.NoError(t, err)
		_, err = s.DispatchOne(ctx)
		require.NoError(t, err)
		owner := s.owner.(*channelTextOwner)
		owner.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
		raw := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"789","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"456"},"statuses":[{"id":"wamid.outbound","recipient_id":"14155550999","status":"read","timestamp":"1780000000"}]}}]}]}`)
		require.NoError(t, s.IngestWhatsAppStatus(ctx, 91, channelWhatsAppSignature(raw), raw))
		require.Equal(t, "read", outboundVisible(t, f, msg.ID).Status)
	})
}
