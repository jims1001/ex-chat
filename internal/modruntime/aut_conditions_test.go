package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
)

// Exercise the unchanged real legacy service as the oracle, rather than keeping
// a second handwritten implementation of its matching semantics in a test.
func TestAUTConditionsMatchActualLegacyService(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	if err := db.AutoMigrate(&domain.Conversation{}, &domain.ConversationLabel{}, &domain.Label{}, &domain.Contact{}, &domain.ContactLabel{}, &domain.Company{}, &domain.Message{}); err != nil {
		t.Fatal(err)
	}
	assignee, team, companyID := uint(31), uint(41), uint(51)
	conv := domain.Conversation{ID: 1, AccountID: 91, InboxID: 11, ContactID: 21, AssigneeID: &assignee, TeamID: &team, Status: "open", Priority: "medium", CustomAttributes: `{"browser_language":"en-US","conversation_language":"en","referer":"https://Example.test/store","mail_subject":"Order","tier":3,"empty":"","null":null,"nested":{"a":1}}`, ObjectVersion: 1}
	contact := domain.Contact{ID: 21, AccountID: 91, CompanyID: &companyID, Email: "VIP@example.test", PhoneNumber: "+12345", CustomAttributes: `{"country_code":"GB","city":"London","company_name":"Fallback"}`}
	company := domain.Company{ID: 51, AccountID: 91, Name: "Acme"}
	labels := []domain.Label{{ID: 61, AccountID: 91, Title: "VIP Customer"}, {ID: 62, AccountID: 91, Title: "Billing"}}
	for _, model := range []any{&conv, &contact, &company, &labels, &domain.ConversationLabel{ConversationID: conv.ID, LabelID: 61}, &domain.ConversationLabel{ConversationID: conv.ID, LabelID: 62}} {
		if err := db.Create(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	view, _ := conversationFromModel(db, &conv)
	view.CustomAttributes = conv.CustomAttributes
	observation := &automationConditionContext{Conversation: *view, Contact: contactFromModel(&contact), Company: companyFromModel(&company), Labels: []labelView{*labelFromModel(&labels[0]), *labelFromModel(&labels[1])}, contactLoaded: true, labelsLoaded: true}
	msg := domain.Message{ID: 71, AccountID: 91, ConversationID: 1, Content: "Hello CUSTOMER", MessageType: "incoming", Private: false}
	observation.Contact.CustomAttributes = contact.CustomAttributes
	messageObservation := *observation
	message := messageFromModel(msg)
	messageObservation.Message = &message
	condition := func(key, operator string, values ...any) string {
		raw, _ := json.Marshal([]map[string]any{{"attribute_key": key, "filter_operator": operator, "values": values}})
		return string(raw)
	}
	cases := []struct {
		name, raw string
		message   bool
	}{
		{"empty", "", false}, {"empty_array", "[]", false}, {"empty_object", "{}", false},
		{"status", condition("status", "equal_to", "open"), false}, {"case_sensitive_scalar", condition("status", "is", "OPEN"), false},
		{"priority", condition("priority", "not_equal_to", "urgent", "low"), false}, {"inbox_numeric", condition("inbox_id", "is", 11), false},
		{"agent_string", condition("assignee_id", "equal_to", "31"), false}, {"team", condition("team_id", "is", 41), false}, {"contact_id", condition("contact_id", "is", 21), false},
		{"email_contains", condition("email", "contains", "vip"), false}, {"phone_prefix", condition("phone_number", "starts_with", "+123"), false},
		{"country", condition("country_code", "is", "GB"), false}, {"city", condition("city", "includes", "LON"), false}, {"company", condition("company_name", "is", "Acme"), false},
		{"browser", condition("browser_language", "starts_with", "EN"), false}, {"language", condition("conversation_language", "is", "en"), false},
		{"referer", condition("referer", "contains", "example.TEST"), false}, {"subject", condition("mail_subject", "is", "Order"), false},
		{"legacy_numeric_inert", condition("tier", "is_greater_than", 2), false},
		{"legacy_numeric_or", `[{"key":"tier","operator":"is_greater_than","value":2,"query_operator":"OR"},{"key":"status","operator":"is","value":"open"}]`, false},
		{"custom_number", condition("tier", "is", "3"), false}, {"custom_empty", condition("empty", "is_not_present"), false}, {"custom_null", condition("null", "is_not_present"), false},
		{"unknown_missing", condition("missing", "is_not_present"), false}, {"unknown_operator", condition("status", "future_operator", "open"), false},
		{"nil_message", condition("content", "is_not_present"), false}, {"nil_message_negative", condition("content", "does_not_contain", "hi"), false},
		{"content", condition("content", "includes", "customer"), true}, {"message_type", condition("message_type", "is", "incoming"), true}, {"private_false_present", condition("private_note", "is_present"), true}, {"private_false", condition("private_note", "is", false), true},
		{"labels_title", condition("labels", "equal_to", "vip CUSTOMER"), false}, {"label_id", condition("label_ids", "is", "61"), false}, {"labels_contains", condition("tags", "contains", "CUSTOM"), false},
		{"labels_not_equal", condition("conversation_labels", "is_not", "missing"), false}, {"labels_negative", condition("label", "does_not_contain", "VIP"), false}, {"label_not_contains_id", condition("tag", "does_not_contain", 61), false},
		{"labels_empty_values", condition("labels", "equal_to", " "), false}, {"labels_present", condition("labels", "is_present"), false}, {"labels_unsupported_prefix", condition("labels", "starts_with", "VIP"), false},
		{"alias_single", `{"key":"status","operator":"is","value":"open"}`, false},
		{"conditions_wrapper", `{"conditions":[{"key":"status","operator":"is","value":"open"}]}`, false},
		{"rules_wrapper", `{"rules":[{"key":"priority","operator":"is","value":"medium"}]}`, false},
		{"values_wrapper", `{"values":[{"key":"contact_id","operator":"is","value":21}]}`, false},
		{"and_or_fold", `[{"key":"status","operator":"is","value":"closed","query_operator":"OR"},{"key":"priority","operator":"is","value":"medium","query_operator":"AND"},{"key":"inbox_id","operator":"is","value":99}]`, false},
		{"or_from_previous", `[{"key":"status","operator":"is","value":"closed","query_operator":"or"},{"key":"priority","operator":"is","value":"medium"}]`, false},
		{"malformed", "{", false}, {"empty_wrapper", `{"conditions":[]}`, false}, {"null", "null", false}, {"unknown_shape", `{"anything":[]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Model(&domain.AutomationRule{}).Where("account_id = ?", 91).Update("active", false).Error; err != nil {
				t.Fatal(err)
			}
			event := "conversation_created"
			if tc.message {
				event = "message_created"
			}
			rule := domain.AutomationRule{AccountID: 91, Name: tc.name, EventName: event, Active: true, Conditions: tc.raw, Actions: `[{"action_name":"change_priority","action_params":["urgent"]}]`}
			if err := db.Create(&rule).Error; err != nil {
				t.Fatal(err)
			}
			svc := service.NewAutomationService(db, repository.NewConversationRepository(db), repository.NewMessageRepository(db))
			legacyConversation := conv
			if tc.message {
				svc.HandleMessageCreated(&legacyConversation, &msg)
			} else {
				svc.HandleConversationCreated(&legacyConversation)
			}
			var count int64
			if err := db.Model(&domain.AutomationRuleExecution{}).Where("rule_id = ?", rule.ID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			observed := observation
			if tc.message {
				observed = &messageObservation
			}
			matched, reason := matchAutomationConditions(tc.raw, observed)
			if matched != (count == 1) {
				t.Fatalf("legacy matched=%v durable=%v reason=%s raw=%s", count == 1, matched, reason, tc.raw)
			}
		})
	}
}

func TestAUTConditionOwnerDependenciesAreDemandDrivenAndFailClosed(t *testing.T) {
	conv := conversationView{ID: 11, AccountID: 91, ContactID: 21, ObjectVersion: 1, Status: "open", LabelIDs: []uint{61}}
	cfg := businessConfig("AUT")
	r := Runtime{Config: cfg}
	for _, raw := range []string{"[]", `[{"key":"status","operator":"is","value":"open"}]`, `{"conditions":[]}`} {
		if _, err := r.prepareAutomationConditionContext(context.Background(), foundation.ContractActor{Type: "System", ID: "test", AuthSource: "module"}, conv, nil, []domain.AutomationRuleVersion{{Conditions: raw}}); err != nil {
			t.Fatalf("unneeded CUS dependency: %s %v", raw, err)
		}
	}
	var calls []string
	var callsMu sync.Mutex
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var q foundation.QueryEnvelope
		_ = json.NewDecoder(req.Body).Decode(&q)
		callsMu.Lock()
		calls = append(calls, q.QueryType)
		callsMu.Unlock()
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		var data any
		switch q.QueryType {
		case "cus.contact.get.v1":
			data = contactView{ID: 21, AccountID: 91, Email: "vip@test", CustomAttributes: `{"city":"Paris"}`}
		case "cus.label.get.v1":
			data = labelView{ID: 61, AccountID: 91, Title: "VIP"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	r.Config.Dependencies["CUS"] = DependencyConfig{URL: server.URL, Token: "test"}
	raw := `[{"key":"email","operator":"contains","value":"vip"},{"key":"labels","operator":"is","value":"vip"}]`
	versions := []domain.AutomationRuleVersion{{Conditions: raw}, {Conditions: raw}}
	observed, err := r.prepareAutomationConditionContext(context.Background(), foundation.ContractActor{Type: "System", ID: "test", AuthSource: "module"}, conv, nil, versions)
	if err != nil {
		t.Fatal(err)
	}
	callsMu.Lock()
	if len(calls) != 2 {
		t.Fatalf("shared observation queried owners repeatedly: %v", calls)
	}
	callsMu.Unlock()
	if match, reason := matchAutomationConditions(raw, observed); !match || reason != "" {
		t.Fatalf("match=%v reason=%s", match, reason)
	}
	fail.Store(true)
	if _, err := r.prepareAutomationConditionContext(context.Background(), foundation.ContractActor{Type: "System", ID: "test", AuthSource: "module"}, conv, nil, versions); err == nil {
		t.Fatal("dependency failure became an empty context")
	}
}

func TestAUTConditionsFreezeAtDirectAdmissionAndReplay(t *testing.T) {
	testAUTConditionFrozenAdmission(t, false)
}
func TestAUTConditionsPostgresFreezeAtDirectAdmissionAndReplay(t *testing.T) {
	testAUTConditionFrozenAdmission(t, true)
}
func testAUTConditionFrozenAdmission(t *testing.T, pg bool) {
	f := newAUTRunnerFixture(t, pg, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	raw := `[{"key":"status","operator":"is","value":"resolved"}]`
	if err := f.aut.DB.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", f.rule.ID).Update("conditions", raw).Error; err != nil {
		t.Fatal(err)
	}
	execution := f.call(f.startCommand(), 200)
	if execution.Status != "no_match" {
		t.Fatalf("execution=%+v", execution)
	}
	frozen := f.execution(execution.ID)
	if frozen.Error != "conditions_not_matched" || frozen.CompletedAt == nil || frozen.RunnerContext != "" || !strings.Contains(frozen.ActionResults, `"status":"skipped"`) {
		t.Fatalf("no-match checkpoint=%+v", frozen)
	}
	rawSnapshot, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{frozen})
	if err != nil {
		t.Fatal(err)
	}
	var restored []domain.AutomationRuleExecution
	if err := unmarshalAutomationSnapshot(rawSnapshot, &restored); err != nil || len(restored) != 1 || restored[0].Status != "no_match" || restored[0].RunnerContext != "" {
		t.Fatalf("no-match snapshot changed decision: %+v %v", restored, err)
	}
	if err := validateRestoredAutomationSnapshot(f.aut.DB); err != nil {
		t.Fatalf("no-match snapshot violated causal proof: %v", err)
	}
	if err := f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("status", "resolved").Error; err != nil {
		t.Fatal(err)
	}
	delete(f.aut.Config.Dependencies, "CON")
	command := f.startCommand()
	command.CommandID = "replay-new-command"
	command.IdempotencyKey = command.CommandID
	replay := f.call(command, 200)
	if replay.ID != frozen.ID || replay.Status != "no_match" {
		t.Fatalf("reevaluated no-match: %+v", replay)
	}
	if ran, err := f.aut.RunAutomationOnce(context.Background()); err != nil || ran {
		t.Fatalf("no-match dispatched: %v %v", ran, err)
	}
}

func TestAUTReceiptPreflightOutsideTransactionAndReplay(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	store := NewCommandReceiptStore(db, "AUT")
	command := foundation.CommandEnvelope{CommandID: "prepared", CommandType: "aut.test.v1", AccountID: 91, IdempotencyKey: "prepared", Input: map[string]any{"value": 1}, RequestedAt: time.Now().UTC()}
	calls := 0
	prepare := func(ctx context.Context, c foundation.CommandEnvelope) (context.Context, error) {
		calls++
		var count int64
		err := db.WithContext(ctx).Model(&domain.AutomationRule{}).Count(&count).Error
		return ctx, err
	}
	run := func(ctx context.Context) (any, error) { return map[string]any{"ok": true}, nil }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := store.ExecutePrepared(ctx, command, prepare, run); err != nil {
		t.Fatal(err)
	}
	rejecting := func(ctx context.Context, c foundation.CommandEnvelope) (context.Context, error) {
		return ctx, fmt.Errorf("owner unavailable")
	}
	if _, err := store.ExecutePrepared(ctx, command, rejecting, run); err != nil {
		t.Fatalf("replay consulted unavailable owner: %v", err)
	}
	command.Input["value"] = 2
	if _, err := store.ExecutePrepared(ctx, command, rejecting, run); err == nil || !strings.Contains(err.Error(), "different command input") {
		t.Fatalf("conflicting receipt accepted: %v", err)
	}
	if calls != 1 {
		t.Fatalf("preflight calls=%d", calls)
	}
}

func TestAUTReceiptPreparationRaceUsesAcceptedReceipt(t *testing.T) {
	db := newModuleTestDB(t, "AUT")
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	store := NewCommandReceiptStore(db, "AUT")
	command := foundation.CommandEnvelope{CommandID: "race-prepared", CommandType: "aut.test.v1", AccountID: 91, IdempotencyKey: "race-prepared", Input: map[string]any{}, RequestedAt: time.Now().UTC()}
	entered, release := make(chan struct{}), make(chan struct{})
	var effects atomic.Int64
	run := func(ctx context.Context) (any, error) { effects.Add(1); return map[string]any{"accepted": true}, nil }
	result := make(chan error, 1)
	go func() {
		_, err := store.ExecutePrepared(context.Background(), command, func(ctx context.Context, c foundation.CommandEnvelope) (context.Context, error) {
			close(entered)
			<-release
			return ctx, fmt.Errorf("dependency failed during racing preparation")
		}, run)
		result <- err
	}()
	<-entered
	if _, err := store.Execute(context.Background(), command, run); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("committed duplicate lost to preflight failure: %v", err)
	}
	if effects.Load() != 1 {
		t.Fatalf("effects=%d", effects.Load())
	}
	command.IdempotencyKey = "failed-preparation"
	if _, err := store.ExecutePrepared(context.Background(), command, func(ctx context.Context, c foundation.CommandEnvelope) (context.Context, error) {
		return ctx, fmt.Errorf("dependency unavailable")
	}, run); err == nil {
		t.Fatal("failed preparation admitted command")
	}
	var count int64
	db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&count)
	if count != 1 || effects.Load() != 1 {
		t.Fatalf("failed preparation persisted receipt/effect: %d %d", count, effects.Load())
	}
}

func TestAUTConditionsRejectRuleChangedAfterPreparation(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	command := f.startCommand()
	prepared, err := f.aut.prepareAutomationExecution(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewAutomationService(f.aut.DB, nil, nil)
	if err := svc.UpdateRule(&f.rule, map[string]any{"conditions": `[{"key":"status","operator":"is","value":"resolved"}]`}); err != nil {
		t.Fatal(err)
	}
	_, err = NewCommandReceiptStore(f.aut.DB, "AUT").Execute(prepared, command, func(ctx context.Context) (any, error) { return f.aut.startAutomationExecution(ctx, command) })
	if err == nil || !strings.Contains(err.Error(), "rule version changed") {
		t.Fatalf("changed definition used prepared observation: %v", err)
	}
	var count int64
	f.aut.DB.Model(&domain.AutomationRuleExecution{}).Count(&count)
	if count != 0 {
		t.Fatalf("changed rule admitted %d executions", count)
	}
}

func TestAUTConditionsDirectLocalOwnerUsesSingleConnection(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"change_priority","action_params":["urgent"]}]`)
	db := f.aut.DB
	if err := db.AutoMigrate(&domain.Conversation{}, &domain.ConversationLabel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&f.conversation).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	cfg := businessConfig("CON")
	cfg.AllowedCallers["AUT"] = true
	con := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
	if err := con.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	f.aut.Config.Dependencies["CON"] = DependencyConfig{URL: "http://local-con", Token: cfg.AuthToken, LocalHandler: con.Handler()}
	if err := db.Model(&domain.AutomationRuleVersion{}).Where("rule_id = ?", f.rule.ID).Update("conditions", `[{"key":"status","operator":"is","value":"open"}]`).Error; err != nil {
		t.Fatal(err)
	}
	execution := f.call(f.startCommand(), 200)
	if execution.Status != "received" {
		t.Fatalf("prepared local conditions not queued: %+v", execution)
	}
}

func TestAUTNewOwnerActionReceiptBindsActorWithoutChangingExistingDigest(t *testing.T) {
	command := foundation.CommandEnvelope{CommandType: "con.automation.apply.v1", AccountID: 91, Target: foundation.ContractTarget{Type: "conversation", ID: "1"}, Input: map[string]any{"x": 1}, Actor: foundation.ContractActor{Type: "System", ID: "automation", AuthSource: "module"}}
	first, err := commandDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	command.Actor.Type = "User"
	second, _ := commandDigest(command)
	if first == second {
		t.Fatal("new owner action receipt ignored changed actor")
	}
	command.CommandType = "con.conversation.change_status.v1"
	first, _ = commandDigest(command)
	command.Actor.Type = "System"
	second, _ = commandDigest(command)
	if first != second {
		t.Fatal("existing command receipt digest changed semantics")
	}
	old := struct {
		Type      string
		AccountID uint
		Target    foundation.ContractTarget
		Input     map[string]any
	}{command.CommandType, command.AccountID, command.Target, command.Input}
	raw, _ := json.Marshal(old)
	sum := sha256.Sum256(raw)
	if first != hex.EncodeToString(sum[:]) {
		t.Fatal("existing receipt digest is not byte compatible")
	}
}
