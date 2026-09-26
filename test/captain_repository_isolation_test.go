package test

import (
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

func testCaptainRepositoryOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	aicDB, chnDB, conDB, msgDB := openStore("AIC"), openStore("CHN"), openStore("CON"), openStore("MSG")
	ownedInbox := domain.Inbox{AccountID: 1, Name: "Captain Inbox", WebsiteToken: "captain-owned"}
	foreignInbox := domain.Inbox{AccountID: 2, Name: "Foreign Captain Inbox", WebsiteToken: "captain-foreign"}
	if err := chnDB.Create(&ownedInbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := chnDB.Create(&foreignInbox).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	resolved := domain.Conversation{AccountID: 1, InboxID: ownedInbox.ID, ContactID: 1, DisplayID: 1, Status: domain.ConversationStatusResolved, CreatedAt: now.Add(-time.Hour)}
	foreign := domain.Conversation{AccountID: 2, InboxID: foreignInbox.ID, ContactID: 2, DisplayID: 1, Status: domain.ConversationStatusResolved, CreatedAt: now.Add(-time.Hour)}
	if err := conDB.Create(&resolved).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}

	inboxRepo := repository.NewInboxRepository(chnDB)
	convRepo := repository.NewConversationRepositoryWithReadPorts(conDB, nil, nil, inboxRepo, nil, nil, nil, nil)
	msgRepo := repository.NewMessageRepositoryWithConversationPort(msgDB, convRepo)
	repo := repository.NewCaptainRepositoryWithOwnerPorts(aicDB, inboxRepo, convRepo, msgRepo)
	assistant := domain.CaptainAssistant{AccountID: 1, Name: "Separated Captain", Inboxes: []domain.Inbox{foreignInbox}}
	if err := repo.Create(&assistant); err != nil {
		t.Fatalf("AIC create attempted a CHN association write: %v", err)
	}
	if _, err := repo.BindInbox(1, assistant.ID, foreignInbox.ID); err == nil {
		t.Fatal("cross-account inbox binding was accepted")
	}
	binding, err := repo.BindInbox(1, assistant.ID, ownedInbox.ID)
	if err != nil || binding.Inbox == nil || binding.Inbox.ID != ownedInbox.ID {
		t.Fatalf("CHN binding composition failed: binding=%+v err=%v", binding, err)
	}

	message := domain.Message{AccountID: 1, ConversationID: resolved.ID, SenderType: "CaptainAssistant", SenderID: assistant.ID, MessageType: domain.MessageTypeOutgoing, Content: "handled", CreatedAt: now.Add(-30 * time.Minute)}
	if err := msgDB.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	foreignMessage := domain.Message{AccountID: 2, ConversationID: foreign.ID, SenderType: "CaptainAssistant", SenderID: assistant.ID, MessageType: domain.MessageTypeOutgoing, Content: "foreign", CreatedAt: now.Add(-30 * time.Minute)}
	if err := msgDB.Create(&foreignMessage).Error; err != nil {
		t.Fatal(err)
	}

	found, err := repo.FindByID(1, assistant.ID)
	if err != nil || found == nil || len(found.Inboxes) != 1 || found.Inboxes[0].ID != ownedInbox.ID {
		t.Fatalf("assistant inboxes were not composed through CHN: assistant=%+v err=%v", found, err)
	}
	stats, err := repo.GetStats(1, assistant.ID, "7", 0)
	if err != nil || stats.ConversationsHandled.Value != int64(1) || stats.AutoResolutionRate.Value != float64(100) {
		t.Fatalf("CON/MSG stats mismatch: stats=%+v err=%v", stats, err)
	}
	conversations, total, err := repo.GetDrilldown(1, assistant.ID, "conversations_handled", "7", 1, 25)
	if err != nil || total != 1 || len(conversations) != 1 || conversations[0].Inbox == nil || conversations[0].Inbox.ID != ownedInbox.ID {
		t.Fatalf("CON drilldown mismatch: total=%d conversations=%+v err=%v", total, conversations, err)
	}

	var bindingCount int64
	if err := aicDB.Model(&domain.CaptainInbox{}).Count(&bindingCount).Error; err != nil || bindingCount != 1 {
		t.Fatalf("cross-account binding changed AIC: count=%d err=%v", bindingCount, err)
	}
	if aicDB.Migrator().HasTable(&domain.Inbox{}) || aicDB.Migrator().HasTable(&domain.Conversation{}) || aicDB.Migrator().HasTable(&domain.Message{}) {
		t.Fatal("AIC created CHN, CON, or MSG authority tables")
	}

	outageMSG := openStore("MSG")
	outageSQL, err := outageMSG.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := outageSQL.Close(); err != nil {
		t.Fatal(err)
	}
	outageRepo := repository.NewCaptainRepositoryWithOwnerPorts(aicDB, inboxRepo, convRepo, repository.NewMessageRepository(outageMSG))
	if _, err := outageRepo.GetStats(1, assistant.ID, "7", 0); err == nil {
		t.Fatal("MSG outage was hidden by Captain stats")
	}
}
