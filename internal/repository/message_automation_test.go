package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type automationDeferredActivity struct {
	owner *ConversationRepository
	fail  bool
}

func (p *automationDeferredActivity) MessageTargetExists(a, c uint) (bool, error) {
	return p.owner.MessageTargetExists(a, c)
}
func (p *automationDeferredActivity) ApplyMessageActivity(a, c, u uint, incoming bool, at time.Time, key string) error {
	if p.fail {
		return errors.New("scripted owner outage")
	}
	return p.owner.ApplyMessageActivity(a, c, u, incoming, at, key)
}
func (p *automationDeferredActivity) AutomationMessageActivityVersion(a, c uint, key string) (int, error) {
	return p.owner.AutomationMessageActivityVersion(a, c, key)
}
func automationMessageFixture(t *testing.T) (*MessageRepository, *automationDeferredActivity, *gorm.DB, *gorm.DB, domain.Conversation) {
	t.Helper()
	con, msg := paymentDB(t, "aut-con"), paymentDB(t, "aut-msg")
	if err := database.AutoMigrateModule(con, "CON"); err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrateModule(msg, "MSG"); err != nil {
		t.Fatal(err)
	}
	c := domain.Conversation{AccountID: 1, InboxID: 1, ContactID: 1, DisplayID: 1, Status: "open", Priority: "medium", ObjectVersion: 1}
	if err := con.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	p := &automationDeferredActivity{owner: NewConversationRepository(con)}
	r := NewMessageRepositoryWithConversationPort(msg, p)
	r.SetAutomationCapture(true)
	return r, p, con, msg, c
}
func automationTestMessage(t *testing.T, r *MessageRepository, c domain.Conversation) domain.Message {
	t.Helper()
	m := domain.Message{AccountID: c.AccountID, ConversationID: c.ID, SenderType: "Contact", SenderID: 1, MessageType: domain.MessageTypeIncoming, Content: "bounded test", ContentType: "text", Status: "sent"}
	if err := r.Create(&m); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestMessageAutomationWaitsForDurableActivityReceipt(t *testing.T) {
	r, p, _, db, c := automationMessageFixture(t)
	p.fail = true
	m := automationTestMessage(t, r, c)
	if facts, err := r.PendingAutomationFacts(context.Background(), 100); err != nil || len(facts) != 0 {
		t.Fatalf("premature facts=%v err=%v", facts, err)
	}
	p.fail = false
	if n, err := r.ReconcilePendingActivity(100); err != nil || n != 1 {
		t.Fatalf("reconcile=%d/%v", n, err)
	}
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts=%v err=%v", facts, err)
	}
	if id, _ := messageAutomationUint(facts[0].Payload["message_id"]); id != m.ID {
		t.Fatal("wrong message")
	}
	if version, _ := messageAutomationUint(facts[0].Payload["object_version"]); version != 2 {
		t.Fatalf("version=%d", version)
	}
	if err := r.MarkAutomationFactDelivered(context.Background(), facts[0].EventID); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkAutomationFactDelivered(context.Background(), facts[0].EventID); err != nil {
		t.Fatal(err)
	}
	restarted := NewMessageRepositoryWithConversationPort(db, p)
	restarted.SetAutomationCapture(true)
	if facts, err := restarted.PendingAutomationFacts(context.Background(), 100); err != nil || len(facts) != 0 {
		t.Fatalf("replayed facts=%v err=%v", facts, err)
	}
}
func TestMessageAutomationQuarantinesPoisonAndContinues(t *testing.T) {
	for _, kind := range []string{"json", "causation", "conversation", "missing_activity", "invalid_owner_receipt"} {
		t.Run(kind, func(t *testing.T) {
			r, _, con, db, c := automationMessageFixture(t)
			bad := automationTestMessage(t, r, c)
			var row domain.LocalChangeJournal
			if err := db.Where("action = ? AND object_id = ?", "automation_trigger", bad.ID).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			var event foundation.EventEnvelope
			if err := json.Unmarshal([]byte(row.AfterState), &event); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "json":
				row.AfterState = "{"
			case "causation":
				event.CausationID = "wrong"
				raw, _ := json.Marshal(event)
				row.AfterState = string(raw)
			case "conversation":
				event.Payload["conversation_id"] = 999
				raw, _ := json.Marshal(event)
				row.AfterState = string(raw)
			case "missing_activity":
				db.Where("change_id = ?", event.CausationID).Delete(&domain.LocalChangeJournal{})
			case "invalid_owner_receipt":
				con.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND command_id = ?", "CON", event.CausationID).Update("object_version_after", 0)
			}
			if err := db.Model(&domain.LocalChangeJournal{}).Where("id = ?", row.ID).Update("after_state", row.AfterState).Error; err != nil {
				t.Fatal(err)
			}
			good := automationTestMessage(t, r, c)
			facts, err := r.PendingAutomationFacts(context.Background(), 100)
			if err != nil || len(facts) != 1 {
				t.Fatalf("facts=%v err=%v", facts, err)
			}
			if id, _ := messageAutomationUint(facts[0].Payload["message_id"]); id != good.ID {
				t.Fatalf("id=%d want=%d", id, good.ID)
			}
			if err := db.First(&row, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if row.Status != "manual_action_required" || row.Result != "invalid_automation_fact" {
				t.Fatalf("row=%+v", row)
			}
		})
	}
}
func TestMessageAutomationFutureFactRemainsPending(t *testing.T) {
	r, _, _, db, c := automationMessageFixture(t)
	first := automationTestMessage(t, r, c)
	var row domain.LocalChangeJournal
	db.Where("action = ? AND object_id = ?", "automation_trigger", first.ID).First(&row)
	original := row.AfterState
	var event foundation.EventEnvelope
	json.Unmarshal([]byte(original), &event)
	event.OccurredAt = time.Now().Add(time.Hour)
	event.PublishedAt = event.OccurredAt
	raw, _ := json.Marshal(event)
	db.Model(&row).Update("after_state", string(raw))
	second := automationTestMessage(t, r, c)
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts=%v err=%v", facts, err)
	}
	if id, _ := messageAutomationUint(facts[0].Payload["message_id"]); id != second.ID {
		t.Fatal("future fact was returned")
	}
	db.First(&row, row.ID)
	if row.Status != "pending" {
		t.Fatalf("future status=%s", row.Status)
	}
	db.Model(&row).Update("after_state", original)
	facts, err = r.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(facts) != 2 {
		t.Fatalf("future fact did not resume: %v/%v", facts, err)
	}
}

func TestMessageAutomationKnownOrphanIsCancelled(t *testing.T) {
	r, owner, _, db, c := automationMessageFixture(t)
	owner.fail = true // an orphan can only cancel a pending activity delivery
	msg := automationTestMessage(t, r, c)
	var row domain.LocalChangeJournal
	if err := db.Where("action = ? AND object_id = ?", "automation_trigger", msg.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.cancelOrphanedCreate(&msg, row.CausationID); err != nil {
		t.Fatal(err)
	}
	facts, err := r.PendingAutomationFacts(context.Background(), 100)
	if err != nil || len(facts) != 0 {
		t.Fatalf("facts=%v err=%v", facts, err)
	}
	db.First(&row, row.ID)
	if row.Status != "cancelled" || row.Result != "orphaned_message" {
		t.Fatalf("row=%+v", row)
	}
}
