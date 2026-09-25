package handler

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type EnterpriseHandler struct {
	repo *repository.EnterpriseRepository
}

func NewEnterpriseHandler(repo *repository.EnterpriseRepository) *EnterpriseHandler {
	return &EnterpriseHandler{repo: repo}
}

type SetFeatureRequest struct {
	FeatureName string `json:"feature_name" binding:"required"`
	Enabled     bool   `json:"enabled"`
}

func (h *EnterpriseHandler) GetAccountFeatures(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	features, err := h.repo.GetAccountFeatures(accountID)
	if err != nil {
		logger.WithComponent("enterprise").Error("failed to get account features",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get account features")
		return
	}
	response.Success(c, features)
}

func (h *EnterpriseHandler) SetAccountFeature(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req SetFeatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if err := h.repo.SetAccountFeature(accountID, req.FeatureName, req.Enabled); err != nil {
		logger.WithComponent("enterprise").Error("failed to update account feature",
			"account_id", accountID,
			"feature_name", req.FeatureName,
			"enabled", req.Enabled,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update account feature")
		return
	}

	logger.WithComponent("enterprise").Info("account feature updated",
		"account_id", accountID,
		"feature_name", req.FeatureName,
		"enabled", req.Enabled,
	)

	features, _ := h.repo.GetAccountFeatures(accountID)
	response.Success(c, features)
}
