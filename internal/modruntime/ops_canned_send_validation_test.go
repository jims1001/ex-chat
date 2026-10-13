package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOPSCannedSendValidation(t *testing.T) {
	f := newCannedSendFixture(t, newModuleTestDB)
	prepareChanges := map[string]func(*foundation.CommandEnvelope){
		"system": func(c *foundation.CommandEnvelope) { c.Actor.Type = "System" }, "auth": func(c *foundation.CommandEnvelope) { c.Actor.AuthSource = "api" }, "actor_noncanonical": func(c *foundation.CommandEnvelope) { c.Actor.ID = "07" }, "target_type": func(c *foundation.CommandEnvelope) { c.Target.Type = "message" }, "target_noncanonical": func(c *foundation.CommandEnvelope) { c.Target.ID = "011" }, "expected_version": func(c *foundation.CommandEnvelope) { c.Target.ExpectedVersion = 1 }, "account_overflow": func(c *foundation.CommandEnvelope) { c.AccountID = 9223372036854775808 }, "key_spaces": func(c *foundation.CommandEnvelope) { c.IdempotencyKey = " padded " }, "source_number": func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = 31 }, "source_zero": func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = "0" }, "source_overflow": func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = "9223372036854775808" }, "version_number": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = 1 }, "version_negative": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = "-1" }, "version_noncanonical": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = "01" }, "version_overflow": func(c *foundation.CommandEnvelope) { c.Input["object_version"] = "9223372036854775808" }, "null_variables": func(c *foundation.CommandEnvelope) { c.Input["variables"] = nil }, "typed_variable": func(c *foundation.CommandEnvelope) { c.Input["variables"] = map[string]any{"name": 31} }, "bad_variable_name": func(c *foundation.CommandEnvelope) { c.Input["variables"] = map[string]any{"contact.name": "a"} }, "extra_input": func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = "injected" },
	}
	for name, change := range prepareChanges {
		t.Run(name, func(t *testing.T) {
			c := cannedPrepareCommand(name)
			change(&c)
			status := 400
			if name == "system" || name == "auth" || name == "actor_noncanonical" {
				status = 403
			}
			f.deny(t, c, status)
		})
	}
	for _, test := range []struct {
		content string
		want    int
	}{{"", 400}, {" \n\t ", 400}, {strings.Repeat("x", 100001), 400}, {"{{missing}}", 400}, {"{{broken", 400}, {strings.Repeat("x", 100000), 200}} {
		require.NoError(t, f.ops.DB.Model(&domain.CannedResponse{}).Where("id=31").Update("content", test.content).Error)
		c := cannedPrepareCommand(fmt.Sprint("bound-", len(test.content)))
		out := f.call(c)
		require.Equal(t, test.want, out.Code, out.Body.String())
	}
	require.NoError(t, f.ops.DB.Model(&domain.CannedResponse{}).Where("id=31").Update("content", "{{name}}").Error)
	v := f.prepare(t, "send-input")
	sendChanges := map[string]func(*foundation.CommandEnvelope){"id_short": func(c *foundation.CommandEnvelope) { c.Input["intent_id"] = "abc" }, "id_uppercase": func(c *foundation.CommandEnvelope) { c.Input["intent_id"] = strings.Repeat("A", 64) }, "id_nonhex": func(c *foundation.CommandEnvelope) { c.Input["intent_id"] = strings.Repeat("z", 64) }, "content_number": func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = 1 }, "content_blank": func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = " " }, "content_long": func(c *foundation.CommandEnvelope) { c.Input["resolved_content"] = strings.Repeat("x", 100001) }, "client_source": func(c *foundation.CommandEnvelope) { c.Input["canned_response_id"] = "31" }, "custom_key": func(c *foundation.CommandEnvelope) { c.Input["msg_idempotency_key"] = "arbitrary" }}
	for name, change := range sendChanges {
		t.Run(name, func(t *testing.T) { c := cannedIntentCommand(name, v); change(&c); f.deny(t, c, 400) })
	}
	c := cannedIntentCommand("missing", v)
	c.Input["intent_id"] = strings.Repeat("0", 64)
	f.deny(t, c, 404)
	for _, cmd := range []foundation.CommandEnvelope{cannedPrepareCommand("invalidutf8"), cannedIntentCommand("invalidutf8", v)} {
		if cmd.CommandType == cannedResponsePrepareSend {
			cmd.Input["variables"] = map[string]any{"name": string([]byte{0xff})}
		} else {
			cmd.Input["resolved_content"] = string([]byte{0xff})
		}
		require.Error(t, parseCannedSend(context.Background(), cmd))
	}
	require.Zero(t, f.msgCalls.Load())
}

func cannedSendProvenance(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedSendFixture(t, open)
	v := f.prepare(t, "prepare")
	var original domain.LocalChangeJournal
	require.NoError(t, f.ops.DB.Where("change_id = ?", "cmd-"+v.IntentID[:48]).First(&original).Error)
	for _, test := range []struct {
		name, field string
		value       any
	}{{"source", "source_module", "MSG"}, {"capability", "object_type", cannedResponseSend}, {"action", "action", "event_outbox"}, {"pending_result", "result", "pending"}, {"pending_status", "status", "pending"}, {"account", "account_id", 92}, {"actor_type", "actor_type", "System"}, {"actor", "actor_id", 8}, {"full_hash_tail", "idempotency_key_hash", v.IntentID[:48] + strings.Repeat("f", 16)}} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, f.ops.DB.Model(&domain.LocalChangeJournal{}).Where("id = ?", original.ID).Update(test.field, test.value).Error)
			f.deny(t, cannedIntentCommand(test.name, v), 404)
			require.NoError(t, f.ops.DB.Save(&original).Error)
		})
	}
	cases := map[string]func(map[string]any){"snapshot_id": func(m map[string]any) { m["intent_id"] = strings.Repeat("f", 64) }, "snapshot_account": func(m map[string]any) { m["account_id"] = "92" }, "snapshot_actor": func(m map[string]any) { m["actor_id"] = "8" }, "snapshot_conversation": func(m map[string]any) { m["conversation_id"] = "011" }, "numeric_id": func(m map[string]any) { m["canned_response_id"] = 31 }, "source_zero": func(m map[string]any) { m["canned_response_id"] = "0" }, "version_negative": func(m map[string]any) { m["object_version"] = "-1" }, "missing_content": func(m map[string]any) { delete(m, "resolved_content") }, "huge_content": func(m map[string]any) { m["resolved_content"] = strings.Repeat("x", 100001) }, "unknown_field": func(m map[string]any) { m["auth_source"] = "module" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(original.AfterState), &m))
			change(m)
			raw, err := json.Marshal(m)
			require.NoError(t, err)
			require.NoError(t, f.ops.DB.Model(&domain.LocalChangeJournal{}).Where("id=?", original.ID).Update("after_state", string(raw)).Error)
			f.deny(t, cannedIntentCommand(name, v), 503)
			require.NoError(t, f.ops.DB.Save(&original).Error)
		})
	}
	for _, raw := range []string{"null", "{}", original.AfterState + " {}", original.AfterState + " !"} {
		require.NoError(t, f.ops.DB.Model(&domain.LocalChangeJournal{}).Where("id=?", original.ID).Update("after_state", raw).Error)
		f.deny(t, cannedIntentCommand("bad-json", v), 503)
		require.NoError(t, f.ops.DB.Save(&original).Error)
	}
	f.counts(t, 1, 0, 0)
}
func TestOPSCannedSendProvenance(t *testing.T)         { cannedSendProvenance(t, newModuleTestDB) }
func TestPostgresOPSCannedSendProvenance(t *testing.T) { cannedSendProvenance(t, postgresModuleDB) }

func TestOPSCannedSendWire(t *testing.T) {
	f := newCannedSendFixture(t, newModuleTestDB)
	v := f.prepare(t, "prepare")
	for _, c := range []foundation.CommandEnvelope{cannedPrepareCommand("wire"), cannedIntentCommand("wire-send", v)} {
		raw, err := json.Marshal(c)
		require.NoError(t, err)
		for _, bad := range [][]byte{append(append([]byte(nil), raw...), []byte(" {}")...), append(append([]byte(nil), raw...), []byte(" !")...), bytes.Replace(raw, []byte(`"command_id"`), []byte(`"unknown":1,"command_id"`), 1)} {
			r := httptest.NewRequest("POST", "/contracts/commands/"+c.CommandType, bytes.NewReader(bad))
			r.Header.Set("Authorization", "Bearer "+f.ops.Config.AuthToken)
			r.Header.Set(headerCaller, "API")
			r.Header.Set(headerContractVersion, "v1")
			r.Header.Set(headerCorrelationID, c.CorrelationID)
			w := httptest.NewRecorder()
			f.opsHandler.ServeHTTP(w, r)
			require.Equal(t, 400, w.Code, w.Body.String())
		}
	}
	f.counts(t, 1, 0, 0)
}

func TestPostgresOPSCannedSendConcurrency(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprint("different_keys_", different), func(t *testing.T) {
			f := newCannedSendFixture(t, postgresModuleDB)
			v := f.prepare(t, "prepare")
			const n = 8
			var wg sync.WaitGroup
			start := make(chan struct{})
			out := make(chan *httptest.ResponseRecorder, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					key := "send"
					if different {
						key = fmt.Sprint(key, i)
					}
					out <- f.call(cannedIntentCommand(key, v))
				}(i)
			}
			close(start)
			wg.Wait()
			close(out)
			var first []byte
			for r := range out {
				data := messageAdmissionData(t, r)
				if first == nil {
					first = data
				} else {
					require.JSONEq(t, string(first), string(data))
				}
			}
			sends := int64(1)
			if different {
				sends = n
			}
			f.counts(t, 1, sends, 1)
		})
	}
	t.Run("prepare_key_concurrency", func(t *testing.T) {
		f := newCannedSendFixture(t, postgresModuleDB)
		const n = 8
		var wg sync.WaitGroup
		start := make(chan struct{})
		out := make(chan *httptest.ResponseRecorder, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; out <- f.call(cannedPrepareCommand("one-prepare")) }()
		}
		close(start)
		wg.Wait()
		close(out)
		var first []byte
		for r := range out {
			data := messageAdmissionData(t, r)
			if first == nil {
				first = data
			} else {
				require.JSONEq(t, string(first), string(data))
			}
		}
		f.counts(t, 1, 0, 0)
	})
}
func TestPostgresOPSCannedSendCommitFailure(t *testing.T) {
	f := newCannedSendFixture(t, postgresModuleDB)
	v := f.prepare(t, "prepare")
	c := cannedIntentCommand("send", v)
	require.NoError(t, f.ops.DB.Exec(`CREATE FUNCTION fail_canned_send_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.object_type = 'ops.canned_response.send.v1' THEN RAISE EXCEPTION 'injected deferred commit failure'; END IF; RETURN NEW; END $$`).Error)
	require.NoError(t, f.ops.DB.Exec(`CREATE CONSTRAINT TRIGGER fail_canned_send_commit AFTER INSERT OR UPDATE ON local_change_journals DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_canned_send_commit()`).Error)
	out := f.call(c)
	require.Equal(t, 504, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), "result_unknown")
	f.counts(t, 1, 0, 1)
	require.NoError(t, f.ops.DB.Exec(`DROP TRIGGER fail_canned_send_commit ON local_change_journals`).Error)
	require.NoError(t, f.ops.DB.Exec(`DROP FUNCTION fail_canned_send_commit()`).Error)
	messageAdmissionData(t, f.call(c))
	f.counts(t, 1, 1, 1)
}
