package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func confirmedPreviewCommand(key string) foundation.CommandEnvelope {
	c := messageAdmissionCommand(key)
	c.CommandType = messageFromCannedPreview
	c.Input = map[string]any{"canned_response_id": "9007199254740993", "object_version": "0", "resolved_content": "  Hello {{literal}} 世界  "}
	return c
}
func confirmedPreviewCall(f *messageAdmissionFixture, c foundation.CommandEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(f.runtime.Handler(), "/contracts/commands/"+c.CommandType, c, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		r.Header.Set(headerCorrelationID, c.CorrelationID)
	})
}
func confirmedPreviewDeny(t *testing.T, f *messageAdmissionFixture, c foundation.CommandEnvelope, status int) {
	t.Helper()
	before := f.state(t)
	r := confirmedPreviewCall(f, c)
	require.Equal(t, status, r.Code, r.Body.String())
	require.Equal(t, before, f.state(t))
	require.NotContains(t, r.Body.String(), `"data"`)
}
func confirmedPreviewSemantics(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("explicit_text_local_atomic_queue_and_restart", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := confirmedPreviewCommand("happy")
		first := messageAdmissionData(t, confirmedPreviewCall(f, c))
		var v messageView
		require.NoError(t, json.Unmarshal(first, &v))
		require.Equal(t, "Hello {{literal}} 世界", v.Content)
		require.Equal(t, "queued", v.Status)
		var m domain.Message
		require.NoError(t, f.runtime.DB.First(&m).Error)
		require.EqualValues(t, 7, m.SenderID)
		require.Equal(t, "User", m.SenderType)
		require.Equal(t, "outgoing", m.MessageType)
		require.Equal(t, "text", m.ContentType)
		require.False(t, m.Private)
		require.Empty(t, m.EchoID)
		require.True(t, m.CreatedAt.Equal(m.UpdatedAt))
		f.counts(t, 1, 1, 1)
		require.Equal(t, []string{"TEN:7", "TEN:7", "CON"}, f.takeCalls())
		var row domain.LocalChangeJournal
		require.NoError(t, f.runtime.DB.Where("action = ?", "event_outbox").First(&row).Error)
		var event foundation.EventEnvelope
		require.NoError(t, json.Unmarshal([]byte(row.AfterState), &event))
		require.Equal(t, "msg.message.created.v1", event.EventType)
		require.Len(t, event.Payload, 8)
		require.Equal(t, false, event.Payload["private"])
		require.Equal(t, "queued", event.Payload["status"])
		f.restart(t)
		f.conDown.Store(true)
		require.JSONEq(t, string(first), string(messageAdmissionData(t, confirmedPreviewCall(f, c))))
		require.Equal(t, []string{"TEN:7"}, f.takeCalls())
		f.counts(t, 1, 1, 1)
	})
	t.Run("revocation_outage_restore_and_new_key", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		c := confirmedPreviewCommand("revoked")
		messageAdmissionData(t, confirmedPreviewCall(f, c))
		f.revoke(t, 7)
		confirmedPreviewDeny(t, f, c, 404)
		require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAgent}).Error)
		f.fault.Store(1)
		confirmedPreviewDeny(t, f, c, 503)
		f.fault.Store(0)
		messageAdmissionData(t, confirmedPreviewCall(f, c))
		c.IdempotencyKey += "-new"
		messageAdmissionData(t, confirmedPreviewCall(f, c))
		f.counts(t, 2, 2, 2)
	})
	t.Run("intent_and_actor_conflicts", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		base := confirmedPreviewCommand("conflict")
		messageAdmissionData(t, confirmedPreviewCall(f, base))
		changes := []func(*foundation.CommandEnvelope){func(c *foundation.CommandEnvelope) { c.Actor.ID = "8" }, func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = "Hello" }, func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = "Hello {{literal}} 世界" }, func(c *foundation.CommandEnvelope) { c.Input["object_version"] = "1" }, func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = "2" }, func(c *foundation.CommandEnvelope) { c.Target.ID = "12" }}
		for _, change := range changes {
			c := confirmedPreviewCommand("conflict")
			change(&c)
			confirmedPreviewDeny(t, f, c, 409)
		}
		f.counts(t, 1, 1, 1)
	})
	for _, newFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("cross_capability_key_%t", newFirst), func(t *testing.T) {
			f := newMessageAdmissionFixture(t, open)
			a := messageAdmissionCommand("same")
			b := confirmedPreviewCommand("same")
			if newFirst {
				a, b = b, a
			}
			messageAdmissionData(t, confirmedPreviewCall(f, a))
			confirmedPreviewDeny(t, f, b, 409)
			f.counts(t, 1, 1, 1)
		})
	}
	t.Run("no_ops_dependency_or_foreign_source_lookup", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		require.NotContains(t, f.runtime.Config.Dependencies, "OPS")
		c := confirmedPreviewCommand("unverified")
		c.Input["canned_response_id"] = "9223372036854775807"
		c.Input["object_version"] = "9223372036854775807"
		messageAdmissionData(t, confirmedPreviewCall(f, c))
		f.counts(t, 1, 1, 1)
	})
}
func TestMSGConfirmedCannedPreviewSemantics(t *testing.T) {
	confirmedPreviewSemantics(t, newModuleTestDB)
}

func TestMSGConfirmedCannedPreviewValidation(t *testing.T) {
	changes := map[string]func(*foundation.CommandEnvelope){
		"system_actor": func(c *foundation.CommandEnvelope) { c.Actor.Type = "System" }, "wrong_auth": func(c *foundation.CommandEnvelope) { c.Actor.AuthSource = "api" }, "actor_leading_zero": func(c *foundation.CommandEnvelope) { c.Actor.ID = "07" },
		"target_mismatch_type": func(c *foundation.CommandEnvelope) { c.Target.Type = "message" }, "target_number_shape": func(c *foundation.CommandEnvelope) { c.Target.ID = "011" }, "expected_version": func(c *foundation.CommandEnvelope) { c.Target.ExpectedVersion = 1 },
		"source_number": func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = 1 }, "source_zero": func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = "0" }, "source_overflow": func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = "9223372036854775808" },
		"version_number": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = 0 }, "version_negative": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = "-1" }, "version_noncanonical": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = "00" }, "version_overflow": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = "9223372036854775808" },
		"missing": func(c *foundation.CommandEnvelope) { delete(c.Input, "resolved_content") }, "null": func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = nil }, "whitespace": func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = " \n\t " }, "raw_bound": func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = " " + strings.Repeat("x", 100000) },
		"variables": func(c *foundation.CommandEnvelope) { c.Input["variables"] = map[string]any{} }, "echo": func(c *foundation.CommandEnvelope) { c.Input["echo_id"] = "x" }, "private": func(c *foundation.CommandEnvelope) { c.Input["private"] = true }, "sender": func(c *foundation.CommandEnvelope) { c.Input["sender_id"] = "8" }, "content_type": func(c *foundation.CommandEnvelope) { c.Input["content_type"] = "html" },
	}
	f := newMessageAdmissionFixture(t, newModuleTestDB)
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			c := confirmedPreviewCommand(name)
			change(&c)
			status := 400
			if strings.HasPrefix(name, "system") || name == "wrong_auth" || name == "actor_leading_zero" {
				status = 403
			}
			confirmedPreviewDeny(t, f, c, status)
		})
	}
	t.Run("raw_exact_bound", func(t *testing.T) {
		c := confirmedPreviewCommand("bound")
		c.Input["resolved_content"] = strings.Repeat("x", 100000)
		messageAdmissionData(t, confirmedPreviewCall(f, c))
	})
	t.Run("direct_invalid_UTF8", func(t *testing.T) {
		c := confirmedPreviewCommand("utf8")
		c.Input["resolved_content"] = string([]byte{0xff})
		_, err := f.runtime.Contracts.commands[messageFromCannedPreview](context.Background(), c)
		require.Error(t, err)
	})
	t.Run("direct_no_transaction", func(t *testing.T) {
		before := f.state(t)
		_, err := f.runtime.Contracts.commands[messageFromCannedPreview](context.Background(), confirmedPreviewCommand("direct"))
		require.ErrorContains(t, err, "transaction")
		require.Equal(t, before, f.state(t))
	})
	t.Run("direct_current_membership", func(t *testing.T) {
		f.revoke(t, 7)
		before := f.state(t)
		_, err := f.runtime.Contracts.commands[messageFromCannedPreview](context.Background(), confirmedPreviewCommand("direct-revoked"))
		require.Error(t, err)
		require.Equal(t, before, f.state(t))
	})
}

func confirmedPreviewAtomicity(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("event_failure_rolls_back_all", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		require.NoError(t, f.runtime.DB.Callback().Create().Before("gorm:create").Register("confirmed_fail_event", func(tx *gorm.DB) {
			if j, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && j.Action == "event_outbox" {
				tx.AddError(fmt.Errorf("synthetic event failure"))
			}
		}))
		confirmedPreviewDeny(t, f, confirmedPreviewCommand("rollback"), 503)
		f.counts(t, 0, 0, 0)
		require.NoError(t, f.runtime.DB.Callback().Create().Remove("confirmed_fail_event"))
		messageAdmissionData(t, confirmedPreviewCall(f, confirmedPreviewCommand("rollback")))
		f.counts(t, 1, 1, 1)
	})
	t.Run("canceled_before_admission", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		before := f.state(t)
		_, err := f.runtime.Contracts.commands[messageFromCannedPreview](ctx, confirmedPreviewCommand("cancel"))
		require.Error(t, err)
		require.Equal(t, before, f.state(t))
		f.counts(t, 0, 0, 0)
	})
	t.Run("canceled_after_message_insert", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, open)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, f.runtime.DB.Callback().Create().After("gorm:create").Register("confirmed_cancel_message", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*domain.Message); ok {
				cancel()
			}
		}))
		defer f.runtime.DB.Callback().Create().Remove("confirmed_cancel_message")
		c := confirmedPreviewCommand("cancel-insert")
		_, err := f.runtime.Contracts.receipts.Execute(ctx, c, func(txctx context.Context) (any, error) {
			return f.runtime.Contracts.commands[messageFromCannedPreview](txctx, c)
		})
		require.Error(t, err)
		f.counts(t, 0, 0, 0)
	})
}
func TestMSGConfirmedCannedPreviewAtomicity(t *testing.T) {
	confirmedPreviewAtomicity(t, newModuleTestDB)
}
func TestPostgresMSGConfirmedCannedPreviewAtomicity(t *testing.T) {
	confirmedPreviewAtomicity(t, postgresModuleDB)
}

func confirmedPreviewExactIDs(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newMessageAdmissionFixture(t, open)
	const large = uint(9007199254740993)
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: large, Role: domain.RoleAgent}).Error)
	var mismatch bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q foundation.QueryEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&q))
		require.Equal(t, strconv.FormatUint(uint64(large), 10), q.Filter["id"])
		require.Equal(t, "MSG", r.Header.Get(headerCaller))
		id := large
		if mismatch {
			id++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": conversationView{ID: id, AccountID: 91, Status: domain.ConversationStatusOpen}})
	}))
	defer server.Close()
	f.runtime.Config.Dependencies["CON"] = DependencyConfig{URL: server.URL, Token: "synthetic"}
	c := confirmedPreviewCommand("large")
	c.Actor.ID = strconv.FormatUint(uint64(large), 10)
	c.Target.ID = c.Actor.ID
	first := messageAdmissionData(t, confirmedPreviewCall(f, c))
	var m domain.Message
	require.NoError(t, f.runtime.DB.First(&m).Error)
	require.Equal(t, large, m.SenderID)
	require.Equal(t, large, m.ConversationID)
	replay := messageAdmissionData(t, confirmedPreviewCall(f, c))
	for _, raw := range []json.RawMessage{first, replay} {
		var exact messageView
		require.NoError(t, json.Unmarshal(raw, &exact))
		require.Equal(t, large, exact.SenderID)
		require.Equal(t, large, exact.ConversationID)
	}
	require.JSONEq(t, string(first), string(replay))
	mismatch = true
	c.IdempotencyKey += "mismatch"
	confirmedPreviewDeny(t, f, c, 404)
	f.counts(t, 1, 1, 1)
}
func TestMSGConfirmedCannedPreviewExactIDs(t *testing.T) {
	confirmedPreviewExactIDs(t, newModuleTestDB)
}
func TestPostgresMSGConfirmedCannedPreviewExactIDs(t *testing.T) {
	confirmedPreviewExactIDs(t, postgresModuleDB)
}

func TestPostgresMSGConfirmedCannedPreviewConcurrency(t *testing.T) {
	f := newMessageAdmissionFixture(t, postgresModuleDB)
	c := confirmedPreviewCommand("parallel")
	const n = 8
	codes := make(chan int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; codes <- confirmedPreviewCall(f, c).Code }()
	}
	close(start)
	wg.Wait()
	close(codes)
	for code := range codes {
		require.Equal(t, 200, code)
	}
	f.counts(t, 1, 1, 1)
	// A client that lost the success response can recover the exact committed row.
	time.Sleep(time.Millisecond)
	messageAdmissionData(t, confirmedPreviewCall(f, c))
	f.counts(t, 1, 1, 1)
}

func TestMSGConfirmedCannedPreviewTargetAdmission(t *testing.T) {
	for _, mode := range []string{"foreign_account", "wrong_id", "deleting", "unavailable", "missing_dependency", "canceled_after_CON"} {
		t.Run(mode, func(t *testing.T) {
			f := newMessageAdmissionFixture(t, newModuleTestDB)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "unavailable" {
					w.WriteHeader(503)
					return
				}
				v := conversationView{ID: 11, AccountID: 91, Status: domain.ConversationStatusOpen}
				switch mode {
				case "foreign_account":
					v.AccountID = 92
				case "wrong_id":
					v.ID = 12
				case "deleting":
					v.Status = domain.ConversationStatusDeleting
				case "canceled_after_CON":
					cancel()
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
			}))
			defer server.Close()
			f.runtime.Config.Dependencies["CON"] = DependencyConfig{URL: server.URL, Token: "synthetic"}
			c := confirmedPreviewCommand(mode)
			if mode == "missing_dependency" {
				delete(f.runtime.Config.Dependencies, "CON")
			}
			if mode == "canceled_after_CON" {
				_, err := f.runtime.Contracts.receipts.Execute(ctx, c, func(txctx context.Context) (any, error) {
					return f.runtime.Contracts.commands[messageFromCannedPreview](txctx, c)
				})
				require.Error(t, err)
			} else {
				status := 404
				if mode == "unavailable" || mode == "missing_dependency" {
					status = 503
				}
				confirmedPreviewDeny(t, f, c, status)
			}
			f.counts(t, 0, 0, 0)
		})
	}
	t.Run("foreign_tenant_denied", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, newModuleTestDB)
		c := confirmedPreviewCommand("foreign-tenant")
		c.AccountID = 92
		confirmedPreviewDeny(t, f, c, 404)
		f.counts(t, 0, 0, 0)
	})
	t.Run("precise_large_receipt_message_id", func(t *testing.T) {
		f := newMessageAdmissionFixture(t, newModuleTestDB)
		c := confirmedPreviewCommand("large-receipt")
		const large = uint(9007199254740993)
		require.NoError(t, f.runtime.DB.Callback().Create().Before("gorm:create").Register("confirmed_large_message", func(tx *gorm.DB) {
			if m, ok := tx.Statement.Dest.(*domain.Message); ok {
				m.ID = large
			}
		}))
		defer f.runtime.DB.Callback().Create().Remove("confirmed_large_message")
		first := messageAdmissionData(t, confirmedPreviewCall(f, c))
		second := messageAdmissionData(t, confirmedPreviewCall(f, c))
		require.JSONEq(t, string(first), string(second))
		require.Contains(t, string(first), `"id":9007199254740993`)
		require.Contains(t, string(second), `"id":9007199254740993`)
		f.counts(t, 1, 1, 1)
	})
}

func TestMSGConfirmedCannedPreviewWire(t *testing.T) {
	f := newMessageAdmissionFixture(t, newModuleTestDB)
	c := confirmedPreviewCommand("wire")
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	for _, mode := range []string{"unknown_envelope", "trailing_JSON", "trailing_garbage", "bad_bearer", "untrusted_caller"} {
		t.Run(mode, func(t *testing.T) {
			body := string(raw)
			switch mode {
			case "unknown_envelope":
				body = strings.TrimSuffix(body, "}") + `,"unexpected":true}`
			case "trailing_JSON":
				body += " {}"
			case "trailing_garbage":
				body += " garbage"
			}
			req := httptest.NewRequest(http.MethodPost, "/contracts/commands/"+messageFromCannedPreview, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
			req.Header.Set(headerCaller, "API")
			req.Header.Set(headerContractVersion, "v1")
			req.Header.Set(headerCorrelationID, c.CorrelationID)
			status := 400
			if mode == "bad_bearer" {
				req.Header.Set("Authorization", "Bearer invalid")
				status = 401
			}
			if mode == "untrusted_caller" {
				req.Header.Set(headerCaller, "UNKNOWN")
				status = 401
			}
			before := f.state(t)
			out := httptest.NewRecorder()
			f.runtime.Handler().ServeHTTP(out, req)
			require.Equal(t, status, out.Code, out.Body.String())
			require.Equal(t, before, f.state(t))
		})
	}
	f.counts(t, 0, 0, 0)
}
