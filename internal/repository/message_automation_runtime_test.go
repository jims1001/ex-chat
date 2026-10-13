package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Match the MSG runtime's owner journal without importing modruntime (which
// depends on repository). The bridge integration tests also exercise its actual
// command producer. This fixture deliberately uses an isolated MSG database.
func automationRuntimeMessageFact(t *testing.T, db *gorm.DB, conversation domain.Conversation, private bool) (domain.LocalChangeJournal, domain.LocalChangeJournal, foundation.EventEnvelope) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Second)
	status := domain.MessageStatusQueued
	if private {
		status = domain.MessageStatusSaved
	}
	message := domain.Message{AccountID: conversation.AccountID, ConversationID: conversation.ID, SenderType: domain.SenderTypeUser, MessageType: domain.MessageTypeOutgoing, ContentType: domain.ContentTypeText, Content: "immutable private owner body", Private: private, Status: status, CreatedAt: now, UpdatedAt: now}
	require.NoError(t, db.Create(&message).Error)
	prepareSum := sha256.Sum256([]byte(fmt.Sprintf("prepare:%d", message.ID)))
	prepare := hex.EncodeToString(prepareSum[:])
	contentSum := sha256.Sum256([]byte(message.Content))
	contentHash := hex.EncodeToString(contentSum[:])
	commandID := fmt.Sprintf("create-message:%d", message.ID)
	event := foundation.EventEnvelope{
		EventID: foundation.GenerateUUID(), EventType: "msg.message.created.v1", EventVersion: "v1", SourceModule: "MSG", AccountID: message.AccountID,
		AggregateType: "Message", AggregateID: strconv.FormatUint(uint64(message.ID), 10), AggregateVersion: 1, OccurredAt: now, PublishedAt: now,
		Actor: foundation.ContractActor{Type: "System", ID: "automation:7", AuthSource: "module"}, CorrelationID: "root-fact", CausationID: commandID,
		ChangedFields: []string{"created"}, DataClassification: "internal",
		Payload: map[string]any{"message_id": message.ID, "conversation_id": message.ConversationID, "object_version": 8, "sender_type": message.SenderType, "sender_id": message.SenderID, "message_type": message.MessageType, "content_type": message.ContentType, "private": message.Private, "status": message.Status, "local_outcome": status, "origin_execution_id": 17, "automation_depth": 1, "conversation_activity_prepared": prepare, "content_hash": contentHash},
	}
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	outbox := domain.LocalChangeJournal{ChangeID: event.EventID, SourceModule: "MSG", AccountID: message.AccountID, ObjectType: "Message", ObjectDisplayID: event.AggregateID, ObjectVersionAfter: 1, Action: "event_outbox", AfterState: string(raw), ActorType: "System", EventID: event.EventID, CorrelationID: event.CorrelationID, CausationID: event.CausationID, OccurredAt: now, CreatedAt: now, Status: "pending", Result: "pending"}
	require.NoError(t, db.Create(&outbox).Error)
	grant := map[string]any{"prepare_command_id": prepare, "account_id": message.AccountID, "conversation_id": message.ConversationID, "object_version": 8, "content_hash": contentHash, "private": private, "origin_execution_id": 17, "automation_depth": 1, "actor_id": event.Actor.ID}
	raw, err = json.Marshal(map[string]any{"grant": grant, "message": message})
	require.NoError(t, err)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", message.AccountID, prepare)))
	consumed := domain.LocalChangeJournal{ChangeID: "msg-grant-" + hex.EncodeToString(sum[:])[:48], SourceModule: "MSG", AccountID: message.AccountID, ObjectType: "Message", ObjectID: message.ID, Action: "automation_message_consumed", AfterState: string(raw), CommandID: commandID, CorrelationID: event.CorrelationID, OccurredAt: now, CreatedAt: now, Status: "published", Result: "applied"}
	require.NoError(t, db.Create(&consumed).Error)
	// Compare through the same JSON representation returned by the source.
	require.NoError(t, json.Unmarshal([]byte(outbox.AfterState), &event))
	return outbox, consumed, event
}

func automationRuntimeReadJournal(t *testing.T, db *gorm.DB, id uint) domain.LocalChangeJournal {
	t.Helper()
	var result domain.LocalChangeJournal
	require.NoError(t, db.First(&result, id).Error)
	return result
}

func TestMessageAutomationRuntimeFactsPreserveOriginalOwnerEnvelope(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(fmt.Sprintf("private=%t", private), func(t *testing.T) {
			_, _, _, db, conversation := automationMessageFixture(t)
			outbox, _, want := automationRuntimeMessageFact(t, db, conversation, private)
			// There is deliberately no CON port or CON activity delivery receipt.
			repo := NewMessageRepositoryWithConversationPort(db, nil)
			repo.SetAutomationCapture(true)
			facts, err := repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, []foundation.EventEnvelope{want}, facts)
			require.NotContains(t, facts[0].Payload, "content")
			require.Equal(t, "outgoing", facts[0].Payload["message_type"])
			require.Equal(t, private, facts[0].Payload["private"])
			require.EqualValues(t, 8, facts[0].Payload["object_version"])
			require.Equal(t, outbox, automationRuntimeReadJournal(t, db, outbox.ID))
			// A later local edit is not a rewrite of the message's creation fact.
			require.NoError(t, db.Model(&domain.Message{}).Where("id = ?", outbox.ObjectDisplayID).Updates(map[string]any{"content": "edited later", "status": domain.MessageStatusSent}).Error)
			facts, err = repo.PendingAutomationFacts(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, []foundation.EventEnvelope{want}, facts)
			for i := 0; i < 2; i++ {
				require.NoError(t, repo.MarkAutomationFactDelivered(context.Background(), want.EventID))
			}
			require.Equal(t, outbox, automationRuntimeReadJournal(t, db, outbox.ID), "AUT must not steal external event delivery")
			var receiptCount int64
			require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ? AND event_id = ?", "MSG", "automation_delivery", want.EventID).Count(&receiptCount).Error)
			require.EqualValues(t, 1, receiptCount)
			restarted := NewMessageRepositoryWithConversationPort(db, nil)
			restarted.SetAutomationCapture(true)
			facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Empty(t, facts)
		})
	}
}

func TestMessageAutomationRuntimeDeliveredOutboxStillReachesAUT(t *testing.T) {
	repo, _, _, db, c := automationMessageFixture(t)
	outbox, _, event := automationRuntimeMessageFact(t, db, c, false)
	require.NoError(t, db.Model(&outbox).Updates(map[string]any{"status": "delivered", "result": "applied"}).Error)
	before := automationRuntimeReadJournal(t, db, outbox.ID)
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, []foundation.EventEnvelope{event}, facts)
	require.NoError(t, repo.MarkAutomationFactDelivered(context.Background(), event.EventID))
	require.Equal(t, before, automationRuntimeReadJournal(t, db, outbox.ID))
}

func TestMessageAutomationRuntimeQuarantinePreservesOutboxAndContinues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*foundation.EventEnvelope, *domain.LocalChangeJournal, *domain.LocalChangeJournal)
	}{
		{"source", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.SourceModule = "CON" }},
		{"account", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.AccountID++ }},
		{"event_identity", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.EventID = "different" }},
		{"aggregate_identity", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.AggregateID = "999" }},
		{"aggregate_version", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.AggregateVersion++ }},
		{"causation", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.CausationID = "wrong-command" }},
		{"receipt_causation", func(_ *foundation.EventEnvelope, _, c *domain.LocalChangeJournal) { c.CommandID = "wrong-command" }},
		{"receipt_account", func(_ *foundation.EventEnvelope, _, c *domain.LocalChangeJournal) { c.AccountID++ }},
		{"receipt_corrupt", func(_ *foundation.EventEnvelope, _, c *domain.LocalChangeJournal) { c.AfterState = "{" }},
		{"receipt_message", func(_ *foundation.EventEnvelope, _, c *domain.LocalChangeJournal) { c.ObjectID++ }},
		{"receipt_uncommitted", func(_ *foundation.EventEnvelope, _, c *domain.LocalChangeJournal) { c.Status = "pending" }},
		{"correlation", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.CorrelationID = "another-root" }},
		{"private_string", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["private"] = "true" }},
		{"private_mismatch", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.Payload["private"] = true
			e.Payload["status"] = "saved"
			e.Payload["local_outcome"] = "saved"
		}},
		{"message_type", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.Payload["message_type"] = "incoming"
		}},
		{"message_id", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["message_id"] = 7.5 }},
		{"conversation", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["conversation_id"] = 99 }},
		{"version", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["object_version"] = 9 }},
		{"version_string", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["object_version"] = "8" }},
		{"origin", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.Payload["origin_execution_id"] = 18
		}},
		{"missing_origin", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			delete(e.Payload, "origin_execution_id")
		}},
		{"depth", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["automation_depth"] = 4 }},
		{"zero_depth", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["automation_depth"] = 0 }},
		{"prepare", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.Payload["conversation_activity_prepared"] = "wrong"
		}},
		{"content_hash", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.Payload["content_hash"] = "wrong"
		}},
		{"content_leak", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.Payload["content"] = "private body"
		}},
		{"forged_status_before", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.Payload["status_before"] = "resolved"
		}},
		{"forged_status_after", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["status_after"] = "open" }},
		{"actor", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Actor.ID = "automation:8" }},
		{"sender", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["sender_id"] = 9 }},
		{"false_delivery", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) { e.Payload["status"] = "sent" }},
		{"changed_fields", func(e *foundation.EventEnvelope, _, _ *domain.LocalChangeJournal) {
			e.ChangedFields = append(e.ChangedFields, "status")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _, _, db, conversation := automationMessageFixture(t)
			bad, consumed, event := automationRuntimeMessageFact(t, db, conversation, false)
			tc.mutate(&event, &bad, &consumed)
			raw, err := json.Marshal(event)
			require.NoError(t, err)
			bad.AfterState = string(raw)
			require.NoError(t, db.Save(&bad).Error)
			require.NoError(t, db.Save(&consumed).Error)
			before := automationRuntimeReadJournal(t, db, bad.ID)
			_, _, good := automationRuntimeMessageFact(t, db, conversation, true)
			facts, err := repo.PendingAutomationFacts(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, []foundation.EventEnvelope{good}, facts)
			require.Equal(t, before, automationRuntimeReadJournal(t, db, bad.ID))
			var quarantine domain.LocalChangeJournal
			require.NoError(t, db.Where("source_module = ? AND action = ? AND entity_type = ? AND entity_id = ? AND command_id = ?", "MSG", "automation_quarantine", "LocalChangeJournal", bad.ID, bad.ChangeID).First(&quarantine).Error)
			require.Equal(t, "manual_action_required", quarantine.Status)
			facts, err = repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, []foundation.EventEnvelope{good}, facts)
		})
	}
}

func TestMessageAutomationRuntimeFutureUnrelatedAndDeletedFacts(t *testing.T) {
	repo, _, _, db, conversation := automationMessageFixture(t)
	future, _, futureEvent := automationRuntimeMessageFact(t, db, conversation, false)
	original := future.AfterState
	futureEvent.OccurredAt = time.Now().Add(time.Hour).UTC()
	futureEvent.PublishedAt = futureEvent.OccurredAt
	raw, err := json.Marshal(futureEvent)
	require.NoError(t, err)
	require.NoError(t, db.Model(&future).Update("after_state", string(raw)).Error)
	unrelated, _, unrelatedEvent := automationRuntimeMessageFact(t, db, conversation, false)
	unrelatedEvent.EventType = "msg.message.updated.v1"
	raw, err = json.Marshal(unrelatedEvent)
	require.NoError(t, err)
	require.NoError(t, db.Model(&unrelated).Update("after_state", string(raw)).Error)
	ordinaryRuntime, _, ordinaryEvent := automationRuntimeMessageFact(t, db, conversation, false)
	for _, key := range []string{"origin_execution_id", "automation_depth", "conversation_activity_prepared"} {
		delete(ordinaryEvent.Payload, key)
	}
	ordinaryEvent.Actor.ID = "module"
	raw, err = json.Marshal(ordinaryEvent)
	require.NoError(t, err)
	require.NoError(t, db.Model(&ordinaryRuntime).Update("after_state", string(raw)).Error)
	superseded, _, _ := automationRuntimeMessageFact(t, db, conversation, false)
	require.NoError(t, db.Model(&superseded).Updates(map[string]any{"status": "superseded", "result": "rejected"}).Error)
	deleted, _, _ := automationRuntimeMessageFact(t, db, conversation, true)
	require.NoError(t, db.Model(&domain.Message{}).Where("id = ?", deleted.ObjectDisplayID).Update("deleted", true).Error)
	hardDeleted, _, _ := automationRuntimeMessageFact(t, db, conversation, false)
	require.NoError(t, db.Where("id = ?", hardDeleted.ObjectDisplayID).Delete(&domain.Message{}).Error)
	_, _, good := automationRuntimeMessageFact(t, db, conversation, false)
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, []foundation.EventEnvelope{good}, facts)
	for _, row := range []domain.LocalChangeJournal{future, unrelated, ordinaryRuntime} {
		require.Equal(t, "pending", automationRuntimeReadJournal(t, db, row.ID).Status)
		var count int64
		require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND entity_id = ?", "automation_quarantine", row.ID).Count(&count).Error)
		require.Zero(t, count)
	}
	require.Error(t, repo.MarkAutomationFactDelivered(context.Background(), unrelated.EventID))
	require.Error(t, repo.MarkAutomationFactDelivered(context.Background(), ordinaryRuntime.EventID))
	require.NoError(t, db.Model(&future).Update("after_state", original).Error)
	facts, err = repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 2)
	require.Equal(t, future.EventID, facts[0].EventID)
}

func TestMessageAutomationRuntimeCannotStarveOrdinaryMessage(t *testing.T) {
	repo, _, _, db, conversation := automationMessageFixture(t)
	// Fill a whole runtime page with unacknowledged action descendants. The
	// source cannot query AUT readiness, but an ordinary fact must still advance.
	for i := 0; i < 101; i++ {
		automationRuntimeMessageFact(t, db, conversation, false)
	}
	ordinary := automationTestMessage(t, repo, conversation)
	facts, err := repo.PendingAutomationFacts(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	messageID, err := messageAutomationUint(facts[0].Payload["message_id"])
	require.NoError(t, err)
	require.Equal(t, ordinary.ID, messageID)
	require.Equal(t, domain.MessageTypeIncoming, facts[0].Payload["message_type"])
	require.Equal(t, false, facts[0].Payload["private"])
}

func TestMessageAutomationOrdinaryTypedMetadataCannotBeForged(t *testing.T) {
	for _, key := range []string{"message_type", "private", "sender_id", "status_before", "status_after", "origin_execution_id", "automation_depth", "conversation_activity_prepared"} {
		t.Run(key, func(t *testing.T) {
			repo, _, _, db, c := automationMessageFixture(t)
			bad := automationTestMessage(t, repo, c)
			var row domain.LocalChangeJournal
			require.NoError(t, db.Where("action = ? AND object_id = ?", "automation_trigger", bad.ID).First(&row).Error)
			var event foundation.EventEnvelope
			require.NoError(t, json.Unmarshal([]byte(row.AfterState), &event))
			event.Payload[key] = "forged"
			raw, err := json.Marshal(event)
			require.NoError(t, err)
			require.NoError(t, db.Model(&row).Update("after_state", string(raw)).Error)
			facts, err := repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Empty(t, facts)
			require.Equal(t, "manual_action_required", automationRuntimeReadJournal(t, db, row.ID).Status)
		})
	}
}

func TestMessageAutomationOrdinaryReopenTransitionUsesReceipt(t *testing.T) {
	repo, port, con, db, conversation := reopenWorkflowFixture(t)
	// Use the actual owner directly so its optional receipt-transition port is
	// available, rather than the old compatibility wrapper used by prior tests.
	repo.conversation = port.owner
	message := reopenWorkflowMessage(conversation)
	require.NoError(t, repo.CreateWithConversationReopen(&message, conversation.ObjectVersion, true))
	var source domain.LocalChangeJournal
	require.NoError(t, db.Where("action = ? AND object_id = ?", "automation_trigger", message.ID).First(&source).Error)
	require.NoError(t, port.owner.UpdateStatus(conversation.AccountID, conversation.ID, domain.ConversationStatusResolved, nil))
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, domain.ConversationStatusResolved, facts[0].Payload["status_before"])
	require.Equal(t, domain.ConversationStatusOpen, facts[0].Payload["status_after"])
	require.Contains(t, facts[0].ChangedFields, "status")
	require.Equal(t, source, automationRuntimeReadJournal(t, db, source.ID))
	require.EqualValues(t, conversation.ObjectVersion+1, facts[0].Payload["object_version"])
	var current domain.Conversation
	require.NoError(t, con.First(&current, conversation.ID).Error)
	require.Equal(t, domain.ConversationStatusResolved, current.Status)
	require.Equal(t, conversation.ObjectVersion+2, current.ObjectVersion)
}

func TestMessageAutomationOrdinaryNoReopenHasNoStatusChange(t *testing.T) {
	repo, port, _, _, conversation := reopenWorkflowFixture(t)
	repo.conversation = port.owner
	message := reopenWorkflowMessage(conversation)
	require.NoError(t, repo.CreateWithConversationReopen(&message, conversation.ObjectVersion, false))
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.NotContains(t, facts[0].Payload, "status_before")
	require.NotContains(t, facts[0].Payload, "status_after")
	require.NotContains(t, facts[0].ChangedFields, "status")
}
