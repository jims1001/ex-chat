package handler

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
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

type SetConfigRequest struct {
	ConfigKey string `json:"config_key" binding:"required"`
	Value     string `json:"value"`
}

func (h *EnterpriseHandler) GetAccountFeatures(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	features, err := h.repo.GetAccountFeatures(accountID)
	if err != nil {
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
		response.InternalError(c, "Failed to update account feature")
		return
	}

	features, _ := h.repo.GetAccountFeatures(accountID)
	response.Success(c, features)
}

func (h *EnterpriseHandler) ListSystemConfigs(c *gin.Context) {
	configs, err := h.repo.ListSystemConfigs()
	if err != nil {
		response.InternalError(c, "Failed to list system configurations")
		return
	}
	response.Success(c, configs)
}

func (h *EnterpriseHandler) SetSystemConfig(c *gin.Context) {
	var req SetConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if err := h.repo.SetSystemConfig(req.ConfigKey, req.Value); err != nil {
		response.InternalError(c, "Failed to save system configuration")
		return
	}

	response.Success(c, gin.H{"updated": true, "key": req.ConfigKey})
}
