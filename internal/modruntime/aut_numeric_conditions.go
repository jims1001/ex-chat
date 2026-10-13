package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

type automationNumericCatalogObservation struct {
	DefinitionID uint                            `json:"definition_id"`
	Definition   *automationCustomDefinitionView `json:"definition,omitempty"`
}

func parseAutomationProfileConditions(raw, profile string) ([]automationCondition, map[int]domain.AutomationNumericCondition, bool) {
	if profile == "" {
		conditions, ok := parseAutomationConditions(raw)
		return conditions, nil, ok
	}
	if profile != domain.AutomationNumericCustomV1 {
		return nil, nil, false
	}
	numbers, err := domain.AutomationNumericConditions(raw)
	if err != nil {
		return nil, nil, false
	}
	records, err := domain.AutomationConditionRecords(raw)
	if err != nil {
		return nil, nil, false
	}
	byIndex := map[int]domain.AutomationNumericCondition{}
	for _, n := range numbers {
		byIndex[n.Index] = n
	}
	conditions := make([]automationCondition, 0, len(records))
	for i, record := range records {
		if n, ok := byIndex[i]; ok {
			var header struct {
				QueryOperator string `json:"query_operator"`
			}
			if json.Unmarshal(record, &header) != nil {
				return nil, nil, false
			}
			conditions = append(conditions, automationCondition{AttributeKey: n.AttributeKey, FilterOperator: n.FilterOperator, QueryOperator: header.QueryOperator})
		} else {
			cs, ok := parseAutomationConditions("[" + string(record) + "]")
			if !ok || len(cs) != 1 {
				return nil, nil, false
			}
			conditions = append(conditions, cs[0])
		}
	}
	return conditions, byIndex, true
}

func automationProfileAttributeNeeds(raw, profile string) (conversation, contact bool) {
	cs, ns, ok := parseAutomationProfileConditions(raw, profile)
	if !ok {
		return false, false
	}
	for i, c := range cs {
		if n, exists := ns[i]; exists {
			conversation = conversation || n.CustomAttributeType == "conversation_attribute"
			contact = contact || n.CustomAttributeType == "contact_attribute"
			continue
		}
		a, b := automationConditionAttributeNeeds([]automationCondition{c})
		conversation, contact = conversation || a, contact || b
	}
	return
}

// The catalog and requested owner values are read before the AUT transaction.
// The caller's actor is forwarded unchanged; a User actor is never elevated.
func (r *Runtime) prepareAutomationNumericCatalog(ctx context.Context, actor foundation.ContractActor, c conversationView, versions []domain.AutomationRuleVersion, result *automationConditionContext) error {
	ids := map[uint]bool{}
	for _, v := range versions {
		if v.ConditionSemantics != domain.AutomationNumericCustomV1 {
			continue
		}
		ns, err := domain.AutomationNumericConditions(v.Conditions)
		if err != nil {
			continue
		}
		for _, n := range ns {
			ids[n.DefinitionID] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	dep, ok := r.Config.Dependencies["CUS"]
	if !ok {
		return &ContractFailure{Type: "dependency_unavailable", Message: "CUS number catalog dependency is not configured"}
	}
	sorted := make([]uint, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, id := range sorted {
		q := foundation.QueryEnvelope{QueryType: "cus.automation.number_definition.get.v1", AccountID: c.AccountID, Actor: actor, Filter: map[string]any{"id": id}, Fields: []string{"id", "account_id", "attribute_key", "attribute_model", "attribute_display_type"}, Sort: []string{"id"}, Page: "1", Consistency: "strong", RequestedAt: time.Now().UTC()}
		var view automationCustomDefinitionView
		err := newModuleClient("AUT", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, q, &view)
		var failure *ContractFailure
		if errors.As(err, &failure) && (failure.Type == "resource_not_found" || failure.Type == "validation_failed") {
			result.NumericCatalog = append(result.NumericCatalog, automationNumericCatalogObservation{DefinitionID: id})
			continue
		}
		if err != nil {
			return err
		}
		if view.ID != id || view.AccountID != c.AccountID {
			return &ContractFailure{Type: "dependency_unavailable", Message: "number catalog response has invalid tenant or identity"}
		}
		result.NumericCatalog = append(result.NumericCatalog, automationNumericCatalogObservation{DefinitionID: id, Definition: &view})
	}
	return nil
}

func matchAutomationNumericCondition(n domain.AutomationNumericCondition, o *automationConditionContext) (bool, string) {
	var catalog *automationNumericCatalogObservation
	for i := range o.NumericCatalog {
		if o.NumericCatalog[i].DefinitionID == n.DefinitionID {
			catalog = &o.NumericCatalog[i]
			break
		}
	}
	if catalog == nil {
		return false, "condition_context_unavailable"
	}
	d := catalog.Definition
	if d == nil || d.AttributeKey != n.AttributeKey || d.AttributeModel != n.CustomAttributeType || d.AttributeDisplayType != "number" {
		return false, "invalid_frozen_definition"
	}
	raw := o.Conversation.CustomAttributes
	if n.CustomAttributeType == "contact_attribute" {
		if !o.contactLoaded {
			return false, "condition_context_unavailable"
		}
		if o.Contact == nil {
			return false, ""
		}
		raw = o.Contact.CustomAttributes
	}
	if strings.TrimSpace(raw) == "" {
		return false, ""
	}
	if len(raw) > domain.AutomationNumericMaxDefinitionBytes {
		return false, "invalid_frozen_definition"
	}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil {
		return false, "invalid_frozen_definition"
	}
	value, present := values[n.AttributeKey]
	if !present || string(value) == "null" {
		return false, ""
	}
	cmp, ok := domain.CompareAutomationNumbers(json.Number(string(value)), n.Threshold)
	if !ok {
		return false, "invalid_frozen_definition"
	}
	return (n.FilterOperator == "is_greater_than" && cmp > 0) || (n.FilterOperator == "is_less_than" && cmp < 0), ""
}

func matchAutomationConditionsProfile(raw, profile string, observation *automationConditionContext) (bool, string) {
	if profile == "" {
		return matchAutomationConditions(raw, observation)
	}
	cs, ns, valid := parseAutomationProfileConditions(raw, profile)
	if !valid {
		return false, "invalid_frozen_definition"
	}
	if len(cs) == 0 {
		return true, ""
	}
	if observation == nil {
		return false, "condition_context_unavailable"
	}
	result := true
	for i, c := range cs {
		matched := false
		if n, ok := ns[i]; ok {
			var reason string
			matched, reason = matchAutomationNumericCondition(n, observation)
			if reason != "" {
				return false, reason
			}
		} else {
			contact, _, labels := automationConditionNeeds([]automationCondition{c})
			if (contact && !observation.contactLoaded) || (labels && !observation.labelsLoaded) {
				return false, "condition_context_unavailable"
			}
			matched = matchAutomationCondition(c, observation)
		}
		if i == 0 {
			result = matched
		} else if strings.ToUpper(cs[i-1].QueryOperator) == "OR" {
			result = result || matched
		} else {
			result = result && matched
		}
	}
	return result, ""
}
