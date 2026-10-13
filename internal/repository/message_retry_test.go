package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type retryTestPort struct {
	*ConversationRepository
	fail         bool
	loseResponse bool
	beforeApply  func(RetryActivityIntent)
}

func (p *retryTestPort) ApplyRetryActivity(ctx context.Context, i RetryActivityIntent) (RetryActivityReceipt, error) {
	if p.beforeApply != nil {
		p.beforeApply(i)
	}
	if p.fail {
		return RetryActivityReceipt{}, errors.New("scripted CON outage")
	}
	r, err := p.ConversationRepository.ApplyRetryActivity(ctx, i)
	if err == nil && p.loseResponse {
		return RetryActivityReceipt{}, errors.New("scripted lost CON response")
	}
	return r, err
}
func retryFixture(t *testing.T, status string) (*MessageRepository, *retryTestPort, *gorm.DB, *gorm.DB, domain.Conversation) {
	t.Helper()
	con, msg := paymentDB(t, "retry-con"), paymentDB(t, "retry-msg")
	require.NoError(t, database.AutoMigrateModule(con, "CON"))
	require.NoError(t, database.AutoMigrateModule(msg, "MSG"))
	c := domain.Conversation{AccountID: 1, ContactID: 1, InboxID: 1, DisplayID: 1, Status: status, Priority: "medium", ObjectVersion: 7, UnreadCount: 3, LastActivityAt: time.Now().UTC().Add(-time.Hour), Muted: true}
	require.NoError(t, con.Create(&c).Error)
	p := &retryTestPort{ConversationRepository: NewConversationRepository(con)}
	r := NewMessageRepositoryWithConversationPort(msg, p)
	r.SetAutomationCapture(true)
	return r, p, con, msg, c
}
func retryFailedMessage(t *testing.T, db *gorm.DB, c domain.Conversation, kind string, private bool) domain.Message {
	t.Helper()
	m := domain.Message{AccountID: c.AccountID, ConversationID: c.ID, MessageType: kind, Private: private, Status: domain.MessageStatusFailed, SenderType: domain.SenderTypeUser, SenderID: 17, ContentType: domain.ContentTypeText, Content: "private body sentinel", CreatedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)}
	require.NoError(t, db.Create(&m).Error)
	return m
}
func retryCount(t *testing.T, db *gorm.DB, action string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action = ?", action).Count(&n).Error)
	return n
}
func TestExplicitRetryTruthfulTypeMatrixAndOwnerIsolation(t *testing.T) {
	for _, kind := range []string{domain.MessageTypeIncoming, domain.MessageTypeOutgoing, domain.MessageTypeActivity} {
		for _, private := range []bool{false, true} {
			for _, status := range []string{"resolved", "open", "pending", "snoozed"} {
				t.Run(fmt.Sprintf("%s/private=%v/%s", kind, private, status), func(t *testing.T) {
					r, _, con, msg, c := retryFixture(t, status)
					m := retryFailedMessage(t, msg, c, kind, private)
					result, accepted, err := r.RetryMessageAccepted(context.Background(), c.AccountID, c.ID, m.ID)
					require.NoError(t, err)
					require.True(t, accepted)
					require.Equal(t, retryLocalOutcome(kind, private), result.Status)
					require.Equal(t, m.ID, result.ID)
					require.Equal(t, m.Content, result.Content)
					require.True(t, m.CreatedAt.Equal(result.CreatedAt))
					var stored domain.Message
					require.NoError(t, msg.First(&stored, m.ID).Error)
					require.Equal(t, result.Status, stored.Status)
					var after domain.Conversation
					require.NoError(t, con.First(&after, c.ID).Error)
					require.Equal(t, 8, after.ObjectVersion)
					require.Equal(t, 3, after.UnreadCount)
					require.True(t, after.LastActivityAt.After(c.LastActivityAt))
					reopened := kind == domain.MessageTypeIncoming && status == "resolved"
					if reopened {
						require.Equal(t, "open", after.Status)
					} else {
						require.Equal(t, status, after.Status)
					}
					facts, err := r.PendingAutomationFacts(context.Background(), 100)
					require.NoError(t, err)
					require.Len(t, facts, 1)
					f := facts[0]
					require.Equal(t, "msg.message_status.changed.v1", f.EventType)
					require.Equal(t, true, f.Payload["retry_receipt_verified"])
					require.Equal(t, status, f.Payload["retry_observed_status"])
					require.Equal(t, result.Status, f.Payload["local_outcome"])
					require.Equal(t, 8, f.Payload["object_version"])
					if reopened {
						require.Equal(t, "resolved", f.Payload["status_before"])
						require.Equal(t, "open", f.Payload["status_after"])
					} else {
						require.NotContains(t, f.Payload, "status_before")
					}
					require.EqualValues(t, 1, retryCount(t, msg, "retry_attempt"))
					require.EqualValues(t, 1, retryCount(t, con, "retry_activity"))
					require.Zero(t, retryCount(t, con, "automation_trigger"))
					require.False(t, msg.Migrator().HasTable(&domain.Conversation{}))
					require.False(t, con.Migrator().HasTable(&domain.Message{}))
					require.False(t, msg.Migrator().HasTable(&domain.AutomationRuleExecution{}))
					var participants int64
					require.NoError(t, con.Model(&domain.ConversationParticipant{}).Count(&participants).Error)
					require.Zero(t, participants)
					var journals []domain.LocalChangeJournal
					require.NoError(t, msg.Find(&journals).Error)
					for _, j := range journals {
						require.NotContains(t, j.AfterState, m.Content)
					}
					again, accepted, err := r.RetryMessageAccepted(context.Background(), c.AccountID, c.ID, m.ID)
					require.NoError(t, err)
					require.False(t, accepted)
					require.Equal(t, result.Status, again.Status)
					require.EqualValues(t, 1, retryCount(t, con, "retry_activity"))
				})
			}
		}
	}
}
func TestExplicitRetryLegacyNoJournalAndTruthfulStatus(t *testing.T) {
	db := paymentDB(t, "retry-legacy")
	require.NoError(t, db.AutoMigrate(&domain.Message{}))
	r := NewMessageRepositoryWithConversationPort(db, nil)
	for _, kind := range []string{domain.MessageTypeIncoming, domain.MessageTypeOutgoing, domain.MessageTypeActivity} {
		for _, private := range []bool{false, true} {
			m := retryFailedMessage(t, db, domain.Conversation{AccountID: 1, ID: 1}, kind, private)
			result, accepted, err := r.RetryMessageAccepted(context.Background(), 1, 1, m.ID)
			require.NoError(t, err)
			require.True(t, accepted)
			require.Equal(t, retryLocalOutcome(kind, private), result.Status)
			_, _, err = r.RetryMessageAccepted(context.Background(), 1, 1, m.ID)
			require.Error(t, err)
		}
	}
	require.False(t, db.Migrator().HasTable(&domain.LocalChangeJournal{}))
}
func TestExplicitRetryOutageResponseLossRestartAndLaterAttempt(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			r, p, con, msg, c := retryFixture(t, "resolved")
			m := retryFailedMessage(t, msg, c, domain.MessageTypeIncoming, false)
			p.fail, p.loseResponse = !lost, lost
			result, accepted, err := r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
			require.NoError(t, err)
			require.True(t, accepted)
			require.Equal(t, "saved", result.Status)
			var attempt domain.LocalChangeJournal
			require.NoError(t, msg.Where("action = ?", "retry_attempt").First(&attempt).Error)
			require.Equal(t, "pending", attempt.Status)
			_, accepted, err = r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
			require.NoError(t, err)
			require.False(t, accepted)
			p.fail, p.loseResponse = false, false
			restarted := NewMessageRepositoryWithConversationPort(msg, p)
			restarted.SetAutomationCapture(true)
			facts, err := restarted.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Len(t, facts, 1)
			twice, err := restarted.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, facts, twice)
			require.EqualValues(t, 1, retryCount(t, con, "retry_activity"))
			require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), facts[0].EventID))
			require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), facts[0].EventID))
			facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Empty(t, facts)
			require.NoError(t, msg.Model(&domain.Message{}).Where("id = ?", m.ID).Update("status", domain.MessageStatusFailed).Error)
			_, accepted, err = restarted.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
			require.NoError(t, err)
			require.True(t, accepted)
			facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Len(t, facts, 1)
			require.EqualValues(t, 2, facts[0].AggregateVersion)
			require.NotEqual(t, twice[0].CausationID, facts[0].CausationID)
			require.EqualValues(t, 2, retryCount(t, con, "retry_activity"))
			require.EqualValues(t, 2, retryCount(t, msg, "retry_attempt"))
		})
	}
}
func TestExplicitRetryManualInterleaveNeverRebases(t *testing.T) {
	for _, mutation := range []string{"resolution", "deadline", "deleting"} {
		t.Run(mutation, func(t *testing.T) {
			r, p, con, msg, c := retryFixture(t, "resolved")
			m := retryFailedMessage(t, msg, c, domain.MessageTypeIncoming, false)
			p.fail = true
			_, _, err := r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
			require.NoError(t, err)
			update := map[string]any{"object_version": 8, "status": "resolved"}
			if mutation == "deadline" {
				update["status"] = "snoozed"
				update["snoozed_until"] = time.Now().Add(time.Hour)
			}
			if mutation == "deleting" {
				update["status"] = "deleting"
			}
			require.NoError(t, con.Model(&domain.Conversation{}).Where("id = ?", c.ID).Updates(update).Error)
			p.fail = false
			facts, err := r.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			if mutation == "deleting" {
				require.Empty(t, facts)
				require.Zero(t, retryCount(t, con, "retry_activity"))
			} else {
				require.Len(t, facts, 1)
				require.Equal(t, 7, facts[0].Payload["object_version"])
				require.Equal(t, "stale", facts[0].Payload["retry_activity_result"])
				require.NotContains(t, facts[0].Payload, "status_before")
			}
			var after domain.Conversation
			require.NoError(t, con.First(&after, c.ID).Error)
			require.Equal(t, 8, after.ObjectVersion)
			require.Equal(t, update["status"], after.Status)
			require.True(t, after.LastActivityAt.Equal(c.LastActivityAt))
		})
	}
}
func TestExplicitRetryAtomicRollbackAndReceiptRecovery(t *testing.T) {
	for _, failure := range []string{"attempt", "fact", "con_receipt"} {
		t.Run(failure, func(t *testing.T) {
			r, p, con, msg, c := retryFixture(t, "resolved")
			m := retryFailedMessage(t, msg, c, domain.MessageTypeIncoming, false)
			target, action := msg, "retry_attempt"
			if failure == "fact" {
				action = "automation_trigger"
			}
			if failure == "con_receipt" {
				target, action = con, "retry_activity"
			}
			require.NoError(t, target.Exec("CREATE TRIGGER fail_retry BEFORE INSERT ON local_change_journals WHEN NEW.action = '"+action+"' BEGIN SELECT RAISE(ABORT, 'scripted failure'); END").Error)
			_, accepted, err := r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
			var stored domain.Message
			require.NoError(t, msg.First(&stored, m.ID).Error)
			if failure == "con_receipt" {
				require.NoError(t, err)
				require.True(t, accepted)
				require.Equal(t, "saved", stored.Status)
			} else {
				require.Error(t, err)
				require.False(t, accepted)
				require.Equal(t, "failed", stored.Status)
				require.Zero(t, retryCount(t, msg, "retry_attempt"))
				require.Zero(t, retryCount(t, msg, "automation_trigger"))
			}
			var unchanged domain.Conversation
			require.NoError(t, con.First(&unchanged, c.ID).Error)
			require.Equal(t, 7, unchanged.ObjectVersion)
			require.Equal(t, "resolved", unchanged.Status)
			require.NoError(t, target.Exec("DROP TRIGGER fail_retry").Error)
			if failure != "con_receipt" {
				_, _, err = r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
				require.NoError(t, err)
			}
			facts, err := r.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Len(t, facts, 1)
			require.EqualValues(t, 1, retryCount(t, p.db, "retry_activity"))
		})
	}
}
func TestExplicitRetryDeletionCancellationAndMalformedProof(t *testing.T) {
	for _, kind := range []string{"message_deleted", "message_missing", "wrong_tenant", "tamper_intent", "tamper_receipt", "tamper_profile", "tamper_fact"} {
		t.Run(kind, func(t *testing.T) {
			r, p, con, msg, c := retryFixture(t, "resolved")
			m := retryFailedMessage(t, msg, c, domain.MessageTypeIncoming, true)
			p.fail = kind != "tamper_receipt"
			_, _, err := r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
			require.NoError(t, err)
			switch kind {
			case "message_deleted":
				require.NoError(t, msg.Model(&m).Update("deleted", true).Error)
			case "message_missing":
				require.NoError(t, msg.Delete(&m).Error)
			case "wrong_tenant":
				require.NoError(t, msg.Model(&m).Update("account_id", 2).Error)
			case "tamper_intent":
				require.NoError(t, msg.Model(&domain.LocalChangeJournal{}).Where("action = ?", "retry_attempt").Update("object_version_after", 12).Error)
			case "tamper_receipt":
				require.NoError(t, con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "retry_activity").Update("object_version_after", 100).Error)
			default:
				var row domain.LocalChangeJournal
				require.NoError(t, msg.Where("action = ?", "automation_trigger").First(&row).Error)
				var f foundation.EventEnvelope
				require.NoError(t, json.Unmarshal([]byte(row.AfterState), &f))
				if kind == "tamper_profile" {
					f.Payload["retry_profile"] = "unverified"
				} else {
					f.Payload["private"] = false
				}
				raw, _ := json.Marshal(f)
				require.NoError(t, msg.Model(&row).Update("after_state", string(raw)).Error)
			}
			p.fail = false
			facts, err := r.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Empty(t, facts)
			var fact domain.LocalChangeJournal
			require.NoError(t, msg.Where("action = ?", "automation_trigger").First(&fact).Error)
			require.Equal(t, "manual_action_required", fact.Status)
			if strings.HasPrefix(kind, "message_") || kind == "wrong_tenant" {
				var attempt domain.LocalChangeJournal
				require.NoError(t, msg.Where("action = ?", "retry_attempt").First(&attempt).Error)
				require.Equal(t, "cancelled", attempt.Status)
			}
			if kind != "tamper_receipt" {
				require.Zero(t, retryCount(t, con, "retry_activity"))
			}
		})
	}
}
func TestExplicitRetryOwnerOutageDoesNotStarvePagedFacts(t *testing.T) {
	r, p, _, msg, c := retryFixture(t, "open")
	p.fail = true
	for i := 0; i < 105; i++ {
		m := retryFailedMessage(t, msg, c, "outgoing", false)
		_, _, err := r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
		require.NoError(t, err)
	}
	// A later unrelated ordinary family is ready even though every retry waits.
	p.fail = false
	m := domain.Message{AccountID: 1, ConversationID: c.ID, MessageType: "incoming", Status: "sent", Content: "ordinary", ContentType: "text", SenderType: "Contact", SenderID: 1, CreatedAt: time.Now().UTC()}
	require.NoError(t, r.Create(&m))
	p.fail = true
	facts, next, err := r.PendingAutomationFactPage(context.Background(), 100, "")
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, facts, 1)
	require.Equal(t, "msg.message.created.v1", facts[0].EventType)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = r.PendingAutomationFactPage(ctx, 100, "")
	require.ErrorIs(t, err, context.Canceled)
}

func TestExplicitRetryCancellationDoesNotRevertCommit(t *testing.T) {
	r, p, con, msg, c := retryFixture(t, "open")
	m := retryFailedMessage(t, msg, c, "outgoing", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := r.RetryMessageAccepted(ctx, 1, c.ID, m.ID)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, retryCount(t, msg, "retry_attempt"))
	ctx, cancel = context.WithCancel(context.Background())
	p.beforeApply = func(RetryActivityIntent) { cancel() }
	result, accepted, err := r.RetryMessageAccepted(ctx, 1, c.ID, m.ID)
	require.NoError(t, err)
	require.True(t, accepted)
	require.Equal(t, "queued", result.Status)
	require.Zero(t, retryCount(t, con, "retry_activity"))
	p.beforeApply = nil
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.EqualValues(t, 1, retryCount(t, con, "retry_activity"))
}
func TestExplicitRetryHistoricalAndProviderStatusExclusions(t *testing.T) {
	r, _, _, msg, c := retryFixture(t, "open")
	m := domain.Message{AccountID: 1, ConversationID: c.ID, MessageType: "outgoing", Private: true, Status: "failed", Content: "historical import", ContentType: "text", SenderType: "User", SenderID: 1, CreatedAt: time.Now().UTC().Add(-time.Hour)}
	require.NoError(t, r.CreateImported(&m))
	event := foundation.EventEnvelope{EventID: foundation.GenerateUUID(), EventType: "msg.message_status.changed.v1", EventVersion: "v1", SourceModule: "MSG", AccountID: 1, AggregateType: "Message", AggregateID: fmt.Sprint(m.ID), AggregateVersion: 1, OccurredAt: time.Now().UTC(), PublishedAt: time.Now().UTC(), Actor: foundation.ContractActor{Type: "System", ID: "provider", AuthSource: "module"}, CorrelationID: "provider", CausationID: "provider", ChangedFields: []string{"status"}, Payload: map[string]any{"message_id": m.ID, "conversation_id": c.ID, "status": "sent", "operation": "retry"}, DataClassification: "internal"}
	raw, _ := json.Marshal(event)
	require.NoError(t, NewJournalRepository(msg).RecordLocalChange(msg, &domain.LocalChangeJournal{ChangeID: event.EventID, EventID: event.EventID, SourceModule: "MSG", AccountID: 1, ObjectType: "Message", ObjectID: m.ID, ObjectVersionAfter: 1, Action: "event_outbox", AfterState: string(raw)}))
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
	require.Zero(t, retryCount(t, msg, "retry_attempt"))
	require.Zero(t, retryCount(t, msg, "automation_trigger"))
}
func TestExplicitRetryAcknowledgementWaitsForOwnerProof(t *testing.T) {
	r, p, _, msg, c := retryFixture(t, "open")
	p.fail = true
	m := retryFailedMessage(t, msg, c, "outgoing", false)
	_, _, err := r.RetryMessageAccepted(context.Background(), 1, c.ID, m.ID)
	require.NoError(t, err)
	var row domain.LocalChangeJournal
	require.NoError(t, msg.Where("action = ?", "automation_trigger").First(&row).Error)
	require.ErrorIs(t, r.MarkAutomationFactDelivered(context.Background(), row.EventID), gorm.ErrInvalidData)
	require.Zero(t, retryCount(t, msg, "automation_delivery"))
	p.fail = false
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.NoError(t, r.MarkAutomationFactDelivered(context.Background(), row.EventID))
}
