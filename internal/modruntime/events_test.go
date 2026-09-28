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
