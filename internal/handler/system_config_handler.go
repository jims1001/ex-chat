package handler

import (
	"encoding/json"
	"errors"
	"io"

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
	ConfigKey string                `json:"config_key"`
	Value     string                `json:"value"`
	Redacted  configRedactionMarker `json:"redacted"`
}

type configRedactionMarker struct{ present bool }

func (m *configRedactionMarker) UnmarshalJSON(raw []byte) error {
	// Validate every occurrence, including null and duplicate fields, so a
	// later valid boolean cannot hide an earlier malformed response marker.
	if string(raw) != "true" && string(raw) != "false" {
		return errors.New("invalid redaction marker")
	}
	m.present = true
	return nil
}

func (h *SystemConfigHandler) ListSystemConfigs(c *gin.Context) {
	configs, err := h.configs.List()
	if err != nil {
		logger.WithComponent("system_config").Error("failed to list system configs")
		response.InternalError(c, "Failed to list system configurations")
		return
	}
	view := make([]AdminSystemConfig, 0, len(configs))
	for _, config := range configs {
		view = append(view, adminSystemConfig(config))
	}
	response.Success(c, view)
}

func (h *SystemConfigHandler) SetSystemConfig(c *gin.Context) {
	var req SetConfigRequest
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&req); err != nil || req.ConfigKey == "" {
		response.BadRequest(c, "Invalid request payload")
		return
	}
	// Reject trailing JSON too: a second object must not hide a response marker.
	if err := decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "Invalid request payload")
		return
	}
	// Even redacted:false denotes a response DTO, not replacement intent.
	if req.Redacted.present && req.Value != "" {
		response.BadRequest(c, "Invalid request payload")
		return
	}
	if req.Value == "" {
		response.Success(c, gin.H{"updated": false, "key": req.ConfigKey})
		return
	}

	if err := h.configs.Set(req.ConfigKey, req.Value); err != nil {
		logger.WithComponent("system_config").Error("failed to save system config")
		response.InternalError(c, "Failed to save system configuration")
		return
	}

	logger.WithComponent("system_config").Info("system config saved successfully")

	response.Success(c, gin.H{"updated": true, "key": req.ConfigKey})
}
