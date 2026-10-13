package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func cannedEventDelivery(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, mode := range []string{"direct", "fanout"} {
		t.Run(mode, func(t *testing.T) {
			f := newCannedReadFixture(t, open)
			row := domain.CannedResponse{ID: 9007199254740993, AccountID: 91, ShortCode: "large", Content: "old", ObjectVersion: 9007199254740994}
			require.NoError(t, f.runtime.DB.Create(&row).Error)
			c := cannedCommand("large-event", "update")
			cannedTarget(&c, row.ID, uint64(row.ObjectVersion))
			cannedWrite(t, f, c, 200)
			received := make(chan []byte, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/contracts/events/"+cannedResponseChanged {
					w.WriteHeader(404)
					return
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				received <- raw
				w.WriteHeader(200)
			}))
			defer server.Close()
			dep := DependencyConfig{URL: server.URL, Token: "local-test-consumer"}
			want := 1
			if mode == "direct" {
				n, err := NewEventDispatcher(f.runtime.DB, "OPS", dep, "v1", time.Second).DeliverPending(context.Background(), 10)
				require.NoError(t, err)
				require.Equal(t, 1, n)
			} else {
				want = 2
				n, err := NewEventFanoutDispatcher(f.runtime.DB, "OPS", map[string]EventFanoutTarget{"ONE": {Dependency: dep, EventTypes: map[string]bool{cannedResponseChanged: true}}, "TWO": {Dependency: dep, EventTypes: map[string]bool{cannedResponseChanged: true}}}, "v1", time.Second).DeliverPending(context.Background(), 10)
				require.NoError(t, err)
				require.Equal(t, 1, n)
			}
			require.Len(t, received, want)
			for i := 0; i < want; i++ {
				raw := <-received
				require.Contains(t, string(raw), `"id":9007199254740993`)
				require.Contains(t, string(raw), `"object_version":9007199254740995`)
				var envelope map[string]any
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				require.NoError(t, decoder.Decode(&envelope))
				payload := envelope["payload"].(map[string]any)
				require.Equal(t, json.Number("9007199254740993"), payload["id"])
				require.Equal(t, json.Number("9007199254740995"), payload["object_version"])
				require.Equal(t, envelope["aggregate_id"], string(payload["id"].(json.Number)))
				require.Equal(t, envelope["aggregate_version"], payload["object_version"])
				require.NotContains(t, string(raw), "Original")
			}
			var pending int64
			require.NoError(t, f.runtime.DB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "pending").Count(&pending).Error)
			require.Zero(t, pending)
		})
	}
}
func TestOPSCannedResponseEventDelivery(t *testing.T) { cannedEventDelivery(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseEventDelivery(t *testing.T) {
	cannedEventDelivery(t, postgresModuleDB)
}

func cannedWire(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	cases := []struct {
		name   string
		edit   func(string) string
		status int
	}{
		{"fractional_version", func(s string) string { return strings.Replace(s, `"expected_version":1`, `"expected_version":1.0`, 1) }, 400},
		{"exponent_version", func(s string) string { return strings.Replace(s, `"expected_version":1`, `"expected_version":1e0`, 1) }, 400},
		{"string_version", func(s string) string { return strings.Replace(s, `"expected_version":1`, `"expected_version":"1"`, 1) }, 400},
		{"null_version", func(s string) string { return strings.Replace(s, `"expected_version":1`, `"expected_version":null`, 1) }, 400},
		{"negative_version", func(s string) string { return strings.Replace(s, `"expected_version":1`, `"expected_version":-1`, 1) }, 400},
		{"overflow_version", func(s string) string {
			return strings.Replace(s, `"expected_version":1`, `"expected_version":18446744073709551616`, 1)
		}, 400},
		{"unknown_target_field", func(s string) string {
			return strings.Replace(s, `"expected_version":1`, `"expected_version":1,"owner":7`, 1)
		}, 400},
		{"numeric_target_id", func(s string) string { return strings.Replace(s, `"id":"1"`, `"id":1`, 1) }, 400},
		{"unknown_envelope_field", func(s string) string { return strings.TrimSuffix(s, "}") + `,"unexpected":true}` }, 400},
		{"multiple_JSON_values", func(s string) string { return s + ` {}` }, 400},
		{"trailing_garbage", func(s string) string { return s + ` trailing` }, 400},
		// Match Go's established JSON decoding behavior explicitly: malformed text
		// is replaced with U+FFFD before UTF-8 and canonical identity validation.
		{"invalid_UTF8_replacement", func(s string) string {
			return strings.Replace(s, `Original \u003cb\u003econtent\u003c/b\u003e\n`, `bad`+string([]byte{255}), 1)
		}, 200},
		{"isolated_surrogate_replacement", func(s string) string {
			return strings.Replace(s, `Original \u003cb\u003econtent\u003c/b\u003e\n`, `bad\ud800`, 1)
		}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCannedReadFixture(t, open)
			row := cannedCreated(t, f, "seed")
			c := cannedCommand("wire", "update")
			cannedTarget(&c, row.ID, 1)
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			wire := tc.edit(string(raw))
			require.NotEqual(t, string(raw), wire)
			request := httptest.NewRequest(http.MethodPost, "/contracts/commands/"+cannedResponseManage, strings.NewReader(wire))
			request.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerContractVersion, "v1")
			request.Header.Set(headerCorrelationID, c.CorrelationID)
			out := httptest.NewRecorder()
			f.runtime.Handler().ServeHTTP(out, request)
			require.Equal(t, tc.status, out.Code, out.Body.String())
			if tc.status == 200 {
				require.Contains(t, out.Body.String(), "bad�")
				cannedCounts(t, f, 1, 2, 2)
			} else {
				cannedCounts(t, f, 1, 1, 1)
			}
		})
	}
	t.Run("content_above_64KiB_uses_existing_transport_bound", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		f.runtime.Config.MaxRequestBytes = 1 << 20
		c := cannedCommand("long-content", "create")
		c.Input["content"] = strings.Repeat("x", 70000)
		raw := cannedWrite(t, f, c, 200)
		require.Contains(t, string(raw), strings.Repeat("x", 70000))
	})
}
func TestOPSCannedResponseWire(t *testing.T)         { cannedWire(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseWire(t *testing.T) { cannedWire(t, postgresModuleDB) }
