package modruntime

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// inspectAutomationCompatibilityActions is a read-only projection of the actual
// continuing compiler. It never creates an envelope or calls a runtime/owner.
// The caller bounds source bytes and JSON depth before entering this function.
func inspectAutomationCompatibilityActions(raw string, out *domain.AutomationCompatibilitySource, profile domain.AutomationCompatibilityProfile) {
	definitions, valid := parseAutomationActionDefinitions(raw)
	plan, failure := compileAutomationContinuingActions(raw)
	out.LogicalActions = len(definitions)
	out.AttemptedSteps = 0
	out.CompiledSteps = len(plan)
	if !valid || failure != "" {
		code, category := "invalid_action_definition", "invalid"
		if valid && len(definitions) > 100 {
			code, category = "action_definition_limit_exceeded", "limit"
		}
		issue := compatIssue(out, code, category, "blocker", "/actions", "admission_failed_if_matched", "invalid_frozen_definition")
		if valid {
			issue.Details.Count, issue.Details.Limit, issue.Details.Unit = len(definitions), 100, "logical_actions"
		}
		return
	}
	compatEnvelopeIssues(raw, "actions", out)
	if len(definitions) == 0 {
		compatIssue(out, "empty_action_history_difference", "behavior_change", "notice", "/actions", "not_assessed", "")
		return
	}

	failed := make(map[int]string)
	for _, action := range plan {
		if action.FailureReason != "" && action.LogicalIndex != nil {
			failed[*action.LogicalIndex] = action.FailureReason
		}
	}
	for i, definition := range definitions {
		// Compiling one parsed definition is exactly how the continuation compiler
		// establishes the candidate plan before enforcing its reserve budget.
		encoded, _ := json.Marshal([]automationActionDefinition{definition})
		candidate, candidateFailure := compileAutomationActions(string(encoded))
		attempted := len(candidate)
		if candidateFailure != "" {
			attempted = 1 // The attempted local validation step, not an owner call.
		}
		out.AttemptedSteps += attempted
		if reason := failed[i]; reason != "" {
			out.FailedLogicalIndexes = append(out.FailedLogicalIndexes, i)
			code, category := "invalid_action_parameters", "invalid"
			if reason == "action_capability_executor_unavailable" {
				code, category = "unsupported_action", "unsupported"
			} else if candidateFailure == "" {
				code, category = "action_step_limit_exceeded", "limit"
			}
			issue := compatActionIssue(out, i, "", code, category, "blocker", "logical_action_failed_if_reached", reason)
			if code == "action_step_limit_exceeded" {
				issue.Details.Count, issue.Details.Limit, issue.Details.Unit = len(candidate), automationMaxSteps, "owner_steps"
			}
		}
		if definition.CapabilityVersion != "" && definition.CapabilityVersion != "v1" {
			compatActionIssue(out, i, "capability_version", "unsupported_action_version", "unsupported", "review", "logical_action_failed_if_reached", "")
		}
		if definition.CapabilityID != "" && definition.ActionName != "" {
			compatActionIssue(out, i, "capability_id", "conflicting_action_capability", "ambiguity", "review", "logical_action_failed_if_reached", "")
		}

		if definition.CapabilityVersion != "" && definition.CapabilityVersion != "v1" {
			continue
		}
		var param any
		if len(definition.ActionParams) > 0 && decodeAutomationActionParams(definition.ActionParams, &param) != nil {
			continue
		}
		// Names below are allowlisted only for diagnostics. Compiler results above
		// remain authoritative, including unknown capability/version failures.
		name := compatDiagnosticActionName(definition)
		compatInspectActionParams(out, i, name, param)
		if failed[i] == "" {
			compatInspectActionOwners(out, i, candidate, profile)
		}
	}
}

func compatActionIssue(out *domain.AutomationCompatibilitySource, index int, field, code, category, severity, effect, reason string) *domain.AutomationCompatibilityIssue {
	issue := compatIssue(out, code, category, severity, compatActionPath(index, field), effect, reason)
	value := index
	issue.LogicalActionIndex = &value
	return issue
}

func compatDiagnosticActionName(d automationActionDefinition) string {
	if d.CapabilityID != "" {
		if d.ActionName != "" {
			return ""
		}
		switch d.CapabilityID {
		case "conversation.change_status":
			return "change_status"
		case "conversation.change_priority":
			return "change_priority"
		case "conversation.assign_agent":
			return "assign_agent"
		case "conversation.assign_team":
			return "assign_team"
		case "conversation.remove_assigned_agent":
			return "remove_assigned_agent"
		case "conversation.remove_assigned_team":
			return "remove_assigned_team"
		case "conversation.mute":
			return "mute_conversation"
		case "conversation.snooze":
			return "snooze"
		case "conversation.add_label":
			return "add_label"
		case "conversation.remove_label":
			return "remove_label"
		case "message.create_public":
			return "send_message"
		case "message.create_private_note":
			return "add_private_note"
		}
		return ""
	}
	switch d.ActionName {
	case "change_status", "resolve_conversation", "close_conversation", "close", "resolve", "open_conversation", "change_priority", "assign_agent", "assign_team", "remove_assigned_agent", "remove_assigned_team", "mute_conversation", "snooze_conversation", "snooze", "send_message", "send_reply", "reply", "add_private_note", "private_note", "add_label", "add_labels", "remove_label", "remove_labels":
		return d.ActionName
	}
	return ""
}

func compatInspectActionParams(out *domain.AutomationCompatibilitySource, index int, name string, param any) {
	issue := func(code, category, severity, effect string) *domain.AutomationCompatibilityIssue {
		return compatActionIssue(out, index, "action_params", code, category, severity, effect, "")
	}
	switch name {
	case "change_status", "open_conversation":
		compatInspectStringParams(out, index, param, "status")
		switch automationStringParam(param, "status") {
		case "":
			issue("default_applied_open", "default", "review", "default_applied")
		case "closed":
			issue("normalized_resolved", "default", "notice", "default_applied")
		}
	case "resolve_conversation", "close_conversation", "close", "resolve":
		issue("forced_resolved", "default", "notice", "default_applied")
		if param != nil {
			issue("ignored_action_parameters", "ambiguity", "review", "legacy_precedence")
		}
	case "change_priority":
		compatInspectStringParams(out, index, param, "priority")
	case "snooze_conversation", "snooze":
		compatInspectStringParams(out, index, param, "duration")
		duration, err := time.ParseDuration(automationStringParam(param, "duration"))
		if err != nil || duration <= 0 {
			issue("fallback_24h", "default", "review", "default_applied")
		}
	case "send_message", "send_reply", "reply", "add_private_note", "private_note":
		compatInspectStringParams(out, index, param, "message", "content")
		value := automationStringParam(param, "message", "content")
		if len(value) > 100000 {
			detail := issue("message_content_limit_exceeded", "limit", "review", "logical_action_failed_if_reached")
			detail.Details.Count, detail.Details.Limit, detail.Details.Unit = len(value), 100000, "bytes"
		}
	case "assign_agent":
		compatInspectIDParams(out, index, param, "user_id", "agent_id")
	case "assign_team":
		compatInspectIDParams(out, index, param, "team_id", "team_ids", "id")
	case "add_label", "add_labels", "remove_label", "remove_labels":
		compatInspectLabelParams(out, index, param, name == "remove_label" || name == "remove_labels")
	case "remove_assigned_agent", "remove_assigned_team", "mute_conversation":
		if param != nil {
			issue("ignored_action_parameters", "ambiguity", "review", "legacy_precedence")
		}
	}
}

func compatIgnoredParams(out *domain.AutomationCompatibilitySource, index, count int) {
	if count > 0 {
		issue := compatActionIssue(out, index, "action_params", "ignored_extra_action_parameters", "ambiguity", "review", "legacy_precedence", "")
		issue.Details.Count, issue.Details.Unit = count, "parameters"
	}
}

func compatParameterAliasIssue(out *domain.AutomationCompatibilitySource, index int, conflict bool) {
	code, severity := "redundant_parameter_alias", "notice"
	if conflict {
		code, severity = "conflicting_parameter_alias", "review"
	}
	compatActionIssue(out, index, "action_params", code, "ambiguity", severity, "legacy_precedence", "")
}

func compatInspectStringParams(out *domain.AutomationCompatibilitySource, index int, param any, keys ...string) {
	switch value := param.(type) {
	case []any:
		compatIgnoredParams(out, index, len(value)-1)
	case map[string]any:
		selected, present, conflict := "", 0, false
		for _, key := range keys {
			if token, ok := value[key].(string); ok && token != "" {
				if present == 0 {
					selected = token
				} else if token != selected {
					conflict = true
				}
				present++
			}
		}
		if present > 1 {
			compatParameterAliasIssue(out, index, conflict)
		}
		if present > 0 {
			compatIgnoredParams(out, index, len(value)-1)
		} else if len(value) == 1 {
			for key, token := range value {
				known := false
				for _, allowed := range keys {
					known = known || key == allowed
				}
				if _, ok := token.(string); ok && !known {
					compatActionIssue(out, index, "action_params", "singleton_object_fallback", "ambiguity", "review", "legacy_precedence", "")
				}
			}
		}
	}
}

func compatInspectIDParams(out *domain.AutomationCompatibilitySource, index int, param any, keys ...string) {
	selected := param
	if object, ok := param.(map[string]any); ok {
		var first uint
		present, conflict := 0, false
		selected = nil
		for _, key := range keys {
			if candidate, exists := object[key]; exists {
				// Include a platform-sensitive candidate even on the architecture
				// where the current compiler rejects or truncates it.
				compatInspectIDPlatform(out, index, candidate)
				if id := automationIDParam(candidate); id > 0 {
					if present == 0 {
						first, selected = id, candidate
					} else if id != first {
						conflict = true
					}
					present++
				}
			}
		}
		if present > 1 {
			compatParameterAliasIssue(out, index, conflict)
		}
		if present > 0 {
			compatIgnoredParams(out, index, len(object)-1)
		}
	} else {
		compatInspectIDPlatform(out, index, selected)
	}
	ignored := 0
	for {
		array, ok := selected.([]any)
		if !ok || len(array) == 0 {
			break
		}
		ignored += len(array) - 1
		selected = array[0]
	}
	compatIgnoredParams(out, index, ignored)
}

func compatInspectIDPlatform(out *domain.AutomationCompatibilitySource, index int, param any) {
	for {
		array, ok := param.([]any)
		if !ok || len(array) == 0 {
			break
		}
		param = array[0]
	}
	var integer uint64
	switch value := param.(type) {
	case json.Number:
		// Reuse the runtime's bounded, exact parser. Inspect uint64 values so
		// platform coverage remains visible even when the host uint is 32-bit.
		integer, _ = parseAutomationNumberUint64(value)
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err == nil && parsed > 0 {
			integer = parsed
		}
	}
	if integer > 1<<32-1 {
		issue := compatActionIssue(out, index, "action_params", "platform_dependent_identity_range", "coverage", "review", "not_assessed", "")
		issue.Details.Limit, issue.Details.Unit = strconv.IntSize, "bits"
	}
}

func compatInspectLabelParams(out *domain.AutomationCompatibilitySource, index int, param any, remove bool) {
	selected := param
	if object, ok := param.(map[string]any); ok {
		keys := []string{"labels", "label"}
		if remove {
			keys = append(keys, "remove")
		}
		present, conflict := 0, false
		selected = nil
		for _, key := range keys {
			if token, exists := object[key]; exists {
				if present == 0 {
					selected = token
				} else if !reflect.DeepEqual(selected, token) {
					conflict = true
				}
				present++
			}
		}
		if present > 1 {
			compatParameterAliasIssue(out, index, conflict)
		}
		if present > 0 {
			compatIgnoredParams(out, index, len(object)-1)
		}
	}
	values := []any{selected}
	if array, ok := selected.([]any); ok {
		values = array
	}
	trimmed, omitted, duplicates, numeric, maxBytes := 0, 0, 0, 0, 0
	seen := make(map[string]bool)
labelValues:
	for _, value := range values {
		if _, numeric := value.(json.Number); numeric {
			compatInspectIDPlatform(out, index, value)
		}
		var label string
		switch token := value.(type) {
		case string:
			label = token
		case json.Number:
			id := automationIDParam(token)
			if id == 0 {
				break labelValues
			}
			label = strconv.FormatUint(uint64(id), 10)
		default:
			break labelValues
		}
		clean := strings.TrimSpace(label)
		if clean != label {
			trimmed++
		}
		if clean == "" {
			omitted++
			continue
		}
		if len(clean) > maxBytes {
			maxBytes = len(clean)
		}
		if len(clean) > 100 {
			break
		}
		if seen[clean] {
			duplicates++
			continue
		}
		seen[clean] = true
		if id, err := strconv.ParseUint(clean, 10, 64); err == nil && id > 0 {
			numeric++
		}
	}
	for _, observation := range []struct {
		code  string
		count int
	}{{"label_whitespace_trimmed", trimmed}, {"empty_labels_ignored", omitted}, {"duplicate_labels_ignored", duplicates}} {
		if observation.count > 0 {
			issue := compatActionIssue(out, index, "action_params", observation.code, "default", "notice", "legacy_precedence", "")
			issue.Details.Count, issue.Details.Unit = observation.count, "labels"
		}
	}
	if maxBytes > 100 {
		issue := compatActionIssue(out, index, "action_params", "label_token_limit_exceeded", "limit", "review", "logical_action_failed_if_reached", "")
		issue.Details.Count, issue.Details.Limit, issue.Details.Unit = maxBytes, 100, "bytes"
	}
	if labels, ok := automationLabelParams(param, remove); ok && len(labels) > 100 {
		issue := compatActionIssue(out, index, "action_params", "label_count_limit_exceeded", "limit", "review", "logical_action_failed_if_reached", "")
		issue.Details.Count, issue.Details.Limit, issue.Details.Unit = len(labels), 100, "labels"
	}
	if numeric > 0 {
		code := "numeric_label_used_as_title"
		if remove {
			code = "numeric_label_used_as_id"
		}
		issue := compatActionIssue(out, index, "action_params", code, "ambiguity", "review", "legacy_precedence", "")
		issue.Details.Count, issue.Details.Unit = numeric, "labels"
	}
}

func compatInspectActionOwners(out *domain.AutomationCompatibilitySource, index int, candidate []automationLocalAction, profile domain.AutomationCompatibilityProfile) {
	// The maximum encoded value is a lower bound for an individual command,
	// never a sum across separately dispatched commands. A CON label apply uses
	// the resolved ID, so its label text is not an unconditional lower bound.
	lowerBounds := make(map[string]int)
	for _, action := range candidate {
		owner := automationActionOwner(action)
		compatAddOwner(out, owner)
		if action.Operation == "assign_agent" || action.Operation == "assign_team" {
			compatAddOwner(out, "TEN")
		}
		if _, exists := lowerBounds[owner]; !exists {
			lowerBounds[owner] = 0
		}
		hasValue := action.Command == "cus.automation.label.resolve.v1" || action.Command == "con.automation.message.prepare.v1" || action.Command == "msg.automation.message.create.v1" || action.Command == "con.status.change.v1" || action.Command == "con.priority.change.v1" || action.Operation == "assign_agent" || action.Operation == "assign_team"
		if hasValue {
			encoded, _ := json.Marshal(action.Value)
			if len(encoded) > lowerBounds[owner] {
				lowerBounds[owner] = len(encoded)
			}
		}
	}
	for _, owner := range []string{"CON", "CUS", "MSG"} {
		lower, exists := lowerBounds[owner]
		if !exists {
			continue
		}
		capBytes := profile.RequestCapBytes[owner]
		code, severity := "owner_request_size_fit_unknown", "unknown"
		if profile.Assessed && capBytes > 0 && lower > capBytes {
			code, severity = "owner_request_size_lower_bound_exceeded", "blocker"
		}
		issue := compatActionIssue(out, index, "action_params", code, "configuration", severity, "not_assessed", "")
		issue.Details.Owner = owner
		if profile.Assessed && capBytes > 0 {
			issue.Details.Count, issue.Details.Limit, issue.Details.Unit = lower, capBytes, "bytes"
		}
	}
}
