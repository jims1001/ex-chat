package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
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
	t.Run("RPT_SLA_service_uses_CHN_CON_MSG_owner_queries", func(t *testing.T) { testSLAServiceOwnerBoundary(t, openStore) })
	t.Run("RPT_reports_use_CHN_CON_MSG_CUS_owner_queries", func(t *testing.T) { testReportServiceOwnerBoundary(t, openStore) })
	t.Run("MIG_data_import_uses_CUS_contact_owner", func(t *testing.T) { testDataImportContactOwnerBoundary(t, openStore) })
	t.Run("OPS_campaign_audience_uses_CON_CUS_label_owners", func(t *testing.T) { testCampaignAudienceOwnerBoundary(t, openStore) })
	t.Run("OPS_campaign_conversations_use_CON_owner", func(t *testing.T) { testCampaignConversationOwnerBoundary(t, openStore) })
	t.Run("SRH_search_uses_CUS_CON_MSG_KB_owner_queries", func(t *testing.T) { testSearchHandlerOwnerBoundary(t, openStore) })
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
	t.Run("QLT_QA_views_use_IAM_owner_queries", func(t *testing.T) { testQAUserOwnerBoundary(t, openStore) })
	t.Run("KB_articles_use_IAM_author_query", func(t *testing.T) { testPortalAuthorOwnerBoundary(t, openStore) })
	t.Run("CHN_health_uses_CON_MSG_delivery_queries", func(t *testing.T) { testInboxHealthOwnerBoundary(t, openStore) })
	t.Run("AUT_assignments_use_TEN_IAM_owner_queries", func(t *testing.T) { testAutomationAssignmentOwnerBoundary(t, openStore) })
	t.Run("AUT_conditions_use_CUS_contact_company_query", func(t *testing.T) { testAutomationContactOwnerBoundary(t, openStore) })
	t.Run("AUT_label_conditions_use_CON_CUS_owner_queries", func(t *testing.T) { testAutomationLabelOwnerBoundary(t, openStore) })
	t.Run("API_widget_uses_CHN_CUS_CON_OPS_MSG_owner_contracts", func(t *testing.T) { testWidgetRepositoryOwnerBoundary(t, openStore) })
	t.Run("API_widget_message_read_uses_CON_MSG_owner_queries", func(t *testing.T) { testWidgetMessageReadOwnerBoundary(t, openStore) })
	t.Run("TKT_uses_TEN_IAM_CUS_CON_and_keeps_RPT_opaque", func(t *testing.T) { testTicketRepositoryOwnerBoundary(t, openStore) })
	t.Run("CON_inbox_move_uses_CHN_owner_query", func(t *testing.T) { testConversationMoveInboxBoundary(t, openStore) })
	t.Run("AIC_copilot_threads_use_IAM_CON_owner_queries", func(t *testing.T) { testCopilotThreadOwnerBoundary(t, openStore) })
	t.Run("QLT_csat_extensions_use_CON_CUS_CHN_IAM_owner_queries", func(t *testing.T) { testCSATExtensionOwnerBoundary(t, openStore) })
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

func testQAUserOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	qltDB, iamDB := openStore("QLT"), openStore("IAM")
	users := []domain.User{
		{Name: "QA inspector", Email: "qa-inspector@example.test"},
		{Name: "QA second inspector", Email: "qa-second@example.test"},
		{Name: "QA agent", Email: "qa-agent@example.test"},
		{Name: "QA appellant", Email: "qa-appellant@example.test"},
		{Name: "QA reviewer", Email: "qa-reviewer@example.test"},
	}
	for i := range users {
		if err := iamDB.Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	scorecard := domain.QAScorecard{AccountID: 82, Name: "IAM scorecard", TotalScore: 100, PassingScore: 85, Status: "active"}
	if err := qltDB.Create(&scorecard).Error; err != nil {
		t.Fatal(err)
	}
	rule := domain.QASamplingRule{AccountID: 82, Name: "IAM sampling", TargetType: "conversation", ScorecardID: scorecard.ID, AssignedInspectorID: &users[0].ID, Status: "active"}
	if err := qltDB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewQARepositoryWithOwnerPortsAndUsers(qltDB, nil, nil, repository.NewUserRepository(iamDB))
	task := domain.QATask{AccountID: 82, TargetType: "conversation", TargetID: 1, ScorecardID: scorecard.ID, InspectorID: &users[0].ID, SecondInspectorID: &users[1].ID, AgentID: &users[2].ID}
	if err := repo.CreateTask(&task); err != nil {
		t.Fatal(err)
	}
	appeal := domain.QAAppeal{AccountID: 82, TaskID: task.ID, AppealNumber: "AP-IAM-1", AppellantID: users[3].ID, ReviewerID: &users[4].ID, Reason: "owner boundary", Status: "pending"}
	if err := qltDB.Create(&appeal).Error; err != nil {
		t.Fatal(err)
	}
	activity := domain.QAAppealActivity{AccountID: 82, AppealID: appeal.ID, ActorID: users[3].ID, Action: "submitted"}
	if err := qltDB.Create(&activity).Error; err != nil {
		t.Fatal(err)
	}
	rules, err := repo.ListSamplingRules(82)
	if err != nil || len(rules) != 1 || rules[0].AssignedInspector == nil || rules[0].AssignedInspector.ID != users[0].ID {
		t.Fatalf("sampling inspector hydration failed: rules=%+v err=%v", rules, err)
	}
	gotTask, err := repo.GetTaskByID(82, task.ID)
	if err != nil || gotTask.Inspector == nil || gotTask.SecondInspector == nil || gotTask.Agent == nil || len(gotTask.Appeals) != 1 || gotTask.Appeals[0].Appellant == nil || gotTask.Appeals[0].Reviewer == nil {
		t.Fatalf("QA task IAM hydration failed: task=%+v err=%v", gotTask, err)
	}
	gotAppeal, err := repo.GetAppealByID(82, appeal.ID)
	if err != nil || gotAppeal.Appellant == nil || gotAppeal.Reviewer == nil || gotAppeal.Task == nil || gotAppeal.Task.Inspector == nil || len(gotAppeal.Activities) != 1 || gotAppeal.Activities[0].Actor == nil {
		t.Fatalf("QA appeal IAM hydration failed: appeal=%+v err=%v", gotAppeal, err)
	}
	if qltDB.Migrator().HasTable(&domain.User{}) {
		t.Fatal("QLT store contains IAM User table")
	}
}

func testPortalAuthorOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	kbDB, iamDB := openStore("KB"), openStore("IAM")
	author := domain.User{Name: "Knowledge author", Email: "kb-author@example.test"}
	if err := iamDB.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	portal := domain.Portal{AccountID: 141, Name: "Help", Slug: "help-141"}
	if err := kbDB.Create(&portal).Error; err != nil {
		t.Fatal(err)
	}
	category := domain.Category{AccountID: 141, PortalID: portal.ID, Name: "General", Slug: "general"}
	if err := kbDB.Create(&category).Error; err != nil {
		t.Fatal(err)
	}
	article := domain.Article{AccountID: 141, PortalID: portal.ID, CategoryID: category.ID, AuthorID: author.ID, Title: "Owner boundary", Slug: "owner-boundary", Content: "content", Status: "published"}
	if err := kbDB.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewPortalRepositoryWithUserPort(kbDB, repository.NewUserRepository(iamDB))
	articles, err := repo.ListArticles(portal.ID, nil, "published", "")
	if err != nil || len(articles) != 1 || articles[0].Category == nil || articles[0].Author == nil || articles[0].Author.ID != author.ID {
		t.Fatalf("KB article list hydration failed: articles=%+v err=%v", articles, err)
	}
	bySlug, err := repo.FindArticleBySlug(portal.ID, article.Slug)
	if err != nil || bySlug == nil || bySlug.Author == nil || bySlug.Author.ID != author.ID {
		t.Fatalf("KB article slug hydration failed: article=%+v err=%v", bySlug, err)
	}
	byID, err := repo.FindArticleByID(article.ID)
	if err != nil || byID == nil || byID.Author == nil || byID.Author.ID != author.ID {
		t.Fatalf("KB article ID hydration failed: article=%+v err=%v", byID, err)
	}
	if kbDB.Migrator().HasTable(&domain.User{}) {
		t.Fatal("KB store contains IAM User table")
	}
}

func testInboxHealthOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	chnDB, tenDB, conDB, msgDB := openStore("CHN"), openStore("TEN"), openStore("CON"), openStore("MSG")
	inbox := domain.Inbox{AccountID: 151, Name: "health", ChannelType: "Channel::WebWidget", WebsiteToken: "health-token", WebhookURL: "/webhook"}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 151, InboxID: inbox.ID, ContactID: 1, DisplayID: 1}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		message := domain.Message{AccountID: 151, ConversationID: conversation.ID, MessageType: domain.MessageTypeOutgoing, Status: "failed", Content: "failed", CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second)}
		if err := msgDB.Create(&message).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := repository.NewInboxRepositoryWithOwnerPorts(chnDB, repository.NewAccountRepository(tenDB), repository.NewConversationRepository(conDB), repository.NewMessageRepository(msgDB))
	health, err := repo.CheckChannelHealth(151, inbox.ID)
	if err != nil || health.Status != "unhealthy" {
		t.Fatalf("owner delivery health failed: health=%+v err=%v", health, err)
	}
	delivery, ok := health.Checks["delivery"].(map[string]any)
	if !ok || delivery["consecutive_failures"] != 3 {
		t.Fatalf("delivery owner stats missing: %+v", health.Checks["delivery"])
	}
	if chnDB.Migrator().HasTable(&domain.Conversation{}) || chnDB.Migrator().HasTable(&domain.Message{}) {
		t.Fatal("CHN store contains CON or MSG authority table")
	}
}

func testAutomationAssignmentOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	autDB, conDB, tenDB, iamDB := openStore("AUT"), openStore("CON"), openStore("TEN"), openStore("IAM")
	user := domain.User{Name: "Automation Agent", Email: "automation-owner@example.com", Role: domain.RoleAgent}
	if err := iamDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	member := domain.AccountUser{AccountID: 161, UserID: user.ID, Role: domain.RoleAgent}
	team := domain.Team{AccountID: 161, Name: "Automation Team"}
	for _, value := range []any{&member, &team} {
		if err := tenDB.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	conversation := domain.Conversation{AccountID: 161, InboxID: 1, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	actions, _ := json.Marshal([]map[string]any{
		{"action_name": "assign_agent", "action_params": map[string]any{"user_id": user.ID}},
		{"action_name": "assign_team", "action_params": map[string]any{"team_id": team.ID}},
	})
	rule := domain.AutomationRule{AccountID: 161, Name: "Owner assignments", EventName: "conversation_created", Conditions: "[]", Actions: string(actions), Active: true}
	if err := autDB.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	conversations := repository.NewConversationRepository(conDB)
	accounts := repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB))
	cusDB := openStore("CUS")
	service := service.NewAutomationServiceWithOwnerPorts(autDB, conversations, repository.NewMessageRepository(openStore("MSG")), repository.NewLabelRepositoryWithConversationPort(cusDB, conversations), accounts, repository.NewTeamRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB)), repository.NewContactRepository(cusDB))
	service.HandleConversationCreated(&conversation)
	var updated domain.Conversation
	if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.AssigneeID == nil || *updated.AssigneeID != user.ID || updated.TeamID == nil || *updated.TeamID != team.ID {
		t.Fatalf("AUT owner assignment failed: conversation=%+v err=%v", updated, err)
	}
	for name, model := range map[string]any{"TEN": &domain.AccountUser{}, "IAM": &domain.User{}, "CON": &domain.Conversation{}} {
		if autDB.Migrator().HasTable(model) {
			t.Fatalf("AUT store contains %s authority table", name)
		}
	}
}

func testAutomationContactOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	autDB, conDB, cusDB := openStore("AUT"), openStore("CON"), openStore("CUS")
	company := domain.Company{AccountID: 171, Name: "Owner Company"}
	if err := cusDB.Create(&company).Error; err != nil {
		t.Fatal(err)
	}
	contact := domain.Contact{AccountID: 171, CompanyID: &company.ID, Name: "Owner Contact", Email: "owner@company.example", PhoneNumber: "+61000000001", CustomAttributes: `{"country_code":"AU","city":"Sydney"}`}
	if err := cusDB.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	valid := domain.Conversation{AccountID: 171, InboxID: 1, ContactID: contact.ID, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: "medium"}
	foreign := domain.Conversation{AccountID: 172, InboxID: 1, ContactID: contact.ID, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: "medium"}
	for _, conversation := range []*domain.Conversation{&valid, &foreign} {
		if err := conDB.Create(conversation).Error; err != nil {
			t.Fatal(err)
		}
	}
	conditions := `[{"attribute_key":"company_name","filter_operator":"equal_to","values":["Owner Company"]},{"attribute_key":"email","filter_operator":"contains","values":["company.example"],"query_operator":"AND"}]`
	actions := `[{"action_name":"change_priority","action_params":{"priority":"urgent"}}]`
	for _, accountID := range []uint{171, 172} {
		rule := domain.AutomationRule{AccountID: accountID, Name: "CUS owner condition", EventName: "conversation_created", Conditions: conditions, Actions: actions, Active: true}
		if err := autDB.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
	}
	conversations := repository.NewConversationRepository(conDB)
	automation := service.NewAutomationServiceWithOwnerPorts(autDB, conversations, repository.NewMessageRepository(openStore("MSG")), repository.NewLabelRepositoryWithConversationPort(cusDB, conversations), nil, nil, repository.NewContactRepository(cusDB))
	automation.HandleConversationCreated(&valid)
	automation.HandleConversationCreated(&foreign)
	var validResult, foreignResult domain.Conversation
	_ = conDB.First(&validResult, valid.ID).Error
	_ = conDB.First(&foreignResult, foreign.ID).Error
	if validResult.Priority != "urgent" || foreignResult.Priority == "urgent" {
		t.Fatalf("CUS owner condition scope failed: valid=%s foreign=%s", validResult.Priority, foreignResult.Priority)
	}
	if autDB.Migrator().HasTable(&domain.Contact{}) || autDB.Migrator().HasTable(&domain.Company{}) {
		t.Fatal("AUT store contains CUS authority table")
	}
}

func testAutomationLabelOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	autDB, conDB, cusDB := openStore("AUT"), openStore("CON"), openStore("CUS")
	label := domain.Label{AccountID: 181, Title: "owner-vip"}
	if err := cusDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	valid := domain.Conversation{AccountID: 181, InboxID: 1, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: "medium"}
	foreign := domain.Conversation{AccountID: 182, InboxID: 1, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: "medium"}
	for _, conversation := range []*domain.Conversation{&valid, &foreign} {
		if err := conDB.Create(conversation).Error; err != nil {
			t.Fatal(err)
		}
		if err := conDB.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: label.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	conditions := `[{"attribute_key":"labels","filter_operator":"equal_to","values":["owner-vip"]}]`
	actions := `[{"action_name":"change_priority","action_params":{"priority":"urgent"}}]`
	for _, accountID := range []uint{181, 182} {
		rule := domain.AutomationRule{AccountID: accountID, Name: "Owner label condition", EventName: "conversation_created", Conditions: conditions, Actions: actions, Active: true}
		if err := autDB.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
	}
	conversations := repository.NewConversationRepository(conDB)
	labels := repository.NewLabelRepositoryWithConversationPort(cusDB, conversations)
	automation := service.NewAutomationServiceWithOwnerPorts(autDB, conversations, repository.NewMessageRepository(openStore("MSG")), labels, nil, nil, repository.NewContactRepository(cusDB))
	automation.HandleConversationCreated(&valid)
	automation.HandleConversationCreated(&foreign)
	var validResult, foreignResult domain.Conversation
	_ = conDB.First(&validResult, valid.ID).Error
	_ = conDB.First(&foreignResult, foreign.ID).Error
	if validResult.Priority != "urgent" || foreignResult.Priority == "urgent" {
		t.Fatalf("CON/CUS owner label condition scope failed: valid=%s foreign=%s", validResult.Priority, foreignResult.Priority)
	}
	if autDB.Migrator().HasTable(&domain.Label{}) || autDB.Migrator().HasTable(&domain.ConversationLabel{}) {
		t.Fatal("AUT store contains CUS/CON label authority table")
	}
}

func testSLAServiceOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	rptDB, conDB, chnDB, msgDB, tenDB := openStore("RPT"), openStore("CON"), openStore("CHN"), openStore("MSG"), openStore("TEN")
	account := domain.Account{ID: 191, Name: "SLA Tenant"}
	if err := tenDB.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	inbox := domain.Inbox{AccountID: 191, Name: "SLA Inbox", ChannelType: "Channel::WebWidget", Timezone: "UTC", WorkingHoursEnabled: false}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Add(-10 * time.Minute)
	conversation := domain.Conversation{AccountID: 191, InboxID: inbox.ID, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: "medium", CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	policy := domain.SLAPolicy{AccountID: 191, Name: "Owner SLA", FirstResponseTimeThreshold: 60, OnlyDuringBusinessHours: true}
	if err := rptDB.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	for _, message := range []*domain.Message{
		{AccountID: 191, ConversationID: conversation.ID, MessageType: domain.MessageTypeIncoming, SenderType: domain.SenderTypeContact, CreatedAt: createdAt.Add(time.Minute)},
		{AccountID: 191, ConversationID: conversation.ID, MessageType: domain.MessageTypeOutgoing, SenderType: domain.SenderTypeUser, CreatedAt: createdAt.Add(9 * time.Minute)},
	} {
		if err := msgDB.Create(message).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := service.NewSLAService(rptDB, repository.NewConversationRepository(conDB))
	service.SetOwnerRepositories(repository.NewInboxRepository(chnDB), repository.NewMessageRepository(msgDB), repository.NewAccountRepository(tenDB))
	frt, _, _, breached, _, _, selected := service.GetConversationSLADeadlines(&conversation)
	if frt == nil || !breached || selected == nil || selected.ID != policy.ID || conversation.Inbox == nil || conversation.Inbox.ID != inbox.ID {
		t.Fatalf("SLA owner composition failed: frt=%v breached=%v policy=%+v inbox=%+v", frt, breached, selected, conversation.Inbox)
	}
	if breaches := service.ProcessAllBreaches(); breaches != 1 {
		t.Fatalf("expected one breach from TEN-owned account enumeration, got %d", breaches)
	}
	for name, model := range map[string]any{"TEN": &domain.Account{}, "CHN": &domain.Inbox{}, "CON": &domain.Conversation{}, "MSG": &domain.Message{}} {
		if rptDB.Migrator().HasTable(model) {
			t.Fatalf("RPT store contains %s authority table", name)
		}
	}
}

func testReportServiceOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	rptDB, conDB, chnDB, msgDB, cusDB, tenDB, iamDB, extDB := openStore("RPT"), openStore("CON"), openStore("CHN"), openStore("MSG"), openStore("CUS"), openStore("TEN"), openStore("IAM"), openStore("EXT")
	inbox := domain.Inbox{AccountID: 192, Name: "Report Inbox", ChannelType: "Channel::WebWidget"}
	contact := domain.Contact{AccountID: 192, Name: "Report Contact"}
	label := domain.Label{AccountID: 192, Title: "priority", Color: "red"}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	user := domain.User{Name: "Report Agent", Email: "report-agent@example.com", PasswordHash: "test"}
	if err := iamDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	team := domain.Team{AccountID: 192, Name: "Report Team"}
	if err := tenDB.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 192, UserID: user.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.TeamMember{TeamID: team.ID, UserID: user.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := extDB.Create(&domain.AgentBotInbox{AccountID: 192, AgentBotID: 1, InboxID: inbox.ID}).Error; err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Add(-time.Hour)
	conversation := domain.Conversation{AccountID: 192, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: 1, Status: domain.ConversationStatusResolved, AssigneeID: &user.ID, TeamID: &team.ID, CreatedAt: createdAt, UpdatedAt: createdAt.Add(30 * time.Minute)}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: label.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := msgDB.Create(&domain.Message{AccountID: 192, ConversationID: conversation.ID, MessageType: domain.MessageTypeIncoming, CreatedAt: createdAt}).Error; err != nil {
		t.Fatal(err)
	}
	if err := msgDB.Create(&domain.Message{AccountID: 192, ConversationID: conversation.ID, MessageType: domain.MessageTypeOutgoing, SenderType: domain.SenderTypeUser, SenderID: user.ID, CreatedAt: createdAt.Add(5 * time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	reports := service.NewReportService(rptDB)
	reports.SetOwnerRepositories(repository.NewConversationRepository(conDB), repository.NewInboxRepository(chnDB), repository.NewMessageRepository(msgDB), repository.NewContactRepository(cusDB), repository.NewLabelRepositoryWithConversationPort(cusDB, repository.NewConversationRepository(conDB)))
	userRepo := repository.NewUserRepository(iamDB)
	reports.SetWorkforceRepositories(repository.NewAccountRepositoryWithUserPort(tenDB, userRepo), repository.NewTeamRepositoryWithUserPort(tenDB, userRepo), userRepo)
	reports.SetBotOwner(repository.NewAgentBotRepository(extDB))
	summary, err := reports.GetAccountSummary(192)
	if err != nil {
		t.Fatal(err)
	}
	labels, err := reports.GetLabelMetrics(192)
	if err != nil {
		t.Fatal(err)
	}
	channels, err := reports.GetChannelSummary(192, service.ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	agents, err := reports.GetAgentMetrics(192)
	if err != nil {
		t.Fatal(err)
	}
	teams, err := reports.GetTeamMetrics(192)
	if err != nil {
		t.Fatal(err)
	}
	outgoing, err := reports.GetOutgoingMessagesCount(192, service.ReportFilter{}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	botMetrics, err := reports.GetBotMetrics(192, service.ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalConversations != 1 || summary.TotalMessages != 2 || summary.TotalContacts != 1 || len(labels) != 1 || labels[0].ConversationCount != 1 || channels["Channel::WebWidget"].Resolved != 1 || len(agents) != 1 || agents[0].Name != user.Name || len(teams) != 1 || teams[0].MemberCount != 1 || len(outgoing) != 1 || outgoing[0].Name != user.Name || botMetrics.ConversationCount != 1 || botMetrics.MessageCount != 1 {
		t.Fatalf("unexpected owner-composed report: summary=%+v labels=%+v channels=%+v", summary, labels, channels)
	}
	for name, model := range map[string]any{"EXT": &domain.AgentBotInbox{}, "IAM": &domain.User{}, "TEN": &domain.AccountUser{}, "CHN": &domain.Inbox{}, "CON": &domain.Conversation{}, "MSG": &domain.Message{}, "CUS": &domain.Contact{}} {
		if rptDB.Migrator().HasTable(model) {
			t.Fatalf("RPT store contains %s authority table", name)
		}
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

func testWidgetMessageReadOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	conDB, msgDB := openStore("CON"), openStore("MSG")
	conversation := domain.Conversation{AccountID: 92, InboxID: 21, ContactID: 31, DisplayID: 1, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	message := domain.Message{AccountID: 92, ConversationID: conversation.ID, Content: "widget boundary", MessageType: domain.MessageTypeIncoming, SenderType: domain.SenderTypeContact}
	if err := msgDB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}

	conversations := repository.NewConversationRepository(conDB)
	messages := repository.NewMessageRepositoryWithConversationPort(msgDB, conversations)
	gotMessage, err := messages.FindByID(92, message.ID)
	if err != nil || gotMessage == nil || gotMessage.ConversationID != conversation.ID {
		t.Fatalf("MSG widget message lookup failed: message=%+v err=%v", gotMessage, err)
	}
	contactID := uint(31)
	gotConversation, err := conversations.FindWidgetMessageScope(92, 21, gotMessage.ConversationID, &contactID)
	if err != nil || gotConversation == nil || gotConversation.ID != conversation.ID {
		t.Fatalf("CON widget scope lookup failed: conversation=%+v err=%v", gotConversation, err)
	}
	wrongContactID := uint(32)
	if got, err := conversations.FindWidgetMessageScope(92, 21, gotMessage.ConversationID, &wrongContactID); err != nil || got != nil {
		t.Fatalf("CON exposed another visitor conversation: conversation=%+v err=%v", got, err)
	}
	if got, err := conversations.FindWidgetMessageScope(92, 22, gotMessage.ConversationID, nil); err != nil || got != nil {
		t.Fatalf("CON exposed another inbox conversation: conversation=%+v err=%v", got, err)
	}
	if got, err := messages.FindByID(93, message.ID); err != nil || got != nil {
		t.Fatalf("MSG exposed another account message: message=%+v err=%v", got, err)
	}
	if conDB.Migrator().HasTable(&domain.Message{}) || msgDB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("CON/MSG isolated stores contain a foreign authority table")
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

func testConversationMoveInboxBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	conDB, chnDB := openStore("CON"), openStore("CHN")
	source := domain.Inbox{AccountID: 111, Name: "source", WebsiteToken: "move-source"}
	target := domain.Inbox{AccountID: 111, Name: "target", WebsiteToken: "move-target"}
	foreign := domain.Inbox{AccountID: 112, Name: "foreign", WebsiteToken: "move-foreign"}
	for _, inbox := range []*domain.Inbox{&source, &target, &foreign} {
		if err := chnDB.Create(inbox).Error; err != nil {
			t.Fatal(err)
		}
	}
	conversation := domain.Conversation{AccountID: 111, InboxID: source.ID, ContactID: 1, DisplayID: 1}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewConversationRepositoryWithReferences(conDB, nil, nil, repository.NewInboxRepository(chnDB), nil)
	if err := repo.MoveInbox(111, source.ID, target.ID); err != nil {
		t.Fatalf("CHN owner move failed: %v", err)
	}
	var moved domain.Conversation
	if err := conDB.First(&moved, conversation.ID).Error; err != nil || moved.InboxID != target.ID {
		t.Fatalf("conversation was not moved: inbox=%d err=%v", moved.InboxID, err)
	}
	if err := repo.MoveInbox(111, target.ID, foreign.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign CHN inbox accepted: %v", err)
	}
	if conDB.Migrator().HasTable(&domain.Inbox{}) {
		t.Fatal("CON store contains CHN Inbox table")
	}
	if chnDB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("CHN store contains CON Conversation table")
	}
}

func testCopilotThreadOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	aicDB, iamDB, conDB := openStore("AIC"), openStore("IAM"), openStore("CON")
	user := domain.User{Name: "Copilot owner", Email: "copilot-owner@example.test"}
	if err := iamDB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 121, InboxID: 1, ContactID: 1, DisplayID: 1}
	foreignConversation := domain.Conversation{AccountID: 122, InboxID: 1, ContactID: 1, DisplayID: 1}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&foreignConversation).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewCopilotThreadRepositoryWithOwners(aicDB, repository.NewUserRepository(iamDB), repository.NewConversationRepository(conDB))
	thread := domain.CopilotThread{AccountID: 121, UserID: user.ID, ConversationID: &conversation.ID, Title: "owner boundary"}
	if err := repo.CreateThread(&thread); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetThread(121, thread.ID)
	if err != nil || got.User == nil || got.User.ID != user.ID || got.Conversation == nil || got.Conversation.ID != conversation.ID {
		t.Fatalf("owner hydration failed: thread=%+v err=%v", got, err)
	}
	threads, total, err := repo.ListThreads(121, repository.CopilotThreadFilter{})
	if err != nil || total != 1 || len(threads) != 1 || threads[0].User == nil || threads[0].Conversation == nil {
		t.Fatalf("owner list hydration failed: total=%d threads=%+v err=%v", total, threads, err)
	}
	foreign := domain.CopilotThread{AccountID: 121, UserID: user.ID, ConversationID: &foreignConversation.ID, Title: "foreign conversation"}
	if err := repo.CreateThread(&foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetThread(121, foreign.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign CON conversation accepted: %v", err)
	}
	if aicDB.Migrator().HasTable(&domain.User{}) || aicDB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("AIC store contains IAM or CON authority table")
	}
}

func testCSATExtensionOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	qltDB, conDB, cusDB, chnDB, iamDB := openStore("QLT"), openStore("CON"), openStore("CUS"), openStore("CHN"), openStore("IAM")
	contact := domain.Contact{AccountID: 131, Name: "CSAT contact"}
	inbox := domain.Inbox{AccountID: 131, Name: "CSAT inbox", WebsiteToken: "csat-owner-inbox"}
	agent := domain.User{Name: "CSAT agent", Email: "csat-agent@example.test"}
	reviewer := domain.User{Name: "CSAT reviewer", Email: "csat-reviewer@example.test"}
	for _, item := range []struct {
		db    *gorm.DB
		model any
	}{{cusDB, &contact}, {chnDB, &inbox}, {iamDB, &agent}, {iamDB, &reviewer}} {
		if err := item.db.Create(item.model).Error; err != nil {
			t.Fatal(err)
		}
	}
	conversation := domain.Conversation{AccountID: 131, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: 1}
	foreignConversation := domain.Conversation{AccountID: 132, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: 1}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&foreignConversation).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewCSATExtensionRepositoryWithOwners(qltDB, repository.NewConversationRepository(conDB), repository.NewContactRepository(cusDB), repository.NewInboxRepository(chnDB), repository.NewUserRepository(iamDB))
	survey := domain.CSATSurvey{AccountID: 131, ConversationID: conversation.ID, Rating: 5, ReviewStatus: "approved", AssignedAgentID: &agent.ID, ReviewerID: &reviewer.ID}
	if err := qltDB.Create(&survey).Error; err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetSurvey(131, survey.ID)
	if err != nil || got == nil || got.Conversation == nil || got.Conversation.Contact == nil || got.Conversation.Inbox == nil || got.AssignedAgent == nil || got.Reviewer == nil {
		t.Fatalf("CSAT owner hydration failed: survey=%+v err=%v", got, err)
	}
	list, total, err := repo.ListSurveys(131, repository.CSATFilter{InboxID: &inbox.ID})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("CSAT inbox owner filter failed: total=%d list=%+v err=%v", total, list, err)
	}
	metrics, err := repo.GetAdvancedMetrics(131, &inbox.ID, nil, nil, nil)
	if err != nil || len(metrics.AgentMetrics) != 1 || metrics.AgentMetrics[0].AgentName != agent.Name {
		t.Fatalf("CSAT IAM metric hydration failed: metrics=%+v err=%v", metrics, err)
	}
	foreign := domain.CSATSurvey{AccountID: 131, ConversationID: foreignConversation.ID, Rating: 4}
	if err := qltDB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetSurvey(131, foreign.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign CON conversation accepted: %v", err)
	}
	for name, model := range map[string]any{"CON": &domain.Conversation{}, "CUS": &domain.Contact{}, "CHN": &domain.Inbox{}, "IAM": &domain.User{}} {
		if qltDB.Migrator().HasTable(model) {
			t.Fatalf("QLT store contains %s authority table", name)
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

func testSearchHandlerOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	srhDB, cusDB, conDB, msgDB, kbDB := openStore("SRH"), openStore("CUS"), openStore("CON"), openStore("MSG"), openStore("KB")
	for _, contact := range []domain.Contact{
		{AccountID: 1, Name: "needle-contact-one", Email: "one@example.com"},
		{AccountID: 2, Name: "needle-contact-two", Email: "two@example.com"},
	} {
		if err := cusDB.Create(&contact).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, conversation := range []domain.Conversation{
		{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusOpen, CustomAttributes: `{"search":"needle-conversation-one"}`},
		{AccountID: 2, InboxID: 2, ContactID: 2, DisplayID: 1, Status: domain.ConversationStatusOpen, CustomAttributes: `{"search":"needle-conversation-two"}`},
	} {
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range []domain.Message{
		{AccountID: 1, ConversationID: 1, Content: "needle-message-one", MessageType: domain.MessageTypeIncoming},
		{AccountID: 2, ConversationID: 2, Content: "needle-message-two", MessageType: domain.MessageTypeIncoming},
	} {
		if err := msgDB.Create(&message).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, article := range []domain.Article{
		{AccountID: 1, Title: "needle-article-one", Content: "owner search"},
		{AccountID: 2, Title: "needle-article-two", Content: "other tenant"},
	} {
		if err := kbDB.Create(&article).Error; err != nil {
			t.Fatal(err)
		}
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	search := handler.NewSearchHandler(repository.NewContactRepository(cusDB), repository.NewConversationRepository(conDB), repository.NewMessageRepository(msgDB), repository.NewPortalRepository(kbDB))
	router.GET("/:account_id/search", search.GlobalSearch)
	router.GET("/:account_id/search/contacts", search.SearchContacts)
	router.GET("/:account_id/search/conversations", search.SearchConversations)
	router.GET("/:account_id/search/messages", search.SearchMessages)
	router.GET("/:account_id/search/articles", search.SearchArticles)
	checks := []struct {
		path string
		want string
		deny string
	}{
		{"/1/search?q=needle", "needle-contact-one", "needle-contact-two"},
		{"/1/search/contacts?q=needle", "needle-contact-one", "needle-contact-two"},
		{"/1/search/conversations?q=needle", "needle-conversation-one", "needle-conversation-two"},
		{"/1/search/messages?q=needle", "needle-message-one", "needle-message-two"},
		{"/1/search/articles?q=needle", "needle-article-one", "needle-article-two"},
	}
	for _, check := range checks {
		req := httptest.NewRequest(http.MethodGet, check.path, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), check.want) || strings.Contains(res.Body.String(), check.deny) {
			t.Fatalf("owner search %s returned code=%d body=%s", check.path, res.Code, res.Body.String())
		}
	}
	for name, model := range map[string]any{"CUS Contact": &domain.Contact{}, "CON Conversation": &domain.Conversation{}, "MSG Message": &domain.Message{}, "KB Article": &domain.Article{}} {
		if srhDB.Migrator().HasTable(model) {
			t.Fatalf("SRH store contains %s authority table", name)
		}
	}
}

func testCampaignConversationOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	opsDB, cusDB, conDB, msgDB := openStore("OPS"), openStore("CUS"), openStore("CON"), openStore("MSG")
	contacts := []domain.Contact{
		{AccountID: 1, Name: "Resolved contact", Email: "resolved@example.com"},
		{AccountID: 1, Name: "Pending contact", Email: "pending@example.com"},
	}
	for i := range contacts {
		if err := cusDB.Create(&contacts[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	conversations := []domain.Conversation{
		{AccountID: 1, InboxID: 20, ContactID: contacts[0].ID, DisplayID: 1, Status: domain.ConversationStatusResolved},
		{AccountID: 1, InboxID: 20, ContactID: contacts[1].ID, DisplayID: 2, Status: domain.ConversationStatusPending},
		{AccountID: 2, InboxID: 20, ContactID: 999, DisplayID: 1, Status: domain.ConversationStatusOpen},
	}
	for i := range conversations {
		if err := conDB.Create(&conversations[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	campaign := domain.Campaign{AccountID: 1, InboxID: 20, Title: "reuse conversations", Message: "owner lookup", Status: "active", CampaignType: "one_off", Audience: "all"}
	if err := opsDB.Create(&campaign).Error; err != nil {
		t.Fatal(err)
	}

	conversationOwner := repository.NewConversationRepository(conDB)
	campaigns := service.NewCampaignService(opsDB, conversationOwner, repository.NewMessageRepositoryWithConversationPort(msgDB, conversationOwner), repository.NewContactRepository(cusDB))
	sent, err := campaigns.TriggerCampaign(1, campaign.ID)
	if err != nil || sent != 2 {
		t.Fatalf("owner-backed campaign sent=%d err=%v", sent, err)
	}
	var conversationCount int64
	if err := conDB.Model(&domain.Conversation{}).Where("account_id = ?", 1).Count(&conversationCount).Error; err != nil || conversationCount != 2 {
		t.Fatalf("existing conversations were not reused: count=%d err=%v", conversationCount, err)
	}
	if foreign, err := conversationOwner.FindByContactAndInbox(1, 999, 20); err != nil || foreign != nil {
		t.Fatalf("cross-account conversation leaked: conversation=%+v err=%v", foreign, err)
	}
	var deliveries []domain.CampaignDelivery
	if err := opsDB.Where("campaign_id = ?", campaign.ID).Order("contact_id ASC").Find(&deliveries).Error; err != nil || len(deliveries) != 2 || deliveries[0].ConversationID != conversations[0].ID || deliveries[1].ConversationID != conversations[1].ID {
		t.Fatalf("unexpected delivery conversations: deliveries=%+v err=%v", deliveries, err)
	}
	if opsDB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("OPS store contains CON Conversation authority table")
	}
}

func testCampaignAudienceOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	opsDB, cusDB, conDB, msgDB := openStore("OPS"), openStore("CUS"), openStore("CON"), openStore("MSG")
	matched := domain.Contact{AccountID: 1, Name: "Matched", Email: "matched@example.com"}
	unmatched := domain.Contact{AccountID: 1, Name: "Unmatched", Email: "unmatched@example.com"}
	if err := cusDB.Create(&matched).Error; err != nil {
		t.Fatal(err)
	}
	if err := cusDB.Create(&unmatched).Error; err != nil {
		t.Fatal(err)
	}
	label := domain.Label{AccountID: 1, Title: "vip-customer"}
	if err := cusDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	matchedConversation := domain.Conversation{AccountID: 1, InboxID: 10, ContactID: matched.ID, DisplayID: 1, Status: domain.ConversationStatusOpen}
	unmatchedConversation := domain.Conversation{AccountID: 1, InboxID: 10, ContactID: unmatched.ID, DisplayID: 2, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&matchedConversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&unmatchedConversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&domain.ConversationLabel{ConversationID: matchedConversation.ID, LabelID: label.ID}).Error; err != nil {
		t.Fatal(err)
	}
	for _, campaign := range []domain.Campaign{
		{AccountID: 1, InboxID: 10, Title: "exact", Message: "exact", Status: "active", CampaignType: "ongoing", Audience: `{"labels":["vip-customer"]}`},
		{AccountID: 1, InboxID: 10, Title: "fuzzy", Message: "fuzzy", Status: "active", CampaignType: "ongoing", Audience: "vip"},
	} {
		if err := opsDB.Create(&campaign).Error; err != nil {
			t.Fatal(err)
		}
	}

	conversations := repository.NewConversationRepository(conDB)
	campaigns := service.NewCampaignService(opsDB, conversations, repository.NewMessageRepositoryWithConversationPort(msgDB, conversations), repository.NewContactRepository(cusDB))
	campaigns.SetAudienceOwner(repository.NewLabelRepositoryWithConversationPort(cusDB, conversations))
	count, err := campaigns.TriggerOngoingCampaignForContact(1, 10, matched.ID, matchedConversation.ID)
	if err != nil || count != 2 {
		t.Fatalf("matching contact deliveries=%d err=%v", count, err)
	}
	count, err = campaigns.TriggerOngoingCampaignForContact(1, 10, unmatched.ID, unmatchedConversation.ID)
	if err != nil || count != 0 {
		t.Fatalf("unmatched contact deliveries=%d err=%v", count, err)
	}
	for name, model := range map[string]any{"CUS Contact": &domain.Contact{}, "CUS Label": &domain.Label{}, "CON Conversation": &domain.Conversation{}, "CON label relation": &domain.ConversationLabel{}, "MSG Message": &domain.Message{}} {
		if opsDB.Migrator().HasTable(model) {
			t.Fatalf("OPS store contains %s authority table", name)
		}
	}
	if cusDB.Migrator().HasTable(&domain.Conversation{}) || cusDB.Migrator().HasTable(&domain.ConversationLabel{}) {
		t.Fatal("CUS store contains CON authority tables")
	}
	if conDB.Migrator().HasTable(&domain.Label{}) {
		t.Fatal("CON store contains CUS Label authority table")
	}
}

func testDataImportContactOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	migDB, cusDB := openStore("MIG"), openStore("CUS")
	for _, contact := range []domain.Contact{
		{AccountID: 1, Name: "Existing", Email: "existing@example.com"},
		{AccountID: 2, Name: "Other tenant", Email: "other@example.com"},
	} {
		if err := cusDB.Create(&contact).Error; err != nil {
			t.Fatal(err)
		}
	}

	imports := service.NewDataImportServiceWithContactOwner(migDB, repository.NewContactRepository(cusDB))
	raw := "name,email\nExisting,existing@example.com\nNew Tenant One,other@example.com\n"
	validation, err := imports.Prevalidate(1, "contacts", "csv", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !validation.Valid || len(validation.Warnings) != 1 || !strings.Contains(validation.Warnings[0].Reason, "existing@example.com") {
		t.Fatalf("unexpected owner-backed validation: %+v", validation)
	}

	dataImport := domain.DataImport{AccountID: 1, ImportType: "contacts", SourceProvider: "csv", RawData: raw}
	if err := imports.ExecuteImport(context.Background(), 1, &dataImport); err != nil {
		t.Fatal(err)
	}
	if dataImport.Status != "completed" || dataImport.ProcessedRecords != 1 || dataImport.SkippedRecords != 1 {
		t.Fatalf("unexpected import result: %+v", dataImport)
	}
	created, err := repository.NewContactRepository(cusDB).FindByEmail(1, "other@example.com")
	if err != nil || created == nil {
		t.Fatalf("CUS owner did not create account-scoped contact: contact=%+v err=%v", created, err)
	}
	if migDB.Migrator().HasTable(&domain.Contact{}) {
		t.Fatal("MIG store contains CUS Contact authority table")
	}

	brokenCUS, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "broken-cus.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	unavailable := service.NewDataImportServiceWithContactOwner(migDB, repository.NewContactRepository(brokenCUS))
	if _, err := unavailable.Prevalidate(1, "contacts", "csv", "name,email\nUnavailable,unavailable@example.com\n"); err == nil {
		t.Fatal("CUS owner failure was hidden during prevalidation")
	}
	failedImport := domain.DataImport{AccountID: 1, ImportType: "contacts", SourceProvider: "csv", RawData: "name,email\nUnavailable,unavailable@example.com\n"}
	if err := unavailable.ExecuteImport(context.Background(), 1, &failedImport); err != nil {
		t.Fatal(err)
	}
	if failedImport.Status != "failed" || failedImport.FailedRecords != 1 || !strings.Contains(failedImport.ErrorsJSON, "Contact lookup failed") {
		t.Fatalf("CUS owner failure was not recorded: %+v", failedImport)
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
