package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func conversationAutomationTestRepository(t *testing.T, journal bool) *ConversationRepository {
	t.Helper()
	db := openMFATestDB(t, filepath.Join(t.TempDir(), "conversation.sqlite"))
	db.Config.IgnoreRelationshipsWhenMigrating = true
	require.NoError(t, db.AutoMigrate(&domain.Conversation{}))
	if journal {
		require.NoError(t, db.AutoMigrate(&domain.LocalChangeJournal{}))
	}
	return &ConversationRepository{db: db}
}

func automationTestConversation(t *testing.T, repo *ConversationRepository) domain.Conversation {
	t.Helper()
	conversation := domain.Conversation{AccountID: 91, InboxID: 17, ContactID: 19, Status: "open", ObjectVersion: 700}
	require.NoError(t, repo.Create(&conversation))
	require.Equal(t, 1, conversation.ObjectVersion)
	return conversation
}

func TestConversationAutomationCapturesEveryOrdinaryMutation(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	conversation := automationTestConversation(t, repo)
	id, account := conversation.ID, conversation.AccountID
	assignee, team, status, priority := uint(31), uint(41), "pending", "urgent"
	mutations := []func() error{
		func() error { conversation.Muted = true; return repo.Update(&conversation) },
		func() error { return repo.UpdateStatus(account, id, "resolved", nil) },
		func() error { return repo.UpdatePriority(account, id, "high") },
		func() error { return repo.Assign(account, id, &assignee) },
		func() error { return repo.AssignTeam(account, id, &team) },
		func() error { return repo.AssignWithTeam(account, id, true, nil, true, nil) },
		func() error {
			return repo.ApplyMacroMutation(account, id, MacroConversationMutation{Status: &status, Priority: &priority})
		},
	}
	for i, mutate := range mutations {
		require.NoError(t, mutate(), "mutation %d", i)
		var stored domain.Conversation
		require.NoError(t, repo.db.First(&stored, id).Error)
		require.Equal(t, i+2, stored.ObjectVersion)
	}
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, len(mutations)+1)
	for i, fact := range facts {
		require.NoError(t, fact.Validate())
		_, err := uuid.Parse(fact.EventID)
		require.NoError(t, err)
		require.Equal(t, fact.EventID, fact.CorrelationID)
		require.Equal(t, "CON", fact.SourceModule)
		require.Equal(t, "Conversation", fact.AggregateType)
		require.Equal(t, strconv.FormatUint(uint64(id), 10), fact.AggregateID)
		require.EqualValues(t, i+1, fact.AggregateVersion)
		require.EqualValues(t, i+1, fact.Payload["object_version"])
		require.EqualValues(t, id, fact.Payload["conversation_id"])
		if i == 0 {
			require.Equal(t, "con.conversation.created.v1", fact.EventType)
			require.Equal(t, "conversation_created", fact.Payload["event_name"])
		} else {
			require.Equal(t, "con.conversation.changed.v1", fact.EventType)
			require.Equal(t, "conversation_updated", fact.Payload["event_name"])
		}
	}
	first, err := repo.PendingAutomationFacts(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, facts[0].EventID, first[0].EventID)
	require.NoError(t, repo.MarkAutomationFactDelivered(context.Background(), first[0].EventID))
	require.NoError(t, repo.MarkAutomationFactDelivered(context.Background(), first[0].EventID))
	remaining, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, remaining, len(mutations))
	var receipts int64
	require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_delivery").Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
}

func TestConversationAutomationLegacyNeedsNoJournalAndRejectsStaleSave(t *testing.T) {
	repo := conversationAutomationTestRepository(t, false)
	conversation := automationTestConversation(t, repo)
	stale := conversation
	require.NoError(t, repo.UpdateStatus(conversation.AccountID, conversation.ID, "resolved", nil))
	stale.Muted = true
	require.ErrorIs(t, repo.Update(&stale), ErrConversationVersionConflict)
	var stored domain.Conversation
	require.NoError(t, repo.db.First(&stored, conversation.ID).Error)
	require.Equal(t, "resolved", stored.Status)
	require.False(t, stored.Muted)
	require.Equal(t, 2, stored.ObjectVersion)
	stored.Muted = true
	require.NoError(t, repo.Update(&stored))
	require.Equal(t, 3, stored.ObjectVersion)
	for _, mutate := range []func() error{
		func() error { return repo.Assign(stored.AccountID, stored.ID, nil) },
		func() error { return repo.AssignTeam(stored.AccountID, stored.ID, nil) },
		func() error { return repo.AssignWithTeam(stored.AccountID, stored.ID, true, nil, false, nil) },
		func() error { return repo.UpdatePriority(stored.AccountID, stored.ID, "high") },
		func() error { return repo.ApplyMacroMutation(stored.AccountID, stored.ID, MacroConversationMutation{}) },
	} {
		require.NoError(t, mutate())
	}
	require.NoError(t, repo.db.First(&stored, conversation.ID).Error)
	require.Equal(t, 8, stored.ObjectVersion)
	facts, err := repo.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	require.Empty(t, facts)
	require.False(t, repo.db.Migrator().HasTable(&domain.LocalChangeJournal{}))
}

func TestConversationAutomationCaptureFailureRollsBackOwner(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	conversation := automationTestConversation(t, repo)
	repo.SetAutomationCapture(true)
	require.NoError(t, repo.db.Callback().Create().Before("gorm:create").Register("reject_automation_fact", func(tx *gorm.DB) {
		if change, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && change.Action == "automation_trigger" {
			_ = tx.AddError(errors.New("injected journal failure"))
		}
	}))
	conversation.Muted = true
	require.ErrorContains(t, repo.Update(&conversation), "injected journal failure")
	require.Equal(t, 1, conversation.ObjectVersion)
	var stored domain.Conversation
	require.NoError(t, repo.db.First(&stored, conversation.ID).Error)
	require.False(t, stored.Muted)
	require.Equal(t, 1, stored.ObjectVersion)
	newConversation := domain.Conversation{AccountID: 91, InboxID: 17, ContactID: 19}
	require.ErrorContains(t, repo.Create(&newConversation), "injected journal failure")
	require.Zero(t, newConversation.ID)
	var count int64
	require.NoError(t, repo.db.Model(&domain.Conversation{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Count(&count).Error)
	require.Zero(t, count, "the activation marker must roll back too")
}

func TestConversationAutomationTenantBoundaryAndNoResurrection(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	conversation := automationTestConversation(t, repo)
	for _, mutate := range []func() error{
		func() error { return repo.UpdateStatus(92, conversation.ID, "resolved", nil) },
		func() error { return repo.UpdatePriority(92, conversation.ID, "urgent") },
		func() error { return repo.Assign(92, conversation.ID, nil) },
		func() error { return repo.AssignTeam(92, conversation.ID, nil) },
		func() error { return repo.ApplyMacroMutation(92, conversation.ID, MacroConversationMutation{}) },
	} {
		require.ErrorIs(t, mutate(), gorm.ErrRecordNotFound)
	}
	wrongTenant := conversation
	wrongTenant.AccountID = 92
	require.ErrorIs(t, repo.Update(&wrongTenant), gorm.ErrRecordNotFound)
	require.NoError(t, repo.db.Delete(&domain.Conversation{}, conversation.ID).Error)
	require.ErrorIs(t, repo.Update(&conversation), gorm.ErrRecordNotFound)
	var count int64
	require.NoError(t, repo.db.Model(&domain.Conversation{}).Count(&count).Error)
	require.Zero(t, count)
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1, "failed owner mutations must not create facts")
}

func appendConversationRuntimeFact(t *testing.T, repo *ConversationRepository, kind string, occurredAt time.Time) foundation.EventEnvelope {
	t.Helper()
	eventID := uuid.NewString()
	event := foundation.EventEnvelope{
		EventID: eventID, EventType: kind, EventVersion: "v1", SourceModule: "CON", AccountID: 91,
		AggregateType: "Conversation", AggregateID: "17", AggregateVersion: 2,
		OccurredAt: occurredAt, PublishedAt: occurredAt,
		Actor:         foundation.ContractActor{Type: "System", ID: "AUT", AuthSource: "module"},
		CorrelationID: "original-root", CausationID: "aut-step-command", ChangedFields: []string{"status"},
		Payload:            map[string]any{"conversation_id": 17, "object_version": 2, "automation_ancestry": []string{"root", "parent"}, "automation_depth": 2},
		DataClassification: "internal",
	}
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, repo.db.Create(&domain.LocalChangeJournal{
		ChangeID: eventID, SourceModule: "CON", AccountID: 91, ObjectType: "Conversation", ObjectDisplayID: "17",
		ObjectVersionAfter: 2, Action: "event_outbox", AfterState: string(encoded), ActorType: "System", EventID: eventID,
		CorrelationID: event.CorrelationID, CausationID: event.CausationID, OccurredAt: occurredAt, Result: "pending", Status: "pending",
	}).Error)
	// Normalize JSON number/list representations exactly as a pending read does.
	require.NoError(t, json.Unmarshal(encoded, &event))
	return event
}

func TestConversationAutomationRuntimeReceiptsAndActivationSurviveRestart(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	old := appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().Add(-time.Hour))
	repo.SetAutomationCapture(true)
	// The first poll persists activation even if there are no new facts.
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
	status := appendConversationRuntimeFact(t, repo, "con.conversation_status.changed.v1", time.Now().UTC())
	assignment := appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().UTC())
	ignored := appendConversationRuntimeFact(t, repo, "con.conversation.deleted.v1", time.Now().UTC())
	// A separate reader after restart must preserve the original enable boundary.
	restarted := &ConversationRepository{db: repo.db}
	restarted.SetAutomationCapture(true)
	facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, []foundation.EventEnvelope{status, assignment}, facts)
	require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), status.EventID))
	require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), status.EventID))
	var outbox domain.LocalChangeJournal
	require.NoError(t, repo.db.Where("change_id = ?", status.EventID).First(&outbox).Error)
	require.Equal(t, "pending", outbox.Status)
	require.Equal(t, "pending", outbox.Result)
	// External delivery may happen before automation, without suppressing it.
	require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", assignment.EventID).Updates(map[string]any{"status": "delivered", "result": "applied"}).Error)
	facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, []foundation.EventEnvelope{assignment}, facts)
	require.ErrorIs(t, restarted.MarkAutomationFactDelivered(context.Background(), ignored.EventID), gorm.ErrInvalidData)
	outbox = domain.LocalChangeJournal{}
	require.NoError(t, repo.db.Where("change_id = ?", old.EventID).First(&outbox).Error)
	require.Equal(t, "pending", outbox.Status)
	restarted.SetAutomationCapture(false)
	facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
}

func TestConversationAutomationConcurrentSnapshotUpdatesHaveOneWinner(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	original := automationTestConversation(t, repo)
	const workers = 8
	results := make(chan error, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			candidate := original
			candidate.CustomAttributes = fmt.Sprintf(`{"worker":%d}`, i)
			results <- repo.Update(&candidate)
		}(i)
	}
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, ErrConversationVersionConflict)
		}
	}
	require.Equal(t, 1, winners)
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 2)
	require.EqualValues(t, 2, facts[1].AggregateVersion)
}

func TestConversationAutomationLabelsCaptureOnlyActualChangesAndRollback(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	require.NoError(t, repo.db.AutoMigrate(&domain.ConversationLabel{}))
	repo.SetAutomationCapture(true)
	conversation := automationTestConversation(t, repo)
	require.NoError(t, repo.AttachLabelReference(conversation.AccountID, conversation.ID, 41))
	require.NoError(t, repo.AttachLabelReference(conversation.AccountID, conversation.ID, 41))
	require.NoError(t, repo.DetachLabel(conversation.AccountID, conversation.ID, 41))
	require.NoError(t, repo.DetachLabel(conversation.AccountID, conversation.ID, 41))
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 3)
	require.Equal(t, []string{"labels"}, facts[1].ChangedFields)
	require.Equal(t, []string{"labels"}, facts[2].ChangedFields)
	var current domain.Conversation
	require.NoError(t, repo.db.First(&current, conversation.ID).Error)
	require.Equal(t, 3, current.ObjectVersion)
	require.ErrorIs(t, repo.AttachLabelReference(92, conversation.ID, 42), gorm.ErrRecordNotFound)
	require.NoError(t, repo.db.Callback().Create().Before("gorm:create").Register("reject_label_fact", func(tx *gorm.DB) {
		if record, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && record.Action == "automation_trigger" {
			_ = tx.AddError(errors.New("injected label capture failure"))
		}
	}))
	require.ErrorContains(t, repo.AttachLabelReference(conversation.AccountID, conversation.ID, 43), "injected label capture failure")
	var count int64
	require.NoError(t, repo.db.Model(&domain.ConversationLabel{}).Where("conversation_id = ?", conversation.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, repo.db.First(&current, conversation.ID).Error)
	require.Equal(t, 3, current.ObjectVersion)
}

func TestConversationAutomationEditableUpdatePreservesOwnerProjections(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	conversation := automationTestConversation(t, repo)
	// Simulate a projection update that does not participate in general editing.
	require.NoError(t, repo.db.Model(&domain.Conversation{}).Where("id = ?", conversation.ID).Updates(map[string]any{
		"unread_count": 9, "sla_status": "breached", "contact_id": 81, "inbox_id": 82,
	}).Error)
	conversation.Priority = "high"
	require.NoError(t, repo.Update(&conversation))
	var stored domain.Conversation
	require.NoError(t, repo.db.First(&stored, conversation.ID).Error)
	require.Equal(t, 9, stored.UnreadCount)
	require.Equal(t, "breached", stored.SLAStatus)
	require.EqualValues(t, 81, stored.ContactID)
	require.EqualValues(t, 82, stored.InboxID)
	require.Equal(t, "high", stored.Priority)
	stale := stored
	require.NoError(t, repo.ToggleMute(stored.AccountID, stored.ID, true))
	require.ErrorIs(t, repo.Update(&stale), ErrConversationVersionConflict)
	merged, err := repo.UpdateCustomAttributes(stored.AccountID, stored.ID, map[string]any{"first": 1, "precise": json.Number("9007199254740993")})
	require.NoError(t, err)
	require.EqualValues(t, 1, merged["first"])
	merged, err = repo.UpdateCustomAttributes(stored.AccountID, stored.ID, map[string]any{"second": 2})
	require.NoError(t, err)
	require.Equal(t, json.Number("1"), merged["first"])
	require.Equal(t, json.Number("9007199254740993"), merged["precise"])
	require.EqualValues(t, 2, merged["second"])
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 5)
	require.Equal(t, []string{"muted"}, facts[2].ChangedFields)
	require.Equal(t, []string{"custom_attributes"}, facts[3].ChangedFields)
	require.Equal(t, []string{"custom_attributes"}, facts[4].ChangedFields)
}

func TestConversationAutomationMessageActivityVersionIsStableAndTenantScoped(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	conversation := automationTestConversation(t, repo)
	for _, changeID := range []string{uuid.NewString(), strings.Repeat("x", 64)} {
		before := conversation.ObjectVersion
		require.NoError(t, repo.ApplyMessageActivity(conversation.AccountID, conversation.ID, 0, true, time.Now().UTC(), changeID))
		version, err := repo.AutomationMessageActivityVersion(conversation.AccountID, conversation.ID, changeID)
		require.NoError(t, err)
		require.Equal(t, before+1, version)
		require.NoError(t, repo.UpdatePriority(conversation.AccountID, conversation.ID, "urgent"))
		stable, err := repo.AutomationMessageActivityVersion(conversation.AccountID, conversation.ID, changeID)
		require.NoError(t, err)
		require.Equal(t, version, stable)
		_, err = repo.AutomationMessageActivityVersion(92, conversation.ID, changeID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		_, err = repo.AutomationMessageActivityVersion(conversation.AccountID, conversation.ID+100, changeID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		require.NoError(t, repo.db.First(&conversation, conversation.ID).Error)
	}
	_, err := repo.AutomationMessageActivityVersion(conversation.AccountID, conversation.ID, "missing")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestConversationAutomationQuarantinesPoisonWithoutStarvingLaterFacts(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	conversation := automationTestConversation(t, repo)
	var first domain.LocalChangeJournal
	require.NoError(t, repo.db.Where("action = ?", "automation_trigger").First(&first).Error)
	require.NoError(t, repo.UpdatePriority(conversation.AccountID, conversation.ID, "high"))
	var later domain.LocalChangeJournal
	require.NoError(t, repo.db.Where("action = ? AND id > ?", "automation_trigger", first.ID).First(&later).Error)
	// Duplicate corrupt metadata must not quarantine/suppress the valid event.
	require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("id = ?", first.ID).Updates(map[string]any{"after_state": "{broken-json", "event_id": later.EventID}).Error)
	facts, err := repo.PendingAutomationFacts(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, later.EventID, facts[0].EventID)
	facts, err = repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	var quarantines []domain.LocalChangeJournal
	require.NoError(t, repo.db.Where("action = ?", "automation_quarantine").Find(&quarantines).Error)
	require.Len(t, quarantines, 1)
	require.Equal(t, first.ID, quarantines[0].EntityID)
	require.Equal(t, first.ChangeID, quarantines[0].CommandID)
	require.Equal(t, "manual_action_required", quarantines[0].Status)
	require.Equal(t, "invalid_automation_fact", quarantines[0].Reason)
	require.NoError(t, repo.db.First(&first, first.ID).Error)
	require.Equal(t, "pending", first.Status)
	require.Equal(t, "{broken-json", first.AfterState)
	require.NoError(t, repo.MarkAutomationFactDelivered(context.Background(), later.EventID))
	facts, err = repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
}

func TestConversationAutomationQuarantineLeavesRuntimeOutboxPending(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	_, err := repo.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	bad := appendConversationRuntimeFact(t, repo, "con.conversation_status.changed.v1", time.Now().UTC())
	good := appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().UTC())
	require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", bad.EventID).Update("after_state", "null").Error)
	facts, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, []foundation.EventEnvelope{good}, facts)
	var original domain.LocalChangeJournal
	require.NoError(t, repo.db.Where("change_id = ?", bad.EventID).First(&original).Error)
	require.Equal(t, "pending", original.Status)
	require.Equal(t, "pending", original.Result)
	var quarantine domain.LocalChangeJournal
	require.NoError(t, repo.db.Where("action = ? AND entity_id = ?", "automation_quarantine", original.ID).First(&quarantine).Error)
	require.Equal(t, "manual_action_required", quarantine.Status)
}

func TestConversationAutomationValidatesPayloadOwnerIdentityAndVersion(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		value any
	}{
		{"other conversation", "conversation_id", 999},
		{"zero version", "object_version", 0},
		{"fractional version", "object_version", 1.5},
		{"string version", "object_version", "2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := conversationAutomationTestRepository(t, true)
			repo.SetAutomationCapture(true)
			_, err := repo.PendingAutomationFacts(context.Background(), 1)
			require.NoError(t, err)
			bad := appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().UTC())
			bad.Payload[test.field] = test.value
			payload, err := json.Marshal(bad)
			require.NoError(t, err)
			require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", bad.EventID).Update("after_state", string(payload)).Error)
			facts, err := repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Empty(t, facts)
			var count int64
			require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_quarantine").Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
	t.Run("runtime sequence differs from owner version", func(t *testing.T) {
		repo := conversationAutomationTestRepository(t, true)
		repo.SetAutomationCapture(true)
		_, err := repo.PendingAutomationFacts(context.Background(), 1)
		require.NoError(t, err)
		event := appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().UTC())
		event.Payload["object_version"] = 99
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", event.EventID).Update("after_state", string(payload)).Error)
		facts, err := repo.PendingAutomationFacts(context.Background(), 1)
		require.NoError(t, err)
		require.Len(t, facts, 1)
		require.EqualValues(t, 2, facts[0].AggregateVersion)
		require.EqualValues(t, 99, facts[0].Payload["object_version"])
	})
}

func TestConversationAutomationDefersFutureWithoutStarvingHealthyFact(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	_, err := repo.PendingAutomationFacts(context.Background(), 1)
	require.NoError(t, err)
	future := appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().UTC().Add(time.Hour))
	healthy := appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().UTC())
	facts, err := repo.PendingAutomationFacts(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, []foundation.EventEnvelope{healthy}, facts)
	var count int64
	require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Where("action IN ? AND event_id = ?", []string{"automation_quarantine", "automation_delivery"}, future.EventID).Count(&count).Error)
	require.Zero(t, count)
	var original domain.LocalChangeJournal
	require.NoError(t, repo.db.Where("change_id = ?", future.EventID).First(&original).Error)
	require.Equal(t, "pending", original.Status)
}
