package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This immutable grant contains no message content. MSG verifies it through CON
// before opening its own receipt transaction, so neither owner holds a database
// transaction while waiting on the other owner.
type automationMessageAuthorizationView struct {
	PrepareCommandID  string `json:"prepare_command_id"`
	AccountID         uint   `json:"account_id"`
	ConversationID    uint   `json:"conversation_id"`
	ObjectVersion     int    `json:"object_version"`
	ContentHash       string `json:"content_hash"`
	Private           bool   `json:"private"`
	OriginExecutionID uint   `json:"origin_execution_id"`
	AutomationDepth   uint   `json:"automation_depth"`
	ActorID           string `json:"actor_id"`
}

type automationConversationAction struct {
	operation      string
	activityPolicy string
	id             uint
	until          *time.Time
}
type automationOwnerValidationKey struct{}

func validAutomationOwnerActor(actor foundation.ContractActor) bool {
	if actor.Type != "System" || actor.AuthSource != "module" || !strings.HasPrefix(actor.ID, "automation:") {
		return false
	}
	rule, err := strconv.ParseUint(strings.TrimPrefix(actor.ID, "automation:"), 10, 64)
	return err == nil && rule > 0 && actor.ID == "automation:"+strconv.FormatUint(rule, 10)
}

func automationOwnerProvenance(command foundation.CommandEnvelope) (uint, uint, error) {
	if command.AccountID == 0 || !validAutomationOwnerActor(command.Actor) {
		return 0, 0, &ContractFailure{Type: "permission_denied", Message: "automation owner actor is required"}
	}
	origin, originErr := uintFilter(command.Input, "origin_execution_id")
	depth, depthErr := uintFilter(command.Input, "automation_depth")
	if originErr != nil || depthErr != nil || depth > automationMaxDepth {
		return 0, 0, &ContractFailure{Type: "validation_failed", Message: "bounded automation provenance is required"}
	}
	return origin, depth, nil
}

func parseAutomationConversationAction(command foundation.CommandEnvelope) (uint, automationConversationAction, error) {
	var action automationConversationAction
	if _, _, err := automationOwnerProvenance(command); err != nil {
		return 0, action, err
	}
	conversationID, failure := conversationCommandTarget(command)
	if failure != nil {
		return 0, action, failure
	}
	if command.Target.ExpectedVersion == 0 {
		return 0, action, &ContractFailure{Type: "validation_failed", Message: "expected conversation version is required"}
	}
	if value, exists := command.Input["activity_policy"]; exists {
		policy, ok := value.(string)
		if !ok || policy != automationTouchValidActivity {
			return 0, action, &ContractFailure{Type: "validation_failed", Message: "automation activity policy is invalid"}
		}
		action.activityPolicy = policy
	}
	action.operation, _ = command.Input["operation"].(string)
	key := ""
	switch action.operation {
	case "assign_agent":
		key = "assignee_id"
	case "assign_team":
		key = "team_id"
	case "add_label":
		key = "label_id"
	case "remove_label":
		value, exists := command.Input["label_id"]
		id, err := optionalUint(value)
		if !exists || value == nil || err != nil {
			return 0, action, &ContractFailure{Type: "validation_failed", Message: "frozen label_id is required"}
		}
		if id != nil {
			action.id = *id
		}
		if action.id == 0 && action.activityPolicy != "" {
			// Zero is only the frozen missing-label resolution sentinel. It is not
			// a general label ID or a way to turn malformed definitions into touches.
			label, ok := command.Input["missing_label"].(string)
			if !ok || label == "" || strings.TrimSpace(label) != label || len(label) > 100 {
				return 0, action, &ContractFailure{Type: "validation_failed", Message: "frozen missing label target is required"}
			}
		}
	case "remove_assigned_agent", "remove_assigned_team", "mute_conversation":
	case "snooze_conversation":
		raw, ok := command.Input["snoozed_until"].(string)
		until, err := time.Parse(time.RFC3339Nano, raw)
		// Compare with the frozen request time, not retry time. A late recovery must
		// never turn a frozen snooze duration into a new relative deadline.
		if !ok || err != nil || command.RequestedAt.IsZero() || !until.After(command.RequestedAt) {
			return 0, action, &ContractFailure{Type: "validation_failed", Message: "frozen future snoozed_until is required"}
		}
		until = until.UTC()
		action.until = &until
	default:
		return 0, action, &ContractFailure{Type: "validation_failed", Message: "automation conversation operation is invalid"}
	}
	if key != "" {
		id, err := uintFilter(command.Input, key)
		if err != nil {
			return 0, action, &ContractFailure{Type: "validation_failed", Message: key + " is required"}
		}
		action.id = id
	}
	return conversationID, action, nil
}

func (r *Runtime) registerAutomationConversationActions() error {
	if err := r.Contracts.RegisterCommand("con.automation.apply.v1", r.applyAutomationConversationAction); err != nil {
		return err
	}
	if err := r.Contracts.RegisterCommandPreflight("con.automation.apply.v1", r.preflightAutomationConversationAction); err != nil {
		return err
	}
	if err := r.Contracts.RegisterCommand("con.automation.message.prepare.v1", r.prepareAutomationMessage); err != nil {
		return err
	}
	return r.Contracts.RegisterQuery("con.automation.message_authorization.get.v1", r.getAutomationMessageAuthorization)
}

func (r *Runtime) preflightAutomationConversationAction(ctx context.Context, command foundation.CommandEnvelope) (context.Context, error) {
	_, action, err := parseAutomationConversationAction(command)
	if err != nil {
		return ctx, err
	}
	switch action.operation {
	case "assign_agent", "assign_team":
		dependency, ok := r.Config.Dependencies["TEN"]
		if !ok {
			return ctx, &ContractFailure{Type: "dependency_unavailable", Message: "assignment validation is unavailable"}
		}
		client := newModuleClient("CON", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		if action.operation == "assign_agent" {
			query := foundation.QueryEnvelope{QueryType: "ten.permissions.check.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"user_id": strconv.FormatUint(uint64(action.id), 10)}, Sort: []string{"user_id"}, Page: "1", Fields: []string{"account_id", "user_id", "allowed"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
			var member membershipCheckView
			if err := client.query(ctx, query, &member); err != nil {
				return ctx, err
			}
			if member.AccountID != command.AccountID || member.UserID != action.id || !member.Allowed {
				return ctx, &ContractFailure{Type: "resource_not_found", Message: "account member was not found"}
			}
		} else {
			query := foundation.QueryEnvelope{QueryType: "ten.permissions.check.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"team_id": strconv.FormatUint(uint64(action.id), 10)}, Sort: []string{"team_id"}, Page: "1", Fields: []string{"account_id", "team_id", "allowed"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
			var team teamView
			if err := client.query(ctx, query, &team); err != nil {
				return ctx, err
			}
			if team.AccountID != command.AccountID || team.TeamID != action.id || !team.Allowed {
				return ctx, &ContractFailure{Type: "resource_not_found", Message: "team was not found"}
			}
		}
	case "add_label", "remove_label":
		if action.id == 0 {
			break
		}
		dependency, ok := r.Config.Dependencies["CUS"]
		if !ok {
			return ctx, &ContractFailure{Type: "dependency_unavailable", Message: "label validation is unavailable"}
		}
		query := foundation.QueryEnvelope{QueryType: "cus.label.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": strconv.FormatUint(uint64(action.id), 10)}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
		var label labelView
		if err := newModuleClient("CON", dependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &label); err != nil {
			return ctx, err
		}
		if label.ID != action.id || label.AccountID != command.AccountID {
			return ctx, &ContractFailure{Type: "resource_not_found", Message: "label was not found"}
		}
	}
	digest, err := commandDigest(command)
	if err != nil {
		return ctx, &ContractFailure{Type: "validation_failed", Message: "automation command could not be normalized"}
	}
	return context.WithValue(ctx, automationOwnerValidationKey{}, digest), nil
}

func loadAutomationConversation(tx *gorm.DB, command foundation.CommandEnvelope, conversationID uint) (*domain.Conversation, error) {
	if command.Target.ExpectedVersion == 0 {
		return nil, &ContractFailure{Type: "validation_failed", Message: "expected conversation version is required"}
	}
	var conversation domain.Conversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ? AND status <> ?", command.AccountID, conversationID, domain.ConversationStatusDeleting).First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
	}
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
	}
	if conversation.ObjectVersion < 1 || uint64(conversation.ObjectVersion) != command.Target.ExpectedVersion {
		return nil, &ContractFailure{Type: "state_conflict", Message: "conversation version changed"}
	}
	return &conversation, nil
}

func (r *Runtime) applyAutomationConversationAction(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
	conversationID, action, err := parseAutomationConversationAction(command)
	if err != nil {
		return nil, err
	}
	digest, digestErr := commandDigest(command)
	if proof, ok := ctx.Value(automationOwnerValidationKey{}).(string); !ok || digestErr != nil || proof != digest {
		return nil, &ContractFailure{Type: "permission_denied", Message: "automation owner validation is required"}
	}
	tx := Transaction(ctx, r.DB)
	conversation, err := loadAutomationConversation(tx, command, conversationID)
	if err != nil {
		return nil, err
	}
	statusBefore := conversation.Status
	updates := map[string]any{}
	changed := []string{}
	switch action.operation {
	case "assign_agent":
		if conversation.AssigneeID == nil || *conversation.AssigneeID != action.id {
			updates["assignee_id"] = action.id
			changed = append(changed, "assignee_id")
		}
	case "remove_assigned_agent":
		if conversation.AssigneeID != nil {
			updates["assignee_id"] = nil
			changed = append(changed, "assignee_id")
		}
	case "assign_team":
		if conversation.TeamID == nil || *conversation.TeamID != action.id {
			updates["team_id"] = action.id
			changed = append(changed, "team_id")
		}
	case "remove_assigned_team":
		if conversation.TeamID != nil {
			updates["team_id"] = nil
			changed = append(changed, "team_id")
		}
	case "mute_conversation":
		if !conversation.Muted {
			updates["muted"] = true
			changed = append(changed, "muted")
		}
		if conversation.Status != domain.ConversationStatusResolved {
			updates["status"] = domain.ConversationStatusResolved
			changed = append(changed, "status")
		}
		if conversation.SnoozedUntil != nil {
			updates["snoozed_until"] = nil
			changed = append(changed, "snoozed_until")
		}
	case "snooze_conversation":
		if conversation.Status != domain.ConversationStatusSnoozed {
			updates["status"] = domain.ConversationStatusSnoozed
			changed = append(changed, "status")
		}
		if conversation.SnoozedUntil == nil || !conversation.SnoozedUntil.Equal(*action.until) {
			updates["snoozed_until"] = *action.until
			changed = append(changed, "snoozed_until")
		}
	case "add_label", "remove_label":
		if action.id != 0 {
			var count int64
			if err := tx.Model(&domain.ConversationLabel{}).Where("conversation_id = ? AND label_id = ?", conversationID, action.id).Count(&count).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation labels are unavailable"}
			}
			if (action.operation == "add_label" && count == 0) || (action.operation == "remove_label" && count != 0) {
				changed = append(changed, "label_ids")
			}
		}
	}
	activityOnly := len(changed) == 0
	if activityOnly && action.activityPolicy == "" {
		return conversationFromModel(tx, conversation)
	}
	now := time.Now().UTC()
	if activityOnly {
		// Preserve old mutation behavior. A new activity-only effect advances
		// observably even under clock skew, at PostgreSQL timestamp precision.
		now = now.Truncate(time.Microsecond)
		if !now.After(conversation.LastActivityAt) {
			now = conversation.LastActivityAt.Truncate(time.Microsecond).Add(time.Microsecond)
		}
	}
	updates["object_version"], updates["last_activity_at"], updates["updated_at"] = gorm.Expr("object_version + 1"), now, now
	result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND object_version = ? AND status <> ?", command.AccountID, conversationID, command.Target.ExpectedVersion, domain.ConversationStatusDeleting).Updates(updates)
	if result.Error != nil || result.RowsAffected != 1 {
		return nil, &ContractFailure{Type: "state_conflict", Message: "conversation version changed or target is unavailable"}
	}
	if !activityOnly && action.operation == "add_label" {
		if err := tx.Create(&domain.ConversationLabel{ConversationID: conversationID, LabelID: action.id}).Error; err != nil {
			return nil, &ContractFailure{Type: "state_conflict", Message: "conversation label could not be attached"}
		}
	} else if !activityOnly && action.operation == "remove_label" {
		if err := tx.Where("conversation_id = ? AND label_id = ?", conversationID, action.id).Delete(&domain.ConversationLabel{}).Error; err != nil {
			return nil, &ContractFailure{Type: "state_conflict", Message: "conversation label could not be removed"}
		}
	}
	if err := tx.Where("account_id = ? AND id = ?", command.AccountID, conversationID).First(conversation).Error; err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
	}
	if activityOnly {
		// Legacy TouchActivity emitted no automation trigger. The command receipt
		// owns the activity/version effect and this marker; no phantom cascade is
		// created and retries return that exact committed result.
		view, err := conversationFromModel(tx, conversation)
		if err != nil {
			return nil, err
		}
		return &automationConversationActivityView{conversationView: *view, ActivityOnly: true}, nil
	}
	payload := map[string]any{"conversation_id": conversationID, "object_version": conversation.ObjectVersion, "operation": action.operation, "changed_fields": changed}
	if statusBefore != conversation.Status {
		payload["status_before"], payload["status_after"] = statusBefore, conversation.Status
	}
	if err := r.appendBusinessEvent(ctx, command, "con.conversation.changed.v1", "Conversation", command.Target.ID, changed, payload); err != nil {
		return nil, err
	}
	return conversationFromModel(tx, conversation)
}

func (r *Runtime) prepareAutomationMessage(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
	origin, depth, err := automationOwnerProvenance(command)
	if err != nil {
		return nil, err
	}
	conversationID, failure := conversationCommandTarget(command)
	if failure != nil {
		return nil, failure
	}
	content, ok := command.Input["content"].(string)
	private, privateOK := command.Input["private"].(bool)
	if !ok || strings.TrimSpace(content) == "" || len(content) > 100000 || !privateOK || command.CommandID == "" {
		return nil, &ContractFailure{Type: "validation_failed", Message: "automation message content and visibility are required"}
	}
	tx := Transaction(ctx, r.DB)
	conversation, err := loadAutomationConversation(tx, command, conversationID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND object_version = ? AND status <> ?", command.AccountID, conversationID, command.Target.ExpectedVersion, domain.ConversationStatusDeleting).Updates(map[string]any{"object_version": gorm.Expr("object_version + 1"), "last_activity_at": now, "updated_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		return nil, &ContractFailure{Type: "state_conflict", Message: "conversation version changed or target is unavailable"}
	}
	hash := sha256.Sum256([]byte(content))
	grant := automationMessageAuthorizationView{PrepareCommandID: command.CommandID, AccountID: command.AccountID, ConversationID: conversationID, ObjectVersion: conversation.ObjectVersion + 1, ContentHash: hex.EncodeToString(hash[:]), Private: private, OriginExecutionID: origin, AutomationDepth: depth, ActorID: command.Actor.ID}
	raw, err := json.Marshal(grant)
	if err != nil {
		return nil, err
	}
	grantHash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", command.AccountID, command.CommandID)))
	journal := domain.LocalChangeJournal{ChangeID: "aut-msg-" + hex.EncodeToString(grantHash[:])[:48], SourceModule: "CON", AccountID: command.AccountID, ObjectType: "Conversation", ObjectID: conversationID, ObjectDisplayID: command.Target.ID, ObjectVersionBefore: conversation.ObjectVersion, ObjectVersionAfter: grant.ObjectVersion, Action: "automation_message_authorization", AfterState: string(raw), ActorType: command.Actor.Type, SourceType: "automation", SourceID: command.Actor.ID, CommandID: command.CommandID, CorrelationID: command.CorrelationID, CausationID: command.CausationID, OccurredAt: now, RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "applied", Status: "published", DataClassification: domain.ClassificationInternal, RetentionClass: domain.RetentionBusinessChange}
	if err := tx.Create(&journal).Error; err != nil {
		return nil, &ContractFailure{Type: "state_conflict", Message: "automation message authorization could not be recorded"}
	}
	conversation.ObjectVersion, conversation.LastActivityAt, conversation.UpdatedAt = grant.ObjectVersion, now, now
	return conversationFromModel(tx, conversation)
}

func (r *Runtime) getAutomationMessageAuthorization(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
	if query.AccountID == 0 || !validAutomationOwnerActor(query.Actor) {
		return nil, &ContractFailure{Type: "permission_denied", Message: "automation owner actor is required"}
	}
	prepareID, ok := query.Filter["prepare_command_id"].(string)
	if !ok || strings.TrimSpace(prepareID) == "" {
		return nil, &ContractFailure{Type: "validation_failed", Message: "prepare_command_id is required"}
	}
	var journal domain.LocalChangeJournal
	err := r.DB.WithContext(ctx).Where("source_module = ? AND account_id = ? AND action = ? AND command_id = ?", "CON", query.AccountID, "automation_message_authorization", prepareID).First(&journal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &ContractFailure{Type: "resource_not_found", Message: "automation message authorization was not found"}
	}
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "automation message authorization is unavailable"}
	}
	var grant automationMessageAuthorizationView
	if json.Unmarshal([]byte(journal.AfterState), &grant) != nil || grant.PrepareCommandID != prepareID || grant.AccountID != query.AccountID || grant.ConversationID == 0 || grant.ConversationID != journal.ObjectID || grant.ObjectVersion != journal.ObjectVersionAfter || grant.ObjectVersion < 2 || grant.ActorID != query.Actor.ID || grant.ActorID != journal.SourceID || grant.OriginExecutionID == 0 || grant.AutomationDepth == 0 || grant.AutomationDepth > automationMaxDepth || len(grant.ContentHash) != sha256.Size*2 {
		return nil, &ContractFailure{Type: "state_conflict", Message: "automation message authorization is invalid"}
	}
	if _, err := hex.DecodeString(grant.ContentHash); err != nil {
		return nil, &ContractFailure{Type: "state_conflict", Message: "automation message authorization is invalid"}
	}
	if _, exists := query.Filter["conversation_id"]; exists {
		id, err := uintFilter(query.Filter, "conversation_id")
		if err != nil || id != grant.ConversationID {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "automation message authorization was not found"}
		}
	}
	var conversation domain.Conversation
	err = r.DB.WithContext(ctx).Where("account_id = ? AND id = ? AND status <> ?", query.AccountID, grant.ConversationID, domain.ConversationStatusDeleting).First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
	}
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
	}
	if conversation.ObjectVersion != grant.ObjectVersion {
		return nil, &ContractFailure{Type: "state_conflict", Message: "conversation version changed after message preparation"}
	}
	return grant, nil
}

// A message saved under CON's preparation grant already applied its one CON
// activity/version transition. A verified delivery records only the event inbox
// receipt; never apply that activity again or overwrite intervening manual work.
func (r *Runtime) consumePreparedAutomationMessageActivity(ctx context.Context, event foundation.EventEnvelope, conversationID uint) (bool, error) {
	marker, exists := event.Payload["conversation_activity_prepared"]
	if !exists {
		return false, nil
	}
	id, ok := marker.(string)
	if !ok || id == "" || !validAutomationOwnerActor(event.Actor) {
		return true, &ContractFailure{Type: "validation_failed", Message: "prepared message activity marker is invalid"}
	}
	var row domain.LocalChangeJournal
	if err := Transaction(ctx, r.DB).Where("source_module = ? AND account_id = ? AND action = ? AND command_id = ?", "CON", event.AccountID, "automation_message_authorization", id).First(&row).Error; err != nil {
		return true, &ContractFailure{Type: "state_conflict", Message: "prepared message activity grant is unavailable"}
	}
	var grant automationMessageAuthorizationView
	origin, e1 := uintFilter(event.Payload, "origin_execution_id")
	depth, e2 := uintFilter(event.Payload, "automation_depth")
	version, e3 := uintFilter(event.Payload, "object_version")
	messageID, e4 := uintFilter(event.Payload, "message_id")
	private, privateOK := event.Payload["private"].(bool)
	expectedStatus := "queued"
	if private {
		expectedStatus = "saved"
	}
	senderID, senderErr := optionalUint(event.Payload["sender_id"])
	if json.Unmarshal([]byte(row.AfterState), &grant) != nil || grant.PrepareCommandID != id || grant.AccountID != event.AccountID || grant.ConversationID != conversationID || grant.ActorID != event.Actor.ID || e1 != nil || e2 != nil || e3 != nil || e4 != nil || event.AggregateID != strconv.Itoa(int(messageID)) || grant.OriginExecutionID != origin || grant.AutomationDepth != depth || grant.ObjectVersion != int(version) || grant.ContentHash != event.Payload["content_hash"] || !privateOK || grant.Private != private || event.Payload["message_type"] != domain.MessageTypeOutgoing || event.Payload["sender_type"] != domain.SenderTypeUser || senderErr != nil || senderID != nil || event.Payload["status"] != expectedStatus || event.SourceModule != "MSG" || event.EventType != "msg.message.created.v1" || row.ObjectID != conversationID || row.ObjectVersionAfter != grant.ObjectVersion || row.Result != "applied" || row.Status != "published" {
		return true, &ContractFailure{Type: "state_conflict", Message: "prepared message activity does not match its CON grant"}
	}
	return true, nil
}
