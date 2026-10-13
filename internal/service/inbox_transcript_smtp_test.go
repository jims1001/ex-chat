package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

// Reflection allows this regression to execute against the frozen legacy
// selector, which lacked any durable, password-free Inbox route proof.
func TestManagedTranscriptInboxBindingProvenance(t *testing.T) {
	service := &EmailService{fromEmail: "fallback@example.test"}
	conv := &domain.Conversation{AccountID: 17, InboxID: 29, Inbox: &domain.Inbox{ID: 29, AccountID: 17, CreatedAt: time.Date(2026, 10, 7, 1, 2, 3, 123456789, time.UTC), ProviderConfig: `{"smtp_address":"127.0.0.1","smtp_port":"2525","smtp_username":"route@example.test","smtp_password":"disposable-password"}`}}
	sender, from := service.getSenderForConversation(conv)
	require.NotNil(t, sender)
	require.Equal(t, "route@example.test", from)
	method := reflect.ValueOf(service).MethodByName("ManagedTranscriptInboxBinding")
	require.True(t, method.IsValid(), "selected Inbox SMTP needs durable password-free owner route provenance")
	output := method.Call([]reflect.Value{reflect.ValueOf(conv)})
	require.Len(t, output, 2)
	require.True(t, output[1].Bool())
	require.False(t, output[0].IsNil())
	encoded, err := json.Marshal(output[0].Interface())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "disposable-password")
	require.NotContains(t, string(encoded), "127.0.0.1")
	require.NotContains(t, string(encoded), "route@example.test")
	require.Contains(t, string(encoded), "inbox_smtp_v1")
}

type inboxSMTPTestRepo struct {
	mu         sync.Mutex
	inbox      *domain.Inbox
	calls      int
	beforeRead func(int, *domain.Inbox) (*domain.Inbox, error)
}

func (r *inboxSMTPTestRepo) FindInboxSMTP(ctx context.Context, accountID, inboxID uint) (*domain.Inbox, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if r.beforeRead != nil {
		return r.beforeRead(r.calls, r.inbox)
	}
	if r.inbox == nil {
		return nil, nil
	}
	clone := *r.inbox
	return &clone, nil
}

func inboxSMTPTestConversation(t *testing.T, sender *SMTPEmailSender) *domain.Conversation {
	t.Helper()
	config, err := json.Marshal(map[string]any{"smtp_address": sender.Host, "smtp_port": sender.Port, "smtp_username": sender.Username, "smtp_password": sender.Password, "smtp_enabled": true})
	require.NoError(t, err)
	return &domain.Conversation{AccountID: 17, InboxID: 29, Inbox: &domain.Inbox{ID: 29, AccountID: 17, CreatedAt: time.Date(2026, 10, 7, 1, 2, 3, 123456789, time.UTC), ProviderConfig: string(config)}}
}

func inboxSMTPTestChange(t *testing.T, inbox *domain.Inbox, key string, value any) {
	t.Helper()
	var cfg map[string]any
	require.NoError(t, json.Unmarshal([]byte(inbox.ProviderConfig), &cfg))
	cfg[key] = value
	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	inbox.ProviderConfig = string(encoded)
}

func TestManagedTranscriptInboxBindingSelectorAndFingerprint(t *testing.T) {
	s := &EmailService{fromEmail: "fallback@example.test"}
	conv := inboxSMTPTestConversation(t, NewSMTPEmailSender("127.0.0.1", "2525", "route@example.test", "disposable-original"))
	binding, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.NotNil(t, binding)
	require.NoError(t, domain.ValidateInboxSMTPBinding(*binding))
	require.Equal(t, "17", binding.AccountID)
	require.Equal(t, "29", binding.InboxID)
	require.Equal(t, "2026-10-07T01:02:03.123456789Z", binding.CreatedAt)

	inboxSMTPTestChange(t, conv.Inbox, "smtp_password", "disposable-rotated")
	inboxSMTPTestChange(t, conv.Inbox, "unrelated_provider_setting", map[string]any{"setting": "new"})
	rotated, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.Equal(t, binding, rotated, "password rotation and unrelated fields preserve route identity")
	conv.Inbox.ProviderConfig = `{"address":"127.0.0.1","port":2525,"smtp_login":"route@example.test","smtp_password":"disposable-rotated"}`
	aliased, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.Equal(t, binding, aliased, "all legacy aliases share the same selected route")

	for _, tc := range []struct {
		name, key string
		value     any
	}{
		{"host", "smtp_address", "localhost"},
		{"port", "smtp_port", "2526"},
		{"user-and-from", "smtp_username", "other@example.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyConv := *conv
			copyInbox := *conv.Inbox
			copyConv.Inbox = &copyInbox
			inboxSMTPTestChange(t, &copyInbox, tc.key, tc.value)
			changed, selected := s.ManagedTranscriptInboxBinding(&copyConv)
			require.True(t, selected)
			require.NotNil(t, changed)
			require.NotEqual(t, binding.Fingerprint, changed.Fingerprint)
		})
	}

	// Primary aliases win even when secondary aliases differ.
	conv.Inbox.ProviderConfig = `{"smtp_address":"127.0.0.1","address":"localhost","smtp_port":"2525","port":2526,"smtp_username":"route@example.test","smtp_login":"other@example.test","smtp_password":"disposable"}`
	priority, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.Equal(t, binding, priority)

	// Authentication usernames without @ retain the actual fallback From.
	conv.Inbox.ProviderConfig = `{"smtp_address":"127.0.0.1","smtp_username":"local-user","smtp_password":"disposable"}`
	fallback, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.NotNil(t, fallback)
	legacy, from := s.getSenderForConversation(conv)
	require.Equal(t, "587", legacy.(*SMTPEmailSender).Port)
	require.Equal(t, "fallback@example.test", from)
	s.SetFromEmail("changed@example.test")
	changed, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.NotEqual(t, fallback.Fingerprint, changed.Fingerprint)
}

func TestManagedTranscriptInboxBindingUnprovableSelectedRouteFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*domain.Conversation)
	}{
		{"zero-created", func(c *domain.Conversation) { c.Inbox.CreatedAt = time.Time{} }},
		{"wrong-account", func(c *domain.Conversation) { c.Inbox.AccountID++ }},
		{"wrong-inbox", func(c *domain.Conversation) { c.Inbox.ID++ }},
		{"zero-account", func(c *domain.Conversation) { c.AccountID = 0 }},
		{"zero-inbox", func(c *domain.Conversation) { c.InboxID = 0 }},
		{"missing-password", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "smtp_password", "") }},
		{"oauth", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "smtp_authentication", "oauth2") }},
		{"oauth-casing", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "SMTP_AUTHENTICATION", "oauth2") }},
		{"disable-starttls", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "smtp_enable_starttls_auto", false) }},
		{"transport-override", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "smtp_mode", "implicit_tls") }},
		{"oauth-enabled", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "oauth_enabled", true) }},
		{"bad-host", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "smtp_address", " 127.0.0.1") }},
		{"bad-port", func(c *domain.Conversation) { inboxSMTPTestChange(t, c.Inbox, "smtp_port", "65536") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &EmailService{fromEmail: "fallback@example.test"}
			conv := inboxSMTPTestConversation(t, NewSMTPEmailSender("127.0.0.1", "2525", "route@example.test", "disposable"))
			tc.mutate(conv)
			legacy, _ := s.getSenderForConversation(conv)
			require.NotNil(t, legacy, "unsupported managed proof must not change legacy branch selection")
			binding, selected := s.ManagedTranscriptInboxBinding(conv)
			require.True(t, selected)
			require.Nil(t, binding, "invalid selected route cannot fall through to direct or global")
		})
	}
}

func TestManagedTranscriptInboxBindingNeverInfersProvenanceFromEqualTuple(t *testing.T) {
	for _, kind := range []string{"global", "custom", "mock", "none"} {
		t.Run(kind, func(t *testing.T) {
			sender := NewSMTPEmailSender("127.0.0.1", "2525", "route@example.test", "disposable")
			s := &EmailService{fromEmail: "route@example.test"}
			switch kind {
			case "global":
				s.sender = sender
				s.globalSMTP = sender
				s.globalSMTPConfig = *sender
				s.globalSMTPFrom = s.fromEmail
			case "custom":
				s.SetSender(sender)
			case "mock":
				s.SetSender(NewMockEmailSender())
			}
			conv := inboxSMTPTestConversation(t, sender)
			route, selected := s.ManagedTranscriptInboxBinding(conv)
			require.True(t, selected)
			require.NotNil(t, route)
			_, global := s.ManagedTranscriptSender(conv)
			require.False(t, global)
			inboxSMTPTestChange(t, conv.Inbox, "smtp_enabled", false)
			route, selected = s.ManagedTranscriptInboxBinding(conv)
			require.False(t, selected)
			require.Nil(t, route)
			_, global = s.ManagedTranscriptSender(conv)
			require.Equal(t, kind == "global", global)
		})
	}
}

func TestInboxSMTPBindingRejectsNoncanonicalEvidence(t *testing.T) {
	s := &EmailService{fromEmail: "fallback@example.test"}
	conv := inboxSMTPTestConversation(t, NewSMTPEmailSender("127.0.0.1", "2525", "", ""))
	original, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.NotNil(t, original)
	for _, field := range []string{"AccountID", "InboxID"} {
		for _, value := range []string{"", "0", "00", "01", "+17", "-1", "17.0", "1e3", " 17", "17 ", "18446744073709551616"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				binding := *original
				reflect.ValueOf(&binding).Elem().FieldByName(field).SetString(value)
				require.Error(t, domain.ValidateInboxSMTPBinding(binding))
			})
		}
	}
	for _, tc := range []struct{ field, value string }{
		{"Kind", "global"}, {"Kind", ""},
		{"CreatedAt", ""}, {"CreatedAt", "0001-01-01T00:00:00Z"}, {"CreatedAt", "2026-10-07T01:02:03.123456789+00:00"}, {"CreatedAt", "2026-10-07T01:02:03.000Z"},
		{"Fingerprint", ""}, {"Fingerprint", strings.Repeat("a", 64)}, {"Fingerprint", domain.InboxSMTPFingerprintPrefix + strings.Repeat("A", 64)}, {"Fingerprint", domain.InboxSMTPFingerprintPrefix + strings.Repeat("f", 62)},
	} {
		t.Run(tc.field+"/"+tc.value, func(t *testing.T) {
			binding := *original
			reflect.ValueOf(&binding).Elem().FieldByName(tc.field).SetString(tc.value)
			require.Error(t, domain.ValidateInboxSMTPBinding(binding))
		})
	}
	// Preserve exact integers above JavaScript's lossless float range.
	binding := *original
	binding.AccountID = "9007199254740993"
	binding.InboxID = "18446744073709551615"
	require.NoError(t, domain.ValidateInboxSMTPBinding(binding))
	encoded, err := json.Marshal(binding)
	require.NoError(t, err)
	var decoded domain.InboxSMTPBinding
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, binding, decoded)
}

func TestInboxSMTPFinalRouteRecheckPreventsDATA(t *testing.T) {
	for _, when := range []string{"initial", "final"} {
		for _, change := range []string{"host", "port", "user", "from", "created", "account", "id", "deleted", "disabled", "credential-loss", "oauth", "repository-error"} {
			t.Run(when+"/"+change, func(t *testing.T) {
				sender, fixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
				sender.Username = "route@example.test"
				sender.Password = "disposable"
				conv := inboxSMTPTestConversation(t, sender)
				s := &EmailService{fromEmail: "fallback@example.test"}
				binding, selected := s.ManagedTranscriptInboxBinding(conv)
				require.True(t, selected)
				require.NotNil(t, binding)
				repo := &inboxSMTPTestRepo{inbox: conv.Inbox}
				repo.beforeRead = func(call int, inbox *domain.Inbox) (*domain.Inbox, error) {
					clone := *inbox
					if when == "final" && call == 1 {
						return &clone, nil
					}
					switch change {
					case "host":
						inboxSMTPTestChange(t, &clone, "smtp_address", "localhost")
					case "port":
						inboxSMTPTestChange(t, &clone, "smtp_port", "1")
					case "user":
						inboxSMTPTestChange(t, &clone, "smtp_username", "another-user")
					case "from":
						inboxSMTPTestChange(t, &clone, "smtp_username", "other@example.test")
					case "created":
						clone.CreatedAt = clone.CreatedAt.Add(time.Nanosecond)
					case "account":
						clone.AccountID++
					case "id":
						clone.ID++
					case "deleted":
						return nil, nil
					case "disabled":
						inboxSMTPTestChange(t, &clone, "smtp_enabled", false)
					case "credential-loss":
						inboxSMTPTestChange(t, &clone, "smtp_password", "")
					case "oauth":
						inboxSMTPTestChange(t, &clone, "smtp_authentication", "oauth2")
					case "repository-error":
						return nil, errors.New("private-provider-error disposable")
					}
					return &clone, nil
				}
				adapter := NewInboxTranscriptSMTP(repo, s.GetFromEmail())
				marker := false
				result := adapter.SendInboxTranscript(context.Background(), *binding, "route@example.test", "recipient@example.test", "frozen-subject", "frozen-html", "frozen-text", func() error { marker = true; return nil })
				require.Equal(t, domain.OrdinarySMTPPermanentFailure, result.Outcome)
				require.False(t, marker)
				require.NotContains(t, fmt.Sprintf("%+v", result), "private-provider-error")
				require.NotContains(t, fmt.Sprintf("%+v", result), "disposable")
				if when == "final" {
					fixture.wait(t)
					require.Equal(t, 2, repo.calls)
					require.Contains(t, fixture.commands, "RCPT TO:<recipient@example.test>")
					require.NotContains(t, fixture.commands, "DATA")
					require.Empty(t, fixture.body)
				} else {
					require.Equal(t, 1, repo.calls)
				}
			})
		}
	}
}

// Each session is separate, but both attempts use the same loopback endpoint.
func newInboxSMTPRetryFixture(t *testing.T, behaviors []ordinarySMTPBehavior) (*SMTPEmailSender, []*ordinarySMTPFixture) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	require.NoError(t, listener.SetDeadline(time.Now().Add(5*time.Second)))
	fixtures := make([]*ordinarySMTPFixture, len(behaviors))
	for i := range fixtures {
		fixtures[i] = &ordinarySMTPFixture{done: make(chan struct{})}
	}
	t.Cleanup(func() {
		_ = listener.Close()
		for _, f := range fixtures {
			f.mu.Lock()
			f.closed = true
			if f.conn != nil {
				_ = f.conn.Close()
			}
			f.mu.Unlock()
			f.wait(t)
		}
	})
	go func() {
		for i, behavior := range behaviors {
			f := fixtures[i]
			conn, err := listener.Accept()
			if err != nil {
				f.err = err
				close(f.done)
				continue
			}
			f.mu.Lock()
			f.conn = conn
			closed := f.closed
			f.mu.Unlock()
			if closed {
				_ = conn.Close()
				close(f.done)
				continue
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			f.err = f.serve(conn, behavior)
			_ = conn.Close()
			close(f.done)
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	return NewSMTPEmailSender(host, port, "route@example.test", "disposable-original"), fixtures
}

func TestInboxSMTPRestartReadsRotatedPasswordOnSameFrozenRoute(t *testing.T) {
	sender, fixtures := newInboxSMTPRetryFixture(t, []ordinarySMTPBehavior{{rejectAt: "mail", code: 450}, {}})
	conv := inboxSMTPTestConversation(t, sender)
	s := &EmailService{fromEmail: "fallback@example.test"}
	binding, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.NotNil(t, binding)
	repo := &inboxSMTPTestRepo{inbox: conv.Inbox}
	markerCount := 0
	submit := func(adapter domain.InboxTranscriptSMTP) domain.OrdinarySMTPResult {
		return adapter.SendInboxTranscript(context.Background(), *binding, "route@example.test", "recipient@example.test", "frozen-subject", "frozen-html", "frozen-text", func() error { markerCount++; return nil })
	}
	first := submit(NewInboxTranscriptSMTP(repo, s.GetFromEmail()))
	require.Equal(t, domain.OrdinarySMTPPreSubmissionTransient, first.Outcome)
	require.Zero(t, markerCount)
	fixtures[0].wait(t)
	inboxSMTPTestChange(t, conv.Inbox, "smtp_password", "disposable-rotated")
	second := submit(NewInboxTranscriptSMTP(repo, s.GetFromEmail()))
	require.Equal(t, domain.OrdinarySMTPAccepted, second.Outcome)
	require.Equal(t, 1, markerCount)
	fixtures[1].wait(t)
	require.Equal(t, 3, repo.calls, "each attempt loads current config and successful attempt rechecks it before marker")
	for i, password := range []string{"disposable-original", "disposable-rotated"} {
		encoded := base64.StdEncoding.EncodeToString([]byte("\x00route@example.test\x00" + password))
		require.Contains(t, fixtures[i].commands, "AUTH PLAIN "+encoded)
	}
	require.NotContains(t, fixtures[0].commands, "DATA")
	require.Contains(t, fixtures[1].commands, "DATA")
	require.Contains(t, fixtures[1].body, "frozen-subject")
	require.Contains(t, fixtures[1].body, "frozen-html")
	require.Contains(t, fixtures[1].body, "frozen-text")
}

func TestInboxSMTPPreservesMarkerAndUnknownEvidence(t *testing.T) {
	for _, mode := range []string{"marker-error", "lost-ack", "changed-after-final-check"} {
		t.Run(mode, func(t *testing.T) {
			behavior := ordinarySMTPBehavior{}
			if mode == "lost-ack" {
				behavior.dropAt = "data-ack"
			}
			sender, fixture := newOrdinarySMTPFixture(t, behavior)
			conv := inboxSMTPTestConversation(t, sender)
			s := &EmailService{fromEmail: "fallback@example.test"}
			binding, selected := s.ManagedTranscriptInboxBinding(conv)
			require.True(t, selected)
			require.NotNil(t, binding)
			repo := &inboxSMTPTestRepo{inbox: conv.Inbox}
			markerCount := 0
			result := NewInboxTranscriptSMTP(repo, s.GetFromEmail()).SendInboxTranscript(context.Background(), *binding, s.GetFromEmail(), "recipient@example.test", "frozen-subject", "frozen-html", "frozen-text", func() error {
				markerCount++
				if mode == "marker-error" {
					return errors.New("private-marker-error")
				}
				if mode == "changed-after-final-check" {
					inboxSMTPTestChange(t, conv.Inbox, "smtp_enabled", false)
				}
				return nil
			})
			fixture.wait(t)
			require.Equal(t, 1, markerCount)
			require.Equal(t, 2, repo.calls)
			switch mode {
			case "marker-error":
				require.Equal(t, domain.OrdinarySMTPCancelledBeforeSubmission, result.Outcome)
				require.NotContains(t, fixture.commands, "DATA")
			case "lost-ack":
				require.Equal(t, domain.OrdinarySMTPUnknown, result.Outcome)
				require.Contains(t, fixture.body, "frozen-text")
			case "changed-after-final-check":
				require.Equal(t, domain.OrdinarySMTPAccepted, result.Outcome, "a later CHN change cannot retract DATA after MSG's marker")
			}
		})
	}
}

func TestInboxSMTPRejectsPayloadFromAndUnusableProofBeforeNetwork(t *testing.T) {
	sender := NewSMTPEmailSender("127.0.0.1", "1", "", "")
	conv := inboxSMTPTestConversation(t, sender)
	s := &EmailService{fromEmail: "fallback@example.test"}
	binding, selected := s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.NotNil(t, binding)
	for _, tc := range []struct {
		name, from string
		proof      domain.InboxSMTPBinding
		nilHook    bool
	}{
		{name: "mismatched-from", from: "other@example.test", proof: *binding},
		{name: "display-name-from", from: "Display <fallback@example.test>", proof: *binding},
		{name: "empty-proof", from: s.GetFromEmail()},
		{name: "nil-hook", from: s.GetFromEmail(), proof: *binding, nilHook: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &inboxSMTPTestRepo{inbox: conv.Inbox}
			hook := func() error { t.Fatal("invalid route reached durable marker"); return nil }
			if tc.nilHook {
				hook = nil
			}
			result := NewInboxTranscriptSMTP(repo, s.GetFromEmail()).SendInboxTranscript(context.Background(), tc.proof, tc.from, "recipient@example.test", "subject", "html", "text", hook)
			require.Equal(t, domain.OrdinarySMTPPermanentFailure, result.Outcome)
			require.LessOrEqual(t, repo.calls, 1)
		})
	}
	s.SetFromEmail("Display <fallback@example.test>")
	binding, selected = s.ManagedTranscriptInboxBinding(conv)
	require.True(t, selected)
	require.Nil(t, binding)
}

func TestInboxSMTPDifferentInboxesWithSameFromKeepSelectedEndpoint(t *testing.T) {
	first, firstFixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
	second, secondFixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
	global, globalFixture := newOrdinarySMTPFixture(t, ordinarySMTPBehavior{})
	s := &EmailService{fromEmail: "same@example.test", sender: global, globalSMTP: global, globalSMTPConfig: *global, globalSMTPFrom: "same@example.test"}
	require.NotEqual(t, first.Port, second.Port)
	for i, tc := range []struct {
		sender  *SMTPEmailSender
		fixture *ordinarySMTPFixture
	}{{first, firstFixture}, {second, secondFixture}} {
		conv := inboxSMTPTestConversation(t, tc.sender)
		conv.InboxID += uint(i)
		conv.Inbox.ID = conv.InboxID
		binding, selected := s.ManagedTranscriptInboxBinding(conv)
		require.True(t, selected)
		require.NotNil(t, binding)
		repo := &inboxSMTPTestRepo{inbox: conv.Inbox}
		text := fmt.Sprintf("frozen-inbox-%d", conv.InboxID)
		result := NewInboxTranscriptSMTP(repo, s.GetFromEmail()).SendInboxTranscript(context.Background(), *binding, s.GetFromEmail(), "recipient@example.test", "subject", text, text, func() error { return nil })
		require.Equal(t, domain.OrdinarySMTPAccepted, result.Outcome)
		tc.fixture.wait(t)
		require.Contains(t, tc.fixture.body, text)
		require.Contains(t, tc.fixture.commands, "MAIL FROM:<same@example.test>")
	}
	globalFixture.mu.Lock()
	globalConnected := globalFixture.conn != nil
	globalFixture.mu.Unlock()
	require.False(t, globalConnected, "global endpoint receives no connection for managed Inbox routes")
	require.NotContains(t, firstFixture.body, "frozen-inbox-30")
	require.NotContains(t, secondFixture.body, "frozen-inbox-29")
}
