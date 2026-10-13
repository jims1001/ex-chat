package modruntime

import (
	"encoding/json"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
)

func TestCONStatusTriggerCarriesAtomicBeforeAfterAndNoopHasNoAlias(t *testing.T) {
	r := newAutomationOwnerRuntime(t, "CON")
	seedAutomationOwnerConversation(t, r)
	for index, status := range []string{"resolved", "resolved", "open"} {
		key := []string{"resolve-transition", "resolve-same", "open-transition"}[index]
		command := automationOwnerCommand(key, "con.status.change.v1", uint64(index+1), map[string]any{"status": status})
		callAutomationOwner(t, r, command, 200)
		callAutomationOwner(t, r, command, 200)
	}
	var rows []domain.LocalChangeJournal
	require.NoError(t, r.DB.Where("action = ?", "event_outbox").Order("id ASC").Find(&rows).Error)
	require.Len(t, rows, 3, "replay cannot duplicate transition facts")
	for index, row := range rows {
		var fact foundation.EventEnvelope
		require.NoError(t, json.Unmarshal([]byte(row.AfterState), &fact))
		require.Equal(t, "con.conversation_status.changed.v1", fact.EventType)
		require.Contains(t, fact.ChangedFields, "status")
		require.EqualValues(t, index+2, fact.Payload["object_version"])
		if index == 1 {
			require.NotContains(t, fact.Payload, "status_before")
			require.NotContains(t, fact.Payload, "status_after")
		} else {
			require.Equal(t, []string{"open", "", "resolved"}[index], fact.Payload["status_before"])
			require.Equal(t, []string{"resolved", "", "open"}[index], fact.Payload["status_after"])
		}
	}
}

func TestCONMuteTriggerCapturesResolutionInSameOwnerFact(t *testing.T) {
	r := newAutomationOwnerRuntime(t, "CON")
	seedAutomationOwnerConversation(t, r)
	command := automationOwnerCommand("mute-transition", "con.automation.apply.v1", 1, map[string]any{"operation": "mute_conversation"})
	callAutomationOwner(t, r, command, 200)
	callAutomationOwner(t, r, command, 200)
	command = automationOwnerCommand("mute-no-transition", "con.automation.apply.v1", 2, map[string]any{"operation": "mute_conversation"})
	callAutomationOwner(t, r, command, 200)
	var rows []domain.LocalChangeJournal
	require.NoError(t, r.DB.Where("action = ?", "event_outbox").Find(&rows).Error)
	require.Len(t, rows, 1)
	var fact foundation.EventEnvelope
	require.NoError(t, json.Unmarshal([]byte(rows[0].AfterState), &fact))
	require.Equal(t, "con.conversation.changed.v1", fact.EventType)
	require.Equal(t, "open", fact.Payload["status_before"])
	require.Equal(t, "resolved", fact.Payload["status_after"])
	require.Contains(t, fact.ChangedFields, "status")
}
