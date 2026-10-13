package modruntime

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestOrdinaryEmailGlobalInboxPrefixCompatibility(t *testing.T) {
	ordinaryGlobalInboxPrefixCompatibility(t, false)
}

func TestPostgresOrdinaryEmailInboxPrefixCompatibility(t *testing.T) {
	if os.Getenv("DB_DSN") == "" {
		t.Skip("PostgreSQL compatibility test requires DB_DSN")
	}
	ordinaryGlobalInboxPrefixCompatibility(t, true)
}

func ordinaryGlobalInboxPrefixCompatibility(t *testing.T, pg bool) {
	for _, prefix := range []string{"acct-inbox-v1:", "wdg-inbox-v1:"} {
		t.Run(prefix+"public_admission", func(t *testing.T) {
			r := ordinaryRuntime(t, ordinaryDB(t, pg))
			cmd := ordinaryCommand(prefix + "legacy-global-command")
			first := ordinaryCall(r, cmd)
			require.Equal(t, 200, first.Code)
			replay := ordinaryCall(r, cmd)
			require.Equal(t, 200, replay.Code)
			var rows []domain.OrdinaryEmailIntent
			require.NoError(t, r.DB.Find(&rows).Error)
			require.Len(t, rows, 1)
			require.Equal(t, r.Config.OrdinaryEmail.key(), rows[0].TransportKey)
			require.Nil(t, rows[0].RequestScopeHash)
			require.False(t, ordinarySnapshotHasRoute(rows[0]))
		})
		for _, mode := range []string{"adapterless", "combined"} {
			t.Run(prefix+"persisted_worker_"+mode, func(t *testing.T) {
				r := ordinaryRuntime(t, ordinaryDB(t, pg))
				item := ordinaryEnqueue(t, r, "legacy-global-safe")
				require.NoError(t, r.DB.Model(&item).Update("command_id", prefix+"legacy-global-command").Error)
				if mode == "combined" {
					r.InboxTranscriptSMTP = inboxTranscriptTestTransport(func(context.Context, domain.InboxSMTPBinding, string, string, string, string, string, func() error) domain.OrdinarySMTPResult {
						t.Error("proven global work reached Inbox transport")
						return domain.OrdinarySMTPResult{Outcome: domain.OrdinarySMTPPermanentFailure}
					})
				}
				ran, err := r.RunOrdinaryEmailOnce(context.Background())
				require.NoError(t, err)
				require.True(t, ran)
				require.Equal(t, "sent", ordinaryRead(t, r, item.ID).State)
			})
		}
		for _, scrubbed := range []bool{false, true} {
			name := prefix + "v2_snapshot_live"
			if scrubbed {
				name = prefix + "v2_snapshot_scrubbed"
			}
			t.Run(name, func(t *testing.T) {
				r := ordinaryRuntime(t, ordinaryDB(t, pg))
				item := ordinaryEnqueue(t, r, "legacy-global-safe")
				item.CommandID = prefix + "legacy-global-command"
				item.EmailType = "transcript"
				if scrubbed {
					item.Anonymized = true
					item.State = "cancelled"
					item.Payload = ""
					item.PayloadHash = ""
					item.TransportKey = ""
				}
				raw, err := json.Marshal(ordinaryIntentTable{Codec: "msg.ordinary_email_intent.v2", Rows: []ordinaryIntentSnapshot{{OrdinaryEmailIntent: item, Private: &ordinaryIntentPrivate{Payload: item.Payload, PayloadHash: item.PayloadHash, TransportKey: item.TransportKey, CommandID: item.CommandID, CorrelationID: item.CorrelationID}}}})
				require.NoError(t, err)
				var rows []domain.OrdinaryEmailIntent
				require.NoError(t, unmarshalOrdinaryIntents(raw, &rows))
				require.Len(t, rows, 1)
				require.Nil(t, rows[0].RequestScopeHash)
				require.False(t, ordinarySnapshotHasRoute(rows[0]))
				if scrubbed {
					require.Empty(t, rows[0].Payload)
					require.Empty(t, rows[0].TransportKey)
					require.Equal(t, "cancelled", rows[0].State)
					require.NoError(t, r.DB.Save(&rows[0]).Error)
					ran, err := r.RunOrdinaryEmailOnce(context.Background())
					require.NoError(t, err)
					require.False(t, ran)
					require.Equal(t, "cancelled", ordinaryRead(t, r, item.ID).State)
				} else {
					require.Equal(t, r.Config.OrdinaryEmail.key(), rows[0].TransportKey)
				}
			})
		}
	}
}
