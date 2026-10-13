package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const channelTestSID = "AC11111111111111111111111111111111"
const channelTestToken = "test-only-token-111111111111111111"

type channelTestProvider struct {
	fail  error
	calls int
}

func (p *channelTestProvider) ExchangeWhatsApp(context.Context, channel.Config, string, string, string) (string, error) {
	p.calls++
	return "test-only-whatsapp-token", p.fail
}
func (p *channelTestProvider) VerifyTwilio(context.Context, string, string, string) error {
	p.calls++
	return p.fail
}

func channelTestConfig() channel.Config {
	return channel.Config{PublicBaseURL: "https://chat.example.test", EncryptionKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))), WhatsAppAppID: "1234", WhatsAppAppSecret: strings.Repeat("s", 32), WhatsAppVerifyToken: strings.Repeat("v", 32), WhatsAppAPIVersion: "v25.0"}
}

type channelFixture struct {
	s   *ChannelIngressService
	p   *channelTestProvider
	dbs map[string]*gorm.DB
}

func newChannelFixture(t *testing.T) channelFixture {
	t.Helper()
	dbs := map[string]*gorm.DB{}
	for _, owner := range []string{"CHN", "CUS", "CON", "MSG"} {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
		if err := database.AutoMigrateModule(db, owner); err != nil {
			t.Fatal(err)
		}
		dbs[owner] = db
	}
	p := &channelTestProvider{}
	return channelFixture{s: newChannelTestService(dbs, p), p: p, dbs: dbs}
}
func newChannelTestService(dbs map[string]*gorm.DB, p channel.Provider) *ChannelIngressService {
	inbox := repository.NewInboxRepositoryWithAccountPort(dbs["CHN"], nil)
	contact := repository.NewContactRepository(dbs["CUS"])
	conv := repository.NewConversationRepositoryWithReferences(dbs["CON"], nil, nil, inbox, contact)
	msg := repository.NewMessageRepositoryWithPorts(dbs["MSG"], conv, nil, contact)
	return NewChannelIngressService(channelTestConfig(), p, inbox, contact, conv, msg)
}
func channelCount(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
func channelTwilioForm(id, text string) []byte {
	return []byte(url.Values{"AccountSid": {channelTestSID}, "MessageSid": {id}, "From": {"+14155550999"}, "To": {"+14155550123"}, "Body": {text}, "NumMedia": {"0"}, "FutureField": {"covered-by-signature"}}.Encode())
}
func channelTwilioSignature(account uint, raw []byte) string {
	params, _ := url.ParseQuery(string(raw))
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data := channelTestConfig().Callback(account, "twilio")
	for _, k := range keys {
		data += k + params.Get(k)
	}
	mac := hmac.New(sha1.New, []byte(channelTestToken))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
func channelWhatsAppBody(id, text string) []byte {
	return []byte(fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"789","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"456"},"contacts":[{"wa_id":"14155550999","profile":{"name":"Sender"}}],"messages":[{"from":"14155550999","id":%q,"timestamp":"1780000000","type":"text","text":{"body":%q}}]}}]}]}`, id, text))
}
func channelWhatsAppSignature(raw []byte) string {
	mac := hmac.New(sha256.New, []byte(channelTestConfig().WhatsAppAppSecret))
	mac.Write(raw)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
func setupTwilio(t *testing.T, f channelFixture) *ChannelSetupResult {
	t.Helper()
	result, err := f.s.CreateTwilio(context.Background(), 91, TwilioSetup{InboxName: "SMS", AccountSID: channelTestSID, AuthToken: channelTestToken, PhoneNumber: "+14155550123", Medium: "sms"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func setupWhatsApp(t *testing.T, f channelFixture) *ChannelSetupResult {
	t.Helper()
	result, err := f.s.AuthorizeWhatsApp(context.Background(), 91, WhatsAppSetup{InboxName: "WhatsApp", Code: "test-only-code", WabaID: "789", PhoneNumberID: "456"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestChannelSetupPersistsEncryptedMappingAndRollsBackConflict(t *testing.T) {
	f := newChannelFixture(t)
	result := setupTwilio(t, f)
	var c domain.ChannelConnection
	if err := f.dbs["CHN"].First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.InboxID != result.InboxID || c.ProviderAccountID != channelTestSID || c.SecretCiphertext == "" || strings.Contains(c.SecretCiphertext, channelTestToken) {
		t.Fatal("missing encrypted mapping")
	}
	encoded, _ := json.Marshal(c)
	if strings.Contains(string(encoded), "ciphertext") || strings.Contains(string(encoded), channelTestToken) {
		t.Fatal("secret exposed in JSON")
	}
	var inbox domain.Inbox
	f.dbs["CHN"].First(&inbox)
	if inbox.ProviderConfig != "" {
		t.Fatal("credential in public provider config")
	}
	if _, err := f.s.CreateTwilio(context.Background(), 92, TwilioSetup{InboxName: "Other", AccountSID: channelTestSID, AuthToken: channelTestToken, PhoneNumber: "+14155550123"}); err == nil {
		t.Fatal("duplicate recipient allowed")
	}
	if n := channelCount(t, f.dbs["CHN"], &domain.Inbox{}); n != 1 {
		t.Fatal("orphan inbox after conflict", n)
	}
	f.p.fail = channel.ErrProvider
	if _, err := f.s.AuthorizeWhatsApp(context.Background(), 91, WhatsAppSetup{Code: "code", WabaID: "789", PhoneNumberID: "456"}); err == nil {
		t.Fatal("failed provider authorization succeeded")
	}
	if n := channelCount(t, f.dbs["CHN"], &domain.Inbox{}); n != 1 {
		t.Fatal("failed provider created inbox", n)
	}
}

func TestChannelTwilioSignedIngressDurableReplayAndConversationReuse(t *testing.T) {
	f := newChannelFixture(t)
	setup := setupTwilio(t, f)
	ctx := context.Background()
	raw := channelTwilioForm("SM22222222222222222222222222222222", "hello")
	for _, test := range []struct {
		account uint
		sig     string
		body    []byte
	}{{91, "", raw}, {91, "invalid", raw}, {92, channelTwilioSignature(92, raw), raw}, {91, channelTwilioSignature(91, raw), append(append([]byte{}, raw...), []byte("&Extra=changed")...)}} {
		if _, err := f.s.IngestTwilio(ctx, test.account, test.sig, test.body); err == nil {
			t.Fatal("untrusted event accepted")
		}
	}
	if n := channelCount(t, f.dbs["CUS"], &domain.Contact{}); n != 0 {
		t.Fatal("untrusted event wrote contact")
	}
	created, err := f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, raw), raw)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	// A new service/repository instance must use durable receipts and mappings.
	f.s = newChannelTestService(f.dbs, f.p)
	created, err = f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, raw), raw)
	if err != nil || created {
		t.Fatal("replay", created, err)
	}
	raw2 := channelTwilioForm("SM33333333333333333333333333333333", "second")
	if _, err := f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, raw2), raw2); err != nil {
		t.Fatal(err)
	}
	for owner, model := range map[string]any{"CUS": &domain.Contact{}, "CON": &domain.Conversation{}} {
		if n := channelCount(t, f.dbs[owner], model); n != 1 {
			t.Fatal(owner, n)
		}
	}
	if n := channelCount(t, f.dbs["CUS"], &domain.ContactInbox{}); n != 1 {
		t.Fatal(n)
	}
	if n := channelCount(t, f.dbs["MSG"], &domain.Message{}); n != 2 {
		t.Fatal(n)
	}
	var conv domain.Conversation
	f.dbs["CON"].First(&conv)
	if conv.InboxID != setup.InboxID || conv.AccountID != 91 || conv.UnreadCount != 2 {
		t.Fatal(conv)
	}
	changed := channelTwilioForm("SM22222222222222222222222222222222", "changed")
	if _, err := f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, changed), changed); !errors.Is(err, repository.ErrChannelReplayConflict) {
		t.Fatal("conflicting replay", err)
	}
	if err := f.dbs["CON"].Model(&conv).Update("status", "resolved").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, raw), raw); err != nil {
		t.Fatal(err)
	}
	if n := channelCount(t, f.dbs["CON"], &domain.Conversation{}); n != 1 {
		t.Fatal("replay created empty session")
	}
	raw3 := channelTwilioForm("SM44444444444444444444444444444444", "new session")
	if _, err := f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, raw3), raw3); err != nil {
		t.Fatal(err)
	}
	if n := channelCount(t, f.dbs["CON"], &domain.Conversation{}); n != 2 {
		t.Fatal(n)
	}
	for owner, foreign := range map[string]string{"CHN": "contacts", "CUS": "conversations", "CON": "messages", "MSG": "inboxes"} {
		if f.dbs[owner].Migrator().HasTable(foreign) {
			t.Fatal("cross-owner table", owner, foreign)
		}
	}
}

func TestChannelWhatsAppSignedRecipientRoutingAndFullBatchPreflight(t *testing.T) {
	f := newChannelFixture(t)
	setupWhatsApp(t, f)
	ctx := context.Background()
	cfg := channelTestConfig()
	if err := f.s.VerifyWhatsApp(ctx, 91, "subscribe", "wrong", "challenge"); err == nil {
		t.Fatal("bad verify token")
	}
	if err := f.s.VerifyWhatsApp(ctx, 92, "subscribe", cfg.WhatsAppVerifyToken, "challenge"); err == nil {
		t.Fatal("foreign handshake")
	}
	if err := f.s.VerifyWhatsApp(ctx, 91, "subscribe", cfg.WhatsAppVerifyToken, "challenge"); err != nil {
		t.Fatal(err)
	}
	raw := channelWhatsAppBody("wamid.test1", "hello")
	if _, err := f.s.IngestWhatsApp(ctx, 91, "", raw); err == nil {
		t.Fatal("unsigned body")
	}
	unknown := []byte(strings.Replace(string(raw), `"phone_number_id":"456"`, `"phone_number_id":"999"`, 1))
	if _, err := f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(unknown), unknown); err == nil {
		t.Fatal("unmapped recipient")
	}
	media := []byte(strings.Replace(string(raw), `"type":"text"`, `"type":"image"`, 1))
	if _, err := f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(media), media); !errors.Is(err, channel.ErrUnsupported) {
		t.Fatal(err)
	}
	// One valid and one foreign recipient in the same envelope: neither writes.
	var first, second map[string]any
	json.Unmarshal(raw, &first)
	json.Unmarshal(unknown, &second)
	first["entry"] = append(first["entry"].([]any), second["entry"].([]any)...)
	batch, _ := json.Marshal(first)
	if _, err := f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(batch), batch); err == nil {
		t.Fatal("mixed batch")
	}
	if n := channelCount(t, f.dbs["MSG"], &domain.Message{}); n != 0 {
		t.Fatal(n)
	}
	count, err := f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(raw), raw)
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	count, err = f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(raw), raw)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if n := channelCount(t, f.dbs["CUS"], &domain.ContactInbox{}); n != 1 {
		t.Fatal(n)
	}
}

func TestChannelWriteFailureRollsBackReceiptAndRetryReusesOwners(t *testing.T) {
	f := newChannelFixture(t)
	setupTwilio(t, f)
	raw := channelTwilioForm("SM22222222222222222222222222222222", "hello")
	sig := channelTwilioSignature(91, raw)
	ctx := context.Background()
	f.dbs["MSG"].Callback().Create().Before("gorm:create").Register("test:fail-channel-message", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Message" {
			tx.AddError(errors.New("injected persistence failure"))
		}
	})
	if _, err := f.s.IngestTwilio(ctx, 91, sig, raw); err == nil {
		t.Fatal("write failure acknowledged")
	}
	if n := channelCount(t, f.dbs["MSG"], &domain.ChannelMessageReceipt{}); n != 0 {
		t.Fatal("receipt escaped rollback")
	}
	f.dbs["MSG"].Callback().Create().Remove("test:fail-channel-message")
	f.s = newChannelTestService(f.dbs, f.p)
	if created, err := f.s.IngestTwilio(ctx, 91, sig, raw); err != nil || !created {
		t.Fatal(created, err)
	}
	if n := channelCount(t, f.dbs["CUS"], &domain.Contact{}); n != 1 {
		t.Fatal(n)
	}
	if n := channelCount(t, f.dbs["CON"], &domain.Conversation{}); n != 1 {
		t.Fatal(n)
	}
	if n := channelCount(t, f.dbs["MSG"], &domain.Message{}); n != 1 {
		t.Fatal(n)
	}
}

func TestChannelRevocationAndDeletedInboxFailClosed(t *testing.T) {
	for _, remove := range []string{"inbox", "account"} {
		t.Run(remove, func(t *testing.T) {
			f := newChannelFixture(t)
			setup := setupTwilio(t, f)
			ctx := context.Background()
			status, err := f.s.inboxes.AccountDeletionStatus(91)
			if err != nil || status.CredentialCount < 1 || status.Ready {
				t.Fatal(status, err)
			}
			if remove == "inbox" {
				err = f.s.inboxes.Delete(91, setup.InboxID)
			} else {
				_, err = f.s.inboxes.RevokeAccountChannels(91)
			}
			if err != nil {
				t.Fatal(err)
			}
			var tombstone domain.ChannelConnection
			if err := f.dbs["CHN"].First(&tombstone).Error; err != nil {
				t.Fatal(err)
			}
			if tombstone.RevokedAt == nil || tombstone.SecretCiphertext != "" || tombstone.RecipientID != "" || tombstone.ProviderAccountID != "" {
				t.Fatal("revocation did not erase credentials/raw recipient")
			}
			raw := channelTwilioForm("SM22222222222222222222222222222222", "hello")
			if _, err := f.s.IngestTwilio(ctx, 91, channelTwilioSignature(91, raw), raw); err == nil {
				t.Fatal("revoked ingress accepted")
			}
			if n := channelCount(t, f.dbs["CUS"], &domain.Contact{}); n != 0 {
				t.Fatal(n)
			}
		})
	}
}

func TestChannelConversationDeletionRaceCancelsOrphanAndRetainsTombstone(t *testing.T) {
	f := newChannelFixture(t)
	setupTwilio(t, f)
	ctx := context.Background()
	raw := channelTwilioForm("SM22222222222222222222222222222222", "hello")
	sig := channelTwilioSignature(91, raw)
	f.dbs["MSG"].Callback().Create().After("gorm:create").Register("test:delete-channel-conversation", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Message" {
			if err := f.dbs["CON"].Model(&domain.Conversation{}).Where("account_id = ?", 91).Update("status", domain.ConversationStatusDeleting).Error; err != nil {
				tx.AddError(err)
			}
		}
	})
	if _, err := f.s.IngestTwilio(ctx, 91, sig, raw); err == nil {
		t.Fatal("deletion-racing ingress acknowledged as created")
	}
	if n := channelCount(t, f.dbs["MSG"], &domain.Message{}); n != 0 {
		t.Fatal("orphan persisted", n)
	}
	if n := channelCount(t, f.dbs["MSG"], &domain.ChannelMessageReceipt{}); n != 1 {
		t.Fatal("missing deletion tombstone", n)
	}
	f.dbs["MSG"].Callback().Create().Remove("test:delete-channel-conversation")
	if created, err := f.s.IngestTwilio(ctx, 91, sig, raw); err != nil || created {
		t.Fatal("retry resurrected deleted content", created, err)
	}
}

func TestChannelSenderCannotBePreclaimedByGenericIdentifier(t *testing.T) {
	f := newChannelFixture(t)
	setup := setupTwilio(t, f)
	attacker := domain.Contact{AccountID: 91, Identifier: "+14155550999", Name: "Visitor controlled"}
	if err := f.dbs["CUS"].Omit("Company", "Labels").Create(&attacker).Error; err != nil {
		t.Fatal(err)
	}
	raw := channelTwilioForm("SM22222222222222222222222222222222", "victim text")
	if _, err := f.s.IngestTwilio(context.Background(), 91, channelTwilioSignature(91, raw), raw); err != nil {
		t.Fatal(err)
	}
	var msg domain.Message
	f.dbs["MSG"].First(&msg)
	if msg.SenderID == attacker.ID {
		t.Fatal("generic identifier hijacked trusted channel")
	}
	var relation domain.ContactInbox
	f.dbs["CUS"].First(&relation)
	if relation.ContactID == attacker.ID || relation.InboxID != setup.InboxID {
		t.Fatal(relation)
	}
}

func TestChannelRevokedRecipientCannotTransferCapturedWhatsAppEvent(t *testing.T) {
	f := newChannelFixture(t)
	setup := setupWhatsApp(t, f)
	ctx := context.Background()
	raw := channelWhatsAppBody("wamid.captured", "old event")
	if _, err := f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(raw), raw); err != nil {
		t.Fatal(err)
	}
	if err := f.s.inboxes.Delete(91, setup.InboxID); err != nil {
		t.Fatal(err)
	}
	for _, account := range []uint{91, 92} {
		if _, err := f.s.AuthorizeWhatsApp(ctx, account, WhatsAppSetup{Code: "another-code", WabaID: "789", PhoneNumberID: "456"}); err == nil {
			t.Fatal("revoked recipient rebound", account)
		}
		if _, err := f.s.IngestWhatsApp(ctx, account, channelWhatsAppSignature(raw), raw); err == nil {
			t.Fatal("captured event accepted after revocation", account)
		}
	}
	if n := channelCount(t, f.dbs["CHN"], &domain.Inbox{}); n != 0 {
		t.Fatal("failed rebind created orphan inbox", n)
	}
	if n := channelCount(t, f.dbs["MSG"], &domain.Message{}); n != 1 {
		t.Fatal("captured event duplicated", n)
	}
}

func TestChannelContactMergeRejectedBeforeOwnerMutation(t *testing.T) {
	f := newChannelFixture(t)
	setupTwilio(t, f)
	raw := channelTwilioForm("SM22222222222222222222222222222222", "hello")
	if _, err := f.s.IngestTwilio(context.Background(), 91, channelTwilioSignature(91, raw), raw); err != nil {
		t.Fatal(err)
	}
	var original domain.Contact
	f.dbs["CUS"].First(&original)
	base := domain.Contact{AccountID: 91, Name: "Merge target"}
	if err := f.dbs["CUS"].Omit("Company", "Labels").Create(&base).Error; err != nil {
		t.Fatal(err)
	}
	// Even with missing TKT/BIL/OPS tables, the explicit conflict must win before
	// any owner ports are invoked, leaving all references intact.
	if err := f.s.contacts.MergeContacts(91, base.ID, original.ID); !errors.Is(err, repository.ErrChannelContactMergeUnsupported) {
		t.Fatal(err)
	}
	var conv domain.Conversation
	f.dbs["CON"].First(&conv)
	if conv.ContactID != original.ID {
		t.Fatal("conversation moved before conflict")
	}
	var msg domain.Message
	f.dbs["MSG"].First(&msg)
	if msg.SenderID != original.ID {
		t.Fatal("message moved before conflict")
	}
	next := channelTwilioForm("SM33333333333333333333333333333333", "after rejected merge")
	if _, err := f.s.IngestTwilio(context.Background(), 91, channelTwilioSignature(91, next), next); err != nil {
		t.Fatal("rejected merge broke ingress", err)
	}
}

func TestChannelCorruptWhatsAppCredentialFailsIngressAndHandshake(t *testing.T) {
	f := newChannelFixture(t)
	setupWhatsApp(t, f)
	if err := f.dbs["CHN"].Model(&domain.ChannelConnection{}).Where("account_id = ?", 91).Update("secret_ciphertext", "corrupt").Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	raw := channelWhatsAppBody("wamid.test", "hello")
	if _, err := f.s.IngestWhatsApp(ctx, 91, channelWhatsAppSignature(raw), raw); !errors.Is(err, channel.ErrNotConfigured) {
		t.Fatal(err)
	}
	if err := f.s.VerifyWhatsApp(ctx, 91, "subscribe", channelTestConfig().WhatsAppVerifyToken, "challenge"); !errors.Is(err, channel.ErrNotConfigured) {
		t.Fatal(err)
	}
	if n := channelCount(t, f.dbs["CUS"], &domain.Contact{}); n != 0 {
		t.Fatal(n)
	}
}

func TestChannelHealthDoesNotInventProviderState(t *testing.T) {
	f := newChannelFixture(t)
	setup := setupTwilio(t, f)
	health, err := f.s.inboxes.CheckChannelHealth(91, setup.InboxID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "configured_not_probed" || health.Verified || health.QualityRating != "UNKNOWN" || health.MessagingLimit != "UNKNOWN" {
		t.Fatal(health)
	}
}
