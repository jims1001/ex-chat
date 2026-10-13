package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

func newAutomationOwnerRuntime(t *testing.T, module string) *Runtime {
	t.Helper()
	cfg := businessConfig(module)
	cfg.MaxTimeout = 2 * time.Second
	cfg.AllowedCallers["AUT"], cfg.AllowedCallers["CON"], cfg.AllowedCallers["MSG"] = true, true, true
	runtime := &Runtime{Config: cfg, DB: newModuleTestDB(t, module), Contracts: NewContractRegistry(module)}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	return runtime
}
func automationOwnerCommand(key, kind string, version uint64, input map[string]any) foundation.CommandEnvelope {
	input["origin_execution_id"], input["automation_depth"] = uint(51), uint(1)
	return foundation.CommandEnvelope{CommandID: key, CommandType: kind, AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "automation:7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: "1", ExpectedVersion: version}, Input: input, IdempotencyKey: key, CorrelationID: key, CausationID: "test-fact", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 2000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
}
func callAutomationOwner(t *testing.T, r *Runtime, command foundation.CommandEnvelope, status int) *httptest.ResponseRecorder {
	t.Helper()
	response := performContractRequest(r.Handler(), "/contracts/commands/"+command.CommandType, command, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+r.Config.AuthToken)
		req.Header.Set(headerCaller, "AUT")
		req.Header.Set(headerCorrelationID, command.CorrelationID)
	})
	if response.Code != status {
		t.Fatalf("%s: code=%d body=%s expected=%d", command.CommandID, response.Code, response.Body.String(), status)
	}
	return response
}
func automationOwnerConversation(t *testing.T, response *httptest.ResponseRecorder) conversationView {
	t.Helper()
	var envelope struct {
		Data conversationView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
func seedAutomationOwnerConversation(t *testing.T, r *Runtime) domain.Conversation {
	t.Helper()
	now := time.Now().UTC()
	conversation := domain.Conversation{ID: 1, AccountID: 91, InboxID: 11, ContactID: 21, Status: "open", Priority: "medium", ObjectVersion: 1, CustomAttributes: `{"tier":"gold"}`, CreatedAt: now, UpdatedAt: now, LastActivityAt: now}
	if err := r.DB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	return conversation
}

func TestAutomationOwnerConversationMutationsCASNoopAndRollback(t *testing.T) {
	r := newAutomationOwnerRuntime(t, "CON")
	initial := seedAutomationOwnerConversation(t, r)
	noop := automationOwnerCommand("remove-empty", "con.automation.apply.v1", 1, map[string]any{"operation": "remove_assigned_agent"})
	view := automationOwnerConversation(t, callAutomationOwner(t, r, noop, 200))
	if view.ObjectVersion != 1 || !view.LastActivityAt.Equal(initial.LastActivityAt) || view.CustomAttributes != "" {
		t.Fatalf("no-op changed state: %+v", view)
	}
	mute := automationOwnerCommand("mute", "con.automation.apply.v1", 1, map[string]any{"operation": "mute_conversation"})
	view = automationOwnerConversation(t, callAutomationOwner(t, r, mute, 200))
	if !view.Muted || view.Status != "resolved" || view.ObjectVersion != 2 {
		t.Fatalf("mute=%+v", view)
	}
	changedActor := mute
	changedActor.Actor.ID = "automation:8"
	callAutomationOwner(t, r, changedActor, 409)
	view = automationOwnerConversation(t, callAutomationOwner(t, r, mute, 200))
	if view.ObjectVersion != 2 {
		t.Fatalf("replay=%+v", view)
	}
	noop = automationOwnerCommand("mute-again", "con.automation.apply.v1", 2, map[string]any{"operation": "mute_conversation"})
	if got := automationOwnerConversation(t, callAutomationOwner(t, r, noop, 200)); got.ObjectVersion != 2 {
		t.Fatalf("mute no-op=%+v", got)
	}
	stale := automationOwnerCommand("stale", "con.automation.apply.v1", 1, map[string]any{"operation": "remove_assigned_team"})
	callAutomationOwner(t, r, stale, 409)
	absent := automationOwnerCommand("no-version", "con.automation.apply.v1", 0, map[string]any{"operation": "mute_conversation"})
	callAutomationOwner(t, r, absent, 400)
	foreign := automationOwnerCommand("foreign", "con.automation.apply.v1", 2, map[string]any{"operation": "mute_conversation"})
	foreign.AccountID = 92
	callAutomationOwner(t, r, foreign, 404)
	unauthorized := automationOwnerCommand("manual", "con.automation.apply.v1", 2, map[string]any{"operation": "mute_conversation"})
	unauthorized.Actor.ID = "api"
	callAutomationOwner(t, r, unauthorized, 403)
	noProof := automationOwnerCommand("no-proof", "con.automation.apply.v1", 2, map[string]any{"operation": "mute_conversation"})
	if _, err := r.applyAutomationConversationAction(context.Background(), noProof); err == nil {
		t.Fatal("direct handler bypassed owner validation")
	}
	snooze := automationOwnerCommand("snooze-frozen", "con.automation.apply.v1", 2, map[string]any{"operation": "snooze_conversation", "snoozed_until": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)})
	snooze.RequestedAt = time.Now().Add(-2 * time.Hour).UTC()
	view = automationOwnerConversation(t, callAutomationOwner(t, r, snooze, 200))
	if view.ObjectVersion != 3 || view.Status != "snoozed" || view.SnoozedUntil == nil {
		t.Fatalf("frozen late snooze=%+v", view)
	}
	if err := r.DB.Exec(`CREATE TRIGGER reject_automation_event BEFORE INSERT ON local_change_journals WHEN NEW.action = 'event_outbox' BEGIN SELECT RAISE(ABORT, 'event outbox unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	rollback := automationOwnerCommand("rollback-mute", "con.automation.apply.v1", 3, map[string]any{"operation": "mute_conversation"})
	callAutomationOwner(t, r, rollback, 503)
	var stored domain.Conversation
	if err := r.DB.First(&stored, 1).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ObjectVersion != 3 || stored.Status != "snoozed" || stored.SnoozedUntil == nil {
		t.Fatalf("failed event partially committed mutation: %+v", stored)
	}
	var events []domain.LocalChangeJournal
	if err := r.DB.Where("action = ?", "event_outbox").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%d", len(events))
	}
	for _, row := range events {
		var event foundation.EventEnvelope
		if json.Unmarshal([]byte(row.AfterState), &event) != nil || event.Payload["origin_execution_id"] != float64(51) || event.Payload["automation_depth"] != float64(1) {
			t.Fatalf("missing ancestry: %s", row.AfterState)
		}
	}
	if err := r.DB.Model(&domain.Conversation{}).Where("id = ?", 1).Update("status", domain.ConversationStatusDeleting).Error; err != nil {
		t.Fatal(err)
	}
	deleted := automationOwnerCommand("deleting", "con.automation.apply.v1", 3, map[string]any{"operation": "remove_assigned_agent"})
	callAutomationOwner(t, r, deleted, 404)
}

func TestAutomationOwnerAssignmentPreflightAndFrozenLabels(t *testing.T) {
	con := newAutomationOwnerRuntime(t, "CON")
	pool, err := con.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	seedAutomationOwnerConversation(t, con)
	var allowed atomic.Bool
	allowed.Store(true)
	var checks atomic.Int32
	// Reading CON here proves the owner transaction has not started: the SQLite
	// runtime has one connection, so an in-transaction validation would time out.
	ten := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks.Add(1)
		var q foundation.QueryEnvelope
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var count int64
		if err := con.DB.WithContext(r.Context()).Model(&domain.Conversation{}).Count(&count).Error; err != nil {
			w.WriteHeader(503)
			return
		}
		if id, err := uintFilter(q.Filter, "user_id"); err == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": membershipCheckView{AccountID: q.AccountID, UserID: id, Allowed: allowed.Load() && id == 17}})
			return
		}
		id, _ := uintFilter(q.Filter, "team_id")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": teamView{AccountID: q.AccountID, TeamID: id, Allowed: allowed.Load() && id == 27}})
	}))
	defer ten.Close()
	con.Config.Dependencies["TEN"] = DependencyConfig{URL: ten.URL, Token: "test"}
	assign := automationOwnerCommand("assign", "con.automation.apply.v1", 1, map[string]any{"operation": "assign_agent", "assignee_id": uint(17)})
	view := automationOwnerConversation(t, callAutomationOwner(t, con, assign, 200))
	if view.AssigneeID == nil || *view.AssigneeID != 17 || view.ObjectVersion != 2 {
		t.Fatalf("assign=%+v", view)
	}
	allowed.Store(false)
	before := checks.Load()
	callAutomationOwner(t, con, assign, 200)
	if checks.Load() != before {
		t.Fatal("receipt replay consulted mutable owner validation")
	}
	invalid := automationOwnerCommand("foreign-assignee", "con.automation.apply.v1", 2, map[string]any{"operation": "assign_agent", "assignee_id": uint(99)})
	callAutomationOwner(t, con, invalid, 404)
	allowed.Store(true)
	team := automationOwnerCommand("team", "con.automation.apply.v1", 2, map[string]any{"operation": "assign_team", "team_id": uint(27)})
	view = automationOwnerConversation(t, callAutomationOwner(t, con, team, 200))
	if view.TeamID == nil || *view.TeamID != 27 || view.ObjectVersion != 3 {
		t.Fatalf("team=%+v", view)
	}
	for i, op := range []string{"remove_assigned_agent", "remove_assigned_team"} {
		cmd := automationOwnerCommand(op, "con.automation.apply.v1", uint64(3+i), map[string]any{"operation": op})
		view = automationOwnerConversation(t, callAutomationOwner(t, con, cmd, 200))
	}
	if view.AssigneeID != nil || view.TeamID != nil || view.ObjectVersion != 5 {
		t.Fatalf("removals=%+v", view)
	}
	cus := newAutomationOwnerRuntime(t, "CUS")
	own := domain.Label{AccountID: 91, Title: "gold"}
	foreign := domain.Label{AccountID: 92, Title: "foreign"}
	if err := cus.DB.Create(&own).Error; err != nil {
		t.Fatal(err)
	}
	if err := cus.DB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(cus.Handler())
	defer server.Close()
	con.Config.Dependencies["CUS"] = DependencyConfig{URL: server.URL, Token: cus.Config.AuthToken}
	label := automationOwnerCommand("label", "con.automation.apply.v1", 5, map[string]any{"operation": "add_label", "label_id": own.ID})
	view = automationOwnerConversation(t, callAutomationOwner(t, con, label, 200))
	if view.ObjectVersion != 6 || len(view.LabelIDs) != 1 || view.LabelIDs[0] != own.ID {
		t.Fatalf("label=%+v", view)
	}
	duplicate := automationOwnerCommand("label-again", "con.automation.apply.v1", 6, map[string]any{"operation": "add_label", "label_id": own.ID})
	if got := automationOwnerConversation(t, callAutomationOwner(t, con, duplicate, 200)); got.ObjectVersion != 6 {
		t.Fatalf("duplicate label bumped version: %+v", got)
	}
	bad := automationOwnerCommand("foreign-label", "con.automation.apply.v1", 6, map[string]any{"operation": "add_label", "label_id": foreign.ID})
	callAutomationOwner(t, con, bad, 404)
	missing := automationOwnerCommand("remove-missing", "con.automation.apply.v1", 6, map[string]any{"operation": "remove_label", "label_id": 0})
	if got := automationOwnerConversation(t, callAutomationOwner(t, con, missing, 200)); got.ObjectVersion != 6 {
		t.Fatalf("missing removal=%+v", got)
	}
	remove := automationOwnerCommand("remove-label", "con.automation.apply.v1", 6, map[string]any{"operation": "remove_label", "label_id": own.ID})
	view = automationOwnerConversation(t, callAutomationOwner(t, con, remove, 200))
	if view.ObjectVersion != 7 || len(view.LabelIDs) != 0 {
		t.Fatalf("remove=%+v", view)
	}
	for _, foreign := range []any{&domain.AccountUser{}, &domain.Team{}, &domain.Label{}, &domain.Message{}} {
		if con.DB.Migrator().HasTable(foreign) {
			t.Fatalf("CON gained foreign table %T", foreign)
		}
	}
}

func TestAutomationOwnerMessagePreparationIsBoundedImmutableAndContentFree(t *testing.T) {
	con := newAutomationOwnerRuntime(t, "CON")
	seedAutomationOwnerConversation(t, con)
	cmd := automationOwnerCommand("prepare-note", "con.automation.message.prepare.v1", 1, map[string]any{"content": "secret automation note", "private": true})
	view := automationOwnerConversation(t, callAutomationOwner(t, con, cmd, 200))
	if view.ObjectVersion != 2 {
		t.Fatalf("prepared=%+v", view)
	}
	callAutomationOwner(t, con, cmd, 200)
	changedActor := cmd
	changedActor.Actor.ID = "automation:8"
	callAutomationOwner(t, con, changedActor, 409)
	var journals []domain.LocalChangeJournal
	if err := con.DB.Find(&journals).Error; err != nil {
		t.Fatal(err)
	}
	if len(journals) != 2 {
		t.Fatalf("prepare rows=%d", len(journals))
	}
	for _, journal := range journals {
		if strings.Contains(journal.AfterState, "secret automation note") || journal.Action == "event_outbox" {
			t.Fatalf("prepare leaked content or premature fact: %+v", journal)
		}
	}
	query := foundation.QueryEnvelope{QueryType: "con.automation.message_authorization.get.v1", AccountID: 91, Actor: cmd.Actor, Filter: map[string]any{"prepare_command_id": cmd.CommandID, "conversation_id": 1}, Sort: []string{"id"}, Page: "1", Fields: []string{"account_id", "conversation_id", "content_hash"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	got, err := con.getAutomationMessageAuthorization(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	grant := got.(automationMessageAuthorizationView)
	hash := sha256.Sum256([]byte("secret automation note"))
	if grant.ObjectVersion != 2 || grant.ContentHash != hex.EncodeToString(hash[:]) || !grant.Private || grant.OriginExecutionID != 51 || grant.AutomationDepth != 1 || grant.ActorID != cmd.Actor.ID {
		t.Fatalf("grant=%+v", grant)
	}
	query.AccountID = 92
	if _, err := con.getAutomationMessageAuthorization(context.Background(), query); err == nil {
		t.Fatal("foreign account grant read")
	}
	query.AccountID = 91
	query.Actor.ID = "automation:8"
	if _, err := con.getAutomationMessageAuthorization(context.Background(), query); err == nil {
		t.Fatal("foreign actor grant read")
	}
	query.Actor = cmd.Actor
	query.Filter["conversation_id"] = 2
	if _, err := con.getAutomationMessageAuthorization(context.Background(), query); err == nil {
		t.Fatal("foreign conversation grant read")
	}
	query.Filter["conversation_id"] = 1
	if err := con.DB.Model(&domain.Conversation{}).Where("id = ?", 1).Update("object_version", 3).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := con.getAutomationMessageAuthorization(context.Background(), query); err == nil {
		t.Fatal("stale preparation still authorizes message")
	}
	callAutomationOwner(t, con, cmd, 200)
	reused := cmd
	reused.IdempotencyKey = "different-receipt"
	reused.Target.ExpectedVersion = 3
	callAutomationOwner(t, con, reused, 409)
	var stored domain.Conversation
	if err := con.DB.First(&stored, 1).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ObjectVersion != 3 {
		t.Fatalf("duplicate prepare partially committed: %+v", stored)
	}
}

func TestAutomationOwnerCustomerLabelResolutionScopesReplaysAndMissingRemoval(t *testing.T) {
	cus := newAutomationOwnerRuntime(t, "CUS")
	makeCommand := func(key, operation, label string) foundation.CommandEnvelope {
		cmd := automationOwnerCommand(key, "cus.automation.label.resolve.v1", 0, map[string]any{"operation": operation, "label": label})
		cmd.Target = foundation.ContractTarget{Type: "account", ID: "91"}
		return cmd
	}
	getLabel := func(response *httptest.ResponseRecorder) labelView {
		var envelope struct {
			Data labelView `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	create := makeCommand("create-label", "add", " gold ")
	first := getLabel(callAutomationOwner(t, cus, create, 200))
	if first.ID == 0 || first.AccountID != 91 || first.Title != "gold" {
		t.Fatalf("created=%+v", first)
	}
	changedActor := create
	changedActor.Actor.ID = "automation:8"
	callAutomationOwner(t, cus, changedActor, 409)
	if replay := getLabel(callAutomationOwner(t, cus, create, 200)); replay.ID != first.ID {
		t.Fatalf("replay=%+v", replay)
	}
	if same := getLabel(callAutomationOwner(t, cus, makeCommand("same-title", "add", "gold"), 200)); same.ID != first.ID {
		t.Fatalf("find=%+v", same)
	}
	for i, target := range []string{"gold", strconv.Itoa(int(first.ID))} {
		if found := getLabel(callAutomationOwner(t, cus, makeCommand(fmt.Sprintf("remove-%d", i), "remove", target), 200)); found.ID != first.ID {
			t.Fatalf("lookup=%+v", found)
		}
	}
	for i, target := range []string{"missing", "9999", "1.5"} {
		if missing := getLabel(callAutomationOwner(t, cus, makeCommand(fmt.Sprintf("missing-%d", i), "remove", target), 200)); missing.ID != 0 || missing.AccountID != 91 {
			t.Fatalf("missing=%+v", missing)
		}
	}
	foreign := domain.Label{AccountID: 92, Title: "other"}
	if err := cus.DB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if missing := getLabel(callAutomationOwner(t, cus, makeCommand("foreign-id", "remove", strconv.Itoa(int(foreign.ID))), 200)); missing.ID != 0 {
		t.Fatalf("foreign=%+v", missing)
	}
	handler := cus.Handler()
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := makeCommand(fmt.Sprintf("parallel-%d", i), "add", "concurrent")
			// SQLite may reject a read-to-write upgrade under contention. The
			// retriable result must leave neither a label nor a receipt behind.
			var response *httptest.ResponseRecorder
			for attempt := 0; attempt < 20; attempt++ {
				response = performContractRequest(handler, "/contracts/commands/"+cmd.CommandType, cmd, func(req *http.Request) {
					req.Header.Set("Authorization", "Bearer "+cus.Config.AuthToken)
					req.Header.Set(headerCaller, "AUT")
					req.Header.Set(headerCorrelationID, cmd.CorrelationID)
				})
				if response.Code != 503 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if response.Code != 200 {
				errs <- fmt.Errorf("parallel %d: %d %s", i, response.Code, response.Body.String())
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var count int64
	if err := cus.DB.Model(&domain.Label{}).Where("account_id = ? AND title = ?", 91, "concurrent").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("concurrent labels=%d err=%v", count, err)
	}
}

func TestAutomationOwnerNativePGConcurrentMuteAndLabelResolution(t *testing.T) {
	conConfig := businessConfig("CON")
	conConfig.MaxTimeout = 10 * time.Second
	conConfig.AllowedCallers["AUT"] = true
	con := &Runtime{Config: conConfig, DB: postgresModuleDB(t, "CON"), Contracts: NewContractRegistry("CON")}
	if err := con.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	seedAutomationOwnerConversation(t, con)
	// Independent commands at one expected version serialize at the owner row:
	// exactly one commits and the losing CAS cannot produce a second owner fact.
	var wg sync.WaitGroup
	conHandler := con.Handler()
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := automationOwnerCommand(fmt.Sprintf("pg-mute-%d", i), "con.automation.apply.v1", 1, map[string]any{"operation": "mute_conversation"})
			response := performContractRequest(conHandler, "/contracts/commands/"+cmd.CommandType, cmd, func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+con.Config.AuthToken)
				req.Header.Set(headerCaller, "AUT")
				req.Header.Set(headerCorrelationID, cmd.CorrelationID)
			})
			codes <- response.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("CAS responses=%v", counts)
	}
	var stored domain.Conversation
	if err := con.DB.First(&stored, 1).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ObjectVersion != 2 || !stored.Muted || stored.Status != "resolved" {
		t.Fatalf("muted=%+v", stored)
	}
	var events int64
	if err := con.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("events=%d err=%v", events, err)
	}
	cusConfig := businessConfig("CUS")
	cusConfig.MaxTimeout = 10 * time.Second
	cusConfig.AllowedCallers["AUT"] = true
	cus := &Runtime{Config: cusConfig, DB: postgresModuleDB(t, "CUS"), Contracts: NewContractRegistry("CUS")}
	if err := cus.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	cusHandler := cus.Handler()
	errors := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := automationOwnerCommand(fmt.Sprintf("pg-label-%d", i), "cus.automation.label.resolve.v1", 0, map[string]any{"operation": "add", "label": "concurrent"})
			cmd.Target = foundation.ContractTarget{Type: "account", ID: "91"}
			cmd.TimeoutPolicy.TimeoutMS = 10000
			response := performContractRequest(cusHandler, "/contracts/commands/"+cmd.CommandType, cmd, func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+cus.Config.AuthToken)
				req.Header.Set(headerCaller, "AUT")
				req.Header.Set(headerCorrelationID, cmd.CorrelationID)
			})
			if response.Code != 200 {
				errors <- fmt.Sprintf("label %d: %d %s", i, response.Code, response.Body.String())
			}
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var labels int64
	if err := cus.DB.Model(&domain.Label{}).Where("account_id = ? AND title = ?", 91, "concurrent").Count(&labels).Error; err != nil || labels != 1 {
		t.Fatalf("labels=%d err=%v", labels, err)
	}
}

func TestAutomationOwnerConditionViewsRequireExplicitModuleFields(t *testing.T) {
	for _, module := range []string{"CON", "CUS"} {
		t.Run(module, func(t *testing.T) {
			r := newAutomationOwnerRuntime(t, module)
			kind := "con.conversation.get.v1"
			attrs := `{"tier":"gold"}`
			if module == "CON" {
				seedAutomationOwnerConversation(t, r)
			} else {
				kind = "cus.contact.get.v1"
				if err := r.DB.Create(&domain.Contact{ID: 1, AccountID: 91, Name: "Contact", CustomAttributes: attrs}).Error; err != nil {
					t.Fatal(err)
				}
			}
			query := foundation.QueryEnvelope{QueryType: kind, AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "automation-bridge", AuthSource: "module"}, Filter: map[string]any{"id": 1}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
			get := r.Contracts.queries[kind]
			value, err := get(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(value)
			if strings.Contains(string(raw), "custom_attributes") {
				t.Fatalf("default view leaked attrs: %s", raw)
			}
			query.Fields = append(query.Fields, "custom_attributes")
			value, err = get(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]any
			raw, _ = json.Marshal(value)
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			if object["custom_attributes"] != attrs {
				t.Fatalf("explicit module attrs=%s", raw)
			}
			query.Actor = foundation.ContractActor{Type: "User", ID: "7", AuthSource: "session"}
			if _, err := get(context.Background(), query); err == nil {
				t.Fatal("non-module actor received custom attributes")
			}
		})
	}
}
