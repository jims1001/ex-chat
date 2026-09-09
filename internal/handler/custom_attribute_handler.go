package handler

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type CustomAttributeHandler struct {
	repo *repository.CustomAttributeRepository
}

func NewCustomAttributeHandler(repo *repository.CustomAttributeRepository) *CustomAttributeHandler {
	return &CustomAttributeHandler{repo: repo}
}

func getAccountIDFromContext(c *gin.Context) uint {
	if raw, exists := c.Get(middleware.ContextAccountID); exists {
		if id, ok := raw.(uint); ok && id > 0 {
			return id
		}
	}
	if param := c.Param("account_id"); param != "" {
		if id, err := strconv.ParseUint(param, 10, 32); err == nil && id > 0 {
			return uint(id)
		}
	}
	return 0
}

func parseValuesToString(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(bytes)
	}
}

type CreateCustomAttributeRequest struct {
	AttributeDisplayName string `json:"attribute_display_name" binding:"required"`
	AttributeKey         string `json:"attribute_key" binding:"required"`
	AttributeModel       string `json:"attribute_model" binding:"required"` // contact_attribute, conversation_attribute
	AttributeDisplayType string `json:"attribute_display_type"`
	AttributeDescription string `json:"attribute_description"`
	AttributeValues      any    `json:"attribute_values"`
	DefaultValue         string `json:"default_value"`
}

type UpdateCustomAttributeRequest struct {
	AttributeDisplayName string `json:"attribute_display_name"`
	AttributeDisplayType string `json:"attribute_display_type"`
	AttributeDescription string `json:"attribute_description"`
	AttributeValues      any    `json:"attribute_values"`
	DefaultValue         string `json:"default_value"`
}

// List lists custom attribute definitions for an account and optional model
func (h *CustomAttributeHandler) List(c *gin.Context) {
	accountID := getAccountIDFromContext(c)
	model := strings.TrimSpace(c.Query("attribute_model"))

	list, err := h.repo.List(accountID, model)
	if err != nil {
		logger.WithComponent("custom_attribute").Error("failed to list custom attribute definitions",
			"account_id", accountID,
			"model", model,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list custom attribute definitions")
		return
	}

	logger.WithComponent("custom_attribute").Info("custom attribute definitions listed",
		"account_id", accountID,
		"model", model,
		"count", len(list),
	)

	response.Success(c, list)
}

// Get returns details for a single custom attribute definition
func (h *CustomAttributeHandler) Get(c *gin.Context) {
	accountID := getAccountIDFromContext(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid attribute definition ID")
		return
	}

	def, err := h.repo.Get(accountID, uint(id))
	if err != nil || def == nil {
		logger.WithComponent("custom_attribute").Warn("custom attribute definition not found or access denied",
			"account_id", accountID,
			"attribute_id", id,
		)
		response.NotFound(c, "Custom attribute definition not found")
		return
	}

	logger.WithComponent("custom_attribute").Info("custom attribute definition retrieved",
		"account_id", accountID,
		"attribute_id", def.ID,
		"key", def.AttributeKey,
		"display_name", def.AttributeDisplayName,
		"model", def.AttributeModel,
	)

	response.Success(c, def)
}

// Create creates a new custom attribute definition
func (h *CustomAttributeHandler) Create(c *gin.Context) {
	accountID := getAccountIDFromContext(c)

	var req CreateCustomAttributeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.WithComponent("custom_attribute").Warn("create custom attribute definition failed: invalid json",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	displayType := strings.TrimSpace(req.AttributeDisplayType)
	if displayType == "" {
		displayType = "text"
	}

	def := domain.CustomAttributeDefinition{
		AccountID:            accountID,
		AttributeDisplayName: strings.TrimSpace(req.AttributeDisplayName),
		AttributeKey:         strings.TrimSpace(req.AttributeKey),
		AttributeModel:       strings.TrimSpace(req.AttributeModel),
		AttributeDisplayType: displayType,
		AttributeDescription: strings.TrimSpace(req.AttributeDescription),
		AttributeValues:      parseValuesToString(req.AttributeValues),
		DefaultValue:         strings.TrimSpace(req.DefaultValue),
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}

	if err := h.repo.Create(&def); err != nil {
		logger.WithComponent("custom_attribute").Error("failed to create custom attribute definition",
			"account_id", accountID,
			"key", req.AttributeKey,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create custom attribute definition")
		return
	}

	logger.WithComponent("custom_attribute").Info("custom attribute definition created successfully",
		"account_id", accountID,
		"attribute_id", def.ID,
		"key", def.AttributeKey,
		"display_name", def.AttributeDisplayName,
		"model", def.AttributeModel,
	)

	response.Created(c, def)
}

// Update edits/updates an existing custom attribute definition
func (h *CustomAttributeHandler) Update(c *gin.Context) {
	accountID := getAccountIDFromContext(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid attribute definition ID")
		return
	}

	def, err := h.repo.Get(accountID, uint(id))
	if err != nil || def == nil {
		logger.WithComponent("custom_attribute").Warn("custom attribute definition to update not found or access denied",
			"account_id", accountID,
			"attribute_id", id,
		)
		response.NotFound(c, "Custom attribute definition not found")
		return
	}

	var req UpdateCustomAttributeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.WithComponent("custom_attribute").Warn("update custom attribute definition failed: invalid json",
			"account_id", accountID,
			"attribute_id", id,
			"error", err.Error(),
		)
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if name := strings.TrimSpace(req.AttributeDisplayName); name != "" {
		def.AttributeDisplayName = name
	}
	if dt := strings.TrimSpace(req.AttributeDisplayType); dt != "" {
		def.AttributeDisplayType = dt
	}
	if desc := strings.TrimSpace(req.AttributeDescription); desc != "" {
		def.AttributeDescription = desc
	}
	if req.AttributeValues != nil {
		def.AttributeValues = parseValuesToString(req.AttributeValues)
	}
	if req.DefaultValue != "" {
		def.DefaultValue = strings.TrimSpace(req.DefaultValue)
	}
	def.UpdatedAt = time.Now().UTC()

	if err := h.repo.Update(def); err != nil {
		logger.WithComponent("custom_attribute").Error("failed to update custom attribute definition",
			"account_id", accountID,
			"attribute_id", def.ID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update custom attribute definition: "+err.Error())
		return
	}

	logger.WithComponent("custom_attribute").Info("custom attribute definition updated successfully",
		"account_id", accountID,
		"attribute_id", def.ID,
		"key", def.AttributeKey,
		"display_name", def.AttributeDisplayName,
		"model", def.AttributeModel,
	)

	response.Success(c, def)
}

// Delete deletes a custom attribute definition
func (h *CustomAttributeHandler) Delete(c *gin.Context) {
	accountID := getAccountIDFromContext(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid attribute definition ID")
		return
	}

	if err := h.repo.Delete(accountID, uint(id)); err != nil {
		logger.WithComponent("custom_attribute").Error("failed to delete custom attribute definition",
			"account_id", accountID,
			"attribute_id", uint(id),
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete attribute definition")
		return
	}

	logger.WithComponent("custom_attribute").Info("custom attribute definition deleted successfully",
		"account_id", accountID,
		"attribute_id", uint(id),
	)

	response.Success(c, gin.H{"deleted": true})
}
