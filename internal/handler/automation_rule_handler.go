package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// ----------------- Automation Rules (OPS / Automation) -----------------

// ListAutomationRules lists all automation rules for current account
func (h *AdvancedHandler) ListAutomationRules(c *gin.Context) {
	accID := c.GetUint("account_id")
	rules, err := h.automationService.ListRules(accID)
	if err != nil {
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
	return validateAutomationRuleForMode(eventName, conditionsRaw, actionsRaw, false)
}

func validateAutomationRuleForMode(eventName string, conditionsRaw, actionsRaw any, durable bool) error {
	return validateAutomationRuleWithNumericProfile(eventName, conditionsRaw, actionsRaw, durable, false)
}

func validateAutomationRuleWithNumericProfile(eventName string, conditionsRaw, actionsRaw any, durable, numericProfile bool) error {
	if eventName != "" && !allowedAutomationEvents[eventName] && !(durable && eventName == "conversation_resolved") {
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
			op := cond.FilterOperator
			if op == "" {
				op = cond.Operator
			}
			adoptedCustomNumber := numericProfile && (op == "is_greater_than" || op == "is_less_than")
			if attr != "" && !allowedAutomationConditionAttributes[attr] && !strings.HasPrefix(attr, "custom_attribute") && !adoptedCustomNumber {
				return fmt.Errorf("automation condition attribute '%s' is not supported", attr)
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

// validateAutomationRuleForProfile keeps the legacy API matrix unchanged unless
// the caller explicitly opts in to the versioned numeric profile.
func validateAutomationRuleForProfile(eventName string, conditionsRaw, actionsRaw any, durable bool, profile string) error {
	if profile == "" {
		return validateAutomationRuleForMode(eventName, conditionsRaw, actionsRaw, durable)
	}
	conditions, ok := conditionsRaw.(string)
	if !ok {
		raw, err := json.Marshal(conditionsRaw)
		if err != nil {
			return fmt.Errorf("conditions are invalid: %w", err)
		}
		conditions = string(raw)
	}
	if err := domain.ValidateAutomationConditionSemantics(profile, conditions); err != nil {
		return err
	}
	// Validate every selected leaf, including single-object definitions, using
	// the legacy attribute/operator/action matrix with only numeric custom keys
	// opened by the explicit profile. Raw records preserve number tokens.
	records, err := domain.AutomationConditionRecords(conditions)
	if err != nil {
		return err
	}
	return validateAutomationRuleWithNumericProfile(eventName, records, actionsRaw, durable, true)
}

func automationConditionSemantics(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	profile, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("condition_semantics must be a string")
	}
	return profile, nil
}

// CreateAutomationRule creates a new automation rule
func (h *AdvancedHandler) CreateAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	var req struct {
		Name               string          `json:"name" binding:"required"`
		Description        string          `json:"description"`
		EventName          string          `json:"event_name" binding:"required"`
		Conditions         any             `json:"conditions"`
		ConditionSemantics json.RawMessage `json:"condition_semantics"`
		Actions            any             `json:"actions"`
		Active             *bool           `json:"active"`
	}
	if err := bindAutomationRuleJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	profile, err := automationConditionSemantics(req.ConditionSemantics)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := validateAutomationRuleForProfile(req.EventName, req.Conditions, req.Actions, h.durableAutomation, profile); err != nil {
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
		AccountID:          accID,
		Name:               req.Name,
		Description:        req.Description,
		EventName:          req.EventName,
		Conditions:         condStr,
		ConditionSemantics: profile,
		Actions:            actStr,
		Active:             active,
	}
	if err := h.automationService.CreateRule(&rule); err != nil {
		if errors.Is(err, service.ErrAutomationEventUnsupported) || errors.Is(err, service.ErrAutomationConditionSemanticsInvalid) || errors.Is(err, service.ErrAutomationConditionMode) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, err.Error())
		return
	}
	response.Created(c, rule)
}

// GetAutomationRule gets an automation rule by ID
func (h *AdvancedHandler) GetAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	rule, err := h.automationService.FindRule(accID, uint(id))
	if err != nil || rule == nil {
		response.NotFound(c, "Automation rule not found")
		return
	}
	response.Success(c, rule)
}

// UpdateAutomationRule updates an existing automation rule
func (h *AdvancedHandler) UpdateAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	existing, err := h.automationService.FindRule(accID, uint(id))
	if err != nil || existing == nil {
		response.NotFound(c, "Automation rule not found")
		return
	}
	var req map[string]any
	if err := bindAutomationRuleJSON(c, &req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var eventName string
	if ev, ok := req["event_name"].(string); ok {
		eventName = ev
	}
	profile := existing.ConditionSemantics
	if raw, present := req["condition_semantics"]; present {
		var valid bool
		profile, valid = raw.(string)
		if !valid {
			response.BadRequest(c, "condition_semantics must be a string")
			return
		}
	}
	conditions := req["conditions"]
	if _, present := req["conditions"]; !present && profile != "" {
		conditions = existing.Conditions
	}
	if err := validateAutomationRuleForProfile(eventName, conditions, req["actions"], h.durableAutomation, profile); err != nil {
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
	if err := h.automationService.UpdateRule(existing, req); err != nil {
		if errors.Is(err, service.ErrAutomationEventUnsupported) || errors.Is(err, service.ErrAutomationConditionSemanticsInvalid) || errors.Is(err, service.ErrAutomationConditionMode) {
			response.BadRequest(c, err.Error())
			return
		}
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, existing)
}

// DeleteAutomationRule deletes an automation rule by ID
func (h *AdvancedHandler) DeleteAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	if err := h.automationService.DeleteRule(accID, uint(id)); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// CloneAutomationRule clones an existing automation rule with full condition and action trees
func (h *AdvancedHandler) CloneAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	sourceRule, err := h.automationService.FindRule(accID, uint(id))
	if err != nil || sourceRule == nil {
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
		AccountID:          accID,
		Name:               cloneName,
		Description:        sourceRule.Description,
		EventName:          sourceRule.EventName,
		Conditions:         sourceRule.Conditions,
		ConditionSemantics: sourceRule.ConditionSemantics,
		Actions:            sourceRule.Actions,
		Active:             active,
	}

	if err := h.automationService.CreateRule(&clonedRule); err != nil {
		if errors.Is(err, service.ErrAutomationEventUnsupported) || errors.Is(err, service.ErrAutomationConditionSemanticsInvalid) || errors.Is(err, service.ErrAutomationConditionMode) {
			response.BadRequest(c, err.Error())
			return
		}
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

	if !validAutomationHistoryView(c) {
		return
	}
	options := service.AutomationExecutionPageOptions{Cursor: c.Query("cursor"), Order: c.Query("order")}
	if _, exists := c.Request.URL.Query()["limit"]; exists {
		raw := c.Query("limit")
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 || strings.Trim(raw, "0123456789") != "" {
			response.BadRequest(c, "Invalid automation history limit")
			return
		}
		options.Limit = limit
	}
	page, err := h.automationService.ListExecutionsPage(accID, ruleIDStr, convIDStr, status, options)
	if err != nil {
		if errors.Is(err, service.ErrAutomationHistoryQuery) {
			response.BadRequest(c, "Invalid automation history query")
		} else {
			response.InternalError(c, "Failed to load automation history")
		}
		return
	}
	if page.NextCursor != "" {
		c.Header("X-Next-Cursor", page.NextCursor)
	}
	// Let allowed cross-origin browser clients read the optional page cursor.
	exposed := c.Writer.Header().Get("Access-Control-Expose-Headers")
	if exposed == "" {
		c.Header("Access-Control-Expose-Headers", "X-Next-Cursor")
	} else {
		c.Header("Access-Control-Expose-Headers", exposed+", X-Next-Cursor")
	}
	if c.Query("view") == "compat_v1" {
		views, err := h.automationService.CompatibilityExecutionViews(page.Items)
		if err != nil {
			response.InternalError(c, "Failed to load automation history view")
			return
		}
		response.Success(c, views)
		return
	}
	response.Success(c, page.Items)
}

// GetAutomationRuleExecution returns a single execution detail
func (h *AdvancedHandler) GetAutomationRuleExecution(c *gin.Context) {
	if !validAutomationHistoryView(c) {
		return
	}
	accID := c.GetUint("account_id")
	execID, _ := strconv.ParseUint(c.Param("execution_id"), 10, 32)

	execution, err := h.automationService.FindExecution(accID, uint(execID))
	if err != nil || execution == nil {
		response.NotFound(c, "Automation rule execution record not found")
		return
	}

	if c.Query("view") == "compat_v1" {
		h.respondAutomationCompatibilityExecution(c, execution)
		return
	}
	response.Success(c, execution)
}

// CancelAutomationRuleExecution is tenant-scoped and permission-gated in routing.
// It never rewinds an already committed owner action.
func (h *AdvancedHandler) CancelAutomationRuleExecution(c *gin.Context) {
	if !validAutomationHistoryView(c) {
		return
	}
	accountID := c.GetUint("account_id")
	id, err := strconv.ParseUint(c.Param("execution_id"), 10, 32)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid execution ID")
		return
	}
	execution, err := h.automationService.FindExecution(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to load automation execution")
		return
	}
	if execution == nil {
		response.NotFound(c, "Automation execution not found")
		return
	}
	result, err := h.automationService.CancelExecution(c.Request.Context(), accountID, uint(id))
	if err != nil {
		response.Error(c, 409, "Automation execution cannot be cancelled in its current state")
		return
	}
	if c.Query("view") == "compat_v1" {
		// Cancellation still uses its established owner command and permissions.
		// Read the resulting state; an already accepted final effect can finish.
		current, err := h.automationService.FindExecution(accountID, uint(id))
		if err != nil || current == nil {
			response.InternalError(c, "Failed to load automation history view")
			return
		}
		h.respondAutomationCompatibilityExecution(c, current)
		return
	}
	response.Success(c, result)
}

func validAutomationHistoryView(c *gin.Context) bool {
	view := c.Query("view")
	if view != "" && view != "raw" && view != "compat_v1" {
		response.BadRequest(c, "Invalid automation history view")
		return false
	}
	return true
}
func (h *AdvancedHandler) respondAutomationCompatibilityExecution(c *gin.Context, execution *domain.AutomationRuleExecution) {
	views, err := h.automationService.CompatibilityExecutionViews([]domain.AutomationRuleExecution{*execution})
	if err != nil {
		response.InternalError(c, "Failed to load automation history view")
		return
	}
	response.Success(c, views[0])
}

func bindAutomationRuleJSON(c *gin.Context, out any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return binding.Validator.ValidateStruct(out)
}
