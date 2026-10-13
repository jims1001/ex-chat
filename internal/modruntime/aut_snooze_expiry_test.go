package modruntime

import (
	"context"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

func snoozeAliasFixture(t *testing.T, pg bool) (*autRunnerFixture, *repository.ConversationRepository) {
	t.Helper()
	f := newAUTRunnerFixture(t, pg, "[]")
	past := time.Now().UTC().Add(-time.Minute)
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).
		Updates(map[string]any{"status": "snoozed", "snoozed_until": past}).Error)
	source := triggerAliasSource(f)
	return f, source
}

func TestAUTSnoozeExpiryAliasesRecoverOnceThroughOrdinaryRules(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "SQLite", true: "Postgres"}[pg], func(t *testing.T) {
			f, source := snoozeAliasFixture(t, pg)
			opened := triggerAliasRule(t, f, 91, "conversation_opened", `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`, `[{"action_name":"resolve_conversation"}]`)
			updated := triggerAliasRule(t, f, 91, "conversation_updated", `[{"attribute_key":"status","filter_operator":"equal_to","values":["resolved"]}]`, `[{"action_name":"change_priority","action_params":"urgent"}]`)
			triggerAliasRule(t, f, 92, "conversation_opened", "[]", `[{"action_name":"change_priority","action_params":"low"}]`)
			winners, err := source.ResumeExpiredSnoozed(context.Background(), time.Now().UTC())
			require.NoError(t, err)
			require.Len(t, winners, 1)
			fact := triggerAliasFact(t, source)
			require.Equal(t, "snoozed", fact.Payload["status_before"])
			require.Equal(t, "open", fact.Payload["status_after"])
			// Restart after CON commit, before admission: the same durable fact
			// recovers without another expiry mutation or synthetic opened fact.
			restarted := triggerAliasSource(f)
			require.Equal(t, fact, triggerAliasFact(t, restarted))
			require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), fact))
			require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), fact.EventID))
			rows := triggerAliasExecutions(t, f, fact.EventID)
			require.Len(t, rows, 2)
			require.Equal(t, []uint{opened.ID, updated.ID}, []uint{rows[0].RuleID, rows[1].RuleID})
			require.Equal(t, rows[0].FactBatchKey, rows[1].FactBatchKey)
			require.EqualValues(t, 2, rows[0].TargetVersion)
			require.EqualValues(t, 2, rows[1].TargetVersion)
			f.run()
			f.assertTarget("resolved", "medium", 3)
			restored := restoreAUTOwnerFixture(t, f, pg)
			restored.run()
			restored.run()
			restored.assertTarget("resolved", "urgent", 4)
			triggerAliasResult(t, restored.execution(rows[0].ID), "succeeded", 3)
			triggerAliasResult(t, restored.execution(rows[1].ID), "succeeded", 4)
			require.NoError(t, restored.aut.AcceptAutomationFact(context.Background(), fact))
			require.NoError(t, restored.aut.AcceptAutomationFact(context.Background(), fact))
			ran, err := restored.aut.RunAutomationOnce(context.Background())
			require.NoError(t, err)
			require.False(t, ran, "acknowledged expiry replay must not dispatch again")
			require.Len(t, triggerAliasExecutions(t, restored, fact.EventID), 2)
			restored.assertTarget("resolved", "urgent", 4)
		})
	}
}

func TestAUTSnoozeExpiryManualEditFailsStaleAction(t *testing.T) {
	f, source := snoozeAliasFixture(t, false)
	triggerAliasRule(t, f, 91, "conversation_opened", "[]", `[{"action_name":"change_priority","action_params":"urgent"}]`)
	winners, err := source.ResumeExpiredSnoozed(context.Background(), time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, winners, 1)
	fact := triggerAliasFact(t, source)
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), fact))
	rows := triggerAliasExecutions(t, f, fact.EventID)
	require.Len(t, rows, 1)
	require.NoError(t, source.UpdateStatus(91, f.conversation.ID, "resolved", nil))
	f.run()
	f.assertTarget("resolved", "medium", 3)
	require.Equal(t, "failed", f.execution(rows[0].ID).Status)
}
