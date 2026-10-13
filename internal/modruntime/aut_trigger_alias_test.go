package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
)

func triggerAliasRule(t *testing.T, f *autRunnerFixture, account uint, event, conditions, actions string) domain.AutomationRule {
	t.Helper()
	rule := domain.AutomationRule{AccountID: account, Name: event, EventName: event, Conditions: conditions, Actions: actions, Active: true}
	require.NoError(t, service.NewAutomationService(f.aut.DB, nil, nil).CreateRule(&rule))
	return rule
}

func triggerAliasSource(f *autRunnerFixture) *repository.ConversationRepository {
	source := repository.NewConversationRepositoryWithReadPorts(f.con, nil, nil, nil, nil, nil, nil, nil)
	source.SetAutomationCapture(true)
	return source
}

func triggerAliasExecutions(t *testing.T, f *autRunnerFixture, eventID string) []domain.AutomationRuleExecution {
	t.Helper()
	var rows []domain.AutomationRuleExecution
	require.NoError(t, f.aut.DB.Where("event_id = ?", eventID).Order("id ASC").Find(&rows).Error)
	return rows
}

func triggerAliasFact(t *testing.T, source AutomationFactSource) foundation.EventEnvelope {
	t.Helper()
	facts, err := source.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	return facts[0]
}

func triggerAliasResult(t *testing.T, execution domain.AutomationRuleExecution, status string, version int) {
	t.Helper()
	require.Equal(t, status, execution.Status, "execution=%+v", execution)
	var results []automationActionResult
	require.NoError(t, json.Unmarshal([]byte(execution.ActionResults), &results))
	require.Len(t, results, 1)
	if status == "no_match" {
		require.Equal(t, "conditions_not_matched", execution.Error)
		require.Equal(t, "skipped", results[0].Status)
		require.Zero(t, results[0].ObjectVersion)
	} else {
		require.Equal(t, "succeeded", results[0].Status)
		require.Equal(t, version, results[0].ObjectVersion)
	}
}

func TestAUTOrdinaryStatusAliasesThroughOwnerBridge(t *testing.T) {
	for _, writer := range []string{"status", "update", "macro"} {
		for _, transition := range []struct{ before, after, alias string }{
			{"open", "resolved", "conversation_resolved"},
			{"resolved", "open", "conversation_opened"},
			{"pending", "open", "conversation_opened"},
			{"snoozed", "open", "conversation_opened"},
		} {
			t.Run(writer+"/"+transition.before+"_to_"+transition.after, func(t *testing.T) {
				f := newAUTRunnerFixture(t, false, "[]")
				require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", transition.before).Error)
				condition := fmt.Sprintf(`[{"attribute_key":"status","filter_operator":"equal_to","values":[%q]}]`, transition.after)
				updated := triggerAliasRule(t, f, 91, "conversation_updated", condition, `[{"action_name":"change_priority","action_params":"high"}]`)
				alias := triggerAliasRule(t, f, 91, transition.alias, condition, `[{"action_name":"change_priority","action_params":"urgent"}]`)
				nonmatching := triggerAliasRule(t, f, 91, transition.alias, `[{"attribute_key":"status","filter_operator":"equal_to","values":["never"]}]`, `[{"action_name":"change_priority","action_params":"low"}]`)
				opposite := "conversation_opened"
				if transition.alias == opposite {
					opposite = "conversation_resolved"
				}
				triggerAliasRule(t, f, 91, opposite, "[]", `[{"action_name":"change_priority","action_params":"low"}]`)
				source := triggerAliasSource(f)
				switch writer {
				case "status":
					require.NoError(t, source.UpdateStatus(91, f.conversation.ID, transition.after, nil))
				case "update":
					var c domain.Conversation
					require.NoError(t, f.con.First(&c, f.conversation.ID).Error)
					c.Status = transition.after
					require.NoError(t, source.Update(&c))
				case "macro":
					require.NoError(t, source.ApplyMacroMutation(91, f.conversation.ID, repository.MacroConversationMutation{Status: &transition.after}))
				}
				fact := triggerAliasFact(t, source)
				require.Equal(t, transition.before, fact.Payload["status_before"])
				require.Equal(t, transition.after, fact.Payload["status_after"])
				bridge := AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{source}}
				_, err := bridge.RunOnce(context.Background())
				require.NoError(t, err)
				rows := triggerAliasExecutions(t, f, fact.EventID)
				require.Len(t, rows, 3, "opposite alias and creation must not fire")
				require.Equal(t, []uint{updated.ID, alias.ID, nonmatching.ID}, []uint{rows[0].RuleID, rows[1].RuleID, rows[2].RuleID})
				triggerAliasResult(t, rows[0], "succeeded", 3)
				triggerAliasResult(t, rows[1], "succeeded", 4)
				triggerAliasResult(t, rows[2], "no_match", 0)
				require.Equal(t, "conversation_updated", rows[0].EventName)
				require.Equal(t, transition.alias, rows[1].EventName)
				require.NotEmpty(t, rows[0].FactBatchKey)
				for i, row := range rows {
					require.EqualValues(t, 2, row.TargetVersion)
					require.Equal(t, rows[0].FactBatchKey, row.FactBatchKey)
					require.Nil(t, row.MessageID)
					if i > 0 {
						require.NotNil(t, row.RunnerPredecessorID)
						require.Equal(t, rows[i-1].ID, *row.RunnerPredecessorID)
					}
				}
				f.assertTarget(transition.after, "urgent", 4)
				// Intake replay cannot duplicate any logical alias execution.
				require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), fact))
				require.Len(t, triggerAliasExecutions(t, f, fact.EventID), 3)
			})
		}
	}
}

func TestAUTOrdinaryMixedAliasesFollowRuleIDAcrossEventNames(t *testing.T) {
	f := newAUTRunnerFixture(t, false, "[]")
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", "resolved").Error)
	// Alias first deliberately reverses the owner's base-event/alias order.
	// Grouping candidates by event name would violate this causal rule order.
	opened := triggerAliasRule(t, f, 91, "conversation_opened", "[]", `[{"action_name":"change_priority","action_params":"high"}]`)
	updated := triggerAliasRule(t, f, 91, "conversation_updated", "[]", `[{"action_name":"change_priority","action_params":"urgent"}]`)
	source := triggerAliasSource(f)
	require.NoError(t, source.UpdateStatus(91, f.conversation.ID, "open", nil))
	fact := triggerAliasFact(t, source)
	bridge := AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{source}}
	_, err := bridge.RunOnce(context.Background())
	require.NoError(t, err)
	rows := triggerAliasExecutions(t, f, fact.EventID)
	require.Len(t, rows, 2)
	require.Equal(t, []uint{opened.ID, updated.ID}, []uint{rows[0].RuleID, rows[1].RuleID})
	require.Equal(t, []string{"conversation_opened", "conversation_updated"}, []string{rows[0].EventName, rows[1].EventName})
	require.Nil(t, rows[0].RunnerPredecessorID)
	require.NotNil(t, rows[1].RunnerPredecessorID)
	require.Equal(t, rows[0].ID, *rows[1].RunnerPredecessorID)
	require.Equal(t, rows[0].FactBatchKey, rows[1].FactBatchKey)
	triggerAliasResult(t, rows[0], "succeeded", 3)
	triggerAliasResult(t, rows[1], "succeeded", 4)
	f.assertTarget("open", "urgent", 4)
}

func TestAUTOrdinaryStatusNoopAndNonterminalTransitionsHaveNoAlias(t *testing.T) {
	for _, transition := range []struct{ before, after string }{{"open", "open"}, {"resolved", "resolved"}, {"open", "pending"}, {"open", "snoozed"}} {
		t.Run(transition.before+"_to_"+transition.after, func(t *testing.T) {
			f := newAUTRunnerFixture(t, false, "[]")
			require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", transition.before).Error)
			updated := triggerAliasRule(t, f, 91, "conversation_updated", "[]", `[{"action_name":"change_priority","action_params":"high"}]`)
			for _, event := range []string{"conversation_opened", "conversation_resolved"} {
				triggerAliasRule(t, f, 91, event, "[]", `[{"action_name":"change_priority","action_params":"low"}]`)
			}
			source := triggerAliasSource(f)
			require.NoError(t, source.UpdateStatus(91, f.conversation.ID, transition.after, nil))
			fact := triggerAliasFact(t, source)
			if transition.before == transition.after {
				require.NotContains(t, fact.Payload, "status_before")
				require.NotContains(t, fact.Payload, "status_after")
			}
			bridge := AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{source}}
			_, err := bridge.RunOnce(context.Background())
			require.NoError(t, err)
			rows := triggerAliasExecutions(t, f, fact.EventID)
			require.Len(t, rows, 1)
			require.Equal(t, updated.ID, rows[0].RuleID)
			triggerAliasResult(t, rows[0], "succeeded", 3)
			f.assertTarget(transition.after, "high", 3)
		})
	}
}

func TestAUTOrdinaryOpenCreationOnlyEmitsCreated(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":"urgent"}]`)
	for _, event := range []string{"conversation_updated", "conversation_opened", "conversation_resolved"} {
		triggerAliasRule(t, f, 91, event, "[]", `[{"action_name":"change_priority","action_params":"low"}]`)
	}
	source := triggerAliasSource(f)
	created := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, Status: "open", Priority: "medium"}
	require.NoError(t, source.Create(&created))
	fact := triggerAliasFact(t, source)
	require.Equal(t, "con.conversation.created.v1", fact.EventType)
	require.NotContains(t, fact.Payload, "status_before")
	require.NotContains(t, fact.Payload, "status_after")
	bridge := AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{source}}
	_, err := bridge.RunOnce(context.Background())
	require.NoError(t, err)
	rows := triggerAliasExecutions(t, f, fact.EventID)
	require.Len(t, rows, 1)
	require.Equal(t, f.rule.ID, rows[0].RuleID)
	require.Equal(t, "conversation_created", rows[0].EventName)
	triggerAliasResult(t, rows[0], "succeeded", 2)
	require.NoError(t, f.con.First(&created, created.ID).Error)
	require.Equal(t, "urgent", created.Priority)
	f.assertTarget("open", "medium", 1)
}

func TestAUTOrdinaryStatusAliasesRespectTenantBoundary(t *testing.T) {
	f := newAUTRunnerFixture(t, false, "[]")
	local := triggerAliasRule(t, f, 91, "conversation_resolved", "[]", `[{"action_name":"change_priority","action_params":"urgent"}]`)
	foreignRule := triggerAliasRule(t, f, 92, "conversation_resolved", "[]", `[{"action_name":"change_priority","action_params":"low"}]`)
	foreign := domain.Conversation{AccountID: 92, InboxID: 11, ContactID: 21, DisplayID: 1, Status: "open", Priority: "medium", ObjectVersion: 1}
	require.NoError(t, f.con.Create(&foreign).Error)
	source := triggerAliasSource(f)
	require.Error(t, source.UpdateStatus(92, f.conversation.ID, "resolved", nil))
	facts, err := source.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
	require.NoError(t, source.UpdateStatus(91, f.conversation.ID, "resolved", nil))
	fact := triggerAliasFact(t, source)
	bridge := AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{source}}
	_, err = bridge.RunOnce(context.Background())
	require.NoError(t, err)
	rows := triggerAliasExecutions(t, f, fact.EventID)
	require.Len(t, rows, 1)
	require.Equal(t, local.ID, rows[0].RuleID)
	require.EqualValues(t, 91, rows[0].AccountID)
	var count int64
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("rule_id = ?", foreignRule.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, f.con.First(&foreign, foreign.ID).Error)
	require.Equal(t, "medium", foreign.Priority)
	require.Equal(t, "open", foreign.Status)
	require.Equal(t, 1, foreign.ObjectVersion)
	f.assertTarget("resolved", "urgent", 3)
}

func TestAUTOrdinaryMixedAliasSnapshotPreservesDeferredSiblingConditions(t *testing.T) {
	f := newAUTRunnerFixture(t, false, "[]")
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", "pending").Error)
	updated := triggerAliasRule(t, f, 91, "conversation_updated", "[]", `[{"action_name":"resolve_conversation"}]`)
	resolved := triggerAliasRule(t, f, 91, "conversation_opened", `[{"attribute_key":"status","filter_operator":"equal_to","values":["resolved"]}]`, `[{"action_name":"change_priority","action_params":"low"}]`)
	opened := triggerAliasRule(t, f, 91, "conversation_opened", `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`, `[{"action_name":"change_priority","action_params":"urgent"}]`)
	source := triggerAliasSource(f)
	require.NoError(t, source.UpdateStatus(91, f.conversation.ID, "open", nil))
	fact := triggerAliasFact(t, source)
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), fact))
	require.NoError(t, source.MarkAutomationFactDelivered(context.Background(), fact.EventID))
	rows := triggerAliasExecutions(t, f, fact.EventID)
	require.Len(t, rows, 3)
	require.Equal(t, []uint{updated.ID, resolved.ID, opened.ID}, []uint{rows[0].RuleID, rows[1].RuleID, rows[2].RuleID})
	require.Equal(t, "evaluating", rows[1].Status)
	f.run()
	f.assertTarget("resolved", "medium", 3)
	// Frozen definitions survive restore, but later decisions wait for the
	// first sibling's confirmed status mutation before observing conditions.
	restored := restoreAUTOwnerFixture(t, f, false)
	for _, row := range triggerAliasExecutions(t, f, fact.EventID) {
		assertAUTSnapshotEqual(t, row, restored.execution(row.ID))
	}
	restored.run() // Evaluate the resolved condition
	restored.run() // Apply low priority
	restored.run() // The former open condition now misses
	restored.assertTarget("resolved", "low", 4)
	triggerAliasResult(t, restored.execution(rows[1].ID), "succeeded", 4)
	triggerAliasResult(t, restored.execution(rows[2].ID), "no_match", 0)
	require.NoError(t, restored.aut.AcceptAutomationFact(context.Background(), fact))
	require.Len(t, triggerAliasExecutions(t, restored, fact.EventID), 3)
	if ran, err := restored.aut.RunAutomationOnce(context.Background()); err != nil || ran {
		t.Fatalf("snapshot replay dispatched again: %v %v", ran, err)
	}

	path := filepath.Join(t.TempDir(), "mixed-alias.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "mixed-alias", path))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, defect := range []string{"missing_predecessor", "different_event", "foreign_tenant", "different_target"} {
		t.Run(defect, func(t *testing.T) {
			var file snapshotFile
			require.NoError(t, json.Unmarshal(original, &file))
			var table automationExecutionSnapshotTable
			require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
			require.Len(t, table.Rows, 3)
			switch defect {
			case "missing_predecessor":
				table.Rows[2].Private.RunnerPredecessorID = nil
			case "different_event":
				table.Rows[2].EventName = "conversation_resolved"
			case "foreign_tenant":
				table.Rows[2].AccountID = 92
			case "different_target":
				table.Rows[2].TargetVersion++
			}
			raw, err := json.Marshal(table)
			require.NoError(t, err)
			file.Snapshot.Tables["automation_rule_executions"] = raw
			payload, err := json.Marshal(file.Snapshot)
			require.NoError(t, err)
			file.Checksum = sha256Bytes(payload)
			raw, err = json.Marshal(file)
			require.NoError(t, err)
			candidate := filepath.Join(t.TempDir(), "invalid.json")
			require.NoError(t, os.WriteFile(candidate, raw, 0600))
			target := newModuleTestDB(t, "AUT")
			sentinel := domain.AutomationRule{ID: 700, AccountID: 91, Name: "keep", EventName: "conversation_created", Conditions: "[]", Actions: "[]"}
			require.NoError(t, target.Create(&sentinel).Error)
			require.Error(t, RestoreSnapshot(context.Background(), target, "AUT", "mixed-alias", candidate, nil))
			require.NoError(t, target.First(&sentinel, sentinel.ID).Error)
			require.Equal(t, "keep", sentinel.Name)
			var count int64
			require.NoError(t, target.Model(&domain.AutomationRuleExecution{}).Count(&count).Error)
			require.Zero(t, count, "invalid alias restore must roll back all execution rows")
		})
	}
}

func TestAUTOrdinaryMessageReopenAliasesKeepMessageConditionsScoped(t *testing.T) {
	f := newAutomationActionFixture(t, false, "[]")
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", "resolved").Error)
	contact := domain.Contact{ID: f.conversation.ContactID, AccountID: 91, Name: "visitor"}
	require.NoError(t, f.owners["CUS"].DB.Create(&contact).Error)
	messageRule := triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", `[{"attribute_key":"content","filter_operator":"contains","values":["reopen"]}]`, `[{"action_name":"change_priority","action_params":"high"}]`)
	updatedRule := triggerAliasRule(t, f.autRunnerFixture, 91, "conversation_updated", `[{"attribute_key":"content","filter_operator":"is_present"}]`, `[{"action_name":"change_priority","action_params":"low"}]`)
	openedRule := triggerAliasRule(t, f.autRunnerFixture, 91, "conversation_opened", `[{"attribute_key":"content","filter_operator":"is_not_present"}]`, `[{"action_name":"change_priority","action_params":"urgent"}]`)
	contacts := repository.NewContactRepository(f.owners["CUS"].DB)
	conversations := repository.NewConversationRepositoryWithReadPorts(f.con, nil, nil, nil, contacts, nil, nil, nil)
	messages := repository.NewMessageRepositoryWithPorts(f.owners["MSG"].DB, conversations, nil, contacts)
	messages.SetAutomationCapture(true)
	message := domain.Message{AccountID: 91, ConversationID: f.conversation.ID, SenderType: domain.SenderTypeContact, SenderID: contact.ID, MessageType: domain.MessageTypeIncoming, Content: "please reopen", ContentType: domain.ContentTypeText, Status: domain.MessageStatusSent}
	require.NoError(t, messages.CreateWithConversationReopen(&message, 1, true))
	fact := triggerAliasFact(t, messages)
	require.Equal(t, "resolved", fact.Payload["status_before"])
	require.Equal(t, "open", fact.Payload["status_after"])
	bridge := AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{messages}}
	_, err := bridge.RunOnce(context.Background())
	require.NoError(t, err)
	rows := triggerAliasExecutions(t, f.autRunnerFixture, fact.EventID)
	require.Len(t, rows, 3)
	require.Equal(t, []uint{messageRule.ID, updatedRule.ID, openedRule.ID}, []uint{rows[0].RuleID, rows[1].RuleID, rows[2].RuleID})
	triggerAliasResult(t, rows[0], "succeeded", 3)
	triggerAliasResult(t, rows[1], "no_match", 0)
	triggerAliasResult(t, rows[2], "succeeded", 4)
	for i, row := range rows {
		require.NotNil(t, row.MessageID)
		require.Equal(t, message.ID, *row.MessageID)
		require.EqualValues(t, 2, row.TargetVersion)
		require.Equal(t, rows[0].FactBatchKey, row.FactBatchKey)
		if i > 0 {
			require.NotNil(t, row.RunnerPredecessorID)
			require.Equal(t, rows[i-1].ID, *row.RunnerPredecessorID)
		}
	}
	f.assertTarget("open", "urgent", 4)
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), fact))
	require.Len(t, triggerAliasExecutions(t, f.autRunnerFixture, fact.EventID), 3)
}
