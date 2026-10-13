package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This private checkpoint distinguishes newly deferred siblings from old durable
// executions whose admission decision is already final. No live rule is consulted
// when resuming. A lease may repeat read-only preparation after a crash, but only
// one fenced transaction can commit a condition decision before any action.
type automationSiblingEvaluation struct {
	ConditionSemantics string                          `json:"condition_semantics,omitempty"`
	Codec              string                          `json:"codec"`
	Conditions         string                          `json:"conditions"`
	Actions            string                          `json:"actions"`
	Metadata           automationRunnerContext         `json:"metadata"`
	Initial            automationConditionObservation  `json:"initial"`
	Decision           string                          `json:"decision"`
	Version            uint                            `json:"version"`
	Observation        *automationConditionObservation `json:"observation,omitempty"`
	Reason             string                          `json:"reason,omitempty"`
	EvaluatedAt        *time.Time                      `json:"evaluated_at,omitempty"`
}

type automationConditionObservation struct {
	NumericCatalog []automationNumericCatalogObservation `json:"numeric_catalog,omitempty"`
	Conversation   conversationView                      `json:"conversation"`
	Message        *messageView                          `json:"message,omitempty"`
	Contact        *contactView                          `json:"contact,omitempty"`
	Company        *companyView                          `json:"company,omitempty"`
	Labels         []labelView                           `json:"labels"`
	ContactLoaded  bool                                  `json:"contact_loaded"`
	LabelsLoaded   bool                                  `json:"labels_loaded"`
}

func freezeAutomationObservation(c *automationConditionContext) automationConditionObservation {
	return automationConditionObservation{NumericCatalog: c.NumericCatalog, Conversation: c.Conversation, Message: c.Message, Contact: c.Contact, Company: c.Company, Labels: c.Labels, ContactLoaded: c.contactLoaded, LabelsLoaded: c.labelsLoaded}
}
func (c automationConditionObservation) context() *automationConditionContext {
	return &automationConditionContext{NumericCatalog: c.NumericCatalog, Conversation: c.Conversation, Message: c.Message, Contact: c.Contact, Company: c.Company, Labels: c.Labels, contactLoaded: c.ContactLoaded, labelsLoaded: c.LabelsLoaded}
}
func (r *Runtime) queueAutomationSiblingEvaluation(ctx context.Context, command foundation.CommandEnvelope, e *domain.AutomationRuleExecution, version domain.AutomationRuleVersion, observation *automationConditionContext) error {
	if observation == nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "sibling condition observation is unavailable"}
	}
	c := automationSiblingEvaluation{ConditionSemantics: version.ConditionSemantics, Codec: "aut.sibling_conditions.v1", Conditions: version.Conditions, Actions: version.Actions, Metadata: automationRunnerContext{ActivityPolicy: automationTouchValidActivity, FailurePolicy: automationContinueDefinite, Actor: foundation.ContractActor{Type: "System", ID: "automation:" + strconv.Itoa(int(e.RuleID)), AuthSource: "module"}, CorrelationID: command.CorrelationID, CausationID: command.CommandID, RequestedAt: command.RequestedAt}, Initial: freezeAutomationObservation(observation), Decision: "pending"}
	if c.ConditionSemantics == domain.AutomationNumericCustomV1 {
		c.Codec = "aut.sibling_conditions.v2"
	} else {
		c.Initial.NumericCatalog = nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	e.RunnerEvaluation, e.Status = string(raw), "evaluating"
	return Transaction(ctx, r.DB).Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ?", e.AccountID, e.ID).Updates(map[string]any{"runner_evaluation": e.RunnerEvaluation, "status": e.Status}).Error
}

func validAutomationConditionObservation(e domain.AutomationRuleExecution, o automationConditionObservation, version uint) bool {
	c := o.Conversation
	if c.ID != e.ConversationID || c.AccountID != e.AccountID || c.ObjectVersion < 1 || uint(c.ObjectVersion) != version || c.Status == domain.ConversationStatusDeleting {
		return false
	}
	if e.EventName == "message_created" {
		if o.Message == nil || e.MessageID == nil || o.Message.ID != *e.MessageID || o.Message.AccountID != e.AccountID || o.Message.ConversationID != e.ConversationID || o.Message.Deleted {
			return false
		}
	} else if o.Message != nil {
		return false
	}
	if o.Contact != nil && (!o.ContactLoaded || o.Contact.AccountID != e.AccountID || o.Contact.ID != c.ContactID) {
		return false
	}
	if o.Company != nil && (o.Contact == nil || o.Contact.CompanyID == nil || o.Company.ID != *o.Contact.CompanyID || o.Company.AccountID != e.AccountID) {
		return false
	}
	lastID := uint(0)
	for _, entry := range o.NumericCatalog {
		if entry.DefinitionID == 0 || entry.DefinitionID <= lastID {
			return false
		}
		lastID = entry.DefinitionID
		if entry.Definition != nil && (entry.Definition.ID != entry.DefinitionID || entry.Definition.AccountID != e.AccountID) {
			return false
		}
	}
	ids, seen := map[uint]bool{}, map[uint]bool{}
	for _, id := range c.LabelIDs {
		if id == 0 || ids[id] {
			return false
		}
		ids[id] = true
	}
	for _, label := range o.Labels {
		if !o.LabelsLoaded || label.AccountID != e.AccountID || label.ID == 0 || !ids[label.ID] || seen[label.ID] {
			return false
		}
		seen[label.ID] = true
	}
	return true
}

func decodeAutomationSiblingEvaluation(e domain.AutomationRuleExecution) (automationSiblingEvaluation, bool) {
	var c automationSiblingEvaluation
	if e.RunnerEvaluation == "" || decodeStrictSnapshotJSON([]byte(e.RunnerEvaluation), &c) != nil || !validAutomationSiblingProfile(e, c) || e.FactBatchKey == "" || e.RunnerPredecessorID == nil || !validAutomationRunnerIdentity(e, c.Metadata) || len(c.Metadata.Actions) != 0 || len(c.Metadata.Dispatches) != 0 || !validAutomationConditionObservation(e, c.Initial, e.TargetVersion) {
		return c, false
	}
	conditions, _, validConditions := parseAutomationProfileConditions(c.Conditions, c.ConditionSemantics)
	if validConditions && len(conditions) == 0 {
		return c, false
	}
	if _, reason := matchAutomationConditionsProfile(c.Conditions, c.ConditionSemantics, c.Initial.context()); reason == "condition_context_unavailable" {
		return c, false
	}
	switch c.Decision {
	case "pending":
		if c.Version != 0 || c.Observation != nil || c.EvaluatedAt != nil || c.Reason != "" || e.RunnerContext != "" || e.RunnerNextAction != 0 || e.RunnerTargetVersion != e.TargetVersion || e.ActionResults != "[]" {
			return c, false
		}
		if e.Status != "evaluating" && e.Status != "cancelled" && e.Status != "manual_action_required" {
			return c, false
		}
	case "matched", "no_match", "failed":
		if c.Version < e.TargetVersion || c.EvaluatedAt == nil || c.EvaluatedAt.IsZero() {
			return c, false
		}
		if c.Observation != nil {
			if !validAutomationConditionObservation(e, *c.Observation, c.Version) {
				return c, false
			}
			// Cached owner/message values and immutable conversation attributes cannot
			// silently change when a later sibling is evaluated.
			a, b := c.Initial, c.Observation
			if !reflect.DeepEqual(a.NumericCatalog, b.NumericCatalog) || !reflect.DeepEqual(a.Message, b.Message) || !reflect.DeepEqual(a.Contact, b.Contact) || !reflect.DeepEqual(a.Company, b.Company) || a.ContactLoaded != b.ContactLoaded || a.Conversation.CustomAttributes != b.Conversation.CustomAttributes || a.Conversation.ContactID != b.Conversation.ContactID || a.Conversation.InboxID != b.Conversation.InboxID {
				return c, false
			}
			matched, reason := matchAutomationConditionsProfile(c.Conditions, c.ConditionSemantics, b.context())
			if c.Decision == "matched" && (!matched || reason != "" || c.Reason != "") {
				return c, false
			}
			if c.Decision == "no_match" && (matched || reason != "" || c.Reason != "conditions_not_matched" || e.Status != "no_match") {
				return c, false
			}
			if c.Decision == "failed" && (reason != "invalid_frozen_definition" || c.Reason != reason) {
				return c, false
			}
		} else if c.Decision != "failed" || (c.Reason != "target_version_stale" && c.Reason != "target_unavailable") {
			return c, false
		}
	default:
		return c, false
	}
	if c.Decision == "failed" && (e.Status != "failed" || e.Error != c.Reason || e.RunnerContext != "" || e.CompletedAt == nil) {
		return c, false
	}
	if c.Decision == "matched" && e.RunnerContext == "" {
		actions, valid := parseAutomationActionDefinitions(c.Actions)
		status, reason := "succeeded", ""
		if !valid {
			status, reason = "failed", "invalid_frozen_definition"
		} else if len(actions) > 0 {
			_, reason = compileAutomationRunnerActions(c.Actions, c.Metadata.FailurePolicy)
			if reason == "" {
				return c, false
			}
			status = "failed"
		}
		if e.Status != status || e.Error != reason || e.CompletedAt == nil {
			return c, false
		}
	}
	if e.RunnerContext != "" {
		var runner automationRunnerContext
		if decodeStrictSnapshotJSON([]byte(e.RunnerContext), &runner) != nil || runner.ActivityPolicy != c.Metadata.ActivityPolicy || runner.FailurePolicy != c.Metadata.FailurePolicy {
			return c, false
		}
	}
	return c, true
}

// Apply only the conversation fields that legacy actions mutate on their shared
// in-memory pointer. Labels deliberately use fresh owned storage below. Contact,
// company, message, IDs and custom attributes retain the admission observation.
func projectAutomationSiblingConversation(tx *gorm.DB, e domain.AutomationRuleExecution, c conversationView) (conversationView, error) {
	var previous []domain.AutomationRuleExecution
	if err := tx.Where("account_id = ? AND fact_batch_key = ? AND id < ?", e.AccountID, e.FactBatchKey, e.ID).Order("id ASC").Find(&previous).Error; err != nil {
		return c, err
	}
	for _, p := range previous {
		if p.RunnerContext == "" {
			continue
		}
		var runner automationRunnerContext
		var results []automationActionResult
		if json.Unmarshal([]byte(p.ActionResults), &results) != nil || json.Unmarshal([]byte(p.RunnerContext), &runner) != nil || p.RunnerNextAction < 0 || p.RunnerNextAction > len(runner.Actions) || len(results) != len(runner.Actions) {
			return c, errAutomationCausalCheckpoint
		}
		for i, a := range runner.Actions[:p.RunnerNextAction] {
			if results[i].Status != "succeeded" {
				continue
			}
			switch a.Command {
			case "con.status.change.v1":
				c.Status = a.Value
			case "con.priority.change.v1":
				c.Priority = a.Value
			case "con.automation.apply.v1":
				id, _ := strconv.ParseUint(a.Value, 10, 64)
				value := uint(id)
				switch a.Operation {
				case "assign_agent":
					c.AssigneeID = &value
				case "assign_team":
					c.TeamID = &value
				case "remove_assigned_agent":
					c.AssigneeID = nil
				case "remove_assigned_team":
					c.TeamID = nil
				case "mute_conversation":
					c.Status = domain.ConversationStatusResolved
				case "snooze_conversation":
					c.Status = domain.ConversationStatusSnoozed
				}
			}
		}
	}
	return c, nil
}

func (r *Runtime) prepareAutomationSiblingObservation(ctx context.Context, e domain.AutomationRuleExecution, c automationSiblingEvaluation, projected conversationView, version uint) (*automationConditionObservation, string, error) {
	dep, ok := r.Config.Dependencies["CON"]
	if !ok {
		return nil, "", &ContractFailure{Type: "dependency_unavailable", Message: "CON dependency is not configured"}
	}
	q := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: e.AccountID, Actor: c.Metadata.Actor, Filter: map[string]any{"id": e.ConversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id", "object_version", "status", "label_ids"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	var live conversationView
	if err := newModuleClient("AUT", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, q, &live); err != nil {
		var f *ContractFailure
		if errors.As(err, &f) && f.Type == "resource_not_found" {
			return nil, "target_unavailable", nil
		}
		return nil, "", err
	}
	if live.ID != e.ConversationID || live.AccountID != e.AccountID || live.ObjectVersion < 1 || live.Status == domain.ConversationStatusDeleting {
		return nil, "target_unavailable", nil
	}
	if uint(live.ObjectVersion) != version {
		return nil, "target_version_stale", nil
	}
	projected.ObjectVersion = int(version)
	observation := c.Initial
	observation.Conversation = projected
	conditions, numbers, _ := parseAutomationProfileConditions(c.Conditions, c.ConditionSemantics)
	for index := range numbers {
		conditions[index].AttributeKey = ""
	}
	_, _, labelsNeeded := automationConditionNeeds(conditions)
	if labelsNeeded {
		projected.LabelIDs = live.LabelIDs
		observation.Conversation.LabelIDs = live.LabelIDs
		// Ask only for labels, retaining the cached contact/company observation.
		labels, err := r.prepareAutomationConditionContext(ctx, c.Metadata.Actor, projected, nil, []domain.AutomationRuleVersion{{Conditions: `[{"attribute_key":"labels"}]`}})
		if err != nil {
			return nil, "", err
		}
		observation.Labels, observation.LabelsLoaded = labels.Labels, true
	}
	return &observation, "", nil
}

// commitAutomationSiblingEvaluation is fenced by the same durable execution lease
// as owner dispatch. Cross-owner reads are always outside this local transaction.
func (r *Runtime) completeAutomationSiblingEvaluation(ctx context.Context, e domain.AutomationRuleExecution, c automationSiblingEvaluation, projected conversationView, version uint, lease string) error {
	observation, reason, readErr := r.prepareAutomationSiblingObservation(ctx, e, c, projected, version)
	return r.DB.Transaction(func(tx *gorm.DB) error {
		var current domain.AutomationRuleExecution
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ? AND runner_lease = ? AND status = ?", e.AccountID, e.ID, lease, "evaluating").First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.RunnerEvaluation != e.RunnerEvaluation {
			return errors.New("sibling evaluation checkpoint changed under lease")
		}
		updates := map[string]any{"runner_lease": "", "runner_lease_until": nil, "runner_retry_at": nil}
		if readErr != nil {
			retry := time.Now().UTC().Add(time.Second)
			updates["runner_retry_at"] = retry
			return tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ? AND runner_lease = ?", e.AccountID, e.ID, lease).Updates(updates).Error
		}
		proved, ready, err := automationPredecessorVersion(tx, current)
		if err != nil {
			return err
		}
		if !ready || proved != version {
			return errAutomationCausalCheckpoint
		}
		c.Observation, c.Version, c.Reason = observation, version, reason
		now := time.Now().UTC()
		c.EvaluatedAt = &now
		matched := false
		if reason == "" {
			matched, reason = matchAutomationConditionsProfile(c.Conditions, c.ConditionSemantics, observation.context())
			c.Reason = reason
		}
		c.Decision = "matched"
		if reason != "" {
			c.Decision = "failed"
		} else if !matched {
			c.Decision = "no_match"
			c.Reason = "conditions_not_matched"
		}
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		current.RunnerEvaluation = string(raw)
		updates["runner_evaluation"] = current.RunnerEvaluation
		if err := tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ? AND runner_lease = ?", e.AccountID, e.ID, lease).Updates(updates).Error; err != nil {
			return err
		}
		txctx := context.WithValue(ctx, transactionKey{}, tx)
		status, reason := "succeeded", c.Reason
		actions, valid := parseAutomationActionDefinitions(c.Actions)
		if c.Decision == "failed" {
			status = "failed"
		} else if c.Decision == "no_match" {
			status = "no_match"
		} else if !valid {
			status, reason = "failed", "invalid_frozen_definition"
		} else if len(actions) > 0 {
			compiled, failure := compileAutomationRunnerActions(c.Actions, c.Metadata.FailurePolicy)
			if failure != "" {
				status, reason = "failed", failure
			} else {
				if err := r.queueAutomationExecution(txctx, c.Metadata.command(current), &current, compiled, c.Metadata.ActivityPolicy); err != nil {
					return err
				}
				return tx.Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND id = ?", current.AccountID, current.ID).Update("status", "received").Error
			}
		}
		if valid && len(actions) > 0 {
			results := make([]automationActionResult, len(actions))
			for i := range results {
				results[i] = automationActionResult{Index: i, Status: "skipped", Reason: reason}
				if reason == "action_capability_executor_unavailable" && i == 0 {
					results[i].Status = "failed"
				}
			}
			raw, _ := json.Marshal(results)
			current.ActionResults = string(raw)
		}
		return r.finishAutomationExecution(txctx, c.Metadata.command(current), &current, status, reason)
	})
}

func validAutomationSiblingProfile(e domain.AutomationRuleExecution, c automationSiblingEvaluation) bool {
	if c.ConditionSemantics != e.RunnerConditionSemantics {
		return false
	}
	switch c.Codec {
	case "aut.sibling_conditions.v1":
		return c.ConditionSemantics == "" && len(c.Initial.NumericCatalog) == 0 && (c.Observation == nil || len(c.Observation.NumericCatalog) == 0)
	case "aut.sibling_conditions.v2":
		return c.ConditionSemantics == domain.AutomationNumericCustomV1
	default:
		return false
	}
}
