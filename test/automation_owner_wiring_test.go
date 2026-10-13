package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAutomationOwnerWiring(t *testing.T) { runAutomationOwnerWiring(t, false) }

func TestPostgresAutomationOwnerWiring(t *testing.T) {
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL automation owner wiring")
	}
	runAutomationOwnerWiring(t, true)
}

// The real router owns the writers. Separate capture-enabled readers let the test
// inspect committed facts, but cannot turn capture on in a detached router writer.
// Background workers are deliberately disabled; every AUT effect is driven below.
func ownerWiringFixture(t *testing.T, native bool, mode string) *durableAutomationAPIFixture {
	t.Helper()
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "wiring.db"), JWTSecret: "owner-wiring-http-secret-2026-123456", JWTExpirationHours: 24, AutomationExecutionMode: mode}
	var db *gorm.DB
	var err error
	if native {
		c, e := pgx.ParseConfig(os.Getenv("DB_DSN"))
		require.NoError(t, e)
		hosts := []string{c.Host}
		for _, fallback := range c.Fallbacks {
			hosts = append(hosts, fallback.Host)
		}
		for _, host := range hosts {
			ip := net.ParseIP(host)
			require.True(t, host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
		}
		c.ConnectTimeout = 5 * time.Second
		admin := stdlib.OpenDB(*c)
		t.Cleanup(func() { require.NoError(t, admin.Close()) })
		schema := "automation_wiring_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err = admin.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		t.Cleanup(func() { _, e := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, e) })
		c.RuntimeParams["search_path"] = schema
		conn := stdlib.OpenDB(*c)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		db, err = gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		require.NoError(t, database.AutoMigrate(db))
		cfg.DBDriver = "postgres"
	} else {
		db, err = database.InitDB(cfg)
		require.NoError(t, err)
		conn, e := db.DB()
		require.NoError(t, e)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
	}
	gin.SetMode(gin.TestMode)
	f := &durableAutomationAPIFixture{t: t, db: db, router: router.SetupRouter(cfg, db, ws.NewHub())}
	f.con = repository.NewConversationRepository(db)
	f.msg = repository.NewMessageRepositoryWithConversationPort(db, f.con)
	if mode == "durable" {
		f.con.SetAutomationCapture(true)
		f.msg.SetAutomationCapture(true)
		f.restartBridge()
	}
	return f
}

type ownerWiringVisitor struct {
	path, token  string
	conversation domain.Conversation
}

func ownerWiringPublic(t *testing.T, f *durableAutomationAPIFixture, a durableAutomationAPITenant) ownerWiringVisitor {
	t.Helper()
	var contact struct {
		domain.Contact
		SourceID string `json:"source_id"`
		Token    string `json:"visitor_token"`
	}
	base := fmt.Sprintf("/public/api/v1/inboxes/%d/contacts", a.inboxID)
	f.request("", http.MethodPost, base, map[string]any{}, http.StatusCreated, &contact)
	require.NotEmpty(t, contact.SourceID)
	require.NotEmpty(t, contact.Token)
	a.contactID = contact.ID
	conv := f.conversation(a)
	return ownerWiringVisitor{path: base + "/" + contact.SourceID + fmt.Sprintf("/conversations/%d", conv.ID), token: contact.Token, conversation: conv}
}

func ownerWiringPublicRequest(t *testing.T, f *durableAutomationAPIFixture, v ownerWiringVisitor, method, suffix string, payload any, status int) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(method, v.path+suffix, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Visitor-Session", v.token)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, status, w.Code, "%s %s: %s", method, req.URL.Path, w.Body.String())
	return w
}

func ownerWiringMacro(t *testing.T, f *durableAutomationAPIFixture, a durableAutomationAPITenant, actions ...any) domain.Macro {
	t.Helper()
	raw, err := json.Marshal(actions)
	require.NoError(t, err)
	var macro domain.Macro
	f.request(a.token, http.MethodPost, a.path("/macros"), map[string]any{"name": "Owner wiring macro", "visibility": "global", "actions": string(raw)}, http.StatusCreated, &macro)
	require.NotZero(t, macro.ID)
	return macro
}

func ownerWiringExecute(t *testing.T, f *durableAutomationAPIFixture, a durableAutomationAPITenant, macro domain.Macro, convID uint, want string) {
	t.Helper()
	var result struct {
		Status  string          `json:"status"`
		Results map[uint]string `json:"results"`
	}
	f.request(a.token, http.MethodPost, a.path(fmt.Sprintf("/macros/%d/execute", macro.ID)), map[string]any{"conversation_ids": []uint{convID}}, http.StatusOK, &result)
	require.Equal(t, want, result.Status)
	if want == "success" {
		require.Equal(t, "success", result.Results[convID])
	} else {
		require.NotEqual(t, "success", result.Results[convID])
	}
}

func ownerWiringFacts(t *testing.T, f *durableAutomationAPIFixture) ([]foundation.EventEnvelope, []foundation.EventEnvelope) {
	t.Helper()
	con, err := f.con.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	msg, err := f.msg.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	return con, msg
}

func ownerWiringOrdinaryFact(t *testing.T, event foundation.EventEnvelope, module string, a durableAutomationAPITenant, conv domain.Conversation) {
	t.Helper()
	require.NoError(t, event.Validate())
	require.Equal(t, module, event.SourceModule)
	require.Equal(t, a.id, event.AccountID)
	require.Equal(t, fmt.Sprint(conv.ID), fmt.Sprint(event.Payload["conversation_id"]))
	require.Equal(t, "System", event.Actor.Type)
	require.Equal(t, "module", event.Actor.AuthSource)
	for _, key := range []string{"origin_execution_id", "automation_depth", "origin_object_version", "conversation_activity_prepared"} {
		require.NotContains(t, event.Payload, key)
	}
	if module == "CON" {
		require.Equal(t, "CON", event.Actor.ID)
		require.Equal(t, event.EventID, event.CorrelationID)
		require.Equal(t, event.EventID, event.CausationID)
		require.EqualValues(t, conv.ObjectVersion, event.AggregateVersion)
	} else {
		require.Equal(t, "monolith", event.Actor.ID)
		require.NotEmpty(t, event.CausationID)
		require.Equal(t, event.CorrelationID, event.CausationID)
	}
}

func ownerWiringExecution(t *testing.T, f *durableAutomationAPIFixture, a durableAutomationAPITenant, rule domain.AutomationRule, event foundation.EventEnvelope, status, reason string) domain.AutomationRuleExecution {
	t.Helper()
	var matches []domain.AutomationRuleExecution
	for _, e := range f.executions(a, rule.ID) {
		if e.EventID == event.EventID {
			matches = append(matches, e)
		}
	}
	require.Len(t, matches, 1)
	e := matches[0]
	require.Equal(t, status, e.Status)
	require.Equal(t, reason, e.Error)
	require.Nil(t, e.OriginExecutionID)
	require.Zero(t, e.AutomationDepth)
	return e
}

func ownerWiringFailCapture(t *testing.T, f *durableAutomationAPIFixture, module string, finalOnly bool) func() {
	t.Helper()
	name := "fail_owner_wiring_capture"
	require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if j, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && j.SourceModule == module && j.Action == "automation_trigger" && (!finalOnly || strings.Contains(j.ChangedFields, "status")) {
			tx.AddError(errors.New("synthetic owner wiring capture failure"))
		}
	}))
	return func() { require.NoError(t, f.db.Callback().Create().Remove(name)) }
}

func ownerWiringCount(t *testing.T, f *durableAutomationAPIFixture, model any, query string, args ...any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, f.db.Model(model).Where(query, args...).Count(&count).Error)
	return count
}

func runAutomationOwnerWiring(t *testing.T, native bool) {
	for _, source := range []string{"macro", "public"} {
		t.Run(source+"_status", func(t *testing.T) {
			f := ownerWiringFixture(t, native, "durable")
			a := f.tenant("status")
			v := ownerWiringPublic(t, f, a)
			conv := v.conversation
			f.flush()
			rule := f.rule(a, "conversation_resolved", []any{}, []any{durableAutomationAction("change_priority", "high")})
			require.Len(t, f.versions(a, rule.ID), 1)
			macro := ownerWiringMacro(t, f, a, durableAutomationAction("change_status", "resolved"))
			mutate := func() {
				if source == "macro" {
					ownerWiringExecute(t, f, a, macro, conv.ID, "success")
				} else {
					ownerWiringPublicRequest(t, f, v, http.MethodPost, "/toggle_status", map[string]any{"status": "resolved"}, http.StatusOK)
				}
			}
			before := f.state(a, conv.ID, "open", "low")
			mutate()
			committed := f.state(a, conv.ID, "resolved", "low")
			require.Equal(t, before.ObjectVersion+1, committed.ObjectVersion)
			require.Empty(t, f.executions(a, rule.ID))
			facts, messages := ownerWiringFacts(t, f)
			require.Len(t, facts, 1, "real router writer must capture exactly one committed CON fact")
			require.Empty(t, messages)
			event := facts[0]
			ownerWiringOrdinaryFact(t, event, "CON", a, committed)
			require.Equal(t, "open", event.Payload["status_before"])
			require.Equal(t, "resolved", event.Payload["status_after"])
			// Admission freezes the original action even if the rule is edited before draining.
			f.admit(event)
			f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), map[string]any{"name": "Edited after admission", "event_name": "conversation_resolved", "conditions": []any{}, "actions": []any{durableAutomationAction("change_priority", "urgent")}, "active": true}, http.StatusOK, nil)
			require.Len(t, f.versions(a, rule.ID), 2)
			f.flush()
			after := f.state(a, conv.ID, "resolved", "high")
			require.Equal(t, committed.ObjectVersion+1, after.ObjectVersion)
			e := f.execution(a, rule.ID, "succeeded", "")
			require.EqualValues(t, 1, e.RuleVersion)
			require.Equal(t, event.EventID, e.EventID)
			require.EqualValues(t, committed.ObjectVersion, e.TargetVersion)
			require.Nil(t, e.OriginExecutionID)
			require.Zero(t, e.AutomationDepth)
			f.restartBridge()
			f.admit(event)
			f.admit(event)
			require.Zero(t, f.flush())
			require.Equal(t, e.ID, f.execution(a, rule.ID, "succeeded", "").ID)
			require.Equal(t, after.ObjectVersion, f.state(a, conv.ID, "resolved", "high").ObjectVersion)
			mutate()
			repeated := f.state(a, conv.ID, "resolved", "high")
			facts, messages = ownerWiringFacts(t, f)
			require.Len(t, facts, 1)
			require.Empty(t, messages)
			require.NotContains(t, facts[0].Payload, "status_before")
			require.NotContains(t, facts[0].Payload, "status_after")
			f.flush()
			require.Len(t, f.executions(a, rule.ID), 1)
			require.Equal(t, repeated.ObjectVersion, f.state(a, conv.ID, "resolved", "high").ObjectVersion)
		})
	}
	for _, which := range []string{"macro_status", "public_status", "macro_message", "macro_label"} {
		t.Run(which+"_capture_rollback", func(t *testing.T) {
			f := ownerWiringFixture(t, native, "durable")
			a := f.tenant("rollback")
			v := ownerWiringPublic(t, f, a)
			conv := v.conversation
			f.flush()
			action := durableAutomationAction("change_status", "resolved")
			module := "CON"
			if which == "macro_message" {
				action = durableAutomationAction("send_message", "must roll back")
				module = "MSG"
			}
			if which == "macro_label" {
				action = durableAutomationAction("add_label", "rollback-label")
			}
			macro := ownerWiringMacro(t, f, a, action)
			remove := ownerWiringFailCapture(t, f, module, false)
			if which == "public_status" {
				ownerWiringPublicRequest(t, f, v, http.MethodPost, "/toggle_status", map[string]any{"status": "resolved"}, http.StatusInternalServerError)
			} else {
				ownerWiringExecute(t, f, a, macro, conv.ID, "failed")
			}
			remove()
			stored := f.state(a, conv.ID, "open", "low")
			require.Equal(t, conv.ObjectVersion, stored.ObjectVersion)
			facts, msg := ownerWiringFacts(t, f)
			require.Empty(t, facts)
			require.Empty(t, msg)
			require.Zero(t, ownerWiringCount(t, f, &domain.Message{}, "conversation_id = ?", conv.ID))
			require.Zero(t, ownerWiringCount(t, f, &domain.ConversationLabel{}, "conversation_id = ?", conv.ID))
			require.Zero(t, f.flush())
		})
	}
	t.Run("macro_multiaction_root_version_fence", func(t *testing.T) {
		f := ownerWiringFixture(t, native, "durable")
		a := f.tenant("multi")
		conv := f.conversation(a)
		f.flush()
		updated := f.rule(a, "conversation_updated", []any{}, []any{durableAutomationAction("change_priority", "high")})
		messageRule := f.rule(a, "message_created", []any{}, []any{durableAutomationAction("change_priority", "urgent")})
		macro := ownerWiringMacro(t, f, a, durableAutomationAction("add_label", "one-label"), durableAutomationAction("send_message", "one local macro message"), durableAutomationAction("change_status", "resolved"))
		ownerWiringExecute(t, f, a, macro, conv.ID, "success")
		committed := f.state(a, conv.ID, "resolved", "low")
		facts, messages := ownerWiringFacts(t, f)
		require.Len(t, facts, 2)
		require.Len(t, messages, 1)
		require.EqualValues(t, 1, ownerWiringCount(t, f, &domain.Message{}, "conversation_id = ?", conv.ID))
		require.EqualValues(t, 1, ownerWiringCount(t, f, &domain.ConversationLabel{}, "conversation_id = ?", conv.ID))
		var label, final foundation.EventEnvelope
		for _, event := range facts {
			if len(event.ChangedFields) == 1 && event.ChangedFields[0] == "labels" {
				label = event
			} else {
				final = event
			}
		}
		require.NotEmpty(t, label.EventID)
		require.NotEmpty(t, final.EventID)
		require.Less(t, label.AggregateVersion, final.AggregateVersion)
		ownerWiringOrdinaryFact(t, final, "CON", a, committed)
		ownerWiringOrdinaryFact(t, messages[0], "MSG", a, committed)
		require.Equal(t, fmt.Sprint(committed.ObjectVersion-1), fmt.Sprint(messages[0].Payload["object_version"]))
		f.flush()
		after := f.state(a, conv.ID, "resolved", "high")
		require.Equal(t, committed.ObjectVersion+1, after.ObjectVersion)
		oldLabel := ownerWiringExecution(t, f, a, updated, label, "failed", "target_version_stale")
		require.EqualValues(t, label.AggregateVersion, oldLabel.TargetVersion)
		oldMessage := ownerWiringExecution(t, f, a, messageRule, messages[0], "failed", "target_version_stale")
		require.EqualValues(t, committed.ObjectVersion-1, oldMessage.TargetVersion)
		ownerWiringExecution(t, f, a, updated, final, "succeeded", "")
		f.restartBridge()
		for _, event := range append(facts, messages...) {
			f.admit(event)
		}
		require.Zero(t, f.flush())
		require.Equal(t, after.ObjectVersion, f.state(a, conv.ID, "resolved", "high").ObjectVersion)
		require.EqualValues(t, 1, ownerWiringCount(t, f, &domain.LocalChangeJournal{}, "source_module = ? AND action = ? AND account_id = ? AND changed_fields = ?", "CON", "automation_trigger", a.id, "labels"))
		require.EqualValues(t, 1, ownerWiringCount(t, f, &domain.LocalChangeJournal{}, "source_module = ? AND action = ? AND account_id = ?", "MSG", "automation_trigger", a.id))
	})
	t.Run("macro_final_failure_preserves_earlier_commits", func(t *testing.T) {
		f := ownerWiringFixture(t, native, "durable")
		a := f.tenant("partial")
		conv := f.conversation(a)
		f.flush()
		macro := ownerWiringMacro(t, f, a, durableAutomationAction("add_label", "committed-label"), durableAutomationAction("add_private_note", "committed local note"), durableAutomationAction("change_status", "resolved"))
		remove := ownerWiringFailCapture(t, f, "CON", true)
		ownerWiringExecute(t, f, a, macro, conv.ID, "failed")
		remove()
		stored := f.state(a, conv.ID, "open", "low")
		require.Greater(t, stored.ObjectVersion, conv.ObjectVersion)
		facts, messages := ownerWiringFacts(t, f)
		require.Len(t, facts, 1)
		require.Equal(t, []string{"labels"}, facts[0].ChangedFields)
		require.Len(t, messages, 1)
		require.EqualValues(t, 1, ownerWiringCount(t, f, &domain.Message{}, "conversation_id = ? AND private = ?", conv.ID, true))
		require.EqualValues(t, 1, ownerWiringCount(t, f, &domain.ConversationLabel{}, "conversation_id = ?", conv.ID))
		f.flush()
	})
	t.Run("public_custom_attributes", func(t *testing.T) {
		f := ownerWiringFixture(t, native, "durable")
		a := f.tenant("attributes")
		v := ownerWiringPublic(t, f, a)
		conv := v.conversation
		f.flush()
		rule := f.rule(a, "conversation_updated", []any{}, []any{durableAutomationAction("change_priority", "high")})
		ownerWiringPublicRequest(t, f, v, http.MethodPatch, "/custom_attributes", map[string]any{"custom_attributes": map[string]any{"wiring": "captured"}}, http.StatusOK)
		committed := f.state(a, conv.ID, "open", "low")
		require.Contains(t, committed.CustomAttributes, "captured")
		facts, messages := ownerWiringFacts(t, f)
		require.Len(t, facts, 1)
		require.Empty(t, messages)
		require.Equal(t, []string{"custom_attributes"}, facts[0].ChangedFields)
		ownerWiringOrdinaryFact(t, facts[0], "CON", a, committed)
		f.flush()
		after := f.state(a, conv.ID, "open", "high")
		require.Equal(t, committed.ObjectVersion+1, after.ObjectVersion)
		ownerWiringExecution(t, f, a, rule, facts[0], "succeeded", "")
		f.restartBridge()
		f.admit(facts[0])
		require.Zero(t, f.flush())
		require.Equal(t, after.ObjectVersion, f.state(a, conv.ID, "open", "high").ObjectVersion)
	})
	t.Run("macro_authority", func(t *testing.T) {
		f := ownerWiringFixture(t, native, "durable")
		a, b := f.tenant("owner"), f.tenant("foreign")
		conv, foreign := f.conversation(a), f.conversation(b)
		f.flush()
		macro := ownerWiringMacro(t, f, a, durableAutomationAction("change_status", "resolved"))
		path := a.path(fmt.Sprintf("/macros/%d/execute", macro.ID))
		body := map[string]any{"conversation_id": conv.ID}
		f.request("", http.MethodPost, path, body, http.StatusUnauthorized, nil)
		f.request(b.token, http.MethodPost, path, body, http.StatusForbidden, nil)
		ownerWiringExecute(t, f, a, macro, foreign.ID, "failed")
		var member domain.AccountUser
		require.NoError(t, f.db.Where("account_id = ?", a.id).First(&member).Error)
		require.NoError(t, f.db.Delete(&member).Error)
		f.request(a.token, http.MethodPost, path, body, http.StatusForbidden, nil)
		var stored domain.Conversation
		require.NoError(t, f.db.First(&stored, conv.ID).Error)
		require.Equal(t, conv.ObjectVersion, stored.ObjectVersion)
		f.state(b, foreign.ID, "open", "low")
		facts, messages := ownerWiringFacts(t, f)
		require.Empty(t, facts)
		require.Empty(t, messages)
	})
	t.Run("public_authority", func(t *testing.T) {
		f := ownerWiringFixture(t, native, "durable")
		a, b := f.tenant("owner"), f.tenant("foreign")
		v, other := ownerWiringPublic(t, f, a), ownerWiringPublic(t, f, b)
		f.flush()
		body := map[string]any{"status": "resolved"}
		for _, token := range []string{"", "invalid", other.token} {
			bad := v
			bad.token = token
			ownerWiringPublicRequest(t, f, bad, http.MethodPost, "/toggle_status", body, http.StatusForbidden)
		}
		wrongConversation := v
		wrongConversation.path = strings.TrimSuffix(v.path, fmt.Sprint(v.conversation.ID)) + fmt.Sprint(other.conversation.ID)
		ownerWiringPublicRequest(t, f, wrongConversation, http.MethodPost, "/toggle_status", body, http.StatusNotFound)
		require.NoError(t, f.db.Where("inbox_id = ? AND contact_id = ?", a.inboxID, v.conversation.ContactID).Delete(&domain.ContactInbox{}).Error)
		ownerWiringPublicRequest(t, f, v, http.MethodPost, "/toggle_status", body, http.StatusForbidden)
		require.Equal(t, v.conversation.ObjectVersion, f.state(a, v.conversation.ID, "open", "low").ObjectVersion)
		require.Equal(t, other.conversation.ObjectVersion, f.state(b, other.conversation.ID, "open", "low").ObjectVersion)
		facts, messages := ownerWiringFacts(t, f)
		require.Empty(t, facts)
		require.Empty(t, messages)
	})
	t.Run("default_off", func(t *testing.T) {
		f := ownerWiringFixture(t, native, "")
		a := f.tenant("legacy")
		v := ownerWiringPublic(t, f, a)
		conv := v.conversation
		rule := f.rule(a, "conversation_updated", []any{}, []any{durableAutomationAction("change_priority", "urgent")})
		macro := ownerWiringMacro(t, f, a, durableAutomationAction("add_label", "legacy-label"), durableAutomationAction("add_private_note", "legacy local note"), durableAutomationAction("change_status", "resolved"))
		ownerWiringExecute(t, f, a, macro, conv.ID, "success")
		after := f.state(a, conv.ID, "resolved", "low")
		ownerWiringPublicRequest(t, f, v, http.MethodPost, "/toggle_status", map[string]any{"status": "open"}, http.StatusOK)
		ownerWiringPublicRequest(t, f, v, http.MethodPatch, "/custom_attributes", map[string]any{"custom_attributes": map[string]any{"legacy": true}}, http.StatusOK)
		final := f.state(a, conv.ID, "open", "low")
		require.Equal(t, after.ObjectVersion+2, final.ObjectVersion)
		require.Contains(t, final.CustomAttributes, "legacy")
		var messages []domain.Message
		require.NoError(t, f.db.Where("conversation_id = ?", conv.ID).Find(&messages).Error)
		require.Len(t, messages, 1)
		require.True(t, messages[0].Private)
		require.Equal(t, domain.MessageTypeOutgoing, messages[0].MessageType)
		require.Equal(t, domain.MessageStatusSent, messages[0].Status)
		require.NotZero(t, messages[0].SenderID)
		require.EqualValues(t, 1, ownerWiringCount(t, f, &domain.ConversationLabel{}, "conversation_id = ?", conv.ID))
		require.Empty(t, f.executions(a, rule.ID))
		require.Zero(t, ownerWiringCount(t, f, &domain.LocalChangeJournal{}, "action = ?", "automation_trigger"))
		require.False(t, f.con.AutomationCaptureEnabled())
		require.False(t, f.msg.AutomationCaptureEnabled())
		require.False(t, repository.NewConversationRepository(f.db).AutomationCaptureEnabled())
		require.False(t, repository.NewMessageRepository(f.db).AutomationCaptureEnabled())
	})
}
