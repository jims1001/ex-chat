package modruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

func automationActionOwner(a automationLocalAction) string {
	if a.Owner == "" {
		return "CON"
	}
	return a.Owner
}
func automationActionKey(e domain.AutomationRuleExecution, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("aut:%s:%d", *e.ExecutionKey, index)))
	return hex.EncodeToString(sum[:])
}
func automationDispatch(c automationRunnerContext, e domain.AutomationRuleExecution, results []automationActionResult, index int, timeout int, at time.Time) foundation.CommandEnvelope {
	a := c.Actions[index]
	command := c.command(e)
	command.CommandID = automationActionKey(e, index)
	command.IdempotencyKey = command.CommandID
	command.CommandType = a.Command
	if a.Command != "con.status.change.v1" && a.Command != "con.priority.change.v1" {
		command.RequestedAt = at.UTC()
	}
	command.Target = foundation.ContractTarget{Type: "conversation", ID: strconv.Itoa(int(e.ConversationID)), ExpectedVersion: uint64(e.RunnerTargetVersion)}
	input := map[string]any{"origin_execution_id": e.ID, "automation_depth": e.AutomationDepth + 1}
	switch a.Command {
	case "con.status.change.v1", "con.priority.change.v1":
		input[a.Field] = a.Value
	case "con.automation.apply.v1":
		input["operation"] = a.Operation
		if c.ActivityPolicy != "" {
			input["activity_policy"] = c.ActivityPolicy
		}
		switch a.Operation {
		case "assign_agent", "assign_team":
			input[a.Field] = a.Value
		case "snooze_conversation":
			duration, _ := time.ParseDuration(a.Value)
			deadline := at.Add(duration).UTC()
			// PostgreSQL timestamptz stores microseconds. Canonicalize once before
			// dispatch so its receipt and SQLite confirm the identical instant.
			if deadline.Nanosecond()%1000 != 0 {
				deadline = deadline.Truncate(time.Microsecond).Add(time.Microsecond)
			}
			input["snoozed_until"] = deadline.Format(time.RFC3339Nano)
		case "add_label", "remove_label":
			input["label_id"] = strconv.FormatUint(uint64(results[index-1].ResourceID), 10)
			if c.ActivityPolicy != "" && a.Operation == "remove_label" && results[index-1].ResourceID == 0 {
				input["missing_label"] = a.Value
			}
		}
	case "cus.automation.label.resolve.v1":
		command.Target = foundation.ContractTarget{Type: "account", ID: strconv.Itoa(int(e.AccountID))}
		input["operation"] = a.Operation
		input["label"] = a.Value
	case "con.automation.message.prepare.v1":
		input["content"] = a.Value
		input["private"] = a.Private
	case "msg.automation.message.create.v1":
		input["content"] = a.Value
		input["private"] = a.Private
		input["prepare_command_id"] = automationActionKey(e, index-1)
	}
	command.Input = input
	if timeout <= 0 {
		timeout = 1000
	}
	query := "con.conversation.get.v1"
	if a.Owner == "CUS" {
		query = "cus.label.get.v1"
	}
	if a.Owner == "MSG" {
		query = "msg.message.get.v1"
	}
	command.TimeoutPolicy = foundation.TimeoutPolicy{TimeoutMS: timeout, OnTimeout: "query", StatusQuery: query}
	return command
}

func validAutomationLocalAction(a automationLocalAction) bool {
	if a.FailureReason != "" {
		return a.Command == "" && a.Owner == "" && a.Capability == "" && a.Field == "" && a.Value == "" && a.Operation == "" && !a.Private && a.LogicalIndex != nil && (a.FailureReason == "invalid_action_parameters" || a.FailureReason == "action_capability_executor_unavailable")
	}
	switch a.Command {
	case "con.status.change.v1":
		return a.Owner == "" && a.Operation == "" && !a.Private && a.Capability == "conversation.change_status" && a.Field == "status" && (a.Value == "open" || a.Value == "pending" || a.Value == "resolved")
	case "con.priority.change.v1":
		return a.Owner == "" && a.Operation == "" && !a.Private && a.Capability == "conversation.change_priority" && a.Field == "priority" && (a.Value == "low" || a.Value == "medium" || a.Value == "high" || a.Value == "urgent")
	case "con.automation.apply.v1":
		if a.Owner != "" || a.Private {
			return false
		}
		switch a.Operation {
		case "assign_agent", "assign_team":
			field := "assignee_id"
			if a.Operation == "assign_team" {
				field = "team_id"
			}
			id, err := strconv.ParseUint(a.Value, 10, 64)
			return err == nil && id > 0 && strconv.FormatUint(id, 10) == a.Value && a.Field == field && a.Capability == "conversation."+a.Operation
		case "remove_assigned_agent", "remove_assigned_team", "mute_conversation":
			return a.Capability == "conversation."+a.Operation && a.Field == "" && a.Value == ""
		case "snooze_conversation":
			d, err := time.ParseDuration(a.Value)
			return err == nil && d > 0 && a.Capability == "conversation.snooze" && a.Field == "duration"
		case "add_label", "remove_label":
			return a.Capability == "conversation."+a.Operation && a.Field == "label" && a.Value != "" && len(a.Value) <= 100
		}
	case "cus.automation.label.resolve.v1":
		return a.Owner == "CUS" && !a.Private && (a.Operation == "add" || a.Operation == "remove") && a.Capability == "conversation."+a.Operation+"_label" && a.Field == "label" && a.Value != "" && len(a.Value) <= 100
	case "con.automation.message.prepare.v1", "msg.automation.message.create.v1":
		capability := "message.create_public"
		if a.Private {
			capability = "message.create_private_note"
		}
		owner := ""
		if a.Command == "msg.automation.message.create.v1" {
			owner = "MSG"
		}
		return a.Owner == owner && a.Operation == "" && a.Capability == capability && a.Field == "content" && a.Value != "" && len(a.Value) <= 100000
	}
	return false
}
func validAutomationPlan(actions []automationLocalAction) bool {
	if len(actions) == 0 || len(actions) > automationMaxSteps {
		return false
	}
	for i, a := range actions {
		if !validAutomationLocalAction(a) {
			return false
		}
		switch a.Command {
		case "cus.automation.label.resolve.v1":
			if i+1 >= len(actions) {
				return false
			}
			next := actions[i+1]
			if ((a.LogicalIndex != nil || next.LogicalIndex != nil) && !automationSameLogicalAction(a, next)) || next.Command != "con.automation.apply.v1" || next.Operation != a.Operation+"_label" || next.Value != a.Value {
				return false
			}
		case "con.automation.apply.v1":
			if a.Operation == "add_label" || a.Operation == "remove_label" {
				if i == 0 {
					return false
				}
				p := actions[i-1]
				if p.Command != "cus.automation.label.resolve.v1" || p.Operation+"_label" != a.Operation || p.Value != a.Value {
					return false
				}
			}
		case "con.automation.message.prepare.v1":
			if i+1 >= len(actions) {
				return false
			}
			n := actions[i+1]
			if ((a.LogicalIndex != nil || n.LogicalIndex != nil) && !automationSameLogicalAction(a, n)) || n.Command != "msg.automation.message.create.v1" || n.Value != a.Value || n.Private != a.Private {
				return false
			}
		case "msg.automation.message.create.v1":
			if i == 0 {
				return false
			}
			p := actions[i-1]
			if p.Command != "con.automation.message.prepare.v1" || p.Value != a.Value || p.Private != a.Private {
				return false
			}
		}
	}
	return true
}

// Owner proof is a bounded version transition, not a count of action steps.
func automationResultAdvances(a automationLocalAction, result automationActionResult, before uint, activityPolicy ...string) (uint, bool) {
	policy := ""
	if len(activityPolicy) > 1 {
		return 0, false
	}
	if len(activityPolicy) == 1 {
		policy = activityPolicy[0]
	}
	if !validAutomationActivityPolicy(policy) {
		return 0, false
	}
	if result.Status != "succeeded" || result.ObjectVersion <= 0 || result.Step != automationActionStep(a) {
		return 0, false
	}
	after := uint(result.ObjectVersion)
	switch a.Command {
	case "con.status.change.v1", "con.priority.change.v1", "con.automation.message.prepare.v1":
		if after != before+1 || result.ResourceID != 0 || result.Outcome != "" {
			return 0, false
		}
	case "con.automation.apply.v1":
		if result.ResourceID != 0 {
			return 0, false
		}
		if policy == automationTouchValidActivity {
			if after != before+1 || (result.Outcome != "" && result.Outcome != automationActivityTouched) {
				return 0, false
			}
		} else if (after != before && after != before+1) || result.Outcome != "" {
			return 0, false
		}
	case "cus.automation.label.resolve.v1":
		if after != before || (result.Outcome != "resolved" && result.Outcome != "missing") || (result.ResourceID == 0) != (result.Outcome == "missing") || (a.Operation == "add" && result.ResourceID == 0) {
			return 0, false
		}
	case "msg.automation.message.create.v1":
		outcome := "queued"
		if a.Private {
			outcome = "saved"
		}
		if after != before || result.ResourceID == 0 || result.Outcome != outcome {
			return 0, false
		}
	default:
		return 0, false
	}
	return after, true
}
func automationPrefixVersion(c automationRunnerContext, results []automationActionResult, count int, base uint) (uint, bool) {
	for i := 0; i < count; i++ {
		var ok bool
		base, ok = automationCheckpointStep(c, results, i, base)
		if !ok {
			return 0, false
		}
	}
	return base, true
}
func automationJSONEqual(a, b any) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && string(x) == string(y)
}

// Validate a frozen dispatch by rebuilding every field from the private plan and
// confirmed prior receipts. Only the first snooze deadline/timeout are variable.
func validAutomationDispatch(c automationRunnerContext, e domain.AutomationRuleExecution, results []automationActionResult, index int, before uint) bool {
	if len(c.Dispatches) == 0 {
		return c.Actions[index].Command == "con.status.change.v1" || c.Actions[index].Command == "con.priority.change.v1"
	}
	if len(c.Dispatches) != len(c.Actions) {
		return false
	}
	d := c.Dispatches[index]
	if c.Actions[index].FailureReason != "" {
		return automationEmptyDispatch(d) && (results[index].Status == "pending" || results[index].Status == "failed" || results[index].Status == "skipped")
	}
	if results[index].Status == "skipped" {
		return automationEmptyDispatch(d)
	}
	if d.CommandID == "" {
		return automationEmptyDispatch(d) && (results[index].Status == "pending" || results[index].Status == "skipped")
	}
	at := c.RequestedAt
	if c.Actions[index].Command != "con.status.change.v1" && c.Actions[index].Command != "con.priority.change.v1" {
		at = d.RequestedAt
		if at.Before(c.RequestedAt) {
			return false
		}
	}
	e.RunnerTargetVersion = before
	expected := automationDispatch(c, e, results, index, d.TimeoutPolicy.TimeoutMS, at)
	return d.TimeoutPolicy.TimeoutMS > 0 && d.Validate() == nil && automationJSONEqual(expected, d)
}
func automationEmptyDispatch(d foundation.CommandEnvelope) bool {
	return reflect.DeepEqual(d, foundation.CommandEnvelope{})
}

func automationConversationResultMatches(a automationLocalAction, command foundation.CommandEnvelope, e domain.AutomationRuleExecution, updated conversationView) bool {
	if updated.AccountID != e.AccountID || updated.ID != e.ConversationID || updated.Status == domain.ConversationStatusDeleting {
		return false
	}
	switch a.Command {
	case "con.status.change.v1":
		return updated.Status == a.Value
	case "con.priority.change.v1":
		return updated.Priority == a.Value
	case "con.automation.message.prepare.v1":
		return true
	case "con.automation.apply.v1":
		switch a.Operation {
		case "assign_agent":
			return updated.AssigneeID != nil && strconv.FormatUint(uint64(*updated.AssigneeID), 10) == a.Value
		case "assign_team":
			return updated.TeamID != nil && strconv.FormatUint(uint64(*updated.TeamID), 10) == a.Value
		case "remove_assigned_agent":
			return updated.AssigneeID == nil
		case "remove_assigned_team":
			return updated.TeamID == nil
		case "mute_conversation":
			return updated.Muted && updated.Status == domain.ConversationStatusResolved && updated.SnoozedUntil == nil
		case "snooze_conversation":
			value, _ := command.Input["snoozed_until"].(string)
			until, err := time.Parse(time.RFC3339Nano, value)
			return err == nil && updated.Status == domain.ConversationStatusSnoozed && updated.SnoozedUntil != nil && updated.SnoozedUntil.Equal(until)
		case "add_label", "remove_label":
			id, _ := optionalUint(command.Input["label_id"])
			found := false
			if id != nil {
				for _, label := range updated.LabelIDs {
					if label == *id {
						found = true
					}
				}
			}
			return found == (a.Operation == "add_label")
		}
	}
	return false
}

func automationActionStep(a automationLocalAction) string {
	if a.FailureReason != "" {
		return "validate_action"
	}
	switch a.Command {
	case "cus.automation.label.resolve.v1":
		return "resolve_label"
	case "con.automation.apply.v1":
		return "apply_conversation"
	case "con.automation.message.prepare.v1":
		return "prepare_message"
	case "msg.automation.message.create.v1":
		return "create_message"
	}
	return ""
}
