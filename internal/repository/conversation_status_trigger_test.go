package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConversationStatusTriggersCaptureActualTransitionAcrossOrdinaryWriters(t *testing.T) {
	for _, method := range []string{"status", "update", "macro"} {
		t.Run(method, func(t *testing.T) {
			repo := conversationAutomationTestRepository(t, true)
			repo.SetAutomationCapture(true)
			conversation := automationTestConversation(t, repo)
			previous := conversation.Status
			for i, status := range []string{"open", "resolved", "resolved", "pending", "open", "snoozed", "open"} {
				switch method {
				case "status":
					require.NoError(t, repo.UpdateStatus(conversation.AccountID, conversation.ID, status, nil))
				case "update":
					require.NoError(t, repo.db.First(&conversation, conversation.ID).Error)
					conversation.Status = status
					require.NoError(t, repo.Update(&conversation))
				case "macro":
					require.NoError(t, repo.ApplyMacroMutation(conversation.AccountID, conversation.ID, MacroConversationMutation{Status: &status}))
				}
				facts, err := repo.PendingAutomationFacts(context.Background(), 100)
				require.NoError(t, err)
				require.Len(t, facts, i+2, "one owner fact, not separate status and updated facts")
				fact := facts[len(facts)-1]
				require.EqualValues(t, i+2, fact.Payload["object_version"])
				require.Equal(t, "conversation_updated", fact.Payload["event_name"])
				if status == previous {
					require.NotContains(t, fact.Payload, "status_before")
					require.NotContains(t, fact.Payload, "status_after")
				} else {
					require.Equal(t, previous, fact.Payload["status_before"])
					require.Equal(t, status, fact.Payload["status_after"])
					require.Contains(t, fact.ChangedFields, "status")
				}
				previous = status
			}
			facts, err := repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, "conversation_created", facts[0].Payload["event_name"])
			require.NotContains(t, facts[0].Payload, "status_before", "open creation is not an opened transition")
		})
	}
}

func TestConversationStatusTriggerRollbackAndReplay(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	conversation := automationTestConversation(t, repo)
	repo.SetAutomationCapture(true)
	require.NoError(t, repo.db.Callback().Create().Before("gorm:create").Register("reject_status_fact", func(tx *gorm.DB) {
		if change, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && change.Action == "automation_trigger" {
			_ = tx.AddError(errors.New("status journal failure"))
		}
	}))
	require.ErrorContains(t, repo.UpdateStatus(conversation.AccountID, conversation.ID, "resolved", nil), "status journal failure")
	var current domain.Conversation
	require.NoError(t, repo.db.First(&current, conversation.ID).Error)
	require.Equal(t, "open", current.Status)
	require.Equal(t, 1, current.ObjectVersion)
	require.NoError(t, repo.db.Callback().Create().Remove("reject_status_fact"))
	require.NoError(t, repo.UpdateStatus(conversation.AccountID, conversation.ID, "resolved", nil))
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	restarted := &ConversationRepository{db: repo.db}
	restarted.SetAutomationCapture(true)
	replayed, err := restarted.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, facts, replayed)
	require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), facts[0].EventID))
	require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), facts[0].EventID))
	facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
}

func TestConversationStatusTriggerQuarantinesMalformedTransition(t *testing.T) {
	for _, defect := range []string{"missing_before", "same_status", "unknown_status", "missing_changed_status", "created"} {
		t.Run(defect, func(t *testing.T) {
			repo := conversationAutomationTestRepository(t, true)
			conversation := automationTestConversation(t, repo)
			repo.SetAutomationCapture(true)
			require.NoError(t, repo.UpdateStatus(conversation.AccountID, conversation.ID, "resolved", nil))
			var row domain.LocalChangeJournal
			require.NoError(t, repo.db.Where("action = ?", "automation_trigger").First(&row).Error)
			var fact foundation.EventEnvelope
			require.NoError(t, json.Unmarshal([]byte(row.AfterState), &fact))
			switch defect {
			case "missing_before":
				delete(fact.Payload, "status_before")
			case "same_status":
				fact.Payload["status_before"] = "resolved"
			case "unknown_status":
				fact.Payload["status_after"] = "deleted"
			case "missing_changed_status":
				fact.ChangedFields = []string{"priority"}
			case "created":
				fact.EventType = "con.conversation.created.v1"
				fact.Payload["event_name"] = "conversation_created"
			}
			raw, err := json.Marshal(fact)
			require.NoError(t, err)
			require.NoError(t, repo.db.Model(&row).Update("after_state", string(raw)).Error)
			require.NoError(t, repo.UpdatePriority(conversation.AccountID, conversation.ID, "high"))
			facts, err := repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Len(t, facts, 1)
			require.EqualValues(t, 3, facts[0].Payload["object_version"])
			var count int64
			require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_quarantine").Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestConversationMessageTransitionComesFromImmutableReceipt(t *testing.T) {
	r, p, con, msgDB, conversation := reopenWorkflowFixture(t)
	msg := reopenWorkflowMessage(conversation)
	require.NoError(t, r.CreateWithConversationReopen(&msg, conversation.ObjectVersion, true))
	create := reopenWorkflowCreate(t, msgDB, msg)
	before, after, err := p.owner.AutomationMessageActivityTransition(conversation.AccountID, conversation.ID, create.ChangeID)
	require.NoError(t, err)
	require.Equal(t, "resolved", before)
	require.Equal(t, "open", after)
	require.NoError(t, p.owner.UpdateStatus(conversation.AccountID, conversation.ID, "resolved", nil))
	before, after, err = p.owner.AutomationMessageActivityTransition(conversation.AccountID, conversation.ID, create.ChangeID)
	require.NoError(t, err)
	require.Equal(t, "resolved", before)
	require.Equal(t, "open", after, "manual resolution cannot replace the captured reopen")
	_, _, err = p.owner.AutomationMessageActivityTransition(conversation.AccountID+1, conversation.ID, create.ChangeID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	receipt := reopenWorkflowReceipt(t, con, conversation, create.ChangeID)
	state := reopenWorkflowJSON(t, receipt.AfterState)
	delete(state, "status_before")
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, con.Model(&receipt).Update("after_state", string(raw)).Error)
	_, _, err = p.owner.AutomationMessageActivityTransition(conversation.AccountID, conversation.ID, create.ChangeID)
	require.ErrorIs(t, err, gorm.ErrInvalidData)
	delete(state, "status_after")
	raw, err = json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, con.Model(&receipt).Update("after_state", string(raw)).Error)
	before, after, err = p.owner.AutomationMessageActivityTransition(conversation.AccountID, conversation.ID, create.ChangeID)
	require.NoError(t, err)
	require.Empty(t, before)
	require.Empty(t, after, "pre-marker historical receipts must not acquire invented aliases")
}

func TestConversationMessageTransitionExcludedWhenReopenDoesNotHappen(t *testing.T) {
	for _, reason := range []string{"muted", "blocked", "stale", "not_resolved", "no_intent"} {
		t.Run(reason, func(t *testing.T) {
			r, p, con, msgDB, conversation := reopenWorkflowFixture(t)
			switch reason {
			case "muted":
				require.NoError(t, con.Model(&conversation).Update("muted", true).Error)
			case "blocked":
				p.owner.contacts.(*reopenWorkflowContactOwner).contact.Blocked = true
			case "stale":
				require.NoError(t, p.owner.UpdatePriority(conversation.AccountID, conversation.ID, "high"))
			case "not_resolved":
				require.NoError(t, con.Model(&conversation).Update("status", "open").Error)
			}
			msg := reopenWorkflowMessage(conversation)
			require.NoError(t, r.CreateWithConversationReopen(&msg, conversation.ObjectVersion, reason != "no_intent"))
			create := reopenWorkflowCreate(t, msgDB, msg)
			before, after, err := p.owner.AutomationMessageActivityTransition(conversation.AccountID, conversation.ID, create.ChangeID)
			require.NoError(t, err)
			require.Empty(t, before)
			require.Empty(t, after)
		})
	}
}
