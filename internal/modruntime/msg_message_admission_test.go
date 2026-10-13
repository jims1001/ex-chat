package modruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type messageAdmissionFixture struct {
	runtime *Runtime
	ten     *gorm.DB
	fault   atomic.Int32
	conDown atomic.Bool
	mu      sync.Mutex
	calls   []string
}

func newMessageAdmissionFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *messageAdmissionFixture {
	t.Helper()
	f := &messageAdmissionFixture{ten: open(t, "TEN")}
	for _, id := range []uint{7, 8} {
		require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: id, Role: domain.RoleAgent}).Error)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.MaxTimeout = 3 * time.Second
	tenCfg.AllowedCallers["MSG"] = true
	tenCfg.Dependencies["IAM"] = DependencyConfig{URL: "http://127.0.0.1:1", Token: "unused"}
	tenRuntime := &Runtime{Config: tenCfg, DB: f.ten, Contracts: NewContractRegistry("TEN")}
	require.NoError(t, tenRuntime.RegisterBusinessContracts())
	tenHandler := tenRuntime.Handler()
	tenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var q foundation.QueryEnvelope
		if err = json.Unmarshal(raw, &q); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		require.Equal(t, "/contracts/queries/ten.permissions.check.v1", r.URL.Path)
		require.Equal(t, "MSG", r.Header.Get(headerCaller))
		require.Equal(t, "strong", q.Consistency)
		id, err := uintFilter(q.Filter, "user_id")
		require.NoError(t, err)
		f.record(fmt.Sprintf("TEN:%d", id))
		switch f.fault.Load() {
		case 1:
			w.WriteHeader(503)
			return
		case 2:
			_, _ = w.Write([]byte(`{"data":`))
			return
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": membershipCheckView{AccountID: 92, UserID: id, Allowed: true}})
			return
		case 4:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": membershipCheckView{AccountID: q.AccountID, UserID: id + 1, Allowed: true}})
			return
		case 5:
			_, _ = w.Write([]byte(`{"data":{"account_id":91,"user_id":"bad","allowed":true}}`))
			return
		case 6:
			_, _ = w.Write([]byte(`{"data":null}`))
			return
		case 7:
			_, _ = w.Write([]byte(`{}`))
			return
		}
		tenHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(tenServer.Close)
	conRegistry := NewContractRegistry("CON")
	require.NoError(t, conRegistry.RegisterQuery("con.conversation.get.v1", func(_ context.Context, q foundation.QueryEnvelope) (any, error) {
		f.record("CON")
		if f.conDown.Load() {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "offline"}
		}
		id, err := uintFilter(q.Filter, "id")
		if err != nil || q.AccountID != 91 || id != 11 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
		}
		return conversationView{ID: 11, AccountID: 91, ContactID: 21, Status: domain.ConversationStatusOpen}, nil
	}))
	conCfg := businessConfig("CON")
	conCfg.MaxTimeout = 3 * time.Second
	conCfg.AllowedCallers["MSG"] = true
	conServer := httptest.NewServer(Runtime{Config: conCfg, Contracts: conRegistry}.Handler())
	t.Cleanup(conServer.Close)
	cfg := businessConfig("MSG")
	cfg.MaxTimeout = 3 * time.Second
	cfg.MaxRequestBytes = 200000
	cfg.Dependencies = map[string]DependencyConfig{"TEN": {URL: tenServer.URL, Token: tenCfg.AuthToken}, "CON": {URL: conServer.URL, Token: conCfg.AuthToken}}
	f.runtime = &Runtime{Config: cfg, DB: open(t, "MSG"), Contracts: NewContractRegistry("MSG")}
	if f.runtime.DB.Dialector.Name() == "postgres" {
		for _, owner := range []struct {
			name string
			db   *gorm.DB
		}{{"MSG", f.runtime.DB}, {"TEN", f.ten}} {
			var zone string
			require.NoError(t, owner.db.Raw("SHOW TimeZone").Scan(&zone).Error)
			require.Equal(t, "Asia/Shanghai", zone)
			t.Logf("%s PostgreSQL session TimeZone=%s; process time.Local=%s", owner.name, zone, time.Local)
		}
	}
	f.restart(t)
	return f
}
func (f *messageAdmissionFixture) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}
func (f *messageAdmissionFixture) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := append([]string(nil), f.calls...)
	f.calls = nil
	return s
}
func (f *messageAdmissionFixture) restart(t *testing.T) {
	t.Helper()
	f.runtime.Contracts = NewContractRegistry("MSG")
	f.runtime.Contracts.UseCommandReceipts(f.runtime.DB)
	require.NoError(t, f.runtime.RegisterBusinessContracts())
}
func messageAdmissionCommand(key string) foundation.CommandEnvelope {
	return foundation.CommandEnvelope{CommandID: "message-" + key, CommandType: "msg.message.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: "11"}, Input: map[string]any{"conversation_id": 11, "sender_type": "User", "sender_id": 8, "message_type": "outgoing", "content": "private fixture text", "echo_id": "echo-" + key}, IdempotencyKey: "key-" + key, CorrelationID: "correlation-" + key, CausationID: "cause-" + key, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 2500, OnTimeout: "query", StatusQuery: "msg.message.get.v1"}}
}
func (f *messageAdmissionFixture) call(c foundation.CommandEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(f.runtime.Handler(), "/contracts/commands/msg.message.create.v1", c, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		r.Header.Set(headerCorrelationID, c.CorrelationID)
	})
}
func messageAdmissionData(t *testing.T, r *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	require.Equal(t, 200, r.Code, r.Body.String())
	var v struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &v))
	return v.Data
}
func (f *messageAdmissionFixture) state(t *testing.T) string {
	t.Helper()
	var messages []domain.Message
	var journal []domain.LocalChangeJournal
	var attempts []domain.OutboundMessageAttempt
	require.NoError(t, f.runtime.DB.Order("id").Find(&messages).Error)
	require.NoError(t, f.runtime.DB.Order("id").Find(&journal).Error)
	require.NoError(t, f.runtime.DB.Order("id").Find(&attempts).Error)
	raw, err := json.Marshal([]any{messages, journal, attempts})
	require.NoError(t, err)
	return string(raw)
}
func (f *messageAdmissionFixture) counts(t *testing.T, m, r, e int64) {
	t.Helper()
	for _, check := range []struct {
		model any
		where string
		want  int64
	}{{&domain.Message{}, "1=1", m}, {&domain.LocalChangeJournal{}, "action = 'command_receipt'", r}, {&domain.LocalChangeJournal{}, "action = 'event_outbox'", e}, {&domain.OutboundMessageAttempt{}, "1=1", 0}} {
		var n int64
		require.NoError(t, f.runtime.DB.Model(check.model).Where(check.where).Count(&n).Error)
		require.Equal(t, check.want, n, check.where)
	}
}
func (f *messageAdmissionFixture) deny(t *testing.T, c foundation.CommandEnvelope, status int) {
	t.Helper()
	before := f.state(t)
	r := f.call(c)
	require.Equal(t, status, r.Code, r.Body.String())
	require.NotContains(t, r.Body.String(), "private fixture text")
	require.NotContains(t, r.Body.String(), `"data"`)
	require.Equal(t, before, f.state(t))
}
func (f *messageAdmissionFixture) revoke(t *testing.T, id uint) {
	t.Helper()
	result := f.ten.Where("account_id = ? AND user_id = ?", 91, id).Delete(&domain.AccountUser{})
	require.NoError(t, result.Error)
	require.EqualValues(t, 1, result.RowsAffected)
}

func messageAdmissionAuthority(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("first_receipt_echo_revoke_restore", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("authority")
		first := messageAdmissionData(t, f.call(c))
		var persisted domain.Message
		require.NoError(t, f.runtime.DB.First(&persisted).Error)
		persistedView, err := json.Marshal(messageFromModel(persisted))
		require.NoError(t, err)

		require.Equal(t, []string{"TEN:7", "TEN:7", "CON", "TEN:8"}, f.takeCalls())
		f.counts(t, 1, 1, 1)
		require.JSONEq(t, string(first), string(messageAdmissionData(t, f.call(c))))
		require.Equal(t, []string{"TEN:7"}, f.takeCalls())
		f.counts(t, 1, 1, 1)
		before := f.state(t)
		f.revoke(t, 7)
		f.deny(t, c, 404)
		require.Equal(t, []string{"TEN:7"}, f.takeCalls())
		echo := messageAdmissionCommand("echo")
		echo.Input["echo_id"] = c.Input["echo_id"]
		f.deny(t, echo, 404)
		require.Equal(t, []string{"TEN:7"}, f.takeCalls())
		require.Equal(t, before, f.state(t))
		require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAgent}).Error)
		require.JSONEq(t, string(first), string(messageAdmissionData(t, f.call(c))))
		require.Equal(t, []string{"TEN:7"}, f.takeCalls())
		require.Equal(t, before, f.state(t))
		// Echo reads the persisted row, preserving native timestamp precision/scan
		// location. Exact receipt output comparisons above remain unchanged.
		require.JSONEq(t, string(persistedView), string(messageAdmissionData(t, f.call(echo))))
		require.Equal(t, []string{"TEN:7", "TEN:7", "CON", "TEN:8"}, f.takeCalls())
		f.counts(t, 1, 2, 1)
	})
	t.Run("actor_denied_sender_allowed", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		f.revoke(t, 7)
		f.deny(t, messageAdmissionCommand("actor-denied"), 404)
		require.Equal(t, []string{"TEN:7"}, f.takeCalls())
		f.counts(t, 0, 0, 0)
	})
	t.Run("actor_allowed_sender_denied", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		f.revoke(t, 8)
		f.deny(t, messageAdmissionCommand("sender-denied"), 404)
		require.Equal(t, []string{"TEN:7", "TEN:7", "CON", "TEN:8"}, f.takeCalls())
		f.counts(t, 0, 0, 0)
	})
	t.Run("direct_entry_denies_before_echo_or_input", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("direct")
		messageAdmissionData(t, f.call(c))
		f.takeCalls()
		f.revoke(t, 7)
		before := f.state(t)
		for _, badInput := range []bool{false, true} {
			if badInput {
				c.Input = nil
			}
			out, err := f.runtime.Contracts.commands[c.CommandType](context.Background(), c)
			require.Nil(t, out)
			var failure *ContractFailure
			require.ErrorAs(t, err, &failure)
			require.Equal(t, "resource_not_found", failure.Type)
			require.Equal(t, []string{"TEN:7"}, f.takeCalls())
			require.Equal(t, before, f.state(t))
		}
	})
	t.Run("second_check_revocation_rolls_back_pending_receipt", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("between-checks")
		original := f.runtime.Contracts.authorizers[c.CommandType]
		f.runtime.Contracts.authorizers[c.CommandType] = func(ctx context.Context, c foundation.CommandEnvelope) error {
			if err := original(ctx, c); err != nil {
				return err
			}
			f.revoke(t, 7)
			return nil
		}
		f.deny(t, c, 404)
		require.Equal(t, []string{"TEN:7", "TEN:7"}, f.takeCalls())
		f.counts(t, 0, 0, 0)
	})
}
func TestMSGMessageActorAdmissionAuthority(t *testing.T) {
	messageAdmissionAuthority(t, newModuleTestDB)
}
func TestPostgresMSGMessageActorAdmissionAuthority(t *testing.T) {
	messageAdmissionAuthority(t, postgresModuleDB)
}

func messageAdmissionFailures(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, tc := range []struct{ name, id string }{{"nonnumeric", "api"}, {"zero", "0"}, {"negative", "-1"}, {"overflow", "18446744073709551616"}, {"whitespace", " 7"}, {"fractional", "7.0"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMessageAdmissionFixture(t, open)
			c := messageAdmissionCommand(tc.name)
			c.Actor.ID = tc.id
			f.deny(t, c, 400)
			require.Empty(t, f.takeCalls())
		})
	}
	for _, tc := range []struct {
		name   string
		fault  int32
		status int
	}{{"unavailable", 1, 503}, {"malformed", 2, 503}, {"wrong_account", 3, 404}, {"wrong_user", 4, 404}, {"wrong_type", 5, 503}, {"null_member", 6, 404}, {"missing_data", 7, 503}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMessageAdmissionFixture(t, open)
			c := messageAdmissionCommand(tc.name)
			messageAdmissionData(t, f.call(c))
			f.takeCalls()
			f.fault.Store(tc.fault)
			f.deny(t, c, tc.status)
			require.Equal(t, []string{"TEN:7"}, f.takeCalls())
			fresh := messageAdmissionCommand(tc.name + "-fresh")
			f.deny(t, fresh, tc.status)
			require.Equal(t, []string{"TEN:7"}, f.takeCalls())
		})
	}
	t.Run("missing_TEN", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("missing")
		messageAdmissionData(t, f.call(c))
		f.takeCalls()
		delete(f.runtime.Config.Dependencies, "TEN")
		f.deny(t, c, 503)
		f.deny(t, messageAdmissionCommand("fresh-missing"), 503)
		require.Empty(t, f.takeCalls())
	})
	t.Run("transport_outage", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("outage")
		f.runtime.Config.Dependencies["TEN"] = DependencyConfig{URL: "http://127.0.0.1:1", Token: "unused"}
		f.deny(t, c, 503)
		require.Empty(t, f.takeCalls())
	})
	t.Run("canceled_direct", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("cancel")
		before := f.state(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out, err := f.runtime.Contracts.commands[c.CommandType](ctx, c)
		require.Nil(t, out)
		var failure *ContractFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "dependency_unavailable", failure.Type)
		require.Empty(t, f.takeCalls())
		require.Equal(t, before, f.state(t))
	})
	t.Run("empty_ID_direct", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("empty")
		c.Actor.ID = ""
		before := f.state(t)
		out, err := f.runtime.Contracts.commands[c.CommandType](context.Background(), c)
		require.Nil(t, out)
		var failure *ContractFailure
		require.ErrorAs(t, err, &failure)
		require.Equal(t, "validation_failed", failure.Type)
		require.Empty(t, f.takeCalls())
		require.Equal(t, before, f.state(t))
	})
	t.Run("foreign_account", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("foreign")
		c.AccountID = 92
		f.deny(t, c, 404)
		require.Equal(t, []string{"TEN:7"}, f.takeCalls())
	})
}
func TestMSGMessageActorAdmissionFailures(t *testing.T) { messageAdmissionFailures(t, newModuleTestDB) }
func TestPostgresMSGMessageActorAdmissionFailures(t *testing.T) {
	messageAdmissionFailures(t, postgresModuleDB)
}

func messageAdmissionCompatibility(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, actor := range []string{"System", "Contact"} {
		t.Run(actor+"_incoming_without_TEN_and_cached_without_CON", func(t *testing.T) {
			f := newMessageAdmissionFixture(t, open)
			delete(f.runtime.Config.Dependencies, "TEN")
			c := messageAdmissionCommand(actor)
			c.Actor = foundation.ContractActor{Type: actor, ID: "service-nonnumeric", AuthSource: "module"}
			c.Input["sender_type"] = "Contact"
			c.Input["sender_id"] = 21
			c.Input["message_type"] = "incoming"
			first := messageAdmissionData(t, f.call(c))
			require.Equal(t, []string{"CON"}, f.takeCalls())
			var view messageView
			require.NoError(t, json.Unmarshal(first, &view))
			require.Equal(t, "saved", view.Status)
			require.Equal(t, "text", view.ContentType)
			delete(f.runtime.Config.Dependencies, "CON")
			f.conDown.Store(true)
			require.JSONEq(t, string(first), string(messageAdmissionData(t, f.call(c))))
			require.Empty(t, f.takeCalls())
			f.counts(t, 1, 1, 1)
		})
	}
	t.Run("System_User_sender_keeps_membership_check", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("system-sender")
		c.Actor = foundation.ContractActor{Type: "System", ID: "service", AuthSource: "module"}
		messageAdmissionData(t, f.call(c))
		require.Equal(t, []string{"CON", "TEN:8"}, f.takeCalls())
		f.revoke(t, 8)
		c = messageAdmissionCommand("system-denied")
		c.Actor = foundation.ContractActor{Type: "System", ID: "service", AuthSource: "module"}
		f.deny(t, c, 404)
		require.Equal(t, []string{"CON", "TEN:8"}, f.takeCalls())
	})
	t.Run("nonmodule_source_and_leading_zero_ID", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := messageAdmissionCommand("auth-source")
		c.Actor.AuthSource = "session"
		c.Actor.ID = "0007"
		messageAdmissionData(t, f.call(c))
		require.Equal(t, []string{"TEN:7", "TEN:7", "CON", "TEN:8"}, f.takeCalls())
	})
	t.Run("exact_ID_beyond_float64", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		id := uint(9007199254740993)
		require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: id, Role: domain.RoleAgent}).Error)
		c := messageAdmissionCommand("large-id")
		c.Actor.ID = strconv.FormatUint(uint64(id), 10)
		messageAdmissionData(t, f.call(c))
		require.Equal(t, []string{"TEN:9007199254740993", "TEN:9007199254740993", "CON", "TEN:8"}, f.takeCalls())
	})
	for _, tc := range []struct {
		name         string
		edit         func(*foundation.CommandEnvelope)
		status       int
		storedStatus string
		wantLen      int
	}{
		{"default_trim", func(c *foundation.CommandEnvelope) { c.Input["content"] = " \tprivate fixture text\n" }, 200, "queued", 20},
		{"byte_limit", func(c *foundation.CommandEnvelope) { c.Input["content"] = " " + strings.Repeat("界", 33333) + "x " }, 200, "queued", 100000},
		{"over_byte_limit", func(c *foundation.CommandEnvelope) { c.Input["content"] = strings.Repeat("界", 33333) + "xx" }, 400, "", 0},
		{"blank", func(c *foundation.CommandEnvelope) { c.Input["content"] = " \t\n" }, 400, "", 0},
		{"private_outgoing", func(c *foundation.CommandEnvelope) { c.Input["private"] = true }, 200, "saved", 20},
		{"private_incoming", func(c *foundation.CommandEnvelope) {
			c.Input["private"] = true
			c.Input["sender_type"] = "Contact"
			c.Input["sender_id"] = 21
			c.Input["message_type"] = "incoming"
		}, 400, "", 0},
		{"wrong_target", func(c *foundation.CommandEnvelope) { c.Target.ID = "12" }, 400, "", 0},
		{"foreign_conversation", func(c *foundation.CommandEnvelope) { c.Target.ID = "12"; c.Input["conversation_id"] = 12 }, 404, "", 0},
		{"wrong_contact", func(c *foundation.CommandEnvelope) {
			c.Input["sender_type"] = "Contact"
			c.Input["sender_id"] = 22
			c.Input["message_type"] = "incoming"
		}, 404, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMessageAdmissionFixture(t, open)
			c := messageAdmissionCommand(tc.name)
			tc.edit(&c)
			if tc.status != 200 {
				f.deny(t, c, tc.status)
				f.counts(t, 0, 0, 0)
				return
			}
			raw := messageAdmissionData(t, f.call(c))
			var v messageView
			require.NoError(t, json.Unmarshal(raw, &v))
			require.Equal(t, tc.storedStatus, v.Status)
			require.Equal(t, "text", v.ContentType)
			require.Len(t, v.Content, tc.wantLen)
			f.counts(t, 1, 1, 1)
			var row domain.LocalChangeJournal
			require.NoError(t, f.runtime.DB.Where("action = ?", "event_outbox").First(&row).Error)
			var event foundation.EventEnvelope
			require.NoError(t, json.Unmarshal([]byte(row.AfterState), &event))
			require.Equal(t, "msg.message.created.v1", event.EventType)
			require.Equal(t, tc.storedStatus, event.Payload["status"])
		})
	}
	t.Run("owner_SQL_only", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		log := &cannedPreviewSQLLog{Interface: logger.Default.LogMode(logger.Silent)}
		f.runtime.DB = f.runtime.DB.Session(&gorm.Session{Logger: log})
		f.restart(t)
		messageAdmissionData(t, f.call(messageAdmissionCommand("owner")))
		queries := log.take()
		require.NotEmpty(t, queries)
		for _, sql := range queries {
			low := strings.ToLower(sql)
			for _, foreign := range []string{"account_users", "conversations", "users", "canned_responses", "inboxes"} {
				require.NotContains(t, low, foreign)
			}
			require.True(t, strings.Contains(low, "messages") || strings.Contains(low, "local_change_journals"), sql)
		}
		require.Equal(t, []string{"TEN:7", "TEN:7", "CON", "TEN:8"}, f.takeCalls())
		f.counts(t, 1, 1, 1)
	})
}
func TestMSGMessageActorAdmissionCompatibility(t *testing.T) {
	messageAdmissionCompatibility(t, newModuleTestDB)
}
func TestPostgresMSGMessageActorAdmissionCompatibility(t *testing.T) {
	messageAdmissionCompatibility(t, postgresModuleDB)
}

func messageAdmissionLegacy(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newMessageAdmissionFixture(t, open)
	c := messageAdmissionCommand("legacy")
	c.Input = map[string]any{"conversation_id": 11, "sender_type": "User", "sender_id": 8, "message_type": "outgoing", "content": "private fixture text", "echo_id": "legacy"}
	// This literal is the historical four-field normalization, independent of
	// commandDigest and deliberately containing no actor metadata.
	const legacyJSON = `{"Type":"msg.message.create.v1","AccountID":91,"Target":{"type":"conversation","id":"11"},"Input":{"content":"private fixture text","conversation_id":11,"echo_id":"legacy","message_type":"outgoing","sender_id":8,"sender_type":"User"}}`
	sum := sha256.Sum256([]byte(legacyJSON))
	wantDigest := hex.EncodeToString(sum[:])
	digest, err := commandDigest(c)
	require.NoError(t, err)
	require.Equal(t, wantDigest, digest)
	// The persisted JSON intentionally contains a >2^53 numeric value. Historical
	// MSG decoding remains float64, unlike newer precise-number capabilities.
	const legacyOutput = `{"id":9007199254740993,"content":"private fixture text","status":"queued","legacy":true}`
	key := sha256.Sum256([]byte(c.IdempotencyKey))
	now := time.Now().UTC()
	row := domain.LocalChangeJournal{ChangeID: "cmd-" + hex.EncodeToString(key[:])[:48], SourceModule: "MSG", AccountID: 91, ObjectType: c.CommandType, Action: "command_receipt", ActorType: "User", ActorID: 7, CommandID: c.CommandID, CorrelationID: c.CorrelationID, CausationID: c.CausationID, IdempotencyKeyHash: hex.EncodeToString(key[:]), Diff: wantDigest, AfterState: legacyOutput, Result: "applied", Status: "delivered", OccurredAt: now, RecordedAt: now, IngestedAt: now, CreatedAt: now}
	require.NoError(t, f.runtime.DB.Create(&row).Error)
	delete(f.runtime.Config.Dependencies, "CON")
	assertReplay := func() {
		t.Helper()
		before := f.state(t)
		raw := messageAdmissionData(t, f.call(c))
		var actual map[string]any
		require.NoError(t, json.Unmarshal(raw, &actual))
		var legacy any
		require.NoError(t, json.Unmarshal([]byte(legacyOutput), &legacy))
		require.Equal(t, legacy, any(actual))
		require.IsType(t, float64(0), actual["id"])
		require.Equal(t, float64(9007199254740992), actual["id"])
		require.Equal(t, before, f.state(t))
		require.Equal(t, []string{"TEN:" + c.Actor.ID}, f.takeCalls())
		var stored domain.LocalChangeJournal
		require.NoError(t, f.runtime.DB.Where("change_id = ?", row.ChangeID).First(&stored).Error)
		require.Equal(t, wantDigest, stored.Diff)
		require.Equal(t, legacyOutput, stored.AfterState)
	}
	assertReplay()
	f.restart(t)
	assertReplay()
	c.Actor.ID = "8"
	changed, err := commandDigest(c)
	require.NoError(t, err)
	require.Equal(t, wantDigest, changed)
	assertReplay()
	c.Actor.ID = "9"
	f.deny(t, c, 404)
	require.Equal(t, []string{"TEN:9"}, f.takeCalls())
	c.Actor.ID = "7"
	path := filepath.Join(t.TempDir(), "message-legacy-snapshot.json")
	require.NoError(t, CreateSnapshot(f.runtime.DB, "MSG", "phase66", path))
	restored := open(t, "MSG")
	require.NoError(t, RestoreSnapshot(context.Background(), restored, "MSG", "phase66", path, nil))
	f.runtime.DB = restored
	f.restart(t)
	assertReplay()
	f.counts(t, 0, 1, 0)
	f.revoke(t, 7)
	f.deny(t, c, 404)
	require.Equal(t, []string{"TEN:7"}, f.takeCalls())
}
func TestMSGMessageActorAdmissionLegacyRecovery(t *testing.T) {
	messageAdmissionLegacy(t, newModuleTestDB)
}
func TestPostgresMSGMessageActorAdmissionLegacyRecovery(t *testing.T) {
	messageAdmissionLegacy(t, postgresModuleDB)
}
