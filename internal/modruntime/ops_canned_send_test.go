package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

type cannedSendFixture struct {
	ops, msg   *Runtime
	opsHandler http.Handler
	ten, con   *gorm.DB
	down       atomic.Int32
	tenDown    atomic.Bool
	msgCalls   atomic.Int64
	mu         sync.Mutex
	commands   []foundation.CommandEnvelope
	afterMSG   func()
}

func newCannedSendFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *cannedSendFixture {
	t.Helper()
	f := &cannedSendFixture{ten: open(t, "TEN"), con: open(t, "CON")}
	for _, row := range []domain.AccountUser{{AccountID: 91, UserID: 7, Role: domain.RoleAgent}, {AccountID: 91, UserID: 8, Role: domain.RoleAdministrator}, {AccountID: 92, UserID: 7, Role: domain.RoleAgent}} {
		require.NoError(t, f.ten.Create(&row).Error)
	}
	require.NoError(t, f.con.Create(&domain.Conversation{ID: 11, AccountID: 91, ContactID: 21, Status: domain.ConversationStatusOpen}).Error)
	cfg := businessConfig("TEN")
	cfg.MaxTimeout = 5 * time.Second
	cfg.AllowedCallers["OPS"] = true
	cfg.AllowedCallers["MSG"] = true
	cfg.Dependencies["IAM"] = DependencyConfig{URL: "http://127.0.0.1:1", Token: "unused"}
	ten := &Runtime{Config: cfg, DB: f.ten, Contracts: NewContractRegistry("TEN")}
	require.NoError(t, ten.RegisterBusinessContracts())
	tenHandler := ten.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.tenDown.Load() {
			w.WriteHeader(503)
			return
		}
		tenHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	cc := businessConfig("CON")
	cc.MaxTimeout = 5 * time.Second
	cc.AllowedCallers["MSG"] = true
	cr := NewContractRegistry("CON")
	require.NoError(t, cr.RegisterQuery("con.conversation.get.v1", func(ctx context.Context, q foundation.QueryEnvelope) (any, error) {
		raw, ok := q.Filter["id"].(string)
		id, valid := cannedResponseID(raw)
		if !ok || !valid {
			return nil, cannedSendInvalid()
		}
		var row domain.Conversation
		if err := f.con.WithContext(ctx).Where("account_id = ? AND id = ?", q.AccountID, id).First(&row).Error; err != nil {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "missing conversation"}
		}
		return conversationView{ID: row.ID, AccountID: row.AccountID, ContactID: row.ContactID, Status: row.Status}, nil
	}))
	cs := httptest.NewServer(cr.Handler(cc))
	t.Cleanup(cs.Close)
	mc := businessConfig("MSG")
	mc.MaxTimeout = 5 * time.Second
	mc.MaxRequestBytes = 2 * 1024 * 1024
	mc.AllowedCallers["OPS"] = true
	mc.Dependencies["TEN"] = DependencyConfig{URL: ts.URL, Token: cfg.AuthToken}
	mc.Dependencies["CON"] = DependencyConfig{URL: cs.URL, Token: cc.AuthToken}
	f.msg = &Runtime{Config: mc, DB: open(t, "MSG"), Contracts: NewContractRegistry("MSG")}
	f.msg.Contracts.UseCommandReceipts(f.msg.DB)
	require.NoError(t, f.msg.RegisterBusinessContracts())
	msgHandler := f.msg.Handler()
	ms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.msgCalls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var c foundation.CommandEnvelope
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		f.mu.Lock()
		f.commands = append(f.commands, c)
		f.mu.Unlock()
		// Base DB query uses a different connection from the active OPS send receipt.
		// The preparation must already be committed before this owner can be called.
		intentID := strings.TrimPrefix(c.IdempotencyKey, cannedResponseSend+":")
		var prepared domain.LocalChangeJournal
		if len(intentID) != 64 || f.ops.DB.WithContext(r.Context()).Where("change_id = ? AND result = ?", "cmd-"+intentID[:48], "applied").First(&prepared).Error != nil {
			t.Error("prepare was not independently visible before MSG")
			w.WriteHeader(500)
			return
		}
		if f.down.Load() == 3 {
			w.WriteHeader(503)
			return
		}
		result := httptest.NewRecorder()
		msgHandler.ServeHTTP(result, r)
		if f.afterMSG != nil {
			f.afterMSG()
		}
		if f.down.Load() == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		if f.down.Load() == 2 {
			_, _ = w.Write([]byte(`{"data":null}`))
			return
		}
		if f.down.Load() == 4 {
			var p struct{ Data messageView }
			_ = json.Unmarshal(result.Body.Bytes(), &p)
			p.Data.ConversationID++
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		w.WriteHeader(result.Code)
		_, _ = w.Write(result.Body.Bytes())
	}))
	t.Cleanup(ms.Close)
	oc := businessConfig("OPS")
	oc.MaxTimeout = 5 * time.Second
	oc.MaxRequestBytes = 2 * 1024 * 1024
	oc.Dependencies["TEN"] = DependencyConfig{URL: ts.URL, Token: cfg.AuthToken}
	oc.Dependencies["MSG"] = DependencyConfig{URL: ms.URL, Token: mc.AuthToken}
	f.ops = &Runtime{Config: oc, DB: open(t, "OPS"), Contracts: NewContractRegistry("OPS")}
	f.restart(t)
	if f.ops.DB.Dialector.Name() == "postgres" {
		for owner, db := range map[string]*gorm.DB{"OPS": f.ops.DB, "MSG": f.msg.DB, "TEN": f.ten, "CON": f.con} {
			var zone string
			require.NoError(t, db.Raw("SHOW TimeZone").Scan(&zone).Error)
			require.Equal(t, "Asia/Shanghai", zone)
			t.Logf("%s PostgreSQL session TimeZone=%s; process time.Local=%s", owner, zone, time.Local)
		}
	}
	require.NoError(t, f.ops.DB.Create(&domain.CannedResponse{ID: 31, AccountID: 91, ShortCode: "greeting", Content: "  Hello {{name}} 世界  ", ObjectVersion: 1}).Error)
	return f
}
func (f *cannedSendFixture) restart(t *testing.T) {
	t.Helper()
	f.ops.Contracts = NewContractRegistry("OPS")
	f.ops.Contracts.UseCommandReceipts(f.ops.DB)
	require.NoError(t, f.ops.RegisterBusinessContracts())
	f.opsHandler = f.ops.Handler()
}
func cannedPrepareCommand(key string) foundation.CommandEnvelope {
	c := messageAdmissionCommand(key)
	c.CommandType = cannedResponsePrepareSend
	c.TimeoutPolicy.TimeoutMS = 4500
	c.TimeoutPolicy.StatusQuery = cannedResponseGet
	c.Input = map[string]any{"canned_response_id": "31", "object_version": "1", "variables": map[string]any{"name": "{{literal}}"}}
	return c
}
func cannedIntentCommand(key string, v cannedSendIntent) foundation.CommandEnvelope {
	c := cannedPrepareCommand(key)
	c.CommandType = cannedResponseSend
	c.Input = map[string]any{"intent_id": v.IntentID, "resolved_content": v.ResolvedContent}
	c.Target.ID = v.ConversationID
	return c
}
func (f *cannedSendFixture) call(c foundation.CommandEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(f.opsHandler, "/contracts/commands/"+c.CommandType, c, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.ops.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		r.Header.Set(headerCorrelationID, c.CorrelationID)
	})
}
func (f *cannedSendFixture) prepare(t *testing.T, key string) cannedSendIntent {
	t.Helper()
	var v cannedSendIntent
	require.NoError(t, json.Unmarshal(messageAdmissionData(t, f.call(cannedPrepareCommand(key))), &v))
	return v
}
func (f *cannedSendFixture) counts(t *testing.T, prep, send, msg int64) {
	t.Helper()
	for _, check := range []struct {
		db    *gorm.DB
		model any
		where string
		n     int64
	}{{f.ops.DB, &domain.LocalChangeJournal{}, "object_type = '" + cannedResponsePrepareSend + "'", prep}, {f.ops.DB, &domain.LocalChangeJournal{}, "object_type = '" + cannedResponseSend + "'", send}, {f.ops.DB, &domain.LocalChangeJournal{}, "action = 'event_outbox'", 0}, {f.msg.DB, &domain.Message{}, "1=1", msg}, {f.msg.DB, &domain.LocalChangeJournal{}, "action = 'command_receipt'", msg}, {f.msg.DB, &domain.LocalChangeJournal{}, "action = 'event_outbox'", msg}, {f.msg.DB, &domain.OutboundMessageAttempt{}, "1=1", 0}} {
		var n int64
		require.NoError(t, check.db.Model(check.model).Where(check.where).Count(&n).Error)
		require.Equal(t, check.n, n, check.where)
	}
}
func (f *cannedSendFixture) deny(t *testing.T, c foundation.CommandEnvelope, status int) {
	t.Helper()
	calls := f.msgCalls.Load()
	out := f.call(c)
	require.Equal(t, status, out.Code, out.Body.String())
	require.NotContains(t, out.Body.String(), `"data"`)
	require.NotContains(t, out.Body.String(), "Hello")
	require.Equal(t, calls, f.msgCalls.Load())
}

func cannedSendLifecycle(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedSendFixture(t, open)
	p := cannedPrepareCommand("prepare")
	v := f.prepare(t, "prepare")
	f.counts(t, 1, 0, 0)
	require.Zero(t, f.msgCalls.Load())
	require.Equal(t, "  Hello {{literal}} 世界  ", v.ResolvedContent)
	require.NoError(t, f.ops.DB.Model(&domain.CannedResponse{}).Where("id = 31").Updates(map[string]any{"content": "changed", "object_version": 2}).Error)
	var replay cannedSendIntent
	require.NoError(t, json.Unmarshal(messageAdmissionData(t, f.call(p)), &replay))
	require.Equal(t, v, replay)
	other := cannedPrepareCommand("stale")
	f.deny(t, other, 409)
	c := cannedIntentCommand("send", v)
	first := messageAdmissionData(t, f.call(c))
	var m messageView
	require.NoError(t, json.Unmarshal(first, &m))
	require.Equal(t, strings.TrimSpace(v.ResolvedContent), m.Content)
	require.Equal(t, "queued", m.Status)
	f.counts(t, 1, 1, 1)
	require.NoError(t, f.ops.DB.Delete(&domain.CannedResponse{}, 31).Error)
	f.restart(t)
	f.down.Store(3)
	require.JSONEq(t, string(first), string(messageAdmissionData(t, f.call(c))))
	require.Equal(t, int64(1), f.msgCalls.Load())
	f.down.Store(0)
	c2 := cannedIntentCommand("different-send-key", v)
	require.JSONEq(t, string(first), string(messageAdmissionData(t, f.call(c2))))
	f.counts(t, 1, 2, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.commands, 2)
	require.Equal(t, f.commands[0].Input, f.commands[1].Input)
	require.Equal(t, f.commands[0].IdempotencyKey, f.commands[1].IdempotencyKey)
	require.LessOrEqual(t, len(f.commands[0].CommandID), 64)
	for _, model := range []any{&domain.Message{}, &domain.Conversation{}, &domain.AccountUser{}} {
		require.False(t, f.ops.DB.Migrator().HasTable(model))
	}
	require.False(t, f.msg.DB.Migrator().HasTable(&domain.CannedResponse{}))
}
func TestOPSCannedSendLifecycle(t *testing.T)         { cannedSendLifecycle(t, newModuleTestDB) }
func TestPostgresOPSCannedSendLifecycle(t *testing.T) { cannedSendLifecycle(t, postgresModuleDB) }

func cannedSendUnknownRecovery(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, mode := range []int32{1, 2, 4} {
		t.Run(fmt.Sprint("committed_response_", mode), func(t *testing.T) {
			f := newCannedSendFixture(t, open)
			v := f.prepare(t, "prepare")
			c := cannedIntentCommand("send", v)
			f.down.Store(mode)
			out := f.call(c)
			require.Equal(t, 504, out.Code, out.Body.String())
			require.Contains(t, out.Body.String(), "result_unknown")
			f.counts(t, 1, 0, 1)
			require.NoError(t, f.ops.DB.Delete(&domain.CannedResponse{}, 31).Error)
			f.restart(t)
			f.down.Store(0)
			first := messageAdmissionData(t, f.call(c))
			second := messageAdmissionData(t, f.call(cannedIntentCommand("retry-new-key", v)))
			require.JSONEq(t, string(first), string(second))
			f.counts(t, 1, 2, 1)
		})
	}
	t.Run("ops_receipt_update_rollback", func(t *testing.T) {
		f := newCannedSendFixture(t, open)
		v := f.prepare(t, "prepare")
		c := cannedIntentCommand("send", v)
		require.NoError(t, f.ops.DB.Callback().Update().Before("gorm:update").Register("test:ops-send-receipt-fail", func(tx *gorm.DB) {
			if row, ok := tx.Statement.Model.(*domain.LocalChangeJournal); ok && row.ObjectType == cannedResponseSend {
				tx.AddError(errors.New("injected receipt update rollback"))
			}
		}))
		out := f.call(c)
		require.Equal(t, 504, out.Code, out.Body.String())
		f.counts(t, 1, 0, 1)
		require.NoError(t, f.ops.DB.Callback().Update().Remove("test:ops-send-receipt-fail"))
		messageAdmissionData(t, f.call(c))
		f.counts(t, 1, 1, 1)
	})
	t.Run("request_canceled_after_msg_commit", func(t *testing.T) {
		f := newCannedSendFixture(t, open)
		v := f.prepare(t, "prepare")
		c := cannedIntentCommand("send", v)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		called := make(chan struct{})
		f.afterMSG = func() { cancel(); close(called) }
		raw, err := json.Marshal(c)
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/contracts/commands/"+c.CommandType, bytes.NewReader(raw)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+f.ops.Config.AuthToken)
		req.Header.Set(headerCaller, "API")
		req.Header.Set(headerContractVersion, "v1")
		req.Header.Set(headerCorrelationID, c.CorrelationID)
		out := httptest.NewRecorder()
		f.opsHandler.ServeHTTP(out, req)
		require.Equal(t, 504, out.Code, out.Body.String())
		<-called
		// Wait for the canceled transaction to finish before changing the fixture hook.
		require.Eventually(t, func() bool { db, _ := f.ops.DB.DB(); return db.Stats().InUse == 0 }, 2*time.Second, 5*time.Millisecond)
		f.afterMSG = nil
		f.counts(t, 1, 0, 1)
		messageAdmissionData(t, f.call(c))
		f.counts(t, 1, 1, 1)
	})
}
func TestOPSCannedSendUnknownRecovery(t *testing.T) { cannedSendUnknownRecovery(t, newModuleTestDB) }
func TestPostgresOPSCannedSendUnknownRecovery(t *testing.T) {
	cannedSendUnknownRecovery(t, postgresModuleDB)
}

func cannedSendMembership(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedSendFixture(t, open)
	v := f.prepare(t, "prepare")
	p := cannedPrepareCommand("prepare")
	c := cannedIntentCommand("send", v)
	messageAdmissionData(t, f.call(c))
	require.NoError(t, f.ten.Where("account_id = 91 AND user_id = 7").Delete(&domain.AccountUser{}).Error)
	for _, x := range []foundation.CommandEnvelope{p, c, cannedIntentCommand("new-send", v)} {
		f.deny(t, x, 404)
	}
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAgent}).Error)
	f.tenDown.Store(true)
	f.deny(t, p, 503)
	f.deny(t, c, 503)
	f.tenDown.Store(false)
	messageAdmissionData(t, f.call(c))
	for name, alter := range map[string]func(*foundation.CommandEnvelope){
		"prepare_variables": func(x *foundation.CommandEnvelope) { x.Input["variables"] = map[string]any{"name": "changed"} },
		"prepare_version":   func(x *foundation.CommandEnvelope) { x.Input["object_version"] = "2" },
		"prepare_source":    func(x *foundation.CommandEnvelope) { x.Input["canned_response_id"] = "32" },
	} {
		t.Run(name, func(t *testing.T) { x := cannedPrepareCommand("prepare"); alter(&x); f.deny(t, x, 409) })
	}
	for name, alter := range map[string]func(*foundation.CommandEnvelope){
		"send_confirmation": func(x *foundation.CommandEnvelope) { x.Input["resolved_content"] = "changed" },
		"send_target":       func(x *foundation.CommandEnvelope) { x.Target.ID = "12" },
		"send_intent":       func(x *foundation.CommandEnvelope) { x.Input["intent_id"] = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) { x := cannedIntentCommand("send", v); alter(&x); f.deny(t, x, 409) })
	}
	changed := p
	changed.Actor.ID = "8"
	f.deny(t, changed, 409)
	changed = c
	changed.Actor.ID = "8"
	f.deny(t, changed, 409)
	changed = cannedIntentCommand("other-actor", v)
	changed.Actor.ID = "8"
	f.deny(t, changed, 404)
	changed = cannedIntentCommand("other-account", v)
	changed.AccountID = 92
	f.deny(t, changed, 404)
	changed = cannedIntentCommand("other-conversation", v)
	changed.Target.ID = "12"
	f.deny(t, changed, 404)
	changed = cannedIntentCommand("different-confirmation", v)
	changed.Input["resolved_content"] = strings.TrimSpace(v.ResolvedContent)
	f.deny(t, changed, 409)
	f.counts(t, 1, 1, 1)
	for _, cmd := range []foundation.CommandEnvelope{p, c} {
		_, err := f.ops.Contracts.commands[cmd.CommandType](context.Background(), cmd)
		var fail *ContractFailure
		require.ErrorAs(t, err, &fail)
		require.Equal(t, "dependency_unavailable", fail.Type)
	}
}
func TestOPSCannedSendMembership(t *testing.T)         { cannedSendMembership(t, newModuleTestDB) }
func TestPostgresOPSCannedSendMembership(t *testing.T) { cannedSendMembership(t, postgresModuleDB) }

func cannedSendExactAndSnapshot(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedSendFixture(t, open)
	const id uint = 9007199254740993
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: id, UserID: id, Role: domain.RoleAgent}).Error)
	require.NoError(t, f.con.Create(&domain.Conversation{ID: id, AccountID: id, ContactID: id, Status: domain.ConversationStatusOpen}).Error)
	require.NoError(t, f.ops.DB.Create(&domain.CannedResponse{ID: id, AccountID: id, ShortCode: "large", Content: "{{name}}", ObjectVersion: int64(id + 2)}).Error)
	c := cannedPrepareCommand("large")
	c.AccountID = id
	c.Actor.ID = strconv.FormatUint(uint64(id), 10)
	c.Target.ID = c.Actor.ID
	c.Input["canned_response_id"] = c.Actor.ID
	c.Input["object_version"] = strconv.FormatUint(uint64(id+2), 10)
	var v cannedSendIntent
	require.NoError(t, json.Unmarshal(messageAdmissionData(t, f.call(c)), &v))
	require.Equal(t, c.Actor.ID, v.ActorID)
	require.Equal(t, c.Actor.ID, v.AccountID)
	require.Equal(t, c.Actor.ID, v.CannedResponseID)
	require.Equal(t, c.Input["object_version"], v.ObjectVersion)
	path := filepath.Join(t.TempDir(), "ops.json")
	require.NoError(t, CreateSnapshot(f.ops.DB, "OPS", "canned-intent", path))
	target := open(t, "OPS")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "OPS", "canned-intent", path, nil))
	f.ops.DB = target
	f.restart(t)
	var restored cannedSendIntent
	require.NoError(t, json.Unmarshal(messageAdmissionData(t, f.call(c)), &restored))
	require.Equal(t, v, restored)
	send := cannedIntentCommand("large-send", v)
	send.AccountID = id
	send.Actor = c.Actor
	// Force a large MSG identifier on both engines without depending on sequence allocation.
	require.NoError(t, f.msg.DB.Callback().Create().Before("gorm:create").Register("test:large-message-id", func(tx *gorm.DB) {
		if m, ok := tx.Statement.Dest.(*domain.Message); ok {
			m.ID = id
		}
	}))
	first := messageAdmissionData(t, f.call(send))
	require.Contains(t, string(first), `"id":9007199254740993`)
	replay := messageAdmissionData(t, f.call(send))
	require.Contains(t, string(replay), `"id":9007199254740993`)
	for _, raw := range [][]byte{first, replay} {
		var exact messageView
		require.NoError(t, json.Unmarshal(raw, &exact))
		require.Equal(t, id, exact.ID)
		require.Equal(t, id, exact.AccountID)
		require.Equal(t, id, exact.ConversationID)
		require.Equal(t, id, exact.SenderID)
	}
	f.counts(t, 1, 1, 1)
}
func TestOPSCannedSendExactAndSnapshot(t *testing.T) { cannedSendExactAndSnapshot(t, newModuleTestDB) }
func TestPostgresOPSCannedSendExactAndSnapshot(t *testing.T) {
	cannedSendExactAndSnapshot(t, postgresModuleDB)
}
