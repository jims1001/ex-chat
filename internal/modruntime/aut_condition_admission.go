package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type automationPreparedRuleKey struct{}
type automationPreparedRule struct{ RuleID, Version uint }

// prepareAutomationExecution is only a read-side preparation. The command
// handler still serializes against the rule and verifies the selected version.
func (r *Runtime) prepareAutomationExecution(ctx context.Context, command foundation.CommandEnvelope) (context.Context, error) {
	failure := func(kind, message string) (context.Context, error) {
		return ctx, &ContractFailure{Type: kind, Message: message}
	}
	ruleID, err := strconv.ParseUint(command.Target.ID, 10, 64)
	if command.Target.Type != "rule" || err != nil || ruleID == 0 {
		return failure("validation_failed", "rule target is required")
	}
	conversationID, err := uintFilter(command.Input, "conversation_id")
	if err != nil {
		return failure("validation_failed", "conversation_id is required")
	}
	targetVersion, err := uintFilter(command.Input, "target_version")
	if err != nil {
		return failure("validation_failed", "target_version is required")
	}
	var rule domain.AutomationRule
	if err := r.DB.WithContext(ctx).Where("account_id = ? AND id = ?", command.AccountID, ruleID).First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return failure("resource_not_found", "rule was not found")
		}
		return failure("dependency_unavailable", "rule store is unavailable")
	}
	var version domain.AutomationRuleVersion
	q := r.DB.WithContext(ctx).Where("account_id = ? AND rule_id = ?", command.AccountID, ruleID)
	if _, exists := command.Input["rule_version"]; exists {
		value, err := uintFilter(command.Input, "rule_version")
		if err != nil {
			return failure("validation_failed", "rule_version is invalid")
		}
		q = q.Where("version = ?", value)
	}
	if err := q.Order("version DESC").First(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return failure("resource_not_found", "rule version was not found")
		}
		return failure("dependency_unavailable", "rule version store is unavailable")
	}
	// A semantic replay with a fresh command ID must also survive dependency loss.
	eventID, _ := command.Input["event_id"].(string)
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s:%d:%d", command.AccountID, ruleID, version.Version, eventID, conversationID, targetVersion)))
	var existing int64
	if err := r.DB.WithContext(ctx).Model(&domain.AutomationRuleExecution{}).Where("account_id = ? AND execution_key = ?", command.AccountID, hex.EncodeToString(hash[:])).Count(&existing).Error; err != nil {
		return failure("dependency_unavailable", "execution store is unavailable")
	}
	if existing > 0 {
		return ctx, nil
	}
	if !rule.Active {
		return failure("state_conflict", "rule is disabled")
	}
	eventName, _ := command.Input["event_name"].(string)
	if eventName != version.EventName {
		return failure("validation_failed", "event_name does not match frozen rule")
	}
	var latest uint
	if err := r.DB.WithContext(ctx).Model(&domain.AutomationRuleVersion{}).Where("account_id = ? AND rule_id = ?", command.AccountID, ruleID).Select("MAX(version)").Scan(&latest).Error; err != nil {
		return failure("dependency_unavailable", "rule version store is unavailable")
	}
	if latest != version.Version {
		return failure("state_conflict", "rule version is no longer active")
	}
	dep, ok := r.Config.Dependencies["CON"]
	if !ok {
		return failure("dependency_unavailable", "CON dependency is not configured")
	}
	query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id", "object_version", "status", "priority", "inbox_id", "contact_id", "assignee_id", "team_id", "label_ids"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	if attributes, _ := automationProfileAttributeNeeds(version.Conditions, version.ConditionSemantics); attributes {
		query.Fields = append(query.Fields, "custom_attributes")
	}
	var conversation conversationView
	if err := newModuleClient("AUT", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
		return ctx, err
	}
	if conversation.ID != conversationID || conversation.AccountID != command.AccountID || conversation.Status == domain.ConversationStatusDeleting {
		return failure("resource_not_found", "conversation was not found")
	}
	if conversation.ObjectVersion <= 0 || uint(conversation.ObjectVersion) != targetVersion {
		return failure("state_conflict", "target version is stale")
	}
	verified := automationVerifiedTarget{AccountID: command.AccountID, ConversationID: conversationID, Version: targetVersion}
	var message *messageView
	if eventName == "message_created" {
		id, err := uintFilter(command.Input, "message_id")
		if err != nil {
			return failure("validation_failed", "message_id is required for message_created")
		}
		dep, ok := r.Config.Dependencies["MSG"]
		if !ok {
			return failure("dependency_unavailable", "MSG dependency is not configured")
		}
		query.QueryType, query.Filter, query.Fields = "msg.message.get.v1", map[string]any{"id": id}, []string{"id", "account_id", "conversation_id", "content", "message_type", "private", "deleted"}
		message = &messageView{}
		if err := newModuleClient("AUT", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, message); err != nil {
			return ctx, err
		}
		if message.ID != id || message.AccountID != command.AccountID || message.ConversationID != conversationID || message.Deleted {
			return failure("resource_not_found", "message was not found in conversation")
		}
		verified.MessageID = &id
	} else if _, exists := command.Input["message_id"]; exists {
		return failure("validation_failed", "message_id is only valid for message_created")
	}
	observation, err := r.prepareAutomationConditionContext(ctx, command.Actor, conversation, message, []domain.AutomationRuleVersion{version})
	if err != nil {
		return ctx, err
	}
	ctx = context.WithValue(ctx, automationPreparedRuleKey{}, automationPreparedRule{RuleID: rule.ID, Version: version.Version})
	ctx = context.WithValue(ctx, automationVerifiedTargetKey{}, verified)
	return context.WithValue(ctx, automationConditionContextKey{}, observation), nil
}
