package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

func identityEvent(version uint64) foundation.EventEnvelope {
	now := time.Now().UTC()
	return foundation.EventEnvelope{EventID: "identity-event-" + strconv.FormatUint(version, 10), EventType: "iam.identity.changed.v1", EventVersion: "v1", SourceModule: "IAM", AccountID: 1, AggregateType: "User", AggregateID: "7", AggregateVersion: version, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "event-correlation", CausationID: "command-1", ChangedFields: []string{"name"}, Payload: map[string]any{"user_id": 7}, DataClassification: "internal"}
}

func deliverEvent(handler http.Handler, event foundation.EventEnvelope, mutate func(*http.Request)) *httptest.ResponseRecorder {
	body, _ := json.Marshal(event)
	request := httptest.NewRequest(http.MethodPost, "/contracts/events/"+event.EventType, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer ten-secret")
	request.Header.Set(headerCaller, "IAM")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, event.CorrelationID)
	if mutate != nil {
		mutate(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestEventOutboxDeliveryDedupGapAndRecovery(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	outbox := NewEventOutbox(iamDB, "IAM")
	event1 := identityEvent(1)
	if err := iamDB.Transaction(func(tx *gorm.DB) error { return outbox.Append(tx, event1) }); err != nil {
		t.Fatal(err)
	}
	tenDB := newModuleTestDB(t, "TEN")
	registry := NewContractRegistry("TEN")
	var applied atomic.Int64
	if err := registry.RegisterEvent(event1.EventType, func(context.Context, foundation.EventEnvelope) error { applied.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("TEN")
	runtime := Runtime{Config: cfg, DB: tenDB, Contracts: registry}
	handler := runtime.Handler()
	if response := deliverEvent(handler, event1, nil); response.Code != http.StatusOK {
		t.Fatalf("event1 status=%d body=%s", response.Code, response.Body.String())
	}
	if response := deliverEvent(handler, event1, nil); response.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", response.Code, response.Body.String())
	}
	if applied.Load() != 1 {
		t.Fatalf("duplicate applied %d times", applied.Load())
	}
	event3 := identityEvent(3)
	if response := deliverEvent(handler, event3, nil); response.Code != http.StatusConflict {
		t.Fatalf("gap status=%d body=%s", response.Code, response.Body.String())
	}
	if applied.Load() != 1 {
		t.Fatal("gap event was applied")
	}
	event2 := identityEvent(2)
	if response := deliverEvent(handler, event2, nil); response.Code != http.StatusOK {
		t.Fatalf("event2 status=%d body=%s", response.Code, response.Body.String())
	}
	if response := deliverEvent(handler, event3, nil); response.Code != http.StatusOK {
		t.Fatalf("event3 recovery status=%d body=%s", response.Code, response.Body.String())
	}
	if applied.Load() != 3 {
		t.Fatalf("recovery applied=%d", applied.Load())
	}
	if err := outbox.MarkDelivered(event1.EventID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := iamDB.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", event1.EventID).Pluck("status", &status).Error; err != nil || status != "delivered" {
		t.Fatalf("outbox status=%q err=%v", status, err)
	}
}

func TestEventOutboxAndConsumerRollbackAtomically(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	event := identityEvent(1)
	forced := errors.New("forced")
	if err := iamDB.Transaction(func(tx *gorm.DB) error {
		if err := NewEventOutbox(iamDB, "IAM").Append(tx, event); err != nil {
			return err
		}
		return forced
	}); !errors.Is(err, forced) {
		t.Fatal(err)
	}
	var outboxCount int64
	if err := iamDB.Model(&domain.LocalChangeJournal{}).Where("event_id = ?", event.EventID).Count(&outboxCount).Error; err != nil || outboxCount != 0 {
		t.Fatalf("outbox rollback count=%d err=%v", outboxCount, err)
	}
	tenDB := newModuleTestDB(t, "TEN")
	inbox := NewEventInbox(tenDB, "TEN")
	_, err := inbox.Consume(context.Background(), event, func(ctx context.Context) error {
		if err := Transaction(ctx, tenDB).Create(&domain.Account{ID: 9, Name: "projection"}).Error; err != nil {
			return err
		}
		return forced
	})
	if !errors.Is(err, forced) {
		var failure *ContractFailure
		if !errors.As(err, &failure) || failure.Message != "event inbox transaction failed" {
			t.Fatalf("consume error=%v", err)
		}
	}
	var accounts, receipts int64
	_ = tenDB.Model(&domain.Account{}).Count(&accounts).Error
	_ = tenDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_consumed").Count(&receipts).Error
	if accounts != 0 || receipts != 0 {
		t.Fatalf("consumer rollback accounts=%d receipts=%d", accounts, receipts)
	}
}

func TestEventDispatcherRetainsPendingUntilConsumerRecovers(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	event := identityEvent(1)
	outbox := NewEventOutbox(iamDB, "IAM")
	if err := outbox.Append(nil, event); err != nil {
		t.Fatal(err)
	}
	tenDB := newModuleTestDB(t, "TEN")
	registry := NewContractRegistry("TEN")
	if err := registry.RegisterEvent(event.EventType, func(context.Context, foundation.EventEnvelope) error { return nil }); err != nil {
		t.Fatal(err)
	}
	tenRuntime := Runtime{Config: businessConfig("TEN"), DB: tenDB, Contracts: registry}
	server, address := startHTTPServer(t, "127.0.0.1:0", tenRuntime.Handler())
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewEventDispatcher(iamDB, "IAM", DependencyConfig{URL: "http://" + address, Token: "ten-secret"}, "v1", 100*time.Millisecond)
	if delivered, err := dispatcher.DeliverPending(context.Background(), 10); err == nil || delivered != 0 {
		t.Fatalf("stopped consumer delivered=%d err=%v", delivered, err)
	}
	var status string
	_ = iamDB.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", event.EventID).Pluck("status", &status).Error
	if status != "pending" {
		t.Fatalf("failed delivery status=%s", status)
	}
	server, _ = startHTTPServer(t, address, tenRuntime.Handler())
	defer server.Shutdown(context.Background())
	if delivered, err := dispatcher.DeliverPending(context.Background(), 10); err != nil || delivered != 1 {
		t.Fatalf("recovery delivered=%d err=%v", delivered, err)
	}
	_ = iamDB.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", event.EventID).Pluck("status", &status).Error
	if status != "delivered" {
		t.Fatalf("recovered status=%s", status)
	}
}

func TestEventFanoutPersistsPerTargetProgressAndRecovers(t *testing.T) {
	sourceDB := newModuleTestDB(t, "CUS")
	event := foundation.EventEnvelope{EventID: "fanout-contact-merge-1", EventType: "cus.contact.merged.v1", EventVersion: "v1", SourceModule: "CUS", AccountID: 91, AggregateType: "Contact", AggregateID: "101", AggregateVersion: 1, OccurredAt: time.Now().UTC(), PublishedAt: time.Now().UTC(), Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "fanout-correlation", CausationID: "fanout-command", ChangedFields: []string{"merged"}, Payload: map[string]any{"base_contact_id": 101, "merged_contact_id": 202}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "CUS").Append(nil, event); err != nil {
		t.Fatal(err)
	}

	startConsumer := func(module, address string, applied *atomic.Int64) (*http.Server, string) {
		db := newModuleTestDB(t, module)
		registry := NewContractRegistry(module)
		if err := registry.RegisterEvent(event.EventType, func(context.Context, foundation.EventEnvelope) error { applied.Add(1); return nil }); err != nil {
			t.Fatal(err)
		}
		cfg := businessConfig(module)
		cfg.AllowedCallers["CUS"] = true
		return startHTTPServer(t, address, Runtime{Config: cfg, DB: db, Contracts: registry}.Handler())
	}
	var conApplied, msgApplied atomic.Int64
	conServer, conAddress := startConsumer("CON", "127.0.0.1:0", &conApplied)
	defer conServer.Shutdown(context.Background())
	msgServer, msgAddress := startConsumer("MSG", "127.0.0.1:0", &msgApplied)
	if err := msgServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	targets := map[string]EventFanoutTarget{
		"CON": {Dependency: DependencyConfig{URL: "http://" + conAddress, Token: businessConfig("CON").AuthToken}, EventTypes: map[string]bool{event.EventType: true}},
		"MSG": {Dependency: DependencyConfig{URL: "http://" + msgAddress, Token: businessConfig("MSG").AuthToken}, EventTypes: map[string]bool{event.EventType: true}},
	}
	dispatcher := NewEventFanoutDispatcher(sourceDB, "CUS", targets, "v1", time.Second)
	if delivered, err := dispatcher.DeliverPending(context.Background(), 10); err == nil || delivered != 0 {
		t.Fatalf("partial fanout delivered=%d err=%v", delivered, err)
	}
	var pending, deliveryReceipts int64
	_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "pending").Count(&pending).Error
	_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&deliveryReceipts).Error
	if conApplied.Load() != 1 || msgApplied.Load() != 0 || pending != 1 || deliveryReceipts != 1 {
		t.Fatalf("partial con=%d msg=%d pending=%d receipts=%d", conApplied.Load(), msgApplied.Load(), pending, deliveryReceipts)
	}
	msgServer, _ = startConsumer("MSG", msgAddress, &msgApplied)
	defer msgServer.Shutdown(context.Background())
	if delivered, err := dispatcher.DeliverPending(context.Background(), 10); err != nil || delivered != 1 {
		t.Fatalf("recovered fanout delivered=%d err=%v", delivered, err)
	}
	var deliveredOutbox int64
	_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&deliveredOutbox).Error
	_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&deliveryReceipts).Error
	if conApplied.Load() != 1 || msgApplied.Load() != 1 || deliveredOutbox != 1 || deliveryReceipts != 2 {
		t.Fatalf("recovered con=%d msg=%d outbox=%d receipts=%d", conApplied.Load(), msgApplied.Load(), deliveredOutbox, deliveryReceipts)
	}
	if delivered, err := dispatcher.DeliverPending(context.Background(), 10); err != nil || delivered != 0 {
		t.Fatalf("completed fanout replay delivered=%d err=%v", delivered, err)
	}
}
