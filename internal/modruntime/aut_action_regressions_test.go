package modruntime

import (
	"context"
	"encoding/json"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAUTRegressionAutomationMessageDeletionBoundary(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "private"}[private], func(t *testing.T) {
			action := "send_message"
			if private {
				action = "private_note"
			}
			f := newAutomationActionFixture(t, false, `[{"action_name":"`+action+`","action_params":"late secret"}]`)
			e := f.call(f.startCommand(), 200)
			f.run()
			if p := f.execution(e.ID); p.RunnerNextAction != 1 {
				t.Fatalf("prepare=%+v", p)
			}
			stores := map[string]*gorm.DB{"MSG": f.owners["MSG"].DB}
			for _, owner := range []string{"RPT", "QLT", "RTM", "TKT", "API", "AIC", "OPS", "BIL"} {
				stores[owner] = newModuleTestDB(t, owner)
			}
			cleanup, err := repository.NewConversationCleanup(stores)
			if err != nil {
				t.Fatal(err)
			}
			conRepo := repository.NewConversationRepositoryWithPorts(f.con, nil, cleanup)
			dep := f.owners["MSG"].Config.Dependencies["CON"]
			original := dep.LocalHandler
			deleted := false
			dep.LocalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				record := httptest.NewRecorder()
				original.ServeHTTP(record, r)
				if strings.Contains(r.URL.Path, "message_authorization") && record.Code == 200 && !deleted {
					if err := conRepo.Delete(91, f.conversation.ID); err != nil {
						t.Fatalf("full owner delete: %v", err)
					}
					deleted = true
				}
				for k, v := range record.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(record.Code)
				w.Write(record.Body.Bytes())
			})
			f.owners["MSG"].Config.Dependencies["CON"] = dep
			f.run()
			var conversations, messages int64
			f.con.Model(&domain.Conversation{}).Count(&conversations)
			f.owners["MSG"].DB.Model(&domain.Message{}).Count(&messages)
			var events []domain.LocalChangeJournal
			f.owners["MSG"].DB.Where("action = ?", "event_outbox").Find(&events)
			final := f.execution(e.ID)
			var results []automationActionResult
			json.Unmarshal([]byte(final.ActionResults), &results)
			t.Logf("deleted=%v conversations=%d messages=%d events=%d status=%s results=%+v", deleted, conversations, messages, len(events), final.Status, results)
			if !deleted || conversations != 0 {
				t.Fatal("probe did not delete target")
			}
			if messages != 0 {
				t.Fatalf("late message survives completed owner cleanup: %d", messages)
			}
		})
	}
}

func TestAUTRegressionLegacyActionDefinitionAliases(t *testing.T) {
	for _, raw := range []string{`[{"name":"assign_agent","params":{"user_id":7}}]`, `[{"action_name":"assign_agent","params":{"user_id":7}}]`, `{"actions":[{"action_name":"mute_conversation"}]}`, `{"action_name":"mute_conversation"}`} {
		plan, reason := compileAutomationActions(raw)
		if reason != "" {
			t.Errorf("valid legacy definition rejected: %s reason=%s plan=%+v", raw, reason, plan)
		}
	}
}

func TestAUTRegressionAutomationMessageMonolithFactSource(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"new message"}]`)
	e := f.call(f.startCommand(), 200)
	f.drain(e.ID)
	conRepo := repository.NewConversationRepositoryWithPorts(f.con, nil, nil)
	msgRepo := repository.NewMessageRepositoryWithConversationPort(f.owners["MSG"].DB, conRepo)
	msgRepo.SetAutomationCapture(true)
	facts, err := msgRepo.PendingAutomationFacts(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var events int64
	f.owners["MSG"].DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events)
	t.Logf("committed MSG outbox events=%d available monolith facts=%d", events, len(facts))
	if len(facts) != 1 || facts[0].Payload["origin_execution_id"] != float64(e.ID) {
		t.Fatalf("durable action message fact missing immutable ancestry: %+v", facts)
	}
}

func TestAUTRegressionAutomationMessageEventDoesNotAdvanceCONTwice(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"new message"},{"action_name":"change_priority","action_params":"urgent"}]`)
	e := f.call(f.startCommand(), 200)
	f.run()
	f.run()
	before := f.conversationState()
	var row domain.LocalChangeJournal
	if err := f.owners["MSG"].DB.Where("action = ?", "event_outbox").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var event foundation.EventEnvelope
	if err := json.Unmarshal([]byte(row.AfterState), &event); err != nil {
		t.Fatal(err)
	}
	con := f.owners["CON"]
	response := performContractRequest(con.Handler(), "/contracts/events/"+event.EventType, event, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+con.Config.AuthToken)
		r.Header.Set(headerCaller, "MSG")
		r.Header.Set(headerCorrelationID, event.CorrelationID)
	})
	if response.Code != 200 {
		t.Fatalf("event apply %d %s", response.Code, response.Body.String())
	}
	after := f.conversationState()
	final := f.drain(e.ID)
	t.Logf("CON before event=%d after event=%d execution=%s results=%s", before.ObjectVersion, after.ObjectVersion, final.Status, final.ActionResults)
	if before.ObjectVersion != after.ObjectVersion || final.Status != "succeeded" {
		t.Fatal("prepared message activity was applied a second time and invalidated next action")
	}
}

func TestAUTRegressionAutomationIDPrecision(t *testing.T) {
	for _, raw := range []string{`[{"action_name":"assign_agent","action_params":[9007199254740993]}]`, `[{"action_name":"remove_label","action_params":[9007199254740993]}]`} {
		actions, reason := compileAutomationActions(raw)
		if reason == "" && actions[0].Value != "9007199254740993" {
			t.Errorf("silently changed valid integer ID: %s => %+v", raw, actions)
		}
	}
}
func TestAUTRegressionPreciseIdentityAcrossOwnerContracts(t *testing.T) {
	const large uint = 9007199254740993
	f := newAutomationActionFixture(t, false, `[{"action_name":"assign_agent","action_params":[9007199254740993]},{"action_name":"remove_label","action_params":[9007199254740993]}]`)
	if err := f.owners["TEN"].DB.Create(&domain.AccountUser{AccountID: 91, UserID: large}).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint{large - 1, large} {
		if err := f.owners["CUS"].DB.Create(&domain.Label{ID: id, AccountID: 91, Title: strconv.FormatUint(uint64(id), 10)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.con.Create(&domain.ConversationLabel{ConversationID: f.conversation.ID, LabelID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	e := f.call(f.startCommand(), 200)
	final := f.drain(e.ID)
	if final.Status != "succeeded" {
		t.Fatalf("final=%+v", final)
	}
	state := f.conversationState()
	if state.AssigneeID == nil || *state.AssigneeID != large {
		t.Fatalf("assigned wrong identity: %+v", state.AssigneeID)
	}
	var links []domain.ConversationLabel
	f.con.Find(&links)
	if len(links) != 1 || links[0].LabelID != large-1 {
		t.Fatalf("removed wrong label: %+v", links)
	}
}
func TestAUTRegressionFrozenWrapperPlansExecute(t *testing.T) {
	for _, raw := range []string{`[{"name":"assign_agent","params":{"user_id":7}}]`, `{"actions":[{"action_name":"mute_conversation"}]}`, `{"action_name":"mute_conversation"}`} {
		f := newAutomationActionFixture(t, false, raw)
		e := f.call(f.startCommand(), 200)
		if result := f.drain(e.ID); result.Status != "succeeded" {
			t.Fatalf("wrapper %s => %+v", raw, result)
		}
	}
}
func TestAUTRegressionModuleEnvelopeRejectsRoundedIDs(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[]`)
	call := func(key string, actions any, want int) {
		command := f.startCommand()
		command.CommandID = key
		command.IdempotencyKey = key
		command.CommandType = "aut.rule.create.v1"
		command.Target = foundation.ContractTarget{Type: "account", ID: "91"}
		command.Input = map[string]any{"name": "precision", "event_name": "conversation_created", "conditions": []any{}, "actions": actions}
		response := performContractRequest(f.aut.Handler(), "/contracts/commands/"+command.CommandType, command, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+f.aut.Config.AuthToken)
			r.Header.Set(headerCaller, "API")
			r.Header.Set(headerCorrelationID, command.CorrelationID)
		})
		if response.Code != want {
			t.Fatalf("module precision=%d %s", response.Code, response.Body.String())
		}
	}
	call("unsafe-number", []any{map[string]any{"action_name": "assign_agent", "action_params": []any{float64(9007199254740993)}}}, 400)
	call("exact-string", `[{"action_name":"assign_agent","action_params":[9007199254740993]}]`, 200)
	var version domain.AutomationRuleVersion
	f.aut.DB.Order("id DESC").First(&version)
	if !strings.Contains(version.Actions, "9007199254740993") {
		t.Fatalf("string identity lost: %s", version.Actions)
	}
}
