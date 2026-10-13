package repository

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func outboundFixture(t *testing.T) (*MessageRepository, *gorm.DB, domain.Conversation) {
	t.Helper()
	r, _, _, db, c := retryFixture(t, domain.ConversationStatusOpen)
	require.NoError(t, db.AutoMigrate(&domain.OutboundMessageAttempt{}))
	return r, db, c
}

func outboundInput(c domain.Conversation) (domain.Message, domain.OutboundMessageAttempt) {
	m := domain.Message{AccountID: c.AccountID, ConversationID: c.ID, SenderID: 17, SenderType: domain.SenderTypeUser, MessageType: domain.MessageTypeOutgoing, ContentType: domain.ContentTypeText, Content: "outbound body sentinel", Status: domain.MessageStatusQueued}
	a := domain.OutboundMessageAttempt{ID: foundation.GenerateUUID(), AccountID: c.AccountID, ConversationID: c.ID, InboxID: c.InboxID, ContactID: c.ContactID, ConnectionID: 3, Provider: "twilio", ProviderAccountID: "ACtest", SenderID: "+15005550006", RecipientID: "+15005550007", ConfigFingerprint: "non-secret-config-digest", APIVersion: "2010-04-01", CallbackURL: "https://app.example.test/status", Body: m.Content, ContentType: m.ContentType, MessageType: m.MessageType, SenderType: m.SenderType, ActorID: m.SenderID}
	return m, a
}

func createOutbound(t *testing.T, r *MessageRepository, c domain.Conversation) (domain.Message, domain.OutboundMessageAttempt) {
	t.Helper()
	m, a := outboundInput(c)
	created, err := r.CreateOutboundMessage(context.Background(), &m, &a)
	require.NoError(t, err)
	require.True(t, created)
	return m, a
}

func dispatchOutbound(t *testing.T, r *MessageRepository, a domain.OutboundMessageAttempt) domain.OutboundMessageAttempt {
	t.Helper()
	now := time.Now().UTC()
	claimed, err := r.ClaimNextOutbound(context.Background(), now, time.Minute)
	require.NoError(t, err)
	require.Equal(t, a.ID, claimed.ID)
	started, err := r.StartOutboundDispatch(context.Background(), a.AccountID, a.ID, claimed.ClaimToken, now)
	require.NoError(t, err)
	return *started
}

func TestOutboundAtomicCreateAndExactReplay(t *testing.T) {
	r, db, c := outboundFixture(t)
	m, a := outboundInput(c)
	originalM, originalA := m, a
	created, err := r.CreateOutboundMessage(context.Background(), &m, &a)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, m.ID, a.MessageID)
	require.Equal(t, 1, a.Sequence)
	created, err = r.CreateOutboundMessage(context.Background(), &originalM, &originalA)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, m.ID, originalM.ID)
	var count int64
	require.NoError(t, db.Model(&domain.Message{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	changedM, changedA := outboundInput(c)
	changedA.ID = a.ID
	changedM.Content, changedA.Body = "different", "different"
	_, err = r.CreateOutboundMessage(context.Background(), &changedM, &changedA)
	require.ErrorIs(t, err, ErrOutboundConflict)
	var journals []domain.LocalChangeJournal
	require.NoError(t, db.Find(&journals).Error)
	for _, journal := range journals {
		raw, err := json.Marshal(journal)
		require.NoError(t, err)
		require.NotContains(t, string(raw), m.Content)
	}
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(raw), m.Content)
	badM, badA := outboundInput(c)
	badA.RecipientID = ""
	_, err = r.CreateOutboundMessage(context.Background(), &badM, &badA)
	require.Error(t, err)
	require.NoError(t, db.Model(&domain.Message{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	dispatched := dispatchOutbound(t, r, a)
	require.NoError(t, r.ApplyOutboundStatus(context.Background(), dispatched, "SMfirst", domain.MessageStatusDelivered))
	replayM, replayA := outboundInput(c)
	replayA.ID = a.ID
	created, err = r.CreateOutboundMessage(context.Background(), &replayM, &replayA)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, domain.MessageStatusDelivered, replayM.Status)
	require.NoError(t, db.Model(&domain.Message{}).Where("id = ?", m.ID).Update("deleted", true).Error)
	replayM, replayA = outboundInput(c)
	replayA.ID = a.ID
	_, err = r.CreateOutboundMessage(context.Background(), &replayM, &replayA)
	require.ErrorIs(t, err, ErrOutboundConflict)
}

func TestOutboundConcurrentCreateAndClaims(t *testing.T) {
	r, db, c := outboundFixture(t)
	baseM, baseA := outboundInput(c)
	const workers = 12
	var wg sync.WaitGroup
	created, errs := make([]bool, workers), make([]error, workers)
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m, a := baseM, baseA
			created[n], errs[n] = r.CreateOutboundMessage(context.Background(), &m, &a)
		}(n)
	}
	wg.Wait()
	accepted := 0
	for n := range errs {
		require.NoError(t, errs[n])
		if created[n] {
			accepted++
		}
	}
	require.Equal(t, 1, accepted)
	claims := make([]*domain.OutboundMessageAttempt, workers)
	now := time.Now().UTC()
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Independent repository instances share only durable MSG state.
			other := NewMessageRepositoryWithConversationPort(db, r.conversation)
			claims[n], errs[n] = other.ClaimNextOutbound(context.Background(), now, time.Minute)
		}(n)
	}
	wg.Wait()
	claimed := 0
	for n := range errs {
		if errs[n] == nil {
			claimed++
			require.Equal(t, baseA.ID, claims[n].ID)
		} else {
			require.ErrorIs(t, errs[n], gorm.ErrRecordNotFound)
		}
	}
	require.Equal(t, 1, claimed)
}

func TestOutboundClaimExpiryAndDispatchFence(t *testing.T) {
	r, db, c := outboundFixture(t)
	_, a := createOutbound(t, r, c)
	ctx, now := context.Background(), time.Now().UTC()
	first, err := r.ClaimNextOutbound(ctx, now, time.Second)
	require.NoError(t, err)
	_, err = r.StartOutboundDispatch(ctx, a.AccountID, a.ID, first.ClaimToken, now.Add(time.Second))
	require.ErrorIs(t, err, ErrOutboundClaimLost)
	second, err := r.ClaimNextOutbound(ctx, now.Add(2*time.Second), time.Second)
	require.NoError(t, err)
	require.NotEqual(t, first.ClaimToken, second.ClaimToken)
	_, err = r.StartOutboundDispatch(ctx, a.AccountID, a.ID, first.ClaimToken, now.Add(2*time.Second))
	require.ErrorIs(t, err, ErrOutboundClaimLost)
	require.ErrorIs(t, r.CancelClaimedOutbound(ctx, a.AccountID, a.ID, first.ClaimToken, now.Add(2*time.Second)), ErrOutboundClaimLost)
	_, err = r.StartOutboundDispatch(ctx, a.AccountID, a.ID, second.ClaimToken, now.Add(2*time.Second))
	require.NoError(t, err)
	_, err = r.StartOutboundDispatch(ctx, a.AccountID, a.ID, second.ClaimToken, now.Add(2*time.Second))
	require.ErrorIs(t, err, ErrOutboundClaimLost)
	restarted := NewMessageRepositoryWithConversationPort(db, r.conversation)
	_, err = restarted.ClaimNextOutbound(ctx, now.Add(4*time.Second), time.Minute)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	after, err := restarted.GetOutboundAttempt(ctx, a.AccountID, a.ID)
	require.NoError(t, err)
	require.Equal(t, domain.OutboundStateUnknown, after.State)
	require.Equal(t, domain.MessageStatusUnknown, after.Status)
	require.False(t, after.RetrySafe)
	require.ErrorIs(t, restarted.CancelClaimedOutbound(ctx, a.AccountID, a.ID, second.ClaimToken, now.Add(4*time.Second)), ErrOutboundClaimLost)
	_, retry := outboundInput(c)
	_, _, err = restarted.RetryOutboundMessage(ctx, c.AccountID, c.ID, a.MessageID, &retry)
	require.ErrorIs(t, err, ErrMessageNotRetryable)
	// A late response can establish delivery; expiry never makes another send.
	require.NoError(t, restarted.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, second.ClaimToken, "SMlate", domain.MessageStatusProviderAccepted, "accepted"))
	after, err = restarted.GetOutboundAttempt(ctx, a.AccountID, a.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MessageStatusProviderAccepted, after.Status)
}

func TestOutboundCallbackBeforeResponseMonotonicAndBinding(t *testing.T) {
	r, _, c := outboundFixture(t)
	_, a := createOutbound(t, r, c)
	ctx := context.Background()
	require.ErrorIs(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusSent), ErrOutboundConflict)
	a = dispatchOutbound(t, r, a)
	for _, status := range []string{domain.MessageStatusSent, domain.MessageStatusFailed, domain.MessageStatusProviderAccepted, domain.MessageStatusSent, domain.MessageStatusDelivered, domain.MessageStatusSent, domain.MessageStatusFailed, domain.MessageStatusRead, domain.MessageStatusDelivered} {
		require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", status))
	}
	require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "SMtest", domain.MessageStatusProviderAccepted, "accepted"))
	require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "", domain.MessageStatusFailed, "rejected"))
	require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "", domain.MessageStatusUnknown, "unknown"))
	after, err := r.GetOutboundAttempt(ctx, a.AccountID, a.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MessageStatusRead, after.Status)
	require.False(t, after.RetrySafe)
	require.Equal(t, "SMtest", after.ProviderMessageID)
	require.ErrorIs(t, r.ApplyOutboundStatus(ctx, a, "SMother", domain.MessageStatusRead), ErrOutboundConflict)
	require.ErrorIs(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, foundation.GenerateUUID(), "SMtest", domain.MessageStatusSent, "accepted"), ErrOutboundClaimLost)
	mutations := map[string]func(*domain.OutboundMessageAttempt){
		"account":          func(v *domain.OutboundMessageAttempt) { v.AccountID++ },
		"conversation":     func(v *domain.OutboundMessageAttempt) { v.ConversationID++ },
		"message":          func(v *domain.OutboundMessageAttempt) { v.MessageID++ },
		"inbox":            func(v *domain.OutboundMessageAttempt) { v.InboxID++ },
		"contact":          func(v *domain.OutboundMessageAttempt) { v.ContactID++ },
		"connection":       func(v *domain.OutboundMessageAttempt) { v.ConnectionID++ },
		"sequence":         func(v *domain.OutboundMessageAttempt) { v.Sequence++ },
		"provider":         func(v *domain.OutboundMessageAttempt) { v.Provider += "x" },
		"provider_account": func(v *domain.OutboundMessageAttempt) { v.ProviderAccountID += "x" },
		"sender":           func(v *domain.OutboundMessageAttempt) { v.SenderID += "x" },
		"recipient":        func(v *domain.OutboundMessageAttempt) { v.RecipientID += "x" },
		"fingerprint":      func(v *domain.OutboundMessageAttempt) { v.ConfigFingerprint += "x" },
		"api_version":      func(v *domain.OutboundMessageAttempt) { v.APIVersion += "x" },
		"callback":         func(v *domain.OutboundMessageAttempt) { v.CallbackURL += "x" },
		"body":             func(v *domain.OutboundMessageAttempt) { v.Body += "x" },
		"content_type":     func(v *domain.OutboundMessageAttempt) { v.ContentType += "x" },
		"message_type":     func(v *domain.OutboundMessageAttempt) { v.MessageType += "x" },
		"sender_type":      func(v *domain.OutboundMessageAttempt) { v.SenderType += "x" },
		"actor":            func(v *domain.OutboundMessageAttempt) { v.ActorID++ },
		"private":          func(v *domain.OutboundMessageAttempt) { v.Private = true },
		"created":          func(v *domain.OutboundMessageAttempt) { v.CreatedAt = v.CreatedAt.Add(time.Second) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := a
			mutate(&changed)
			err := r.ApplyOutboundStatus(ctx, changed, "SMtest", domain.MessageStatusRead)
			require.Error(t, err)
		})
	}
}

func TestOutboundDeletionAndEditDoNotPromoteMessage(t *testing.T) {
	for _, mutation := range []string{"delete", "hard_delete", "body", "actor", "private", "type"} {
		t.Run(mutation, func(t *testing.T) {
			r, db, c := outboundFixture(t)
			m, a := createOutbound(t, r, c)
			a = dispatchOutbound(t, r, a)
			updates := map[string]any{}
			switch mutation {
			case "delete":
				updates["deleted"], updates["content"] = true, "[deleted]"
			case "body":
				updates["content"] = "edited"
			case "actor":
				updates["sender_id"] = 99
			case "private":
				updates["private"] = true
			case "type":
				updates["content_type"] = "image"
			case "hard_delete":
				require.NoError(t, db.Delete(&domain.Message{}, m.ID).Error)
			}
			if len(updates) != 0 {
				require.NoError(t, db.Model(&domain.Message{}).Where("id = ?", m.ID).Updates(updates).Error)
			}
			var before domain.Message
			readErr := db.First(&before, m.ID).Error
			require.NoError(t, r.ApplyOutboundStatus(context.Background(), a, "SMtest", domain.MessageStatusDelivered))
			after, err := r.GetOutboundAttempt(context.Background(), a.AccountID, a.ID)
			require.NoError(t, err)
			require.Equal(t, domain.MessageStatusDelivered, after.Status)
			var current domain.Message
			err = db.First(&current, m.ID).Error
			if mutation == "hard_delete" {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				return
			}
			require.NoError(t, readErr)
			require.NoError(t, err)
			require.Equal(t, before, current)
		})
	}
}

func TestOutboundPreDispatchValidationAndCancellation(t *testing.T) {
	r, db, c := outboundFixture(t)
	m, a := createOutbound(t, r, c)
	ctx, now := context.Background(), time.Now().UTC()
	claimed, err := r.ClaimNextOutbound(ctx, now, time.Minute)
	require.NoError(t, err)
	require.NoError(t, db.Model(&domain.Message{}).Where("id = ?", m.ID).Update("content", "changed after queueing").Error)
	_, err = r.StartOutboundDispatch(ctx, a.AccountID, a.ID, claimed.ClaimToken, now)
	require.ErrorIs(t, err, ErrOutboundCancelled)
	require.ErrorIs(t, r.CancelClaimedOutbound(ctx, a.AccountID, a.ID, claimed.ClaimToken, now), ErrOutboundClaimLost)
	after, err := r.GetOutboundAttempt(ctx, a.AccountID, a.ID)
	require.NoError(t, err)
	require.Nil(t, after.DispatchStartedAt)
	require.Equal(t, domain.OutboundStateCancelled, after.State)
	var current domain.Message
	require.NoError(t, db.First(&current, m.ID).Error)
	require.Equal(t, "changed after queueing", current.Content)
	require.Equal(t, domain.MessageStatusQueued, current.Status)
	_, err = r.ClaimNextOutbound(ctx, now.Add(2*time.Minute), time.Minute)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestOutboundRetryOnlyDefiniteRejectionAndNoBypass(t *testing.T) {
	for _, outcome := range []string{"rejected", "unknown", "accepted", "failed_callback", "sent_then_failed"} {
		t.Run(outcome, func(t *testing.T) {
			r, db, c := outboundFixture(t)
			m, a := createOutbound(t, r, c)
			a = dispatchOutbound(t, r, a)
			ctx := context.Background()
			switch outcome {
			case "rejected":
				require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "", domain.MessageStatusFailed, "rejected"))
			case "unknown":
				require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "", domain.MessageStatusUnknown, "unknown"))
			case "accepted":
				require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "SMtest", domain.MessageStatusProviderAccepted, "accepted"))
			case "failed_callback":
				require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusFailed))
			case "sent_then_failed":
				require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusSent))
				require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusFailed))
			}
			_, _, err := r.RetryMessageAccepted(ctx, c.AccountID, c.ID, m.ID)
			require.ErrorIs(t, err, ErrMessageNotRetryable)
			_, next := outboundInput(c)
			result, accepted, err := r.RetryOutboundMessage(ctx, c.AccountID, c.ID, m.ID, &next)
			if outcome != "rejected" {
				require.ErrorIs(t, err, ErrMessageNotRetryable)
				require.False(t, accepted)
				return
			}
			require.NoError(t, err)
			require.True(t, accepted)
			require.Equal(t, domain.MessageStatusQueued, result.Status)
			require.Equal(t, 2, next.Sequence)
			require.NotEqual(t, a.ID, next.ID)
			_, replay := outboundInput(c)
			_, accepted, err = r.RetryOutboundMessage(ctx, c.AccountID, c.ID, m.ID, &replay)
			require.NoError(t, err)
			require.False(t, accepted)
			require.Equal(t, next.ID, replay.ID)
			var count int64
			require.NoError(t, db.Model(&domain.OutboundMessageAttempt{}).Count(&count).Error)
			require.EqualValues(t, 2, count)
			require.EqualValues(t, 1, retryCount(t, db, "retry_attempt"))
		})
	}
}

func TestOutboundProviderLookupRequiresExactUnambiguousBinding(t *testing.T) {
	r, db, c := outboundFixture(t)
	_, a := createOutbound(t, r, c)
	a = dispatchOutbound(t, r, a)
	ctx := context.Background()
	require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusSent))
	found, err := r.FindOutboundAttemptByProvider(ctx, a.AccountID, a.Provider, a.ProviderAccountID, a.SenderID, a.RecipientID, "SMtest")
	require.NoError(t, err)
	require.Equal(t, a.ID, found.ID)
	_, err = r.FindOutboundAttemptByProvider(ctx, a.AccountID+1, a.Provider, a.ProviderAccountID, a.SenderID, a.RecipientID, "SMtest")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = r.FindOutboundAttemptByProvider(ctx, a.AccountID, a.Provider, a.ProviderAccountID, a.SenderID, a.RecipientID+"x", "SMtest")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	duplicate := *found
	duplicate.ID, duplicate.Sequence = foundation.GenerateUUID(), 2
	require.Error(t, db.Create(&duplicate).Error)
	// Retain defensive ambiguity rejection for a preexisting corrupt database.
	require.NoError(t, db.Migrator().DropIndex(&domain.OutboundMessageAttempt{}, "ux_outbound_provider_identity"))
	require.NoError(t, db.Create(&duplicate).Error)
	_, err = r.FindOutboundAttemptByProvider(ctx, a.AccountID, a.Provider, a.ProviderAccountID, a.SenderID, a.RecipientID, "SMtest")
	require.ErrorIs(t, err, ErrOutboundConflict)
}

func TestOutboundLocalFailedFirstRetryAndAtomicFailure(t *testing.T) {
	r, db, c := outboundFixture(t)
	m := retryFailedMessage(t, db, c, domain.MessageTypeOutgoing, false)
	_, a := outboundInput(c)
	a.Body = m.Content
	a.RecipientID = ""
	_, accepted, err := r.RetryOutboundMessage(context.Background(), c.AccountID, c.ID, m.ID, &a)
	require.Error(t, err)
	require.False(t, accepted)
	var current domain.Message
	require.NoError(t, db.First(&current, m.ID).Error)
	require.Equal(t, domain.MessageStatusFailed, current.Status)
	require.Zero(t, retryCount(t, db, "retry_attempt"))
	a.RecipientID = "+15005550007"
	_, accepted, err = r.RetryOutboundMessage(context.Background(), c.AccountID, c.ID, m.ID, &a)
	require.NoError(t, err)
	require.True(t, accepted)
	require.Equal(t, 1, a.Sequence)
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	for _, fact := range facts {
		raw, err := json.Marshal(fact)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(raw), m.Content))
	}
	_, err = r.GetOutboundAttempt(context.Background(), 99, a.ID)
	require.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestOutboundAuditIsBoundedAndLegacyStatusCannotPromote(t *testing.T) {
	r, db, c := outboundFixture(t)
	m, a := createOutbound(t, r, c)
	require.ErrorIs(t, r.UpdateStatus(m.ID, domain.MessageStatusDelivered), ErrOutboundConflict)
	a = dispatchOutbound(t, r, a)
	ctx := context.Background()
	for _, status := range []string{domain.MessageStatusSent, domain.MessageStatusSent, domain.MessageStatusFailed, domain.MessageStatusDelivered, domain.MessageStatusSent, domain.MessageStatusRead, domain.MessageStatusRead} {
		require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", status))
	}
	var audit []domain.LocalChangeJournal
	require.NoError(t, db.Where("source_type = ?", "msg.outbound_attempt.v1").Order("id ASC").Find(&audit).Error)
	require.Len(t, audit, 5)
	require.Equal(t, "outbound_attempt", audit[0].Action)
	for _, row := range audit {
		require.NotEqual(t, "automation_trigger", row.Action)
		require.NotEqual(t, "event_outbox", row.Action)
		for _, raw := range []string{row.BeforeState, row.AfterState} {
			if raw == "" {
				continue
			}
			var values map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &values))
			require.Len(t, values, 3)
			require.Equal(t, a.ID, values["attempt_id"])
			require.EqualValues(t, m.ID, values["message_id"])
			require.Contains(t, values, "status")
			require.NotContains(t, raw, a.Body)
			require.NotContains(t, raw, a.RecipientID)
			require.NotContains(t, raw, a.ConfigFingerprint)
		}
	}
	latest, err := r.LatestOutboundAttempt(ctx, c.AccountID, c.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MessageStatusRead, latest.Status)
	_, err = r.LatestOutboundAttempt(ctx, c.AccountID+1, c.ID, m.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = r.LatestOutboundAttempt(ctx, c.AccountID, c.ID+1, m.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	local := retryFailedMessage(t, db, c, domain.MessageTypeIncoming, false)
	require.NoError(t, r.UpdateStatus(local.ID, domain.MessageStatusSent))
}

func TestOutboundDeletedBeforeDispatchIsTerminal(t *testing.T) {
	for _, hard := range []bool{false, true} {
		r, db, c := outboundFixture(t)
		m, a := createOutbound(t, r, c)
		ctx, now := context.Background(), time.Now().UTC()
		claimed, err := r.ClaimNextOutbound(ctx, now, time.Minute)
		require.NoError(t, err)
		if hard {
			require.NoError(t, db.Delete(&domain.Message{}, m.ID).Error)
		} else {
			require.NoError(t, db.Model(&domain.Message{}).Where("id = ?", m.ID).Updates(map[string]any{"deleted": true, "content": "[deleted]"}).Error)
		}
		_, err = r.StartOutboundDispatch(ctx, a.AccountID, a.ID, claimed.ClaimToken, now)
		require.ErrorIs(t, err, ErrOutboundCancelled)
		current, err := r.GetOutboundAttempt(ctx, a.AccountID, a.ID)
		require.NoError(t, err)
		require.Equal(t, domain.OutboundStateCancelled, current.State)
		require.Nil(t, current.DispatchStartedAt)
		_, err = r.ClaimNextOutbound(ctx, now.Add(2*time.Minute), time.Minute)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		var visible domain.Message
		err = db.First(&visible, m.ID).Error
		if hard {
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		} else {
			require.NoError(t, err)
			require.True(t, visible.Deleted)
			require.Equal(t, "[deleted]", visible.Content)
			require.Equal(t, domain.MessageStatusQueued, visible.Status)
		}
	}
}

// PostgreSQL uses independent connections and isolated disposable owner schemas.
// This is opt-in and rejects any non-loopback DSN before opening a connection.
func TestPostgresOutboundClaimsAndCallbackRaces(t *testing.T) {
	if os.Getenv("EX_CHAT_AUT_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUT_POSTGRES_TESTS=1 and disposable loopback DB_DSN")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DB_DSN"))
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	t.Cleanup(func() { _ = admin.Close() })
	prefix := "outbound_test_" + strings.ReplaceAll(foundation.GenerateUUID(), "-", "")
	t.Cleanup(func() {
		for _, owner := range []string{"msg", "con"} {
			_, err := admin.Exec("DROP SCHEMA IF EXISTS " + prefix + "_" + owner + " CASCADE")
			require.NoError(t, err)
		}
	})
	open := func(owner string) *gorm.DB {
		schema := prefix + "_" + owner
		_, err := admin.Exec("CREATE SCHEMA IF NOT EXISTS " + schema)
		require.NoError(t, err)
		clone := cfg.Copy()
		clone.RuntimeParams["search_path"] = schema
		pool := stdlib.OpenDB(*clone)
		pool.SetMaxOpenConns(8)
		t.Cleanup(func() { _ = pool.Close() })
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent), IgnoreRelationshipsWhenMigrating: true})
		require.NoError(t, err)
		return db
	}
	con, msg := open("con"), open("msg")
	require.NoError(t, con.AutoMigrate(&domain.Conversation{}, &domain.LocalChangeJournal{}))
	require.NoError(t, msg.AutoMigrate(&domain.Message{}, &domain.OutboundMessageAttempt{}, &domain.LocalChangeJournal{}))
	r := NewMessageRepositoryWithConversationPort(msg, NewConversationRepository(con))
	r.SetAutomationCapture(true)
	c := domain.Conversation{AccountID: 1, ContactID: 1, InboxID: 1, DisplayID: 1, ObjectVersion: 1, Status: "open"}
	require.NoError(t, con.Create(&c).Error)
	baseM, baseA := outboundInput(c)
	const n = 12
	var wg sync.WaitGroup
	accepted, errs := make([]bool, n), make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, a := baseM, baseA
			accepted[i], errs[i] = r.CreateOutboundMessage(context.Background(), &m, &a)
		}(i)
	}
	wg.Wait()
	wins := 0
	for i, err := range errs {
		require.NoError(t, err)
		if accepted[i] {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	now := time.Now().UTC()
	claims := make([]*domain.OutboundMessageAttempt, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claims[i], errs[i] = r.ClaimNextOutbound(context.Background(), now, time.Minute)
		}(i)
	}
	wg.Wait()
	wins = 0
	var claimed *domain.OutboundMessageAttempt
	for i, err := range errs {
		if err == nil {
			wins++
			claimed = claims[i]
		} else {
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		}
	}
	require.Equal(t, 1, wins)
	started, err := r.StartOutboundDispatch(context.Background(), c.AccountID, claimed.ID, claimed.ClaimToken, now)
	require.NoError(t, err)
	statuses := []string{domain.MessageStatusSent, domain.MessageStatusRead, domain.MessageStatusFailed, domain.MessageStatusDelivered}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = r.ApplyOutboundStatus(context.Background(), *started, "SMrace", statuses[i%len(statuses)])
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	current, err := r.GetOutboundAttempt(context.Background(), c.AccountID, started.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MessageStatusRead, current.Status)
	require.False(t, current.RetrySafe)
	var visible domain.Message
	require.NoError(t, msg.First(&visible, current.MessageID).Error)
	require.Equal(t, domain.MessageStatusRead, visible.Status)
	require.False(t, msg.Migrator().HasTable(&domain.Conversation{}))
	require.False(t, con.Migrator().HasTable(&domain.Message{}))
}

func TestOutboundAuthoritativeFailureAndOldAttemptReplay(t *testing.T) {
	t.Run("provider response can authoritatively fail", func(t *testing.T) {
		r, db, c := outboundFixture(t)
		m, a := createOutbound(t, r, c)
		a = dispatchOutbound(t, r, a)
		ctx := context.Background()
		require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "SMtest", domain.MessageStatusFailed, "accepted"))
		require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "SMtest", domain.MessageStatusProviderAccepted, "accepted"))
		current, err := r.GetOutboundAttempt(ctx, a.AccountID, a.ID)
		require.NoError(t, err)
		require.Equal(t, domain.MessageStatusFailed, current.Status)
		require.True(t, current.FailureObserved)
		require.False(t, current.RetrySafe)
		var visible domain.Message
		require.NoError(t, db.First(&visible, m.ID).Error)
		require.Equal(t, domain.MessageStatusFailed, visible.Status)
		require.True(t, visible.DeliveryFailed)
	})
	t.Run("accepted failure remains terminal across late response", func(t *testing.T) {
		r, db, c := outboundFixture(t)
		m, a := createOutbound(t, r, c)
		a = dispatchOutbound(t, r, a)
		ctx := context.Background()
		require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "SMtest", domain.MessageStatusProviderAccepted, "accepted"))
		require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusFailed))
		require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "SMtest", domain.MessageStatusProviderAccepted, "accepted"))
		require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusProviderAccepted))
		current, err := r.GetOutboundAttempt(ctx, a.AccountID, a.ID)
		require.NoError(t, err)
		require.Equal(t, domain.MessageStatusFailed, current.Status)
		require.True(t, current.FailureObserved)
		require.False(t, current.RetrySafe)
		var visible domain.Message
		require.NoError(t, db.First(&visible, m.ID).Error)
		require.Equal(t, domain.MessageStatusFailed, visible.Status)
		require.True(t, visible.DeliveryFailed)
	})
	t.Run("sent failure is diagnostic and delivery supersedes", func(t *testing.T) {
		r, _, c := outboundFixture(t)
		_, a := createOutbound(t, r, c)
		a = dispatchOutbound(t, r, a)
		ctx := context.Background()
		require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusSent))
		for i := 0; i < 3; i++ {
			require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusFailed))
		}
		current, err := r.GetOutboundAttempt(ctx, a.AccountID, a.ID)
		require.NoError(t, err)
		require.Equal(t, domain.MessageStatusSent, current.Status)
		require.True(t, current.FailureObserved)
		var visible domain.Message
		require.NoError(t, r.db.First(&visible, current.MessageID).Error)
		require.Equal(t, domain.MessageStatusSent, visible.Status)
		require.True(t, visible.DeliveryFailed)
		require.EqualValues(t, 1, retryCount(t, r.db, "outbound_failure"))
		require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusDelivered))
		require.NoError(t, r.ApplyOutboundStatus(ctx, a, "SMtest", domain.MessageStatusFailed))
		current, err = r.GetOutboundAttempt(ctx, a.AccountID, a.ID)
		require.NoError(t, err)
		require.Equal(t, domain.MessageStatusDelivered, current.Status)
		require.False(t, current.FailureObserved)
		require.NoError(t, r.db.First(&visible, current.MessageID).Error)
		require.Equal(t, domain.MessageStatusDelivered, visible.Status)
		require.False(t, visible.DeliveryFailed)
		require.EqualValues(t, 1, retryCount(t, r.db, "outbound_failure"))
	})
	t.Run("old rejection does not overwrite new retry", func(t *testing.T) {
		r, db, c := outboundFixture(t)
		m, a := createOutbound(t, r, c)
		a = dispatchOutbound(t, r, a)
		ctx := context.Background()
		require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "", domain.MessageStatusFailed, "rejected"))
		_, next := outboundInput(c)
		_, accepted, err := r.RetryOutboundMessage(ctx, c.AccountID, c.ID, m.ID, &next)
		require.NoError(t, err)
		require.True(t, accepted)
		require.NoError(t, r.CompleteOutboundDispatch(ctx, a.AccountID, a.ID, a.ClaimToken, "", domain.MessageStatusFailed, "rejected"))
		var visible domain.Message
		require.NoError(t, db.First(&visible, m.ID).Error)
		require.Equal(t, domain.MessageStatusQueued, visible.Status)
		latest, err := r.LatestOutboundAttempt(ctx, c.AccountID, c.ID, m.ID)
		require.NoError(t, err)
		require.Equal(t, next.ID, latest.ID)
		require.Equal(t, domain.OutboundStateQueued, latest.State)
	})
}

func TestOutboundProviderIdentityCannotBindAnotherAttempt(t *testing.T) {
	r, db, c := outboundFixture(t)
	m1, a1 := createOutbound(t, r, c)
	a1 = dispatchOutbound(t, r, a1)
	m2, a2 := outboundInput(c)
	a2.RecipientID = "+15005550008" // Recipient must not weaken provider ID uniqueness.
	created, err := r.CreateOutboundMessage(context.Background(), &m2, &a2)
	require.NoError(t, err)
	require.True(t, created)
	a2 = dispatchOutbound(t, r, a2)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	attempts := []domain.OutboundMessageAttempt{a1, a2}
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = r.ApplyOutboundStatus(context.Background(), attempts[i], "SMunique", domain.MessageStatusSent)
		}(i)
	}
	wg.Wait()
	wins := 0
	for i, err := range errs {
		current, readErr := r.GetOutboundAttempt(context.Background(), c.AccountID, attempts[i].ID)
		require.NoError(t, readErr)
		if err == nil {
			wins++
			require.Equal(t, "SMunique", current.ProviderMessageID)
			require.Equal(t, domain.MessageStatusSent, current.Status)
		} else {
			require.ErrorIs(t, err, ErrOutboundConflict)
			require.Empty(t, current.ProviderMessageID)
			require.Equal(t, domain.MessageStatusQueued, current.Status)
		}
	}
	require.Equal(t, 1, wins)
	var statuses []string
	require.NoError(t, db.Model(&domain.Message{}).Where("id IN ?", []uint{m1.ID, m2.ID}).Pluck("status", &statuses).Error)
	require.ElementsMatch(t, []string{domain.MessageStatusQueued, domain.MessageStatusSent}, statuses)
	require.EqualValues(t, 1, retryCount(t, db, "outbound_status"))
}
