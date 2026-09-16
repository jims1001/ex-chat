package test

import (
	"fmt"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupOwnershipBoundaryDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:ownership_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&domain.Account{}, &domain.Inbox{}, &domain.Contact{}, &domain.Conversation{},
		&domain.Message{}, &domain.Label{}, &domain.ConversationLabel{},
		&domain.Ticket{}, &domain.TicketActivity{}, &domain.TicketStatusHistory{},
		&domain.Order{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedOwnershipAccount(t *testing.T, db *gorm.DB, name string) (domain.Account, domain.Inbox, domain.Contact, domain.Conversation) {
	t.Helper()
	account := domain.Account{Name: name}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	inbox := domain.Inbox{AccountID: account.ID, Name: name + " Inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: name}
	if err := db.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	contact := domain.Contact{AccountID: account.ID, Name: name + " Contact"}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: 1, Status: domain.ConversationStatusOpen}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	return account, inbox, contact, conversation
}

func TestOwnerRepositoriesRejectCrossTenantReferences(t *testing.T) {
	db := setupOwnershipBoundaryDB(t)
	accountA, inboxA, contactA, conversationA := seedOwnershipAccount(t, db, "tenant-a")
	accountB, inboxB, _, conversationB := seedOwnershipAccount(t, db, "tenant-b")

	msgRepo := repository.NewMessageRepository(db)
	message := domain.Message{AccountID: accountA.ID, ConversationID: conversationB.ID, MessageType: domain.MessageTypeActivity, Content: "forbidden"}
	if err := msgRepo.Create(&message); err == nil {
		t.Fatal("expected cross-tenant message reference to be rejected")
	}
	var messageCount int64
	_ = db.Model(&domain.Message{}).Where("content = ?", "forbidden").Count(&messageCount).Error
	if messageCount != 0 {
		t.Fatalf("cross-tenant message was persisted: count=%d", messageCount)
	}
	importedMessage := domain.Message{AccountID: accountA.ID, ConversationID: conversationB.ID, MessageType: domain.MessageTypeIncoming, Content: "forbidden import"}
	if err := msgRepo.CreateImported(&importedMessage); err == nil {
		t.Fatal("expected cross-tenant imported message reference to be rejected")
	}

	convRepo := repository.NewConversationRepository(db)
	importedConversation := domain.Conversation{AccountID: accountA.ID, InboxID: inboxA.ID, ContactID: contactA.ID, DisplayID: 2}
	importedConversation.InboxID = conversationB.InboxID
	if err := convRepo.CreateImported(&importedConversation); err == nil {
		t.Fatal("expected cross-tenant imported conversation reference to be rejected")
	}

	ticketRepo := repository.NewTicketRepository(db)
	ticket := domain.Ticket{AccountID: accountA.ID, ConversationID: &conversationB.ID, Title: "forbidden ticket"}
	if err := ticketRepo.Create(&ticket); err == nil {
		t.Fatal("expected cross-tenant ticket reference to be rejected")
	}

	orderRepo := repository.NewOrderRepository(db)
	order := domain.Order{AccountID: accountA.ID, ConversationID: &conversationA.ID, OrderID: "cross-tenant-order"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	order.AccountID = accountB.ID
	order.ConversationID = &conversationB.ID
	if err := orderRepo.Update(accountB.ID, &order); err == nil {
		t.Fatal("expected cross-tenant order reference to be rejected")
	}

	if err := convRepo.MoveInbox(accountA.ID, inboxA.ID, inboxB.ID); err == nil {
		t.Fatal("expected cross-tenant inbox migration to be rejected")
	}
}

func TestConversationLabelOwnerGuardsBothTenants(t *testing.T) {
	db := setupOwnershipBoundaryDB(t)
	accountA, _, _, conversationA := seedOwnershipAccount(t, db, "label-a")
	accountB, _, _, _ := seedOwnershipAccount(t, db, "label-b")
	labelB := domain.Label{AccountID: accountB.ID, Title: "private"}
	if err := db.Create(&labelB).Error; err != nil {
		t.Fatal(err)
	}
	convRepo := repository.NewConversationRepository(db)
	if err := convRepo.AttachLabel(accountA.ID, conversationA.ID, labelB.ID); err == nil {
		t.Fatal("expected cross-tenant label attachment to be rejected")
	}
}

func TestImportedRecordsPreserveHistoricalStateWithoutLiveSideEffects(t *testing.T) {
	db := setupOwnershipBoundaryDB(t)
	account, inbox, contact, existing := seedOwnershipAccount(t, db, "migration")
	historicalTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	convRepo := repository.NewConversationRepository(db)
	importedConversation := domain.Conversation{
		AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID,
		DisplayID: 42, Status: domain.ConversationStatusResolved,
		CreatedAt: historicalTime, UpdatedAt: historicalTime, LastActivityAt: historicalTime,
	}
	if err := convRepo.CreateImported(&importedConversation); err != nil {
		t.Fatal(err)
	}
	if importedConversation.DisplayID != 42 {
		t.Fatalf("display_id changed: got %d", importedConversation.DisplayID)
	}

	msgRepo := repository.NewMessageRepository(db)
	importedMessage := domain.Message{
		AccountID: account.ID, ConversationID: existing.ID, MessageType: domain.MessageTypeIncoming,
		Content: "historical", CreatedAt: historicalTime, UpdatedAt: historicalTime,
	}
	if err := msgRepo.CreateImported(&importedMessage); err != nil {
		t.Fatal(err)
	}
	var reloaded domain.Conversation
	if err := db.First(&reloaded, existing.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.UnreadCount != existing.UnreadCount || !reloaded.LastActivityAt.Equal(existing.LastActivityAt) {
		t.Fatalf("historical import applied live side effects: unread=%d activity=%s", reloaded.UnreadCount, reloaded.LastActivityAt)
	}
}
