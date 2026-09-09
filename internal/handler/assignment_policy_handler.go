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

type AssignmentPolicyHandler struct {
	repo *repository.AssignmentPolicyRepository
}

func NewAssignmentPolicyHandler(repo *repository.AssignmentPolicyRepository) *AssignmentPolicyHandler {
	return &AssignmentPolicyHandler{repo: repo}
}

type CreateAssignmentPolicyRequest struct {
	Name               string `json:"name" binding:"required"`
	Description        string `json:"description"`
	StrategyType       string `json:"strategy_type"`
	Enabled            *bool  `json:"enabled"`
	WorkingHoursOnly   *bool  `json:"working_hours_only"`
	AgentCapacityLimit int    `json:"agent_capacity_limit"`
	FallbackAssigneeID *uint  `json:"fallback_assignee_id"`
	FallbackTeamID     *uint  `json:"fallback_team_id"`
	RuleConfig         string `json:"rule_config"`
}

type UpdateAssignmentPolicyRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	StrategyType       string `json:"strategy_type"`
	Enabled            *bool  `json:"enabled"`
	WorkingHoursOnly   *bool  `json:"working_hours_only"`
	AgentCapacityLimit *int   `json:"agent_capacity_limit"`
	FallbackAssigneeID *uint  `json:"fallback_assignee_id"`
	FallbackTeamID     *uint  `json:"fallback_team_id"`
	RuleConfig         string `json:"rule_config"`
}

func (h *AssignmentPolicyHandler) List(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	policies, err := h.repo.ListByAccount(accountID)
	if err != nil {
		logger.WithComponent("assignment_policy").Error("failed to list assignment policies",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list assignment policies: "+err.Error())
		return
	}

	response.Success(c, policies)
}

func (h *AssignmentPolicyHandler) Create(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateAssignmentPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	strategy := req.StrategyType
	if strategy == "" {
		strategy = "round_robin"
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	workingHoursOnly := false
	if req.WorkingHoursOnly != nil {
		workingHoursOnly = *req.WorkingHoursOnly
	}

	policy := domain.AssignmentPolicy{
		AccountID:          accountID,
		Name:               req.Name,
		Description:        req.Description,
		StrategyType:       strategy,
		Enabled:            enabled,
		WorkingHoursOnly:   workingHoursOnly,
		AgentCapacityLimit: req.AgentCapacityLimit,
		FallbackAssigneeID: req.FallbackAssigneeID,
		FallbackTeamID:     req.FallbackTeamID,
		RuleConfig:         req.RuleConfig,
	}

	if err := h.repo.Create(&policy); err != nil {
		logger.WithComponent("assignment_policy").Error("failed to create assignment policy",
			"account_id", accountID,
			"name", req.Name,
			"strategy", strategy,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create assignment policy: "+err.Error())
		return
	}

	logger.WithComponent("assignment_policy").Info("assignment policy created successfully",
		"account_id", accountID,
		"policy_id", policy.ID,
		"name", policy.Name,
		"strategy", policy.StrategyType,
	)

	created, _ := h.repo.FindByID(accountID, policy.ID)
	response.Created(c, created)
}

func (h *AssignmentPolicyHandler) Get(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid assignment policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Assignment policy not found")
		return
	}

	response.Success(c, policy)
}

func (h *AssignmentPolicyHandler) Update(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid assignment policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Assignment policy not found")
		return
	}

	var req UpdateAssignmentPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Name != "" {
		policy.Name = req.Name
	}
	if req.Description != "" {
		policy.Description = req.Description
	}
	if req.StrategyType != "" {
		policy.StrategyType = req.StrategyType
	}
	if req.Enabled != nil {
		policy.Enabled = *req.Enabled
	}
	if req.WorkingHoursOnly != nil {
		policy.WorkingHoursOnly = *req.WorkingHoursOnly
	}
	if req.AgentCapacityLimit != nil {
		policy.AgentCapacityLimit = *req.AgentCapacityLimit
	}
	if req.FallbackAssigneeID != nil {
		policy.FallbackAssigneeID = req.FallbackAssigneeID
	}
	if req.FallbackTeamID != nil {
		policy.FallbackTeamID = req.FallbackTeamID
	}
	if req.RuleConfig != "" {
		policy.RuleConfig = req.RuleConfig
	}

	if err := h.repo.Update(policy); err != nil {
		logger.WithComponent("assignment_policy").Error("failed to update assignment policy",
			"account_id", accountID,
			"policy_id", uint(id),
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update assignment policy: "+err.Error())
		return
	}

	logger.WithComponent("assignment_policy").Info("assignment policy updated successfully",
		"account_id", accountID,
		"policy_id", policy.ID,
		"name", policy.Name,
	)

	updated, _ := h.repo.FindByID(accountID, policy.ID)
	response.Success(c, updated)
}

func (h *AssignmentPolicyHandler) Delete(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid assignment policy ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Assignment policy not found")
		return
	}

	if err := h.repo.Delete(accountID, uint(id)); err != nil {
		logger.WithComponent("assignment_policy").Error("failed to delete assignment policy",
			"account_id", accountID,
			"policy_id", uint(id),
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete assignment policy: "+err.Error())
		return
	}

	logger.WithComponent("assignment_policy").Info("assignment policy deleted successfully",
		"account_id", accountID,
		"policy_id", uint(id),
	)

	response.Success(c, gin.H{"id": uint(id), "deleted": true})
}
