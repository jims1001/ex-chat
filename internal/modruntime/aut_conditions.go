package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

// Initial owner-port observations are made before AUT opens its intake transaction.
// Deferred siblings preserve the shared fields and project proven earlier effects;
// their committed observations and decisions remain immutable before dispatch.
type automationConditionContextKey struct{}
type automationConditionContext struct {
	Conversation                conversationView
	Message                     *messageView
	Contact                     *contactView
	Company                     *companyView
	NumericCatalog              []automationNumericCatalogObservation
	Labels                      []labelView
	contactLoaded, labelsLoaded bool
}

type automationCondition struct {
	AttributeKey   string `json:"attribute_key"`
	FilterOperator string `json:"filter_operator"`
	Values         []any  `json:"values"`
	Value          any    `json:"value"`
	Key            string `json:"key"`
	Operator       string `json:"operator"`
	QueryOperator  string `json:"query_operator"`
}

// Keep the accepted legacy structures and precedence. In particular an empty
// wrapper or null is not the same as an explicitly empty rule.
func parseAutomationConditions(raw string) ([]automationCondition, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil, true
	}
	normalize := func(conditions []automationCondition) ([]automationCondition, bool) {
		for i := range conditions {
			c := &conditions[i]
			if c.AttributeKey == "" {
				c.AttributeKey = c.Key
			}
			if c.FilterOperator == "" {
				c.FilterOperator = c.Operator
			}
			if len(c.Values) == 0 && c.Value != nil {
				c.Values = []any{c.Value}
			}
		}
		return conditions, true
	}
	var conditions []automationCondition
	if json.Unmarshal([]byte(raw), &conditions) == nil && len(conditions) > 0 {
		return normalize(conditions)
	}
	var wrapper struct {
		Conditions []automationCondition `json:"conditions"`
		Rules      []automationCondition `json:"rules"`
		Values     []automationCondition `json:"values"`
	}
	if json.Unmarshal([]byte(raw), &wrapper) == nil {
		if len(wrapper.Conditions) > 0 {
			return normalize(wrapper.Conditions)
		}
		if len(wrapper.Rules) > 0 {
			return normalize(wrapper.Rules)
		}
		if len(wrapper.Values) > 0 {
			return normalize(wrapper.Values)
		}
	}
	var single automationCondition
	if json.Unmarshal([]byte(raw), &single) == nil && (single.AttributeKey != "" || single.Key != "") {
		return normalize([]automationCondition{single})
	}
	return nil, false
}

func automationConditionNeeds(conditions []automationCondition) (contact, company, labels bool) {
	for _, c := range conditions {
		switch c.AttributeKey {
		case "email", "phone_number", "country_code", "city", "company_name":
			contact = true
			company = company || c.AttributeKey == "company_name"
		case "labels", "label", "tag", "tags", "label_ids", "conversation_labels":
			labels = true
		}
	}
	return
}

// Custom attributes are an explicit owner projection, never an accidental
// expansion of ordinary conversation/contact read views.
func automationConditionAttributeNeeds(conditions []automationCondition) (conversation, contact bool) {
	for _, c := range conditions {
		switch c.AttributeKey {
		case "country_code", "city", "company_name":
			contact = true
		case "status", "priority", "inbox_id", "assignee_id", "team_id", "contact_id", "content", "message_type", "private_note", "email", "phone_number", "labels", "label", "tag", "tags", "label_ids", "conversation_labels":
		default:
			conversation = true
		}
	}
	return
}

func (r *Runtime) prepareAutomationConditionContext(ctx context.Context, actor foundation.ContractActor, conversation conversationView, message *messageView, versions []domain.AutomationRuleVersion) (*automationConditionContext, error) {
	result := &automationConditionContext{Conversation: conversation, Message: message}
	var contactNeeded, companyNeeded, labelsNeeded, contactAttributesNeeded bool
	for _, version := range versions {
		conditions, numbers, valid := parseAutomationProfileConditions(version.Conditions, version.ConditionSemantics)
		if !valid {
			continue
		}
		legacyConditions := make([]automationCondition, 0, len(conditions))
		for i, c := range conditions {
			if _, numeric := numbers[i]; !numeric {
				legacyConditions = append(legacyConditions, c)
			}
		}
		contact, company, labels := automationConditionNeeds(legacyConditions)
		_, attrs := automationProfileAttributeNeeds(version.Conditions, version.ConditionSemantics)
		for _, n := range numbers {
			contact = contact || n.CustomAttributeType == "contact_attribute"
		}
		contactAttributesNeeded = contactAttributesNeeded || attrs
		contactNeeded, companyNeeded, labelsNeeded = contactNeeded || contact, companyNeeded || company, labelsNeeded || labels
	}
	result.contactLoaded, result.labelsLoaded = contactNeeded, labelsNeeded
	if err := r.prepareAutomationNumericCatalog(ctx, actor, conversation, versions, result); err != nil {
		return nil, err
	}
	if (!contactNeeded || conversation.ContactID == 0) && (!labelsNeeded || len(conversation.LabelIDs) == 0) {
		return result, nil
	}
	dep, ok := r.Config.Dependencies["CUS"]
	if !ok {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "CUS condition dependency is not configured"}
	}
	query := foundation.QueryEnvelope{AccountID: conversation.AccountID, Actor: actor, Sort: []string{"id"}, Page: "1", Consistency: "strong", RequestedAt: time.Now().UTC()}
	lookup := func(capability string, id uint, out any) (bool, error) {
		query.QueryType, query.Filter, query.Fields = capability, map[string]any{"id": id}, []string{"id", "account_id", "name", "email", "phone_number", "company_id", "title"}
		if capability == "cus.contact.get.v1" && contactAttributesNeeded {
			query.Fields = append(query.Fields, "custom_attributes")
		}
		err := newModuleClient("AUT", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, out)
		var failure *ContractFailure
		if errors.As(err, &failure) && failure.Type == "resource_not_found" {
			return false, nil
		}
		return err == nil, err
	}
	invalid := func() (*automationConditionContext, error) {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "condition owner response does not match the requested tenant or identity"}
	}
	if contactNeeded && conversation.ContactID > 0 {
		var contact contactView
		found, err := lookup("cus.contact.get.v1", conversation.ContactID, &contact)
		if err != nil {
			return nil, err
		}
		if found {
			if contact.ID != conversation.ContactID || contact.AccountID != conversation.AccountID {
				return invalid()
			}
			result.Contact = &contact
			if companyNeeded && contact.CompanyID != nil {
				var company companyView
				found, err = lookup("cus.company.get.v1", *contact.CompanyID, &company)
				if err != nil {
					return nil, err
				}
				if found {
					if company.ID != *contact.CompanyID || company.AccountID != conversation.AccountID {
						return invalid()
					}
					result.Company = &company
				}
			}
		}
	}
	if labelsNeeded {
		for _, id := range conversation.LabelIDs {
			var label labelView
			found, err := lookup("cus.label.get.v1", id, &label)
			if err != nil {
				return nil, err
			}
			if found {
				if label.ID != id || label.AccountID != conversation.AccountID {
					return invalid()
				}
				result.Labels = append(result.Labels, label)
			}
		}
	}
	return result, nil
}

func matchAutomationConditions(raw string, observation *automationConditionContext) (bool, string) {
	conditions, valid := parseAutomationConditions(raw)
	if !valid {
		return false, "invalid_frozen_definition"
	}
	if len(conditions) == 0 {
		return true, ""
	}
	if observation == nil {
		return false, "condition_context_unavailable"
	}
	contact, _, labels := automationConditionNeeds(conditions)
	if (contact && !observation.contactLoaded) || (labels && !observation.labelsLoaded) {
		return false, "condition_context_unavailable"
	}
	result := true
	for i, c := range conditions {
		matched := matchAutomationCondition(c, observation)
		if i == 0 {
			result = matched
		} else if strings.ToUpper(conditions[i-1].QueryOperator) == "OR" {
			result = result || matched
		} else {
			result = result && matched
		}
	}
	return result, ""
}

func automationCustomValue(raw, key string) (any, bool) {
	var attrs map[string]any
	if json.Unmarshal([]byte(raw), &attrs) != nil {
		return nil, false
	}
	value, exists := attrs[key]
	return value, exists
}

func matchAutomationCondition(c automationCondition, observation *automationConditionContext) bool {
	conv, msg, contact := observation.Conversation, observation.Message, observation.Contact
	var target any
	switch c.AttributeKey {
	case "status":
		target = conv.Status
	case "priority":
		target = conv.Priority
	case "inbox_id":
		target = int(conv.InboxID)
	case "assignee_id":
		if conv.AssigneeID != nil {
			target = int(*conv.AssigneeID)
		}
	case "team_id":
		if conv.TeamID != nil {
			target = int(*conv.TeamID)
		}
	case "contact_id":
		target = int(conv.ContactID)
	case "content":
		if msg != nil {
			target = msg.Content
		}
	case "message_type":
		if msg != nil {
			target = msg.MessageType
		}
	case "private_note":
		if msg != nil {
			target = msg.Private
		}
	case "email":
		if contact != nil {
			target = contact.Email
		}
	case "phone_number":
		if contact != nil {
			target = contact.PhoneNumber
		}
	case "country_code", "city", "company_name":
		if contact != nil {
			if c.AttributeKey == "company_name" && observation.Company != nil {
				target = observation.Company.Name
			}
			if target == nil {
				target, _ = automationCustomValue(contact.CustomAttributes, c.AttributeKey)
			}
		}
	case "browser_language", "conversation_language", "referer", "mail_subject":
		target, _ = automationCustomValue(conv.CustomAttributes, c.AttributeKey)
	case "labels", "label", "tag", "tags", "label_ids", "conversation_labels":
		return matchAutomationLabels(c, observation.Labels)
	default:
		var exists bool
		target, exists = automationCustomValue(conv.CustomAttributes, c.AttributeKey)
		if !exists {
			return false
		}
	}
	switch c.FilterOperator {
	case "is_present":
		return target != nil && fmt.Sprint(target) != ""
	case "is_not_present":
		return target == nil || fmt.Sprint(target) == ""
	case "equal_to", "is":
		for _, v := range c.Values {
			if fmt.Sprint(target) == fmt.Sprint(v) {
				return true
			}
		}
		return false
	case "not_equal_to", "is_not":
		for _, v := range c.Values {
			if fmt.Sprint(target) == fmt.Sprint(v) {
				return false
			}
		}
		return true
	case "contains", "includes", "does_not_contain", "starts_with":
		for _, v := range c.Values {
			value, needle := strings.ToLower(fmt.Sprint(target)), strings.ToLower(fmt.Sprint(v))
			matched := strings.Contains(value, needle)
			if c.FilterOperator == "starts_with" {
				matched = strings.HasPrefix(value, needle)
			}
			if matched {
				return c.FilterOperator != "does_not_contain"
			}
		}
		return c.FilterOperator == "does_not_contain"
	}
	return false
}

func matchAutomationLabels(c automationCondition, labels []labelView) bool {
	switch c.FilterOperator {
	case "is_present":
		return len(labels) > 0
	case "is_not_present":
		return len(labels) == 0
	case "equal_to", "is", "contains", "includes", "not_equal_to", "is_not", "does_not_contain":
		negative := c.FilterOperator == "not_equal_to" || c.FilterOperator == "is_not" || c.FilterOperator == "does_not_contain"
		for _, label := range labels {
			for _, v := range c.Values {
				value := strings.TrimSpace(fmt.Sprint(v))
				if value == "" {
					continue
				}
				equal := strings.EqualFold(label.Title, value) || fmt.Sprint(label.ID) == value
				contains := strings.Contains(strings.ToLower(label.Title), strings.ToLower(value))
				matched := equal
				if c.FilterOperator == "contains" || c.FilterOperator == "includes" {
					matched = equal || contains
				}
				if c.FilterOperator == "does_not_contain" {
					matched = contains
				}
				if matched {
					return !negative
				}
			}
		}
		return negative
	}
	return false
}
