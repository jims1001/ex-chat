package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The adjacent integers deliberately straddle binary64's exact-integer boundary.
const numericRecoveryOriginal = `{"score":9007199254740993}`

func numericRecoveryDefinition(t *testing.T, f *automationActionFixture, model string) domain.CustomAttributeDefinition {
	t.Helper()
	d := domain.CustomAttributeDefinition{AccountID: 91, AttributeDisplayName: "Score", AttributeKey: "score", AttributeModel: model, AttributeDisplayType: "number"}
	require.NoError(t, f.owners["CUS"].DB.Create(&d).Error)
	if model == "contact_attribute" {
		require.NoError(t, f.owners["CUS"].DB.Create(&domain.Contact{ID: f.conversation.ContactID, AccountID: 91, CustomAttributes: numericRecoveryOriginal}).Error)
	} else {
		require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", numericRecoveryOriginal).Error)
	}
	return d
}

func numericRecoveryConditions(d domain.CustomAttributeDefinition, precedingStatus bool) string {
	leaf := fmt.Sprintf(`{"attribute_key":"score","filter_operator":"is_greater_than","values":[9007199254740992],"custom_attribute_type":%q,"custom_attribute_definition_id":%d}`, d.AttributeModel, d.ID)
	if precedingStatus {
		leaf = `{"attribute_key":"status","filter_operator":"equal_to","values":["resolved"]},` + leaf
	}
	return "[" + leaf + "]"
}

func numericRecoveryRule(t *testing.T, f *automationActionFixture, event, conditions, actions string) domain.AutomationRule {
	t.Helper()
	rule := domain.AutomationRule{AccountID: 91, Name: "Numeric recovery", EventName: event, ConditionSemantics: domain.AutomationNumericCustomV1, Conditions: conditions, Actions: actions, Active: true}
	svc := service.NewAutomationService(f.aut.DB, nil, nil)
	svc.SetDurableExecution(true)
	require.NoError(t, svc.CreateRule(&rule))
	return rule
}

func numericRecoveryAdmit(t *testing.T, f *automationActionFixture) []domain.AutomationRuleExecution {
	t.Helper()
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	require.GreaterOrEqual(t, len(rows), 2)
	for _, row := range rows[1:] {
		numericRecoveryDecision(t, row, "pending")
	}
	return rows
}

func numericRecoveryDecision(t *testing.T, e domain.AutomationRuleExecution, decision string) automationSiblingEvaluation {
	t.Helper()
	c, valid := decodeAutomationSiblingEvaluation(e)
	require.True(t, valid, "invalid numeric checkpoint: %+v", e)
	require.Equal(t, domain.AutomationNumericCustomV1, e.RunnerConditionSemantics)
	require.Equal(t, domain.AutomationNumericCustomV1, c.ConditionSemantics)
	require.Equal(t, "aut.sibling_conditions.v2", c.Codec)
	require.Equal(t, decision, c.Decision)
	require.Len(t, c.Initial.NumericCatalog, 1)
	return c
}

func TestAUTNumericRecoveryFreezesProfileCatalogAndOwnerValues(t *testing.T) {
	for _, model := range []string{"conversation_attribute", "contact_attribute"} {
		t.Run(model, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			d := numericRecoveryDefinition(t, f, model)
			rule := numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
			rows := numericRecoveryAdmit(t, f)
			initial := numericRecoveryDecision(t, rows[1], "pending")
			// Neither later live edits nor removal of the catalog entry can change
			// what the admitted rule version meant or which exact value it observed.
			svc := service.NewAutomationService(f.aut.DB, nil, nil)
			svc.SetDurableExecution(true)
			require.NoError(t, svc.UpdateRule(&rule, map[string]any{"condition_semantics": "", "conditions": siblingRecoveryOpen, "actions": `[{"action_name":"change_priority","action_params":["low"]}]`}))
			require.NoError(t, f.owners["CUS"].DB.Delete(&d).Error)
			require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", `{"score":"edited"}`).Error)
			if model == "contact_attribute" {
				require.NoError(t, f.owners["CUS"].DB.Model(&domain.Contact{}).Where("id = ?", f.conversation.ContactID).Update("custom_attributes", `{"score":-1}`).Error)
			}
			f.run()
			var customerReads atomic.Int32
			siblingRecoveryHook(f, "CUS", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				customerReads.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			})
			f.run()
			matched := numericRecoveryDecision(t, f.execution(rows[1].ID), "matched")
			require.Equal(t, initial.Initial.NumericCatalog, matched.Observation.NumericCatalog)
			require.Equal(t, initial.Initial.Contact, matched.Observation.Contact)
			require.Equal(t, initial.Initial.Conversation.CustomAttributes, matched.Observation.Conversation.CustomAttributes)
			require.Equal(t, uint(2), matched.Version)
			require.Zero(t, customerReads.Load(), "numeric owner values/catalog are frozen, not fetched again")
			f.run()
			f.assertTarget("resolved", "urgent", 3)
			require.Equal(t, uint(1), f.execution(rows[1].ID).RuleVersion)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTNumericRecoveryAdmissionOutagesAreAtomicAndRetryable(t *testing.T) {
	for _, owner := range []string{"CUS", "CON"} {
		t.Run(owner, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			d := numericRecoveryDefinition(t, f, "contact_attribute")
			numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
			var unavailable atomic.Bool
			unavailable.Store(true)
			siblingRecoveryHook(f, owner, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if unavailable.Load() && strings.Contains(r.URL.Path, "/queries/") {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				next.ServeHTTP(w, r)
			})
			event := bridgeFact(f.autRunnerFixture, "conversation_created")
			require.Error(t, f.aut.AcceptAutomationFact(context.Background(), event))
			var executions, admissions int64
			require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Count(&executions).Error)
			require.NoError(t, f.aut.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_fact_received").Count(&admissions).Error)
			require.Zero(t, executions)
			require.Zero(t, admissions)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 0)
			unavailable.Store(false)
			restarted := f.aut
			require.NoError(t, restarted.AcceptAutomationFact(context.Background(), event))
			require.NoError(t, restarted.AcceptAutomationFact(context.Background(), event))
			rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
			require.Len(t, rows, 2)
			f.run()
			f.run()
			numericRecoveryDecision(t, f.execution(rows[1].ID), "matched")
			f.run()
			f.assertTarget("resolved", "urgent", 3)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
		})
	}
}

func TestAUTNumericRecoveryDeferredReadOutagesRetryAfterRestart(t *testing.T) {
	for _, owner := range []string{"CON", "CUS"} {
		t.Run(owner, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			d := numericRecoveryDefinition(t, f, "conversation_attribute")
			conditions := numericRecoveryConditions(d, true)
			if owner == "CUS" {
				label := domain.Label{AccountID: 91, Title: "vip"}
				require.NoError(t, f.owners["CUS"].DB.Create(&label).Error)
				require.NoError(t, f.con.Create(&domain.ConversationLabel{ConversationID: f.conversation.ID, LabelID: label.ID}).Error)
				conditions = strings.TrimSuffix(conditions, "]") + `,{"key":"labels","operator":"is","value":"vip"}]`
			}
			numericRecoveryRule(t, f, f.rule.EventName, conditions, siblingRecoveryUrgent)
			rows := numericRecoveryAdmit(t, f)
			f.run()
			var unavailable atomic.Bool
			unavailable.Store(true)
			siblingRecoveryHook(f, owner, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if unavailable.Load() && strings.Contains(r.URL.Path, "/queries/") {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				next.ServeHTTP(w, r)
			})
			f.run()
			waiting := f.execution(rows[1].ID)
			require.Equal(t, "evaluating", waiting.Status)
			require.Equal(t, rows[1].RunnerEvaluation, waiting.RunnerEvaluation)
			require.NotNil(t, waiting.RunnerRetryAt)
			require.Nil(t, waiting.CompletedAt)
			require.Empty(t, waiting.RunnerLease)
			numericRecoveryDecision(t, waiting, "pending")
			siblingRecoveryIdle(t, f.autRunnerFixture)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 1)
			unavailable.Store(false)
			siblingRecoveryRetryNow(t, f.autRunnerFixture, waiting.ID)
			restarted := f.aut
			ran, err := restarted.RunAutomationOnce(context.Background())
			require.NoError(t, err)
			require.True(t, ran)
			numericRecoveryDecision(t, f.execution(waiting.ID), "matched")
			f.run()
			f.assertTarget("resolved", "urgent", 3)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
		})
	}
}

func TestAUTNumericRecoveryCancellationBeforeAndDuringEvaluation(t *testing.T) {
	for _, inflight := range []bool{false, true} {
		t.Run(fmt.Sprint(inflight), func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			d := numericRecoveryDefinition(t, f, "conversation_attribute")
			numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
			numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), `[{"action_name":"change_priority","action_params":["high"]}]`)
			rows := numericRecoveryAdmit(t, f)
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
			require.Equal(t, "cancelled", f.cancel(rows[1].ID).Status)
			if inflight {
				close(release)
				require.NoError(t, <-done)
			} else {
				f.run()
			}
			cancelled := f.execution(rows[1].ID)
			require.Equal(t, "cancelled", cancelled.Status)
			require.Equal(t, rows[1].RunnerEvaluation, cancelled.RunnerEvaluation)
			numericRecoveryDecision(t, cancelled, "pending")
			f.run()
			numericRecoveryDecision(t, f.execution(rows[2].ID), "matched")
			f.run()
			f.assertTarget("resolved", "high", 3)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTNumericRecoverySnapshotRestoresFrozenDecision(t *testing.T) {
	for _, state := range []string{"pending", "lost_lease", "matched", "no_match", "invalid_value"} {
		t.Run(state, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			d := numericRecoveryDefinition(t, f, "conversation_attribute")
			if state == "no_match" || state == "invalid_value" {
				value := `{"score":null}`
				if state == "invalid_value" {
					value = `{"score":"9007199254740993"}`
				}
				require.NoError(t, f.con.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", value).Error)
			}
			numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
			rows := numericRecoveryAdmit(t, f)
			f.run()
			if state == "matched" || state == "no_match" || state == "invalid_value" {
				f.run()
			}
			if state == "lost_lease" {
				require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("id = ?", rows[1].ID).Updates(map[string]any{"runner_lease": "dead-numeric-worker", "runner_lease_until": time.Now().UTC().Add(-time.Minute)}).Error)
			}
			before := f.execution(rows[1].ID)
			// Restore AUT and CON independently, with no CUS connection at all.
			restored := restoreAUTOwnerFixture(t, f.autRunnerFixture, false)
			assertAUTSnapshotEqual(t, before, restored.execution(before.ID))
			if state == "pending" || state == "lost_lease" {
				restored.run()
			}
			if state == "no_match" {
				numericRecoveryDecision(t, restored.execution(before.ID), "no_match")
				require.Equal(t, "conditions_not_matched", restored.execution(before.ID).Error)
				restored.assertTarget("resolved", "medium", 2)
			} else if state == "invalid_value" {
				numericRecoveryDecision(t, restored.execution(before.ID), "failed")
				require.Equal(t, "invalid_frozen_definition", restored.execution(before.ID).Error)
				restored.assertTarget("resolved", "medium", 2)
			} else {
				numericRecoveryDecision(t, restored.execution(before.ID), "matched")
				restored.run()
				restored.assertTarget("resolved", "urgent", 3)
				siblingRecoveryReceipts(t, restored, 2)
			}
			siblingRecoveryIdle(t, restored)
			assertAUTSnapshotEqual(t, before, f.execution(before.ID))
		})
	}
}

func TestAUTNumericRecoverySnapshotTamperingFailsAtomically(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	d := numericRecoveryDefinition(t, f, "conversation_attribute")
	numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
	rows := numericRecoveryAdmit(t, f)
	f.run()
	f.run()
	path := filepath.Join(t.TempDir(), "numeric-good.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "numeric-tamper", path))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	type mutation func(*automationExecutionSnapshotTable, *automationSiblingEvaluation)
	cases := map[string]mutation{
		"drop_execution_profile": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			s.Rows[1].Private.RunnerConditionSemantics = ""
		},
		"unknown_execution_profile": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			s.Rows[1].Private.RunnerConditionSemantics = "numeric_custom.v99"
		},
		"drop_evaluation_profile": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) { c.ConditionSemantics = "" },
		"downgrade_sibling_codec": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Codec = "aut.sibling_conditions.v1"
		},
		"drop_initial_catalog": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Initial.NumericCatalog = nil
		},
		"catalog_wrong_tenant": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Initial.NumericCatalog[0].Definition.AccountID++
		},
		"catalog_wrong_identity": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Initial.NumericCatalog[0].DefinitionID++
		},
		"catalog_duplicate_identity": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Initial.NumericCatalog = append(c.Initial.NumericCatalog, c.Initial.NumericCatalog[0])
		},
		"changed_observed_catalog": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Observation.NumericCatalog[0].Definition.AttributeKey = "different"
		},
		"changed_observed_value": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Observation.Conversation.CustomAttributes = `{"score":0}`
		},
		"changed_frozen_threshold": func(s *automationExecutionSnapshotTable, c *automationSiblingEvaluation) {
			c.Conditions = strings.ReplaceAll(c.Conditions, "9007199254740992", "0")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var file snapshotFile
			require.NoError(t, json.Unmarshal(original, &file))
			var table automationExecutionSnapshotTable
			require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
			require.Equal(t, "aut.execution.v5", table.Codec)
			var evaluation automationSiblingEvaluation
			require.NoError(t, json.Unmarshal([]byte(table.Rows[1].Private.RunnerEvaluation), &evaluation))
			mutate(&table, &evaluation)
			raw, err := json.Marshal(evaluation)
			require.NoError(t, err)
			table.Rows[1].Private.RunnerEvaluation = string(raw)
			file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
			require.NoError(t, err)
			raw, err = json.Marshal(file.Snapshot)
			require.NoError(t, err)
			file.Checksum = sha256Bytes(raw)
			raw, err = json.Marshal(file)
			require.NoError(t, err)
			corrupt := filepath.Join(t.TempDir(), "corrupt.json")
			require.NoError(t, os.WriteFile(corrupt, raw, 0600))
			target := newModuleTestDB(t, "AUT")
			sentinel := domain.AutomationRule{ID: 700, AccountID: 91, Name: "preserve destination", EventName: "conversation_created", Conditions: "[]", Actions: "[]"}
			require.NoError(t, target.Create(&sentinel).Error)
			rebuilt := false
			require.Error(t, RestoreSnapshot(context.Background(), target, "AUT", "numeric-tamper", corrupt, func(context.Context, *gorm.DB) error { rebuilt = true; return nil }))
			require.False(t, rebuilt)
			var preserved domain.AutomationRule
			require.NoError(t, target.First(&preserved, sentinel.ID).Error)
			require.Equal(t, sentinel.Name, preserved.Name)
			var count int64
			require.NoError(t, target.Model(&domain.AutomationRuleExecution{}).Where("id IN ?", []uint{rows[0].ID, rows[1].ID}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "recovery_complete").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestAUTNumericRecoveryOldCodecsRejectNumericMetadata(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	d := numericRecoveryDefinition(t, f, "conversation_attribute")
	numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
	rows := numericRecoveryAdmit(t, f)
	raw, err := marshalAutomationSnapshot(rows)
	require.NoError(t, err)
	for _, codec := range []string{"aut.execution.v1", "aut.execution.v2", "aut.execution.v3", "aut.execution.v4"} {
		for _, marker := range []string{"profile", "catalog", "sibling_codec"} {
			t.Run(codec+"/"+marker, func(t *testing.T) {
				var table automationExecutionSnapshotTable
				require.NoError(t, json.Unmarshal(raw, &table))
				table.Codec = codec
				// Remove unrelated newer policies so this really isolates numeric metadata.
				for i := range table.Rows {
					p := table.Rows[i].Private
					var runner automationRunnerContext
					if p.RunnerContext != "" {
						require.NoError(t, json.Unmarshal([]byte(p.RunnerContext), &runner))
						runner.ActivityPolicy, runner.FailurePolicy = "", ""
						encoded, err := json.Marshal(runner)
						require.NoError(t, err)
						p.RunnerContext = string(encoded)
					}
				}
				var c automationSiblingEvaluation
				p := table.Rows[1].Private
				require.NoError(t, json.Unmarshal([]byte(p.RunnerEvaluation), &c))
				c.Metadata.ActivityPolicy, c.Metadata.FailurePolicy = "", ""
				if marker != "profile" {
					p.RunnerConditionSemantics, c.ConditionSemantics = "", ""
				}
				if marker != "catalog" {
					c.Initial.NumericCatalog = nil
				}
				if marker != "sibling_codec" {
					c.Codec = "aut.sibling_conditions.v1"
				}
				encoded, err := json.Marshal(c)
				require.NoError(t, err)
				p.RunnerEvaluation = string(encoded)
				encoded, err = json.Marshal(table)
				require.NoError(t, err)
				var decoded []domain.AutomationRuleExecution
				require.Error(t, unmarshalAutomationSnapshot(encoded, &decoded))
				require.Empty(t, decoded)
			})
		}
	}
}

func TestAUTNumericRecoveryOldEmptyMarkerKeepsFrozenLegacyPlan(t *testing.T) {
	for _, codec := range []string{"aut.execution.v1", "aut.execution.v2", "aut.execution.v3", "aut.execution.v4"} {
		t.Run(codec, func(t *testing.T) {
			f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			id := f.call(f.startCommand(), 200).ID
			e := f.execution(id)
			var runner automationRunnerContext
			require.NoError(t, json.Unmarshal([]byte(e.RunnerContext), &runner))
			plan, reason := compileAutomationActions(f.rule.Actions)
			require.Empty(t, reason)
			require.NoError(t, f.aut.queueAutomationExecution(context.Background(), runner.command(e), &e, plan))
			path := filepath.Join(t.TempDir(), "old-empty-marker.json")
			require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "old-empty-marker", path))
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var file snapshotFile
			require.NoError(t, json.Unmarshal(raw, &file))
			var table map[string]any
			require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
			table["codec"] = codec
			for _, row := range table["rows"].([]any) {
				p := row.(map[string]any)["private"].(map[string]any)
				delete(p, "runner_condition_semantics")
				if codec == "aut.execution.v1" {
					delete(p, "runner_evaluation")
				}
			}
			file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
			require.NoError(t, err)
			raw, err = json.Marshal(file.Snapshot)
			require.NoError(t, err)
			file.Checksum = sha256Bytes(raw)
			raw, err = json.Marshal(file)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			target := newModuleTestDB(t, "AUT")
			require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "old-empty-marker", path, nil))
			restarted := f.aut
			restarted.DB = target
			ran, err := restarted.RunAutomationOnce(context.Background())
			require.NoError(t, err)
			require.True(t, ran)
			var result domain.AutomationRuleExecution
			require.NoError(t, target.First(&result, id).Error)
			require.Empty(t, result.RunnerConditionSemantics)
			require.Empty(t, result.RunnerEvaluation)
			require.Equal(t, "succeeded", result.Status)
			f.assertTarget("resolved", "medium", 2)
			siblingRecoveryReceipts(t, f, 1)
		})
	}
}

func TestAUTNumericRecoveryCascadeKeepsProfileAndCausalOrder(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"send_message","action_params":"one"},{"action_name":"private_note","action_params":"two"}]`)
	d := numericRecoveryDefinition(t, f, "conversation_attribute")
	con, msg := cascadeSources(t, f)
	numeric := numericRecoveryRule(t, f, "message_created", numericRecoveryConditions(d, false), siblingRecoveryUrgent)
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	bridge := &AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{con, msg}}
	drainCascadeBridge(t, bridge)
	var runs []domain.AutomationRuleExecution
	require.NoError(t, f.aut.DB.Where("rule_id = ?", numeric.ID).Order("id ASC").Find(&runs).Error)
	require.Len(t, runs, 2)
	for _, run := range runs {
		require.Equal(t, "succeeded", run.Status)
		require.Equal(t, domain.AutomationNumericCustomV1, run.RunnerConditionSemantics)
		require.NotNil(t, run.OriginExecutionID)
		require.Equal(t, uint(1), run.AutomationDepth)
	}
	require.Greater(t, runs[1].TargetVersion, runs[0].TargetVersion)
	f.assertTarget("open", "urgent", 5)
	drainCascadeBridge(t, bridge)
	require.NoError(t, validateRestoredAutomationSnapshot(f.aut.DB))
	var count int64
	require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("rule_id = ?", numeric.ID).Count(&count).Error)
	require.Equal(t, int64(2), count)
}

// The native PostgreSQL helper skips unless DB_DSN is explicitly supplied.
func TestPostgresAUTNumericRecoveryConcurrentWorkersCommitOneDecision(t *testing.T) {
	f := newAutomationActionFixture(t, true, `[{"action_name":"resolve_conversation"}]`)
	d := numericRecoveryDefinition(t, f, "conversation_attribute")
	numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
	rows := numericRecoveryAdmit(t, f)
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
	numericRecoveryDecision(t, stored, "matched")
	require.Equal(t, int32(1), reads.Load())
	f.assertTarget("resolved", "urgent", 3)
	siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTNumericRecoveryFrozenInvalidCatalogCannotBeRepairedLater(t *testing.T) {
	for _, drift := range []string{"missing", "attribute_key", "attribute_model", "attribute_display_type"} {
		t.Run(drift, func(t *testing.T) {
			f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
			d := numericRecoveryDefinition(t, f, "conversation_attribute")
			numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
			if drift == "missing" {
				require.NoError(t, f.owners["CUS"].DB.Delete(&d).Error)
			} else {
				value := map[string]string{"attribute_key": "changed_score", "attribute_model": "contact_attribute", "attribute_display_type": "text"}[drift]
				require.NoError(t, f.owners["CUS"].DB.Model(&d).Update(drift, value).Error)
			}
			rows := numericRecoveryAdmit(t, f)
			// Restore valid live metadata after admission. The original invalid
			// definition observation still determines this frozen execution.
			if drift == "missing" {
				require.NoError(t, f.owners["CUS"].DB.Create(&d).Error)
			} else {
				require.NoError(t, f.owners["CUS"].DB.Model(&d).Updates(map[string]any{"attribute_key": "score", "attribute_model": "conversation_attribute", "attribute_display_type": "number"}).Error)
			}
			f.run()
			f.run()
			failed := f.execution(rows[1].ID)
			require.Equal(t, "failed", failed.Status)
			require.Equal(t, "invalid_frozen_definition", failed.Error)
			numericRecoveryDecision(t, failed, "failed")
			require.Nil(t, failed.RunnerRetryAt)
			f.assertTarget("resolved", "medium", 2)
			siblingRecoveryReceipts(t, f.autRunnerFixture, 1)
			siblingRecoveryIdle(t, f.autRunnerFixture)
		})
	}
}

func TestAUTNumericRecoveryUnknownPredecessorWaitsForReceipt(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	d := numericRecoveryDefinition(t, f, "conversation_attribute")
	numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
	rows := numericRecoveryAdmit(t, f)
	f.lost["CON"].Store(true)
	f.run()
	require.Equal(t, "waiting", f.execution(rows[0].ID).Status)
	f.assertTarget("resolved", "medium", 2)
	var reads atomic.Int32
	siblingRecoveryHook(f, "CON", func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.Contains(r.URL.Path, "/queries/") {
			reads.Add(1)
		}
		next.ServeHTTP(w, r)
	})
	siblingRecoveryIdle(t, f.autRunnerFixture)
	require.Zero(t, reads.Load(), "unconfirmed predecessor effect cannot start numeric evaluation")
	require.Equal(t, rows[1].RunnerEvaluation, f.execution(rows[1].ID).RunnerEvaluation)
	siblingRecoveryRetryNow(t, f.autRunnerFixture, rows[0].ID)
	restarted := f.aut
	ran, err := restarted.RunAutomationOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, "succeeded", f.execution(rows[0].ID).Status)
	f.run()
	numericRecoveryDecision(t, f.execution(rows[1].ID), "matched")
	siblingRecoveryReceipts(t, f.autRunnerFixture, 1)
	f.run()
	f.assertTarget("resolved", "urgent", 3)
	siblingRecoveryReceipts(t, f.autRunnerFixture, 2)
	siblingRecoveryIdle(t, f.autRunnerFixture)
}

func TestAUTNumericRecoveryOldV4EmptyMarkerDoesNotAdoptNumericOperators(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	d := numericRecoveryDefinition(t, f, "conversation_attribute")
	// Identical-looking numeric leaves remain legacy when no version opted in.
	siblingRecoveryRule(t, f.autRunnerFixture, numericRecoveryConditions(d, true), siblingRecoveryUrgent)
	rows := siblingRecoveryAdmit(t, f.autRunnerFixture)
	f.run()
	path := filepath.Join(t.TempDir(), "legacy-numeric-leaf.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "legacy-numeric-leaf", path))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var file snapshotFile
	require.NoError(t, json.Unmarshal(raw, &file))
	var table map[string]any
	require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
	table["codec"] = "aut.execution.v4"
	for _, row := range table["rows"].([]any) {
		delete(row.(map[string]any)["private"].(map[string]any), "runner_condition_semantics")
	}
	file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
	require.NoError(t, err)
	raw, err = json.Marshal(file.Snapshot)
	require.NoError(t, err)
	file.Checksum = sha256Bytes(raw)
	raw, err = json.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))
	target := newModuleTestDB(t, "AUT")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "legacy-numeric-leaf", path, nil))
	restarted := f.aut
	restarted.DB = target
	ran, err := restarted.RunAutomationOnce(context.Background())
	require.NoError(t, err)
	require.True(t, ran)
	var result domain.AutomationRuleExecution
	require.NoError(t, target.First(&result, rows[1].ID).Error)
	require.Empty(t, result.RunnerConditionSemantics)
	require.Equal(t, "no_match", result.Status)
	siblingRecoveryDecision(t, result, "no_match")
	f.assertTarget("resolved", "medium", 2)
	siblingRecoveryReceipts(t, f.autRunnerFixture, 1)
}

func TestAUTNumericRecoveryOldV4RejectsAdoptedRulesWithoutExecutions(t *testing.T) {
	f := newAutomationActionFixture(t, false, `[]`)
	d := numericRecoveryDefinition(t, f, "conversation_attribute")
	numericRecoveryRule(t, f, f.rule.EventName, numericRecoveryConditions(d, false), siblingRecoveryUrgent)
	path := filepath.Join(t.TempDir(), "numeric-rules-only.json")
	require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "numeric-rules-only", path))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, selector := range []string{"rule_and_version", "rule_only", "version_only"} {
		t.Run(selector, func(t *testing.T) {
			var file snapshotFile
			require.NoError(t, json.Unmarshal(original, &file))
			var table automationExecutionSnapshotTable
			require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_executions"], &table))
			require.Empty(t, table.Rows)
			table.Codec = "aut.execution.v4"
			file.Snapshot.Tables["automation_rule_executions"], err = json.Marshal(table)
			require.NoError(t, err)
			if selector == "rule_only" {
				var versions []domain.AutomationRuleVersion
				require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rule_versions"], &versions))
				for i := range versions {
					versions[i].ConditionSemantics = ""
				}
				file.Snapshot.Tables["automation_rule_versions"], err = json.Marshal(versions)
				require.NoError(t, err)
			}
			if selector == "version_only" {
				var rules []domain.AutomationRule
				require.NoError(t, json.Unmarshal(file.Snapshot.Tables["automation_rules"], &rules))
				for i := range rules {
					rules[i].ConditionSemantics = ""
				}
				file.Snapshot.Tables["automation_rules"], err = json.Marshal(rules)
				require.NoError(t, err)
			}
			raw, err := json.Marshal(file.Snapshot)
			require.NoError(t, err)
			file.Checksum = sha256Bytes(raw)
			raw, err = json.Marshal(file)
			require.NoError(t, err)
			corrupt := filepath.Join(t.TempDir(), "old-adopted-rules.json")
			require.NoError(t, os.WriteFile(corrupt, raw, 0600))
			target := newModuleTestDB(t, "AUT")
			sentinel := domain.AutomationRule{ID: 700, AccountID: 91, Name: "preserve destination", EventName: "conversation_created", Conditions: "[]", Actions: "[]"}
			require.NoError(t, target.Create(&sentinel).Error)
			rebuilt := false
			require.Error(t, RestoreSnapshot(context.Background(), target, "AUT", "numeric-rules-only", corrupt, func(context.Context, *gorm.DB) error { rebuilt = true; return nil }))
			require.False(t, rebuilt)
			var preserved domain.AutomationRule
			require.NoError(t, target.First(&preserved, sentinel.ID).Error)
			require.Equal(t, sentinel.Name, preserved.Name)
			var rules, executions, markers int64
			require.NoError(t, target.Model(&domain.AutomationRule{}).Count(&rules).Error)
			require.Equal(t, int64(1), rules)
			require.NoError(t, target.Model(&domain.AutomationRuleExecution{}).Count(&executions).Error)
			require.Zero(t, executions)
			require.NoError(t, target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "recovery_complete").Count(&markers).Error)
			require.Zero(t, markers)
		})
	}
}

func TestAUTNumericRecoveryOldV4RejectsCaseInsensitivePrivateProfileAlias(t *testing.T) {
	f := newAUTRunnerFixture(t, false, `[{"action_name":"resolve_conversation"}]`)
	id := f.call(f.startCommand(), 200).ID
	raw, err := marshalAutomationSnapshot([]domain.AutomationRuleExecution{f.execution(id)})
	require.NoError(t, err)
	var table map[string]any
	require.NoError(t, json.Unmarshal(raw, &table))
	table["codec"] = "aut.execution.v4"
	private := table["rows"].([]any)[0].(map[string]any)["private"].(map[string]any)
	delete(private, "runner_condition_semantics")
	baseline, err := json.Marshal(table)
	require.NoError(t, err)
	var decoded []domain.AutomationRuleExecution
	require.NoError(t, unmarshalAutomationSnapshot(baseline, &decoded), "ordinary old empty-profile row remains valid")
	require.Len(t, decoded, 1)
	private["Runner_Condition_Semantics"] = domain.AutomationNumericCustomV1
	raw, err = json.Marshal(table)
	require.NoError(t, err)
	decoded = nil
	require.Error(t, unmarshalAutomationSnapshot(raw, &decoded), "case-insensitive JSON decoding cannot hide a numeric marker from old-codec validation")
	require.Empty(t, decoded)
}
