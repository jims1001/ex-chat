package test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type recoverableConversationPort struct {
	owner       *repository.ConversationRepository
	unavailable bool
}

func (p *recoverableConversationPort) MessageTargetExists(accountID, conversationID uint) (bool, error) {
	return p.owner.MessageTargetExists(accountID, conversationID)
}

func (p *recoverableConversationPort) ApplyMessageActivity(accountID, conversationID, actorID uint, incoming bool, occurredAt time.Time, changeID string) error {
	if p.unavailable {
		return errors.New("CON unavailable")
	}
	return p.owner.ApplyMessageActivity(accountID, conversationID, actorID, incoming, occurredAt, changeID)
}

// This is a staged architecture acceptance gate, not a regression test of the
// combined server. Keep it visibly separate until ARCH-08's extraction work is
// complete; passing the regular suite must never imply this gate has passed.
func TestModuleIsolationAcceptance(t *testing.T) {
	if os.Getenv("EX_CHAT_MODULE_ACCEPTANCE") != "1" {
		t.Skip("ARCH-08 independent-store acceptance: run make test-module-isolation")
	}
	openStore := func(module string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), module+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		if err := database.AutoMigrateModule(db, module); err != nil {
			t.Fatal(err)
		}
		return db
	}
	t.Run("MSG_create_uses_CON_public_contract", func(t *testing.T) {
		conDB, msgDB := openStore("CON"), openStore("MSG")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusOpen}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		if msgDB.Migrator().HasTable(&domain.Conversation{}) {
			t.Fatal("fixture violates the isolated store boundary")
		}
		message := domain.Message{AccountID: 1, ConversationID: conversation.ID, MessageType: domain.MessageTypeIncoming, Content: "isolation acceptance fixture"}
		msgRepo := repository.NewMessageRepositoryWithConversationPort(msgDB, repository.NewConversationRepository(conDB))
		if err := msgRepo.Create(&message); err != nil {
			t.Fatalf("ARCH-08: MSG cannot create against separate CON/MSG stores: %v; wire public CON contracts, do not copy CON tables into MSG", err)
		}
		if msgDB.Migrator().HasTable(&domain.Conversation{}) {
			t.Fatal("MSG created a foreign authority table")
		}
		var changes int64
		if err := msgDB.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND account_id = ? AND object_id = ?", "MSG", 1, message.ID).Count(&changes).Error; err != nil || changes != 1 {
			t.Fatalf("message creation requires one atomic local change: count=%d err=%v", changes, err)
		}
		var updated domain.Conversation
		if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.UnreadCount != 1 {
			t.Fatalf("CON activity was not applied: unread=%d err=%v", updated.UnreadCount, err)
		}
		// Replay must not create a second unread increment.
		if _, err := msgRepo.ReconcilePendingActivity(10); err != nil {
			t.Fatal(err)
		}
		if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.UnreadCount != 1 {
			t.Fatalf("CON activity was applied twice: unread=%d err=%v", updated.UnreadCount, err)
		}
		second := domain.Message{AccountID: 1, ConversationID: conversation.ID, MessageType: domain.MessageTypeIncoming, Content: "second"}
		if err := msgRepo.Create(&second); err != nil {
			t.Fatal(err)
		}
		if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.UnreadCount != 2 {
			t.Fatalf("second CON activity unread=%d err=%v", updated.UnreadCount, err)
		}
		var latest domain.LocalChangeJournal
		if err := conDB.Where("source_module = ? AND object_type = ?", "CON", "Conversation").Order("id DESC").First(&latest).Error; err != nil || latest.ObjectVersionBefore != updated.ObjectVersion-1 || latest.ObjectVersionAfter != updated.ObjectVersion {
			t.Fatalf("CON version journal=%d->%d current=%d err=%v", latest.ObjectVersionBefore, latest.ObjectVersionAfter, updated.ObjectVersion, err)
		}
	})
	t.Run("CON_failure_replays_once", func(t *testing.T) {
		conDB, msgDB := openStore("CON"), openStore("MSG")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		port := &recoverableConversationPort{owner: repository.NewConversationRepository(conDB), unavailable: true}
		msgRepo := repository.NewMessageRepositoryWithConversationPort(msgDB, port)
		msg := domain.Message{AccountID: 1, ConversationID: conversation.ID, SenderType: domain.SenderTypeContact, SenderID: 7, MessageType: domain.MessageTypeIncoming, Content: "replay fixture"}
		if err := msgRepo.Create(&msg); err != nil {
			t.Fatal(err)
		}
		var pending int64
		if err := msgDB.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND status = ?", "MSG", "pending").Count(&pending).Error; err != nil || pending != 1 {
			t.Fatalf("expected one durable pending fact: %d %v", pending, err)
		}
		if _, err := msgRepo.ReconcilePendingActivity(10); err == nil {
			t.Fatal("CON outage was hidden during reconciliation")
		}
		port.unavailable = false
		if count, err := msgRepo.ReconcilePendingActivity(10); err != nil || count != 1 {
			t.Fatalf("recovery count=%d err=%v", count, err)
		}
		if count, err := msgRepo.ReconcilePendingActivity(10); err != nil || count != 0 {
			t.Fatalf("duplicate replay count=%d err=%v", count, err)
		}
		// Simulate a crash after CON committed but before MSG marked the
		// journal delivered. A fresh repository must replay without a second
		// unread increment.
		if err := msgDB.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND object_id = ?", "MSG", msg.ID).Update("status", "pending").Error; err != nil {
			t.Fatal(err)
		}
		restarted := repository.NewMessageRepositoryWithConversationPort(msgDB, repository.NewConversationRepository(conDB))
		if count, err := restarted.ReconcilePendingActivity(10); err != nil || count != 1 {
			t.Fatalf("restart replay count=%d err=%v", count, err)
		}
		var updated domain.Conversation
		if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.UnreadCount != 1 {
			t.Fatalf("CON unread=%d err=%v", updated.UnreadCount, err)
		}
	})
	t.Run("journal_failure_rolls_back_message", func(t *testing.T) {
		conDB, msgDB := openStore("CON"), openStore("MSG")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		if err := msgDB.Migrator().DropTable(&domain.LocalChangeJournal{}); err != nil {
			t.Fatal(err)
		}
		msg := domain.Message{AccountID: 1, ConversationID: conversation.ID, SenderType: domain.SenderTypeUser, SenderID: 8, Content: "must roll back"}
		if err := repository.NewMessageRepositoryWithConversationPort(msgDB, repository.NewConversationRepository(conDB)).Create(&msg); err == nil {
			t.Fatal("write succeeded without local journal")
		}
		var messages int64
		if err := msgDB.Model(&domain.Message{}).Count(&messages).Error; err != nil || messages != 0 {
			t.Fatalf("unlogged write escaped: messages=%d err=%v", messages, err)
		}
	})
	t.Run("CON_mark_unread_uses_MSG_query", func(t *testing.T) {
		conDB, msgDB := openStore("CON"), openStore("MSG")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		createdAt := time.Now().UTC()
		for _, accountID := range []uint{1, 1, 2} {
			msg := domain.Message{AccountID: accountID, ConversationID: conversation.ID, MessageType: domain.MessageTypeIncoming, Content: "unread"}
			msg.CreatedAt = createdAt
			if err := msgDB.Create(&msg).Error; err != nil {
				t.Fatal(err)
			}
		}
		owner := repository.NewConversationRepositoryWithMessagePort(conDB, repository.NewMessageRepositoryWithConversationPort(msgDB, nil))
		if err := owner.MarkUnread(1, conversation.ID); err != nil {
			t.Fatal(err)
		}
		var updated domain.Conversation
		if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.UnreadCount != 2 {
			t.Fatalf("tenant-scoped unread=%d err=%v", updated.UnreadCount, err)
		}
		if conDB.Migrator().HasTable(&domain.Message{}) {
			t.Fatal("CON created a foreign MSG table")
		}
	})
	t.Run("MSG_sender_enrichment_uses_IAM_CUS", func(t *testing.T) {
		msgDB, iamDB, cusDB := openStore("MSG"), openStore("IAM"), openStore("CUS")
		user := domain.User{Name: "Agent"}
		contact := domain.Contact{AccountID: 1, Name: "Visitor"}
		if err := iamDB.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		if err := cusDB.Create(&contact).Error; err != nil {
			t.Fatal(err)
		}
		for _, sender := range []struct {
			typeName string
			id       uint
		}{{domain.SenderTypeUser, user.ID}, {domain.SenderTypeContact, contact.ID}} {
			msg := domain.Message{AccountID: 1, ConversationID: 9, SenderType: sender.typeName, SenderID: sender.id, Content: "sender"}
			if err := msgDB.Create(&msg).Error; err != nil {
				t.Fatal(err)
			}
		}
		msgRepo := repository.NewMessageRepositoryWithPorts(msgDB, nil, repository.NewUserRepository(iamDB), repository.NewContactRepository(cusDB))
		messages, total, err := msgRepo.ListByConversation(1, 9, true, 1, 10)
		if err != nil || total != 2 || len(messages) != 2 {
			t.Fatalf("messages=%d total=%d err=%v", len(messages), total, err)
		}
		if _, ok := messages[0].Sender.(domain.User); !ok {
			t.Fatalf("expected IAM user sender, got %T", messages[0].Sender)
		}
		if _, ok := messages[1].Sender.(domain.Contact); !ok {
			t.Fatalf("expected CUS contact sender, got %T", messages[1].Sender)
		}
		if msgDB.Migrator().HasTable(&domain.User{}) || msgDB.Migrator().HasTable(&domain.Contact{}) {
			t.Fatal("MSG created foreign owner tables")
		}
	})
	t.Run("reconcile_skips_stuck_change", func(t *testing.T) {
		conDB, msgDB := openStore("CON"), openStore("MSG")
		first := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1}
		second := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1}
		if err := conDB.Create(&first).Error; err != nil {
			t.Fatal(err)
		}
		if err := conDB.Create(&second).Error; err != nil {
			t.Fatal(err)
		}
		port := &recoverableConversationPort{owner: repository.NewConversationRepository(conDB), unavailable: true}
		messages := repository.NewMessageRepositoryWithConversationPort(msgDB, port)
		for _, conversationID := range []uint{first.ID, second.ID} {
			msg := domain.Message{AccountID: 1, ConversationID: conversationID, MessageType: domain.MessageTypeIncoming, Content: "pending"}
			if err := messages.Create(&msg); err != nil {
				t.Fatal(err)
			}
		}
		if err := conDB.Delete(&first).Error; err != nil {
			t.Fatal(err)
		}
		port.unavailable = false
		if count, err := messages.ReconcilePendingActivity(1); count != 0 || err == nil {
			t.Fatalf("expected stale first change to report error: count=%d err=%v", count, err)
		}
		if count, err := messages.ReconcilePendingActivity(1); count != 1 || err != nil {
			t.Fatalf("expected next bounded batch to progress: count=%d err=%v", count, err)
		}
		var updated domain.Conversation
		if err := conDB.First(&updated, second.ID).Error; err != nil || updated.UnreadCount != 1 {
			t.Fatalf("healthy conversation unread=%d err=%v", updated.UnreadCount, err)
		}
	})
}
