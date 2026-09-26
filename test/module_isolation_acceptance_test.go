package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	t.Run("ORC_company_extensions_use_IAM_CON_MSG_owner_queries", func(t *testing.T) { testCompanyExtensionOwnerBoundary(t, openStore) })
	t.Run("ORC_contact_extensions_use_CHN_CON_MSG_owner_queries", func(t *testing.T) { testContactExtensionOwnerBoundary(t, openStore) })
	t.Run("ORC_contact_lifecycle_uses_all_owner_commands", func(t *testing.T) { testContactRepositoryOwnerBoundary(t, openStore) })
	t.Run("ORC_labels_use_CON_conversation_label_owner", func(t *testing.T) { testLabelRepositoryConversationBoundary(t, openStore) })
	t.Run("ORC_channel_enterprise_facade_uses_seven_owner_stores", func(t *testing.T) { testChannelEnterpriseOwnerBoundary(t, openStore) })
	t.Run("MSG_attachments_validate_message_owner", func(t *testing.T) { testAttachmentRepositoryMessageBoundary(t, openStore) })
	t.Run("OPS_macros_use_CON_TEN_CUS_MSG_owner_commands", func(t *testing.T) { testMacroRepositoryOwnerBoundary(t, openStore) })
	t.Run("EXT_platform_uses_TEN_IAM_owner_contracts", func(t *testing.T) { testPlatformRepositoryOwnerBoundary(t, openStore) })
	t.Run("API_public_uses_CHN_CUS_CON_MSG_owner_contracts", func(t *testing.T) { testPublicRepositoryOwnerBoundary(t, openStore) })
	t.Run("QLT_sampling_uses_TKT_CON_owner_queries", func(t *testing.T) { testQARepositoryOwnerBoundary(t, openStore) })
	t.Run("API_widget_uses_CHN_CUS_CON_OPS_MSG_owner_contracts", func(t *testing.T) { testWidgetRepositoryOwnerBoundary(t, openStore) })
	t.Run("TKT_uses_TEN_IAM_CUS_CON_and_keeps_RPT_opaque", func(t *testing.T) { testTicketRepositoryOwnerBoundary(t, openStore) })
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

func testQARepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	qltDB, tktDB, conDB := openStore("QLT"), openStore("TKT"), openStore("CON")
	agentID := uint(27)
	ticket := domain.Ticket{AccountID: 81, TicketNumber: "TK-QA-81", Title: "owner boundary", Status: "resolved", AssigneeID: &agentID}
	if err := tktDB.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 81, InboxID: 3, ContactID: 4, DisplayID: 5, Status: domain.ConversationStatusResolved, AssigneeID: &agentID}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}

	scorecard := domain.QAScorecard{AccountID: 81, Name: "owner scorecard", TotalScore: 100, PassingScore: 80, Status: "active"}
	if err := qltDB.Create(&scorecard).Error; err != nil {
		t.Fatal(err)
	}
	ticketRule := domain.QASamplingRule{AccountID: 81, Name: "ticket sampling", TargetType: "ticket", ScorecardID: scorecard.ID, Status: "active"}
	conversationRule := domain.QASamplingRule{AccountID: 81, Name: "conversation sampling", TargetType: "conversation", ScorecardID: scorecard.ID, Status: "active"}
	if err := qltDB.Create(&ticketRule).Error; err != nil {
		t.Fatal(err)
	}
	if err := qltDB.Create(&conversationRule).Error; err != nil {
		t.Fatal(err)
	}

	repo := repository.NewQARepositoryWithOwnerPorts(qltDB, repository.NewTicketRepository(tktDB), repository.NewConversationRepository(conDB))
	ticketTasks, err := repo.GenerateTasksFromSampling(81, ticketRule.ID)
	if err != nil || len(ticketTasks) != 1 || ticketTasks[0].TargetID != ticket.ID || ticketTasks[0].TargetRef != "工单 TK-QA-81" || ticketTasks[0].AgentID == nil || *ticketTasks[0].AgentID != agentID {
		t.Fatalf("TKT sampling boundary failed: tasks=%+v err=%v", ticketTasks, err)
	}
	conversationTasks, err := repo.GenerateTasksFromSampling(81, conversationRule.ID)
	if err != nil || len(conversationTasks) != 1 || conversationTasks[0].TargetID != conversation.ID || conversationTasks[0].TargetRef != fmt.Sprintf("会话 #%d", conversation.ID) || conversationTasks[0].AgentID == nil || *conversationTasks[0].AgentID != agentID {
		t.Fatalf("CON sampling boundary failed: tasks=%+v err=%v", conversationTasks, err)
	}
	var taskCount int64
	if err := qltDB.Model(&domain.QATask{}).Where("account_id = ?", 81).Count(&taskCount).Error; err != nil || taskCount != 2 {
		t.Fatalf("QLT task ownership failed: count=%d err=%v", taskCount, err)
	}
	if qltDB.Migrator().HasTable(&domain.Ticket{}) || qltDB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("QLT store contains TKT/CON authority tables")
	}
	if tktDB.Migrator().HasTable(&domain.QATask{}) || conDB.Migrator().HasTable(&domain.QATask{}) {
		t.Fatal("TKT/CON store contains QLT authority table")
	}
}

func testWidgetRepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	apiDB, chnDB, cusDB := openStore("API"), openStore("CHN"), openStore("CUS")
	conDB, opsDB, msgDB := openStore("CON"), openStore("OPS"), openStore("MSG")
	inbox := domain.Inbox{AccountID: 91, Name: "widget", ChannelType: "Channel::WebWidget", WebsiteToken: "widget-owner-token"}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	contact := domain.Contact{AccountID: 91, Name: "visitor", Identifier: "visitor-91"}
	if err := cusDB.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&domain.ContactInbox{ContactID: contact.ID, InboxID: inbox.ID, SourceID: "source-91"}).Error; err != nil {
		t.Fatal(err)
	}
	label := domain.Label{AccountID: 91, Title: "visitor-label"}
	if err := cusDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&domain.ContactLabel{ContactID: contact.ID, LabelID: label.ID}).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 91, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: 1, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: label.ID}).Error; err != nil {
		t.Fatal(err)
	}
	campaign := domain.Campaign{AccountID: 91, InboxID: inbox.ID, Title: "ongoing", Status: "active", CampaignType: "ongoing"}
	if err := opsDB.Create(&campaign).Error; err != nil {
		t.Fatal(err)
	}
	message := domain.Message{AccountID: 91, ConversationID: conversation.ID, Content: "widget message", MessageType: domain.MessageTypeIncoming}
	if err := msgDB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}

	conversations := &repository.ConversationRepository{}
	// Public constructors preserve the owner boundary while each repository uses its own store.
	conversations = repository.NewConversationRepository(conDB)
	contacts := repository.NewContactRepository(cusDB)
	messages := repository.NewMessageRepositoryWithConversationPort(msgDB, conversations)
	labels := repository.NewLabelRepositoryWithConversationPort(cusDB, conversations)
	repo := repository.NewWidgetRepositoryWithOwnerPorts(apiDB, repository.NewInboxRepository(chnDB), contacts, conversations, repository.NewCampaignRepository(opsDB), labels, messages)

	gotInbox, err := repo.GetInboxByWebsiteToken(context.Background(), inbox.WebsiteToken)
	if err != nil || gotInbox == nil || gotInbox.ID != inbox.ID {
		t.Fatalf("CHN widget inbox failed: inbox=%+v err=%v", gotInbox, err)
	}
	gotContact, err := repo.FindContactBySourceID(context.Background(), inbox.ID, "source-91")
	if err != nil || gotContact == nil || gotContact.ID != contact.ID {
		t.Fatalf("CUS widget contact failed: contact=%+v err=%v", gotContact, err)
	}
	rows, total, err := repo.ListContactConversations(context.Background(), 91, inbox.ID, contact.ID, 1, 25)
	if err != nil || total != 1 || len(rows) != 1 || len(rows[0].Messages) != 1 || len(rows[0].Labels) != 1 {
		t.Fatalf("CON/MSG/CUS widget view failed: rows=%+v total=%d err=%v", rows, total, err)
	}
	campaigns, err := repo.ListActiveCampaigns(context.Background(), 91, inbox.ID)
	if err != nil || len(campaigns) != 1 || campaigns[0].ID != campaign.ID {
		t.Fatalf("OPS widget campaign failed: campaigns=%+v err=%v", campaigns, err)
	}
	updated, err := repo.UpdateMessageSubmittedValues(context.Background(), message.ID, conversation.ID, map[string]any{"choice": "yes"}, "submitted")
	if err != nil || updated.Content != "submitted" || !strings.Contains(updated.ContentAttributes, "submitted_values") {
		t.Fatalf("MSG widget update failed: message=%+v err=%v", updated, err)
	}
	event := domain.WidgetEvent{AccountID: 91, InboxID: inbox.ID, ContactID: &contact.ID, ConversationID: &conversation.ID, Name: domain.WidgetEventOpened}
	if err := repo.RecordWidgetEvent(context.Background(), &event); err != nil {
		t.Fatal(err)
	}
	if apiDB.Migrator().HasTable(&domain.Inbox{}) || apiDB.Migrator().HasTable(&domain.Contact{}) || apiDB.Migrator().HasTable(&domain.Conversation{}) || apiDB.Migrator().HasTable(&domain.Campaign{}) || apiDB.Migrator().HasTable(&domain.Message{}) || apiDB.Migrator().HasTable(&domain.Label{}) {
		t.Fatal("API store contains widget foreign authority tables")
	}
	for name, db := range map[string]*gorm.DB{"CHN": chnDB, "CUS": cusDB, "CON": conDB, "OPS": opsDB, "MSG": msgDB} {
		if db.Migrator().HasTable(&domain.WidgetEvent{}) {
			t.Fatalf("%s store contains API WidgetEvent table", name)
		}
	}
}

func testTicketRepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	tktDB, tenDB, iamDB := openStore("TKT"), openStore("TEN"), openStore("IAM")
	cusDB, conDB, rptDB := openStore("CUS"), openStore("CON"), openStore("RPT")
	user := domain.User{Name: "ticket owner", Email: "ticket-owner@example.test"}
	if err := iamDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 101, UserID: user.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	contact := domain.Contact{AccountID: 101, Name: "ticket contact"}
	if err := cusDB.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 101, InboxID: 1, ContactID: contact.ID, DisplayID: 1}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	sla := domain.SLAPolicy{AccountID: 101, Name: "ticket sla"}
	if err := rptDB.Create(&sla).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewTicketRepositoryWithOwnerPorts(tktDB, repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB)), repository.NewUserRepository(iamDB), repository.NewContactRepository(cusDB), repository.NewConversationRepository(conDB))
	ticket := domain.Ticket{AccountID: 101, Title: "isolated ticket", AssigneeID: &user.ID, CreatorID: &user.ID, ContactID: &contact.ID, ConversationID: &conversation.ID, SLAPolicyID: &sla.ID}
	if err := repo.Create(&ticket); err != nil {
		t.Fatalf("owner reference validation failed: %v", err)
	}
	if err := repo.AddWatcher(101, ticket.ID, user.ID); err != nil {
		t.Fatalf("TEN watcher validation failed: %v", err)
	}
	loaded, err := repo.FindByID(101, ticket.ID)
	if err != nil || loaded.Assignee == nil || loaded.Creator == nil || loaded.Contact == nil || loaded.Conversation == nil {
		t.Fatalf("owner hydration failed: ticket=%+v err=%v", loaded, err)
	}
	watchers, err := repo.ListWatchers(101, ticket.ID)
	if err != nil || len(watchers) != 1 || watchers[0].User == nil || watchers[0].User.ID != user.ID {
		t.Fatalf("IAM watcher hydration failed: watchers=%+v err=%v", watchers, err)
	}
	foreign := uint(999999)
	if err := repo.ValidateReferences(101, &foreign, nil, nil, nil); err == nil {
		t.Fatal("foreign TEN member accepted")
	}
	if err := repo.ValidateReferences(101, nil, &foreign, nil, nil); err == nil {
		t.Fatal("foreign CUS contact accepted")
	}
	if err := repo.ValidateReferences(101, nil, nil, &foreign, nil); err == nil {
		t.Fatal("foreign CON conversation accepted")
	}
	if err := repo.ValidateReferences(101, nil, nil, nil, &foreign); err != nil {
		t.Fatalf("TKT attempted to validate opaque RPT reference: %v", err)
	}
	for _, model := range []any{&domain.AccountUser{}, &domain.User{}, &domain.Contact{}, &domain.Conversation{}, &domain.SLAPolicy{}} {
		if tktDB.Migrator().HasTable(model) {
			t.Fatalf("TKT store contains foreign authority table %T", model)
		}
	}
	for name, db := range map[string]*gorm.DB{"TEN": tenDB, "IAM": iamDB, "CUS": cusDB, "CON": conDB, "RPT": rptDB} {
		if db.Migrator().HasTable(&domain.Ticket{}) {
			t.Fatalf("%s store contains TKT authority table", name)
		}
	}
}

func testMacroRepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	opsDB, conDB, tenDB, cusDB, msgDB := openStore("OPS"), openStore("CON"), openStore("TEN"), openStore("CUS"), openStore("MSG")
	member := domain.AccountUser{AccountID: 41, UserID: 51, Role: domain.RoleAgent}
	team := domain.Team{AccountID: 41, Name: "Macro Team"}
	conversation := domain.Conversation{AccountID: 41, InboxID: 1, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusOpen}
	for _, fixture := range []struct {
		db    *gorm.DB
		value any
	}{{tenDB, &member}, {tenDB, &team}, {conDB, &conversation}} {
		if err := fixture.db.Create(fixture.value).Error; err != nil {
			t.Fatal(err)
		}
	}
	actions, _ := json.Marshal([]map[string]any{
		{"action_name": "change_priority", "action_params": []any{"urgent"}},
		{"action_name": "assign_agent", "action_params": []any{member.UserID}},
		{"action_name": "assign_team", "action_params": []any{team.ID}},
		{"action_name": "add_label", "action_params": []any{"vip"}},
		{"action_name": "send_message", "action_params": []any{"owner-routed reply"}},
		{"action_name": "send_attachment", "action_params": map[string]any{"file_url": "https://files.example/macro.txt", "file_type": "text/plain", "file_size": 12}},
	})
	macro := domain.Macro{AccountID: 41, Name: "Owner ports", Actions: string(actions), Visibility: "global"}
	if err := opsDB.Create(&macro).Error; err != nil {
		t.Fatal(err)
	}
	conversations := repository.NewConversationRepository(conDB)
	repo := repository.NewMacroRepositoryWithOwnerPorts(opsDB, conversations, repository.NewAccountRepository(tenDB), repository.NewTeamRepository(tenDB), repository.NewLabelRepositoryWithConversationPort(cusDB, conversations), repository.NewMessageRepositoryWithConversationPort(msgDB, conversations))
	result, err := repo.ExecuteForUser(context.Background(), 41, macro.ID, member.UserID, true, []uint{conversation.ID})
	if err != nil || result[conversation.ID] != "success" {
		t.Fatalf("macro result=%v err=%v", result, err)
	}
	var updated domain.Conversation
	if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.Priority != "urgent" || updated.AssigneeID == nil || *updated.AssigneeID != member.UserID || updated.TeamID == nil || *updated.TeamID != team.ID {
		t.Fatalf("CON mutation=%+v err=%v", updated, err)
	}
	var labelCount, relationCount, messageCount, attachmentCount int64
	_ = cusDB.Model(&domain.Label{}).Where("account_id = ? AND title = ?", 41, "vip").Count(&labelCount).Error
	_ = conDB.Model(&domain.ConversationLabel{}).Where("conversation_id = ?", conversation.ID).Count(&relationCount).Error
	_ = msgDB.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", 41, conversation.ID).Count(&messageCount).Error
	_ = msgDB.Model(&domain.Attachment{}).Where("account_id = ?", 41).Count(&attachmentCount).Error
	if labelCount != 1 || relationCount != 1 || messageCount != 2 || attachmentCount != 1 {
		t.Fatalf("owner counts label=%d relation=%d message=%d attachment=%d", labelCount, relationCount, messageCount, attachmentCount)
	}
	for name, db := range map[string]*gorm.DB{"OPS": opsDB, "CON": conDB, "TEN": tenDB, "CUS": cusDB, "MSG": msgDB} {
		foreign := map[string]any{"OPS": &domain.Conversation{}, "CON": &domain.Macro{}, "TEN": &domain.Label{}, "CUS": &domain.Message{}, "MSG": &domain.Team{}}[name]
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("%s contains a foreign authority table", name)
		}
	}
}

func testPlatformRepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	extDB, tenDB, iamDB := openStore("EXT"), openStore("TEN"), openStore("IAM")
	users := repository.NewUserRepository(iamDB)
	accounts := repository.NewAccountRepositoryWithUserPort(tenDB, users)
	repo := repository.NewPlatformRepositoryWithOwnerPorts(extDB, accounts, users)
	account := domain.Account{Name: "Isolated tenant", Domain: "isolated.example"}
	user := domain.User{Name: "Platform User", Email: "platform-owner@example.com", Role: domain.RoleAgent}
	if err := repo.CreateAccount(context.Background(), &account); err != nil {
		t.Fatal(err)
	}
	if err := users.Create(&user); err != nil {
		t.Fatal(err)
	}
	member, err := repo.AddUserToAccount(context.Background(), account.ID, user.ID, domain.RoleAdministrator)
	if err != nil || member.User == nil || member.User.Email != user.Email {
		t.Fatalf("member=%+v err=%v", member, err)
	}
	listedAccounts, accountTotal, err := repo.ListAccounts(context.Background(), "isolated", 1, 10)
	if err != nil || accountTotal != 1 || len(listedAccounts) != 1 {
		t.Fatalf("accounts=%d total=%d err=%v", len(listedAccounts), accountTotal, err)
	}
	listedUsers, userTotal, err := repo.ListUsers(context.Background(), "platform-owner", domain.RoleAgent, 1, 10)
	if err != nil || userTotal != 1 || len(listedUsers) != 1 {
		t.Fatalf("users=%d total=%d err=%v", len(listedUsers), userTotal, err)
	}
	members, memberTotal, err := repo.ListAccountUsers(context.Background(), account.ID, 1, 10)
	if err != nil || memberTotal != 1 || len(members) != 1 || members[0].User == nil {
		t.Fatalf("members=%+v total=%d err=%v", members, memberTotal, err)
	}
	gotUser, memberships, err := repo.GetUser(context.Background(), user.ID)
	if err != nil || gotUser.Email != user.Email || len(memberships) != 1 || memberships[0].Account == nil {
		t.Fatalf("user=%+v memberships=%+v err=%v", gotUser, memberships, err)
	}
	if err := repo.DeleteUser(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	if membership, err := accounts.GetMembership(account.ID, user.ID); err != nil || membership != nil {
		t.Fatalf("membership survived user delete: %+v err=%v", membership, err)
	}
	if deleted, err := users.FindByID(user.ID); err != nil || deleted != nil {
		t.Fatalf("IAM user survived delete: %+v err=%v", deleted, err)
	}
	for name, check := range map[string]struct {
		db    *gorm.DB
		model any
	}{
		"EXT-account": {extDB, &domain.Account{}}, "EXT-user": {extDB, &domain.User{}}, "EXT-member": {extDB, &domain.AccountUser{}},
		"TEN-user": {tenDB, &domain.User{}}, "IAM-account": {iamDB, &domain.Account{}},
	} {
		if check.db.Migrator().HasTable(check.model) {
			t.Fatalf("%s contains a foreign authority table", name)
		}
	}
}

func testPublicRepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	chnDB, cusDB, conDB, msgDB := openStore("CHN"), openStore("CUS"), openStore("CON"), openStore("MSG")
	inbox := domain.Inbox{AccountID: 71, Name: "Public Inbox", WebsiteToken: "public-owner-token", ChannelType: "Channel::WebWidget"}
	contact := domain.Contact{AccountID: 71, Name: "Public Contact", Identifier: "public-contact", CustomAttributes: `{"tier":"basic"}`}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	relation := domain.ContactInbox{ContactID: contact.ID, InboxID: inbox.ID, SourceID: "public-source"}
	if err := cusDB.Create(&relation).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 71, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: 1, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	publicMessage := domain.Message{AccountID: 71, ConversationID: conversation.ID, Content: "visible", Private: false}
	privateMessage := domain.Message{AccountID: 71, ConversationID: conversation.ID, Content: "hidden", Private: true}
	if err := msgDB.Create(&publicMessage).Error; err != nil {
		t.Fatal(err)
	}
	if err := msgDB.Create(&privateMessage).Error; err != nil {
		t.Fatal(err)
	}
	inboxes, contacts := repository.NewInboxRepository(chnDB), repository.NewContactRepository(cusDB)
	messages := repository.NewMessageRepositoryWithConversationPort(msgDB, nil)
	conversations := repository.NewConversationRepositoryWithReadPorts(conDB, messages, nil, inboxes, contacts, nil, nil, nil)
	repo := repository.NewPublicRepositoryWithOwnerPorts(inboxes, contacts, conversations, messages)
	gotInbox, err := repo.FindInboxByIdentifier(context.Background(), inbox.WebsiteToken)
	if err != nil || gotInbox == nil || gotInbox.ID != inbox.ID {
		t.Fatalf("inbox=%+v err=%v", gotInbox, err)
	}
	gotContact, gotRelation, err := repo.ResolveContact(context.Background(), inbox.ID, 71, relation.SourceID)
	if err != nil || gotContact == nil || gotContact.ID != contact.ID || gotRelation == nil {
		t.Fatalf("contact=%+v relation=%+v err=%v", gotContact, gotRelation, err)
	}
	updatedContact, err := repo.UpdateContact(context.Background(), 71, contact.ID, map[string]any{"name": "Updated Public Contact"}, map[string]any{"tier": "gold"})
	if err != nil || updatedContact.Name != "Updated Public Contact" || !strings.Contains(updatedContact.CustomAttributes, "gold") {
		t.Fatalf("updated contact=%+v err=%v", updatedContact, err)
	}
	listed, total, err := repo.ListContactConversations(context.Background(), 71, inbox.ID, contact.ID, "all", 1, 10)
	if err != nil || total != 1 || len(listed) != 1 || len(listed[0].Messages) != 1 || listed[0].Messages[0].Content != "visible" {
		t.Fatalf("conversations=%+v total=%d err=%v", listed, total, err)
	}
	detail, err := repo.GetContactConversation(context.Background(), 71, inbox.ID, contact.ID, conversation.ID)
	if err != nil || detail == nil || len(detail.Messages) != 1 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	updatedConversation, err := repo.UpdateConversationStatus(context.Background(), 71, conversation.ID, domain.ConversationStatusResolved)
	if err != nil || updatedConversation.Status != domain.ConversationStatusResolved {
		t.Fatalf("conversation=%+v err=%v", updatedConversation, err)
	}
	attributes, err := repo.MergeConversationCustomAttributes(context.Background(), 71, conversation.ID, map[string]any{"source": "public"})
	if err != nil || attributes["source"] != "public" {
		t.Fatalf("attributes=%+v err=%v", attributes, err)
	}
	for name, check := range map[string]struct {
		db    *gorm.DB
		model any
	}{
		"CHN-contact": {chnDB, &domain.Contact{}}, "CUS-conversation": {cusDB, &domain.Conversation{}},
		"CON-message": {conDB, &domain.Message{}}, "MSG-conversation": {msgDB, &domain.Conversation{}},
	} {
		if check.db.Migrator().HasTable(check.model) {
			t.Fatalf("%s contains a foreign authority table", name)
		}
	}
}

func testCompanyExtensionOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	cusDB, iamDB, chnDB, conDB, msgDB := openStore("CUS"), openStore("IAM"), openStore("CHN"), openStore("CON"), openStore("MSG")
	company := domain.Company{AccountID: 1, Name: "Boundary Company"}
	if err := cusDB.Create(&company).Error; err != nil {
		t.Fatal(err)
	}
	contact := domain.Contact{AccountID: 1, CompanyID: &company.ID, Name: "Boundary Contact"}
	if err := cusDB.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	user := domain.User{Name: "Note Author", Email: "company-boundary@example.test"}
	if err := iamDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	inbox := domain.Inbox{AccountID: 1, Name: "Boundary Inbox", ChannelType: "web_widget"}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 1, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen, LastActivityAt: time.Now().UTC()}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	message := domain.Message{AccountID: 1, ConversationID: conversation.ID, SenderType: "contact", SenderID: contact.ID, Content: "company attachment"}
	if err := msgDB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	attachment := domain.Attachment{AccountID: 1, MessageID: message.ID, FileType: "image/png", DataURL: "https://example.test/company.png"}
	if err := msgDB.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}

	contactRepo := repository.NewContactRepository(cusDB)
	convRepo := repository.NewConversationRepositoryWithReadPorts(conDB, nil, nil, repository.NewInboxRepository(chnDB), contactRepo, nil, nil, nil)
	msgRepo := repository.NewMessageRepositoryWithPorts(msgDB, convRepo, repository.NewUserRepository(iamDB), contactRepo)
	repo := repository.NewCompanyExtensionRepositoryWithOwnerPorts(cusDB, repository.NewUserRepository(iamDB), convRepo, msgRepo)
	note := domain.CompanyNote{AccountID: 1, CompanyID: company.ID, UserID: user.ID, Content: "owner composed note"}
	if err := repo.CreateCompanyNote(&note); err != nil || note.User == nil || note.User.Email != user.Email {
		t.Fatalf("IAM note hydration failed: user=%+v err=%v", note.User, err)
	}
	updatedNote, err := repo.UpdateCompanyNote(1, company.ID, note.ID, "updated through CUS")
	if err != nil || updatedNote == nil || updatedNote.User == nil || updatedNote.Content != "updated through CUS" {
		t.Fatalf("CUS note update wrote an IAM association: note=%+v err=%v", updatedNote, err)
	}
	conversations, total, err := repo.ListCompanyConversations(1, company.ID, "", nil, nil, 1, 25)
	if err != nil || total != 1 || len(conversations) != 1 || conversations[0].Contact == nil || conversations[0].Inbox == nil {
		t.Fatalf("CON composition failed: total=%d rows=%d err=%v", total, len(conversations), err)
	}
	stats, err := repo.GetCompanyStats(1, company.ID)
	if err != nil || stats.ConversationsCount != 1 || stats.MessagesCount != 1 || stats.AttachmentsCount != 1 {
		t.Fatalf("owner stats failed: stats=%+v err=%v", stats, err)
	}
	attachments, total, err := repo.ListCompanyAttachments(1, company.ID, repository.CompanyAttachmentFilter{Page: 1, PageSize: 25})
	if err != nil || total != 1 || len(attachments) != 1 || attachments[0].ConversationID != conversation.ID {
		t.Fatalf("MSG attachment composition failed: total=%d rows=%d err=%v", total, len(attachments), err)
	}
	foreignContact := contact.ID + 999
	attachments, total, err = repo.ListCompanyAttachments(1, company.ID, repository.CompanyAttachmentFilter{ContactID: &foreignContact})
	if err != nil || total != 0 || len(attachments) != 0 {
		t.Fatalf("foreign contact escaped company scope: total=%d rows=%d err=%v", total, len(attachments), err)
	}
	if cusDB.Migrator().HasTable(&domain.User{}) || cusDB.Migrator().HasTable(&domain.Conversation{}) || cusDB.Migrator().HasTable(&domain.Message{}) || cusDB.Migrator().HasTable(&domain.Attachment{}) {
		t.Fatal("CUS store contains foreign owner tables")
	}
}

func testContactExtensionOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	cusDB, chnDB, conDB, msgDB := openStore("CUS"), openStore("CHN"), openStore("CON"), openStore("MSG")
	contact := domain.Contact{AccountID: 1, Name: "Boundary Contact", Email: "contact-boundary@example.test"}
	if err := cusDB.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	inbox := domain.Inbox{AccountID: 1, Name: "Boundary Email", ChannelType: "Channel::Email", WebsiteToken: "contact-boundary-1"}
	foreignInbox := domain.Inbox{AccountID: 2, Name: "Foreign Email", ChannelType: "Channel::Email", WebsiteToken: "contact-boundary-2"}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := chnDB.Create(&foreignInbox).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 1, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen, LastActivityAt: time.Now().UTC()}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	message := domain.Message{AccountID: 1, ConversationID: conversation.ID, SenderType: "contact", SenderID: contact.ID, Content: "contact attachment"}
	if err := msgDB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	attachment := domain.Attachment{AccountID: 1, MessageID: message.ID, FileType: "image/png", DataURL: "https://example.test/contact.png"}
	if err := msgDB.Create(&attachment).Error; err != nil {
		t.Fatal(err)
	}

	contactRepo := repository.NewContactRepository(cusDB)
	inboxRepo := repository.NewInboxRepository(chnDB)
	convRepo := repository.NewConversationRepositoryWithReadPorts(conDB, nil, nil, inboxRepo, contactRepo, nil, nil, nil)
	msgRepo := repository.NewMessageRepositoryWithPorts(msgDB, convRepo, nil, contactRepo)
	repo := repository.NewContactExtensionRepositoryWithOwnerPorts(cusDB, inboxRepo, convRepo, msgRepo)

	contactable, err := repo.GetContactableInboxes(1, contact.ID)
	if err != nil || len(contactable) != 1 || contactable[0].Inbox.ID != inbox.ID {
		t.Fatalf("CHN contactable inbox composition failed: rows=%+v err=%v", contactable, err)
	}
	link, err := repo.CreateContactInbox(1, contact.ID, inbox.ID, "")
	if err != nil || link.Inbox == nil || link.SourceID != contact.Email {
		t.Fatalf("CHN validated contact inbox create failed: link=%+v err=%v", link, err)
	}
	if _, err := repo.CreateContactInbox(1, contact.ID, foreignInbox.ID, "foreign"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign CHN inbox was accepted: %v", err)
	}
	links, err := repo.ListContactInboxes(1, contact.ID)
	if err != nil || len(links) != 1 || links[0].Inbox == nil {
		t.Fatalf("CHN inbox hydration failed: rows=%+v err=%v", links, err)
	}
	conversations, total, err := repo.ListContactConversations(1, contact.ID, "", nil, 1, 25)
	if err != nil || total != 1 || len(conversations) != 1 || conversations[0].Inbox == nil {
		t.Fatalf("CON conversation composition failed: total=%d rows=%d err=%v", total, len(conversations), err)
	}
	attachments, total, err := repo.ListContactAttachments(1, contact.ID, repository.ContactAttachmentFilter{Page: 1, PageSize: 25})
	if err != nil || total != 1 || len(attachments) != 1 {
		t.Fatalf("MSG attachment composition failed: total=%d rows=%d err=%v", total, len(attachments), err)
	}
	stats, err := repo.GetContactStats(1, contact.ID)
	if err != nil || stats.ConversationsCount != 1 || stats.MessagesCount != 1 || stats.AttachmentsCount != 1 || stats.ContactInboxesCount != 1 {
		t.Fatalf("owner contact stats failed: stats=%+v err=%v", stats, err)
	}
	if cusDB.Migrator().HasTable(&domain.Inbox{}) || cusDB.Migrator().HasTable(&domain.Conversation{}) || cusDB.Migrator().HasTable(&domain.Message{}) || cusDB.Migrator().HasTable(&domain.Attachment{}) {
		t.Fatal("CUS store contains CHN/CON/MSG authority tables")
	}
}

func testContactRepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	cusDB, conDB, msgDB := openStore("CUS"), openStore("CON"), openStore("MSG")
	tktDB, bilDB, opsDB, rtmDB, apiDB := openStore("TKT"), openStore("BIL"), openStore("OPS"), openStore("RTM"), openStore("API")
	base, mergee := domain.Contact{AccountID: 1, Name: "Base"}, domain.Contact{AccountID: 1, Name: "Mergee"}
	if err := cusDB.Create(&base).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&mergee).Error; err != nil {
		t.Fatal(err)
	}
	conv := domain.Conversation{AccountID: 1, ContactID: mergee.ID, Status: domain.ConversationStatusResolved}
	if err := conDB.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	msg := domain.Message{AccountID: 1, ConversationID: conv.ID, SenderType: "contact", SenderID: mergee.ID, Content: "merge"}
	if err := msgDB.Create(&msg).Error; err != nil {
		t.Fatal(err)
	}
	ticketContact := mergee.ID
	ticket := domain.Ticket{AccountID: 1, TicketNumber: "T-MERGE", Title: "merge", ContactID: &ticketContact}
	if err := tktDB.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	comment := domain.TicketComment{AccountID: 1, TicketID: ticket.ID, ContactID: &ticketContact, Content: "merge"}
	if err := tktDB.Create(&comment).Error; err != nil {
		t.Fatal(err)
	}
	order := domain.Order{AccountID: 1, OrderID: "O-MERGE", ContactID: &ticketContact}
	if err := bilDB.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	delivery := domain.CampaignDelivery{AccountID: 1, CampaignID: 1, ContactID: mergee.ID}
	if err := opsDB.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	call := domain.Call{AccountID: 1, ContactID: mergee.ID, InboxID: 1}
	if err := rtmDB.Create(&call).Error; err != nil {
		t.Fatal(err)
	}
	eventContact := mergee.ID
	event := domain.WidgetEvent{AccountID: 1, InboxID: 1, ContactID: &eventContact, Name: "merge"}
	if err := apiDB.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	convRepo := repository.NewConversationRepository(conDB)
	repo := repository.NewContactRepositoryWithOwnerPorts(cusDB, convRepo, repository.NewMessageRepositoryWithConversationPort(msgDB, nil), repository.NewTicketRepository(tktDB), repository.NewOrderRepositoryWithConversationPort(bilDB, nil), repository.NewCampaignRepository(opsDB))
	if err := repo.MergeContacts(1, base.ID, mergee.ID); err != nil {
		t.Fatal(err)
	}
	// RTM consumes cus.contact.merged.v1 because a synchronous ORC -> RTM edge would create a dependency cycle.
	if err := repository.NewCallRepository(rtmDB).ReassignContact(1, mergee.ID, base.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.NewWidgetRepository(apiDB).ReassignContact(context.Background(), 1, mergee.ID, base.ID); err != nil {
		t.Fatal(err)
	}
	for name, check := range map[string]func() error{
		"CON": func() error {
			return conDB.Where("id = ? AND contact_id = ?", conv.ID, base.ID).First(&domain.Conversation{}).Error
		},
		"MSG": func() error {
			return msgDB.Where("id = ? AND sender_id = ?", msg.ID, base.ID).First(&domain.Message{}).Error
		},
		"TKT": func() error {
			return tktDB.Where("id = ? AND contact_id = ?", ticket.ID, base.ID).First(&domain.Ticket{}).Error
		},
		"BIL": func() error {
			return bilDB.Where("id = ? AND contact_id = ?", order.ID, base.ID).First(&domain.Order{}).Error
		},
		"OPS": func() error {
			return opsDB.Where("id = ? AND contact_id = ?", delivery.ID, base.ID).First(&domain.CampaignDelivery{}).Error
		},
		"RTM": func() error {
			return rtmDB.Where("id = ? AND contact_id = ?", call.ID, base.ID).First(&domain.Call{}).Error
		},
		"API": func() error {
			return apiDB.Where("id = ? AND contact_id = ?", event.ID, base.ID).First(&domain.WidgetEvent{}).Error
		},
	} {
		if err := check(); err != nil {
			t.Fatalf("%s contact reference was not reassigned: %v", name, err)
		}
	}
	if cusDB.Migrator().HasTable(&domain.Conversation{}) || cusDB.Migrator().HasTable(&domain.Message{}) || cusDB.Migrator().HasTable(&domain.Ticket{}) || cusDB.Migrator().HasTable(&domain.Order{}) || cusDB.Migrator().HasTable(&domain.Call{}) || cusDB.Migrator().HasTable(&domain.WidgetEvent{}) {
		t.Fatal("CUS contains a foreign owner table")
	}
}

func testLabelRepositoryConversationBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	cusDB, conDB := openStore("CUS"), openStore("CON")
	label := domain.Label{AccountID: 1, Title: "priority", Color: "#f00"}
	foreignLabel := domain.Label{AccountID: 2, Title: "foreign", Color: "#000"}
	if err := cusDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&foreignLabel).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 1, Status: domain.ConversationStatusOpen}
	foreignConversation := domain.Conversation{AccountID: 2, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&foreignConversation).Error; err != nil {
		t.Fatal(err)
	}

	repo := repository.NewLabelRepositoryWithConversationPort(cusDB, repository.NewConversationRepository(conDB))
	if err := repo.AttachToConversation(1, conversation.ID, label.ID); err != nil {
		t.Fatalf("CON relation attach failed: %v", err)
	}
	labels, err := repo.GetConversationLabels(1, conversation.ID)
	if err != nil || len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("CON/CUS label composition failed: labels=%+v err=%v", labels, err)
	}
	if err := repo.AttachToConversation(1, foreignConversation.ID, label.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-account conversation accepted: %v", err)
	}
	if err := repo.AttachToConversation(1, conversation.ID, foreignLabel.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-account label accepted: %v", err)
	}
	if err := repo.DetachFromConversation(1, conversation.ID, label.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.AttachToConversation(1, conversation.ID, label.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(1, label.ID); err != nil {
		t.Fatalf("owner-coordinated label delete failed: %v", err)
	}
	var relationCount int64
	if err := conDB.Model(&domain.ConversationLabel{}).Where("conversation_id = ? AND label_id = ?", conversation.ID, label.ID).Count(&relationCount).Error; err != nil || relationCount != 0 {
		t.Fatalf("CON label relation was not deleted: count=%d err=%v", relationCount, err)
	}
	if cusDB.Migrator().HasTable(&domain.Conversation{}) || cusDB.Migrator().HasTable(&domain.ConversationLabel{}) {
		t.Fatal("CUS store contains CON authority tables")
	}
	if conDB.Migrator().HasTable(&domain.Label{}) {
		t.Fatal("CON store contains CUS authority tables")
	}
}

func testChannelEnterpriseOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	iamDB, rtmDB, migDB, rptDB := openStore("IAM"), openStore("RTM"), openStore("MIG"), openStore("RPT")
	entDB, bilDB, tenDB := openStore("ENT"), openStore("BIL"), openStore("TEN")
	repo := repository.NewChannelAuthEnterpriseRepositoryWithOwnerStores(iamDB, rtmDB, migDB, rptDB, entDB, bilDB, tenDB)

	if err := repo.SaveSAMLSetting(&domain.SAMLSetting{AccountID: 1, Enabled: true}); err != nil {
		t.Fatalf("IAM write failed: %v", err)
	}
	call := domain.Call{AccountID: 1, InboxID: 10, ProviderCallID: "owner-call"}
	if err := repo.CreateCall(&call); err != nil {
		t.Fatalf("RTM write failed: %v", err)
	}
	if err := repo.CreateDataImport(&domain.DataImport{AccountID: 1}); err != nil {
		t.Fatalf("MIG write failed: %v", err)
	}
	if err := repo.CreateReportingEvent(&domain.ReportingEvent{AccountID: 1, Name: "owner.report"}); err != nil {
		t.Fatalf("RPT write failed: %v", err)
	}
	if err := repo.SaveOnboarding(&domain.Onboarding{AccountID: 1, Step: "profile"}); err != nil {
		t.Fatalf("TEN write failed: %v", err)
	}
	if err := repo.SaveBrandedEmailLayout(&domain.BrandedEmailLayout{AccountID: 1}); err != nil {
		t.Fatalf("ENT write failed: %v", err)
	}
	billing := domain.AccountBilling{AccountID: 1, ActionType: "deposit", Amount: 25, Currency: "USD"}
	brokenENT, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "broken-ent.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	failedRepo := repository.NewChannelAuthEnterpriseRepositoryWithOwnerStores(iamDB, rtmDB, migDB, rptDB, brokenENT, bilDB, tenDB)
	if err := failedRepo.RecordBilling(&billing); err == nil {
		t.Fatal("ENT owner failure was hidden")
	}
	if err := repo.RecordBilling(&billing); err != nil {
		t.Fatalf("BIL to ENT retry failed: %v", err)
	}
	var billingCount int64
	if err := bilDB.Model(&domain.AccountBilling{}).Where("id = ?", billing.ID).Count(&billingCount).Error; err != nil || billingCount != 1 {
		t.Fatalf("BIL record missing: count=%d err=%v", billingCount, err)
	}
	limit, err := repo.GetAccountLimit(1)
	if err != nil || limit.Credits != 1025 {
		t.Fatalf("ENT credit projection failed: limit=%+v err=%v", limit, err)
	}

	checks := []struct {
		name string
		db   *gorm.DB
		bad  any
	}{
		{"IAM", iamDB, &domain.Call{}},
		{"RTM", rtmDB, &domain.SAMLSetting{}},
		{"MIG", migDB, &domain.ReportingEvent{}},
		{"RPT", rptDB, &domain.DataImport{}},
		{"ENT", entDB, &domain.AccountBilling{}},
		{"BIL", bilDB, &domain.AccountLimit{}},
		{"TEN", tenDB, &domain.BrandedEmailLayout{}},
	}
	for _, check := range checks {
		if check.db.Migrator().HasTable(check.bad) {
			t.Fatalf("%s store contains a foreign owner table", check.name)
		}
	}
}

func testAttachmentRepositoryMessageBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	msgDB := openStore("MSG")
	message := domain.Message{AccountID: 1, ConversationID: 10, Content: "attachment owner", MessageType: domain.MessageTypeIncoming}
	foreignMessage := domain.Message{AccountID: 2, ConversationID: 20, Content: "foreign", MessageType: domain.MessageTypeIncoming}
	if err := msgDB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := msgDB.Create(&foreignMessage).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewAttachmentRepository(msgDB)
	attachment := domain.Attachment{AccountID: 1, MessageID: message.ID, FileType: "text/plain", DataURL: "https://example.test/a.txt"}
	if err := repo.Create(context.Background(), &attachment); err != nil {
		t.Fatalf("MSG attachment create failed: %v", err)
	}
	if err := repo.Create(context.Background(), &domain.Attachment{AccountID: 1, MessageID: foreignMessage.ID}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-account message accepted: %v", err)
	}
	attachments, err := repo.ListByMessage(context.Background(), 1, message.ID)
	if err != nil || len(attachments) != 1 || attachments[0].ID != attachment.ID {
		t.Fatalf("account-scoped attachment query failed: rows=%+v err=%v", attachments, err)
	}
	if msgDB.Migrator().HasTable(&domain.Company{}) || msgDB.Migrator().HasTable(&domain.AgentBot{}) || msgDB.Migrator().HasTable(&domain.SLAPolicy{}) {
		t.Fatal("MSG store contains CUS/EXT/RPT authority tables")
	}
}
