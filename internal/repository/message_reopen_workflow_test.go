package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Keep the CON store behind a port, including when delivery is retried by a
// fresh MSG repository. A workflow must not silently fall back to plain activity.
type reopenWorkflowActivity struct {
	*automationDeferredActivity
	reopenCalls int
	legacyCalls int
}

func (p *reopenWorkflowActivity) ApplyMessageActivity(a, c, u uint, incoming bool, at time.Time, key string) error {
	p.legacyCalls++
	return p.automationDeferredActivity.ApplyMessageActivity(a, c, u, incoming, at, key)
}

func (p *reopenWorkflowActivity) ApplyMessageActivityWithReopen(a, c, u uint, incoming bool, at time.Time, key string, expectedVersion int, reopen bool) error {
	p.reopenCalls++
	if p.fail {
		return errors.New("scripted reopen owner outage")
	}
	return p.owner.ApplyMessageActivityWithReopen(a, c, u, incoming, at, key, expectedVersion, reopen)
}

type reopenWorkflowContactOwner struct {
	contact domain.Contact
	fail    bool
	calls   int
}

func (p *reopenWorkflowContactOwner) Exists(a, id uint) (bool, error) {
	return p.contact.AccountID == a && p.contact.ID == id, nil
}
func (p *reopenWorkflowContactOwner) FindSummaryByID(a, id uint) (*domain.Contact, error) {
	p.calls++
	if p.fail {
		return nil, errors.New("scripted CUS outage")
	}
	copy := p.contact
	return &copy, nil
}
func reopenWorkflowOwner(con *gorm.DB, c domain.Conversation) *ConversationRepository {
	owner := NewConversationRepository(con)
	owner.contacts = &reopenWorkflowContactOwner{contact: domain.Contact{ID: c.ContactID, AccountID: c.AccountID}}
	return owner
}

func reopenWorkflowFixture(t *testing.T) (*MessageRepository, *reopenWorkflowActivity, *gorm.DB, *gorm.DB, domain.Conversation) {
	t.Helper()
	_, deferred, con, msg, c := automationMessageFixture(t)
	c.Status = domain.ConversationStatusResolved
	require.NoError(t, con.Model(&domain.Conversation{}).Where("id = ?", c.ID).Update("status", c.Status).Error)
	deferred.owner = reopenWorkflowOwner(con, c)
	p := &reopenWorkflowActivity{automationDeferredActivity: deferred}
	r := NewMessageRepositoryWithConversationPort(msg, p)
	r.SetAutomationCapture(true)
	return r, p, con, msg, c
}

func reopenWorkflowMessage(c domain.Conversation) domain.Message {
	return domain.Message{AccountID: c.AccountID, ConversationID: c.ID, SenderType: domain.SenderTypeContact, SenderID: c.ContactID, MessageType: domain.MessageTypeIncoming, Content: "inbound reopen workflow", ContentType: domain.ContentTypeText, Status: domain.MessageStatusSent}
}

func reopenWorkflowConversation(t *testing.T, con *gorm.DB, c domain.Conversation) domain.Conversation {
	t.Helper()
	var stored domain.Conversation
	require.NoError(t, con.Where("account_id = ? AND id = ?", c.AccountID, c.ID).First(&stored).Error)
	return stored
}

func reopenWorkflowCreate(t *testing.T, msgDB *gorm.DB, msg domain.Message) domain.LocalChangeJournal {
	t.Helper()
	var row domain.LocalChangeJournal
	require.NoError(t, msgDB.Where("source_module = ? AND account_id = ? AND object_id = ? AND action = ?", "MSG", msg.AccountID, msg.ID, "create").First(&row).Error)
	return row
}

func reopenWorkflowReceipt(t *testing.T, con *gorm.DB, c domain.Conversation, key string) domain.LocalChangeJournal {
	t.Helper()
	var rows []domain.LocalChangeJournal
	require.NoError(t, con.Where("source_module = ? AND account_id = ? AND object_id = ? AND command_id = ? AND source_type = ?", "CON", c.AccountID, c.ID, key, "msg.message.created.v1").Find(&rows).Error)
	require.Len(t, rows, 1, "one stable CON receipt must own the entire activity and reopen")
	return rows[0]
}

func reopenWorkflowJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var state map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &state))
	return state
}

func reopenWorkflowFactVersion(t *testing.T, r *MessageRepository, messageID uint, key string) uint {
	t.Helper()
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	id, err := messageAutomationUint(facts[0].Payload["message_id"])
	require.NoError(t, err)
	require.Equal(t, messageID, id)
	require.Equal(t, key, facts[0].CausationID)
	version, err := messageAutomationUint(facts[0].Payload["object_version"])
	require.NoError(t, err)
	return version
}

func TestMessageReopenWorkflowCapturesAtomicOwnerTransition(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	require.True(t, r.AutomationCaptureEnabled())
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
	stored := reopenWorkflowConversation(t, con, c)
	require.Equal(t, domain.ConversationStatusOpen, stored.Status)
	require.Equal(t, 1, stored.UnreadCount)
	require.Equal(t, c.ObjectVersion+1, stored.ObjectVersion, "reopen and activity share one owner version")
	require.True(t, stored.LastActivityAt.Equal(msg.CreatedAt))
	require.Equal(t, 1, p.reopenCalls)
	require.Zero(t, p.legacyCalls)
	create := reopenWorkflowCreate(t, msgDB, msg)
	require.Equal(t, "delivered", create.Status)
	intent := reopenWorkflowJSON(t, create.AfterState)
	require.EqualValues(t, c.ObjectVersion, intent["expected_version"])
	require.Equal(t, true, intent["reopen"])
	receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
	require.Equal(t, c.ObjectVersion, receipt.ObjectVersionBefore)
	require.Equal(t, stored.ObjectVersion, receipt.ObjectVersionAfter)
	require.Equal(t, "message_reopened", receipt.Reason)
	require.Contains(t, receipt.ChangedFields, "status")
	require.EqualValues(t, stored.ObjectVersion, reopenWorkflowJSON(t, receipt.AfterState)["automation_target_version"])
	require.EqualValues(t, stored.ObjectVersion, reopenWorkflowFactVersion(t, r, msg.ID, create.ChangeID))
}

func TestMessageReopenWorkflowReplayPreservesReceiptAfterManualEdit(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
	create := reopenWorkflowCreate(t, msgDB, msg)
	receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
	require.NoError(t, p.owner.UpdateStatus(c.AccountID, c.ID, domain.ConversationStatusResolved, nil))
	manual := reopenWorkflowConversation(t, con, c)
	require.Equal(t, receipt.ObjectVersionAfter+1, manual.ObjectVersion)
	// Simulate a crash after CON committed but before MSG durably acknowledged.
	require.NoError(t, msgDB.Model(&domain.LocalChangeJournal{}).Where("id = ?", create.ID).Update("status", "pending").Error)
	restartedPort := &reopenWorkflowActivity{automationDeferredActivity: &automationDeferredActivity{owner: reopenWorkflowOwner(con, c)}}
	restarted := NewMessageRepositoryWithConversationPort(msgDB, restartedPort)
	restarted.SetAutomationCapture(true)
	n, err := restarted.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, manual, reopenWorkflowConversation(t, con, c), "duplicate delivery must not undo a later resolution or increment unread")
	require.Equal(t, receipt, reopenWorkflowReceipt(t, con, c, create.ChangeID))
	require.Equal(t, 1, restartedPort.reopenCalls)
	require.Zero(t, restartedPort.legacyCalls)
	require.EqualValues(t, receipt.ObjectVersionAfter, reopenWorkflowFactVersion(t, restarted, msg.ID, create.ChangeID))
	n, err = restarted.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestMessageReopenWorkflowSurvivesOwnerOutageAndRestart(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	p.fail = true
	before := reopenWorkflowConversation(t, con, c)
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
	require.NotZero(t, msg.ID)
	require.Equal(t, before, reopenWorkflowConversation(t, con, c))
	create := reopenWorkflowCreate(t, msgDB, msg)
	require.Equal(t, "pending", create.Status)
	intent := reopenWorkflowJSON(t, create.AfterState)
	require.EqualValues(t, c.ObjectVersion, intent["expected_version"])
	require.Equal(t, true, intent["reopen"])
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
	var trigger domain.LocalChangeJournal
	require.NoError(t, msgDB.Where("source_module = ? AND object_id = ? AND action = ?", "MSG", msg.ID, "automation_trigger").First(&trigger).Error)
	restartedPort := &reopenWorkflowActivity{automationDeferredActivity: &automationDeferredActivity{owner: reopenWorkflowOwner(con, c)}}
	restarted := NewMessageRepositoryWithConversationPort(msgDB, restartedPort)
	restarted.SetAutomationCapture(true)
	n, err := restarted.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	stored := reopenWorkflowConversation(t, con, c)
	require.Equal(t, domain.ConversationStatusOpen, stored.Status)
	require.Equal(t, c.ObjectVersion+1, stored.ObjectVersion)
	require.Equal(t, 1, stored.UnreadCount)
	require.Equal(t, 1, restartedPort.reopenCalls)
	require.Zero(t, restartedPort.legacyCalls)
	delivered := reopenWorkflowCreate(t, msgDB, msg)
	require.Equal(t, "delivered", delivered.Status)
	require.Equal(t, create.AfterState, delivered.AfterState, "replay must retain the original intent")
	facts, err = restarted.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, trigger.EventID, facts[0].EventID)
	require.EqualValues(t, stored.ObjectVersion, reopenWorkflowFactVersion(t, restarted, msg.ID, create.ChangeID))
	reopenWorkflowReceipt(t, con, c, create.ChangeID)
}

func TestMessageReopenWorkflowInterveningVersionVetoesOriginalTarget(t *testing.T) {
	for _, reopen := range []bool{true, false} {
		name := "reopen_requested"
		if !reopen {
			name = "blocked_contact_no_reopen"
		}
		t.Run(name, func(t *testing.T) {
			r, p, con, msgDB, c := reopenWorkflowFixture(t)
			p.fail = true
			msg := reopenWorkflowMessage(c)
			require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, reopen))
			create := reopenWorkflowCreate(t, msgDB, msg)
			require.NoError(t, p.owner.UpdatePriority(c.AccountID, c.ID, "high"))
			manual := reopenWorkflowConversation(t, con, c)
			require.Equal(t, c.ObjectVersion+1, manual.ObjectVersion)
			p.fail = false
			n, err := r.ReconcilePendingActivity(100)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			stored := reopenWorkflowConversation(t, con, c)
			require.Equal(t, domain.ConversationStatusResolved, stored.Status)
			require.Equal(t, "high", stored.Priority)
			require.Equal(t, manual.ObjectVersion+1, stored.ObjectVersion)
			require.Equal(t, 1, stored.UnreadCount)
			require.True(t, stored.LastActivityAt.Equal(manual.LastActivityAt), "an older message must not regress activity time")
			receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
			require.Equal(t, "reopen_version_stale", receipt.Reason)
			require.Equal(t, stored.ObjectVersion, receipt.ObjectVersionAfter)
			require.EqualValues(t, c.ObjectVersion, reopenWorkflowJSON(t, receipt.AfterState)["automation_target_version"])
			version, err := p.owner.AutomationMessageActivityVersion(c.AccountID, c.ID, create.ChangeID)
			require.NoError(t, err)
			require.Equal(t, c.ObjectVersion, version, "AUT must use the rejected snapshot, never the newer owner's version")
			require.EqualValues(t, c.ObjectVersion, reopenWorkflowFactVersion(t, r, msg.ID, create.ChangeID))
			require.NoError(t, p.owner.UpdatePriority(c.AccountID, c.ID, "urgent"))
			version, err = p.owner.AutomationMessageActivityVersion(c.AccountID, c.ID, create.ChangeID)
			require.NoError(t, err)
			require.Equal(t, c.ObjectVersion, version)
		})
	}
}

func TestMessageReopenWorkflowRespectsMutedBlockedAndUnresolvedStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		muted  bool
		reopen bool
	}{
		{name: "muted", status: domain.ConversationStatusResolved, muted: true, reopen: true},
		{name: "blocked_contact", status: domain.ConversationStatusResolved, reopen: false},
		{name: "already_open", status: domain.ConversationStatusOpen, reopen: true},
		{name: "pending", status: "pending", reopen: true},
		{name: "snoozed", status: "snoozed", reopen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, con, msgDB, c := reopenWorkflowFixture(t)
			require.NoError(t, con.Model(&domain.Conversation{}).Where("id = ?", c.ID).Updates(map[string]any{"status": tc.status, "muted": tc.muted}).Error)
			msg := reopenWorkflowMessage(c)
			require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, tc.reopen))
			stored := reopenWorkflowConversation(t, con, c)
			require.Equal(t, tc.status, stored.Status)
			require.Equal(t, tc.muted, stored.Muted)
			require.Equal(t, c.ObjectVersion+1, stored.ObjectVersion)
			require.Equal(t, 1, stored.UnreadCount)
			create := reopenWorkflowCreate(t, msgDB, msg)
			receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
			require.NotEqual(t, "message_reopened", receipt.Reason)
			require.Equal(t, tc.reopen, reopenWorkflowJSON(t, create.AfterState)["reopen"])
			require.EqualValues(t, stored.ObjectVersion, reopenWorkflowFactVersion(t, r, msg.ID, create.ChangeID))
		})
	}
}

func TestMessageReopenWorkflowRejectsCrossTenantCreateAndReceiptReplay(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	wrong := reopenWorkflowMessage(c)
	wrong.AccountID++
	require.ErrorIs(t, r.CreateWithConversationReopen(&wrong, c.ObjectVersion, true), gorm.ErrRecordNotFound)
	var count int64
	require.NoError(t, msgDB.Model(&domain.Message{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, msgDB.Model(&domain.LocalChangeJournal{}).Count(&count).Error)
	require.Zero(t, count)
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
	create := reopenWorkflowCreate(t, msgDB, msg)
	receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
	original := reopenWorkflowConversation(t, con, c)
	for _, account := range []uint{c.AccountID, c.AccountID + 1} {
		other := domain.Conversation{AccountID: account, InboxID: c.InboxID, ContactID: c.ContactID, DisplayID: 2, Status: domain.ConversationStatusResolved, ObjectVersion: 1}
		require.NoError(t, con.Create(&other).Error)
		before := reopenWorkflowConversation(t, con, other)
		require.ErrorIs(t, p.owner.ApplyMessageActivityWithReopen(account, other.ID, msg.SenderID, true, msg.CreatedAt, create.ChangeID, other.ObjectVersion, true), gorm.ErrInvalidData)
		require.Equal(t, before, reopenWorkflowConversation(t, con, other))
		_, err := p.owner.AutomationMessageActivityVersion(account, other.ID, create.ChangeID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
	require.ErrorIs(t, p.owner.ApplyMessageActivityWithReopen(c.AccountID+1, c.ID, msg.SenderID, true, msg.CreatedAt, create.ChangeID, c.ObjectVersion, true), gorm.ErrInvalidData)
	require.Equal(t, original, reopenWorkflowConversation(t, con, c))
	require.Equal(t, receipt, reopenWorkflowReceipt(t, con, c, create.ChangeID))
}

func TestMessageReopenWorkflowRejectsFutureOwnerVersion(t *testing.T) {
	_, p, con, _, c := reopenWorkflowFixture(t)
	before := reopenWorkflowConversation(t, con, c)
	key, occurredAt := "future-reopen-version", time.Now().UTC()
	for _, reopen := range []bool{true, false} {
		require.ErrorIs(t, p.owner.ApplyMessageActivityWithReopen(c.AccountID, c.ID, c.ContactID, true, occurredAt, key, c.ObjectVersion+1, reopen), ErrConversationVersionConflict)
		require.Equal(t, before, reopenWorkflowConversation(t, con, c), "future authority must not change status, version, unread, or activity")
		var receipts int64
		require.NoError(t, con.Model(&domain.LocalChangeJournal{}).Where("command_id = ?", key).Count(&receipts).Error)
		require.Zero(t, receipts, "rejected future authority must not reserve a receipt")
	}
	require.NoError(t, p.owner.ApplyMessageActivityWithReopen(c.AccountID, c.ID, c.ContactID, true, occurredAt, key, c.ObjectVersion, true))
	stored := reopenWorkflowConversation(t, con, c)
	require.Equal(t, domain.ConversationStatusOpen, stored.Status)
	require.Equal(t, c.ObjectVersion+1, stored.ObjectVersion)
	require.Equal(t, 1, stored.UnreadCount)
	reopenWorkflowReceipt(t, con, c, key)
}

func TestMessageReopenWorkflowRejectsChangedIntentForCommittedKey(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
	create := reopenWorkflowCreate(t, msgDB, msg)
	receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
	require.NoError(t, p.owner.UpdateStatus(c.AccountID, c.ID, domain.ConversationStatusResolved, nil))
	before := reopenWorkflowConversation(t, con, c)
	for _, intent := range []struct {
		expectedVersion int
		reopen          bool
	}{
		{expectedVersion: c.ObjectVersion + 1, reopen: true},
		{expectedVersion: c.ObjectVersion, reopen: false},
	} {
		require.ErrorIs(t, p.owner.ApplyMessageActivityWithReopen(c.AccountID, c.ID, msg.SenderID, true, msg.CreatedAt, create.ChangeID, intent.expectedVersion, intent.reopen), gorm.ErrInvalidData)
		require.Equal(t, before, reopenWorkflowConversation(t, con, c))
		require.Equal(t, receipt, reopenWorkflowReceipt(t, con, c, create.ChangeID))
	}
	require.ErrorIs(t, p.owner.ApplyMessageActivity(c.AccountID, c.ID, msg.SenderID, true, msg.CreatedAt, create.ChangeID), gorm.ErrInvalidData, "plain activity cannot replace a committed reopen workflow")
	require.NoError(t, p.owner.ApplyMessageActivityWithReopen(c.AccountID, c.ID, msg.SenderID, true, msg.CreatedAt, create.ChangeID, c.ObjectVersion, true), "identical intent remains idempotent after newer owner edits")
	require.Equal(t, before, reopenWorkflowConversation(t, con, c))
	require.Equal(t, receipt, reopenWorkflowReceipt(t, con, c, create.ChangeID))
	require.EqualValues(t, receipt.ObjectVersionAfter, reopenWorkflowFactVersion(t, r, msg.ID, create.ChangeID))
}

func TestMessageReopenWorkflowRollsBackOwnerWhenReceiptFails(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	before := reopenWorkflowConversation(t, con, c)
	require.NoError(t, con.Exec(`CREATE TRIGGER fail_reopen_receipt BEFORE INSERT ON local_change_journals
		WHEN NEW.source_module = 'CON' AND NEW.source_type = 'msg.message.created.v1'
		BEGIN SELECT RAISE(ABORT, 'scripted CON receipt outage'); END`).Error)
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true), "committed MSG create survives CON receipt failure")
	require.Equal(t, before, reopenWorkflowConversation(t, con, c), "status, version, unread, and activity must all roll back")
	create := reopenWorkflowCreate(t, msgDB, msg)
	require.Equal(t, "pending", create.Status)
	var receipts int64
	require.NoError(t, con.Model(&domain.LocalChangeJournal{}).Where("command_id = ?", create.ChangeID).Count(&receipts).Error)
	require.Zero(t, receipts)
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
	require.NoError(t, con.Exec("DROP TRIGGER fail_reopen_receipt").Error)
	n, err := r.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	stored := reopenWorkflowConversation(t, con, c)
	require.Equal(t, domain.ConversationStatusOpen, stored.Status)
	require.Equal(t, before.ObjectVersion+1, stored.ObjectVersion)
	require.Equal(t, 1, stored.UnreadCount)
	require.Equal(t, 2, p.reopenCalls)
	require.Zero(t, p.legacyCalls)
	reopenWorkflowReceipt(t, con, c, create.ChangeID)
	require.EqualValues(t, stored.ObjectVersion, reopenWorkflowFactVersion(t, r, msg.ID, create.ChangeID))
}

func TestMessageReopenWorkflowLegacyCaptureDisabledIsUnchanged(t *testing.T) {
	for _, useWorkflow := range []bool{false, true} {
		name := "plain_create"
		if useWorkflow {
			name = "workflow_create"
		}
		t.Run(name, func(t *testing.T) {
			r, p, con, msgDB, c := reopenWorkflowFixture(t)
			r.SetAutomationCapture(false)
			require.False(t, r.AutomationCaptureEnabled())
			msg := reopenWorkflowMessage(c)
			if useWorkflow {
				require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
			} else {
				require.NoError(t, r.Create(&msg))
			}
			stored := reopenWorkflowConversation(t, con, c)
			require.Equal(t, domain.ConversationStatusResolved, stored.Status)
			require.Equal(t, c.ObjectVersion+1, stored.ObjectVersion)
			require.Equal(t, 1, stored.UnreadCount)
			require.Equal(t, 1, p.legacyCalls)
			require.Zero(t, p.reopenCalls)
			create := reopenWorkflowCreate(t, msgDB, msg)
			require.Empty(t, create.AfterState)
			require.Equal(t, "delivered", create.Status)
			receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
			require.Empty(t, receipt.AfterState)
			require.Empty(t, receipt.Reason)
			var triggers int64
			require.NoError(t, msgDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_trigger").Count(&triggers).Error)
			require.Zero(t, triggers)
			facts, err := r.PendingAutomationFacts(context.Background(), 100)
			require.NoError(t, err)
			require.Empty(t, facts)
		})
	}
}

func TestMessageReopenWorkflowRechecksContactBlockAfterDeferredDelivery(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	p.fail = true
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
	create := reopenWorkflowCreate(t, msgDB, msg)
	cus := p.owner.contacts.(*reopenWorkflowContactOwner)
	cus.contact.Blocked = true // Blocked through another conversation: this CON version did not change.
	p.fail = false
	n, err := r.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	stored := reopenWorkflowConversation(t, con, c)
	require.Equal(t, domain.ConversationStatusResolved, stored.Status)
	require.Equal(t, 1, stored.UnreadCount)
	require.Equal(t, c.ObjectVersion+1, stored.ObjectVersion)
	receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
	require.Equal(t, "message_reopen_blocked", receipt.Reason)
	require.Equal(t, true, reopenWorkflowJSON(t, receipt.AfterState)["contact_blocked"])
	require.EqualValues(t, stored.ObjectVersion, reopenWorkflowFactVersion(t, r, msg.ID, create.ChangeID), "blocked automatic reopen does not suppress legacy message callback")
}

func TestMessageReopenWorkflowContactOutageFailsClosedAndCompletedReceiptSkipsRecheck(t *testing.T) {
	r, p, con, msgDB, c := reopenWorkflowFixture(t)
	cus := p.owner.contacts.(*reopenWorkflowContactOwner)
	cus.fail = true
	msg := reopenWorkflowMessage(c)
	require.NoError(t, r.CreateWithConversationReopen(&msg, c.ObjectVersion, true))
	create := reopenWorkflowCreate(t, msgDB, msg)
	require.Equal(t, "pending", create.Status)
	stored := reopenWorkflowConversation(t, con, c)
	require.Equal(t, domain.ConversationStatusResolved, stored.Status)
	require.Equal(t, c.ObjectVersion, stored.ObjectVersion)
	require.Zero(t, stored.UnreadCount)
	cus.fail = false
	n, err := r.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	applied := reopenWorkflowConversation(t, con, c)
	require.Equal(t, domain.ConversationStatusOpen, applied.Status)
	receipt := reopenWorkflowReceipt(t, con, c, create.ChangeID)
	cus.contact.Blocked = true
	cus.fail = true
	calls := cus.calls
	require.NoError(t, msgDB.Model(&domain.LocalChangeJournal{}).Where("id = ?", create.ID).Update("status", "pending").Error)
	n, err = r.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, calls, cus.calls, "completed owner receipt must not depend on current CUS availability")
	require.Equal(t, applied, reopenWorkflowConversation(t, con, c))
	require.Equal(t, receipt, reopenWorkflowReceipt(t, con, c, create.ChangeID))
}
