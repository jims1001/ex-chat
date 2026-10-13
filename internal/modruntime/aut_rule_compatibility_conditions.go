package modruntime

import (
	"math"
	"reflect"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

func inspectAutomationCompatibilityConditions(raw, event string, out *domain.AutomationCompatibilitySource) {
	conditions, numeric, valid := parseAutomationProfileConditions(raw, out.ConditionSemantics)
	if !valid {
		code := "invalid_condition_definition"
		if out.ConditionSemantics == domain.AutomationNumericCustomV1 {
			code = "invalid_numeric_condition_definition"
		}
		compatIssue(out, code, "invalid", "blocker", "/conditions", "admission_failed_if_matched", "invalid_frozen_definition")
		return
	}
	out.Conditions = len(conditions)
	if len(conditions) > compatMaxConditions {
		issue := compatIssue(out, "analysis_limit_exceeded", "limit", "unknown", "/conditions", "not_assessed", "")
		issue.Details = domain.AutomationCompatibilityDetails{Count: len(conditions), Limit: compatMaxConditions, Unit: "conditions"}
		out.Complete = false
		return
	}
	operands := 0
	for _, c := range conditions {
		operands += compatOperandCount(c.Values)
	}
	if operands > compatMaxOperands {
		issue := compatIssue(out, "analysis_limit_exceeded", "limit", "unknown", "/conditions", "not_assessed", "")
		issue.Details = domain.AutomationCompatibilityDetails{Count: operands, Limit: compatMaxOperands, Unit: "operands"}
		out.Complete = false
		return
	}
	records := compatEnvelopeIssuesWithNumeric(raw, "conditions", out, numeric)
	for i, condition := range conditions {
		if _, adopted := numeric[i]; adopted {
			compatAddOwner(out, "CUS")
			continue
		}
		contact, _, labels := automationConditionNeeds([]automationCondition{condition})
		if contact || labels {
			compatAddOwner(out, "CUS")
		}
	}
	and, or := false, false
	for i, c := range conditions {
		add := func(code, category, severity, field, effect string) *domain.AutomationCompatibilityIssue {
			issue := compatIssue(out, code, category, severity, compatConditionPath(i, field), effect, "")
			index := i
			issue.ConditionIndex = &index
			return issue
		}
		_, adopted := numeric[i]
		label := !adopted && compatLabelKey(c.AttributeKey)
		supported := adopted || (compatSupportedOperator(c.FilterOperator) && !(label && c.FilterOperator == "starts_with"))
		if adopted {
			notice := add("numeric_custom_comparison", "behavior_change", "notice", "filter_operator", "exact_numeric_comparison")
			notice.Details.KnownOperator = c.FilterOperator
			issue := add("numeric_custom_catalog_type_value_not_assessed", "coverage", "unknown", "custom_attribute_definition_id", "requires_runtime_validation")
			issue.Details.Owner = "CUS"
		}
		if !supported {
			code := "unsupported_condition_operator"
			if label {
				code = "unsupported_label_operator"
			}
			issue := add(code, "unsupported", "blocker", "filter_operator", "condition_leaf_false")
			issue.Details.KnownOperator = compatKnownOperator(c.FilterOperator)
		}
		if !adopted && !compatBuiltinKey(c.AttributeKey) {
			add("custom_definition_unverified", "coverage", "unknown", "attribute_key", "literal_conversation_custom_lookup")
			add("missing_custom_key_false", "behavior_change", "review", "attribute_key", "missing_custom_key_false")
		}
		switch c.AttributeKey {
		case "content", "message_type", "private_note":
			if !adopted && event != "message_created" {
				add("message_context_absent", "behavior_change", "review", "attribute_key", "nil_message_observation")
			}
		}
		if i == len(conditions)-1 {
			if c.QueryOperator != "" {
				add("ignored_final_connector", "ambiguity", "notice", "query_operator", "ignored_metadata")
			}
		} else if strings.ToUpper(c.QueryOperator) == "OR" {
			or = true
		} else {
			and = true
			if c.QueryOperator == "" {
				add("default_connector_and", "default", "notice", "query_operator", "default_applied")
			} else if strings.ToUpper(c.QueryOperator) != "AND" {
				add("noncanonical_connector", "ambiguity", "review", "query_operator", "default_and")
			}
		}
		if !adopted && len(c.Values) == 0 && supported && c.FilterOperator != "is_present" && c.FilterOperator != "is_not_present" {
			effect := "empty_operands_false"
			switch c.FilterOperator {
			case "not_equal_to", "is_not", "does_not_contain":
				effect = "empty_operands_true"
			}
			add("empty_condition_operands", "default", "review", "values", effect)
		}
		if len(c.Values) > 0 && (c.FilterOperator == "is_present" || c.FilterOperator == "is_not_present") {
			add("presence_operands_ignored", "ambiguity", "notice", "values", "ignored_metadata")
		}
		if !adopted && compatNumericPrecision(c.Values) {
			add("condition_numeric_precision_risk", "ambiguity", "review", "values", "float64_condition_values")
		}
		if !adopted && compatNonStringOperand(c.Values) {
			add("condition_stringification", "behavior_change", "review", "values", "go_string_comparison")
		}
		if label && supported {
			add("label_comparison_semantics", "behavior_change", "notice", "filter_operator", "title_or_id_membership")
		}
		if i < len(records) {
			obj := records[i]
			values, _ := compatField(obj, "values").([]any)
			value := compatField(obj, "value")
			if len(values) > 0 && value != nil && !reflect.DeepEqual(values, []any{value}) {
				add("conflicting_condition_values", "ambiguity", "review", "values", "legacy_precedence")
			}
		}
	}
	if and && or {
		compatIssue(out, "mixed_connectors_left_fold", "ambiguity", "review", "/conditions", "left_to_right_fold", "")
	}
}
func compatOperandCount(values []any) int {
	count := 0
	var walk func(any)
	walk = func(v any) {
		count++
		switch x := v.(type) {
		case []any:
			for _, v := range x {
				walk(v)
			}
		case map[string]any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	for _, v := range values {
		walk(v)
	}
	return count
}
func compatNumericPrecision(values []any) bool {
	var risk func(any) bool
	risk = func(v any) bool {
		switch x := v.(type) {
		case float64:
			return math.Abs(x) >= 9007199254740992
		case []any:
			for _, v := range x {
				if risk(v) {
					return true
				}
			}
		case map[string]any:
			for _, v := range x {
				if risk(v) {
					return true
				}
			}
		}
		return false
	}
	return risk(values)
}
func compatNonStringOperand(values []any) bool {
	for _, v := range values {
		if _, ok := v.(string); !ok {
			return true
		}
	}
	return false
}
func compatSupportedOperator(op string) bool {
	switch op {
	case "equal_to", "is", "not_equal_to", "is_not", "contains", "includes", "does_not_contain", "starts_with", "is_present", "is_not_present":
		return true
	}
	return false
}
func compatKnownOperator(op string) string {
	if compatSupportedOperator(op) {
		return op
	}
	switch op {
	case "is_greater_than", "is_less_than", "days_before", "attribute_changed", "changed":
		return op
	}
	return "unknown"
}
func compatLabelKey(key string) bool {
	switch key {
	case "labels", "label", "tag", "tags", "label_ids", "conversation_labels":
		return true
	}
	return false
}
func compatBuiltinKey(key string) bool {
	if compatLabelKey(key) {
		return true
	}
	switch key {
	case "status", "priority", "inbox_id", "assignee_id", "team_id", "contact_id", "content", "message_type", "private_note", "email", "phone_number", "country_code", "city", "company_name", "browser_language", "conversation_language", "referer", "mail_subject":
		return true
	}
	return false
}
