package modruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/google/uuid"
)

const automationRetryProfile = "msg.retry_attempt.v1"

// This compatibility view is admitted only through the trusted MSG owner port,
// after it has verified both the accepted attempt and the CON activity receipt.
// A provider status event or the standalone retry command is not a creation fact.
func automationRetryFact(event foundation.EventEnvelope) bool {
	return event.SourceModule == "MSG" && event.EventType == "msg.message_status.changed.v1"
}

func validateAutomationRetryFact(event foundation.EventEnvelope) error {
	invalid := errors.New("invalid verified automation retry fact")
	attempt, ok := event.Payload["retry_attempt_id"].(string)
	sequence, sequenceErr := uintFilter(event.Payload, "retry_sequence")
	message, messageErr := uintFilter(event.Payload, "message_id")
	_, conversationErr := uintFilter(event.Payload, "conversation_id")
	version, versionErr := uintFilter(event.Payload, "object_version")
	expected, expectedErr := uintFilter(event.Payload, "retry_expected_version")
	status, statusOK := event.Payload["retry_observed_status"].(string)
	private, privateOK := event.Payload["private"].(bool)
	messageType := event.Payload["message_type"]
	outcome := domain.MessageStatusSaved
	if !private && messageType == domain.MessageTypeOutgoing {
		outcome = domain.MessageStatusQueued
	}
	activityResult := event.Payload["retry_activity_result"]
	_, attemptErr := uuid.Parse(attempt)
	identity := sha256.Sum256([]byte("msg-retry:" + attempt))
	if !automationRetryFact(event) || event.EventVersion != "v1" || event.Payload["operation"] != "retry" || event.Payload["retry_profile"] != automationRetryProfile || event.Payload["retry_receipt_verified"] != true {
		return invalid
	}
	if !ok || attemptErr != nil || attempt != event.CausationID || event.EventID != "msg-retry-"+hex.EncodeToString(identity[:])[:40] || sequenceErr != nil || uint64(sequence) != event.AggregateVersion {
		return invalid
	}
	if messageErr != nil || conversationErr != nil || versionErr != nil || expectedErr != nil || event.AggregateType != "Message" || event.AggregateID != strconv.FormatUint(uint64(message), 10) || !statusOK || !automationRetryStatus(status) {
		return invalid
	}
	if !privateOK || event.Payload["local_outcome"] != outcome || event.Payload["status"] != outcome || (messageType != domain.MessageTypeIncoming && messageType != domain.MessageTypeOutgoing && messageType != domain.MessageTypeActivity) || (activityResult != "applied" && activityResult != "stale") {
		return invalid
	}
	if (activityResult == "applied" && (expected == ^uint(0) || version != expected+1)) || (activityResult == "stale" && version != expected) {
		return invalid
	}
	// Explicit retries are root business attempts, never recursive AUT actions.
	for _, key := range []string{"origin_execution_id", "automation_depth", "origin_object_version"} {
		if _, exists := event.Payload[key]; exists {
			return invalid
		}
	}
	before, hasBefore := event.Payload["status_before"]
	after, hasAfter := event.Payload["status_after"]
	if hasBefore || hasAfter {
		if !hasBefore || !hasAfter || before != domain.ConversationStatusResolved || after != domain.ConversationStatusOpen || status != domain.ConversationStatusResolved || messageType != domain.MessageTypeIncoming || activityResult != "applied" {
			return invalid
		}
	}
	return nil
}

func automationRetryStatus(status string) bool {
	return status == domain.ConversationStatusOpen || status == domain.ConversationStatusPending || status == domain.ConversationStatusResolved || status == domain.ConversationStatusSnoozed
}

// Retry changes only status among the supported condition fields. Keep the
// before-status observation for the legacy message callback, while its target
// version and conversation aliases describe the committed owner activity.
func automationFactObservation(event foundation.EventEnvelope, eventName string, initial *automationConditionContext) *automationConditionContext {
	if initial == nil {
		return nil
	}
	observation := *initial
	if eventName != "message_created" {
		observation.Message = nil
	} else if automationRetryFact(event) {
		observation.Conversation.Status, _ = event.Payload["retry_observed_status"].(string)
	}
	return &observation
}

// Validate the frozen before/post distinction using AUT's admitted receipt only.
// The existing sibling codec already persists the complete initial observation;
// replay and snapshot restore must not reconstruct it from current owner state.
func validAutomationRetryExecution(event foundation.EventEnvelope, execution domain.AutomationRuleExecution) bool {
	if !automationRetryFact(event) {
		return true
	}
	if event.Payload["retry_activity_result"] == "stale" {
		return execution.Status == "failed" && execution.RunnerContext == "" && execution.RunnerEvaluation == "" && execution.RunnerNextAction == 0
	}
	if execution.RunnerEvaluation == "" {
		return true
	}
	var evaluation automationSiblingEvaluation
	if json.Unmarshal([]byte(execution.RunnerEvaluation), &evaluation) != nil {
		return false
	}
	status := event.Payload["retry_observed_status"]
	if execution.EventName != "message_created" {
		status = event.Payload["status_after"]
	}
	return evaluation.Initial.Conversation.Status == status
}
