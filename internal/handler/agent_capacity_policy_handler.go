package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type AgentCapacityPolicyHandler struct {
	repo *repository.AgentCapacityPolicyRepository
}

func NewAgentCapacityPolicyHandler(repo *repository.AgentCapacityPolicyRepository) *AgentCapacityPolicyHandler {
	return &AgentCapacityPolicyHandler{repo: repo}
}

// Policy CRUD

func (h *AgentCapacityPolicyHandler) List(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	policies, err := h.repo.ListByAccount(accountID)
	if err != nil {
		response.InternalError(c, "Failed to list capacity policies: "+err.Error())
		return
	}

	response.Success(c, policies)
}

func (h *AgentCapacityPolicyHandler) Create(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req struct {
		Name              string `json:"name"`
		Description       string `json:"description"`
		ExclusionRules    string `json:"exclusion_rules"`
		UserID            uint   `json:"user_id"`
		ConversationLimit int    `json:"conversation_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	// Graceful fallback for legacy payload: {"user_id": ..., "conversation_limit": ...}
	if strings.TrimSpace(req.Name) == "" && req.UserID > 0 {
		legacyPolicy := domain.CapacityPolicy{
			AccountID:         accountID,
			UserID:            req.UserID,
			ConversationLimit: req.ConversationLimit,
		}
		if err := h.repo.SaveLegacyCapacityPolicy(&legacyPolicy); err != nil {
			response.InternalError(c, "Failed to save capacity policy: "+err.Error())
			return
		}
		response.Success(c, legacyPolicy)
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		response.BadRequest(c, "Policy name cannot be empty")
		return
	}

	policy := domain.AgentCapacityPolicy{
		AccountID:      accountID,
		Name:           strings.TrimSpace(req.Name),
		Description:    req.Description,
		ExclusionRules: req.ExclusionRules,
	}

	if err := h.repo.Create(&policy); err != nil {
		response.InternalError(c, "Failed to create capacity policy: "+err.Error())
		return
	}

	response.Created(c, policy)
}

func (h *AgentCapacityPolicyHandler) Show(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to get capacity policy: "+err.Error())
		return
	}
	if policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}

	response.Success(c, policy)
}

func (h *AgentCapacityPolicyHandler) Update(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to get capacity policy: "+err.Error())
		return
	}
	if policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}

	var req struct {
		Name           *string `json:"name"`
		Description    *string `json:"description"`
		ExclusionRules *string `json:"exclusion_rules"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			response.BadRequest(c, "Policy name cannot be empty")
			return
		}
		policy.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		policy.Description = *req.Description
	}
	if req.ExclusionRules != nil {
		policy.ExclusionRules = *req.ExclusionRules
	}

	if err := h.repo.Update(policy); err != nil {
		response.InternalError(c, "Failed to update capacity policy: "+err.Error())
		return
	}

	response.Success(c, policy)
}

func (h *AgentCapacityPolicyHandler) Delete(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to get capacity policy: "+err.Error())
		return
	}
	if policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}

	if err := h.repo.Delete(accountID, uint(id)); err != nil {
		response.InternalError(c, "Failed to delete capacity policy: "+err.Error())
		return
	}

	response.Success(c, gin.H{"deleted": true})
}

// Policy Member Management

func (h *AgentCapacityPolicyHandler) ListUsers(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	users, err := h.repo.ListUsers(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to list policy members: "+err.Error())
		return
	}

	response.Success(c, users)
}

func (h *AgentCapacityPolicyHandler) AddUser(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}

	var req struct {
		UserID uint `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if err := h.repo.AddUser(accountID, uint(id), req.UserID); err != nil {
		if errors.Is(err, repository.ErrUserNotInAccount) {
			response.BadRequest(c, "User is not a member of this account")
			return
		}
		response.InternalError(c, "Failed to assign user to capacity policy: "+err.Error())
		return
	}

	response.Success(c, gin.H{"assigned": true, "user_id": req.UserID, "agent_capacity_policy_id": id})
}

func (h *AgentCapacityPolicyHandler) RemoveUser(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	userID, err := strconv.ParseUint(c.Param("user_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	if err := h.repo.RemoveUser(accountID, uint(id), uint(userID)); err != nil {
		response.NotFound(c, "User is not assigned to this capacity policy")
		return
	}

	response.Success(c, gin.H{"removed": true, "user_id": userID})
}

// Per-Inbox Capacity Limits

func (h *AgentCapacityPolicyHandler) CreateInboxLimit(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}

	var req struct {
		InboxID           uint `json:"inbox_id" binding:"required"`
		ConversationLimit *int `json:"conversation_limit" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if *req.ConversationLimit < 0 {
		response.BadRequest(c, "Conversation limit cannot be negative")
		return
	}

	limit, err := h.repo.CreateInboxLimit(accountID, uint(id), req.InboxID, *req.ConversationLimit)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicateInboxLimit) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"success": false,
				"error":   "Inbox is already assigned to this capacity policy",
			})
			return
		}
		response.BadRequest(c, "Failed to create inbox limit: "+err.Error())
		return
	}

	response.Created(c, limit)
}

func (h *AgentCapacityPolicyHandler) UpdateInboxLimit(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	limitID, err := strconv.ParseUint(c.Param("limit_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid limit ID")
		return
	}

	var req struct {
		ConversationLimit int `json:"conversation_limit" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.ConversationLimit < 0 {
		response.BadRequest(c, "Conversation limit cannot be negative")
		return
	}

	limit, err := h.repo.UpdateInboxLimit(accountID, uint(id), uint(limitID), req.ConversationLimit)
	if err != nil {
		response.NotFound(c, "Inbox capacity limit not found for this policy")
		return
	}

	response.Success(c, limit)
}

func (h *AgentCapacityPolicyHandler) DeleteInboxLimit(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid policy ID")
		return
	}

	limitID, err := strconv.ParseUint(c.Param("limit_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid limit ID")
		return
	}

	if err := h.repo.DeleteInboxLimit(accountID, uint(id), uint(limitID)); err != nil {
		response.NotFound(c, "Inbox capacity limit not found for this policy")
		return
	}

	response.Success(c, gin.H{"deleted": true})
}
