package handler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// ----------------- Automation Rules (OPS / Automation) -----------------

// ListAutomationRules lists all automation rules for current account
func (h *AdvancedHandler) ListAutomationRules(c *gin.Context) {
	accID := c.GetUint("account_id")
	var rules []domain.AutomationRule
	if err := h.db.Where("account_id = ?", accID).Order("id ASC").Find(&rules).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, rules)
}

var allowedAutomationEvents = map[string]bool{
	"conversation_created": true,
	"conversation_updated": true,
	"message_created":      true,
	"conversation_opened":  true,
}

var allowedAutomationConditionAttributes = map[string]bool{
	"content":               true,
	"email":                 true,
	"country_code":          true,
	"status":                true,
	"message_type":          true,
	"browser_language":      true,
	"assignee_id":           true,
	"team_id":               true,
	"referer":               true,
	"city":                  true,
	"company_name":          true,
	"inbox_id":              true,
	"mail_subject":          true,
	"phone_number":          true,
	"priority":              true,
	"conversation_language": true,
	"labels":                true,
	"private_note":          true,
	"contact_id":            true,
}

var allowedAutomationFilterOperators = map[string]bool{
	"equal_to":         true,
	"not_equal_to":     true,
	"contains":         true,
	"does_not_contain": true,
	"is_present":       true,
	"is_not_present":   true,
	"starts_with":      true,
	"is_greater_than":  true,
	"is_less_than":     true,
	"days_before":      true,
	"is":               true,
	"is_not":           true,
	"includes":         true,
}

var allowedAutomationActions = map[string]bool{
	"send_message":          true,
	"send_reply":            true,
	"reply":                 true,
	"add_label":             true,
	"add_labels":            true,
	"remove_label":          true,
	"remove_labels":         true,
	"send_email_to_team":    true,
	"assign_team":           true,
	"assign_agent":          true,
	"remove_assigned_agent": true,
	"remove_assigned_team":  true,
	"send_webhook_event":    true,
	"mute_conversation":     true,
	"send_attachment":       true,
	"change_status":         true,
	"resolve_conversation":  true,
	"close_conversation":    true,
	"close":                 true,
	"resolve":               true,
	"open_conversation":     true,
	"open":                  true,
	"pending_conversation":  true,
	"snooze_conversation":   true,
	"snooze":                true,
	"change_priority":       true,
	"send_email_transcript": true,
	"add_private_note":      true,
	"private_note":          true,
}

type singleConditionReq struct {
	AttributeKey   string `json:"attribute_key"`
	Key            string `json:"key"`
	FilterOperator string `json:"filter_operator"`
	Operator       string `json:"operator"`
	QueryOperator  string `json:"query_operator"`
}

type singleActionReq struct {
	ActionName string `json:"action_name"`
	Name       string `json:"name"`
}

func validateAutomationRule(eventName string, conditionsRaw, actionsRaw any) error {
	if eventName != "" && !allowedAutomationEvents[eventName] {
		return fmt.Errorf("invalid event_name '%s'", eventName)
	}

	// Parse and validate conditions
	if conditionsRaw != nil {
		var conds []singleConditionReq
		switch v := conditionsRaw.(type) {
		case string:
			str := strings.TrimSpace(v)
			if str != "" && str != "[]" && str != "{}" {
				if err := json.Unmarshal([]byte(str), &conds); err != nil {
					var wrapper struct {
						Conditions []singleConditionReq `json:"conditions"`
						Rules      []singleConditionReq `json:"rules"`
						Values     []singleConditionReq `json:"values"`
					}
					if err2 := json.Unmarshal([]byte(str), &wrapper); err2 == nil {
						if len(wrapper.Conditions) > 0 {
							conds = wrapper.Conditions
						} else if len(wrapper.Rules) > 0 {
							conds = wrapper.Rules
						} else {
							conds = wrapper.Values
						}
					}
				}
			}
		default:
			b, _ := json.Marshal(v)
			var direct []singleConditionReq
			if err := json.Unmarshal(b, &direct); err == nil {
				conds = direct
			} else {
				var wrapper struct {
					Conditions []singleConditionReq `json:"conditions"`
					Rules      []singleConditionReq `json:"rules"`
					Values     []singleConditionReq `json:"values"`
				}
				if err2 := json.Unmarshal(b, &wrapper); err2 == nil {
					if len(wrapper.Conditions) > 0 {
						conds = wrapper.Conditions
					} else if len(wrapper.Rules) > 0 {
						conds = wrapper.Rules
					} else {
						conds = wrapper.Values
					}
				}
			}
		}

		for _, cond := range conds {
			attr := cond.AttributeKey
			if attr == "" {
				attr = cond.Key
			}
			if attr != "" && !allowedAutomationConditionAttributes[attr] && !strings.HasPrefix(attr, "custom_attribute") {
				return fmt.Errorf("automation condition attribute '%s' is not supported", attr)
			}
			op := cond.FilterOperator
			if op == "" {
				op = cond.Operator
			}
			if op != "" && !allowedAutomationFilterOperators[op] {
				return fmt.Errorf("automation condition filter_operator '%s' is not supported", op)
			}
			qop := strings.ToUpper(strings.TrimSpace(cond.QueryOperator))
			if qop != "" && qop != "AND" && qop != "OR" {
				return fmt.Errorf("query_operator must be either 'AND' or 'OR'")
			}
		}
	}

	// Parse and validate actions
	if actionsRaw != nil {
		var acts []singleActionReq
		switch v := actionsRaw.(type) {
		case string:
			str := strings.TrimSpace(v)
			if str != "" && str != "[]" && str != "{}" {
				if err := json.Unmarshal([]byte(str), &acts); err != nil {
					var wrapper struct {
						Actions []singleActionReq `json:"actions"`
						Values  []singleActionReq `json:"values"`
					}
					if err2 := json.Unmarshal([]byte(str), &wrapper); err2 == nil {
						if len(wrapper.Actions) > 0 {
							acts = wrapper.Actions
						} else {
							acts = wrapper.Values
						}
					}
				}
			}
		default:
			b, _ := json.Marshal(v)
			var direct []singleActionReq
			if err := json.Unmarshal(b, &direct); err == nil {
				acts = direct
			} else {
				var wrapper struct {
					Actions []singleActionReq `json:"actions"`
					Values  []singleActionReq `json:"values"`
				}
				if err2 := json.Unmarshal(b, &wrapper); err2 == nil {
					if len(wrapper.Actions) > 0 {
						acts = wrapper.Actions
					} else {
						acts = wrapper.Values
					}
				}
			}
		}

		for _, act := range acts {
			name := act.ActionName
			if name == "" {
				name = act.Name
			}
			if name != "" && !allowedAutomationActions[name] {
				return fmt.Errorf("automation action '%s' is not supported", name)
			}
		}
	}

	return nil
}

// CreateAutomationRule creates a new automation rule
func (h *AdvancedHandler) CreateAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
		EventName   string `json:"event_name" binding:"required"`
		Conditions  any    `json:"conditions"`
		Actions     any    `json:"actions"`
		Active      *bool  `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := validateAutomationRule(req.EventName, req.Conditions, req.Actions); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var condStr string
	if str, ok := req.Conditions.(string); ok {
		condStr = str
	} else if req.Conditions != nil {
		b, _ := json.Marshal(req.Conditions)
		condStr = string(b)
	}

	var actStr string
	if str, ok := req.Actions.(string); ok {
		actStr = str
	} else if req.Actions != nil {
		b, _ := json.Marshal(req.Actions)
		actStr = string(b)
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}

	rule := domain.AutomationRule{
		AccountID:   accID,
		Name:        req.Name,
		Description: req.Description,
		EventName:   req.EventName,
		Conditions:  condStr,
		Actions:     actStr,
		Active:      active,
	}
	if err := h.db.Create(&rule).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Created(c, rule)
}

// GetAutomationRule gets an automation rule by ID
func (h *AdvancedHandler) GetAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id := c.Param("id")
	var rule domain.AutomationRule
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).First(&rule).Error; err != nil {
		response.NotFound(c, "Automation rule not found")
		return
	}
	response.Success(c, rule)
}

// UpdateAutomationRule updates an existing automation rule
func (h *AdvancedHandler) UpdateAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id := c.Param("id")
	var existing domain.AutomationRule
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).First(&existing).Error; err != nil {
		response.NotFound(c, "Automation rule not found")
		return
	}
	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var eventName string
	if ev, ok := req["event_name"].(string); ok {
		eventName = ev
	}
	if err := validateAutomationRule(eventName, req["conditions"], req["actions"]); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if cond, ok := req["conditions"]; ok && cond != nil {
		if _, isStr := cond.(string); !isStr {
			b, _ := json.Marshal(cond)
			req["conditions"] = string(b)
		}
	}
	if act, ok := req["actions"]; ok && act != nil {
		if _, isStr := act.(string); !isStr {
			b, _ := json.Marshal(act)
			req["actions"] = string(b)
		}
	}
	if err := h.db.Model(&existing).Updates(req).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, existing)
}

// DeleteAutomationRule deletes an automation rule by ID
func (h *AdvancedHandler) DeleteAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id := c.Param("id")
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).Delete(&domain.AutomationRule{}).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// CloneAutomationRule clones an existing automation rule with full condition and action trees
func (h *AdvancedHandler) CloneAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id := c.Param("id")

	var sourceRule domain.AutomationRule
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).First(&sourceRule).Error; err != nil {
		response.NotFound(c, "Automation rule not found")
		return
	}

	var req struct {
		Name   string `json:"name"`
		Active *bool  `json:"active"`
	}
	_ = c.ShouldBindJSON(&req)

	cloneName := strings.TrimSpace(req.Name)
	if cloneName == "" {
		cloneName = fmt.Sprintf("%s (Copy)", sourceRule.Name)
	}

	// Safe default for cloned automation rules is false, unless caller explicitly specifies active
	active := false
	if req.Active != nil {
		active = *req.Active
	}

	clonedRule := domain.AutomationRule{
		AccountID:   accID,
		Name:        cloneName,
		Description: sourceRule.Description,
		EventName:   sourceRule.EventName,
		Conditions:  sourceRule.Conditions,
		Actions:     sourceRule.Actions,
		Active:      active,
	}

	if err := h.db.Select("AccountID", "Name", "Description", "EventName", "Conditions", "Actions", "Active").Create(&clonedRule).Error; err != nil {
		logger.WithComponent("automation").Error("failed to clone automation rule",
			"source_rule_id", sourceRule.ID,
			"account_id", accID,
			"error", err.Error(),
		)
		response.InternalError(c, err.Error())
		return
	}

	logger.WithComponent("automation").Info("automation rule cloned",
		"source_rule_id", sourceRule.ID,
		"cloned_rule_id", clonedRule.ID,
		"account_id", accID,
		"name", clonedRule.Name,
		"active", clonedRule.Active,
	)

	response.Created(c, clonedRule)
}

// ListAutomationRuleExecutions returns execution history for automation rules
func (h *AdvancedHandler) ListAutomationRuleExecutions(c *gin.Context) {
	accID := c.GetUint("account_id")
	ruleIDStr := c.Query("rule_id")
	convIDStr := c.Query("conversation_id")
	status := c.Query("status")

	query := h.db.Where("account_id = ?", accID)
	if ruleIDStr != "" {
		query = query.Where("rule_id = ?", ruleIDStr)
	}
	if convIDStr != "" {
		query = query.Where("conversation_id = ?", convIDStr)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var executions []domain.AutomationRuleExecution
	if err := query.Order("triggered_at DESC").Limit(100).Find(&executions).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, executions)
}

// GetAutomationRuleExecution returns a single execution detail
func (h *AdvancedHandler) GetAutomationRuleExecution(c *gin.Context) {
	accID := c.GetUint("account_id")
	execID := c.Param("execution_id")

	var execution domain.AutomationRuleExecution
	if err := h.db.Where("account_id = ? AND id = ?", accID, execID).First(&execution).Error; err != nil {
		response.NotFound(c, "Automation rule execution record not found")
		return
	}

	response.Success(c, execution)
}
