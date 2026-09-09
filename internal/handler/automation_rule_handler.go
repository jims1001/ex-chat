package handler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
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
	_ = h.db.Model(&existing).Updates(req)
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
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, clonedRule)
}
