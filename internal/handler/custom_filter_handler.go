package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// ----------------- Custom Filters (Saved Filters) -----------------

type CreateCustomFilterReq struct {
	Name       string `json:"name" binding:"required"`
	FilterType string `json:"filter_type" binding:"required"` // conversation, contact
	Query      any    `json:"query" binding:"required"`       // string or JSON object
}

type UpdateCustomFilterReq struct {
	Name       string `json:"name"`
	FilterType string `json:"filter_type"`
	Query      any    `json:"query"`
}

func parseQueryToString(query any) string {
	if query == nil {
		return ""
	}
	switch q := query.(type) {
	case string:
		return strings.TrimSpace(q)
	default:
		bytes, err := json.Marshal(q)
		if err != nil {
			return ""
		}
		return string(bytes)
	}
}

// CreateCustomFilter creates a new saved/custom filter
func (h *AdvancedHandler) CreateCustomFilter(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	var req CreateCustomFilterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.WithComponent("custom_filter").Warn("create custom filter failed: invalid json",
			"account_id", accID,
			"user_id", userID,
			"error", err.Error(),
		)
		response.BadRequest(c, err.Error())
		return
	}

	queryStr := parseQueryToString(req.Query)
	if queryStr == "" {
		logger.WithComponent("custom_filter").Warn("create custom filter failed: empty query",
			"account_id", accID,
			"user_id", userID,
		)
		response.BadRequest(c, "query is required")
		return
	}

	filter := domain.CustomFilter{
		AccountID:  uint(accID),
		UserID:     userID,
		Name:       strings.TrimSpace(req.Name),
		FilterType: strings.TrimSpace(req.FilterType),
		Query:      queryStr,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	if err := h.db.WithContext(c.Request.Context()).Create(&filter).Error; err != nil {
		logger.WithComponent("custom_filter").Error("database error creating custom filter",
			"account_id", accID,
			"user_id", userID,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("custom_filter").Info("custom filter created successfully",
		"account_id", accID,
		"filter_id", filter.ID,
		"user_id", userID,
		"name", filter.Name,
		"filter_type", filter.FilterType,
	)

	response.Created(c, filter)
}

// ListCustomFilters returns saved filters for the account and user
func (h *AdvancedHandler) ListCustomFilters(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	filterType := strings.TrimSpace(c.Query("filter_type"))

	query := h.db.WithContext(c.Request.Context()).Where("account_id = ?", accID)
	if userID > 0 {
		query = query.Where("user_id = ? OR user_id = 0", userID)
	}
	if filterType != "" {
		query = query.Where("filter_type = ?", filterType)
	}

	var filters []domain.CustomFilter
	if err := query.Order("id DESC").Find(&filters).Error; err != nil {
		logger.WithComponent("custom_filter").Error("database error listing custom filters",
			"account_id", accID,
			"user_id", userID,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("custom_filter").Info("custom filters listed",
		"account_id", accID,
		"user_id", userID,
		"filter_type", filterType,
		"count", len(filters),
	)

	response.Success(c, filters)
}

// GetCustomFilter returns details of a single custom filter
func (h *AdvancedHandler) GetCustomFilter(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")

	var filter domain.CustomFilter
	err := h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND id = ?", accID, id).
		First(&filter).Error

	if err != nil {
		logger.WithComponent("custom_filter").Warn("custom filter not found or access denied",
			"account_id", accID,
			"filter_id", id,
			"user_id", userID,
		)
		response.NotFound(c, "Custom filter not found")
		return
	}

	logger.WithComponent("custom_filter").Info("custom filter retrieved",
		"account_id", accID,
		"filter_id", filter.ID,
		"user_id", userID,
		"name", filter.Name,
		"filter_type", filter.FilterType,
	)

	response.Success(c, filter)
}

// UpdateCustomFilter updates an existing custom filter (name, filter_type, query)
func (h *AdvancedHandler) UpdateCustomFilter(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")

	var filter domain.CustomFilter
	if err := h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND id = ?", accID, id).
		First(&filter).Error; err != nil {
		logger.WithComponent("custom_filter").Warn("custom filter to update not found or access denied",
			"account_id", accID,
			"filter_id", id,
			"user_id", userID,
		)
		response.NotFound(c, "Custom filter not found")
		return
	}

	var req UpdateCustomFilterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.WithComponent("custom_filter").Warn("update custom filter failed: invalid json",
			"account_id", accID,
			"filter_id", id,
			"user_id", userID,
			"error", err.Error(),
		)
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if name := strings.TrimSpace(req.Name); name != "" {
		filter.Name = name
	}
	if ft := strings.TrimSpace(req.FilterType); ft != "" {
		filter.FilterType = ft
	}
	if req.Query != nil {
		queryStr := parseQueryToString(req.Query)
		if queryStr != "" {
			filter.Query = queryStr
		}
	}
	filter.UpdatedAt = time.Now().UTC()

	if err := h.db.WithContext(c.Request.Context()).Save(&filter).Error; err != nil {
		logger.WithComponent("custom_filter").Error("database error updating custom filter",
			"account_id", accID,
			"filter_id", id,
			"user_id", userID,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, "Failed to update custom filter: "+err.Error())
		return
	}

	logger.WithComponent("custom_filter").Info("custom filter updated successfully",
		"account_id", accID,
		"filter_id", filter.ID,
		"user_id", userID,
		"name", filter.Name,
		"filter_type", filter.FilterType,
	)

	response.Success(c, filter)
}

// DeleteCustomFilter deletes a custom filter
func (h *AdvancedHandler) DeleteCustomFilter(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")

	res := h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND id = ?", accID, id).
		Delete(&domain.CustomFilter{})

	if res.Error != nil {
		logger.WithComponent("custom_filter").Error("database error deleting custom filter",
			"account_id", accID,
			"filter_id", id,
			"user_id", userID,
			"error", res.Error.Error(),
		)
		response.Error(c, http.StatusInternalServerError, res.Error.Error())
		return
	}

	if res.RowsAffected == 0 {
		logger.WithComponent("custom_filter").Warn("custom filter to delete not found",
			"account_id", accID,
			"filter_id", id,
			"user_id", userID,
		)
	} else {
		logger.WithComponent("custom_filter").Info("custom filter deleted successfully",
			"account_id", accID,
			"filter_id", id,
			"user_id", userID,
		)
	}

	response.Success(c, gin.H{"deleted": true})
}
