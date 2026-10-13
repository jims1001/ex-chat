package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

// AutomationFactSource is implemented by each owner; AUT never scans CON/MSG tables.
type AutomationFactSource interface {
	PendingAutomationFacts(context.Context, int) ([]foundation.EventEnvelope, error)
	MarkAutomationFactDelivered(context.Context, string) error
}

// AutomationFactPager keeps each pass bounded by an owner-local snapshot horizon.
// Its opaque cursor permits healthy later facts to progress past waiting families.
// Sources without this optional port retain the original single-page contract.
type AutomationFactPager interface {
	PendingAutomationFactPage(context.Context, int, string) ([]foundation.EventEnvelope, string, error)
}

// AutomationWorkflowSource is supplied by application composition. Creation
// recovery runs outside AUT Runtime; AUT has no reverse dependency on ORC.
type AutomationWorkflowSource interface {
	ReconcileCreationWorkflows(context.Context, int) (int, error)
}

type AutomationBridge struct {
	Runtime  *Runtime
	Sources  []AutomationFactSource
	Workflow AutomationWorkflowSource
}
type automationVerifiedTargetKey struct{}
type automationVerifiedTarget struct {
	AccountID, ConversationID, Version uint
	MessageID                          *uint
	FactBatchKey                       string
	PredecessorID                      *uint
}

// NewMonolithAutomation composes the same owner contracts and durable runner used
// by isolated modules. It opens no sockets and does not migrate foreign tables.
func NewMonolithAutomation(db *gorm.DB, workflow AutomationWorkflowSource, sources ...AutomationFactSource) (*AutomationBridge, error) {
	if workflow == nil {
		return nil, errors.New("application creation-workflow coordinator is required")
	}
	if err := ClaimAutomationMode(context.Background(), db, "durable"); err != nil {
		return nil, err
	}
	runtimes := map[string]*Runtime{}
	makeRuntime := func(module string) (*Runtime, error) {
		cfg := Config{ModuleID: module, ContractVersion: "v1", AcceptedVersions: map[string]bool{"v1": true}, AuthToken: foundation.GenerateUUID(), AllowedCallers: map[string]bool{"AUT": true, "API": true}, MaxTimeout: 5 * time.Second, MaxRequestBytes: 1 << 20, EnableBusiness: true, EventPollInterval: time.Second, Dependencies: map[string]DependencyConfig{}}

		dependencies := map[string][]string{"TEN": {"IAM"}, "CON": {"TEN", "CUS"}, "MSG": {"CON", "TEN"}, "AUT": {"CON", "MSG", "CUS"}}
		for _, owner := range dependencies[module] {
			target := runtimes[owner]
			cfg.Dependencies[owner] = DependencyConfig{URL: "http://local-" + owner, Token: target.Config.AuthToken, LocalHandler: target.Handler()}
			target.Config.AllowedCallers[module] = true
		}
		r := &Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry(module)}
		if err := r.RegisterBusinessContracts(); err != nil {
			return nil, err
		}
		runtimes[module] = r
		return r, nil
	}
	for _, owner := range []string{"IAM", "TEN", "CUS"} {
		if _, err := makeRuntime(owner); err != nil {
			return nil, err
		}
	}
	con, err := makeRuntime("CON")
	if err != nil {
		return nil, err
	}
	msg, err := makeRuntime("MSG")
	if err != nil {
		return nil, err
	}
	aut, err := makeRuntime("AUT")
	if err != nil {
		return nil, err
	}
	aut.Config.Dependencies["CON"] = DependencyConfig{URL: "http://local-con", Token: con.Config.AuthToken, LocalHandler: con.Handler()}
	aut.Config.Dependencies["MSG"] = DependencyConfig{URL: "http://local-msg", Token: msg.Config.AuthToken, LocalHandler: msg.Handler()}

	return &AutomationBridge{Runtime: aut, Sources: sources, Workflow: workflow}, nil
}

func (b *AutomationBridge) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := b.RunOnce(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (b *AutomationBridge) RunOnce(ctx context.Context) (int, error) {
	count := 0
	var workflowError error
	if b.Workflow != nil {
		completed, err := b.Workflow.ReconcileCreationWorkflows(ctx, 100)
		count += completed
		// The owner gates incomplete workflow facts. A retryable failure here
		// cannot starve unrelated already-admitted jobs or healthy facts.
		workflowError = err
	}

	drain := func() error {
		for i := 0; i < 100; i++ {
			ran, err := b.Runtime.RunAutomationOnce(ctx)
			if err != nil {
				return err
			}
			if !ran {
				break
			}
			count++
		}
		return nil
	}
	if err := drain(); err != nil {
		return count, err
	}
	for _, source := range b.Sources {
		cursor := ""
		for {
			var events []foundation.EventEnvelope
			var next string
			var err error
			if pager, ok := source.(AutomationFactPager); ok {
				events, next, err = pager.PendingAutomationFactPage(ctx, 100, cursor)
			} else {
				events, err = source.PendingAutomationFacts(ctx, 100)
			}
			if err != nil {
				return count, err
			}
			for _, event := range events {
				if err := b.Runtime.AcceptAutomationFact(ctx, event); err != nil {
					if errors.Is(err, errAutomationCascadePending) || errors.Is(err, errAutomationCascadeUnverified) {
						continue // Other causal families and ordinary facts remain eligible.
					}
					return count, err
				}
				if err := source.MarkAutomationFactDelivered(ctx, event.EventID); err != nil {
					return count, err
				}
				count++
				if err := drain(); err != nil {
					return count, err
				}
			}
			if next == "" {
				break
			}
			if next == cursor {
				return count, errors.New("automation source cursor did not advance")
			}
			cursor = next
		}
	}
	return count, workflowError
}

// AcceptAutomationFact is a trusted owner-port entry, not an HTTP command.
// Acceptance freezes only the version already in force at the recorded fact's
// timestamp (not a globally ordered cross-database commit timestamp). A later rule edit/disable/delete produces an explicit failed record;
// an old fact can never silently run a new definition. Queued runs retain their
// compiled actions even when the rule is edited afterwards.
func (r *Runtime) AcceptAutomationFact(ctx context.Context, event foundation.EventEnvelope) error {
	if r.Config.ModuleID != "AUT" || event.Validate() != nil {
		return &ContractFailure{Type: "validation_failed", Message: "invalid automation fact"}
	}
	if event.OccurredAt.After(time.Now().UTC()) {
		return &ContractFailure{Type: "validation_failed", Message: "automation fact time is in the future"}
	}
	eventNames, err := automationFactEventNames(event)
	if err != nil {
		return &ContractFailure{Type: "validation_failed", Message: err.Error()}
	}
	ownerRaw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, cascade := event.Payload["origin_execution_id"]
	conversationID, err := uintFilter(event.Payload, "conversation_id")
	if err != nil {
		return &ContractFailure{Type: "validation_failed", Message: "fact conversation is required"}
	}
	receiptID := automationFactReceipt(event)
	var existing int64
	if err := r.DB.WithContext(ctx).Model(&domain.LocalChangeJournal{}).Where("change_id = ? AND source_module = ? AND account_id = ?", receiptID, "AUT", event.AccountID).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		if cascade || automationRetryFact(event) {
			var receipt domain.LocalChangeJournal
			if err := r.DB.WithContext(ctx).Where("change_id = ? AND source_module = ? AND account_id = ?", receiptID, "AUT", event.AccountID).First(&receipt).Error; err != nil {
				return err
			}
			if (cascade && receipt.BeforeState != string(ownerRaw)) || (automationRetryFact(event) && receipt.AfterState != string(ownerRaw)) {
				return errAutomationCascadeUnverified
			}
		}
		return nil
	}
	if cascade {
		if err := automationCascadeIdentityConflict(r.DB.WithContext(ctx), event, string(ownerRaw)); err != nil {
			if errors.Is(err, errAutomationCascadeUnverified) {
				if recordErr := r.recordAutomationCascadeQuarantine(ctx, event); recordErr != nil {
					return recordErr
				}
			}
			return err
		}
	}
	targetVersion, err := automationCascadeTarget(r.DB.WithContext(ctx), event)
	if errors.Is(err, errAutomationCascadePending) {
		return err
	}
	if errors.Is(err, errAutomationCascadeUnverified) {
		if recordErr := r.recordAutomationCascadeQuarantine(ctx, event); recordErr != nil {
			return recordErr
		}
		return err
	}
	if err != nil {
		return err
	}
	if cascade {
		copyPayload := make(map[string]any, len(event.Payload)+1)
		for key, value := range event.Payload {
			copyPayload[key] = value
		}
		copyPayload["origin_object_version"] = copyPayload["object_version"]
		copyPayload["object_version"] = targetVersion
		event.Payload = copyPayload
	}
	candidates, err := automationVersionsAt(r.DB.WithContext(ctx), event.AccountID, event.OccurredAt)
	if err != nil {
		return err
	}
	eligible := make([]domain.AutomationRuleVersion, 0, len(candidates))
	for _, candidate := range candidates {
		if !eventNames[candidate.EventName] {
			continue
		}
		var rule domain.AutomationRule
		if err := r.DB.WithContext(ctx).Where("account_id = ? AND id = ?", event.AccountID, candidate.RuleID).First(&rule).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return err
		}
		if rule.Active && rule.UpdatedAt.Before(event.OccurredAt) {
			eligible = append(eligible, candidate)
		}
	}

	verified := automationVerifiedTarget{AccountID: event.AccountID, ConversationID: conversationID, Version: targetVersion, FactBatchKey: receiptID}
	actor := foundation.ContractActor{Type: "System", ID: "automation-bridge", AuthSource: "module"}
	query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: event.AccountID, Actor: actor, Filter: map[string]any{"id": conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id", "object_version"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	for _, candidate := range eligible {
		if attributes, _ := automationProfileAttributeNeeds(candidate.Conditions, candidate.ConditionSemantics); attributes {
			query.Fields = append(query.Fields, "custom_attributes")
			break
		}
	}
	var conversation conversationView
	targetFailure := ""
	if automationRetryFact(event) && event.Payload["retry_activity_result"] == "stale" {
		targetFailure = "target_version_stale"
	}
	dep, ok := r.Config.Dependencies["CON"]
	if !ok {
		return &ContractFailure{Type: "dependency_unavailable", Message: "CON dependency is not configured"}
	}
	if err := newModuleClient("AUT", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
		var failure *ContractFailure
		if !errors.As(err, &failure) || failure.Type != "resource_not_found" {
			return err
		}
		targetFailure = "target_unavailable"
	} else if conversation.ID != conversationID || conversation.AccountID != event.AccountID || conversation.ObjectVersion < 1 || conversation.Status == domain.ConversationStatusDeleting {
		targetFailure = "target_unavailable"
	} else {
		if uint(conversation.ObjectVersion) != targetVersion {
			targetFailure = "target_version_stale"
		}
	}
	var conditionMessage *messageView
	if eventNames["message_created"] {
		id, err := uintFilter(event.Payload, "message_id")
		if err != nil {
			return &ContractFailure{Type: "validation_failed", Message: "fact message is required"}
		}
		dep, ok := r.Config.Dependencies["MSG"]
		if !ok {
			return &ContractFailure{Type: "dependency_unavailable", Message: "MSG dependency is not configured"}
		}
		query.QueryType, query.Filter = "msg.message.get.v1", map[string]any{"id": id}
		query.Fields = []string{"id", "account_id", "conversation_id"}
		var msg messageView
		if err := newModuleClient("AUT", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &msg); err != nil {
			var failure *ContractFailure
			if !errors.As(err, &failure) || failure.Type != "resource_not_found" {
				return err
			}
			targetFailure = "target_unavailable"
		} else if msg.ID != id || msg.AccountID != event.AccountID || msg.ConversationID != conversationID || msg.Deleted {
			targetFailure = "target_unavailable"
		}
		verified.MessageID = &id
		conditionMessage = &msg
	}

	var conditionContext *automationConditionContext
	if targetFailure == "" {
		conditionContext, err = r.prepareAutomationConditionContext(ctx, actor, conversation, conditionMessage, eligible)
		if err != nil {
			return err
		}
	}
	// Foreign owner queries above run outside AUT's transaction, including in the
	// single-connection SQLite monolith. Owner CAS still rejects races at execution.
	intakeErr := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", receiptID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			if automationRetryFact(event) {
				var receipt domain.LocalChangeJournal
				if err := tx.Where("change_id = ? AND source_module = ? AND account_id = ?", receiptID, "AUT", event.AccountID).First(&receipt).Error; err != nil {
					return err
				}
				if receipt.AfterState != string(ownerRaw) {
					return errAutomationCascadeUnverified
				}
			}
			return nil
		}
		if cascade {
			var ownerEvent foundation.EventEnvelope
			if json.Unmarshal(ownerRaw, &ownerEvent) != nil {
				return errAutomationCascadeUnverified
			}
			version, err := automationCascadeTarget(tx, ownerEvent)
			if err != nil {
				return err
			}
			if version != targetVersion {
				return errAutomationCascadeUnverified
			}
			claim := domain.LocalChangeJournal{ChangeID: automationCascadeClaim(ownerEvent), SourceModule: "AUT", AccountID: event.AccountID, Action: "automation_cascade_received", EventID: event.EventID, CommandID: event.CausationID, SourceType: event.SourceModule, AfterState: string(ownerRaw), Status: "delivered", Result: "applied", OccurredAt: event.OccurredAt}
			if err := tx.Create(&claim).Error; err != nil {
				return err
			}
		}
		raw, _ := json.Marshal(event)
		receipt := domain.LocalChangeJournal{ChangeID: receiptID, SourceModule: "AUT", AccountID: event.AccountID, Action: "automation_fact_received", SchemaVersion: "aut.fact.v3", EventID: event.EventID, SourceType: event.SourceModule, AfterState: string(raw), Status: "delivered", Result: "applied", OccurredAt: event.OccurredAt}
		if cascade {
			receipt.BeforeState = string(ownerRaw)
			receipt.CommandID = event.CausationID
		}
		if err := tx.Create(&receipt).Error; err != nil {
			return err
		}
		versions, err := automationVersionsAt(tx, event.AccountID, event.OccurredAt)
		if err != nil {
			return err
		}
		seen := map[uint]bool{}
		for _, version := range versions {
			if seen[version.RuleID] {
				continue
			}
			seen[version.RuleID] = true
			if !eventNames[version.EventName] {
				continue
			}
			eventName := version.EventName
			reason := targetFailure
			var rule domain.AutomationRule
			if err := tx.Where("account_id = ? AND id = ?", event.AccountID, version.RuleID).First(&rule).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				reason = "rule_deleted_before_dispatch"
			} else if err != nil {
				return err
			} else if !rule.Active {
				reason = "rule_disabled_before_dispatch"
			} else if !rule.UpdatedAt.Before(event.OccurredAt) {
				reason = "rule_changed_before_dispatch"
			}
			input := map[string]any{"event_id": event.EventID, "event_name": eventName, "conversation_id": conversationID, "target_version": verified.Version, "rule_version": version.Version}
			if verified.MessageID != nil {
				input["message_id"] = *verified.MessageID
			}
			for _, key := range []string{"automation_depth", "origin_execution_id"} {
				if value, ok := event.Payload[key]; ok {
					input[key] = value
				}
			}
			key := fmt.Sprintf("aut-fact:%s:%d", receiptID, version.RuleID)
			digest := sha256.Sum256([]byte(key))
			key = hex.EncodeToString(digest[:])
			command := foundation.CommandEnvelope{CommandID: key, CommandType: "aut.execution.start.v1", AccountID: event.AccountID, Actor: actor, Target: foundation.ContractTarget{Type: "rule", ID: strconv.Itoa(int(version.RuleID))}, Input: input, IdempotencyKey: key, CorrelationID: event.CorrelationID, CausationID: event.EventID, RequestedAt: event.OccurredAt, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 5000, OnTimeout: "query", StatusQuery: "aut.execution.get.v1"}}
			observation := automationFactObservation(event, eventName, conditionContext)
			runCtx := context.WithValue(context.WithValue(context.WithValue(ctx, transactionKey{}, tx), automationVerifiedTargetKey{}, verified), automationConditionContextKey{}, observation)
			if reason == "" {
				view, err := r.startAutomationExecution(runCtx, command)
				if err == nil {
					data, ok := view.(map[string]any)
					id, validID := data["id"].(uint)
					if !ok || !validID || id == 0 {
						return errors.New("automation admission returned an invalid execution identity")
					}
					verified.PredecessorID = &id
					continue
				}
				var f *ContractFailure
				if !errors.As(err, &f) || f.Type == "dependency_unavailable" {
					return err
				}
				reason = "fact_admission_rejected"
			}
			e := domain.AutomationRuleExecution{AccountID: event.AccountID, RuleID: version.RuleID, RuleVersion: version.Version, RunnerConditionSemantics: version.ConditionSemantics, ExecutionKey: &key, EventID: event.EventID, EventName: eventName, ConversationID: conversationID, MessageID: verified.MessageID, TargetVersion: verified.Version, RunnerTargetVersion: verified.Version, FactBatchKey: receiptID, RunnerPredecessorID: verified.PredecessorID, TriggeredAt: time.Now().UTC(), ActionResults: "[]", Status: "received"}
			// Admission failures retain the same immutable ancestry as their
			// siblings, including a disabled/deleted rule in a recursive fact.
			if depth, err := uintFilter(input, "automation_depth"); err == nil {
				e.AutomationDepth = depth
			}
			if origin, err := uintFilter(input, "origin_execution_id"); err == nil {
				e.OriginExecutionID = &origin
			}
			if err := tx.Create(&e).Error; err != nil {
				return err
			}
			if err := r.finishAutomationExecution(runCtx, command, &e, "failed", reason); err != nil {
				return err
			}
			id := e.ID
			verified.PredecessorID = &id
		}
		return freezeAutomationFactBatch(tx, receipt)
	})
	if intakeErr != nil && isUniqueConflict(intakeErr) {
		var receipt domain.LocalChangeJournal
		if err := r.DB.WithContext(ctx).Where("change_id = ? AND source_module = ? AND account_id = ? AND event_id = ?", receiptID, "AUT", event.AccountID, event.EventID).First(&receipt).Error; err == nil {
			if (cascade && receipt.BeforeState != string(ownerRaw)) || (automationRetryFact(event) && receipt.AfterState != string(ownerRaw)) {
				return errAutomationCascadeUnverified
			}
			return nil
		}
		if cascade {
			var owner foundation.EventEnvelope
			if json.Unmarshal(ownerRaw, &owner) != nil {
				return errAutomationCascadeUnverified
			}
			if err := automationCascadeIdentityConflict(r.DB.WithContext(ctx), owner, string(ownerRaw)); err != nil {
				if errors.Is(err, errAutomationCascadeUnverified) {
					if recordErr := r.recordAutomationCascadeQuarantine(ctx, owner); recordErr != nil {
						return recordErr
					}
				}
				return err
			}
		}
	}
	return intakeErr
}
func automationFactReceipt(event foundation.EventEnvelope) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", event.SourceModule, event.AccountID, event.EventID)))
	return "aut-fact-" + hex.EncodeToString(sum[:])[:48]
}

// Fetch one latest snapshot per rule for the common case. Historical replay
// walks only edited rules in pages, using Go timestamps so SQLite TEXT offsets
// cannot change ordering. No arbitrary rule/history limit drops eligible rules.
func automationVersionsAt(tx *gorm.DB, accountID uint, at time.Time) ([]domain.AutomationRuleVersion, error) {
	var latest []domain.AutomationRuleVersion
	if err := tx.Where("account_id = ? AND version = (SELECT MAX(v.version) FROM automation_rule_versions AS v WHERE v.account_id = automation_rule_versions.account_id AND v.rule_id = automation_rule_versions.rule_id)", accountID).Order("rule_id ASC").Find(&latest).Error; err != nil {
		return nil, err
	}
	result := make([]domain.AutomationRuleVersion, 0, len(latest))
	for _, version := range latest {
		for !version.CreatedAt.Before(at) {
			var previous []domain.AutomationRuleVersion
			if err := tx.Where("account_id = ? AND rule_id = ? AND version < ?", accountID, version.RuleID, version.Version).Order("version DESC").Limit(50).Find(&previous).Error; err != nil {
				return nil, err
			}
			if len(previous) == 0 {
				version.ID = 0
				break
			}
			for _, candidate := range previous {
				version = candidate
				if candidate.CreatedAt.Before(at) {
					break
				}
			}
		}
		if version.ID != 0 {
			result = append(result, version)
		}
	}
	return result, nil
}

// CancelAutomation delegates to the same AUT lifecycle/receipt implementation.
func (b *AutomationBridge) CancelAutomation(ctx context.Context, accountID, executionID uint) (any, error) {
	id := fmt.Sprintf("monolith-aut-cancel:%d:%d", accountID, executionID)
	command := foundation.CommandEnvelope{CommandID: id, CommandType: "aut.execution.cancel.v1", AccountID: accountID, Actor: foundation.ContractActor{Type: "System", ID: "monolith-api", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "execution", ID: strconv.Itoa(int(executionID))}, Input: map[string]any{}, IdempotencyKey: id, CorrelationID: id, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 5000, OnTimeout: "query", StatusQuery: "aut.execution.get.v1"}}
	return NewCommandReceiptStore(b.Runtime.DB, "AUT").Execute(ctx, command, func(txctx context.Context) (any, error) { return b.Runtime.cancelAutomationExecution(txctx, command) })
}
