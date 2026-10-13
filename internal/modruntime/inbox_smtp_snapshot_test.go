package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func inboxSnapshotIntent(widget bool) domain.OrdinaryEmailIntent {
	scope := ordinaryPayloadHash("inbox-snapshot-scope")
	now := time.Date(2026, 10, 7, 12, 34, 56, 123456789, time.UTC)
	payload := `{"From":"sender@example.test","To":"recipient@example.test","Subject":"Transcript","HTML":"private body","Text":"private body"}`
	command := accountInboxTranscriptCommandID(scope)
	if widget {
		command = widgetInboxTranscriptCommandID(scope)
	}
	return domain.OrdinaryEmailIntent{
		ID: 1, AccountID: 91, EmailLogID: 2, EmailType: "transcript", RequestScopeHash: &scope,
		RequestInputHash: ordinaryPayloadHash("recipient@example.test"), TranscriptMessages: 2,
		Payload: payload, PayloadHash: ordinaryPayloadHash(payload), CommandID: command, CorrelationID: command,
		RouteKind: "inbox_smtp_v1", RouteAccountID: "91", RouteInboxID: "9007199254740993",
		RouteCreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), RouteFingerprint: domain.InboxSMTPFingerprintPrefix + strings.Repeat("ab", 32),
		State: "pending", MaxAttempts: 4, CreatedAt: now, UpdatedAt: now,
	}
}

func inboxSnapshotMutation(t *testing.T, raw []byte, mutate func(map[string]any, map[string]any, map[string]any)) []byte {
	t.Helper()
	var table map[string]any
	require.NoError(t, json.Unmarshal(raw, &table))
	row := table["rows"].([]any)[0].(map[string]any)
	private := row["private"].(map[string]any)
	mutate(table, row, private)
	out, err := json.Marshal(table)
	require.NoError(t, err)
	return out
}

func TestInboxSMTPSnapshotRoundTripPrivateRoutes(t *testing.T) {
	for _, widget := range []bool{false, true} {
		t.Run(strconv.FormatBool(widget), func(t *testing.T) {
			item := inboxSnapshotIntent(widget)
			raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{item})
			require.NoError(t, err)
			require.Contains(t, string(raw), `"codec":"msg.ordinary_email_intent.v4"`)
			require.Contains(t, string(raw), `"inbox_id":"9007199254740993"`)
			var rows []domain.OrdinaryEmailIntent
			require.NoError(t, unmarshalOrdinaryIntents(raw, &rows))
			require.Equal(t, []domain.OrdinaryEmailIntent{item}, rows)
			public, err := json.Marshal(item)
			require.NoError(t, err)
			for _, value := range []string{"route", "payload", "transport_key", "request", item.RouteInboxID, item.RouteCreatedAt, item.RouteFingerprint, item.CommandID, "private body", "recipient@example.test"} {
				require.NotContains(t, string(public), value)
			}
			for _, forbidden := range []string{"password", "smtp_host", "smtp_port", "username", "provider_config"} {
				require.NotContains(t, string(raw), forbidden)
			}
		})
	}
}

func TestInboxSMTPSnapshotRejectsMalformedOrUnscopedRoutes(t *testing.T) {
	item := inboxSnapshotIntent(false)
	raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{item})
	require.NoError(t, err)
	tests := map[string]func(map[string]any, map[string]any, map[string]any){
		"account_correlation_mismatch": func(_, _ map[string]any, p map[string]any) { p["correlation_id"] = "different-correlation" },
		"route_missing":                func(_, _ map[string]any, p map[string]any) { delete(p, "route") },
		"route_null":                   func(_, _ map[string]any, p map[string]any) { p["route"] = nil },
		"route_empty":                  func(_, _ map[string]any, p map[string]any) { p["route"] = map[string]any{} },
		"route_array":                  func(_, _ map[string]any, p map[string]any) { p["route"] = []any{} },
		"credentials_in_route":         func(_, _ map[string]any, p map[string]any) { p["route"].(map[string]any)["password"] = "secret" },
		"global_transport_on_inbox":    func(_, _ map[string]any, p map[string]any) { p["transport_key"] = strings.Repeat("a", 64) },
		"global_command_on_inbox": func(_, _ map[string]any, p map[string]any) {
			p["command_id"] = accountTranscriptCommandID(*item.RequestScopeHash)
		},
		"generic_command_on_inbox": func(_, _ map[string]any, p map[string]any) { p["command_id"] = "external-command"; p["request"] = nil },
		"request_missing":          func(_, _ map[string]any, p map[string]any) { delete(p, "request") },
		"request_null":             func(_, _ map[string]any, p map[string]any) { p["request"] = nil },
		"different_scope": func(_, _ map[string]any, p map[string]any) {
			p["request"].(map[string]any)["scope_hash"] = strings.Repeat("c", 64)
		},
		"notification_route": func(_ map[string]any, r, _ map[string]any) { r["email_type"] = "notification" },
		"unscrubbed_route_on_tombstone": func(_ map[string]any, r, p map[string]any) {
			r["anonymized"], r["state"] = true, "cancelled"
			p["payload"], p["payload_hash"] = "", ""
			p["request"].(map[string]any)["input_hash"] = ""
			p["request"].(map[string]any)["messages"] = 0
		},
	}
	for key, values := range map[string][]any{
		"kind":        {nil, "", "global", "inbox_smtp_v2"},
		"account_id":  {nil, "", "0", "091", "+91", "-91", "91.0", "9.1e1", " 91", "92", float64(91), "18446744073709551616"},
		"inbox_id":    {nil, "", "0", "01", "+1", "-1", "1.0", "1e1", " 1", float64(1), "18446744073709551616"},
		"created_at":  {nil, "", "0001-01-01T00:00:00Z", "2026-10-07T12:34:56.123456789+00:00", "2026-10-07T13:34:56.123456789+01:00", "2026-10-07T12:34:56.100Z", "yesterday"},
		"fingerprint": {nil, "", strings.Repeat("a", 64), domain.InboxSMTPFingerprintPrefix + strings.Repeat("AB", 32), domain.InboxSMTPFingerprintPrefix + strings.Repeat("a", 63), domain.InboxSMTPFingerprintPrefix + strings.Repeat("a", 65), domain.InboxSMTPFingerprintPrefix + strings.Repeat("g", 64)},
	} {
		for i, value := range values {
			tests[key+"_"+strconv.Itoa(i)] = func(_, _ map[string]any, p map[string]any) { p["route"].(map[string]any)[key] = value }
		}
		tests[key+"_omitted"] = func(_, _ map[string]any, p map[string]any) { delete(p["route"].(map[string]any), key) }
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			target := []domain.OrdinaryEmailIntent{{ID: 123}}
			damaged := inboxSnapshotMutation(t, raw, mutate)
			err := unmarshalOrdinaryIntents(damaged, &target)
			require.EqualError(t, err, "invalid ordinary email intent checkpoint")
			require.Equal(t, []domain.OrdinaryEmailIntent{{ID: 123}}, target, "failed decode must not replace the target")
		})
	}
	for name, damaged := range map[string][]byte{
		"duplicate_route": bytes.Replace(raw, []byte(`"route":`), []byte(`"route":null,"route":`), 1),
		"duplicate_kind":  bytes.Replace(raw, []byte(`"kind":"inbox_smtp_v1"`), []byte(`"kind":"global","kind":"inbox_smtp_v1"`), 1),
		"duplicate_codec": bytes.Replace(raw, []byte(`"codec":`), []byte(`"codec":"msg.ordinary_email_intent.v2","codec":`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			var rows []domain.OrdinaryEmailIntent
			require.Error(t, unmarshalOrdinaryIntents(damaged, &rows))
		})
	}
	widget := inboxSnapshotIntent(true)
	widget.TranscriptMessages = 101
	raw, err = marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{widget})
	require.NoError(t, err)
	var rows []domain.OrdinaryEmailIntent
	require.Error(t, unmarshalOrdinaryIntents(raw, &rows))
	widget.TranscriptMessages = 100
	widget.CorrelationID = "different-correlation"
	raw, err = marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{widget})
	require.NoError(t, err)
	require.Error(t, unmarshalOrdinaryIntents(raw, &rows))
}

func TestInboxSMTPSnapshotRejectsDowngradeAfterRouteErasure(t *testing.T) {
	for _, widget := range []bool{false, true} {
		for _, scrubbed := range []bool{false, true} {
			item := inboxSnapshotIntent(widget)
			if scrubbed {
				item.Anonymized, item.State = true, "cancelled"
				item.Payload, item.PayloadHash, item.RequestInputHash, item.TranscriptMessages = "", "", "", 0
				item.RouteKind, item.RouteAccountID, item.RouteInboxID, item.RouteCreatedAt, item.RouteFingerprint = "", "", "", "", ""
			}
			raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{item})
			require.NoError(t, err)
			for _, codec := range []string{"v1", "v2", "v3"} {
				for _, requestRemoved := range []bool{false, true} {
					name := strconv.FormatBool(widget) + "/" + strconv.FormatBool(scrubbed) + "/" + codec + "/" + strconv.FormatBool(requestRemoved)
					t.Run(name, func(t *testing.T) {
						damaged := inboxSnapshotMutation(t, raw, func(table, _ map[string]any, p map[string]any) {
							table["codec"] = "msg.ordinary_email_intent." + codec
							delete(p, "route")
							if !scrubbed && !requestRemoved && codec != "v1" {
								p["transport_key"] = strings.Repeat("a", 64)
							}
							if requestRemoved {
								p["request"] = nil
							}
							if codec == "v1" {
								delete(p, "request")
							}
						})
						var rows []domain.OrdinaryEmailIntent
						if scrubbed && (requestRemoved || codec == "v1") {
							// Once every distinguishing field is removed, these
							// bytes are also a legitimate legacy global tombstone.
							// No parser can authenticate their erased origin.
							require.NoError(t, unmarshalOrdinaryIntents(damaged, &rows))
							require.Nil(t, rows[0].RequestScopeHash)
							require.True(t, rows[0].Anonymized)
							require.Equal(t, "cancelled", rows[0].State)
						} else {
							require.Error(t, unmarshalOrdinaryIntents(damaged, &rows))
						}
					})
				}
			}
		}
	}
}

func TestInboxSMTPSnapshotPreservesGlobalEvidenceAndWireFormat(t *testing.T) {
	inbox := inboxSnapshotIntent(false)
	global := inbox
	global.RouteKind, global.RouteAccountID, global.RouteInboxID, global.RouteCreatedAt, global.RouteFingerprint = "", "", "", "", ""
	global.TransportKey = strings.Repeat("Z", 64) // Legacy validation intentionally required length, not a new hash format.
	for _, widget := range []bool{false, true} {
		global.CommandID = accountTranscriptCommandID(*global.RequestScopeHash)
		codec := "msg.ordinary_email_intent.v2"
		if widget {
			global.CommandID = widgetTranscriptCommandID(*global.RequestScopeHash)
			codec = "msg.ordinary_email_intent.v3"
		}
		global.CorrelationID = global.CommandID
		if !widget {
			global.CorrelationID = "legacy-account-correlation"
		}
		raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{global})
		require.NoError(t, err)
		expected, err := json.Marshal(ordinaryIntentTable{Codec: codec, Rows: []ordinaryIntentSnapshot{{OrdinaryEmailIntent: global, Private: &ordinaryIntentPrivate{
			Request: &ordinaryTranscriptRequestPrivate{ScopeHash: *global.RequestScopeHash, InputHash: global.RequestInputHash, Messages: global.TranscriptMessages},
			Payload: global.Payload, PayloadHash: global.PayloadHash, TransportKey: global.TransportKey, CommandID: global.CommandID, CorrelationID: global.CorrelationID,
		}}}})
		require.NoError(t, err)
		require.Equal(t, string(expected), string(raw))
		require.NotContains(t, string(raw), `"route"`)
		var rows []domain.OrdinaryEmailIntent
		require.NoError(t, unmarshalOrdinaryIntents(raw, &rows))
		require.Equal(t, global, rows[0])
		mixed, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{global, inbox})
		require.NoError(t, err)
		require.Contains(t, string(mixed), `"route":null`)
		require.NoError(t, unmarshalOrdinaryIntents(mixed, &rows))
		require.Equal(t, []domain.OrdinaryEmailIntent{global, inbox}, rows)
		for _, mutation := range []func(map[string]any, map[string]any, map[string]any){
			func(_, _ map[string]any, p map[string]any) { delete(p, "route") },
			func(_, _ map[string]any, p map[string]any) { p["route"] = map[string]any{} },
			func(_, _ map[string]any, p map[string]any) {
				p["route"] = map[string]any{"kind": "", "account_id": "", "inbox_id": "", "created_at": "", "fingerprint": ""}
			},
		} {
			require.Error(t, unmarshalOrdinaryIntents(inboxSnapshotMutation(t, mixed, mutation), &rows))
		}
	}
}

// Start from an authenticated producer's persisted request and then bind its
// route directly: these tests isolate snapshot/cleanup behavior from admission.
func inboxSnapshotPersisted(t *testing.T, widget, pg bool) (Runtime, domain.OrdinaryEmailIntent) {
	t.Helper()
	var r Runtime
	var item domain.OrdinaryEmailIntent
	if widget {
		f := widgetBridgeSetup(t, pg)
		f.submit(t)
		r, item = *f.b.runtime, f.item(t)
	} else {
		f := accountBridgeSetup(t, pg)
		f.submit(t)
		r, item = *f.b.runtime, f.item(t)
	}
	route := inboxSnapshotIntent(widget)
	item.RouteKind, item.RouteAccountID, item.RouteInboxID = route.RouteKind, route.RouteAccountID, "5"
	item.RouteCreatedAt, item.RouteFingerprint = route.RouteCreatedAt, route.RouteFingerprint
	item.TransportKey = ""
	item.CommandID = accountInboxTranscriptCommandID(*item.RequestScopeHash)
	if widget {
		item.CommandID = widgetInboxTranscriptCommandID(*item.RequestScopeHash)
	}
	item.CorrelationID = item.CommandID
	require.NoError(t, ordinaryEmailPrivateDB(r.DB).Save(&item).Error)
	r.InboxTranscriptSMTP = inboxSnapshotTransport{t}
	return r, item
}

type inboxSnapshotTransport struct{ t *testing.T }

func (s inboxSnapshotTransport) SendInboxTranscript(context.Context, domain.InboxSMTPBinding, string, string, string, string, string, func() error) domain.OrdinarySMTPResult {
	s.t.Error("snapshot test unexpectedly invoked the Inbox SMTP port")
	return domain.OrdinarySMTPResult{}
}

func TestInboxSMTPSnapshotRestoredWorkNeverResends(t *testing.T) { inboxSnapshotRestoreTest(t, false) }
func inboxSnapshotRestoreTest(t *testing.T, pg bool) {
	for _, widget := range []bool{false, true} {
		for _, state := range []string{"pending", "leased", "submitting", "retry"} {
			t.Run(strconv.FormatBool(widget)+"/"+state, func(t *testing.T) {
				r, item := inboxSnapshotPersisted(t, widget, pg)
				if state != "pending" {
					claim, ok, err := r.claimOrdinaryEmail(context.Background())
					require.NoError(t, err)
					require.True(t, ok)
					if state == "submitting" {
						require.NoError(t, r.startOrdinarySubmission(context.Background(), claim))
					}
					if state == "retry" {
						require.NoError(t, r.completeOrdinaryEmail(context.Background(), claim, "pending", "pre_submission_transient", true))
					}
				}
				path := filepath.Join(t.TempDir(), "inbox-msg.json")
				require.NoError(t, CreateSnapshot(r.DB, "MSG", "inbox-safety", path))
				target := ordinaryDB(t, pg)
				if target == nil {
					target = newModuleTestDB(t, "MSG")
				}
				require.NoError(t, RestoreSnapshot(context.Background(), target, "MSG", "inbox-safety", path, nil))
				var restored domain.OrdinaryEmailIntent
				require.NoError(t, target.First(&restored, item.ID).Error)
				require.Equal(t, "unknown", restored.State)
				require.Equal(t, "restored_checkpoint_unverified", restored.ErrorCode)
				require.Equal(t, item.RouteFingerprint, restored.RouteFingerprint)
				require.Equal(t, item.RequestScopeHash, restored.RequestScopeHash)
				r.DB = target
				r.OrdinaryEmailTransport = ordinaryTestTransport(func(context.Context, func() error) domain.OrdinarySMTPResult {
					t.Error("restored route was submitted")
					return domain.OrdinarySMTPResult{}
				})
				ran, err := r.RunOrdinaryEmailOnce(context.Background())
				require.NoError(t, err)
				require.False(t, ran)
			})
		}
	}
}

func TestInboxSMTPSnapshotScrubKeepsOnlyNoResendScope(t *testing.T) { inboxSnapshotScrubTest(t, false) }
func inboxSnapshotScrubTest(t *testing.T, pg bool) {
	for _, widget := range []bool{false, true} {
		t.Run(strconv.FormatBool(widget), func(t *testing.T) {
			r, before := inboxSnapshotPersisted(t, widget, pg)
			require.Equal(t, 200, ordinaryAnonymize(t, r, "scrub-inbox-route").Code)
			after := ordinaryRead(t, r, before.ID)
			require.True(t, after.Anonymized)
			require.Equal(t, "cancelled", after.State)
			require.Equal(t, before.RequestScopeHash, after.RequestScopeHash)
			require.Equal(t, before.CommandID, after.CommandID)
			require.Empty(t, after.RequestInputHash)
			require.Zero(t, after.TranscriptMessages)
			require.False(t, ordinarySnapshotHasRoute(after))
			require.Empty(t, after.Payload)
			require.Empty(t, after.PayloadHash)
			require.Empty(t, after.TransportKey)
			raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{after})
			require.NoError(t, err)
			require.Contains(t, string(raw), `"codec":"msg.ordinary_email_intent.v4"`)
			require.Contains(t, string(raw), `"route":null`)
			require.NotContains(t, string(raw), before.RouteFingerprint)
			var rows []domain.OrdinaryEmailIntent
			require.NoError(t, unmarshalOrdinaryIntents(raw, &rows))
			require.Equal(t, after, rows[0])
			path := filepath.Join(t.TempDir(), "scrubbed-inbox.json")
			require.NoError(t, CreateSnapshot(r.DB, "MSG", "inbox-scrub", path))
			fileRaw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NotContains(t, string(fileRaw), before.RouteFingerprint)
			require.NotContains(t, string(fileRaw), "ACCOUNT_TRANSCRIPT_PRIVATE_BODY")
			require.NotContains(t, string(fileRaw), "WIDGET_TRANSCRIPT_PUBLIC_BODY")
			require.NotContains(t, string(fileRaw), "recipient@example.test")
			target := ordinaryDB(t, pg)
			if target == nil {
				target = newModuleTestDB(t, "MSG")
			}
			require.NoError(t, RestoreSnapshot(context.Background(), target, "MSG", "inbox-scrub", path, nil))
			restored := ordinaryRead(t, Runtime{DB: target}, before.ID)
			require.Equal(t, after.CommandID, restored.CommandID)
			require.True(t, restored.Anonymized)
			require.False(t, ordinarySnapshotHasRoute(restored))
		})
	}
}

func TestInboxSMTPSnapshotPostgres(t *testing.T) {
	t.Run("restore", func(t *testing.T) { inboxSnapshotRestoreTest(t, true) })
	t.Run("scrub", func(t *testing.T) { inboxSnapshotScrubTest(t, true) })
}

func TestInboxSMTPSnapshotProducerIdentityHelpers(t *testing.T) {
	for _, scope := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		account, widget := accountInboxTranscriptCommandID(scope), widgetInboxTranscriptCommandID(scope)
		require.True(t, ordinaryInboxTranscriptCommand(account))
		require.True(t, ordinaryInboxTranscriptCommand(widget))
		require.True(t, ordinaryAccountTranscriptIdentity(account, scope))
		require.True(t, ordinaryWidgetTranscriptIdentity(widget, scope))
		require.False(t, ordinaryAccountTranscriptIdentity(widget, scope))
		require.False(t, ordinaryWidgetTranscriptIdentity(account, scope))
		require.False(t, ordinaryAccountTranscriptIdentity(account, strings.Repeat("c", 64)))
		require.False(t, ordinaryWidgetTranscriptIdentity(widget, strings.Repeat("c", 64)))
		require.LessOrEqual(t, len(account), 64)
		require.LessOrEqual(t, len(widget), 64)
	}
}

func TestInboxSMTPSnapshotRejectsCaseAliasedFields(t *testing.T) {
	original := inboxSnapshotIntent(false)
	raw, err := marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{original})
	require.NoError(t, err)
	for name, mutate := range map[string]func(map[string]any, map[string]any, map[string]any){
		"row_private": func(_, r, _ map[string]any) {
			r["Private"] = map[string]any{"route": map[string]any{"inbox_id": "999"}}
		},
		"row_unicode_state": func(_, r, _ map[string]any) { r["ſtate"] = "sent" },
		"private_route":     func(_, _ map[string]any, p map[string]any) { p["Route"] = map[string]any{"inbox_id": "999"} },
		"private_request":   func(_, _ map[string]any, p map[string]any) { p["Request"] = map[string]any{"messages": 199} },
		"route_fingerprint": func(_, _ map[string]any, p map[string]any) {
			p["route"].(map[string]any)["Fingerprint"] = domain.InboxSMTPFingerprintPrefix + strings.Repeat("cd", 32)
		},
		"route_kind_unicode": func(_, _ map[string]any, p map[string]any) {
			p["route"].(map[string]any)["Kind"] = domain.InboxSMTPBindingKind
		},
		"request_messages": func(_, _ map[string]any, p map[string]any) { p["request"].(map[string]any)["Messages"] = 199 },
		"top_rows":         func(table, _, _ map[string]any) { table["Rows"] = []any{} },
		"top_codec":        func(table, _, _ map[string]any) { table["Codec"] = "msg.ordinary_email_intent.v4" },
	} {
		t.Run(name, func(t *testing.T) {
			target := []domain.OrdinaryEmailIntent{{ID: 123}}
			require.EqualError(t, unmarshalOrdinaryIntents(inboxSnapshotMutation(t, raw, mutate), &target), "invalid ordinary email intent checkpoint")
			require.Equal(t, []domain.OrdinaryEmailIntent{{ID: 123}}, target)
		})
	}
	for name, suffix := range map[string]string{
		"private_after_canonical":             `,"Private":{"route":{"inbox_id":"999"}}}]}`,
		"private_route_after_canonical":       `,"Private":{"Route":{"inbox_id":"999"}}}]}`,
		"private_fingerprint_after_canonical": `,"Private":{"Route":{"Fingerprint":"` + domain.InboxSMTPFingerprintPrefix + strings.Repeat("cd", 32) + `"}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			// Reproduce the original bug: the case alias comes after the full
			// canonical private object and overwrites only its nested route ID.
			damaged := append(bytes.TrimSuffix(append([]byte(nil), raw...), []byte("}]}")), []byte(suffix)...)
			var target []domain.OrdinaryEmailIntent
			require.EqualError(t, unmarshalOrdinaryIntents(damaged, &target), "invalid ordinary email intent checkpoint")
		})
	}
	for _, damaged := range []string{
		`{"codec":"msg.ordinary_email_intent.v4","rows":[],"Codec":"msg.ordinary_email_intent.v3"}`,
		`{"Codec":"msg.ordinary_email_intent.v4","rows":[],"codec":"msg.ordinary_email_intent.v3"}`,
		`{"codec":"msg.ordinary_email_intent.v4","rows":[],"codec":"msg.ordinary_email_intent.v3"}`,
		`{"codec":"msg.ordinary_email_intent.v3","rows":[],"Codec":"msg.ordinary_email_intent.v4"}`,
		`{"codec":"msg.ordinary_email_intent.v4","Rows":[],"rows":[]}`,
	} {
		var target []domain.OrdinaryEmailIntent
		require.EqualError(t, unmarshalOrdinaryIntents([]byte(damaged), &target), "invalid ordinary email intent checkpoint")
	}
	// No v4 declaration means historical v1-v3 parsing behavior is unchanged.
	for _, codec := range []string{"v1", "v2", "v3"} {
		var target []domain.OrdinaryEmailIntent
		require.NoError(t, unmarshalOrdinaryIntents([]byte(`{"codec":"msg.ordinary_email_intent.`+codec+`","rows":[],"Codec":"msg.ordinary_email_intent.`+codec+`"}`), &target))
	}
	original.Payload = `{"Private":{"Route":{"Fingerprint":"opaque user content"}},"ſtate":"arbitrary text"}`
	original.PayloadHash = ordinaryPayloadHash(original.Payload)
	raw, err = marshalOrdinaryIntents([]domain.OrdinaryEmailIntent{original})
	require.NoError(t, err)
	var rows []domain.OrdinaryEmailIntent
	require.NoError(t, unmarshalOrdinaryIntents(raw, &rows))
	require.Equal(t, original.Payload, rows[0].Payload)
}
