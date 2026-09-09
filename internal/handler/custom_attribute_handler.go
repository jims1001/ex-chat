package handler

import (
	"strconv"

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

type CreateCustomAttributeRequest struct {
	AttributeDisplayName string `json:"attribute_display_name" binding:"required"`
	AttributeKey         string `json:"attribute_key" binding:"required"`
	AttributeModel       string `json:"attribute_model" binding:"required"` // contact_attribute, conversation_attribute
	AttributeDisplayType string `json:"attribute_display_type"`
	DefaultValue         string `json:"default_value"`
}

func (h *CustomAttributeHandler) List(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	model := c.Query("attribute_model")
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

	response.Success(c, list)
}

func (h *CustomAttributeHandler) Create(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateCustomAttributeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	displayType := req.AttributeDisplayType
	if displayType == "" {
		displayType = "text"
	}

	def := domain.CustomAttributeDefinition{
		AccountID:            accountID,
		AttributeDisplayName: req.AttributeDisplayName,
		AttributeKey:         req.AttributeKey,
		AttributeModel:       req.AttributeModel,
		AttributeDisplayType: displayType,
		DefaultValue:         req.DefaultValue,
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
		"model", def.AttributeModel,
	)

	response.Created(c, def)
}

func (h *CustomAttributeHandler) Delete(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

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
