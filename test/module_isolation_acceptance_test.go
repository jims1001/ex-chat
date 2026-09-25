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

type unavailableCleanupPort struct{}

func (unavailableCleanupPort) CleanupConversation(uint, uint) error {
	return errors.New("owner unavailable")
}

type deletingDuringCreatePort struct {
	owner *repository.ConversationRepository
	db    *gorm.DB
}

func (p *deletingDuringCreatePort) MessageTargetExists(accountID, conversationID uint) (bool, error) {
	return p.owner.MessageTargetExists(accountID, conversationID)
}

func (p *deletingDuringCreatePort) ApplyMessageActivity(accountID, conversationID, actorID uint, incoming bool, occurredAt time.Time, changeID string) error {
	if err := p.db.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", accountID, conversationID).Update("status", domain.ConversationStatusDeleting).Error; err != nil {
		return err
	}
	return p.owner.ApplyMessageActivity(accountID, conversationID, actorID, incoming, occurredAt, changeID)
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
		if count, err := messages.ReconcilePendingActivity(1); count != 1 || err != nil {
			t.Fatalf("expected stale first change to be cancelled: count=%d err=%v", count, err)
		}
		if count, err := messages.ReconcilePendingActivity(1); count != 1 || err != nil {
			t.Fatalf("expected next bounded batch to progress: count=%d err=%v", count, err)
		}
		var updated domain.Conversation
		if err := conDB.First(&updated, second.ID).Error; err != nil || updated.UnreadCount != 1 {
			t.Fatalf("healthy conversation unread=%d err=%v", updated.UnreadCount, err)
		}
	})
	t.Run("CON_delete_cleans_each_owner_store", func(t *testing.T) {
		conDB := openStore("CON")
		stores := map[string]*gorm.DB{}
		for _, owner := range []string{"MSG", "RPT", "QLT", "RTM", "TKT", "API", "AIC", "OPS", "BIL"} {
			stores[owner] = openStore(owner)
		}
		cleanup, err := repository.NewConversationCleanup(stores)
		if err != nil {
			t.Fatal(err)
		}
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		msg := domain.Message{AccountID: 1, ConversationID: conversation.ID, Content: "remove"}
		if err := stores["MSG"].Create(&msg).Error; err != nil {
			t.Fatal(err)
		}
		otherTenantMessage := domain.Message{AccountID: 2, ConversationID: conversation.ID, Content: "must remain"}
		if err := stores["MSG"].Create(&otherTenantMessage).Error; err != nil {
			t.Fatal(err)
		}
		for _, item := range []struct {
			owner string
			model any
		}{
			{"MSG", &domain.Attachment{AccountID: 1, MessageID: msg.ID, DataURL: "https://example.test/file"}},
			{"MSG", &domain.DraftMessage{AccountID: 1, ConversationID: conversation.ID}},
			{"QLT", &domain.CSATSurvey{AccountID: 1, ConversationID: conversation.ID}},
			{"API", &domain.WidgetEvent{AccountID: 1, ConversationID: &conversation.ID}},
			{"AIC", &domain.CopilotThread{AccountID: 1, ConversationID: &conversation.ID}},
			{"MSG", &domain.EmailLog{AccountID: 1, ConversationID: &conversation.ID}},
			{"TKT", &domain.Ticket{AccountID: 1, ConversationID: &conversation.ID, TicketNumber: "isolation-1", Title: "keep ticket"}},
			{"RTM", &domain.Call{AccountID: 1, ConversationID: &conversation.ID}},
			{"BIL", &domain.Order{AccountID: 1, ConversationID: &conversation.ID}},
		} {
			if err := stores[item.owner].Create(item.model).Error; err != nil {
				t.Fatalf("seed %s: %v", item.owner, err)
			}
		}
		owner := repository.NewConversationRepositoryWithPorts(conDB, repository.NewMessageRepositoryWithConversationPort(stores["MSG"], nil), cleanup)
		if err := owner.Delete(1, conversation.ID); err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := conDB.Model(&domain.Conversation{}).Where("id = ?", conversation.ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("CON retained deleted conversation: count=%d err=%v", count, err)
		}
		for _, check := range []struct {
			owner string
			model any
		}{
			{"MSG", &domain.Attachment{}}, {"MSG", &domain.DraftMessage{}},
			{"QLT", &domain.CSATSurvey{}}, {"API", &domain.WidgetEvent{}}, {"AIC", &domain.CopilotThread{}},
			{"MSG", &domain.EmailLog{}}, {"RTM", &domain.Call{}},
		} {
			if err := stores[check.owner].Model(check.model).Where("account_id = ?", 1).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("%s %T retained: count=%d err=%v", check.owner, check.model, count, err)
			}
		}
		if err := stores["MSG"].Model(&domain.Message{}).Where("account_id = ? AND id = ?", 1, msg.ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("target message remained: count=%d err=%v", count, err)
		}
		if err := stores["MSG"].Model(&domain.Message{}).Where("account_id = ? AND id = ?", 2, otherTenantMessage.ID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("other tenant message was removed: count=%d err=%v", count, err)
		}
		var ticket domain.Ticket
		if err := stores["TKT"].First(&ticket).Error; err != nil || ticket.ConversationID != nil {
			t.Fatalf("ticket link not cleared: link=%v err=%v", ticket.ConversationID, err)
		}
		if conDB.Migrator().HasTable(&domain.Message{}) {
			t.Fatal("CON created a foreign owner table")
		}
	})
	t.Run("CON_delete_resumes_after_owner_failure", func(t *testing.T) {
		conDB := openStore("CON")
		stores := map[string]*gorm.DB{}
		for _, owner := range []string{"MSG", "RPT", "QLT", "RTM", "TKT", "API", "AIC", "OPS", "BIL"} {
			stores[owner] = openStore(owner)
		}
		cleanup, err := repository.NewConversationCleanup(stores)
		if err != nil {
			t.Fatal(err)
		}
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		broken := repository.NewConversationRepositoryWithPorts(conDB, nil, unavailableCleanupPort{})
		if err := broken.Delete(1, conversation.ID); err == nil {
			t.Fatal("owner outage was hidden")
		}
		var pending domain.Conversation
		if err := conDB.First(&pending, conversation.ID).Error; err != nil || pending.Status != domain.ConversationStatusDeleting {
			t.Fatalf("missing durable deletion marker: status=%s err=%v", pending.Status, err)
		}
		if exists, err := broken.MessageTargetExists(1, conversation.ID); err != nil || exists {
			t.Fatalf("deleting conversation still accepts messages: exists=%v err=%v", exists, err)
		}
		recovered := repository.NewConversationRepositoryWithPorts(conDB, nil, cleanup)
		if count, err := recovered.ReconcilePendingDeletes(10); err != nil || count != 1 {
			t.Fatalf("delete recovery count=%d err=%v", count, err)
		}
		var changes int64
		if err := conDB.Model(&domain.LocalChangeJournal{}).Where("change_id = ? AND status = ?", "con-delete-1-1", "delivered").Count(&changes).Error; err != nil || changes != 1 {
			t.Fatalf("deletion journal=%d err=%v", changes, err)
		}
	})
	t.Run("CON_delete_replays_after_partial_owner_cleanup", func(t *testing.T) {
		conDB := openStore("CON")
		stores := map[string]*gorm.DB{}
		for _, owner := range []string{"MSG", "RPT", "QLT", "RTM", "TKT", "API", "AIC", "OPS", "BIL"} {
			stores[owner] = openStore(owner)
		}
		cleanup, err := repository.NewConversationCleanup(stores)
		if err != nil {
			t.Fatal(err)
		}
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		msg := domain.Message{AccountID: 1, ConversationID: conversation.ID, Content: "partial"}
		if err := stores["MSG"].Create(&msg).Error; err != nil {
			t.Fatal(err)
		}
		reportConn, err := stores["RPT"].DB()
		if err != nil {
			t.Fatal(err)
		}
		if err := reportConn.Close(); err != nil {
			t.Fatal(err)
		}
		owner := repository.NewConversationRepositoryWithPorts(conDB, nil, cleanup)
		if err := owner.Delete(1, conversation.ID); err == nil {
			t.Fatal("closed RPT store did not interrupt deletion")
		}
		var count int64
		if err := stores["MSG"].Model(&domain.Message{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("MSG owner cleanup did not commit before RPT failure: count=%d err=%v", count, err)
		}
		stores["RPT"] = openStore("RPT")
		recoveredCleanup, err := repository.NewConversationCleanup(stores)
		if err != nil {
			t.Fatal(err)
		}
		recovered := repository.NewConversationRepositoryWithPorts(conDB, nil, recoveredCleanup)
		if count, err := recovered.ReconcilePendingDeletes(10); err != nil || count != 1 {
			t.Fatalf("partial cleanup recovery count=%d err=%v", count, err)
		}
	})
	t.Run("CON_import_checks_CHN_CUS_owner_references", func(t *testing.T) {
		conDB, chnDB, cusDB := openStore("CON"), openStore("CHN"), openStore("CUS")
		inbox := domain.Inbox{AccountID: 1, Name: "Imported inbox"}
		contact := domain.Contact{AccountID: 1, Name: "Imported contact"}
		if err := chnDB.Create(&inbox).Error; err != nil {
			t.Fatal(err)
		}
		if err := cusDB.Create(&contact).Error; err != nil {
			t.Fatal(err)
		}
		owner := repository.NewConversationRepositoryWithReferences(conDB, nil, nil, repository.NewInboxRepository(chnDB), repository.NewContactRepository(cusDB))
		imported := domain.Conversation{AccountID: 1, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: 37}
		if err := owner.CreateImported(&imported); err != nil {
			t.Fatal(err)
		}
		if imported.DisplayID != 37 {
			t.Fatalf("historical display ID was changed: %d", imported.DisplayID)
		}
		foreign := domain.Conversation{AccountID: 2, InboxID: inbox.ID, ContactID: contact.ID}
		if err := owner.CreateImported(&foreign); err == nil {
			t.Fatal("cross-tenant imported references were accepted")
		}
		if conDB.Migrator().HasTable(&domain.Inbox{}) || conDB.Migrator().HasTable(&domain.Contact{}) {
			t.Fatal("CON created foreign owner reference tables")
		}
	})
	t.Run("MSG_create_cancels_if_CON_deletes_between_check_and_commit", func(t *testing.T) {
		conDB, msgDB := openStore("CON"), openStore("MSG")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		port := &deletingDuringCreatePort{owner: repository.NewConversationRepository(conDB), db: conDB}
		msg := domain.Message{AccountID: 1, ConversationID: conversation.ID, MessageType: domain.MessageTypeIncoming, Content: "raced"}
		if err := repository.NewMessageRepositoryWithConversationPort(msgDB, port).Create(&msg); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("expected deleted CON target to reject message: %v", err)
		}
		var count int64
		if err := msgDB.Model(&domain.Message{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("orphaned message remained: count=%d err=%v", count, err)
		}
		if err := msgDB.Model(&domain.LocalChangeJournal{}).Where("status = ? AND result = ?", "superseded", "rejected").Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("missing cancelled fact: count=%d err=%v", count, err)
		}
	})
	t.Run("MSG_replay_cancels_if_CON_was_deleted", func(t *testing.T) {
		conDB, msgDB := openStore("CON"), openStore("MSG")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		port := &recoverableConversationPort{owner: repository.NewConversationRepository(conDB), unavailable: true}
		repo := repository.NewMessageRepositoryWithConversationPort(msgDB, port)
		msg := domain.Message{AccountID: 1, ConversationID: conversation.ID, MessageType: domain.MessageTypeIncoming, Content: "pending orphan"}
		if err := repo.Create(&msg); err != nil {
			t.Fatal(err)
		}
		if err := conDB.Model(&domain.Conversation{}).Where("id = ?", conversation.ID).Update("status", domain.ConversationStatusDeleting).Error; err != nil {
			t.Fatal(err)
		}
		port.unavailable = false
		if count, err := repo.ReconcilePendingActivity(10); err != nil || count != 1 {
			t.Fatalf("orphan replay count=%d err=%v", count, err)
		}
		var count int64
		if err := msgDB.Model(&domain.Message{}).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("pending orphan remained: count=%d err=%v", count, err)
		}
	})
}
