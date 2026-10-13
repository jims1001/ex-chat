package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type automationPagedSource interface {
	PendingAutomationFactPage(context.Context, int, string) ([]foundation.EventEnvelope, string, error)
	PendingAutomationFacts(context.Context, int) ([]foundation.EventEnvelope, error)
	MarkAutomationFactDelivered(context.Context, string) error
}

func automationPageFixture(t *testing.T, owner string) (automationPagedSource, func() foundation.EventEnvelope, func() automationPagedSource) {
	t.Helper()
	if owner == "MSG" {
		repo, port, _, db, conversation := automationMessageFixture(t)
		appendFact := func() foundation.EventEnvelope {
			_, _, event := automationRuntimeMessageFact(t, db, conversation, false)
			return event
		}
		restart := func() automationPagedSource {
			restarted := NewMessageRepositoryWithConversationPort(db, port)
			restarted.SetAutomationCapture(true)
			return restarted
		}
		return repo, appendFact, restart
	}
	repo := conversationAutomationTestRepository(t, true)
	repo.SetAutomationCapture(true)
	_, err := repo.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	appendFact := func() foundation.EventEnvelope {
		return appendConversationRuntimeFact(t, repo, "con.conversation.changed.v1", time.Now().UTC())
	}
	restart := func() automationPagedSource {
		restarted := &ConversationRepository{db: repo.db}
		restarted.SetAutomationCapture(true)
		return restarted
	}
	return repo, appendFact, restart
}

func TestAutomationFactPageTraversesUnacknowledgedRuntimeFamilies(t *testing.T) {
	for _, owner := range []string{"MSG", "CON"} {
		t.Run(owner, func(t *testing.T) {
			source, appendFact, restart := automationPageFixture(t, owner)
			var expected []foundation.EventEnvelope
			// The owner knows only these immutable facts, not which AUT origin
			// families are still running. None of this first page is acknowledged.
			for i := 0; i < 105; i++ {
				expected = append(expected, appendFact())
			}
			first, cursor, err := source.PendingAutomationFactPage(context.Background(), 100, "")
			require.NoError(t, err)
			require.Equal(t, expected[:100], first)
			require.NotEmpty(t, cursor)
			second, end, err := restart().PendingAutomationFactPage(context.Background(), 100, cursor)
			require.NoError(t, err)
			require.Equal(t, expected[100:], second)
			require.Empty(t, end)
			// Reusing an opaque cursor is stable; two bridge workers need no
			// shared mutable offset, nor a source-side acknowledgement to advance.
			replayed, repeatedEnd, err := source.PendingAutomationFactPage(context.Background(), 100, cursor)
			require.NoError(t, err)
			require.Equal(t, second, replayed)
			require.Equal(t, end, repeatedEnd)
			legacy, err := source.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, first, legacy)
		})
	}
}

func TestAutomationFactPageHorizonExcludesFactsCreatedDuringPass(t *testing.T) {
	for _, owner := range []string{"MSG", "CON"} {
		t.Run(owner, func(t *testing.T) {
			source, appendFact, restart := automationPageFixture(t, owner)
			var expected []foundation.EventEnvelope
			for i := 0; i < 3; i++ {
				expected = append(expected, appendFact())
			}
			first, cursor, err := source.PendingAutomationFactPage(context.Background(), 1, "")
			require.NoError(t, err)
			require.Equal(t, expected[:1], first)
			all := append([]foundation.EventEnvelope{}, first...)
			var newFacts []foundation.EventEnvelope
			for page := 0; cursor != ""; page++ {
				require.Less(t, page, 4, "processing emitted new facts must not extend the captured pass horizon")
				newFacts = append(newFacts, appendFact())
				facts, next, err := restart().PendingAutomationFactPage(context.Background(), 1, cursor)
				require.NoError(t, err)
				all = append(all, facts...)
				cursor = next
			}
			require.Equal(t, expected, all)
			for _, event := range expected {
				require.NoError(t, source.MarkAutomationFactDelivered(context.Background(), event.EventID))
			}
			nextPass, cursor, err := source.PendingAutomationFactPage(context.Background(), 100, "")
			require.NoError(t, err)
			require.Equal(t, newFacts, nextPass)
			require.Empty(t, cursor)
		})
	}
}

func TestMessageAutomationFactPageHorizonAndOrderingAcrossStages(t *testing.T) {
	repo, _, _, db, conversation := automationMessageFixture(t)
	_, _, action := automationRuntimeMessageFact(t, db, conversation, false)
	ordinary := automationTestMessage(t, repo, conversation)
	first, cursor, err := repo.PendingAutomationFactPage(context.Background(), 1, "")
	require.NoError(t, err)
	require.Len(t, first, 1)
	id, err := messageAutomationUint(first[0].Payload["message_id"])
	require.NoError(t, err)
	require.Equal(t, ordinary.ID, id)
	require.NotEmpty(t, cursor)
	// Both stages must retain the original horizon, including when the first
	// runtime row predates the first ordinary row and is reached only later.
	newOrdinary := automationTestMessage(t, repo, conversation)
	_, _, newAction := automationRuntimeMessageFact(t, db, conversation, true)
	second, end, err := repo.PendingAutomationFactPage(context.Background(), 100, cursor)
	require.NoError(t, err)
	require.Equal(t, []foundation.EventEnvelope{action}, second)
	require.Empty(t, end)
	require.NoError(t, repo.MarkAutomationFactDelivered(context.Background(), first[0].EventID))
	require.NoError(t, repo.MarkAutomationFactDelivered(context.Background(), action.EventID))
	next, end, err := repo.PendingAutomationFactPage(context.Background(), 100, "")
	require.NoError(t, err)
	require.Len(t, next, 2)
	id, err = messageAutomationUint(next[0].Payload["message_id"])
	require.NoError(t, err)
	require.Equal(t, newOrdinary.ID, id)
	require.Equal(t, newAction, next[1])
	require.Empty(t, end)
}

func TestAutomationFactPageCursorValidation(t *testing.T) {
	for _, owner := range []string{"MSG", "CON"} {
		t.Run(owner, func(t *testing.T) {
			source, appendFact, _ := automationPageFixture(t, owner)
			appendFact()
			appendFact()
			_, cursor, err := source.PendingAutomationFactPage(context.Background(), 1, "")
			require.NoError(t, err)
			require.NotEmpty(t, cursor)
			raw, err := base64.RawURLEncoding.DecodeString(cursor)
			require.NoError(t, err)
			var valid map[string]any
			require.NoError(t, json.Unmarshal(raw, &valid))
			invalid := []string{"not-a-cursor", strings.Repeat("x", 513), cursor + "\n", base64.RawURLEncoding.EncodeToString([]byte("{}")), base64.RawURLEncoding.EncodeToString(append(raw, []byte(" {}")...))}
			for _, mutation := range []struct {
				key   string
				value any
			}{
				{"owner", "OTHER"}, {"version", 0}, {"version", 2}, {"horizon", 0}, {"horizon", -1}, {"horizon", 1.5}, {"horizon", "100"}, {"after_id", valid["horizon"].(float64) + 1}, {"stage", -1}, {"stage", 2}, {"unexpected", true},
			} {
				var changed map[string]any
				require.NoError(t, json.Unmarshal(raw, &changed))
				changed[mutation.key] = mutation.value
				encoded, err := json.Marshal(changed)
				require.NoError(t, err)
				invalid = append(invalid, base64.RawURLEncoding.EncodeToString(encoded))
			}
			for _, token := range invalid {
				facts, next, err := source.PendingAutomationFactPage(context.Background(), 1, token)
				require.ErrorIs(t, err, gorm.ErrInvalidData, "cursor=%q", token)
				require.Empty(t, facts)
				require.Empty(t, next)
			}
			other := "CON"
			if owner == "CON" {
				other = "MSG"
			}
			otherSource, _, _ := automationPageFixture(t, other)
			_, _, err = otherSource.PendingAutomationFactPage(context.Background(), 1, cursor)
			require.ErrorIs(t, err, gorm.ErrInvalidData, "owner cursors are not interchangeable")
			// Validation does not consume any owner facts.
			facts, err := source.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Len(t, facts, 2)
		})
	}
}

func TestAutomationFactPageSkipsReceiptsAndPoisonBetweenPages(t *testing.T) {
	for _, owner := range []string{"MSG", "CON"} {
		t.Run(owner, func(t *testing.T) {
			source, appendFact, _ := automationPageFixture(t, owner)
			first := appendFact()
			consumed := appendFact()
			poison := appendFact()
			last := appendFact()
			page, cursor, err := source.PendingAutomationFactPage(context.Background(), 1, "")
			require.NoError(t, err)
			require.Equal(t, []foundation.EventEnvelope{first}, page)
			require.NoError(t, source.MarkAutomationFactDelivered(context.Background(), consumed.EventID))
			var db *gorm.DB
			switch repo := source.(type) {
			case *MessageRepository:
				db = repo.db
			case *ConversationRepository:
				db = repo.db
			}
			require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ? AND event_id = ?", owner, "event_outbox", poison.EventID).Update("after_state", "{").Error)
			page, end, err := source.PendingAutomationFactPage(context.Background(), 100, cursor)
			require.NoError(t, err)
			require.Equal(t, []foundation.EventEnvelope{last}, page)
			require.Empty(t, end)
		})
	}
}
