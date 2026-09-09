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

// ----------------- Campaign Handlers -----------------

type CreateCampaignReq struct {
	InboxID      uint       `json:"inbox_id" binding:"required"`
	Title        string     `json:"title" binding:"required"`
	Message      string     `json:"message" binding:"required"`
	Description  string     `json:"description"`
	CampaignType string     `json:"campaign_type"` // one_off, ongoing
	Status       string     `json:"status"`
	Audience     any        `json:"audience"`
	TriggerRules any        `json:"trigger_rules"`
	ScheduledAt  *time.Time `json:"scheduled_at"`
	SenderID     *uint      `json:"sender_id"`
}

type UpdateCampaignReq struct {
	Title        *string    `json:"title"`
	Description  *string    `json:"description"`
	Message      *string    `json:"message"`
	InboxID      *uint      `json:"inbox_id"`
	CampaignType *string    `json:"campaign_type"`
	Status       *string    `json:"status"`
	Audience     any        `json:"audience"`
	TriggerRules any        `json:"trigger_rules"`
	ScheduledAt  *time.Time `json:"scheduled_at"`
	SenderID     *uint      `json:"sender_id"`
}

// CreateCampaign creates a new campaign
func (h *AdvancedHandler) CreateCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	var req CreateCampaignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	audienceStr := ""
	if s, ok := req.Audience.(string); ok {
		audienceStr = s
	} else if req.Audience != nil {
		b, _ := json.Marshal(req.Audience)
		audienceStr = string(b)
	}

	triggerRulesStr := ""
	if s, ok := req.TriggerRules.(string); ok {
		triggerRulesStr = s
	} else if req.TriggerRules != nil {
		b, _ := json.Marshal(req.TriggerRules)
		triggerRulesStr = string(b)
	}

	campType := req.CampaignType
	if campType == "" {
		campType = "one_off"
	}

	status := req.Status
	if status == "" {
		if req.ScheduledAt != nil && req.ScheduledAt.After(time.Now().UTC()) {
			status = "scheduled"
		} else if campType == "ongoing" {
			status = "active"
		} else {
			status = "scheduled"
		}
	}

	camp := domain.Campaign{
		AccountID:    accID,
		InboxID:      req.InboxID,
		Title:        req.Title,
		Description:  req.Description,
		Message:      req.Message,
		CampaignType: campType,
		Status:       status,
		Audience:     audienceStr,
		TriggerRules: triggerRulesStr,
		ScheduledAt:  req.ScheduledAt,
		SenderID:     req.SenderID,
	}

	if err := h.campaignRepo.Create(c.Request.Context(), &camp); err != nil {
		logger.WithComponent("campaign").Error("failed to create campaign",
			"account_id", accID,
			"title", req.Title,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("campaign").Info("campaign created successfully",
		"campaign_id", camp.ID,
		"account_id", accID,
		"title", camp.Title,
		"campaign_type", camp.CampaignType,
		"status", camp.Status,
	)

	response.Created(c, camp)
}

// ListCampaigns lists all campaigns for an account
func (h *AdvancedHandler) ListCampaigns(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	campaigns, err := h.campaignRepo.List(c.Request.Context(), accID)
	if err != nil {
		logger.WithComponent("campaign").Error("failed to list campaigns",
			"account_id", accID,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	// Enrich with delivery stats if available
	for i := range campaigns {
		if stats, err := h.campaignRepo.GetDeliveryStats(c.Request.Context(), accID, campaigns[i].ID); err == nil {
			campaigns[i].DeliveriesCount = stats["total_deliveries"]
			campaigns[i].SentCount = stats["sent"]
			campaigns[i].DeliveredCount = stats["delivered"]
			campaigns[i].FailedCount = stats["failed"]
		}
	}

	response.Success(c, campaigns)
}

// GetCampaign retrieves details for a single campaign including delivery metrics
func (h *AdvancedHandler) GetCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	if h.campaignService != nil {
		camp, err := h.campaignService.GetCampaignWithStats(accID, uint(id))
		if err != nil {
			logger.WithComponent("campaign").Warn("campaign not found",
				"account_id", accID,
				"campaign_id", id,
				"error", err.Error(),
			)
			response.NotFound(c, "Campaign not found")
			return
		}
		response.Success(c, camp)
		return
	}

	camp, err := h.campaignRepo.GetByID(c.Request.Context(), accID, uint(id))
	if err != nil {
		response.NotFound(c, "Campaign not found")
		return
	}
	response.Success(c, camp)
}

// UpdateCampaign updates an existing campaign
func (h *AdvancedHandler) UpdateCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	camp, err := h.campaignRepo.GetByID(c.Request.Context(), accID, uint(id))
	if err != nil {
		logger.WithComponent("campaign").Warn("campaign not found for update",
			"account_id", accID,
			"campaign_id", id,
		)
		response.NotFound(c, "Campaign not found")
		return
	}

	var req UpdateCampaignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Title != nil {
		camp.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		camp.Description = *req.Description
	}
	if req.Message != nil {
		camp.Message = *req.Message
	}
	if req.InboxID != nil {
		camp.InboxID = *req.InboxID
	}
	if req.CampaignType != nil {
		camp.CampaignType = *req.CampaignType
	}
	if req.Status != nil {
		camp.Status = *req.Status
	}
	if req.ScheduledAt != nil {
		camp.ScheduledAt = req.ScheduledAt
	}
	if req.SenderID != nil {
		camp.SenderID = req.SenderID
	}
	if req.Audience != nil {
		if s, ok := req.Audience.(string); ok {
			camp.Audience = s
		} else {
			b, _ := json.Marshal(req.Audience)
			camp.Audience = string(b)
		}
	}
	if req.TriggerRules != nil {
		if s, ok := req.TriggerRules.(string); ok {
			camp.TriggerRules = s
		} else {
			b, _ := json.Marshal(req.TriggerRules)
			camp.TriggerRules = string(b)
		}
	}

	if err := h.campaignRepo.Update(c.Request.Context(), camp); err != nil {
		logger.WithComponent("campaign").Error("failed to update campaign",
			"account_id", accID,
			"campaign_id", id,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update campaign")
		return
	}

	logger.WithComponent("campaign").Info("campaign updated successfully",
		"account_id", accID,
		"campaign_id", camp.ID,
		"title", camp.Title,
		"status", camp.Status,
	)

	response.Success(c, camp)
}

// DeleteCampaign deletes a campaign by ID
func (h *AdvancedHandler) DeleteCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	camp, err := h.campaignRepo.GetByID(c.Request.Context(), accID, uint(id))
	if err != nil {
		logger.WithComponent("campaign").Warn("campaign not found for deletion",
			"account_id", accID,
			"campaign_id", id,
		)
		response.NotFound(c, "Campaign not found")
		return
	}

	if err := h.campaignRepo.Delete(c.Request.Context(), accID, uint(id)); err != nil {
		logger.WithComponent("campaign").Error("failed to delete campaign",
			"account_id", accID,
			"campaign_id", id,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete campaign")
		return
	}

	logger.WithComponent("campaign").Info("campaign deleted successfully",
		"account_id", accID,
		"campaign_id", id,
		"title", camp.Title,
	)

	response.Success(c, gin.H{"deleted": true, "id": id})
}

// TriggerCampaign triggers immediate execution of a campaign
func (h *AdvancedHandler) TriggerCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	if h.campaignService != nil {
		sentCount, err := h.campaignService.TriggerCampaign(accID, uint(id))
		if err != nil {
			logger.WithComponent("campaign").Warn("failed to trigger campaign",
				"account_id", accID,
				"campaign_id", id,
				"error", err.Error(),
			)
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, gin.H{"status": "completed", "sent_count": sentCount})
		return
	}
	response.Success(c, gin.H{"status": "completed"})
}

// PauseCampaign pauses an active or scheduled campaign
func (h *AdvancedHandler) PauseCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	if h.campaignService != nil {
		if err := h.campaignService.PauseCampaign(accID, uint(id)); err != nil {
			logger.WithComponent("campaign").Warn("failed to pause campaign",
				"account_id", accID,
				"campaign_id", id,
				"error", err.Error(),
			)
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, gin.H{"status": "paused", "id": id})
		return
	}

	if err := h.campaignRepo.UpdateStatus(c.Request.Context(), accID, uint(id), "paused"); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"status": "paused", "id": id})
}

// ResumeCampaign resumes a paused campaign
func (h *AdvancedHandler) ResumeCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	if h.campaignService != nil {
		if err := h.campaignService.ResumeCampaign(accID, uint(id)); err != nil {
			logger.WithComponent("campaign").Warn("failed to resume campaign",
				"account_id", accID,
				"campaign_id", id,
				"error", err.Error(),
			)
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, gin.H{"status": "resumed", "id": id})
		return
	}

	if err := h.campaignRepo.UpdateStatus(c.Request.Context(), accID, uint(id), "active"); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"status": "resumed", "id": id})
}

// StopCampaign stops/cancels an active campaign
func (h *AdvancedHandler) StopCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	if h.campaignService != nil {
		if err := h.campaignService.StopCampaign(accID, uint(id)); err != nil {
			logger.WithComponent("campaign").Warn("failed to stop campaign",
				"account_id", accID,
				"campaign_id", id,
				"error", err.Error(),
			)
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, gin.H{"status": "cancelled", "id": id})
		return
	}

	if err := h.campaignRepo.UpdateStatus(c.Request.Context(), accID, uint(id), "cancelled"); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"status": "cancelled", "id": id})
}

// CancelCampaign is an alias for StopCampaign
func (h *AdvancedHandler) CancelCampaign(c *gin.Context) {
	h.StopCampaign(c)
}

// ListCampaignDeliveries returns paginated deliveries for a campaign
func (h *AdvancedHandler) ListCampaignDeliveries(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if h.campaignService != nil {
		deliveries, total, err := h.campaignService.ListCampaignDeliveries(accID, uint(id), page, pageSize)
		if err != nil {
			logger.WithComponent("campaign").Warn("failed to list campaign deliveries",
				"account_id", accID,
				"campaign_id", id,
				"error", err.Error(),
			)
			response.NotFound(c, "Campaign not found")
			return
		}
		response.Success(c, gin.H{
			"deliveries": deliveries,
			"total":      total,
			"page":       page,
			"page_size":  pageSize,
		})
		return
	}

	deliveries, total, err := h.campaignRepo.GetDeliveries(c.Request.Context(), accID, uint(id), page, pageSize)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{
		"deliveries": deliveries,
		"total":      total,
		"page":       page,
		"page_size":  pageSize,
	})
}

// GetCampaignMetrics returns delivery performance metrics for a campaign
func (h *AdvancedHandler) GetCampaignMetrics(c *gin.Context) {
	accID := c.GetUint("account_id")
	if accID == 0 {
		if rawID, err := strconv.ParseUint(c.Param("account_id"), 10, 32); err == nil {
			accID = uint(rawID)
		}
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	if h.campaignService != nil {
		metrics, err := h.campaignService.GetCampaignMetrics(accID, uint(id))
		if err != nil {
			logger.WithComponent("campaign").Warn("failed to get campaign metrics",
				"account_id", accID,
				"campaign_id", id,
				"error", err.Error(),
			)
			response.NotFound(c, "Campaign not found")
			return
		}
		response.Success(c, metrics)
		return
	}

	stats, err := h.campaignRepo.GetDeliveryStats(c.Request.Context(), accID, uint(id))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"campaign_id": id, "stats": stats})
}
