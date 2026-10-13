package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

func retryAutomationFact(message uint, before string, reopen bool) foundation.EventEnvelope {
	attempt := foundation.GenerateUUID()
	now := time.Now().UTC()
	identity := sha256.Sum256([]byte("msg-retry:" + attempt))
	event := foundation.EventEnvelope{EventID: "msg-retry-" + hex.EncodeToString(identity[:])[:40], EventType: "msg.message_status.changed.v1", EventVersion: "v1", SourceModule: "MSG", AccountID: 91, AggregateType: "Message", AggregateID: strconv.FormatUint(uint64(message), 10), AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "retry", AuthSource: "module"}, CorrelationID: attempt, CausationID: attempt, ChangedFields: []string{"message_status"}, DataClassification: "internal", Payload: map[string]any{"operation": "retry", "retry_profile": automationRetryProfile, "retry_attempt_id": attempt, "retry_sequence": 1, "retry_receipt_verified": true, "retry_activity_result": "applied", "retry_observed_status": before, "local_outcome": domain.MessageStatusSaved, "status": domain.MessageStatusSaved, "object_version": 2, "retry_expected_version": 1, "conversation_id": uint(1), "message_id": message, "message_type": domain.MessageTypeIncoming, "private": false}}
	if reopen {
		event.Payload["status_before"], event.Payload["status_after"] = "resolved", "open"
		event.ChangedFields = append(event.ChangedFields, "status")
	}
	return event
}

func TestAUTExplicitRetryFactRequiresVerifiedNarrowProfile(t *testing.T) {
	event := retryAutomationFact(101, "resolved", true)
	names, err := automationFactEventNames(event)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"message_created": true, "conversation_updated": true, "conversation_opened": true}, names)
	mutations := map[string]func(*foundation.EventEnvelope){
		"provider_sent": func(e *foundation.EventEnvelope) { e.Payload = map[string]any{"status": "sent"} },
		"standalone_retry": func(e *foundation.EventEnvelope) {
			e.Payload = map[string]any{"operation": "retry", "status": "queued"}
		},
		"unknown_profile":          func(e *foundation.EventEnvelope) { e.Payload["retry_profile"] = "msg.retry_attempt.v2" },
		"unverified":               func(e *foundation.EventEnvelope) { e.Payload["retry_receipt_verified"] = false },
		"string_verified":          func(e *foundation.EventEnvelope) { e.Payload["retry_receipt_verified"] = "true" },
		"no_attempt":               func(e *foundation.EventEnvelope) { delete(e.Payload, "retry_attempt_id") },
		"wrong_attempt":            func(e *foundation.EventEnvelope) { e.Payload["retry_attempt_id"] = "another" },
		"zero_sequence":            func(e *foundation.EventEnvelope) { e.Payload["retry_sequence"] = 0 },
		"fraction_sequence":        func(e *foundation.EventEnvelope) { e.Payload["retry_sequence"] = 1.2 },
		"wrong_event_identity":     func(e *foundation.EventEnvelope) { e.EventID = foundation.GenerateUUID() },
		"missing_expected_version": func(e *foundation.EventEnvelope) { delete(e.Payload, "retry_expected_version") },
		"wrong_expected_version":   func(e *foundation.EventEnvelope) { e.Payload["retry_expected_version"] = 2 },
		"missing_activity_result":  func(e *foundation.EventEnvelope) { delete(e.Payload, "retry_activity_result") },
		"unknown_activity_result":  func(e *foundation.EventEnvelope) { e.Payload["retry_activity_result"] = "pending" },
		"stale_reopen": func(e *foundation.EventEnvelope) {
			e.Payload["retry_activity_result"], e.Payload["retry_expected_version"] = "stale", 2
		},
		"false_status":         func(e *foundation.EventEnvelope) { e.Payload["status"] = "sent" },
		"wrong_sequence":       func(e *foundation.EventEnvelope) { e.AggregateVersion++ },
		"no_target":            func(e *foundation.EventEnvelope) { delete(e.Payload, "object_version") },
		"wrong_message":        func(e *foundation.EventEnvelope) { e.Payload["message_id"] = uint(202) },
		"wrong_observation":    func(e *foundation.EventEnvelope) { e.Payload["retry_observed_status"] = "open" },
		"deleting_observation": func(e *foundation.EventEnvelope) { e.Payload["retry_observed_status"] = "deleting" },
		"no_observation":       func(e *foundation.EventEnvelope) { delete(e.Payload, "retry_observed_status") },
		"false_sent":           func(e *foundation.EventEnvelope) { e.Payload["local_outcome"] = "sent" },
		"incoming_queued":      func(e *foundation.EventEnvelope) { e.Payload["local_outcome"] = "queued" },
		"private_queued":       func(e *foundation.EventEnvelope) { e.Payload["private"], e.Payload["local_outcome"] = true, "queued" },
		"no_private":           func(e *foundation.EventEnvelope) { delete(e.Payload, "private") },
		"no_message_type":      func(e *foundation.EventEnvelope) { delete(e.Payload, "message_type") },
		"outgoing_reopen":      func(e *foundation.EventEnvelope) { e.Payload["message_type"] = "outgoing" },
		"snooze_reopen": func(e *foundation.EventEnvelope) {
			e.Payload["retry_observed_status"], e.Payload["status_before"] = "snoozed", "snoozed"
		},
		"resolve_alias":          func(e *foundation.EventEnvelope) { e.Payload["status_after"] = "resolved" },
		"half_transition":        func(e *foundation.EventEnvelope) { delete(e.Payload, "status_after") },
		"missing_changed_status": func(e *foundation.EventEnvelope) { e.ChangedFields = []string{"message_status"} },
		"recursive_attempt": func(e *foundation.EventEnvelope) {
			e.Payload["origin_execution_id"], e.Payload["automation_depth"] = uint(7), uint(1)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			copy := retryAutomationFact(101, "resolved", true)
			mutate(&copy)
			_, err := automationFactEventNames(copy)
			require.Error(t, err)
		})
	}
	for _, status := range []string{"open", "pending", "resolved", "snoozed"} {
		for _, messageType := range []string{"incoming", "outgoing", "activity"} {
			for _, private := range []bool{false, true} {
				copy := retryAutomationFact(101, status, false)
				copy.Payload["message_type"], copy.Payload["private"] = messageType, private
				if !private && messageType == "outgoing" {
					copy.Payload["local_outcome"], copy.Payload["status"] = "queued", "queued"
				}
				names, err := automationFactEventNames(copy)
				require.NoError(t, err)
				require.Equal(t, map[string]bool{"message_created": true}, names, "touch-only retry must not create conversation aliases")
			}
		}
	}
}

func retryAutomationMessage(t *testing.T, f *automationActionFixture) domain.Message {
	t.Helper()
	message := domain.Message{AccountID: 91, ConversationID: f.conversation.ID, SenderType: domain.SenderTypeContact, SenderID: f.conversation.ContactID, MessageType: domain.MessageTypeIncoming, Content: "retry context", ContentType: domain.ContentTypeText, Status: domain.MessageStatusSaved}
	require.NoError(t, f.owners["MSG"].DB.Create(&message).Error)
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"status": "open", "object_version": 2}).Error)
	return message
}

func TestAUTExplicitRetryPreservesMessageObservationAndPostReopenAliases(t *testing.T) {
	f := newAutomationActionFixture(t, false, "[]")
	message := retryAutomationMessage(t, f)
	first := triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", `[{"attribute_key":"status","filter_operator":"equal_to","values":["resolved"]},{"attribute_key":"content","filter_operator":"contains","values":["retry context"]},{"attribute_key":"message_type","filter_operator":"equal_to","values":["incoming"]},{"attribute_key":"private_note","filter_operator":"equal_to","values":[false]}]`, `[{"action_name":"change_priority","action_params":"high"}]`)
	opened := triggerAliasRule(t, f.autRunnerFixture, 91, "conversation_opened", `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]},{"attribute_key":"content","filter_operator":"is_not_present"}]`, siblingRecoveryUrgent)
	second := triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", `[{"attribute_key":"status","filter_operator":"equal_to","values":["resolved"]},{"attribute_key":"priority","filter_operator":"equal_to","values":["urgent"]}]`, `[{"action_name":"change_priority","action_params":"low"}]`)
	updated := triggerAliasRule(t, f.autRunnerFixture, 91, "conversation_updated", `[{"attribute_key":"content","filter_operator":"is_present"}]`, siblingRecoveryUrgent)
	event := retryAutomationFact(message.ID, "resolved", true)
	event.Payload["conversation_id"] = f.conversation.ID
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	rows := triggerAliasExecutions(t, f.autRunnerFixture, event.EventID)
	require.Len(t, rows, 4)
	require.Equal(t, []uint{first.ID, opened.ID, second.ID, updated.ID}, []uint{rows[0].RuleID, rows[1].RuleID, rows[2].RuleID, rows[3].RuleID})
	require.Equal(t, "received", rows[0].Status, "first message condition must see pre-reopen resolved")
	for index, status := range map[int]string{1: "open", 2: "resolved", 3: "open"} {
		c := siblingRecoveryDecision(t, rows[index], "pending")
		require.Equal(t, status, c.Initial.Conversation.Status)
		require.Equal(t, 2, c.Initial.Conversation.ObjectVersion)
		if index == 2 {
			require.NotNil(t, c.Initial.Message)
		} else {
			require.Nil(t, c.Initial.Message)
		}
	}
	f.run()
	f.assertTarget("open", "high", 3)
	restored := restoreAUTOwnerFixture(t, f.autRunnerFixture, false)
	// MSG is deliberately not connected after restore. Sibling observations use
	// their frozen message content and status profile, not new cross-owner reads.
	for _, row := range rows {
		assertAUTSnapshotEqual(t, f.execution(row.ID), restored.execution(row.ID))
	}
	for i := 0; i < 5; i++ {
		restored.run()
	}
	restored.assertTarget("open", "low", 5)
	triggerAliasResult(t, restored.execution(rows[0].ID), "succeeded", 3)
	triggerAliasResult(t, restored.execution(rows[1].ID), "succeeded", 4)
	triggerAliasResult(t, restored.execution(rows[2].ID), "succeeded", 5)
	triggerAliasResult(t, restored.execution(rows[3].ID), "no_match", 0)
	require.NoError(t, validateRestoredAutomationSnapshot(restored.aut.DB))
	require.NoError(t, restored.aut.AcceptAutomationFact(context.Background(), event))
	require.Len(t, triggerAliasExecutions(t, restored, event.EventID), 4)
	siblingRecoveryIdle(t, restored)
	// A new payload on the same identity is not a harmless intake replay.
	event.CorrelationID = "altered-correlation"
	require.ErrorIs(t, restored.aut.AcceptAutomationFact(context.Background(), event), errAutomationCascadeUnverified)
}

func TestAUTExplicitRetryDeferredStatusTamperingFailsRestore(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(strconv.FormatBool(alias), func(t *testing.T) {
			f := newAutomationActionFixture(t, false, "[]")
			message := retryAutomationMessage(t, f)
			triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", "[]", siblingRecoveryUrgent)
			eventName, condition, forged := "message_created", siblingRecoveryResolved, "open"
			if alias {
				eventName, condition, forged = "conversation_opened", siblingRecoveryOpen, "resolved"
			}
			triggerAliasRule(t, f.autRunnerFixture, 91, eventName, condition, siblingRecoveryUrgent)
			event := retryAutomationFact(message.ID, "resolved", true)
			event.Payload["conversation_id"] = f.conversation.ID
			require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
			rows := triggerAliasExecutions(t, f.autRunnerFixture, event.EventID)
			require.Len(t, rows, 2)
			checkpoint := siblingRecoveryDecision(t, rows[1], "pending")
			checkpoint.Initial.Conversation.Status = forged
			raw, err := json.Marshal(checkpoint)
			require.NoError(t, err)
			require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[1].ID).Update("runner_evaluation", string(raw)).Error)
			path := filepath.Join(t.TempDir(), "tampered-retry.json")
			require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "retry-tamper", path))
			target := newModuleTestDB(t, "AUT")
			require.Error(t, RestoreSnapshot(context.Background(), target, "AUT", "retry-tamper", path, nil))
			var count int64
			require.NoError(t, target.Model(&domain.AutomationRuleExecution{}).Count(&count).Error)
			require.Zero(t, count, "invalid retry observation restore must be atomic")
		})
	}
}

func TestAUTExplicitRetryStaleTargetCannotDispatch(t *testing.T) {
	f := newAutomationActionFixture(t, false, "[]")
	message := retryAutomationMessage(t, f)
	triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", siblingRecoveryResolved, siblingRecoveryUrgent)
	event := retryAutomationFact(message.ID, "resolved", false)
	event.Payload["conversation_id"] = f.conversation.ID
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"status": "snoozed", "object_version": 3}).Error)
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	rows := triggerAliasExecutions(t, f.autRunnerFixture, event.EventID)
	require.Len(t, rows, 1)
	require.Equal(t, "failed", rows[0].Status)
	require.Equal(t, "target_version_stale", rows[0].Error)
	f.assertTarget("snoozed", "medium", 3)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTExplicitRetryStoredOwnerSourceBridge(t *testing.T) {
	for _, tc := range []struct {
		name, messageType, outcome string
		private, reopen            bool
	}{
		{"public_outgoing", domain.MessageTypeOutgoing, domain.MessageStatusQueued, false, false},
		{"private_outgoing", domain.MessageTypeOutgoing, domain.MessageStatusSaved, true, false},
		{"incoming", domain.MessageTypeIncoming, domain.MessageStatusSaved, false, true},
		{"private_incoming", domain.MessageTypeIncoming, domain.MessageStatusSaved, true, true},
		{"activity", domain.MessageTypeActivity, domain.MessageStatusSaved, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, "[]")
			require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Updates(map[string]any{"status": "resolved", "muted": true}).Error)
			conversations := repository.NewConversationRepositoryWithReadPorts(f.con, nil, nil, nil, nil, nil, nil, nil)
			conversations.SetAutomationCapture(true)
			messages := repository.NewMessageRepositoryWithConversationPort(f.owners["MSG"].DB, conversations)
			messages.SetAutomationCapture(true)
			message := domain.Message{AccountID: 91, ConversationID: f.conversation.ID, SenderType: domain.SenderTypeContact, SenderID: f.conversation.ContactID, MessageType: tc.messageType, Private: tc.private, Content: "same accepted retry", ContentType: domain.ContentTypeText, Status: domain.MessageStatusFailed}
			require.NoError(t, f.owners["MSG"].DB.Create(&message).Error)
			messageRule := triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", siblingRecoveryResolved, `[{"action_name":"change_priority","action_params":"high"}]`)
			openedRule := triggerAliasRule(t, f.autRunnerFixture, 91, "conversation_opened", siblingRecoveryOpen, "[]")
			updatedRule := triggerAliasRule(t, f.autRunnerFixture, 91, "conversation_updated", siblingRecoveryOpen, "[]")
			accepted, isNew, err := messages.RetryMessageAccepted(context.Background(), 91, message.ConversationID, message.ID)
			require.NoError(t, err)
			require.True(t, isNew)
			require.Equal(t, message.ID, accepted.ID)
			require.Equal(t, message.Content, accepted.Content)
			require.True(t, message.CreatedAt.Equal(accepted.CreatedAt))
			require.Equal(t, tc.outcome, accepted.Status)
			fact := triggerAliasFact(t, messages)
			require.Equal(t, tc.outcome, fact.Payload["local_outcome"])
			require.Equal(t, tc.outcome, fact.Payload["status"])
			require.Equal(t, true, fact.Payload["retry_receipt_verified"])
			require.Equal(t, "resolved", fact.Payload["retry_observed_status"])
			require.NoError(t, validateAutomationRetryFact(fact))
			assertRetryAutomationNoDownstreamDelivery(t, f, fact)
			bridge := AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{messages}}
			_, err = bridge.RunOnce(context.Background())
			require.NoError(t, err)
			rows := triggerAliasExecutions(t, f.autRunnerFixture, fact.EventID)
			want, status := 1, "resolved"
			if tc.reopen {
				want, status = 3, "open"
			}
			require.Len(t, rows, want)
			require.Equal(t, messageRule.ID, rows[0].RuleID)
			triggerAliasResult(t, rows[0], "succeeded", 3)
			if tc.reopen {
				require.Equal(t, []uint{openedRule.ID, updatedRule.ID}, []uint{rows[1].RuleID, rows[2].RuleID})
				require.Equal(t, "succeeded", rows[1].Status)
				require.Equal(t, "succeeded", rows[2].Status)
			}
			f.assertTarget(status, "high", 3)
			var conversation domain.Conversation
			require.NoError(t, f.con.First(&conversation, message.ConversationID).Error)
			require.Zero(t, conversation.UnreadCount)
			require.True(t, conversation.Muted, "retry preserves its existing mute-independent reopen policy")
			var ordinaryConversationFacts int64
			require.NoError(t, f.con.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ?", "CON", "automation_trigger").Count(&ordinaryConversationFacts).Error)
			require.Zero(t, ordinaryConversationFacts, "retry receipt must not create a duplicate CON trigger batch")
			require.NoError(t, messages.MarkAutomationFactDelivered(context.Background(), fact.EventID))
			_, isNew, err = messages.RetryMessageAccepted(context.Background(), 91, message.ConversationID, message.ID)
			require.NoError(t, err)
			require.False(t, isNew)
			_, err = bridge.RunOnce(context.Background())
			require.NoError(t, err)
			require.Len(t, triggerAliasExecutions(t, f.autRunnerFixture, fact.EventID), want)
			f.assertTarget(status, "high", 3)
			require.NoError(t, validateRestoredAutomationSnapshot(f.aut.DB))
			// A later genuine failure is a new attempt on the same existing row.
			require.NoError(t, f.owners["MSG"].DB.Model(&domain.Message{}).Where("id = ?", message.ID).Update("status", domain.MessageStatusFailed).Error)
			_, isNew, err = messages.RetryMessageAccepted(context.Background(), 91, message.ConversationID, message.ID)
			require.NoError(t, err)
			require.True(t, isNew)
			later := triggerAliasFact(t, messages)
			require.NotEqual(t, fact.EventID, later.EventID)
			require.EqualValues(t, 2, later.AggregateVersion)
			require.Equal(t, fact.AggregateID, later.AggregateID)
			require.NotContains(t, later.Payload, "status_before", "already-open incoming retry must not reopen again")
			_, err = bridge.RunOnce(context.Background())
			require.NoError(t, err)
			require.Len(t, triggerAliasExecutions(t, f.autRunnerFixture, later.EventID), 1)
			var count int64
			require.NoError(t, f.owners["MSG"].DB.Model(&domain.Message{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

type retryAutomationNoTransport struct{ calls int }

func (transport *retryAutomationNoTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, errors.New("unexpected retry transport attempt")
}

func assertRetryAutomationNoDownstreamDelivery(t *testing.T, f *automationActionFixture, fact foundation.EventEnvelope) {
	t.Helper()
	transport := &retryAutomationNoTransport{}
	dispatcher := NewEventDispatcher(f.owners["MSG"].DB, "MSG", DependencyConfig{URL: "http://no-network.invalid", Token: "local-only"}, "v1", time.Second)
	dispatcher.client.Transport = transport
	delivered, err := dispatcher.DeliverPending(context.Background(), 100)
	require.NoError(t, err)
	require.Zero(t, delivered, "private retry facts are not downstream event_outbox work")
	require.Zero(t, transport.calls, "accepted local retry must not fabricate a provider/event send")
	con := f.owners["CON"]
	require.Nil(t, con.Contracts.events[fact.EventType])
	before := f.conversationState()
	response := deliverEvent(con.Handler(), fact, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+con.Config.AuthToken)
		request.Header.Set(headerCaller, "MSG")
	})
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "event capability is not registered")
	require.Equal(t, before, f.conversationState(), "a status event must not reapply message creation/activity")
	require.Zero(t, failureRecoveryCount(t, f.con, &domain.LocalChangeJournal{}, "action = ?", "event_consumed"))
}

type retryAutomationInterruptedActivity struct {
	*repository.ConversationRepository
	afterCommit bool
}

func (port *retryAutomationInterruptedActivity) ApplyRetryActivity(ctx context.Context, intent repository.RetryActivityIntent) (repository.RetryActivityReceipt, error) {
	if port.afterCommit {
		if _, err := port.ConversationRepository.ApplyRetryActivity(ctx, intent); err != nil {
			return repository.RetryActivityReceipt{}, err
		}
	}
	return repository.RetryActivityReceipt{}, errors.New("scripted retry activity interruption")
}

func TestAUTExplicitRetryOwnerSnapshotsResumePendingAttemptIdentity(t *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		t.Run("after_CON_commit="+strconv.FormatBool(afterCommit), func(t *testing.T) {
			f := newAutomationActionFixture(t, false, "[]")
			require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", "resolved").Error)
			conversations := repository.NewConversationRepositoryWithReadPorts(f.con, nil, nil, nil, nil, nil, nil, nil)
			port := &retryAutomationInterruptedActivity{ConversationRepository: conversations, afterCommit: afterCommit}
			messages := repository.NewMessageRepositoryWithConversationPort(f.owners["MSG"].DB, port)
			messages.SetAutomationCapture(true)
			message := domain.Message{AccountID: 91, ConversationID: f.conversation.ID, SenderType: domain.SenderTypeContact, SenderID: f.conversation.ContactID, MessageType: domain.MessageTypeIncoming, Content: "snapshot existing retry", ContentType: domain.ContentTypeText, Status: domain.MessageStatusFailed}
			require.NoError(t, f.owners["MSG"].DB.Create(&message).Error)
			triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", "[]", "[]")
			accepted, isNew, err := messages.RetryMessageAccepted(context.Background(), 91, message.ConversationID, message.ID)
			require.NoError(t, err)
			require.True(t, isNew)
			require.Equal(t, domain.MessageStatusSaved, accepted.Status)
			var beforeAttempt, beforeFact domain.LocalChangeJournal
			require.NoError(t, f.owners["MSG"].DB.Where("action = ?", "retry_attempt").First(&beforeAttempt).Error)
			require.NoError(t, f.owners["MSG"].DB.Where("action = ?", "automation_trigger").First(&beforeFact).Error)
			require.Equal(t, "pending", beforeAttempt.Status)
			if afterCommit {
				f.assertTarget("open", "medium", 2)
			} else {
				f.assertTarget("resolved", "medium", 1)
			}
			// Round-trip all owner snapshots, including MSG's pending attempt/fact
			// and CON's committed receipt in the lost-response case.
			restored := failureRecoveryRestore(t, f)
			restoredConversations := repository.NewConversationRepositoryWithReadPorts(restored.con, nil, nil, nil, nil, nil, nil, nil)
			restoredMessages := repository.NewMessageRepositoryWithConversationPort(restored.owners["MSG"].DB, restoredConversations)
			restoredMessages.SetAutomationCapture(true)
			var restoredAttempt domain.LocalChangeJournal
			require.NoError(t, restored.owners["MSG"].DB.Where("change_id = ?", beforeAttempt.ChangeID).First(&restoredAttempt).Error)
			require.Equal(t, beforeAttempt.AfterState, restoredAttempt.AfterState)
			require.Equal(t, "pending", restoredAttempt.Status)
			fact := triggerAliasFact(t, restoredMessages)
			require.Equal(t, beforeFact.EventID, fact.EventID)
			require.Equal(t, beforeAttempt.ChangeID, fact.CausationID)
			require.True(t, beforeAttempt.OccurredAt.Equal(fact.OccurredAt))
			require.EqualValues(t, 1, fact.AggregateVersion)
			restored.assertTarget("open", "medium", 2)
			require.EqualValues(t, 1, failureRecoveryCount(t, restored.con, &domain.LocalChangeJournal{}, "action = ?", "retry_activity"))
			assertRetryAutomationNoDownstreamDelivery(t, restored, fact)
			bridge := AutomationBridge{Runtime: &restored.aut, Sources: []AutomationFactSource{restoredMessages}}
			_, err = bridge.RunOnce(context.Background())
			require.NoError(t, err)
			require.Len(t, triggerAliasExecutions(t, restored.autRunnerFixture, fact.EventID), 1)
			_, isNew, err = restoredMessages.RetryMessageAccepted(context.Background(), 91, message.ConversationID, message.ID)
			require.NoError(t, err)
			require.False(t, isNew)
			// Sequence allocation survives the real MSG snapshot and advances only
			// when that same message genuinely becomes failed again.
			require.NoError(t, restored.owners["MSG"].DB.Model(&domain.Message{}).Where("id = ?", message.ID).Update("status", domain.MessageStatusFailed).Error)
			_, isNew, err = restoredMessages.RetryMessageAccepted(context.Background(), 91, message.ConversationID, message.ID)
			require.NoError(t, err)
			require.True(t, isNew)
			later := triggerAliasFact(t, restoredMessages)
			require.NotEqual(t, fact.EventID, later.EventID)
			require.NotEqual(t, fact.CausationID, later.CausationID)
			require.EqualValues(t, 2, later.AggregateVersion)
			require.Equal(t, fact.AggregateID, later.AggregateID)
			_, err = bridge.RunOnce(context.Background())
			require.NoError(t, err)
			require.Len(t, triggerAliasExecutions(t, restored.autRunnerFixture, later.EventID), 1)
			restored.assertTarget("open", "medium", 3)
			require.EqualValues(t, 2, failureRecoveryCount(t, restored.con, &domain.LocalChangeJournal{}, "action = ?", "retry_activity"))
			require.EqualValues(t, 1, failureRecoveryCount(t, restored.owners["MSG"].DB, &domain.Message{}, ""))
		})
	}
}

func TestAUTExplicitRetryStaleReceiptCannotBecomeApplicableAfterRestore(t *testing.T) {
	f := newAutomationActionFixture(t, false, "[]")
	message := retryAutomationMessage(t, f)
	triggerAliasRule(t, f.autRunnerFixture, 91, "message_created", "[]", siblingRecoveryUrgent)
	event := retryAutomationFact(message.ID, "open", false)
	event.Payload["conversation_id"] = f.conversation.ID
	event.Payload["retry_activity_result"], event.Payload["retry_expected_version"] = "stale", 2
	// The independently restored CON happens to match the original failed target.
	// That does not turn a receipt-proven stale attempt into an applied activity.
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	rows := triggerAliasExecutions(t, f.autRunnerFixture, event.EventID)
	require.Len(t, rows, 1)
	require.Equal(t, "failed", rows[0].Status)
	require.Equal(t, "target_version_stale", rows[0].Error)
	f.assertTarget("open", "medium", 2)
	siblingRecoveryIdle(t, f.autRunnerFixture)
	require.NoError(t, validateRestoredAutomationSnapshot(f.aut.DB))
	// Even a well-formed terminal snapshot cannot turn that failed owner activity
	// into an applicable or successful retry compatibility execution.
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[0].ID).Update("status", "succeeded").Error)
	require.Error(t, validateRestoredAutomationSnapshot(f.aut.DB))
}
