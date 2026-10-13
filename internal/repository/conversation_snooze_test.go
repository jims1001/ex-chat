package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func snoozeCandidate(t *testing.T, repo *ConversationRepository, account uint, cutoff time.Time) domain.Conversation {
	t.Helper()
	deadline := cutoff.Add(-time.Minute)
	c := domain.Conversation{AccountID: account, InboxID: 17, ContactID: 19, Status: "snoozed", SnoozedUntil: &deadline, ObjectVersion: 7, LastActivityAt: cutoff.Add(-time.Hour)}
	require.NoError(t, repo.db.Create(&c).Error)
	return c
}

func TestConversationSnoozeExpiryMatrixAndReplay(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy_without_journal", true: "durable"}[capture], func(t *testing.T) {
			repo := conversationAutomationTestRepository(t, capture)
			repo.SetAutomationCapture(capture)
			cutoff := time.Now().UTC().Truncate(time.Millisecond)
			before := snoozeCandidate(t, repo, 91, cutoff)
			equal := snoozeCandidate(t, repo, 92, cutoff)
			require.NoError(t, repo.db.Model(&equal).Update("snoozed_until", cutoff).Error)
			var untouched []domain.Conversation
			for _, status := range []string{"open", "pending", "resolved", "deleting", "snoozed", "snoozed"} {
				c := snoozeCandidate(t, repo, 91, cutoff)
				c.Status = status
				if len(untouched) == 4 {
					future := cutoff.Add(time.Minute)
					c.SnoozedUntil = &future
				}
				if len(untouched) == 5 {
					c.SnoozedUntil = nil
				}
				require.NoError(t, repo.db.Save(&c).Error)
				untouched = append(untouched, c)
			}
			winners, err := repo.ResumeExpiredSnoozed(context.Background(), cutoff)
			require.NoError(t, err)
			require.Len(t, winners, 2)
			require.Equal(t, []uint{before.ID, equal.ID}, []uint{winners[0].ID, winners[1].ID})
			for _, c := range winners {
				require.Equal(t, "open", c.Status)
				require.Nil(t, c.SnoozedUntil)
				require.Equal(t, 8, c.ObjectVersion)
				require.True(t, cutoff.Equal(c.LastActivityAt))
				var stored domain.Conversation
				require.NoError(t, repo.db.First(&stored, c.ID).Error)
				require.Equal(t, c, stored, "return the committed authoritative row")
			}
			for _, expected := range untouched {
				var stored domain.Conversation
				require.NoError(t, repo.db.First(&stored, expected.ID).Error)
				require.Equal(t, expected.Status, stored.Status)
				require.Equal(t, expected.ObjectVersion, stored.ObjectVersion)
				require.Equal(t, expected.SnoozedUntil, stored.SnoozedUntil)
				require.True(t, expected.LastActivityAt.Equal(stored.LastActivityAt))
			}
			winners, err = repo.ResumeExpiredSnoozed(context.Background(), cutoff)
			require.NoError(t, err)
			require.Empty(t, winners)
			if !capture {
				require.False(t, repo.db.Migrator().HasTable(&domain.LocalChangeJournal{}))
				return
			}
			facts, err := repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Len(t, facts, 2)
			for _, fact := range facts {
				require.Equal(t, "CON", fact.SourceModule)
				require.Equal(t, "con.conversation.changed.v1", fact.EventType)
				require.Equal(t, "conversation_updated", fact.Payload["event_name"])
				require.Equal(t, "snoozed", fact.Payload["status_before"])
				require.Equal(t, "open", fact.Payload["status_after"])
				require.EqualValues(t, 8, fact.AggregateVersion)
				require.Equal(t, []string{"last_activity_at", "snoozed_until", "status"}, fact.ChangedFields)
			}
			restarted := &ConversationRepository{db: repo.db}
			restarted.SetAutomationCapture(true)
			replay, err := restarted.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, facts, replay)
			for _, fact := range replay {
				require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), fact.EventID))
				require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), fact.EventID))
			}
			replay, err = restarted.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Empty(t, replay)
		})
	}
}

func TestConversationSnoozeExpiryRejectsStaleScans(t *testing.T) {
	for _, change := range []string{"priority", "open", "resolved", "deleting", "delete", "extend", "null", "resnooze", "wrong_tenant"} {
		t.Run(change, func(t *testing.T) {
			repo := conversationAutomationTestRepository(t, true)
			repo.SetAutomationCapture(true)
			cutoff := time.Now().UTC()
			candidate := snoozeCandidate(t, repo, 91, cutoff)
			switch change {
			case "priority":
				require.NoError(t, repo.UpdatePriority(91, candidate.ID, "urgent"))
			case "open", "resolved", "deleting":
				require.NoError(t, repo.UpdateStatus(91, candidate.ID, change, nil))
			case "delete":
				require.NoError(t, repo.db.Delete(&candidate).Error)
			case "extend":
				require.NoError(t, repo.db.Model(&candidate).Update("snoozed_until", cutoff.Add(time.Hour)).Error)
			case "null":
				require.NoError(t, repo.db.Model(&candidate).Update("snoozed_until", nil).Error)
			case "resnooze":
				deadline := cutoff.Add(-time.Second)
				require.NoError(t, repo.UpdateStatus(91, candidate.ID, "snoozed", &deadline))
			case "wrong_tenant":
				candidate.AccountID++
			}
			var before domain.Conversation
			errBefore := repo.db.First(&before, candidate.ID).Error
			var factsBefore int64
			require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Count(&factsBefore).Error)
			winner, err := repo.resumeExpiredSnoozedCandidate(context.Background(), candidate, cutoff)
			require.NoError(t, err)
			require.Nil(t, winner)
			var after domain.Conversation
			require.Equal(t, errBefore, repo.db.First(&after, candidate.ID).Error)
			require.Equal(t, before, after)
			var factsAfter int64
			require.NoError(t, repo.db.Model(&domain.LocalChangeJournal{}).Count(&factsAfter).Error)
			require.Equal(t, factsBefore, factsAfter)
		})
	}
}

func TestConversationSnoozeExpiryRollbackAndPartialCommit(t *testing.T) {
	for _, failAction := range []string{"automation_activation", "automation_trigger"} {
		t.Run(failAction, func(t *testing.T) {
			repo := conversationAutomationTestRepository(t, true)
			repo.SetAutomationCapture(true)
			cutoff := time.Now().UTC()
			first := snoozeCandidate(t, repo, 91, cutoff)
			second := snoozeCandidate(t, repo, 91, cutoff)
			require.NoError(t, repo.db.Callback().Create().Before("gorm:create").Register("reject_snooze_fact", func(tx *gorm.DB) {
				if change, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && change.Action == failAction && (failAction == "automation_activation" || change.ObjectID == second.ID) {
					_ = tx.AddError(errors.New("snooze journal unavailable"))
				}
			}))
			winners, err := repo.ResumeExpiredSnoozed(context.Background(), cutoff)
			require.ErrorContains(t, err, "snooze journal unavailable")
			if failAction == "automation_trigger" {
				require.Len(t, winners, 1)
				require.Equal(t, first.ID, winners[0].ID)
			} else {
				require.Empty(t, winners)
				var stored domain.Conversation
				require.NoError(t, repo.db.First(&stored, first.ID).Error)
				require.Equal(t, first.Status, stored.Status)
				require.Equal(t, first.ObjectVersion, stored.ObjectVersion)
			}
			var stored domain.Conversation
			require.NoError(t, repo.db.First(&stored, second.ID).Error)
			require.Equal(t, second.Status, stored.Status)
			require.Equal(t, second.ObjectVersion, stored.ObjectVersion)
			require.True(t, second.SnoozedUntil.Equal(*stored.SnoozedUntil))
			require.True(t, second.LastActivityAt.Equal(stored.LastActivityAt))
			require.NoError(t, repo.db.Callback().Create().Remove("reject_snooze_fact"))
			_, err = repo.ResumeExpiredSnoozed(context.Background(), cutoff)
			require.NoError(t, err)
			facts, err := repo.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Len(t, facts, 2)
		})
	}
}

func TestConversationSnoozeExpiryCancelledContext(t *testing.T) {
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	cutoff := time.Now().UTC()
	candidate := snoozeCandidate(t, repo, 91, cutoff)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	winners, err := repo.ResumeExpiredSnoozed(ctx, cutoff)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, winners)
	winner, err := repo.resumeExpiredSnoozedCandidate(ctx, candidate, cutoff)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, winner)
}
