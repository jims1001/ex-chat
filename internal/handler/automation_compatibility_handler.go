package handler

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// ListAutomationRuleCompatibility is a separate read model; existing list,
// detail, and history representations are deliberately unchanged.
func (h *AdvancedHandler) ListAutomationRuleCompatibility(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if len(c.Request.URL.RawQuery) > 4096 {
		response.BadRequest(c, "Invalid automation compatibility query")
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.BadRequest(c, "Invalid automation compatibility query")
		return
	}
	for key, values := range query {
		switch key {
		case "target", "rule_id", "active", "limit", "cursor":
		default:
			response.BadRequest(c, "Invalid automation compatibility query")
			return
		}
		if len(values) != 1 || values[0] == "" {
			response.BadRequest(c, "Invalid automation compatibility query")
			return
		}
	}
	options := service.AutomationCompatibilityOptions{Target: query.Get("target"), RuleID: query.Get("rule_id"), Active: query.Get("active"), Cursor: query.Get("cursor")}
	if raw, ok := query["limit"]; ok {
		limit, err := strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 100 || strings.Trim(raw[0], "0123456789") != "" {
			response.BadRequest(c, "Invalid automation compatibility query")
			return
		}
		options.Limit = limit
	}
	page, err := h.automationService.ListRuleCompatibility(c.Request.Context(), c.GetUint("account_id"), options)
	if err != nil {
		if errors.Is(err, service.ErrAutomationCompatibilityQuery) {
			response.BadRequest(c, "Invalid automation compatibility query")
		} else {
			response.InternalError(c, "Failed to load automation compatibility inventory")
		}
		return
	}
	response.Success(c, page)
}
