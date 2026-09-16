package handler

import (
	"net/http"
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

type AddInboxesToPolicyRequest struct {
	InboxID  *uint  `json:"inbox_id"`
	InboxIDs []uint `json:"inbox_ids"`
	Reassign *bool  `json:"reassign"`
}

func (h *AssignmentPolicyHandler) ListInboxes(c *gin.Context) {
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

	inboxes, err := h.repo.ListInboxes(accountID, uint(id))
	if err != nil {
		logger.WithComponent("assignment_policy").Error("failed to list inboxes for policy",
			"account_id", accountID,
			"policy_id", id,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list inboxes: "+err.Error())
		return
	}

	if inboxes == nil {
		inboxes = []domain.Inbox{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"inboxes": inboxes,
		},
		"inboxes": inboxes,
	})
}

func (h *AssignmentPolicyHandler) AddInboxes(c *gin.Context) {
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

	var req AddInboxesToPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	var targetIDs []uint
	if req.InboxID != nil && *req.InboxID > 0 {
		targetIDs = append(targetIDs, *req.InboxID)
	}
	for _, iid := range req.InboxIDs {
		if iid > 0 {
			already := false
			for _, existing := range targetIDs {
				if existing == iid {
					already = true
					break
				}
			}
			if !already {
				targetIDs = append(targetIDs, iid)
			}
		}
	}

	if len(targetIDs) == 0 {
		response.BadRequest(c, "inbox_id or inbox_ids is required")
		return
	}

	// Validate inboxes belong to this account
	var targetInboxes []domain.Inbox
	if err := h.repo.DB().Model(&domain.Inbox{}).
		Where("account_id = ? AND id IN (?)", accountID, targetIDs).
		Find(&targetInboxes).Error; err != nil || len(targetInboxes) == 0 {
		response.NotFound(c, "Inbox not found")
		return
	}

	allowReassign := (req.Reassign != nil && *req.Reassign) || c.Query("reassign") == "true"
	if !allowReassign {
		var conflictNames []string
		for _, ibx := range targetInboxes {
			if ibx.AssignmentPolicyID != nil && *ibx.AssignmentPolicyID != 0 && *ibx.AssignmentPolicyID != uint(id) {
				conflictNames = append(conflictNames, ibx.Name)
			}
		}
		if len(conflictNames) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "Inbox already associated with another assignment policy",
				"message": "同一 Inbox 不能被多个冲突策略重复关联，请先解绑或设置 reassign=true 重新分配",
				"details": gin.H{
					"conflict_inboxes": conflictNames,
				},
			})
			return
		}
	}

	inboxes, err := h.repo.AddInboxes(accountID, uint(id), targetIDs)
	if err != nil {
		logger.WithComponent("assignment_policy").Error("failed to add inboxes to policy",
			"account_id", accountID,
			"policy_id", id,
			"inbox_ids", targetIDs,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to add inboxes to policy: "+err.Error())
		return
	}

	singleInboxID := uint(0)
	if len(targetIDs) == 1 {
		singleInboxID = targetIDs[0]
	}

	c.JSON(http.StatusOK, gin.H{
		"success":              true,
		"id":                   policy.ID,
		"inbox_id":             singleInboxID,
		"assignment_policy_id": policy.ID,
		"inboxes":              inboxes,
		"data": gin.H{
			"id":                   policy.ID,
			"inbox_id":             singleInboxID,
			"assignment_policy_id": policy.ID,
			"inboxes":              inboxes,
		},
	})
}

func (h *AssignmentPolicyHandler) RemoveInbox(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid assignment policy ID")
		return
	}

	inboxID, err := strconv.ParseUint(c.Param("inbox_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}

	policy, err := h.repo.FindByID(accountID, uint(id))
	if err != nil || policy == nil {
		response.NotFound(c, "Assignment policy not found")
		return
	}

	// Verify inbox belongs to this account
	var inbox domain.Inbox
	if err := h.repo.DB().Where("account_id = ? AND id = ?", accountID, uint(inboxID)).First(&inbox).Error; err != nil {
		response.NotFound(c, "Inbox not found")
		return
	}

	if err := h.repo.RemoveInbox(accountID, uint(id), uint(inboxID)); err != nil {
		logger.WithComponent("assignment_policy").Error("failed to remove inbox from policy",
			"account_id", accountID,
			"policy_id", id,
			"inbox_id", inboxID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to remove inbox from policy: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":              true,
		"deleted":              true,
		"inbox_id":             uint(inboxID),
		"assignment_policy_id": uint(id),
		"data": gin.H{
			"deleted":              true,
			"inbox_id":             uint(inboxID),
			"assignment_policy_id": uint(id),
		},
	})
}

func (h *AssignmentPolicyHandler) RemoveInboxes(c *gin.Context) {
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

	var req struct {
		InboxID  *uint  `json:"inbox_id"`
		InboxIDs []uint `json:"inbox_ids"`
	}
	_ = c.ShouldBindJSON(&req)

	var targetIDs []uint
	if queryInboxID := c.Query("inbox_id"); queryInboxID != "" {
		if qid, err := strconv.ParseUint(queryInboxID, 10, 64); err == nil && qid > 0 {
			targetIDs = append(targetIDs, uint(qid))
		}
	}
	if req.InboxID != nil && *req.InboxID > 0 {
		targetIDs = append(targetIDs, *req.InboxID)
	}
	for _, iid := range req.InboxIDs {
		if iid > 0 {
			targetIDs = append(targetIDs, iid)
		}
	}

	if err := h.repo.RemoveInboxes(accountID, uint(id), targetIDs); err != nil {
		logger.WithComponent("assignment_policy").Error("failed to remove inboxes from policy",
			"account_id", accountID,
			"policy_id", id,
			"inbox_ids", targetIDs,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to remove inboxes from policy: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":              true,
		"deleted":              true,
		"assignment_policy_id": uint(id),
		"data": gin.H{
			"deleted":              true,
			"assignment_policy_id": uint(id),
		},
	})
}
