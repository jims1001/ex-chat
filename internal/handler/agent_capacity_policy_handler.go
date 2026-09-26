package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AgentCapacityPolicyHandler struct {
	repo        *repository.AgentCapacityPolicyRepository
	accountRepo *repository.AccountRepository
	inboxRepo   *repository.InboxRepository
}

func (h *AgentCapacityPolicyHandler) SetOwnerRepositories(accounts *repository.AccountRepository, inboxes *repository.InboxRepository) {
	h.accountRepo = accounts
	h.inboxRepo = inboxes
}

func (h *AgentCapacityPolicyHandler) hydratePolicy(accountID uint, policy *domain.AgentCapacityPolicy) error {
	if h.accountRepo == nil || h.inboxRepo == nil {
		return errors.New("capacity policy owner repositories are not configured")
	}
	limits, err := h.repo.ListInboxLimits(policy.ID)
	if err != nil {
		return err
	}
	ids := make([]uint, 0, len(limits))
	for i := range limits {
		ids = append(ids, limits[i].InboxID)
	}
	if len(ids) > 0 {
		inboxes, err := h.inboxRepo.FindByIDs(accountID, ids)
		if err != nil {
			return err
		}
		byID := make(map[uint]*domain.Inbox, len(inboxes))
		for i := range inboxes {
			byID[inboxes[i].ID] = &inboxes[i]
		}
		for i := range limits {
			inbox := byID[limits[i].InboxID]
			if inbox == nil {
				return gorm.ErrRecordNotFound
			}
			limits[i].Inbox = inbox
		}
	}
	count, err := h.accountRepo.CountCapacityPolicyMembers(accountID, policy.ID)
	if err != nil {
		return err
	}
	policy.InboxCapacityLimits = limits
	policy.UsersCount = int(count)
	return nil
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
		logger.WithComponent("capacity_policy").Error("failed to list capacity policies",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list capacity policies: "+err.Error())
		return
	}
	for i := range policies {
		if err := h.hydratePolicy(accountID, &policies[i]); err != nil {
			response.InternalError(c, "Failed to load capacity policy owners")
			return
		}
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
		if h.accountRepo == nil {
			response.InternalError(c, "Account repository is not configured")
			return
		}
		membership, err := h.accountRepo.GetMembership(accountID, req.UserID)
		if err != nil {
			response.InternalError(c, "Failed to validate account member")
			return
		}
		if membership == nil {
			response.BadRequest(c, "User is not a member of this account")
			return
		}
		legacyPolicy := domain.CapacityPolicy{
			AccountID:         accountID,
			UserID:            req.UserID,
			ConversationLimit: req.ConversationLimit,
		}
		if err := h.repo.SaveLegacyCapacityPolicy(&legacyPolicy); err != nil {
			logger.WithComponent("capacity_policy").Error("failed to save legacy capacity policy",
				"account_id", accountID,
				"user_id", req.UserID,
				"error", err.Error(),
			)
			response.InternalError(c, "Failed to save capacity policy: "+err.Error())
			return
		}

		logger.WithComponent("capacity_policy").Info("saved legacy capacity policy",
			"account_id", accountID,
			"user_id", req.UserID,
			"conversation_limit", req.ConversationLimit,
		)

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
		logger.WithComponent("capacity_policy").Error("failed to create capacity policy",
			"account_id", accountID,
			"name", req.Name,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create capacity policy: "+err.Error())
		return
	}

	logger.WithComponent("capacity_policy").Info("capacity policy created successfully",
		"account_id", accountID,
		"policy_id", policy.ID,
		"name", policy.Name,
	)

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
	if err := h.hydratePolicy(accountID, policy); err != nil {
		response.InternalError(c, "Failed to load capacity policy owners")
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

	policy, err := h.repo.FindSummaryByID(accountID, uint(id))
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
		logger.WithComponent("capacity_policy").Error("failed to update capacity policy",
			"account_id", accountID,
			"policy_id", uint(id),
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update capacity policy: "+err.Error())
		return
	}
	if err := h.hydratePolicy(accountID, policy); err != nil {
		response.InternalError(c, "Failed to load capacity policy owners")
		return
	}

	logger.WithComponent("capacity_policy").Info("capacity policy updated successfully",
		"account_id", accountID,
		"policy_id", policy.ID,
		"name", policy.Name,
	)

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

	policy, err := h.repo.FindSummaryByID(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to get capacity policy: "+err.Error())
		return
	}
	if policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}

	if h.accountRepo == nil {
		response.InternalError(c, "Account repository is not configured")
		return
	}
	if err := h.accountRepo.ClearCapacityPolicy(accountID, uint(id)); err != nil {
		response.InternalError(c, "Failed to clear capacity policy members")
		return
	}
	if err := h.repo.Delete(accountID, uint(id)); err != nil {
		logger.WithComponent("capacity_policy").Error("failed to delete capacity policy",
			"account_id", accountID,
			"policy_id", uint(id),
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete capacity policy: "+err.Error())
		return
	}

	logger.WithComponent("capacity_policy").Info("capacity policy deleted successfully",
		"account_id", accountID,
		"policy_id", uint(id),
	)

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

	if h.accountRepo == nil {
		response.InternalError(c, "Account repository is not configured")
		return
	}
	members, err := h.accountRepo.ListCapacityPolicyMembers(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to list policy members: "+err.Error())
		return
	}

	users := make([]domain.User, 0, len(members))
	for _, member := range members {
		if member.User != nil {
			users = append(users, *member.User)
		}
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

	policy, err := h.repo.FindSummaryByID(accountID, uint(id))
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

	if h.accountRepo == nil {
		response.InternalError(c, "Account repository is not configured")
		return
	}
	if err := h.accountRepo.AssignCapacityPolicy(accountID, req.UserID, uint(id)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.BadRequest(c, "User is not a member of this account")
			return
		}
		logger.WithComponent("capacity_policy").Error("failed to assign user to capacity policy",
			"account_id", accountID,
			"policy_id", uint(id),
			"user_id", req.UserID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to assign user to capacity policy: "+err.Error())
		return
	}

	logger.WithComponent("capacity_policy").Info("user assigned to capacity policy successfully",
		"account_id", accountID,
		"policy_id", uint(id),
		"user_id", req.UserID,
	)

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

	if h.accountRepo == nil {
		response.InternalError(c, "Account repository is not configured")
		return
	}
	if err := h.accountRepo.RemoveCapacityPolicy(accountID, uint(userID), uint(id)); err != nil {
		logger.WithComponent("capacity_policy").Warn("failed to remove user from capacity policy",
			"account_id", accountID,
			"policy_id", uint(id),
			"user_id", uint(userID),
			"error", err.Error(),
		)
		response.NotFound(c, "User is not assigned to this capacity policy")
		return
	}

	logger.WithComponent("capacity_policy").Info("user removed from capacity policy successfully",
		"account_id", accountID,
		"policy_id", uint(id),
		"user_id", uint(userID),
	)

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

	policy, err := h.repo.FindSummaryByID(accountID, uint(id))
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

	if h.inboxRepo == nil {
		response.InternalError(c, "Inbox repository is not configured")
		return
	}
	inbox, err := h.inboxRepo.FindSummaryByID(accountID, req.InboxID)
	if err != nil {
		response.InternalError(c, "Failed to validate inbox")
		return
	}
	if inbox == nil {
		response.BadRequest(c, "Inbox does not belong to this account")
		return
	}
	limit, err := h.repo.CreateInboxLimit(uint(id), req.InboxID, *req.ConversationLimit)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicateInboxLimit) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"success": false,
				"error":   "Inbox is already assigned to this capacity policy",
			})
			return
		}
		logger.WithComponent("capacity_policy").Error("failed to create inbox capacity limit",
			"account_id", accountID,
			"policy_id", uint(id),
			"inbox_id", req.InboxID,
			"error", err.Error(),
		)
		response.BadRequest(c, "Failed to create inbox limit: "+err.Error())
		return
	}
	limit.Inbox = inbox

	logger.WithComponent("capacity_policy").Info("created inbox capacity limit",
		"account_id", accountID,
		"policy_id", uint(id),
		"inbox_id", req.InboxID,
		"conversation_limit", *req.ConversationLimit,
	)

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
	policy, err := h.repo.FindSummaryByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}
	current, err := h.repo.FindInboxLimitByID(uint(id), uint(limitID))
	if err != nil || current == nil {
		response.NotFound(c, "Inbox capacity limit not found for this policy")
		return
	}
	if h.inboxRepo == nil {
		response.InternalError(c, "Inbox repository is not configured")
		return
	}
	inbox, err := h.inboxRepo.FindSummaryByID(accountID, current.InboxID)
	if err != nil {
		response.InternalError(c, "Failed to validate inbox")
		return
	}
	if inbox == nil {
		response.NotFound(c, "Inbox capacity limit not found for this account")
		return
	}

	limit, err := h.repo.UpdateInboxLimit(uint(id), uint(limitID), req.ConversationLimit)
	if err != nil {
		logger.WithComponent("capacity_policy").Warn("inbox capacity limit not found for policy update",
			"account_id", accountID,
			"policy_id", uint(id),
			"limit_id", uint(limitID),
		)
		response.NotFound(c, "Inbox capacity limit not found for this policy")
		return
	}
	limit.Inbox = inbox

	logger.WithComponent("capacity_policy").Info("updated inbox capacity limit",
		"account_id", accountID,
		"policy_id", uint(id),
		"limit_id", uint(limitID),
		"conversation_limit", req.ConversationLimit,
	)

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

	policy, err := h.repo.FindSummaryByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Capacity policy not found")
		return
	}
	if err := h.repo.DeleteInboxLimit(uint(id), uint(limitID)); err != nil {
		logger.WithComponent("capacity_policy").Warn("inbox capacity limit not found for deletion",
			"account_id", accountID,
			"policy_id", uint(id),
			"limit_id", uint(limitID),
		)
		response.NotFound(c, "Inbox capacity limit not found for this policy")
		return
	}

	logger.WithComponent("capacity_policy").Info("deleted inbox capacity limit",
		"account_id", accountID,
		"policy_id", uint(id),
		"limit_id", uint(limitID),
	)

	response.Success(c, gin.H{"deleted": true})
}
