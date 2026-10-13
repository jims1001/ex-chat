package repository

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func messageFenceStores(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "msg-fences.db") + "?_pragma=busy_timeout(5000)"
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(2)
		t.Cleanup(func() { _ = pool.Close() })
		return db
	}
	first, second := open(), open()
	require.NoError(t, database.AutoMigrateModule(first, "MSG"))
	return first, second
}

func assertMessageFenceDeleted(t *testing.T, db *gorm.DB, account, conversation uint) {
	t.Helper()
	var messages int64
	require.NoError(t, db.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", account, conversation).Count(&messages).Error)
	require.Zero(t, messages)
	err := db.Transaction(func(tx *gorm.DB) error { return LockMessageConversation(tx, account, conversation, false) })
	require.ErrorIs(t, err, ErrMessageConversationDeleted)
	var fences []domain.LocalChangeJournal
	require.NoError(t, db.Where("source_module = ? AND account_id = ? AND object_id = ? AND action = ?", "MSG", account, conversation, "message_conversation_fence").Find(&fences).Error)
	require.Len(t, fences, 1)
	require.Equal(t, "deleted", fences[0].Status)
}

func TestMessageConversationFenceCleanupAfterCreationRemovesRowsAndPendingFacts(t *testing.T) {
	createDB, cleanupDB := messageFenceStores(t)
	runMessageFenceCleanupAfterCreation(t, createDB, cleanupDB)
}

func runMessageFenceCleanupAfterCreation(t *testing.T, createDB, cleanupDB *gorm.DB) {
	t.Helper()
	locked, release := make(chan struct{}), make(chan struct{})
	created, cleaned := make(chan error, 1), make(chan error, 1)
	go func() {
		created <- createDB.Transaction(func(tx *gorm.DB) error {
			if err := LockMessageConversation(tx, 91, 11, false); err != nil {
				return err
			}
			message := domain.Message{ID: 1, AccountID: 91, ConversationID: 11, Content: "pending automation", MessageType: "outgoing", ContentType: "text", Status: "queued"}
			if err := tx.Create(&message).Error; err != nil {
				return err
			}
			for _, row := range []domain.LocalChangeJournal{
				{ChangeID: "fence-create", SourceModule: "MSG", AccountID: 91, ObjectType: "Message", ObjectID: 1, Action: "create", Status: "pending"},
				{ChangeID: "fence-trigger", SourceModule: "MSG", AccountID: 91, ObjectType: "Message", ObjectID: 1, Action: "automation_trigger", Status: "pending"},
				{ChangeID: "fence-outbox", SourceModule: "MSG", AccountID: 91, ObjectType: "Message", ObjectDisplayID: "1", Action: "event_outbox", Status: "pending"},
				{ChangeID: "fence-history", SourceModule: "MSG", AccountID: 91, ObjectType: "Message", ObjectDisplayID: "1", Action: "event_outbox", Status: "delivered"},
				{ChangeID: "fence-foreign", SourceModule: "MSG", AccountID: 92, ObjectType: "Message", ObjectDisplayID: "1", Action: "event_outbox", Status: "pending"},
			} {
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-created:
		t.Fatalf("creation before lock: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("creation lock timeout")
	}
	started := make(chan struct{})
	go func() { close(started); cleaned <- newMSGConversationCleanup(cleanupDB).CleanupConversation(91, 11) }()
	<-started
	close(release)
	require.NoError(t, <-created)
	require.NoError(t, <-cleaned)
	assertMessageFenceDeleted(t, createDB, 91, 11)
	var rows []domain.LocalChangeJournal
	require.NoError(t, createDB.Where("change_id IN ?", []string{"fence-create", "fence-trigger", "fence-outbox", "fence-history", "fence-foreign"}).Find(&rows).Error)
	require.Len(t, rows, 5)
	for _, row := range rows {
		switch row.ChangeID {
		case "fence-history":
			require.Equal(t, "delivered", row.Status)
		case "fence-foreign":
			require.Equal(t, "pending", row.Status)
		default:
			require.Equal(t, "superseded", row.Status)
			if row.Action != "create" {
				require.Equal(t, "rejected", row.Result)
			}
		}
	}
	require.NoError(t, newMSGConversationCleanup(cleanupDB).CleanupConversation(91, 11))
	assertMessageFenceDeleted(t, createDB, 91, 11)
}

func TestMessageConversationFenceCreationAfterCleanupCannotResurrect(t *testing.T) {
	createDB, cleanupDB := messageFenceStores(t)
	runMessageFenceCreationAfterCleanup(t, createDB, cleanupDB)
}

func runMessageFenceCreationAfterCleanup(t *testing.T, createDB, cleanupDB *gorm.DB) {
	t.Helper()
	locked, release := make(chan struct{}), make(chan struct{})
	require.NoError(t, cleanupDB.Callback().Delete().Before("gorm:delete").Register("test_fence_cleanup_pause", func(tx *gorm.DB) {
		if tx.Statement.Table == "messages" {
			close(locked)
			<-release
		}
	}))
	cleaned, created := make(chan error, 1), make(chan error, 1)
	go func() { cleaned <- newMSGConversationCleanup(cleanupDB).CleanupConversation(91, 11) }()
	select {
	case <-locked:
	case err := <-cleaned:
		t.Fatalf("cleanup before lock: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup lock timeout")
	}
	started := make(chan struct{})
	go func() {
		close(started)
		created <- createDB.Transaction(func(tx *gorm.DB) error {
			if err := LockMessageConversation(tx, 91, 11, false); err != nil {
				return err
			}
			return tx.Create(&domain.Message{AccountID: 91, ConversationID: 11, Content: "must not appear", MessageType: "outgoing", ContentType: "text", Status: "queued"}).Error
		})
	}()
	<-started
	close(release)
	require.NoError(t, <-cleaned)
	require.ErrorIs(t, <-created, ErrMessageConversationDeleted)
	assertMessageFenceDeleted(t, createDB, 91, 11)
	// The same conversation number in another account is an independent key.
	require.NoError(t, createDB.Transaction(func(tx *gorm.DB) error { return LockMessageConversation(tx, 92, 11, false) }))
}

func TestMessageConversationFenceRollbackAndPersistedRestore(t *testing.T) {
	first, second := messageFenceStores(t)
	require.ErrorIs(t, LockMessageConversation(first, 91, 11, false), gorm.ErrInvalidTransaction)
	require.ErrorIs(t, LockMessageConversation(nil, 91, 11, false), gorm.ErrInvalidData)
	sentinel := errors.New("rollback after tombstone")
	err := first.Transaction(func(tx *gorm.DB) error {
		if err := LockMessageConversation(tx, 91, 11, true); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.NoError(t, second.Transaction(func(tx *gorm.DB) error { return LockMessageConversation(tx, 91, 11, false) }))
	require.NoError(t, newMSGConversationCleanup(first).CleanupConversation(91, 11))
	var rows []domain.LocalChangeJournal
	require.NoError(t, first.Find(&rows).Error)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	// Reconstituting persisted journal records must preserve the terminal fence,
	// independent of process memory and after every message row was removed.
	restored := paymentDB(t, "restored-message-fence")
	require.NoError(t, database.AutoMigrateModule(restored, "MSG"))
	var imported []domain.LocalChangeJournal
	require.NoError(t, json.Unmarshal(raw, &imported))
	require.NoError(t, restored.Create(&imported).Error)
	assertMessageFenceDeleted(t, restored, 91, 11)
	for _, foreign := range []any{&domain.Conversation{}, &domain.Contact{}, &domain.User{}} {
		require.False(t, restored.Migrator().HasTable(foreign))
	}
}

func TestPostgresMessageConversationFenceOrdering(t *testing.T) {
	t.Run("create_before_cleanup", func(t *testing.T) {
		db := creationCausalPostgresDB(t, "MSG")
		runMessageFenceCleanupAfterCreation(t, db, db)
	})
	t.Run("cleanup_before_create", func(t *testing.T) {
		db := creationCausalPostgresDB(t, "MSG")
		runMessageFenceCreationAfterCleanup(t, db, db)
	})
}
