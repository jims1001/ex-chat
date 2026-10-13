package repository

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var errCreationMessageOwnerOffline = errors.New("scripted creation activity outage")

type creationMessageTestActivity struct {
	owner           *ConversationRepository
	mu              sync.Mutex
	fail            bool
	failAfterCommit bool
	calls           int
	workflowID      string
	step            int
	expectedVersion int
	incoming        bool
	validationErr   error
}

type creationMessageUnvalidatedActivity struct {
	messageConversationPort
}

func (*creationMessageUnvalidatedActivity) ApplyCreationMessageActivity(uint, uint, uint, bool, time.Time, string, int, string, int) error {
	return nil
}

func (p *creationMessageTestActivity) ValidateCreationWorkflowMessage(context.Context, string, int, *domain.Message, int) error {
	return p.validationErr
}

func (p *creationMessageTestActivity) MessageTargetExists(a, c uint) (bool, error) {
	return p.owner.MessageTargetExists(a, c)
}

func (p *creationMessageTestActivity) ApplyMessageActivity(uint, uint, uint, bool, time.Time, string) error {
	return errors.New("creation workflow must not use legacy activity")
}

func (p *creationMessageTestActivity) ApplyCreationMessageActivity(a, c, u uint, incoming bool, at time.Time, key string, expectedVersion int, workflowID string, step int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.workflowID, p.step, p.expectedVersion, p.incoming = workflowID, step, expectedVersion, incoming
	if p.fail {
		return errCreationMessageOwnerOffline
	}
	// The actual CON creation-plan checks have separate owner tests. Keep this
	// split-store MSG contract fake idempotent on the real activity receipt.
	if err := p.owner.ApplyMessageActivity(a, c, u, incoming, at, key); err != nil {
		return err
	}
	if p.failAfterCommit {
		return errCreationMessageOwnerOffline
	}
	return nil
}

func creationMessageFixture(t *testing.T) (*MessageRepository, *creationMessageTestActivity, *gorm.DB, *gorm.DB, domain.Conversation) {
	t.Helper()
	_, deferred, con, msg, c := automationMessageFixture(t)
	p := &creationMessageTestActivity{owner: deferred.owner}
	r := NewMessageRepositoryWithConversationPort(msg, p)
	r.SetAutomationCapture(true)
	return r, p, con, msg, c
}

func creationMessagePlan(c domain.Conversation) domain.Message {
	return domain.Message{AccountID: c.AccountID, ConversationID: c.ID, SenderType: domain.SenderTypeContact, SenderID: c.ContactID, MessageType: domain.MessageTypeIncoming, ContentType: domain.ContentTypeText, Content: "initial widget message", Status: domain.MessageStatusSent}
}

func creationMessageRows(t *testing.T, db *gorm.DB, messages, receipts, changes, triggers int64) {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&domain.Message{}).Count(&n).Error)
	require.Equal(t, messages, n)
	for action, want := range map[string]int64{"creation_message_receipt": receipts, "create": changes, "automation_trigger": triggers} {
		require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ?", "MSG", action).Count(&n).Error)
		require.Equal(t, want, n, action)
	}
}

func TestCreationWorkflowMessagePersistsPlanOnceWithoutAutomationTrigger(t *testing.T) {
	for _, messageType := range []string{domain.MessageTypeIncoming, domain.MessageTypeOutgoing} {
		t.Run(messageType, func(t *testing.T) {
			r, p, con, db, c := creationMessageFixture(t)
			plan := creationMessagePlan(c)
			plan.MessageType = messageType
			original := plan
			stored, err := r.CreateCreationWorkflowMessage(context.Background(), "creation-plan", 0, &plan, 1)
			require.NoError(t, err)
			require.NotZero(t, stored.ID)
			require.Equal(t, original, plan, "immutable plan must survive reuse")
			require.Equal(t, "creation-plan", p.workflowID)
			require.Equal(t, 0, p.step)
			require.Equal(t, 1, p.expectedVersion)
			require.Equal(t, messageType == domain.MessageTypeIncoming, p.incoming)
			before := reopenWorkflowConversation(t, con, c)
			retried, err := r.CreateCreationWorkflowMessage(context.Background(), "creation-plan", 0, &plan, 1)
			require.NoError(t, err)
			require.Equal(t, stored, retried)
			require.Equal(t, before, reopenWorkflowConversation(t, con, c))
			creationMessageRows(t, db, 1, 1, 1, 0)
			change := reopenWorkflowCreate(t, db, *stored)
			require.Equal(t, creationMessageChangeID("creation-plan", 0), change.ChangeID)
			require.Len(t, change.ChangeID, 64)
			require.Equal(t, "delivered", change.Status)
			var intent map[string]any
			require.NoError(t, json.Unmarshal([]byte(change.AfterState), &intent))
			require.Equal(t, map[string]any{"expected_version": float64(1), "reopen": false, "creation_workflow_id": "creation-plan", "creation_workflow_step": float64(0)}, intent)
		})
	}
}

func TestCreationWorkflowMessageSurvivesCommitThenOwnerFailureAndRestart(t *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		name := "before_con_commit"
		if afterCommit {
			name = "after_con_commit"
		}
		t.Run(name, func(t *testing.T) {
			r, p, con, db, c := creationMessageFixture(t)
			p.fail, p.failAfterCommit = !afterCommit, afterCommit
			plan := creationMessagePlan(c)
			stored, err := r.CreateCreationWorkflowMessage(context.Background(), "durable-plan", 2, &plan, 3)
			require.ErrorIs(t, err, errCreationMessageOwnerOffline)
			require.NotNil(t, stored)
			require.NotZero(t, stored.ID)
			require.Equal(t, "pending", reopenWorkflowCreate(t, db, *stored).Status)
			creationMessageRows(t, db, 1, 1, 1, 0)
			port := &creationMessageTestActivity{owner: NewConversationRepository(con)}
			restarted := NewMessageRepositoryWithConversationPort(db, port)
			restarted.SetAutomationCapture(true)
			retried, err := restarted.CreateCreationWorkflowMessage(context.Background(), "durable-plan", 2, &plan, 3)
			require.NoError(t, err)
			require.Equal(t, stored, retried)
			require.Equal(t, "delivered", reopenWorkflowCreate(t, db, *stored).Status)
			final := reopenWorkflowConversation(t, con, c)
			require.Equal(t, 1, final.UnreadCount)
			require.Equal(t, 2, final.ObjectVersion)
			creationMessageRows(t, db, 1, 1, 1, 0)
		})
	}
}

func TestCreationWorkflowMessagePendingActivityReplaysAfterRestart(t *testing.T) {
	r, p, con, db, c := creationMessageFixture(t)
	p.fail = true
	plan := creationMessagePlan(c)
	stored, err := r.CreateCreationWorkflowMessage(context.Background(), "reconcile-plan", 1, &plan, 2)
	require.ErrorIs(t, err, errCreationMessageOwnerOffline)
	port := &creationMessageTestActivity{owner: NewConversationRepository(con)}
	restarted := NewMessageRepositoryWithConversationPort(db, port)
	n, err := restarted.ReconcilePendingActivity(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "reconcile-plan", port.workflowID)
	require.Equal(t, 1, port.step)
	require.Equal(t, 2, port.expectedVersion)
	require.Equal(t, "delivered", reopenWorkflowCreate(t, db, *stored).Status)
	creationMessageRows(t, db, 1, 1, 1, 0)
}

func TestCreationWorkflowMessageRejectsChangedDurableIntent(t *testing.T) {
	r, _, con, db, c := creationMessageFixture(t)
	plan := creationMessagePlan(c)
	stored, err := r.CreateCreationWorkflowMessage(context.Background(), "collision-plan", 0, &plan, 1)
	require.NoError(t, err)
	other := domain.Conversation{AccountID: c.AccountID, InboxID: c.InboxID, ContactID: c.ContactID, DisplayID: 2, Status: "open", ObjectVersion: 1}
	require.NoError(t, con.Create(&other).Error)
	otherTenant := other
	otherTenant.ID = 0
	otherTenant.AccountID++
	require.NoError(t, con.Create(&otherTenant).Error)
	for _, mutate := range []func(*domain.Message){
		func(m *domain.Message) { m.Content += " changed" },
		func(m *domain.Message) { m.ConversationID = other.ID },
		func(m *domain.Message) { m.AccountID, m.ConversationID = otherTenant.AccountID, otherTenant.ID },
		func(m *domain.Message) { m.SenderID++ },
		func(m *domain.Message) { m.SenderType = domain.SenderTypeUser },
		func(m *domain.Message) { m.MessageType = domain.MessageTypeOutgoing },
		func(m *domain.Message) { m.ContentType = "text/html" },
		func(m *domain.Message) { m.ContentAttributes = `{"changed":true}` },
		func(m *domain.Message) { m.Translations = `{"en":"changed"}` },
		func(m *domain.Message) { m.EchoID = "changed" },
		func(m *domain.Message) { m.Private = true },
		func(m *domain.Message) { m.Status = "failed" },
	} {
		changed := plan
		mutate(&changed)
		_, err := r.CreateCreationWorkflowMessage(context.Background(), "collision-plan", 0, &changed, 1)
		require.ErrorIs(t, err, ErrCreationWorkflowMessageConflict)
	}
	_, err = r.CreateCreationWorkflowMessage(context.Background(), "collision-plan", 0, &plan, 2)
	require.ErrorIs(t, err, ErrCreationWorkflowMessageConflict)
	creationMessageRows(t, db, 1, 1, 1, 0)
	require.Equal(t, "delivered", reopenWorkflowCreate(t, db, *stored).Status)
	// Editing a message after creation must neither change its original intent
	// nor cause a retry to recreate or overwrite the user's later edit.
	require.NoError(t, db.Model(&domain.Message{}).Where("id = ?", stored.ID).Update("content", "later edit").Error)
	retried, err := r.CreateCreationWorkflowMessage(context.Background(), "collision-plan", 0, &plan, 1)
	require.NoError(t, err)
	require.Equal(t, "later edit", retried.Content)
}

func TestCreationWorkflowMessageTransactionRollback(t *testing.T) {
	for _, target := range []string{"creation_message_receipt", "create"} {
		t.Run(target, func(t *testing.T) {
			r, _, _, db, c := creationMessageFixture(t)
			require.NoError(t, db.Exec(`CREATE TRIGGER fail_creation_journal BEFORE INSERT ON local_change_journals WHEN NEW.action = '`+target+`' BEGIN SELECT RAISE(ABORT, 'scripted MSG journal outage'); END`).Error)
			plan := creationMessagePlan(c)
			stored, err := r.CreateCreationWorkflowMessage(context.Background(), "rollback-plan", 0, &plan, 1)
			require.Error(t, err)
			require.Nil(t, stored)
			creationMessageRows(t, db, 0, 0, 0, 0)
			require.Zero(t, plan.ID)
			require.True(t, plan.CreatedAt.IsZero())
			require.NoError(t, db.Exec("DROP TRIGGER fail_creation_journal").Error)
			_, err = r.CreateCreationWorkflowMessage(context.Background(), "rollback-plan", 0, &plan, 1)
			require.NoError(t, err)
			creationMessageRows(t, db, 1, 1, 1, 0)
		})
	}
}

func TestCreationWorkflowMessageRejectsUnownedCreationFields(t *testing.T) {
	r, _, _, db, c := creationMessageFixture(t)
	for _, mutate := range []func(*domain.Message){
		func(m *domain.Message) { m.ID = 999 },
		func(m *domain.Message) { m.AccountID = 0 },
		func(m *domain.Message) { m.ConversationID = 0 },
		func(m *domain.Message) { m.CreatedAt = time.Now() },
		func(m *domain.Message) { m.UpdatedAt = time.Now() },
		func(m *domain.Message) { m.Deleted = true },
		func(m *domain.Message) { now := time.Now(); m.DeletedAt = &now },
		func(m *domain.Message) { now := time.Now(); m.EditedAt = &now },
		func(m *domain.Message) { m.Sender = map[string]any{"id": 1} },
	} {
		plan := creationMessagePlan(c)
		mutate(&plan)
		_, err := r.CreateCreationWorkflowMessage(context.Background(), "invalid-plan", 0, &plan, 1)
		require.ErrorIs(t, err, gorm.ErrInvalidData)
	}
	plan := creationMessagePlan(c)
	for _, workflow := range []string{"", " "} {
		_, err := r.CreateCreationWorkflowMessage(context.Background(), workflow, 0, &plan, 1)
		require.ErrorIs(t, err, gorm.ErrInvalidData)
	}
	_, err := r.CreateCreationWorkflowMessage(context.Background(), "invalid-plan", -1, &plan, 1)
	require.ErrorIs(t, err, gorm.ErrInvalidData)
	_, err = r.CreateCreationWorkflowMessage(context.Background(), "invalid-plan", 0, &plan, 0)
	require.ErrorIs(t, err, gorm.ErrInvalidData)
	_, err = r.CreateCreationWorkflowMessage(context.Background(), "invalid-plan", 0, nil, 1)
	require.ErrorIs(t, err, gorm.ErrInvalidData)
	creationMessageRows(t, db, 0, 0, 0, 0)
}

func TestCreationWorkflowMessageDoesNotResurrectDeletedMessage(t *testing.T) {
	r, _, _, db, c := creationMessageFixture(t)
	plan := creationMessagePlan(c)
	stored, err := r.CreateCreationWorkflowMessage(context.Background(), "deleted-plan", 0, &plan, 1)
	require.NoError(t, err)
	require.NoError(t, db.Delete(stored).Error)
	_, err = r.CreateCreationWorkflowMessage(context.Background(), "deleted-plan", 0, &plan, 1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	creationMessageRows(t, db, 0, 1, 1, 0)
}

func TestCreationWorkflowMessageConcurrentIndependentConnections(t *testing.T) {
	_, p, _, _, c := creationMessageFixture(t)
	path := filepath.Join(t.TempDir(), "creation-concurrent.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(10000)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
		return db
	}
	db := open()
	require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, database.AutoMigrateModule(db, "MSG"))
	const workers = 12
	repositories := make([]*MessageRepository, workers)
	for i := range repositories {
		repositories[i] = NewMessageRepositoryWithConversationPort(open(), p)
		repositories[i].SetAutomationCapture(true)
	}
	start := make(chan struct{})
	errs := make(chan error, workers)
	ids := make(chan uint, workers)
	var wg sync.WaitGroup
	for _, r := range repositories {
		wg.Add(1)
		go func(r *MessageRepository) {
			defer wg.Done()
			<-start
			plan := creationMessagePlan(c)
			stored, err := r.CreateCreationWorkflowMessage(context.Background(), "concurrent-plan", 0, &plan, 1)
			errs <- err
			if stored != nil {
				ids <- stored.ID
			}
		}(r)
	}
	close(start)
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		require.NoError(t, err)
	}
	var first uint
	for id := range ids {
		if first == 0 {
			first = id
		}
		require.Equal(t, first, id)
	}
	require.NotZero(t, first)
	creationMessageRows(t, db, 1, 1, 1, 0)
}

func TestCreationWorkflowMessageRejectsUnsupportedOwnerAndCancelledContext(t *testing.T) {
	r, p, _, db, c := creationMessageFixture(t)
	plan := creationMessagePlan(c)
	legacy := NewMessageRepositoryWithConversationPort(db, &automationDeferredActivity{owner: p.owner})
	_, err := legacy.CreateCreationWorkflowMessage(context.Background(), "no-capability", 0, &plan, 1)
	require.ErrorIs(t, err, gorm.ErrInvalidData)
	unvalidated := NewMessageRepositoryWithConversationPort(db, &creationMessageUnvalidatedActivity{messageConversationPort: p.owner})
	_, err = unvalidated.CreateCreationWorkflowMessage(context.Background(), "no-validation", 0, &plan, 1)
	require.ErrorIs(t, err, gorm.ErrInvalidData)
	_, err = r.CreateCreationWorkflowMessage(nil, "no-context", 0, &plan, 1)
	require.ErrorIs(t, err, gorm.ErrInvalidData)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.CreateCreationWorkflowMessage(ctx, "cancelled-plan", 0, &plan, 1)
	require.ErrorIs(t, err, context.Canceled)
	plan.AccountID++
	_, err = r.CreateCreationWorkflowMessage(context.Background(), "wrong-tenant", 0, &plan, 1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	creationMessageRows(t, db, 0, 0, 0, 0)
}

func TestCreationWorkflowMessageCanonicalDefaultsAndDistinctSteps(t *testing.T) {
	r, _, _, db, c := creationMessageFixture(t)
	plan := creationMessagePlan(c)
	plan.MessageType, plan.ContentType, plan.Status = "", "", ""
	stored, err := r.CreateCreationWorkflowMessage(context.Background(), "defaults-plan", 0, &plan, 1)
	require.NoError(t, err)
	canonical := creationMessagePlan(c)
	retried, err := r.CreateCreationWorkflowMessage(context.Background(), "defaults-plan", 0, &canonical, 1)
	require.NoError(t, err)
	require.Equal(t, stored, retried)
	next, err := r.CreateCreationWorkflowMessage(context.Background(), "defaults-plan", 1, &canonical, 2)
	require.NoError(t, err)
	require.NotEqual(t, stored.ID, next.ID)
	require.NotEqual(t, creationMessageChangeID("defaults-plan", 0), creationMessageChangeID("defaults-plan", 1))
	creationMessageRows(t, db, 2, 2, 2, 0)
}

func TestCreationWorkflowMessageRequiresCommittedPlanBeforeSuppression(t *testing.T) {
	r, p, _, db, c := creationMessageFixture(t)
	p.validationErr = ErrCreationWorkflowMessageConflict
	plan := creationMessagePlan(c)
	_, err := r.CreateCreationWorkflowMessage(context.Background(), "unknown-plan", 0, &plan, 1)
	require.ErrorIs(t, err, ErrCreationWorkflowMessageConflict)
	require.Zero(t, p.calls)
	creationMessageRows(t, db, 0, 0, 0, 0)
	p.validationErr = errCreationMessageOwnerOffline
	_, err = r.CreateCreationWorkflowMessage(context.Background(), "unverifiable-plan", 0, &plan, 1)
	require.ErrorIs(t, err, errCreationMessageOwnerOffline)
	creationMessageRows(t, db, 0, 0, 0, 0)
}

func TestCreationWorkflowMessageRealOwnerRejectsUnplannedSuppression(t *testing.T) {
	_, p, con, db, c := creationMessageFixture(t)
	owner := p.owner
	owner.SetAutomationCapture(true)
	r := NewMessageRepositoryWithConversationPort(db, owner)
	r.SetAutomationCapture(true)
	draft := domain.Conversation{AccountID: c.AccountID, InboxID: c.InboxID, ContactID: c.ContactID, Status: "open", Priority: "medium"}
	initial := creationMessagePlan(c)
	initial.ConversationID = 0
	require.NoError(t, owner.CreateWithInitialMessages(&draft, []domain.Message{initial}))
	var row domain.LocalChangeJournal
	require.NoError(t, con.Where("action = ? AND object_id = ?", "creation_workflow", draft.ID).First(&row).Error)
	plan, err := validateCreationPlan(row)
	require.NoError(t, err)
	planned := plan.Messages[0]
	changed := planned
	changed.Content = "unapproved content"
	_, err = r.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 0, &changed, 1)
	require.ErrorIs(t, err, ErrCreationWorkflowMessageConflict)
	_, err = r.CreateCreationWorkflowMessage(context.Background(), "uncommitted-workflow", 0, &planned, 1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = r.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 0, &planned, 2)
	require.ErrorIs(t, err, ErrCreationWorkflowMessageConflict)
	_, err = r.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 1, &planned, 2)
	require.ErrorIs(t, err, ErrCreationWorkflowMessageConflict)
	creationMessageRows(t, db, 0, 0, 0, 0)
	stored, err := r.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 0, &planned, 1)
	require.NoError(t, err)
	require.NoError(t, owner.UpdatePriority(draft.AccountID, draft.ID, "urgent"))
	before := reopenWorkflowConversation(t, con, draft)
	retried, err := r.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 0, &planned, 1)
	require.NoError(t, err)
	require.Equal(t, stored, retried)
	require.Equal(t, before, reopenWorkflowConversation(t, con, draft))
	creationMessageRows(t, db, 1, 1, 1, 0)
	ordinary := planned
	ordinary.Content = "ordinary next message"
	require.NoError(t, r.Create(&ordinary))
	creationMessageRows(t, db, 2, 1, 2, 1)
}
