package modruntime

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

const automationMaxSteps = 1000

// Compile valid legacy action forms to a deterministic, bounded owner plan.
// Ambiguous object fallback and lossy numeric coercion are rejected deliberately.
func compileAutomationActions(raw string) ([]automationLocalAction, string) {
	definitions, valid := parseAutomationActionDefinitions(raw)
	if !valid || len(definitions) > 100 {
		return nil, "invalid_frozen_definition"
	}
	actions := make([]automationLocalAction, 0, len(definitions))
	for _, d := range definitions {
		if d.CapabilityVersion != "" && d.CapabilityVersion != "v1" {
			return nil, "invalid_action_parameters"
		}
		name := d.ActionName
		if d.CapabilityID != "" {
			if name != "" {
				return nil, "invalid_action_parameters"
			}
			names := map[string]string{"conversation.change_status": "change_status", "conversation.change_priority": "change_priority", "conversation.assign_agent": "assign_agent", "conversation.remove_assigned_agent": "remove_assigned_agent", "conversation.assign_team": "assign_team", "conversation.remove_assigned_team": "remove_assigned_team", "conversation.mute": "mute_conversation", "conversation.snooze": "snooze", "conversation.add_label": "add_label", "conversation.remove_label": "remove_label", "message.create_public": "send_message", "message.create_private_note": "add_private_note"}
			name = names[d.CapabilityID]
			if name == "" {
				return nil, "action_capability_executor_unavailable"
			}
		}
		var param any
		if len(d.ActionParams) > 0 && decodeAutomationActionParams(d.ActionParams, &param) != nil {
			return nil, "invalid_action_parameters"
		}
		a := automationLocalAction{}
		switch name {
		case "change_status", "resolve_conversation", "close_conversation", "close", "resolve", "open_conversation":
			a = automationLocalAction{Capability: "conversation.change_status", Command: "con.status.change.v1", Field: "status"}
			if name == "resolve_conversation" || name == "close_conversation" || name == "close" || name == "resolve" {
				a.Value = "resolved"
			} else {
				a.Value = automationStringParam(param, "status")
				if a.Value == "" {
					a.Value = "open"
				}
			}
			if a.Value == "closed" {
				a.Value = "resolved"
			}
			if a.Value != "open" && a.Value != "pending" && a.Value != "resolved" {
				return nil, "invalid_action_parameters"
			}
		case "change_priority":
			a = automationLocalAction{Capability: "conversation.change_priority", Command: "con.priority.change.v1", Field: "priority", Value: automationStringParam(param, "priority")}
			if a.Value != "low" && a.Value != "medium" && a.Value != "high" && a.Value != "urgent" {
				return nil, "invalid_action_parameters"
			}
		case "assign_agent", "assign_team":
			keys := []string{"user_id", "agent_id"}
			a.Field = "assignee_id"
			if name == "assign_team" {
				keys = []string{"team_id", "team_ids", "id"}
				a.Field = "team_id"
			}
			id := automationIDParam(param, keys...)
			if id == 0 {
				return nil, "invalid_action_parameters"
			}
			a.Capability = "conversation." + name
			a.Command = "con.automation.apply.v1"
			a.Operation = name
			a.Value = strconv.FormatUint(uint64(id), 10)
		case "remove_assigned_agent", "remove_assigned_team", "mute_conversation":
			a = automationLocalAction{Capability: "conversation." + name, Command: "con.automation.apply.v1", Operation: name}
		case "snooze_conversation", "snooze":
			duration := 24 * time.Hour
			if value := automationStringParam(param, "duration"); value != "" {
				if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
					duration = parsed
				}
			}
			a = automationLocalAction{Capability: "conversation.snooze", Command: "con.automation.apply.v1", Operation: "snooze_conversation", Field: "duration", Value: duration.String()}
		case "send_message", "send_reply", "reply", "add_private_note", "private_note":
			value := automationStringParam(param, "message", "content")
			if strings.TrimSpace(value) == "" || len(value) > 100000 {
				return nil, "invalid_action_parameters"
			}
			private := name == "add_private_note" || name == "private_note"
			capability := "message.create_public"
			if private {
				capability = "message.create_private_note"
			}
			actions = append(actions, automationLocalAction{Capability: capability, Command: "con.automation.message.prepare.v1", Field: "content", Value: value, Private: private})
			a = automationLocalAction{Capability: capability, Command: "msg.automation.message.create.v1", Owner: "MSG", Field: "content", Value: value, Private: private}
		case "add_label", "add_labels", "remove_label", "remove_labels":
			labels, ok := automationLabelParams(param, name == "remove_label" || name == "remove_labels")
			if !ok || len(labels) == 0 || len(labels) > 100 {
				return nil, "invalid_action_parameters"
			}
			operation := "add"
			if name == "remove_label" || name == "remove_labels" {
				operation = "remove"
			}
			for _, label := range labels {
				actions = append(actions, automationLocalAction{Capability: "conversation." + operation + "_label", Command: "cus.automation.label.resolve.v1", Owner: "CUS", Operation: operation, Field: "label", Value: label}, automationLocalAction{Capability: "conversation." + operation + "_label", Command: "con.automation.apply.v1", Operation: operation + "_label", Field: "label", Value: label})
			}
			if len(actions) > automationMaxSteps {
				return nil, "invalid_action_parameters"
			}
			continue
		default:
			return nil, "action_capability_executor_unavailable"
		}
		actions = append(actions, a)
		if len(actions) > automationMaxSteps {
			return nil, "invalid_action_parameters"
		}
	}
	return actions, ""
}

// New admissions preserve independent later actions without silently accepting
// unsupported capabilities. Unparseable whole definitions remain a bounded error.
func compileAutomationContinuingActions(raw string) ([]automationLocalAction, string) {
	definitions, valid := parseAutomationActionDefinitions(raw)
	if !valid || len(definitions) > 100 {
		return nil, "invalid_frozen_definition"
	}
	actions := make([]automationLocalAction, 0, len(definitions))
	for i, d := range definitions {
		encoded, _ := json.Marshal([]automationActionDefinition{d})
		compiled, failure := compileAutomationActions(string(encoded))
		if len(actions)+len(compiled)+len(definitions)-i-1 > automationMaxSteps {
			failure = "invalid_action_parameters"
		}
		if failure != "" {
			compiled = []automationLocalAction{{FailureReason: failure}}
		}
		for j := range compiled {
			index := i
			compiled[j].LogicalIndex = &index
		}
		actions = append(actions, compiled...)
	}
	return actions, ""
}

func compileAutomationRunnerActions(raw, policy string) ([]automationLocalAction, string) {
	if policy == automationContinueDefinite {
		return compileAutomationContinuingActions(raw)
	}
	if policy != "" {
		return nil, "invalid_frozen_definition"
	}
	return compileAutomationActions(raw)
}

func automationStringParam(p any, keys ...string) string {
	switch v := p.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	case map[string]any:
		for _, key := range keys {
			if s, ok := v[key].(string); ok && s != "" {
				return s
			}
		}
		if len(v) == 1 {
			for _, value := range v {
				if s, ok := value.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}
func automationIDParam(p any, keys ...string) uint {
	if number, ok := p.(json.Number); ok {
		parsed, valid := parseAutomationNumberUint64(number)
		if !valid || parsed > uint64(^uint(0)) {
			return 0
		}
		return uint(parsed)
	}
	if v, ok := p.(map[string]any); ok {
		for _, key := range keys {
			if value, exists := v[key]; exists {
				if id := automationIDParam(value); id > 0 {
					return id
				}
			}
		}
		return 0
	}
	if v, ok := p.([]any); ok {
		if len(v) > 0 {
			return automationIDParam(v[0])
		}
		return 0
	}
	if v, ok := p.(string); ok {
		p = strings.TrimSpace(v)
	}
	id, err := uintFilter(map[string]any{"id": p}, "id")
	if err != nil {
		return 0
	}
	return id
}

const automationIDNumberMaxBytes = domain.AutomationIDNumberMaxBytes
const automationIDNumberMaxExponent = domain.AutomationIDNumberMaxExponent

func parseAutomationNumberUint64(number json.Number) (uint64, bool) {
	return domain.ParseAutomationNumberUint64(number)
}

func automationLabelParams(p any, remove bool) ([]string, bool) {
	if v, ok := p.(map[string]any); ok {
		if list, ok := v["labels"]; ok {
			p = list
		} else if label, ok := v["label"]; ok {
			p = label
		} else if list, ok := v["remove"]; ok && remove {
			p = list
		} else {
			return nil, false
		}
	}
	values := []any{p}
	if list, ok := p.([]any); ok {
		values = list
	}
	labels := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		var s string
		switch v := value.(type) {
		case string:
			s = v
		case json.Number:
			id := automationIDParam(v)
			if id == 0 {
				return nil, false
			}
			s = strconv.FormatUint(uint64(id), 10)
		case float64:
			if v <= 0 || v != float64(uint(v)) {
				return nil, false
			}
			s = strconv.FormatUint(uint64(v), 10)
		default:
			return nil, false
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if len(s) > 100 {
			return nil, false
		}
		if !seen[s] {
			seen[s] = true
			labels = append(labels, s)
		}
	}
	return labels, true
}

type automationActionDefinition struct {
	ActionName        string          `json:"action_name"`
	ActionParams      json.RawMessage `json:"action_params"`
	Name              string          `json:"name"`
	Params            json.RawMessage `json:"params"`
	CapabilityID      string          `json:"capability_id"`
	CapabilityVersion string          `json:"capability_version"`
}

func parseAutomationActionDefinitions(raw string) ([]automationActionDefinition, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "{}" {
		return []automationActionDefinition{}, true
	}
	normalize := func(items []automationActionDefinition) ([]automationActionDefinition, bool) {
		for i := range items {
			if items[i].ActionName == "" {
				items[i].ActionName = items[i].Name
			}
			if len(items[i].ActionParams) == 0 || strings.TrimSpace(string(items[i].ActionParams)) == "null" {
				items[i].ActionParams = items[i].Params
			}
		}
		return items, true
	}
	var items []automationActionDefinition
	if json.Unmarshal([]byte(raw), &items) == nil && len(items) > 0 {
		return normalize(items)
	}
	var wrapper struct{ Actions, Rules, Values []automationActionDefinition }
	if json.Unmarshal([]byte(raw), &wrapper) == nil {
		for _, items := range [][]automationActionDefinition{wrapper.Actions, wrapper.Rules, wrapper.Values} {
			if len(items) > 0 {
				return normalize(items)
			}
		}
	}
	var single automationActionDefinition
	if json.Unmarshal([]byte(raw), &single) == nil && (single.ActionName != "" || single.Name != "" || single.CapabilityID != "") {
		return normalize([]automationActionDefinition{single})
	}
	return nil, false
}
func decodeAutomationActionParams(raw []byte, out any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	return decoder.Decode(out)
}

// Public module envelopes historically decode generic maps as float64. Reject
// unsafe identities there rather than freezing a neighboring rounded integer;
// the ordinary API and JSON-string contract form preserve exact JSON numbers.
func automationUnsafeActionNumbers(value any) bool {
	switch v := value.(type) {
	case float64:
		return v >= 9007199254740992 || v <= -9007199254740992
	case []any:
		for _, item := range v {
			if automationUnsafeActionNumbers(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if automationUnsafeActionNumbers(item) {
				return true
			}
		}
	}
	return false
}
