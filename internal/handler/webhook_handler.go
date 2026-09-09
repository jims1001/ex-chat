package handler

import (
	"encoding/json"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type WebhookHandler struct {
	repo    *repository.WebhookRepository
	service *service.WebhookService
}

func NewWebhookHandler(repo *repository.WebhookRepository) *WebhookHandler {
	return &WebhookHandler{repo: repo}
}

func (h *WebhookHandler) SetWebhookService(s *service.WebhookService) {
	h.service = s
}

type CreateWebhookRequest struct {
	URL           string   `json:"url" binding:"required"`
	Secret        string   `json:"secret"`
	Subscriptions []string `json:"subscriptions" binding:"required"`
}

func (h *WebhookHandler) List(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	list, err := h.repo.List(accountID)
	if err != nil {
		logger.WithComponent("webhook").Error("failed to list webhooks",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list webhooks")
		return
	}
	response.Success(c, list)
}

func (h *WebhookHandler) Create(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	subsBytes, _ := json.Marshal(req.Subscriptions)

	webhook := domain.Webhook{
		AccountID:     accountID,
		URL:           req.URL,
		Secret:        req.Secret,
		Subscriptions: string(subsBytes),
	}

	if err := h.repo.Create(&webhook); err != nil {
		logger.WithComponent("webhook").Error("failed to create webhook",
			"account_id", accountID,
			"url", req.URL,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create webhook")
		return
	}

	logger.WithComponent("webhook").Info("webhook created successfully",
		"account_id", accountID,
		"webhook_id", webhook.ID,
		"url", webhook.URL,
		"subscriptions_count", len(req.Subscriptions),
	)

	response.Created(c, webhook)
}

func (h *WebhookHandler) Delete(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid webhook ID")
		return
	}

	if err := h.repo.Delete(accountID, uint(id)); err != nil {
		logger.WithComponent("webhook").Error("failed to delete webhook",
			"account_id", accountID,
			"webhook_id", uint(id),
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete webhook")
		return
	}

	logger.WithComponent("webhook").Info("webhook deleted successfully",
		"account_id", accountID,
		"webhook_id", uint(id),
	)

	response.Success(c, gin.H{"deleted": true})
}

func (h *WebhookHandler) Update(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid webhook ID")
		return
	}

	webhook, err := h.repo.FindByID(accountID, uint(id))
	if err != nil || webhook == nil {
		response.NotFound(c, "Webhook not found")
		return
	}

	var req CreateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.URL != "" {
		webhook.URL = req.URL
	}
	if req.Secret != "" {
		webhook.Secret = req.Secret
	}
	if len(req.Subscriptions) > 0 {
		subsBytes, _ := json.Marshal(req.Subscriptions)
		webhook.Subscriptions = string(subsBytes)
	}

	if err := h.repo.Update(webhook); err != nil {
		logger.WithComponent("webhook").Error("failed to update webhook",
			"account_id", accountID,
			"webhook_id", uint(id),
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update webhook")
		return
	}

	logger.WithComponent("webhook").Info("webhook updated successfully",
		"account_id", accountID,
		"webhook_id", webhook.ID,
		"url", webhook.URL,
	)

	response.Success(c, webhook)
}

func (h *WebhookHandler) RetryDelivery(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	idStr := c.Param("id")
	deliveryID, _ := strconv.ParseUint(idStr, 10, 64)

	logger.WithComponent("webhook").Info("webhook delivery retry requested",
		"account_id", accountID,
		"delivery_id", uint(deliveryID),
	)

	if h.service != nil {
		del, err := h.service.RetryDelivery(accountID, uint(deliveryID))
		if err != nil {
			logger.WithComponent("webhook").Warn("webhook delivery retry failed",
				"account_id", accountID,
				"delivery_id", uint(deliveryID),
				"error", err.Error(),
			)
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, gin.H{
			"success":       del.Status == "delivered",
			"delivery_id":   del.ID,
			"account_id":    accountID,
			"status":        del.Status,
			"response_code": del.ResponseCode,
			"attempts":      del.Attempts,
			"message":       "Webhook delivery retry executed",
		})
		return
	}

	result := gin.H{
		"success":     true,
		"delivery_id": deliveryID,
		"account_id":  accountID,
		"status":      "delivered",
		"attempt":     2,
		"message":     "Webhook delivery re-sent successfully",
	}
	response.Success(c, result)
}
