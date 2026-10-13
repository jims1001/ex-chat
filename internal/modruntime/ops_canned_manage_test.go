package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func cannedCommand(key, operation string) foundation.CommandEnvelope {
	c := foundation.CommandEnvelope{CommandID: key, CommandType: cannedResponseManage, AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"operation": operation}, IdempotencyKey: key, CorrelationID: "canned-correlation", CausationID: "canned-test", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 3000, OnTimeout: "query", StatusQuery: cannedResponseGet}}
	if operation != "delete" {
		c.Input["short_code"] = " Hello "
		c.Input["content"] = "Original <b>content</b>\n"
	}
	return c
}
func cannedTarget(c *foundation.CommandEnvelope, id uint, version uint64) {
	c.Target = foundation.ContractTarget{Type: "canned_response", ID: strconv.FormatUint(uint64(id), 10), ExpectedVersion: version}
}
func (f *cannedReadFixture) write(c foundation.CommandEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(f.runtime.Handler(), "/contracts/commands/"+cannedResponseManage, c, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		r.Header.Set(headerCorrelationID, c.CorrelationID)
	})
}
func cannedWrite(t *testing.T, f *cannedReadFixture, c foundation.CommandEnvelope, status int) json.RawMessage {
	t.Helper()
	out := f.write(c)
	require.Equal(t, status, out.Code, out.Body.String())
	var body struct{ Data json.RawMessage }
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
	return body.Data
}
func cannedCreated(t *testing.T, f *cannedReadFixture, key string) cannedVersionView {
	t.Helper()
	var row cannedVersionView
	require.NoError(t, json.Unmarshal(cannedWrite(t, f, cannedCommand(key, "create"), 200), &row))
	return row
}
func cannedCounts(t *testing.T, f *cannedReadFixture, owners, receipts, events int64) {
	t.Helper()
	var n int64
	require.NoError(t, f.runtime.DB.Model(&domain.CannedResponse{}).Count(&n).Error)
	require.Equal(t, owners, n)
	for action, want := range map[string]int64{"command_receipt": receipts, "event_outbox": events} {
		require.NoError(t, f.runtime.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", action).Count(&n).Error)
		require.Equal(t, want, n, action)
	}
}
func cannedLifecycle(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("shared_member_CRUD_and_exact_read_projection", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "create")
		require.Equal(t, int64(1), row.ObjectVersion)
		require.Equal(t, "hello", row.ShortCode)
		q := cannedReadQuery("detail")
		q.Filter["id"] = row.ID
		var raw struct{ Data map[string]any }
		require.NoError(t, json.Unmarshal(f.query(t, q, 200), &raw))
		require.Len(t, raw.Data, 6)
		require.NotContains(t, raw.Data, "object_version")
		q.Filter["mode"] = "detail_versioned"
		q.Fields = append(q.Fields, "object_version")
		require.NoError(t, json.Unmarshal(f.query(t, q, 200), &raw))
		require.Len(t, raw.Data, 7)
		require.Equal(t, float64(1), raw.Data["object_version"])
		update := cannedCommand("update", "update")
		cannedTarget(&update, row.ID, 1)
		update.Actor.ID = "8"
		update.Input["short_code"] = "\u2003BYE\t"
		update.Input["content"] = " \nkeep whitespace\n "
		require.NoError(t, json.Unmarshal(cannedWrite(t, f, update, 200), &row))
		require.Equal(t, int64(2), row.ObjectVersion)
		require.Equal(t, "bye", row.ShortCode)
		require.Equal(t, " \nkeep whitespace\n ", row.Content)
		stale := update
		stale.IdempotencyKey = "stale"
		cannedWrite(t, f, stale, 409)
		remove := cannedCommand("delete", "delete")
		cannedTarget(&remove, row.ID, 2)
		first := cannedWrite(t, f, remove, 200)
		require.JSONEq(t, string(first), string(cannedWrite(t, f, remove, 200)))
		f.query(t, q, 404)
		cannedCounts(t, f, 0, 3, 3)
		var events []domain.LocalChangeJournal
		require.NoError(t, f.runtime.DB.Where("action = ?", "event_outbox").Order("id").Find(&events).Error)
		for i, record := range events {
			var e foundation.EventEnvelope
			require.NoError(t, json.Unmarshal([]byte(record.AfterState), &e))
			require.Equal(t, uint64(i+1), e.AggregateVersion)
			require.Equal(t, cannedResponseChanged, e.EventType)
			require.Len(t, e.Payload, 3)
			require.NotContains(t, record.AfterState, "Original <b>")
		}
	})
	t.Run("same_key_replay_conflict_actor_and_revoked_membership", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		c := cannedCommand("stable", "create")
		first := cannedWrite(t, f, c, 200)
		c.CommandID = "retry-command"
		c.RequestedAt = time.Now().UTC()
		require.JSONEq(t, string(first), string(cannedWrite(t, f, c, 200)))
		c.Actor.ID = "8"
		cannedWrite(t, f, c, 409)
		c.Actor.ID = "7"
		c.Input["content"] = "different"
		cannedWrite(t, f, c, 409)
		c.Input["content"] = "Original <b>content</b>\n"
		f.fault.Store(1)
		cannedWrite(t, f, c, 503)
		f.fault.Store(0)
		require.NoError(t, f.ten.Where("account_id = ? AND user_id = ?", 91, 7).Delete(&domain.AccountUser{}).Error)
		cannedWrite(t, f, c, 404)
		cannedCounts(t, f, 1, 1, 1)
	})
	t.Run("cross_account_and_legacy_version_zero", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "own")
		other := cannedCommand("foreign", "update")
		other.AccountID = 92
		cannedTarget(&other, row.ID, 1)
		cannedWrite(t, f, other, 404)
		legacy := domain.CannedResponse{AccountID: 91, ShortCode: " legacy ", Content: "keep"}
		require.NoError(t, f.runtime.DB.Create(&legacy).Error)
		for _, op := range []string{"update", "delete"} {
			c := cannedCommand("legacy-"+op, op)
			cannedTarget(&c, legacy.ID, 1)
			cannedWrite(t, f, c, 409)
		}
		q := cannedReadQuery("detail_versioned")
		q.Fields = append(q.Fields, "object_version")
		q.Filter["id"] = legacy.ID
		var body struct{ Data cannedVersionView }
		require.NoError(t, json.Unmarshal(f.query(t, q, 200), &body))
		require.Zero(t, body.Data.ObjectVersion)
		c := cannedCommand("new-account", "create")
		c.AccountID = 92
		c.Target.ID = "92"
		cannedWrite(t, f, c, 200)
		cannedCounts(t, f, 3, 2, 2)
	})
	t.Run("normalized_conflicts_and_legacy_compatibility", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		repo := repository.NewCannedResponseRepository(f.runtime.DB)
		a := domain.CannedResponse{AccountID: 91, ShortCode: "\u2003MiXeD\n", Content: "first"}
		b := a
		require.NoError(t, repo.Create(&a))
		require.NoError(t, repo.Create(&b))
		c := cannedCommand("duplicate-legacy", "create")
		c.Input["short_code"] = "mixed"
		cannedWrite(t, f, c, 409)
		row := cannedCreated(t, f, "runtime")
		c = cannedCommand("duplicate-runtime", "create")
		c.Input["short_code"] = "\theLLo\u2003"
		cannedWrite(t, f, c, 409)
		legacy := domain.CannedResponse{AccountID: 91, ShortCode: "HELLO", Content: "body"}
		require.ErrorIs(t, repo.Create(&legacy), repository.ErrCannedResponseDuplicate)
		a.ShortCode = "Hello"
		require.ErrorIs(t, repo.Update(&a), repository.ErrCannedResponseDuplicate)
		stored, err := repo.FindByID(91, row.ID)
		require.NoError(t, err)
		stored.Content = "bypass"
		stored.ObjectVersion = 0
		require.ErrorIs(t, repo.Update(stored), repository.ErrCannedResponseVersioned)
		require.ErrorIs(t, repo.Delete(91, row.ID), repository.ErrCannedResponseVersioned)
		require.NoError(t, repo.Delete(92, row.ID))
		cannedCounts(t, f, 3, 1, 1)
	})
	t.Run("large_identifiers_and_versions_replay_exactly", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := domain.CannedResponse{ID: 9007199254740993, AccountID: 91, ShortCode: "large", Content: "old", ObjectVersion: 9007199254740994}
		require.NoError(t, f.runtime.DB.Create(&row).Error)
		c := cannedCommand("large", "update")
		cannedTarget(&c, row.ID, uint64(row.ObjectVersion))
		first := cannedWrite(t, f, c, 200)
		require.Contains(t, string(first), `"id":9007199254740993`)
		require.Contains(t, string(first), `"object_version":9007199254740995`)
		replay := cannedWrite(t, f, c, 200)
		require.Contains(t, string(replay), `"id":9007199254740993`)
		require.Contains(t, string(replay), `"object_version":9007199254740995`)
		require.JSONEq(t, string(first), string(cannedWrite(t, f, c, 200)))
	})
}
func TestOPSCannedResponseManagement(t *testing.T)         { cannedLifecycle(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseManagement(t *testing.T) { cannedLifecycle(t, postgresModuleDB) }

func cannedValidation(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	tests := []struct {
		name   string
		mutate func(*foundation.CommandEnvelope)
		status int
	}{
		{"unknown_input", func(c *foundation.CommandEnvelope) { c.Input["owner_id"] = 7 }, 400},
		{"missing_shortcode", func(c *foundation.CommandEnvelope) { delete(c.Input, "short_code") }, 400},
		{"empty_shortcode", func(c *foundation.CommandEnvelope) { c.Input["short_code"] = "\u2003\t" }, 400},
		{"overlong_shortcode", func(c *foundation.CommandEnvelope) { c.Input["short_code"] = strings.Repeat("界", 101) }, 400},
		{"unicode_100_characters", func(c *foundation.CommandEnvelope) { c.Input["short_code"] = strings.Repeat("界", 100) }, 200},
		{"empty_content", func(c *foundation.CommandEnvelope) { c.Input["content"] = "" }, 400},
		{"content_wrong_type", func(c *foundation.CommandEnvelope) { c.Input["content"] = 3 }, 400},
		{"account_target_mismatch", func(c *foundation.CommandEnvelope) { c.Target.ID = "92" }, 400},
		{"create_with_expected_version", func(c *foundation.CommandEnvelope) { c.Target.ExpectedVersion = 1 }, 400},
		{"unknown_operation", func(c *foundation.CommandEnvelope) { c.Input["operation"] = "adopt" }, 400},
		{"wrong_actor_type", func(c *foundation.CommandEnvelope) { c.Actor.Type = "System" }, 403},
		{"wrong_actor_auth", func(c *foundation.CommandEnvelope) { c.Actor.AuthSource = "user" }, 403},
		{"noncanonical_actor_id", func(c *foundation.CommandEnvelope) { c.Actor.ID = "007" }, 403},
		{"not_member", func(c *foundation.CommandEnvelope) { c.Actor.ID = "999" }, 404},
		{"zero_expected_version", func(c *foundation.CommandEnvelope) { c.Input["operation"] = "update"; cannedTarget(c, 1, 0) }, 400},
		{"overflow_version", func(c *foundation.CommandEnvelope) {
			c.Input["operation"] = "update"
			cannedTarget(c, 1, 9223372036854775807)
		}, 400},
		{"noncanonical_target", func(c *foundation.CommandEnvelope) {
			c.Input["operation"] = "update"
			cannedTarget(c, 1, 1)
			c.Target.ID = "01"
		}, 400},
		{"delete_extra_input", func(c *foundation.CommandEnvelope) { c.Input["operation"] = "delete"; cannedTarget(c, 1, 1) }, 400},
		{"whitespace_key", func(c *foundation.CommandEnvelope) { c.IdempotencyKey = " key " }, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newCannedReadFixture(t, open)
			c := cannedCommand("validation", "create")
			tc.mutate(&c)
			cannedWrite(t, f, c, tc.status)
			if tc.status != 200 {
				cannedCounts(t, f, 0, 0, 0)
			}
		})
	}
	t.Run("TEN_failure_responses_do_not_write", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		for fault := int32(1); fault <= 5; fault++ {
			f.fault.Store(fault)
			out := f.write(cannedCommand(fmt.Sprint("fault", fault), "create"))
			require.NotEqual(t, 200, out.Code)
		}
		cannedCounts(t, f, 0, 0, 0)
	})
	t.Run("missing_dependency", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		delete(f.runtime.Config.Dependencies, "TEN")
		cannedWrite(t, f, cannedCommand("missing", "create"), 503)
		cannedCounts(t, f, 0, 0, 0)
	})
	t.Run("old_and_versioned_projection_are_disjoint", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "projection")
		q := cannedReadQuery("detail")
		q.Filter["id"] = row.ID
		q.Fields = append(q.Fields, "object_version")
		f.query(t, q, 400)
		q = cannedReadQuery("detail_versioned")
		q.Filter["id"] = row.ID
		f.query(t, q, 400)
	})
	t.Run("versioned_read_retains_engineering_bounds", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "bounds")
		require.NoError(t, f.runtime.DB.Model(&domain.CannedResponse{}).Where("id = ?", row.ID).Update("content", strings.Repeat("x", repository.CannedResponseReadBytes+1)).Error)
		q := cannedReadQuery("detail_versioned")
		q.Fields = append(q.Fields, "object_version")
		q.Filter["id"] = row.ID
		f.query(t, q, 503)
	})
	t.Run("canceled_context_and_missing_transaction", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := f.runtime.Contracts.receipts.Execute(ctx, cannedCommand("cancel", "create"), func(context.Context) (any, error) { t.Fatal("canceled command ran"); return nil, nil })
		require.Error(t, err)
		_, err = f.runtime.Contracts.commands[cannedResponseManage](context.Background(), cannedCommand("direct", "create"))
		require.Error(t, err)
		cannedCounts(t, f, 0, 0, 0)
	})
}
func TestOPSCannedResponseValidation(t *testing.T)         { cannedValidation(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseValidation(t *testing.T) { cannedValidation(t, postgresModuleDB) }

func cannedRollback(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, stage := range []string{"receipt_insert", "owner_write", "outbox_write", "receipt_finalize"} {
		t.Run(stage, func(t *testing.T) {
			f := newCannedReadFixture(t, open)
			fault := errors.New("injected canned failure")
			name := "canned-" + stage
			if stage == "receipt_finalize" {
				require.NoError(t, f.runtime.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == "local_change_journals" {
						tx.AddError(fault)
					}
				}))
				defer f.runtime.DB.Callback().Update().Remove(name)
			} else {
				require.NoError(t, f.runtime.DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
					if stage == "owner_write" && tx.Statement.Table == "canned_responses" {
						tx.AddError(fault)
					}
					if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && ((stage == "receipt_insert" && row.Action == "command_receipt") || (stage == "outbox_write" && row.Action == "event_outbox")) {
						tx.AddError(fault)
					}
				}))
				defer f.runtime.DB.Callback().Create().Remove(name)
			}
			c := cannedCommand("rollback", "create")
			cannedWrite(t, f, c, 503)
			cannedCounts(t, f, 0, 0, 0)
			if stage == "receipt_finalize" {
				require.NoError(t, f.runtime.DB.Callback().Update().Remove(name))
			} else {
				require.NoError(t, f.runtime.DB.Callback().Create().Remove(name))
			}
			cannedWrite(t, f, c, 200)
			cannedCounts(t, f, 1, 1, 1)
		})
	}
	for _, op := range []string{"update", "delete"} {
		t.Run(op+"_outbox_rollback", func(t *testing.T) {
			f := newCannedReadFixture(t, open)
			row := cannedCreated(t, f, "seed")
			require.NoError(t, f.runtime.DB.Callback().Create().Before("gorm:create").Register("canned-outbox", func(tx *gorm.DB) {
				if r, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && r.Action == "event_outbox" {
					tx.AddError(errors.New("outbox failure"))
				}
			}))
			defer f.runtime.DB.Callback().Create().Remove("canned-outbox")
			c := cannedCommand("mutate", op)
			cannedTarget(&c, row.ID, 1)
			cannedWrite(t, f, c, 503)
			var stored domain.CannedResponse
			require.NoError(t, f.runtime.DB.First(&stored, row.ID).Error)
			require.Equal(t, int64(1), stored.ObjectVersion)
			require.Equal(t, row.Content, stored.Content)
			cannedCounts(t, f, 1, 1, 1)
		})
	}
}
func TestOPSCannedResponseRollback(t *testing.T)         { cannedRollback(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseRollback(t *testing.T) { cannedRollback(t, postgresModuleDB) }
