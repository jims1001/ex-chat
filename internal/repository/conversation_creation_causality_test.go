package repository

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func creationCausalPostgresDB(t *testing.T, module string) *gorm.DB {
	t.Helper()
	if os.Getenv("EX_CHAT_AUT_POSTGRES_TESTS") != "1" {
		t.Skip("requires EX_CHAT_AUT_POSTGRES_TESTS=1 and loopback DB_DSN")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DB_DSN"))
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()))
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	schema := "creation_" + strings.ToLower(module) + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	cfg.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*cfg)
	pool.SetMaxOpenConns(8)
	t.Cleanup(func() {
		_ = pool.Close()
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		_ = admin.Close()
	})
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrateModule(db, module))
	return db
}
func creationCausalOwners(t *testing.T, pg bool) (*ConversationRepository, *MessageRepository) {
	t.Helper()
	var con, msg *gorm.DB
	if pg {
		con = creationCausalPostgresDB(t, "CON")
		msg = creationCausalPostgresDB(t, "MSG")
	} else {
		con = paymentDB(t, "creation-con")
		msg = paymentDB(t, "creation-msg")
		require.NoError(t, database.AutoMigrateModule(con, "CON"))
		require.NoError(t, database.AutoMigrateModule(msg, "MSG"))
	}
	return restartCreationCausalOwners(con, msg)
}
func restartCreationCausalOwners(con, msg *gorm.DB) (*ConversationRepository, *MessageRepository) {
	owner := NewConversationRepository(con)
	owner.SetAutomationCapture(true)
	messages := NewMessageRepositoryWithConversationPort(msg, owner)
	messages.SetAutomationCapture(true)
	return owner, messages
}
func createCausalPlan(t *testing.T, owner *ConversationRepository, count int) domain.Conversation {
	t.Helper()
	c := domain.Conversation{AccountID: 91, InboxID: 17, ContactID: 19, Status: "open", Priority: "medium"}
	messages := []domain.Message{{SenderType: domain.SenderTypeContact, SenderID: c.ContactID, MessageType: domain.MessageTypeIncoming, ContentType: "text", Content: "initial request", Status: "sent"}}
	if count == 2 {
		messages = append(messages, domain.Message{MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "office closed", Status: "sent"})
	}
	require.NoError(t, owner.CreateWithInitialMessages(&c, messages))
	return c
}
func creationCausalPlanRow(t *testing.T, owner *ConversationRepository, c domain.Conversation) domain.LocalChangeJournal {
	t.Helper()
	var row domain.LocalChangeJournal
	require.NoError(t, owner.db.Where("source_module = ? AND account_id = ? AND object_id = ? AND action = ?", "CON", c.AccountID, c.ID, "creation_workflow").First(&row).Error)
	return row
}
func creationCausalFact(t *testing.T, owner *ConversationRepository, c domain.Conversation) (foundation.EventEnvelope, bool) {
	t.Helper()
	facts, err := owner.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	for _, fact := range facts {
		id, _ := messageAutomationUint(fact.Payload["conversation_id"])
		if fact.EventType == "con.conversation.created.v1" && id == c.ID {
			return fact, true
		}
	}
	return foundation.EventEnvelope{}, false
}

func TestConversationCreationCausalityGatesUntilRestartCompletion(t *testing.T) {
	owner, messages := creationCausalOwners(t, false)
	c := createCausalPlan(t, owner, 2)
	_, ready := creationCausalFact(t, owner, c)
	require.False(t, ready)
	creationMessageRows(t, messages.db, 0, 0, 0, 0)
	restarted, rm := restartCreationCausalOwners(owner.db, messages.db)
	n, err := NewCreationWorkflowCoordinator(restarted, rm).ReconcileCreationWorkflows(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	fact, ready := creationCausalFact(t, restarted, c)
	require.True(t, ready)
	version, err := messageAutomationUint(fact.Payload["object_version"])
	require.NoError(t, err)
	require.EqualValues(t, 3, version)
	require.EqualValues(t, 1, fact.AggregateVersion, "delivery preserves the original event-stream identity")
	var captured domain.LocalChangeJournal
	require.NoError(t, owner.db.Where("action = ? AND event_id = ?", "automation_trigger", fact.EventID).First(&captured).Error)
	var original foundation.EventEnvelope
	require.NoError(t, json.Unmarshal([]byte(captured.AfterState), &original))
	require.EqualValues(t, 1, original.AggregateVersion, "stored creation fact is never rebased or rewritten")
	final := reopenWorkflowConversation(t, owner.db, c)
	require.Equal(t, 3, final.ObjectVersion)
	require.Equal(t, 1, final.UnreadCount)
	creationMessageRows(t, messages.db, 2, 2, 2, 0)
	got, err := NewCreationWorkflowCoordinator(restarted, rm).CompleteCreationWorkflow(context.Background(), c.AccountID, c.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "initial request", got[0].Content)
	require.Equal(t, "office closed", got[1].Content)
	require.Equal(t, final, reopenWorkflowConversation(t, owner.db, c))
	facts, err := rm.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, facts)
	require.NoError(t, restarted.MarkAutomationFactDelivered(context.Background(), fact.EventID))
	_, ready = creationCausalFact(t, restarted, c)
	require.False(t, ready)
}

func TestConversationCreationCausalityManualInterleaveCannotRebase(t *testing.T) {
	for _, when := range []string{"before_first", "between_steps", "after_completion"} {
		t.Run(when, func(t *testing.T) {
			owner, messages := creationCausalOwners(t, false)
			c := createCausalPlan(t, owner, 2)
			row := creationCausalPlanRow(t, owner, c)
			plan, err := validateCreationPlan(row)
			require.NoError(t, err)
			if when == "between_steps" {
				_, err = messages.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 0, &plan.Messages[0], 1)
				require.NoError(t, err)
			}
			if when == "after_completion" {
				_, err = NewCreationWorkflowCoordinator(owner, messages).CompleteCreationWorkflow(context.Background(), c.AccountID, c.ID)
				require.NoError(t, err)
			}
			require.NoError(t, owner.UpdatePriority(c.AccountID, c.ID, "urgent"))
			require.NoError(t, owner.UpdateStatus(c.AccountID, c.ID, "pending", nil))
			owner, messages = restartCreationCausalOwners(owner.db, messages.db)
			_, err = NewCreationWorkflowCoordinator(owner, messages).ReconcileCreationWorkflows(context.Background(), 100)
			require.NoError(t, err)
			fact, ready := creationCausalFact(t, owner, c)
			require.True(t, ready)
			version, err := messageAutomationUint(fact.Payload["object_version"])
			require.NoError(t, err)
			want := uint(1)
			if when == "after_completion" {
				want = 3
			}
			require.Equal(t, want, version)
			final := reopenWorkflowConversation(t, owner.db, c)
			require.Equal(t, 5, final.ObjectVersion)
			require.Equal(t, "pending", final.Status)
			require.Equal(t, "urgent", final.Priority)
			require.Less(t, version, uint(final.ObjectVersion), "manual versions must never be adopted")
			creationMessageRows(t, messages.db, 2, 2, 2, 0)
		})
	}
}

func TestConversationCreationCausalityPlanAndFactAreAtomic(t *testing.T) {
	for _, action := range []string{"creation_workflow", "automation_trigger"} {
		t.Run(action, func(t *testing.T) {
			owner, messages := creationCausalOwners(t, false)
			require.NoError(t, owner.db.Exec(`CREATE TRIGGER fail_create_plan BEFORE INSERT ON local_change_journals WHEN NEW.action = '`+action+`' BEGIN SELECT RAISE(ABORT, 'plan failure'); END`).Error)
			c := domain.Conversation{AccountID: 91, InboxID: 17, ContactID: 19, Status: "open"}
			err := owner.CreateWithInitialMessages(&c, []domain.Message{{MessageType: "incoming", Content: "initial"}})
			require.Error(t, err)
			require.Zero(t, c.ID)
			var count int64
			require.NoError(t, owner.db.Model(&domain.Conversation{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, owner.db.Model(&domain.LocalChangeJournal{}).Where("action IN ?", []string{"creation_workflow", "automation_trigger"}).Count(&count).Error)
			require.Zero(t, count)
			creationMessageRows(t, messages.db, 0, 0, 0, 0)
		})
	}
}

func TestConversationCreationCausalityReceiptRollbackRecoversExactlyOnce(t *testing.T) {
	for _, action := range []string{"update", "creation_workflow_completed"} {
		t.Run(action, func(t *testing.T) {
			owner, messages := creationCausalOwners(t, false)
			c := createCausalPlan(t, owner, 2)
			require.NoError(t, owner.db.Exec(`CREATE TRIGGER fail_creation_receipt BEFORE INSERT ON local_change_journals WHEN NEW.action = '`+action+`' BEGIN SELECT RAISE(ABORT, 'receipt failure'); END`).Error)
			_, err := NewCreationWorkflowCoordinator(owner, messages).CompleteCreationWorkflow(context.Background(), c.AccountID, c.ID)
			require.Error(t, err)
			_, ready := creationCausalFact(t, owner, c)
			require.False(t, ready)
			final := reopenWorkflowConversation(t, owner.db, c)
			if action == "update" {
				require.Equal(t, 1, final.ObjectVersion)
				require.Zero(t, final.UnreadCount)
			} else {
				require.Equal(t, 3, final.ObjectVersion)
				require.Equal(t, 1, final.UnreadCount)
			}
			require.NoError(t, owner.db.Exec("DROP TRIGGER fail_creation_receipt").Error)
			owner, messages = restartCreationCausalOwners(owner.db, messages.db)
			n, err := NewCreationWorkflowCoordinator(owner, messages).ReconcileCreationWorkflows(context.Background(), 100)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			fact, ready := creationCausalFact(t, owner, c)
			require.True(t, ready)
			version, err := messageAutomationUint(fact.Payload["object_version"])
			require.NoError(t, err)
			require.EqualValues(t, 3, version)
			final = reopenWorkflowConversation(t, owner.db, c)
			require.Equal(t, 3, final.ObjectVersion)
			require.Equal(t, 1, final.UnreadCount)
			creationMessageRows(t, messages.db, 2, 2, 2, 0)
		})
	}
}

func TestConversationCreationCausalityPoisonCannotHideHealthyPlan(t *testing.T) {
	owner, messages := creationCausalOwners(t, false)
	bad := createCausalPlan(t, owner, 1)
	good := createCausalPlan(t, owner, 1)
	row := creationCausalPlanRow(t, owner, bad)
	require.NoError(t, owner.db.Model(&domain.LocalChangeJournal{}).Where("id = ?", row.ID).Update("after_state", "{}").Error)
	n, err := NewCreationWorkflowCoordinator(owner, messages).ReconcileCreationWorkflows(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	_, ready := creationCausalFact(t, owner, bad)
	require.False(t, ready)
	_, ready = creationCausalFact(t, owner, good)
	require.True(t, ready)
	row = creationCausalPlanRow(t, owner, bad)
	require.Equal(t, "manual_action_required", row.Status)
	creationMessageRows(t, messages.db, 1, 1, 1, 0)
}

func TestConversationCreationCausalityRejectsForgedCompletionAndTenant(t *testing.T) {
	owner, messages := creationCausalOwners(t, false)
	c := createCausalPlan(t, owner, 1)
	row := creationCausalPlanRow(t, owner, c)
	plan, err := validateCreationPlan(row)
	require.NoError(t, err)
	foreign := plan.Messages[0]
	foreign.AccountID++
	_, err = messages.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 0, &foreign, 1)
	require.Error(t, err)
	forged := plan.Messages[0]
	forged.Content = "unplanned suppressed content"
	_, err = messages.CreateCreationWorkflowMessage(context.Background(), plan.WorkflowID, 0, &forged, 1)
	require.Error(t, err)
	creationMessageRows(t, messages.db, 0, 0, 0, 0)
	_, err = NewCreationWorkflowCoordinator(owner, messages).CompleteCreationWorkflow(context.Background(), c.AccountID, c.ID)
	require.NoError(t, err)
	var done domain.LocalChangeJournal
	require.NoError(t, owner.db.Where("action = ?", "creation_workflow_completed").First(&done).Error)
	var state conversationCreationCompletion
	require.NoError(t, json.Unmarshal([]byte(done.AfterState), &state))
	state.TargetVersion = 99
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, owner.db.Model(&domain.LocalChangeJournal{}).Where("id = ?", done.ID).Updates(map[string]any{"after_state": string(raw), "integrity_hash": creationPlanDigest(string(raw)), "object_version_after": 99}).Error)
	_, ready := creationCausalFact(t, owner, c)
	require.False(t, ready)
	creationMessageRows(t, messages.db, 1, 1, 1, 0)
}

func runCreationCausalConcurrentTest(t *testing.T, pg bool) {
	owner, messages := creationCausalOwners(t, pg)
	c := createCausalPlan(t, owner, 2)
	const n = 8
	errs := make(chan error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		con, msg := restartCreationCausalOwners(owner.db, messages.db)
		_ = msg
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := NewCreationWorkflowCoordinator(con, msg).CompleteCreationWorkflow(context.Background(), c.AccountID, c.ID)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	final := reopenWorkflowConversation(t, owner.db, c)
	require.Equal(t, 3, final.ObjectVersion)
	require.Equal(t, 1, final.UnreadCount)
	creationMessageRows(t, messages.db, 2, 2, 2, 0)
	var count int64
	require.NoError(t, owner.db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "creation_workflow_completed").Count(&count).Error)
	require.EqualValues(t, 1, count)
	fact, ready := creationCausalFact(t, owner, c)
	require.True(t, ready)
	v, err := messageAutomationUint(fact.Payload["object_version"])
	require.NoError(t, err)
	require.EqualValues(t, 3, v)
}
func TestConversationCreationCausalityConcurrentWorkers(t *testing.T) {
	runCreationCausalConcurrentTest(t, false)
}
func TestPostgresConversationCreationCausalityConcurrentWorkers(t *testing.T) {
	runCreationCausalConcurrentTest(t, true)
}

func TestConversationCreationCausalityNormalizesRealOwnerDefaults(t *testing.T) {
	owner, messages := creationCausalOwners(t, false)
	c := domain.Conversation{AccountID: 91, InboxID: 17, ContactID: 19, Status: "open"}
	require.NoError(t, owner.CreateWithInitialMessages(&c, []domain.Message{{Content: "default incoming message"}}))
	got, err := NewCreationWorkflowCoordinator(owner, messages).CompleteCreationWorkflow(context.Background(), c.AccountID, c.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, domain.MessageTypeIncoming, got[0].MessageType)
	require.Equal(t, domain.ContentTypeText, got[0].ContentType)
	require.Equal(t, domain.MessageStatusSent, got[0].Status)
	final := reopenWorkflowConversation(t, owner.db, c)
	require.Equal(t, 2, final.ObjectVersion)
	require.Equal(t, 1, final.UnreadCount)
	creationMessageRows(t, messages.db, 1, 1, 1, 0)
	_, ready := creationCausalFact(t, owner, c)
	require.True(t, ready)
}
