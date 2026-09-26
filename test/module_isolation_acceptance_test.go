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
	t.Run("CHN_policy_reference_is_composed_outside_owner_storage", func(t *testing.T) { testInboxPolicyBoundary(t, openStore) })
	t.Run("CHN_campaigns_are_queried_through_OPS", func(t *testing.T) { testInboxCampaignBoundary(t, openStore) })
	t.Run("CHN_agent_bot_bindings_are_owned_by_EXT", func(t *testing.T) { testInboxAgentBotBoundary(t, openStore) })
	t.Run("RTE_policy_fallbacks_are_composed_through_TEN_IAM", func(t *testing.T) { testAssignmentPolicyFallbackBoundary(t, openStore) })
	t.Run("RTE_routing_uses_CHN_TEN_IAM_CON_owner_queries", func(t *testing.T) { testRoutingServiceOwnerBoundary(t, openStore) })
	t.Run("RTE_snooze_scheduler_uses_CON_owner_command", func(t *testing.T) { testRoutingSnoozeOwnerBoundary(t, openStore) })
	t.Run("RTE_capacity_policies_use_TEN_CHN_owner_contracts", func(t *testing.T) { testAgentCapacityPolicyOwnerBoundary(t, openStore) })
	t.Run("RPT_applied_slas_use_CON_owner_queries", func(t *testing.T) { testAppliedSLAConversationBoundary(t, openStore) })
	t.Run("AIC_captain_uses_CHN_CON_MSG_owner_queries", func(t *testing.T) { testCaptainRepositoryOwnerBoundary(t, openStore) })
	t.Run("TEN_account_members_validate_IAM_before_write", func(t *testing.T) { testAccountRepositoryIAMBoundary(t, openStore) })
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
	t.Run("CON_find_hydrates_owner_data_without_foreign_tables", func(t *testing.T) {
		conDB, chnDB, cusDB, iamDB, tenDB := openStore("CON"), openStore("CHN"), openStore("CUS"), openStore("IAM"), openStore("TEN")
		inbox := domain.Inbox{AccountID: 1, Name: "Inbox"}
		contact := domain.Contact{AccountID: 1, Name: "Contact"}
		user := domain.User{Name: "Agent"}
		team := domain.Team{AccountID: 1, Name: "Team"}
		label := domain.Label{AccountID: 1, Title: "Priority"}
		for _, item := range []struct {
			db    *gorm.DB
			model any
		}{{chnDB, &inbox}, {cusDB, &contact}, {iamDB, &user}, {tenDB, &team}, {cusDB, &label}} {
			if err := item.db.Create(item.model).Error; err != nil {
				t.Fatal(err)
			}
		}
		conversation := domain.Conversation{AccountID: 1, InboxID: inbox.ID, ContactID: contact.ID, AssigneeID: &user.ID, TeamID: &team.ID}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		if err := conDB.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: label.ID}).Error; err != nil {
			t.Fatal(err)
		}
		owner := repository.NewConversationRepositoryWithReadPorts(conDB, nil, nil, repository.NewInboxRepository(chnDB), repository.NewContactRepository(cusDB), repository.NewUserRepository(iamDB), repository.NewTeamRepository(tenDB), repository.NewLabelRepository(cusDB))
		found, err := owner.FindByID(1, conversation.ID)
		if err != nil || found == nil {
			t.Fatalf("separate-owner read failed: found=%v err=%v", found, err)
		}
		if found.Inbox == nil || found.Contact == nil || found.Assignee == nil || found.Team == nil || len(found.Labels) != 1 {
			t.Fatalf("incomplete owner hydration: inbox=%v contact=%v assignee=%v team=%v labels=%d", found.Inbox, found.Contact, found.Assignee, found.Team, len(found.Labels))
		}
		listed, total, err := owner.List(1, "", "", nil, nil, 1, 10)
		if err != nil || total != 1 || len(listed) != 1 || len(listed[0].Labels) != 1 {
			t.Fatalf("cross-store conversation list: count=%d total=%d err=%v", len(listed), total, err)
		}
		filtered, total, err := owner.Filter(1, []domain.FilterRule{{AttributeKey: "labels", FilterOperator: "equal_to", Values: []any{"Priority"}}}, 1, 10)
		if err != nil || total != 1 || len(filtered) != 1 {
			t.Fatalf("cross-store label filter: count=%d total=%d err=%v", len(filtered), total, err)
		}
		if conDB.Migrator().HasTable(&domain.Inbox{}) || conDB.Migrator().HasTable(&domain.Contact{}) || conDB.Migrator().HasTable(&domain.User{}) {
			t.Fatal("CON created foreign owner tables")
		}
	})
	t.Run("CON_label_and_attachment_ports_use_owner_stores", func(t *testing.T) {
		conDB, cusDB, msgDB := openStore("CON"), openStore("CUS"), openStore("MSG")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		label := domain.Label{AccountID: 1, Title: "Account one"}
		foreignLabel := domain.Label{AccountID: 2, Title: "Account two"}
		if err := cusDB.Create(&label).Error; err != nil {
			t.Fatal(err)
		}
		if err := cusDB.Create(&foreignLabel).Error; err != nil {
			t.Fatal(err)
		}
		msg := domain.Message{AccountID: 1, ConversationID: conversation.ID, Content: "with attachment"}
		if err := msgDB.Create(&msg).Error; err != nil {
			t.Fatal(err)
		}
		attachment := domain.Attachment{AccountID: 1, MessageID: msg.ID, DataURL: "https://example.test/a"}
		if err := msgDB.Create(&attachment).Error; err != nil {
			t.Fatal(err)
		}
		owner := repository.NewConversationRepositoryWithReadPorts(conDB, repository.NewMessageRepositoryWithConversationPort(msgDB, nil), nil, nil, nil, nil, nil, repository.NewLabelRepository(cusDB))
		if err := owner.AttachLabel(1, conversation.ID, foreignLabel.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("foreign label accepted: %v", err)
		}
		if err := owner.AttachLabel(1, conversation.ID, label.ID); err != nil {
			t.Fatal(err)
		}
		attachments, err := owner.ListAttachments(1, conversation.ID)
		if err != nil || len(attachments) != 1 || attachments[0].ID != attachment.ID {
			t.Fatalf("owner attachment query: count=%d err=%v", len(attachments), err)
		}
		if err := owner.DetachLabel(1, conversation.ID, label.ID); err != nil {
			t.Fatal(err)
		}
		var links int64
		if err := conDB.Model(&domain.ConversationLabel{}).Count(&links).Error; err != nil || links != 0 {
			t.Fatalf("label relation remained: count=%d err=%v", links, err)
		}
		if conDB.Migrator().HasTable(&domain.Label{}) || conDB.Migrator().HasTable(&domain.Attachment{}) {
			t.Fatal("CON created foreign owner tables")
		}
	})
	t.Run("BIL_order_update_validates_CON_reference_without_foreign_table", func(t *testing.T) {
		bilDB, conDB := openStore("BIL"), openStore("CON")
		conversation := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
		order := domain.Order{AccountID: 1}
		if err := bilDB.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
		owner := repository.NewOrderRepositoryWithConversationPort(bilDB, repository.NewConversationRepository(conDB))
		order.ConversationID = &conversation.ID
		if err := owner.Update(1, &order); err != nil {
			t.Fatalf("BIL could not validate CON reference: %v", err)
		}
		if bilDB.Migrator().HasTable(&domain.Conversation{}) {
			t.Fatal("BIL created a foreign conversation table")
		}
		foreign := domain.Conversation{AccountID: 2, InboxID: 1, ContactID: 1}
		if err := conDB.Create(&foreign).Error; err != nil {
			t.Fatal(err)
		}
		order.ConversationID = &foreign.ID
		if err := owner.Update(1, &order); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("foreign CON reference accepted: %v", err)
		}
	})
	t.Run("SYS_configs_do_not_depend_on_ENT_storage", func(t *testing.T) {
		sysDB, entDB := openStore("SYS"), openStore("ENT")
		configs := repository.NewSystemConfigRepository(sysDB)
		features := repository.NewEnterpriseRepository(entDB)
		if err := configs.Set("site_name", "Example"); err != nil {
			t.Fatal(err)
		}
		value, err := configs.Get("site_name")
		if err != nil || value != "Example" {
			t.Fatalf("SYS config read: value=%q err=%v", value, err)
		}
		if err := features.SetAccountFeature(1, "chat", true); err != nil {
			t.Fatal(err)
		}
		if entDB.Migrator().HasTable(&domain.SystemConfig{}) || sysDB.Migrator().HasTable(&domain.AccountFeature{}) {
			t.Fatal("SYS or ENT created another owner's table")
		}
	})
	t.Run("TEN_team_members_use_IAM_without_foreign_writes", func(t *testing.T) {
		tenDB, iamDB := openStore("TEN"), openStore("IAM")
		user := domain.User{Name: "Original"}
		if err := iamDB.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		owner := repository.NewTeamRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB))
		team := domain.Team{AccountID: 1, Name: "Support"}
		if err := owner.Create(&team); err != nil {
			t.Fatal(err)
		}
		if err := owner.AddMember(team.ID, user.ID); err != nil {
			t.Fatal(err)
		}
		found, err := owner.FindByID(1, team.ID)
		if err != nil || found == nil || len(found.Members) != 1 || found.Members[0].ID != user.ID {
			t.Fatalf("team hydration failed: team=%v err=%v", found, err)
		}
		found.Members[0].Name = "Must not be written by TEN"
		found.Name = "Renamed"
		if err := owner.Update(found); err != nil {
			t.Fatal(err)
		}
		if err := iamDB.First(&user, user.ID).Error; err != nil || user.Name != "Original" {
			t.Fatalf("TEN wrote IAM data: user=%v err=%v", user, err)
		}
		if err := owner.Delete(2, team.ID); err != nil {
			t.Fatal(err)
		}
		listed, err := owner.List(1)
		if err != nil || len(listed) != 1 || len(listed[0].Members) != 1 {
			t.Fatalf("foreign account removed team members: teams=%v err=%v", listed, err)
		}
		if tenDB.Migrator().HasTable(&domain.User{}) {
			t.Fatal("TEN created IAM users table")
		}
		unwired := repository.NewTeamRepositoryWithUserPort(tenDB, nil)
		if _, err := unwired.ListMembers(team.ID); !errors.Is(err, gorm.ErrInvalidData) {
			t.Fatalf("missing IAM port hidden: %v", err)
		}
	})
	t.Run("TEN_account_members_hydrate_IAM_and_only_save_membership", func(t *testing.T) {
		tenDB, iamDB := openStore("TEN"), openStore("IAM")
		user := domain.User{Name: "Account agent"}
		if err := iamDB.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		owner := repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB))
		if err := owner.AddMember(1, user.ID, domain.RoleAgent); err != nil {
			t.Fatal(err)
		}
		members, err := owner.ListMembers(1)
		if err != nil || len(members) != 1 || members[0].User == nil || members[0].User.ID != user.ID {
			t.Fatalf("account hydration failed: members=%v err=%v", members, err)
		}
		members[0].User.Name = "Should stay in IAM"
		members[0].Role = domain.RoleAdministrator
		if err := owner.UpdateMember(&members[0]); err != nil {
			t.Fatal(err)
		}
		if err := iamDB.First(&user, user.ID).Error; err != nil || user.Name != "Account agent" {
			t.Fatalf("TEN wrote IAM: %v %v", user, err)
		}
		membership, err := owner.GetMembership(1, user.ID)
		if err != nil || membership == nil || membership.Role != domain.RoleAdministrator {
			t.Fatalf("membership not saved: %v %v", membership, err)
		}
		foreign, err := owner.ListMembers(2)
		if err != nil || len(foreign) != 0 {
			t.Fatalf("foreign members returned: %v %v", foreign, err)
		}
		if tenDB.Migrator().HasTable(&domain.User{}) {
			t.Fatal("TEN created IAM users table")
		}
	})
	t.Run("CHN_members_and_fallback_use_TEN_IAM_stores", func(t *testing.T) {
		chnDB, tenDB, iamDB := openStore("CHN"), openStore("TEN"), openStore("IAM")
		user := domain.User{Name: "Agent"}
		if err := iamDB.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		accounts := repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB))
		if err := accounts.AddMember(1, user.ID, domain.RoleAgent); err != nil {
			t.Fatal(err)
		}
		owner := repository.NewInboxRepositoryWithAccountPort(chnDB, accounts)
		inbox := domain.Inbox{AccountID: 1, Name: "Support", WebsiteToken: "split-chn"}
		if err := owner.Create(&inbox); err != nil {
			t.Fatal(err)
		}
		agents, err := owner.ListAssignableAgents(1, inbox.ID)
		if err != nil || len(agents) != 1 {
			t.Fatalf("account fallback: %v %v", agents, err)
		}
		if err := owner.AddMember(inbox.ID, user.ID); err != nil {
			t.Fatal(err)
		}
		found, err := owner.FindByID(1, inbox.ID)
		if err != nil || found == nil || len(found.Members) != 1 {
			t.Fatalf("inbox hydration: %v %v", found, err)
		}
		found.Members[0].Name = "Do not save"
		if err := owner.Update(found); err != nil {
			t.Fatal(err)
		}
		if err := iamDB.First(&user, user.ID).Error; err != nil || user.Name != "Agent" {
			t.Fatalf("foreign write: %v %v", user, err)
		}
		if _, err := owner.ListAssignableAgents(2, inbox.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("foreign inbox accepted: %v", err)
		}
		unwired := repository.NewInboxRepositoryWithAccountPort(chnDB, nil)
		if _, err := unwired.ListAssignableAgents(1, inbox.ID); !errors.Is(err, gorm.ErrInvalidData) {
			t.Fatalf("dependency failure hidden: %v", err)
		}
		if chnDB.Migrator().HasTable(&domain.User{}) || chnDB.Migrator().HasTable(&domain.AccountUser{}) {
			t.Fatal("CHN created foreign tables")
		}
	})
}
