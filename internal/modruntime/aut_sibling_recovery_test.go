package modruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const siblingRecoveryResolved = `[{"attribute_key":"status","filter_operator":"equal_to","values":["resolved"]}]`
const siblingRecoveryOpen = `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`
const siblingRecoveryUrgent = `[{"action_name":"change_priority","action_params":["urgent"]}]`

func siblingRecoveryRule(t *testing.T, f *autRunnerFixture, conditions, actions string) domain.AutomationRule {
	t.Helper()
	rule := domain.AutomationRule{AccountID: 91, Name: "Deferred recovery", EventName: f.rule.EventName, Conditions: conditions, Actions: actions, Active: true}
	require.NoError(t, service.NewAutomationService(f.aut.DB, nil, nil).CreateRule(&rule))
	return rule
}

func siblingRecoveryAdmit(t *testing.T, f *autRunnerFixture) []domain.AutomationRuleExecution {
	t.Helper()
	event := bridgeFact(f, "conversation_created")
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	rows := causalExecutions(t, f, event.EventID)
	require.GreaterOrEqual(t, len(rows), 2)
	for _, row := range rows[1:] {
		require.Equal(t, "evaluating", row.Status, "execution=%+v", row)
		siblingRecoveryDecision(t, row, "pending")
	}
	return rows
}

func siblingRecoveryDecision(t *testing.T, e domain.AutomationRuleExecution, decision string) automationSiblingEvaluation {
	t.Helper()
	var checkpoint automationSiblingEvaluation
	require.NoError(t, json.Unmarshal([]byte(e.RunnerEvaluation), &checkpoint))
	require.Equal(t, "aut.sibling_conditions.v1", checkpoint.Codec)
	require.Equal(t, decision, checkpoint.Decision, "execution=%+v", e)
	if decision == "pending" {
		require.Nil(t, checkpoint.EvaluatedAt)
		require.Nil(t, checkpoint.Observation)
		require.Zero(t, checkpoint.Version)
		require.Empty(t, e.RunnerContext)
	} else {
		require.NotNil(t, checkpoint.EvaluatedAt)
		require.False(t, checkpoint.EvaluatedAt.IsZero())
		require.GreaterOrEqual(t, checkpoint.Version, e.TargetVersion)
	}
	return checkpoint
}

func siblingRecoveryRetryNow(t *testing.T, f *autRunnerFixture, id uint) {
	t.Helper()
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", id).Update("runner_retry_at", time.Now().UTC().Add(-time.Second)).Error)
}

func siblingRecoveryIdle(t *testing.T, f *autRunnerFixture) {
	t.Helper()
	ran, err := f.aut.RunAutomationOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
}

func siblingRecoveryReceipts(t *testing.T, f *autRunnerFixture, want int64) {
	t.Helper()
	var count int64
	require.NoError(t, f.con.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&count).Error)
	require.Equal(t, want, count, "owner commands must not be duplicated or sent before the condition decision")
}

// Each hook is installed only after admission, so it intercepts evaluation reads
// without changing the immutable initial observation or a real owner command.
func siblingRecoveryHook(f *automationActionFixture, owner string, hook func(http.ResponseWriter, *http.Request, http.Handler)) {
	dep := f.aut.Config.Dependencies[owner]
	original := dep.LocalHandler
	dep.LocalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hook(w, r, original) })
	f.aut.Config.Dependencies[owner] = dep
}

func siblingRecoveryWaitRead(t *testing.T, read <-chan struct{}) {
	t.Helper()
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not reach the read-only sibling observation")
	}
}

func TestAUTSiblingRecoveryWaitsForUnknownPredecessor(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryResolved, siblingRecoveryUrgent)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	f.lost["CON"].Store(true)
	f.run()
	require.Equal(t, "waiting", f.execution(rows[0].ID).Status)
	f.assertTarget("resolved", "medium", 2)
	before := f.execution(rows[1].ID).RunnerEvaluation
	var queries atomic.Int32
	siblingRecoveryHook(f, "CON", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.Contains(r.URL.Path, "/queries/") {
			queries.Add(1)
		}
		next.ServeHTTP(w, r)
	})
	siblingRecoveryIdle(t, f.autRunnerFixture)
	require.Zero(t, queries.Load(), "unknown owner effects must not authorize condition reads")
	require.Equal(t, before, f.execution(rows[1].ID).RunnerEvaluation)
	siblingRecoveryRetryNow(t, f.autRunnerFixture, rows[0].ID)
	f.run() // Recover the predecessor's existing receipt, rather than redispatch it.
	require.Equal(t, "succeeded", f.execution(rows[0].ID).Status)
	f.run() // Commit a condition decision in its own pass, before any owner action.
	decision := siblingRecoveryDecision(t, f.execution(rows[1].ID), "matched")
	require.Equal(t, uint(2), decision.Version)
	siblingRecoveryReceipts(t, f.autRunnerFixture, 1)
	f.run()
	f.assertTarget("resolved", "urgent", 3)
	siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
}

func TestAUTSiblingRecoveryCancellationBeforeAndDuringEvaluation(t *testing.T) {
	for _, inflight := range []bool{false, true} {
		t.Run(strconv.FormatBool(inflight), func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryResolved, siblingRecoveryUrgent)
			siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryResolved, `[{"action_name":"change_priority","action_params":["high"]}]`)
			rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
			var release chan struct{}
			var done chan error
			if inflight {
				f.run()
				read := make(chan struct{})
				release, done = make(chan struct{}), make(chan error, 1)
				var once sync.Once
				siblingRecoveryHook(f, "CON", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
					if strings.Contains(r.URL.Path, "/queries/") {
						once.Do(func() { close(read); <-release })
					}
					next.ServeHTTP(w, r)
				})
				go func() { _, err := f.aut.RunAutomationOnce(context.Background()); done <- err }()
				siblingRecoveryWaitRead(t, read)
				require.NotEmpty(t, f.execution(rows[1].ID).RunnerLease)
			}
			cancelled := f.cancel(rows[1].ID)
			require.Equal(t, "cancelled", cancelled.Status)
			if inflight {
				close(release)
				require.NoError(t, <-done)
			} else {
				f.run()
			}
			stored := f.execution(rows[1].ID)
			require.Equal(t, "cancelled", stored.Status)
			siblingRecoveryDecision(t, stored, "pending")
			require.Zero(t, stored.RunnerNextAction)
			f.run() // A cancelled sibling carries the confirmed predecessor version.
			f.run()
			f.assertTarget("resolved", "high", 3)
			require.Equal(t, "succeeded", f.execution(rows[2].ID).Status)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTSiblingRecoveryDependencyReadFailureRetriesWithoutNoMatch(t *testing.T) {
	for _, owner := range []string{"CON", "CUS"} {
		t.Run(owner, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[]`)
			label := domain.Label{AccountID: 91, Title: "vip"}
			require.NoError(t, f.owners["CUS"].DB.Create(&label).Error)
			require.NoError(t, f.con.Create(&domain.ConversationLabel{ConversationID: f.conversation.ID, LabelID: label.ID}).Error)
			siblingRecoveryRule(t, f.autRunnerFixture, `[{"key":"labels","operator":"is","value":"vip"}]`, siblingRecoveryUrgent)
			rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
			var unavailable atomic.Bool
			unavailable.Store(true)
			siblingRecoveryHook(f, owner, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if unavailable.Load() && strings.Contains(r.URL.Path, "/queries/") {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				next.ServeHTTP(w, r)
			})
			before := rows[1].RunnerEvaluation
			f.run()
			stored := f.execution(rows[1].ID)
			require.Equal(t, "evaluating", stored.Status)
			require.Equal(t, before, stored.RunnerEvaluation)
			require.NotNil(t, stored.RunnerRetryAt)
			require.Nil(t, stored.CompletedAt)
			require.Empty(t, stored.RunnerLease)
			siblingRecoveryDecision(t, stored, "pending")
			siblingRecoveryReceipts(t, f.autRunnerFixture, 0)
			siblingRecoveryIdle(t, f.autRunnerFixture)
			unavailable.Store(false)
			siblingRecoveryRetryNow(t, f.autRunnerFixture, stored.ID)
			restarted := f.aut
			ran, err := restarted.RunAutomationOnce(context.Background())
			require.NoError(t, err)
			require.True(t, ran)
			siblingRecoveryDecision(t, f.execution(stored.ID), "matched")
			f.run()
			f.assertTarget("open", "urgent", 2)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 1)
		})
	}
}

func TestAUTSiblingRecoveryLostEvaluationLeaseCommitsOnlyOnce(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[]`)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryOpen, siblingRecoveryUrgent)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	read, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var reads atomic.Int32
	siblingRecoveryHook(f, "CON", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.Contains(r.URL.Path, "/queries/") && reads.Add(1) == 1 {
			// The first worker observes successfully, then loses its lease before commit.
			recorder := httptest.NewRecorder()
			next.ServeHTTP(recorder, r)
			close(read)
			<-release
			for key, values := range recorder.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(recorder.Body.Bytes())
			return
		}
		next.ServeHTTP(w, r)
	})
	go func() { _, err := f.aut.RunAutomationOnce(context.Background()); done <- err }()
	siblingRecoveryWaitRead(t, read)
	claimed := f.execution(rows[1].ID)
	require.NotEmpty(t, claimed.RunnerLease)
	siblingRecoveryDecision(t, claimed, "pending")
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", claimed.ID).Update("runner_lease_until", time.Now().UTC().Add(-time.Minute)).Error)
	restarted := f.aut
	ran, err := restarted.RunAutomationOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ran)
	committed := f.execution(claimed.ID)
	siblingRecoveryDecision(t, committed, "matched")
	siblingRecoveryReceipts(t, f.autRunnerFixture, 0)
	close(release)
	require.NoError(t, <-done)
	require.Equal(t, committed.RunnerEvaluation, f.execution(claimed.ID).RunnerEvaluation, "the stale worker must not commit another decision")
	require.Equal(t, int32(2), reads.Load())
	f.run()
	f.assertTarget("open", "urgent", 2)
	siblingRecoveryReceipts(t, f.autRunnerFixture, 1)
	require.Equal(t, committed.RunnerEvaluation, f.execution(claimed.ID).RunnerEvaluation)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTSiblingRecoveryManualOwnerInterleaveIsStale(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	siblingRecoveryRule(t, f, siblingRecoveryResolved, siblingRecoveryUrgent)
	rows := siblingRecoveryAdmit(t, f)
	f.run()
	// A normal repository writer creates an unrelated CON version after the
	// predecessor's confirmed effect. Evaluation cannot rebase onto it.
	require.NoError(t, repository.NewConversationRepository(f.con).UpdateStatus(91, f.conversation.ID, "pending", nil))
	f.run()
	stored := f.execution(rows[1].ID)
	require.Equal(t, "failed", stored.Status)
	require.Equal(t, "target_version_stale", stored.Error)
	decision := siblingRecoveryDecision(t, stored, "failed")
	require.Equal(t, uint(2), decision.Version)
	require.Nil(t, decision.Observation)
	f.assertTarget("pending", "medium", 3)
	siblingRecoveryReceipts(t, f, 1)
	siblingRecoveryIdle(t, f)
}

func TestAUTSiblingRecoveryNoOpAndNoMatchCarryConfirmedVersion(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	siblingRecoveryRule(t, f, siblingRecoveryResolved, `[]`)
	siblingRecoveryRule(t, f, siblingRecoveryOpen, siblingRecoveryUrgent)
	siblingRecoveryRule(t, f, siblingRecoveryResolved, siblingRecoveryUrgent)
	rows := siblingRecoveryAdmit(t, f)
	f.run()
	f.run()
	noop := f.execution(rows[1].ID)
	require.Equal(t, "succeeded", noop.Status)
	require.Equal(t, "[]", noop.ActionResults)
	require.Equal(t, uint(2), siblingRecoveryDecision(t, noop, "matched").Version)
	f.run()
	miss := f.execution(rows[2].ID)
	require.Equal(t, "no_match", miss.Status)
	require.Equal(t, "conditions_not_matched", miss.Error)
	require.Equal(t, uint(2), siblingRecoveryDecision(t, miss, "no_match").Version)
	siblingRecoveryReceipts(t, f, 1)
	f.run()
	require.Equal(t, uint(2), siblingRecoveryDecision(t, f.execution(rows[3].ID), "matched").Version)
	f.run()
	f.assertTarget("resolved", "urgent", 3)
	siblingRecoveryReceipts(t, f, 2)
}

func TestAUTSiblingRecoveryRejectsWrongOwnerObservation(t *testing.T) {
	for _, owner := range []string{"CON", "CUS"} {
		for _, field := range []string{"account_id", "id"} {
			t.Run(owner+"/"+field, func(t *testing.T) {
				f := newAutomationActionFixture(t, false, `[]`)
				label := domain.Label{AccountID: 91, Title: "vip"}
				require.NoError(t, f.owners["CUS"].DB.Create(&label).Error)
				require.NoError(t, f.con.Create(&domain.ConversationLabel{ConversationID: f.conversation.ID, LabelID: label.ID}).Error)
				siblingRecoveryRule(t, f.autRunnerFixture, `[{"key":"labels","operator":"is","value":"vip"}]`, siblingRecoveryUrgent)
				rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
				siblingRecoveryHook(f, owner, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
					recorder := httptest.NewRecorder()
					next.ServeHTTP(recorder, r)
					if strings.Contains(r.URL.Path, "/queries/") && recorder.Code == http.StatusOK {
						var body map[string]any
						if json.Unmarshal(recorder.Body.Bytes(), &body) == nil {
							if data, ok := body["data"].(map[string]any); ok {
								data[field] = float64(999999)
							}
							w.Header().Set("Content-Type", "application/json")
							_ = json.NewEncoder(w).Encode(body)
							return
						}
					}
					w.WriteHeader(recorder.Code)
					_, _ = w.Write(recorder.Body.Bytes())
				})
				f.run()
				stored := f.execution(rows[1].ID)
				if owner == "CON" {
					require.Equal(t, "failed", stored.Status)
					require.Equal(t, "target_unavailable", stored.Error)
					siblingRecoveryDecision(t, stored, "failed")
				} else {
					require.Equal(t, "evaluating", stored.Status)
					require.NotNil(t, stored.RunnerRetryAt)
					siblingRecoveryDecision(t, stored, "pending")
				}
				require.NotEqual(t, "no_match", stored.Status)
				f.assertTarget("open", "medium", 1)
				siblingRecoveryReceipts(t, f.autRunnerFixture, 0)
			})
		}
	}
}

func TestAUTSiblingRecoverySnapshotPreservesDecisionAndFrozenDefinition(t *testing.T) {
	for _, state := range []string{"pending", "lost_lease", "matched", "no_match"} {
		t.Run(state, func(t *testing.T) {
			f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			conditions := siblingRecoveryResolved
			if state == "no_match" {
				conditions = siblingRecoveryOpen
			}
			rule := siblingRecoveryRule(t, f, conditions, siblingRecoveryUrgent)
			rows := siblingRecoveryAdmit(t, f)
			f.run()
			if state == "matched" || state == "no_match" {
				f.run()
			}
			if state == "lost_lease" {
				require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[1].ID).Updates(map[string]any{"runner_lease": "dead-evaluation-worker", "runner_lease_until": time.Now().UTC().Add(-time.Minute)}).Error)
			}
			// Editing the rule publishes a new immutable version. The admitted
			// sibling must keep both its original conditions and its action plan.
			require.NoError(t, service.NewAutomationService(f.aut.DB, nil, nil).UpdateRule(&rule, map[string]any{"conditions": siblingRecoveryOpen, "actions": `[{"action_name":"change_priority","action_params":["low"]}]`}))
			before := f.execution(rows[1].ID)
			restored := restoreAUTOwnerFixture(t, f, false)
			assertAUTSnapshotEqual(t, before, restored.execution(before.ID))
			require.Equal(t, before.RunnerEvaluation, restored.execution(before.ID).RunnerEvaluation)
			if state == "pending" || state == "lost_lease" {
				restored.run()
			}
			if state == "no_match" {
				siblingRecoveryDecision(t, restored.execution(before.ID), "no_match")
				siblingRecoveryIdle(t, restored)
				restored.assertTarget("resolved", "medium", 2)
				siblingRecoveryReceipts(t, restored, 1)
			} else {
				decision := restored.execution(before.ID).RunnerEvaluation
				siblingRecoveryDecision(t, restored.execution(before.ID), "matched")
				restored.run()
				restored.assertTarget("resolved", "urgent", 3)
				require.Equal(t, decision, restored.execution(before.ID).RunnerEvaluation)
				siblingRecoveryReceipts(t, restored, 2)
			}
			require.Equal(t, uint(1), restored.execution(before.ID).RuleVersion)
			assertAUTSnapshotEqual(t, before, f.execution(before.ID))
		})
	}
}

func TestAUTSiblingRecoveryCachesInitialValuesButRefreshesLabels(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"add_label","action_params":["vip"]}]`)
	for _, model := range []any{&domain.AutomationRule{}, &domain.AutomationRuleVersion{}} {
		require.NoError(t, f.aut.DB.Model(model).Where("account_id = ?", 91).Update("event_name", "message_created").Error)
	}
	f.rule.EventName = "message_created"
	company := domain.Company{ID: 51, AccountID: 91, Name: "Original Company"}
	contact := domain.Contact{ID: f.conversation.ContactID, AccountID: 91, Email: "original@example.test", CompanyID: &company.ID, CustomAttributes: `{"city":"Original City"}`}
	message := domain.Message{AccountID: 91, ConversationID: f.conversation.ID, Content: "original message", MessageType: "incoming", Status: "delivered"}
	require.NoError(t, f.owners["CUS"].DB.Create(&company).Error)
	require.NoError(t, f.owners["CUS"].DB.Create(&contact).Error)
	require.NoError(t, f.owners["MSG"].DB.Create(&message).Error)
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", `{"browser_language":"original-language"}`).Error)
	conditions := `[
		{"key":"email","operator":"is","value":"original@example.test"},
		{"key":"company_name","operator":"is","value":"Original Company"},
		{"key":"city","operator":"is","value":"Original City"},
		{"key":"content","operator":"is","value":"original message"},
		{"key":"browser_language","operator":"is","value":"original-language"},
		{"key":"labels","operator":"is","value":"vip"}
	]`
	siblingRecoveryRule(t, f.autRunnerFixture, conditions, siblingRecoveryUrgent)
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	event.SourceModule, event.EventType, event.AggregateType = "MSG", "msg.message.created.v1", "Message"
	event.AggregateID = strconv.Itoa(int(message.ID))
	event.Payload["message_id"] = message.ID
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	require.Len(t, rows, 2)
	initial := siblingRecoveryDecision(t, rows[1], "pending")
	require.Empty(t, initial.Initial.Labels)
	// CUS and MSG may change independently while this sibling is queued. The
	// legacy shared observation keeps those values and conversation attributes.
	require.NoError(t, f.owners["CUS"].DB.Model(&contact).Updates(map[string]any{"email": "changed@example.test", "custom_attributes": `{"city":"Changed City"}`}).Error)
	require.NoError(t, f.owners["CUS"].DB.Model(&company).Update("name", "Changed Company").Error)
	require.NoError(t, f.owners["MSG"].DB.Model(&message).Update("content", "changed message").Error)
	require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", `{"browser_language":"changed-language"}`).Error)
	f.run() // Resolve the label in CUS.
	f.run() // Apply the label to CON and confirm version 2.
	require.Equal(t, "succeeded", f.execution(rows[0].ID).Status)
	var customerReads, messageReads atomic.Int32
	siblingRecoveryHook(f, "CUS", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.Contains(r.URL.Path, "/queries/") {
			customerReads.Add(1)
		}
		next.ServeHTTP(w, r)
	})
	siblingRecoveryHook(f, "MSG", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.Contains(r.URL.Path, "/queries/") {
			messageReads.Add(1)
		}
		next.ServeHTTP(w, r)
	})
	f.run()
	decision := siblingRecoveryDecision(t, f.execution(rows[1].ID), "matched")
	require.NotNil(t, decision.Observation)
	require.Equal(t, initial.Initial.Contact, decision.Observation.Contact)
	require.Equal(t, initial.Initial.Company, decision.Observation.Company)
	require.Equal(t, initial.Initial.Message, decision.Observation.Message)
	require.Equal(t, initial.Initial.Conversation.CustomAttributes, decision.Observation.Conversation.CustomAttributes)
	require.Len(t, decision.Observation.Labels, 1)
	require.Equal(t, "vip", decision.Observation.Labels[0].Title)
	require.Equal(t, int32(1), customerReads.Load(), "only the new label is refreshed")
	require.Zero(t, messageReads.Load())
	f.run()
	f.assertTarget("open", "urgent", 3)
}

// Uses isolated native PostgreSQL schemas and is intentionally gated by DB_DSN.
// It exercises independent runner values contending for one evaluation lease.
func TestPostgresAUTSiblingRecoveryConcurrentWorkersCommitOneDecision(t *testing.T) {
	f := newAutomationActionFixture(t, true, `[{"action_name":"resolve_conversation"}]`)
	siblingRecoveryRule(t, f.autRunnerFixture, siblingRecoveryResolved, siblingRecoveryUrgent)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	f.run()
	var reads atomic.Int32
	siblingRecoveryHook(f, "CON", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.Contains(r.URL.Path, "/queries/") {
			reads.Add(1)
			time.Sleep(20 * time.Millisecond)
		}
		next.ServeHTTP(w, r)
	})
	for round := 0; round < 2; round++ {
		start, done := make(chan struct{}), make(chan error, 8)
		for worker := 0; worker < 8; worker++ {
			replica := f.aut
			go func() { <-start; _, err := replica.RunAutomationOnce(context.Background()); done <- err }()
		}
		close(start)
		for worker := 0; worker < 8; worker++ {
			require.NoError(t, <-done)
		}
	}
	stored := f.execution(rows[1].ID)
	require.Equal(t, "succeeded", stored.Status)
	siblingRecoveryDecision(t, stored, "matched")
	require.Equal(t, int32(1), reads.Load(), "one lease owns the read and decision")
	f.assertTarget("resolved", "urgent", 3)
	siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTSiblingRecoveryOldV1SnapshotKeepsAdmissionDecisions(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	siblingRecoveryRule(t, f, siblingRecoveryOpen, siblingRecoveryUrgent)
	siblingRecoveryRule(t, f, siblingRecoveryResolved, `[{"action_name":"change_priority","action_params":["low"]}]`)
	rows := siblingRecoveryAdmit(t, f)
	// Rebuild the first queued rule as its historical stop-on-failure plan too.
	var firstContext automationRunnerContext
	require.NoError(t, json.Unmarshal([]byte(rows[0].RunnerContext), &firstContext))
	firstPlan, firstReason := compileAutomationActions(f.rule.Actions)
	require.Empty(t, firstReason)
	require.NoError(t, f.aut.queueAutomationExecution(context.Background(), firstContext.command(rows[0]), &rows[0], firstPlan))
	// Construct the pre-upgrade durable rows: the second rule already matched
	// the admission-time open status; the third already missed resolved. This
	// is deliberately an old frozen decision, not a new deferred evaluation.
	matched := rows[1]
	checkpoint := siblingRecoveryDecision(t, matched, "pending")
	actions, reason := compileAutomationActions(checkpoint.Actions)
	require.Empty(t, reason)
	require.NoError(t, f.aut.queueAutomationExecution(context.Background(), checkpoint.Metadata.command(matched), &matched, actions))
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", matched.ID).Updates(map[string]any{"runner_evaluation": "", "status": "received"}).Error)
	miss := rows[2]
	missCheckpoint := siblingRecoveryDecision(t, miss, "pending")
	miss.ActionResults = `[{"action_index":0,"status":"skipped","reason_code":"conditions_not_matched"}]`
	require.NoError(t, f.aut.finishAutomationExecution(context.Background(), missCheckpoint.Metadata.command(miss), &miss, "no_match", "conditions_not_matched"))
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", miss.ID).Update("runner_evaluation", "").Error)
	manifest, err := json.Marshal(automationFactBatchManifest{Codec: "aut.fact_batch.v1", ExecutionIDs: []uint{rows[0].ID, rows[1].ID, rows[2].ID}})
	require.NoError(t, err)
	require.NoError(t, f.aut.DB.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", rows[0].FactBatchKey).Updates(map[string]any{"schema_version": "aut.fact.v2", "diff": string(manifest)}).Error)
	path := filepath.Join(t.TempDir(), "old-v1.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "old-v1", path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var file snapshotFile
	require.NoError(t, json.Unmarshal(raw, &file))
	var table map[string]any
	require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
	table["codec"] = "aut.execution.v1"
	for _, row := range table["rows"].([]any) {
		delete(row.(map[string]any)["private"].(map[string]any), "runner_evaluation")
	}
	file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
	require.NoError(t, err)
	snapshot, err := json.Marshal(file.Snapshot)
	require.NoError(t, err)
	file.Checksum = sha256Bytes(snapshot)
	raw, err = json.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	target := newModuleTestDB(t, "AUT")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "old-v1", path, nil))
	restored := &autRunnerFixture{t: t, aut: f.aut, con: f.con, rule: f.rule, conversation: f.conversation}
	restored.aut.DB = target
	for _, original := range rows {
		require.Empty(t, restored.execution(original.ID).RunnerEvaluation, "old v1 must never invent a deferred decision")
	}
	require.Equal(t, "received", restored.execution(matched.ID).Status)
	require.Equal(t, "no_match", restored.execution(miss.ID).Status)
	restored.run()
	restored.assertTarget("resolved", "medium", 2)
	restored.run() // Executes the old match despite status now being resolved.
	restored.assertTarget("resolved", "urgent", 3)
	require.Equal(t, "succeeded", restored.execution(matched.ID).Status)
	require.Equal(t, "no_match", restored.execution(miss.ID).Status)
	siblingRecoveryReceipts(t, restored, 2)
	siblingRecoveryIdle(t, restored)
}

func TestAUTSiblingRecoveryCorruptSnapshotRollsBackAtomically(t *testing.T) {
	type corruption struct {
		state string
		edit  func(*automationExecutionSnapshotTable, *automationSiblingEvaluation, *[]domain.LocalChangeJournal)
	}
	cases := map[string]corruption{
		"pending_claims_decision": {"pending", func(_ *automationExecutionSnapshotTable, c *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			c.Decision = "matched"
		}},
		"foreign_initial_owner": {"pending", func(_ *automationExecutionSnapshotTable, c *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			c.Initial.Conversation.AccountID++
		}},
		"changed_frozen_conditions": {"pending", func(_ *automationExecutionSnapshotTable, c *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			c.Conditions = siblingRecoveryOpen
		}},
		"changed_frozen_actions": {"pending", func(_ *automationExecutionSnapshotTable, c *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			c.Actions = `[]`
		}},
		"decision_wrong_version": {"matched", func(_ *automationExecutionSnapshotTable, c *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			c.Version++
		}},
		"changed_cached_attributes": {"matched", func(_ *automationExecutionSnapshotTable, c *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			c.Observation.Conversation.CustomAttributes = `{"changed":true}`
		}},
		"changed_predecessor_projection": {"matched", func(_ *automationExecutionSnapshotTable, c *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			c.Observation.Conversation.Priority = "high"
		}},
		"v1_contains_new_decision": {"matched", func(table *automationExecutionSnapshotTable, _ *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			table.Codec = "aut.execution.v1"
		}},
		"dropped_matched_decision": {"matched", func(table *automationExecutionSnapshotTable, _ *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			table.Rows[1].Private.RunnerEvaluation = ""
		}},
		"dropped_no_match_decision": {"no_match", func(table *automationExecutionSnapshotTable, _ *automationSiblingEvaluation, _ *[]domain.LocalChangeJournal) {
			table.Rows[1].Private.RunnerEvaluation = ""
		}},
		"manifest_drops_evaluation": {"matched", func(_ *automationExecutionSnapshotTable, _ *automationSiblingEvaluation, journals *[]domain.LocalChangeJournal) {
			for i := range *journals {
				if (*journals)[i].Action != "automation_fact_received" {
					continue
				}
				var manifest automationFactBatchManifest
				_ = json.Unmarshal([]byte((*journals)[i].Diff), &manifest)
				manifest.EvaluationIDs = nil
				raw, _ := json.Marshal(manifest)
				(*journals)[i].Diff = string(raw)
			}
		}},
		"manifest_downgrade": {"matched", func(_ *automationExecutionSnapshotTable, _ *automationSiblingEvaluation, journals *[]domain.LocalChangeJournal) {
			for i := range *journals {
				if (*journals)[i].Action != "automation_fact_received" {
					continue
				}
				var manifest automationFactBatchManifest
				_ = json.Unmarshal([]byte((*journals)[i].Diff), &manifest)
				manifest.Codec = "aut.fact_batch.v1"
				raw, _ := json.Marshal(manifest)
				(*journals)[i].Diff = string(raw)
			}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			conditions := siblingRecoveryResolved
			if tc.state == "no_match" {
				conditions = siblingRecoveryOpen
			}
			siblingRecoveryRule(t, f, conditions, siblingRecoveryUrgent)
			rows := siblingRecoveryAdmit(t, f)
			f.run()
			if tc.state != "pending" {
				f.run()
			}
			path := filepath.Join(t.TempDir(), "corrupt.json")
			require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "corrupt", path))
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var file snapshotFile
			require.NoError(t, json.Unmarshal(raw, &file))
			var table automationExecutionSnapshotTable
			require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
			var checkpoint automationSiblingEvaluation
			require.NoError(t, json.Unmarshal([]byte(table.Rows[1].Private.RunnerEvaluation), &checkpoint))
			var journals []domain.LocalChangeJournal
			require.NoError(t, json.Unmarshal(file.Snapshot.Tables["local_change_journals"], &journals))
			original := table.Rows[1].Private.RunnerEvaluation
			tc.edit(&table, &checkpoint, &journals)
			if table.Rows[1].Private.RunnerEvaluation == original {
				encoded, err := json.Marshal(checkpoint)
				require.NoError(t, err)
				table.Rows[1].Private.RunnerEvaluation = string(encoded)
			}
			file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
			require.NoError(t, err)
			file.Snapshot.Tables["local_change_journals"], err = json.Marshal(journals)
			require.NoError(t, err)
			snapshot, err := json.Marshal(file.Snapshot)
			require.NoError(t, err)
			file.Checksum = sha256Bytes(snapshot) // Exercise semantic validation, not just the file checksum.
			raw, err = json.Marshal(file)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			target := newModuleTestDB(t, "AUT")
			sentinel := domain.AutomationRule{ID: 700, AccountID: 91, Name: "keep existing rules", EventName: "conversation_created", Conditions: "[]", Actions: "[]"}
			require.NoError(t, target.Create(&sentinel).Error)
			rebuilt := false
			err = RestoreSnapshot(context.Background(), target, "AUT", "corrupt", path, func(context.Context, *gorm.DB) error { rebuilt = true; return nil })
			require.Error(t, err)
			require.False(t, rebuilt)
			var preserved domain.AutomationRule
			require.NoError(t, target.First(&preserved, sentinel.ID).Error)
			require.Equal(t, sentinel.Name, preserved.Name)
			var executions, markers int64
			require.NoError(t, target.Model(&domain.AutomationRuleExecution{}).Where("id IN ?", []uint{rows[0].ID, rows[1].ID}).Count(&executions).Error)
			require.NoError(t, target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "recovery_complete").Count(&markers).Error)
			require.Zero(t, executions)
			require.Zero(t, markers)
		})
	}
}
