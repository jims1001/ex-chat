package test

import (
	"context"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"gorm.io/gorm"
)

func testRoutingSnoozeOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	rteDB, conDB := openStore("RTE"), openStore("CON")
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)

	fixtures := []domain.Conversation{
		{AccountID: 1, InboxID: 10, Status: domain.ConversationStatusSnoozed, SnoozedUntil: &past},
		{AccountID: 2, InboxID: 20, Status: domain.ConversationStatusSnoozed, SnoozedUntil: &past},
		{AccountID: 1, InboxID: 10, Status: domain.ConversationStatusSnoozed, SnoozedUntil: &future},
		{AccountID: 1, InboxID: 10, Status: domain.ConversationStatusOpen, SnoozedUntil: &past},
	}
	for i := range fixtures {
		if err := conDB.Create(&fixtures[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	owner := repository.NewConversationRepository(conDB)
	owner.SetAutomationCapture(true)
	routing := service.NewRoutingService(rteDB, owner, nil)
	resumed, err := routing.ResumeSnoozedConversations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 2 || resumed[0].ID != fixtures[0].ID || resumed[1].ID != fixtures[1].ID {
		t.Fatalf("resumed=%+v", resumed)
	}
	for _, expected := range fixtures[:2] {
		var stored domain.Conversation
		if err := conDB.First(&stored, expected.ID).Error; err != nil || stored.Status != domain.ConversationStatusOpen || stored.SnoozedUntil != nil || stored.LastActivityAt.IsZero() {
			t.Fatalf("conversation %d was not resumed: %+v err=%v", expected.ID, stored, err)
		}
	}
	for _, expected := range fixtures[2:] {
		var stored domain.Conversation
		if err := conDB.First(&stored, expected.ID).Error; err != nil || stored.Status != expected.Status {
			t.Fatalf("conversation %d changed unexpectedly: %+v err=%v", expected.ID, stored, err)
		}
	}
	if repeated, err := routing.ResumeSnoozedConversations(context.Background()); err != nil || len(repeated) != 0 {
		t.Fatalf("resume was not idempotent: count=%d err=%v", len(repeated), err)
	}
	if rteDB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("RTE created the CON authority table")
	}

	facts, err := owner.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(facts) != 2 {
		t.Fatalf("CON expiry facts=%+v err=%v", facts, err)
	}
	for _, fact := range facts {
		if fact.SourceModule != "CON" || fact.Payload["status_before"] != "snoozed" || fact.Payload["status_after"] != "open" {
			t.Fatalf("invalid owner expiry transition: %+v", fact)
		}
	}
	if rteDB.Migrator().HasTable(&domain.AutomationRuleExecution{}) {
		t.Fatal("RTE created the AUT execution authority table")
	}
	var rteFacts int64
	if err := rteDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_trigger").Count(&rteFacts).Error; err != nil || rteFacts != 0 {
		t.Fatalf("RTE must not capture CON expiry facts: count=%d err=%v", rteFacts, err)
	}

	conSQL, err := conDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conSQL.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := routing.ResumeSnoozedConversations(context.Background()); err == nil {
		t.Fatal("CON outage was hidden by the scheduler")
	}
}
