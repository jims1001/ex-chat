package handler

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AppliedSLAHandler handles endpoints for Applied SLAs, events and metrics
type AppliedSLAHandler struct {
	db             *gorm.DB
	appliedSLARepo *repository.AppliedSLARepository
	slaService     *service.SLAService
}

// NewAppliedSLAHandler creates a new AppliedSLAHandler instance
func NewAppliedSLAHandler(db *gorm.DB, appliedSLARepo *repository.AppliedSLARepository, slaService *service.SLAService) *AppliedSLAHandler {
	return &AppliedSLAHandler{
		db:             db,
		appliedSLARepo: appliedSLARepo,
		slaService:     slaService,
	}
}

func (h *AppliedSLAHandler) getAccountID(c *gin.Context) uint {
	if raw, exists := c.Get(middleware.ContextAccountID); exists {
		if u, ok := raw.(uint); ok && u > 0 {
			return u
		}
	}
	if param := c.Param("account_id"); param != "" {
		if id, err := strconv.ParseUint(param, 10, 32); err == nil && id > 0 {
			return uint(id)
		}
	}
	return 0
}

func (h *AppliedSLAHandler) parseFilter(c *gin.Context) repository.AppliedSLAFilter {
	filter := repository.AppliedSLAFilter{
		Label:     strings.TrimSpace(c.Query("label")),
		SLAStatus: strings.TrimSpace(c.Query("sla_status")),
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	filter.Page = page

	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", c.DefaultQuery("per_page", "25")))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	filter.PageSize = pageSize

	if rawSince := c.DefaultQuery("since", c.Query("from")); rawSince != "" {
		if t, err := time.Parse(time.RFC3339, rawSince); err == nil {
			filter.Since = &t
		}
	}
	if rawUntil := c.DefaultQuery("until", c.Query("to")); rawUntil != "" {
		if t, err := time.Parse(time.RFC3339, rawUntil); err == nil {
			filter.Until = &t
		}
	}

	if rawInboxID := c.Query("inbox_id"); rawInboxID != "" {
		if id, err := strconv.ParseUint(rawInboxID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.InboxID = &u
		}
	}
	if rawTeamID := c.Query("team_id"); rawTeamID != "" {
		if id, err := strconv.ParseUint(rawTeamID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.TeamID = &u
		}
	}
	if rawPolicyID := c.DefaultQuery("sla_policy_id", c.Query("sla_id")); rawPolicyID != "" {
		if id, err := strconv.ParseUint(rawPolicyID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.SLAPolicyID = &u
		}
	}
	if rawAgentID := c.DefaultQuery("assigned_agent_id", c.Query("user_id")); rawAgentID != "" {
		if id, err := strconv.ParseUint(rawAgentID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.AssignedAgentID = &u
		}
	}

	if c.Query("only_missed") == "true" || c.Query("missed") == "true" {
		filter.OnlyMissed = true
	}

	return filter
}

// ListAppliedSLAs lists applied SLAs conforming to Chatwoot upstream response format
func (h *AppliedSLAHandler) ListAppliedSLAs(c *gin.Context) {
	accountID := h.getAccountID(c)
	filter := h.parseFilter(c)

	list, total, err := h.appliedSLARepo.ListAppliedSLAs(accountID, filter)
	if err != nil {
		logger.WithComponent("sla").Error("failed to list applied slas",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list applied SLAs")
		return
	}

	payload := make([]gin.H, 0, len(list))
	for _, item := range list {
		var contactName string
		if item.Conversation != nil && item.Conversation.Contact != nil {
			contactName = item.Conversation.Contact.Name
		}
		var assigneeData any
		if item.Conversation != nil && item.Conversation.Assignee != nil {
			assigneeData = gin.H{
				"id":   item.Conversation.Assignee.ID,
				"name": item.Conversation.Assignee.Name,
			}
		}

		// Calculate due at values if conversation & policy exist
		var frtDueUnix, nrtDueUnix, rtDueUnix *int64
		if item.Conversation != nil && h.slaService != nil {
			frtDue, nrtDue, rtDue, _, _, _, _ := h.slaService.GetConversationSLADeadlines(item.Conversation)
			if frtDue != nil {
				u := frtDue.Unix()
				frtDueUnix = &u
			}
			if nrtDue != nil {
				u := nrtDue.Unix()
				nrtDueUnix = &u
			}
			if rtDue != nil {
				u := rtDue.Unix()
				rtDueUnix = &u
			}
		}

		appliedSLAData := gin.H{
			"id":                                item.ID,
			"sla_id":                            item.SLAPolicyID,
			"sla_status":                        item.SLAStatus,
			"created_at":                        item.CreatedAt.Unix(),
			"updated_at":                        item.UpdatedAt.Unix(),
			"sla_name":                          "",
			"sla_description":                   "",
			"sla_first_response_time_threshold": 0,
			"sla_next_response_time_threshold":  0,
			"sla_resolution_time_threshold":    0,
			"sla_only_during_business_hours":    false,
			"sla_frt_due_at":                    frtDueUnix,
			"sla_nrt_due_at":                    nrtDueUnix,
			"sla_rt_due_at":                     rtDueUnix,
		}

		if item.SLAPolicy != nil {
			appliedSLAData["sla_name"] = item.SLAPolicy.Name
			appliedSLAData["sla_description"] = item.SLAPolicy.Description
			appliedSLAData["sla_first_response_time_threshold"] = item.SLAPolicy.FirstResponseTimeThreshold
			appliedSLAData["sla_next_response_time_threshold"] = item.SLAPolicy.NextResponseTimeThreshold
			appliedSLAData["sla_resolution_time_threshold"] = item.SLAPolicy.ResolutionTimeThreshold
			appliedSLAData["sla_only_during_business_hours"] = item.SLAPolicy.OnlyDuringBusinessHours
		}

		events := make([]gin.H, 0, len(item.SLAEvents))
		for _, ev := range item.SLAEvents {
			events = append(events, gin.H{
				"id":         ev.ID,
				"event_type": ev.EventType,
				"meta":       ev.Meta,
				"created_at": ev.CreatedAt.Unix(),
				"updated_at": ev.UpdatedAt.Unix(),
			})
		}

		payload = append(payload, gin.H{
			"applied_sla": appliedSLAData,
			"conversation": gin.H{
				"id": item.ConversationID,
				"contact": gin.H{
					"name": contactName,
				},
				"assignee": assigneeData,
			},
			"sla_events": events,
		})
	}

	logger.WithComponent("sla").Info("applied slas listed successfully",
		"account_id", accountID,
		"count", len(payload),
		"total", total,
		"page", filter.Page,
	)

	c.JSON(200, gin.H{
		"payload": payload,
		"meta": gin.H{
			"count":        total,
			"current_page": filter.Page,
		},
	})
}

// GetMetrics returns hit-rate and misses statistics
func (h *AppliedSLAHandler) GetMetrics(c *gin.Context) {
	accountID := h.getAccountID(c)
	filter := h.parseFilter(c)

	metrics, err := h.appliedSLARepo.GetMetrics(accountID, filter)
	if err != nil {
		logger.WithComponent("sla").Error("failed to get applied sla metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get applied SLA metrics")
		return
	}

	logger.WithComponent("sla").Info("applied sla metrics retrieved",
		"account_id", accountID,
		"total", metrics.TotalAppliedSLAs,
		"misses", metrics.NumberOfSLAMisses,
		"hit_rate", metrics.HitRate,
	)

	c.JSON(200, metrics)
}

// Download streams a CSV file of breached conversations with UTF-8 BOM
func (h *AppliedSLAHandler) Download(c *gin.Context) {
	accountID := h.getAccountID(c)
	filter := h.parseFilter(c)

	list, err := h.appliedSLARepo.GetMissedAppliedSLAs(accountID, filter)
	if err != nil {
		logger.WithComponent("sla").Error("failed to get missed applied slas for download",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to generate CSV export")
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=breached_conversation.csv")

	// UTF-8 BOM bytes so Windows Excel recognizes Chinese characters properly
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(c.Writer)
	headers := []string{
		"Conversation ID",
		"SLA Policy Breached",
		"Assignee",
		"Team",
		"Inbox",
		"Labels",
		"Conversation Link",
		"Breached Events",
	}
	_ = writer.Write(headers)

	for _, item := range list {
		policyName := ""
		if item.SLAPolicy != nil {
			policyName = item.SLAPolicy.Name
		}
		assigneeName := ""
		teamName := ""
		inboxName := ""
		if item.Conversation != nil {
			if item.Conversation.Assignee != nil {
				assigneeName = item.Conversation.Assignee.Name
			}
			if item.Conversation.Team != nil {
				teamName = item.Conversation.Team.Name
			}
			if item.Conversation.Inbox != nil {
				inboxName = item.Conversation.Inbox.Name
			}
		}

		eventTypes := make([]string, 0, len(item.SLAEvents))
		for _, ev := range item.SLAEvents {
			eventTypes = append(eventTypes, ev.EventType)
		}

		convLink := fmt.Sprintf("/app/accounts/%d/conversations/%d", accountID, item.ConversationID)

		row := []string{
			strconv.Itoa(int(item.ConversationID)),
			policyName,
			assigneeName,
			teamName,
			inboxName,
			"",
			convLink,
			strings.Join(eventTypes, ", "),
		}
		_ = writer.Write(row)
	}

	writer.Flush()

	logger.WithComponent("sla").Info("breached conversation report downloaded successfully",
		"account_id", accountID,
		"records_count", len(list),
	)
}

// GetConversationAppliedSLA returns full SLA compliance details for a specific conversation
func (h *AppliedSLAHandler) GetConversationAppliedSLA(c *gin.Context) {
	accountID := h.getAccountID(c)
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var conv domain.Conversation
	if err := h.db.Preload("Inbox").Where("account_id = ? AND id = ?", accountID, convID).First(&conv).Error; err != nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	applied, _ := h.appliedSLARepo.GetAppliedSLAByConversation(accountID, uint(convID))

	frtDue, nrtDue, resDue, isFRTBreached, isNRTBreached, isResBreached, policy := h.slaService.GetConversationSLADeadlines(&conv)
	now := time.Now().UTC()

	var frtRemainingSec, nrtRemainingSec, resRemainingSec *int
	if frtDue != nil {
		rem := int(frtDue.Sub(now).Seconds())
		if rem < 0 {
			rem = 0
		}
		frtRemainingSec = &rem
	}
	if nrtDue != nil {
		rem := int(nrtDue.Sub(now).Seconds())
		if rem < 0 {
			rem = 0
		}
		nrtRemainingSec = &rem
	}
	if resDue != nil {
		rem := int(resDue.Sub(now).Seconds())
		if rem < 0 {
			rem = 0
		}
		resRemainingSec = &rem
	}

	appliedStatus := "active"
	if applied != nil {
		appliedStatus = applied.SLAStatus
	} else if conv.SLAStatus != "" {
		appliedStatus = conv.SLAStatus
	}

	response.Success(c, gin.H{
		"conversation_id":              conv.ID,
		"sla_status":                   conv.SLAStatus,
		"applied_status":               appliedStatus,
		"applied_sla":                  applied,
		"applied_policy":               policy,
		"first_response_due_at":        frtDue,
		"first_response_breached":      isFRTBreached,
		"first_response_remaining_sec": frtRemainingSec,
		"next_response_due_at":         nrtDue,
		"next_response_breached":       isNRTBreached,
		"next_response_remaining_sec":  nrtRemainingSec,
		"resolution_due_at":            resDue,
		"resolution_breached":          isResBreached,
		"resolution_remaining_sec":     resRemainingSec,
	})
}

// ApplySLARequest defines input to assign an SLA policy to a conversation
type ApplySLARequest struct {
	SLAPolicyID uint `json:"sla_policy_id" binding:"required"`
}

// ApplySLA assigns or switches an SLA policy on a conversation
func (h *AppliedSLAHandler) ApplySLA(c *gin.Context) {
	accountID := h.getAccountID(c)
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req ApplySLARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid SLA policy payload: sla_policy_id is required")
		return
	}

	// Validate policy exists in account
	var policy domain.SLAPolicy
	if err := h.db.Where("account_id = ? AND id = ?", accountID, req.SLAPolicyID).First(&policy).Error; err != nil {
		response.NotFound(c, "SLA policy not found")
		return
	}

	// Validate conversation exists in account
	var conv domain.Conversation
	if err := h.db.Where("account_id = ? AND id = ?", accountID, convID).First(&conv).Error; err != nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	// Update Conversation's SLA policy
	pid := req.SLAPolicyID
	conv.SLAPolicyID = &pid
	_ = h.db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Update("sla_policy_id", pid).Error

	// Ensure AppliedSLA record
	if _, err := h.appliedSLARepo.EnsureAppliedSLA(accountID, uint(convID), pid); err != nil {
		logger.WithComponent("sla").Error("failed to create applied sla",
			"account_id", accountID,
			"conversation_id", convID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to apply SLA")
		return
	}

	// Evaluate conversation immediately
	_, _ = h.slaService.EvaluateConversation(&conv)

	logger.WithComponent("sla").Info("sla policy applied to conversation",
		"account_id", accountID,
		"conversation_id", convID,
		"sla_policy_id", req.SLAPolicyID,
	)

	// Refetch applied SLA with relations
	fullApplied, _ := h.appliedSLARepo.GetAppliedSLAByConversation(accountID, uint(convID))
	response.Success(c, fullApplied)
}

// RemoveSLA unbinds the SLA policy from a conversation
func (h *AppliedSLAHandler) RemoveSLA(c *gin.Context) {
	accountID := h.getAccountID(c)
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	if err := h.appliedSLARepo.DeleteByConversation(accountID, uint(convID)); err != nil {
		logger.WithComponent("sla").Error("failed to remove applied sla",
			"account_id", accountID,
			"conversation_id", convID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to remove applied SLA")
		return
	}

	logger.WithComponent("sla").Info("applied sla removed from conversation",
		"account_id", accountID,
		"conversation_id", convID,
	)

	response.Success(c, gin.H{"deleted": true})
}
