package handler

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type SystemConfigHandler struct {
	configs *repository.SystemConfigRepository
}

func NewSystemConfigHandler(configs *repository.SystemConfigRepository) *SystemConfigHandler {
	return &SystemConfigHandler{configs: configs}
}

type SetConfigRequest struct {
	ConfigKey string `json:"config_key" binding:"required"`
	Value     string `json:"value"`
}

func (h *SystemConfigHandler) ListSystemConfigs(c *gin.Context) {
	configs, err := h.configs.List()
	if err != nil {
		logger.WithComponent("system_config").Error("failed to list system configs", "error", err.Error())
		response.InternalError(c, "Failed to list system configurations")
		return
	}
	response.Success(c, configs)
}

func (h *SystemConfigHandler) SetSystemConfig(c *gin.Context) {
	var req SetConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if err := h.configs.Set(req.ConfigKey, req.Value); err != nil {
		logger.WithComponent("system_config").Error("failed to save system config",
			"config_key", req.ConfigKey,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to save system configuration")
		return
	}

	logger.WithComponent("system_config").Info("system config saved successfully",
		"config_key", req.ConfigKey,
	)

	response.Success(c, gin.H{"updated": true, "key": req.ConfigKey})
}
