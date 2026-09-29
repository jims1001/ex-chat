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

func TestRuntimeEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerDB := newModuleTestDB(t, "CON")
	consumerRegistry := NewContractRegistry("CON")
	delivered := make(chan struct{}, 1)
	if err := consumerRegistry.RegisterEvent("cus.contact.merged.v1", func(context.Context, foundation.EventEnvelope) error {
		delivered <- struct{}{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("CON")
	consumerCfg.AllowedCallers["CUS"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: consumerDB, Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())

	sourceDB := newModuleTestDB(t, "CUS")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-fanout-1", EventType: "cus.contact.merged.v1", EventVersion: "v1", SourceModule: "CUS", AccountID: 91, AggregateType: "Contact", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "automatic-correlation", CausationID: "automatic-command", ChangedFields: []string{"merged"}, Payload: map[string]any{"base_contact_id": 101, "merged_contact_id": 202}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "CUS").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("CUS")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"CON": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errors := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { errors <- err })
	select {
	case <-delivered:
	case err := <-errors:
		t.Fatalf("automatic fanout error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic fanout did not deliver pending event")
	}
	var outbox, receipts int64
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("automatic outbox=%d receipts=%d", outbox, receipts)
	}
}

func TestMEDDeletedEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerDB := newModuleTestDB(t, "AUD")
	consumerRegistry := NewContractRegistry("AUD")
	delivered := make(chan foundation.EventEnvelope, 1)
	if err := consumerRegistry.RegisterEvent("med.media.deleted.v1", func(_ context.Context, event foundation.EventEnvelope) error {
		delivered <- event
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("AUD")
	consumerCfg.AllowedCallers["MED"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: consumerDB, Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())

	sourceDB := newModuleTestDB(t, "MED")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-media-delete-1", EventType: "med.media.deleted.v1", EventVersion: "v1", SourceModule: "MED", AccountID: 91, AggregateType: "MediaAsset", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "automatic-media-correlation", CausationID: "media-delete-command", ChangedFields: []string{"status"}, Payload: map[string]any{"asset_id": 101, "status": "deleted"}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "MED").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("MED")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"AUD": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errors := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { errors <- err })
	select {
	case received := <-delivered:
		if received.SourceModule != "MED" || received.Payload["status"] != "deleted" {
			t.Fatalf("unexpected delivered event: %+v", received)
		}
	case err := <-errors:
		t.Fatalf("automatic MED fanout error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic MED fanout did not deliver pending event")
	}
	var outbox, receipts int64
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("automatic MED outbox=%d receipts=%d", outbox, receipts)
	}
}

func TestCONDeletedEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerDB := newModuleTestDB(t, "AUD")
	consumerRegistry := NewContractRegistry("AUD")
	delivered := make(chan foundation.EventEnvelope, 1)
	if err := consumerRegistry.RegisterEvent("con.conversation.deleted.v1", func(_ context.Context, event foundation.EventEnvelope) error { delivered <- event; return nil }); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("AUD")
	consumerCfg.AllowedCallers["CON"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: consumerDB, Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())
	sourceDB := newModuleTestDB(t, "CON")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-con-delete-1", EventType: "con.conversation.deleted.v1", EventVersion: "v1", SourceModule: "CON", AccountID: 91, AggregateType: "Conversation", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "automatic-con-correlation", CausationID: "con-delete-command", ChangedFields: []string{"deleted"}, Payload: map[string]any{"conversation_id": 101, "operation": "deleted"}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "CON").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("CON")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"AUD": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { failures <- err })
	select {
	case received := <-delivered:
		if received.SourceModule != "CON" || received.Payload["operation"] != "deleted" {
			t.Fatalf("unexpected event: %+v", received)
		}
	case err := <-failures:
		t.Fatalf("fanout error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic CON fanout did not deliver")
	}
	deadline := time.Now().Add(time.Second)
	var outbox, receipts int64
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("outbox=%d receipts=%d", outbox, receipts)
	}
}

func TestMSGCreatedEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerDB := newModuleTestDB(t, "AUD")
	consumerRegistry := NewContractRegistry("AUD")
	delivered := make(chan foundation.EventEnvelope, 1)
	if err := consumerRegistry.RegisterEvent("msg.message.created.v1", func(_ context.Context, event foundation.EventEnvelope) error { delivered <- event; return nil }); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("AUD")
	consumerCfg.AllowedCallers["MSG"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: consumerDB, Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())
	sourceDB := newModuleTestDB(t, "MSG")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-msg-create-1", EventType: "msg.message.created.v1", EventVersion: "v1", SourceModule: "MSG", AccountID: 91, AggregateType: "Message", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, CorrelationID: "automatic-msg-correlation", CausationID: "msg-create-command", ChangedFields: []string{"created"}, Payload: map[string]any{"message_id": 101, "conversation_id": 11, "message_type": "outgoing", "status": "sent"}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "MSG").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("MSG")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"AUD": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { failures <- err })
	select {
	case received := <-delivered:
		if received.SourceModule != "MSG" || received.Payload["status"] != "sent" {
			t.Fatalf("unexpected event=%+v", received)
		}
	case err := <-failures:
		t.Fatalf("fanout=%v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic MSG fanout did not deliver")
	}
	deadline := time.Now().Add(time.Second)
	var outbox, receipts int64
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("outbox=%d receipts=%d", outbox, receipts)
	}
}

func TestRTECapacityEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerDB := newModuleTestDB(t, "AUD")
	consumerRegistry := NewContractRegistry("AUD")
	delivered := make(chan foundation.EventEnvelope, 1)
	if err := consumerRegistry.RegisterEvent("rte.capacity.changed.v1", func(_ context.Context, event foundation.EventEnvelope) error { delivered <- event; return nil }); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("AUD")
	consumerCfg.AllowedCallers["RTE"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: consumerDB, Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())
	sourceDB := newModuleTestDB(t, "RTE")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-rte-capacity-1", EventType: "rte.capacity.changed.v1", EventVersion: "v1", SourceModule: "RTE", AccountID: 91, AggregateType: "CapacityPolicy", AggregateID: "7", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, CorrelationID: "automatic-rte-correlation", CausationID: "rte-capacity-command", ChangedFields: []string{"conversation_limit"}, Payload: map[string]any{"policy_id": 7, "user_id": 7, "conversation_limit": 12}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "RTE").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("RTE")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"AUD": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { failures <- err })
	select {
	case received := <-delivered:
		if received.SourceModule != "RTE" || received.Payload["conversation_limit"] != float64(12) {
			t.Fatalf("unexpected event=%+v", received)
		}
	case err := <-failures:
		t.Fatalf("fanout=%v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic RTE fanout did not deliver")
	}
	deadline := time.Now().Add(time.Second)
	var outbox, receipts int64
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("outbox=%d receipts=%d", outbox, receipts)
	}
}

func TestTKTTicketCreatedEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerRegistry := NewContractRegistry("AUD")
	delivered := make(chan foundation.EventEnvelope, 1)
	if err := consumerRegistry.RegisterEvent("tkt.ticket.created.v1", func(_ context.Context, event foundation.EventEnvelope) error { delivered <- event; return nil }); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("AUD")
	consumerCfg.AllowedCallers["TKT"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: newModuleTestDB(t, "AUD"), Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())
	sourceDB := newModuleTestDB(t, "TKT")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-tkt-create-1", EventType: "tkt.ticket.created.v1", EventVersion: "v1", SourceModule: "TKT", AccountID: 91, AggregateType: "Ticket", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, CorrelationID: "automatic-tkt-correlation", CausationID: "tkt-create-command", ChangedFields: []string{"created"}, Payload: map[string]any{"ticket_id": 101, "ticket_number": "TKT-101", "status": "open"}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "TKT").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("TKT")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"AUD": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { failures <- err })
	select {
	case received := <-delivered:
		if received.SourceModule != "TKT" || received.Payload["status"] != "open" {
			t.Fatalf("unexpected event=%+v", received)
		}
	case err := <-failures:
		t.Fatalf("fanout=%v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic TKT fanout did not deliver")
	}
	deadline := time.Now().Add(time.Second)
	var outbox, receipts int64
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("outbox=%d receipts=%d", outbox, receipts)
	}
}

func TestQLTQualityEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerRegistry := NewContractRegistry("AUD")
	delivered := make(chan foundation.EventEnvelope, 1)
	if err := consumerRegistry.RegisterEvent("qlt.quality_review.changed.v1", func(_ context.Context, event foundation.EventEnvelope) error { delivered <- event; return nil }); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("AUD")
	consumerCfg.AllowedCallers["QLT"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: newModuleTestDB(t, "AUD"), Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())
	sourceDB := newModuleTestDB(t, "QLT")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-qlt-review-1", EventType: "qlt.quality_review.changed.v1", EventVersion: "v1", SourceModule: "QLT", AccountID: 91, AggregateType: "QATask", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, CorrelationID: "automatic-qlt-correlation", CausationID: "qlt-review-command", ChangedFields: []string{"created"}, Payload: map[string]any{"task_id": 101, "status": "pending"}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "QLT").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("QLT")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"AUD": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { failures <- err })
	select {
	case received := <-delivered:
		if received.SourceModule != "QLT" || received.Payload["status"] != "pending" {
			t.Fatalf("unexpected event=%+v", received)
		}
	case err := <-failures:
		t.Fatalf("fanout=%v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic QLT fanout did not deliver")
	}
	deadline := time.Now().Add(time.Second)
	var outbox, receipts int64
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("outbox=%d receipts=%d", outbox, receipts)
	}
}

func TestKBKnowledgeEventWorkerAutomaticallyDeliversPendingOutbox(t *testing.T) {
	consumerRegistry := NewContractRegistry("AUD")
	delivered := make(chan foundation.EventEnvelope, 1)
	if err := consumerRegistry.RegisterEvent("kb.knowledge.published.v1", func(_ context.Context, event foundation.EventEnvelope) error { delivered <- event; return nil }); err != nil {
		t.Fatal(err)
	}
	consumerCfg := businessConfig("AUD")
	consumerCfg.AllowedCallers["KB"] = true
	server, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: consumerCfg, DB: newModuleTestDB(t, "AUD"), Contracts: consumerRegistry}.Handler())
	defer server.Shutdown(context.Background())
	sourceDB := newModuleTestDB(t, "KB")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "automatic-kb-published-1", EventType: "kb.knowledge.published.v1", EventVersion: "v1", SourceModule: "KB", AccountID: 91, AggregateType: "Article", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, CorrelationID: "automatic-kb-correlation", CausationID: "kb-publish-command", ChangedFields: []string{"status"}, Payload: map[string]any{"article_id": 101, "status": "published"}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "KB").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	sourceCfg := businessConfig("KB")
	sourceCfg.EventPollInterval = 10 * time.Millisecond
	sourceCfg.EventTargets = map[string]EventFanoutTarget{"AUD": {Dependency: DependencyConfig{URL: "http://" + address, Token: consumerCfg.AuthToken}, EventTypes: map[string]bool{event.EventType: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failures := make(chan error, 1)
	go (Runtime{Config: sourceCfg, DB: sourceDB}).RunEventFanout(ctx, func(err error) { failures <- err })
	select {
	case received := <-delivered:
		if received.SourceModule != "KB" || received.Payload["status"] != "published" {
			t.Fatalf("unexpected event=%+v", received)
		}
	case err := <-failures:
		t.Fatalf("fanout=%v", err)
	case <-time.After(time.Second):
		t.Fatal("automatic KB fanout did not deliver")
	}
	deadline := time.Now().Add(time.Second)
	var outbox, receipts int64
	for time.Now().Before(deadline) {
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&outbox).Error
		_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
		if outbox == 1 && receipts == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outbox != 1 || receipts != 1 {
		t.Fatalf("outbox=%d receipts=%d", outbox, receipts)
	}
}
