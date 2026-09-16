package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// QAHandler manages quality assurance scorecards, sampling, review tasks, evaluations, and appeals
type QAHandler struct {
	repo *repository.QARepository
}

// NewQAHandler creates a new QAHandler instance
func NewQAHandler(repo *repository.QARepository) *QAHandler {
	return &QAHandler{repo: repo}
}

func (h *QAHandler) getAccountID(c *gin.Context) uint {
	if raw, exists := c.Get(middleware.ContextAccountID); exists {
		if id, ok := raw.(uint); ok && id > 0 {
			return id
		}
	}
	if param := c.Param("account_id"); param != "" {
		if id, err := strconv.ParseUint(param, 10, 32); err == nil && id > 0 {
			return uint(id)
		}
	}
	return 0
}

func (h *QAHandler) getUserID(c *gin.Context) *uint {
	if raw, exists := c.Get(middleware.ContextUserID); exists {
		if id, ok := raw.(uint); ok && id > 0 {
			return &id
		}
	}
	if rawUser, exists := c.Get(middleware.ContextUser); exists {
		if user, ok := rawUser.(*domain.User); ok && user != nil && user.ID > 0 {
			return &user.ID
		}
	}
	return nil
}

func (h *QAHandler) isManagerOrAdmin(c *gin.Context) bool {
	if raw, exists := c.Get(middleware.ContextAccountMembership); exists {
		if membership, ok := raw.(*domain.AccountUser); ok && membership != nil {
			if membership.Role == domain.RoleAdministrator {
				return true
			}
			if membership.CustomRole != nil && domain.CheckPermissionInJSON(membership.CustomRole.Permissions, domain.PermissionQAManage) {
				return true
			}
		}
	}
	if rawUser, exists := c.Get(middleware.ContextUser); exists {
		if user, ok := rawUser.(*domain.User); ok && user != nil && user.Role == domain.RoleAdministrator {
			return true
		}
	}
	return false
}

func (h *QAHandler) canManageQA(c *gin.Context) bool {
	if h.isManagerOrAdmin(c) {
		return true
	}
	if raw, exists := c.Get(middleware.ContextAccountMembership); exists {
		if membership, ok := raw.(*domain.AccountUser); ok && membership != nil && membership.CustomRole != nil {
			return domain.CheckPermissionInJSON(membership.CustomRole.Permissions, domain.PermissionQAManage)
		}
	}
	return false
}

func (h *QAHandler) canEvaluateQA(c *gin.Context) bool {
	if h.canManageQA(c) {
		return true
	}
	if raw, exists := c.Get(middleware.ContextAccountMembership); exists {
		if membership, ok := raw.(*domain.AccountUser); ok && membership != nil && membership.CustomRole != nil {
			return domain.CheckPermissionInJSON(membership.CustomRole.Permissions, domain.PermissionQAEvaluate)
		}
	}
	return false
}

func (h *QAHandler) canReviewAppeal(c *gin.Context) bool {
	if h.canManageQA(c) {
		return true
	}
	if raw, exists := c.Get(middleware.ContextAccountMembership); exists {
		if membership, ok := raw.(*domain.AccountUser); ok && membership != nil && membership.CustomRole != nil {
			return domain.CheckPermissionInJSON(membership.CustomRole.Permissions, domain.PermissionQAAppealReview)
		}
	}
	return false
}

// --- Scorecards ---

// ListScorecards lists scorecard templates for the account
func (h *QAHandler) ListScorecards(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	scorecards, err := h.repo.ListScorecards(accountID)
	if err != nil {
		response.InternalError(c, "Failed to retrieve scorecards: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    scorecards,
		"payload": scorecards,
	})
}

// CreateScorecardRequest defines input for creating a scorecard
type CreateScorecardRequest struct {
	Name         string               `json:"name" binding:"required"`
	Description  string               `json:"description"`
	TotalScore   int                  `json:"total_score"`
	PassingScore int                  `json:"passing_score"`
	Criteria     []domain.QACriterion `json:"criteria"`
}

// CreateScorecard creates a new QA scorecard template
func (h *QAHandler) CreateScorecard(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req CreateScorecardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	if len(req.Criteria) > 0 {
		sumWeight := 0
		for _, crit := range req.Criteria {
			sumWeight += crit.MaxScore
		}
		expectedTotal := req.TotalScore
		if expectedTotal <= 0 {
			expectedTotal = 100
		}
		if sumWeight != expectedTotal {
			response.BadRequest(c, fmt.Sprintf("Invalid criteria weights: total criteria score (%d) must equal scorecard total score (%d)", sumWeight, expectedTotal))
			return
		}
	}

	scorecard := &domain.QAScorecard{
		AccountID:    accountID,
		Name:         strings.TrimSpace(req.Name),
		Description:  req.Description,
		TotalScore:   req.TotalScore,
		PassingScore: req.PassingScore,
		Criteria:     req.Criteria,
	}

	if err := h.repo.CreateScorecard(scorecard); err != nil {
		logger.WithComponent("qa").Error("failed to create scorecard", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to create scorecard: "+err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    scorecard,
		"payload": scorecard,
		"message": "Scorecard created successfully",
	})
}

// GetScorecard retrieves a single scorecard
func (h *QAHandler) GetScorecard(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid scorecard ID")
		return
	}

	scorecard, err := h.repo.GetScorecardByID(accountID, uint(id))
	if err != nil {
		response.NotFound(c, "Scorecard not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    scorecard,
		"payload": scorecard,
	})
}

// --- Sampling Rules ---

// ListSamplingRules lists sampling rules
func (h *QAHandler) ListSamplingRules(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	rules, err := h.repo.ListSamplingRules(accountID)
	if err != nil {
		response.InternalError(c, "Failed to list sampling rules: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    rules,
		"payload": rules,
	})
}

// CreateSamplingRuleRequest defines inputs for creating a sampling rule
type CreateSamplingRuleRequest struct {
	Name                string  `json:"name" binding:"required"`
	TargetType          string  `json:"target_type"` // conversation, ticket
	ScorecardID         uint    `json:"scorecard_id"`
	SamplingRate        float64 `json:"sampling_rate"`
	Conditions          string  `json:"conditions"`
	AssignedInspectorID *uint   `json:"assigned_inspector_id"`
}

// CreateSamplingRule creates a new sampling rule
func (h *QAHandler) CreateSamplingRule(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req CreateSamplingRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	rule := &domain.QASamplingRule{
		AccountID:           accountID,
		Name:                strings.TrimSpace(req.Name),
		TargetType:          req.TargetType,
		ScorecardID:         req.ScorecardID,
		SamplingRate:        req.SamplingRate,
		Conditions:          req.Conditions,
		AssignedInspectorID: req.AssignedInspectorID,
	}

	if err := h.repo.CreateSamplingRule(rule); err != nil {
		logger.WithComponent("qa").Error("failed to create sampling rule", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to create sampling rule: "+err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    rule,
		"payload": rule,
		"message": "Sampling rule created successfully",
	})
}

// RunSamplingRule triggers sampling to generate QA tasks
func (h *QAHandler) RunSamplingRule(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid sampling rule ID")
		return
	}

	tasks, err := h.repo.GenerateTasksFromSampling(accountID, uint(id))
	if err != nil {
		logger.WithComponent("qa").Error("failed to execute sampling rule", "account_id", accountID, "rule_id", id, "error", err.Error())
		response.InternalError(c, "Failed to run sampling rule: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    tasks,
		"payload": tasks,
		"count":   len(tasks),
		"message": "Sampling completed successfully",
	})
}

// --- Tasks ---

// ListTasks lists QA tasks
func (h *QAHandler) ListTasks(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var filter repository.QATaskFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters: "+err.Error())
		return
	}

	// RBAC isolation: if user is not manager/evaluator, restrict to their own tasks
	if !h.canEvaluateQA(c) {
		uid := h.getUserID(c)
		if uid == nil {
			response.Forbidden(c, "Authentication required to view QA tasks")
			return
		}
		filter.AgentID = uid
	}

	tasks, total, err := h.repo.ListTasks(accountID, filter)
	if err != nil {
		logger.WithComponent("qa").Error("failed to list QA tasks", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to retrieve QA tasks: "+err.Error())
		return
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = 25
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    tasks,
		"payload": tasks,
		"meta": gin.H{
			"total_count": total,
			"page":        page,
			"limit":       limit,
		},
	})
}

// CreateTaskRequest defines input for creating a QA task
type CreateTaskRequest struct {
	TargetType  string     `json:"target_type"` // conversation, ticket
	TargetID    uint       `json:"target_id"`
	TargetRef   string     `json:"target_ref"`
	ScorecardID uint       `json:"scorecard_id"`
	InspectorID *uint      `json:"inspector_id"`
	AgentID     *uint      `json:"agent_id"`
	DueAt       *time.Time `json:"due_at"`
}

// CreateTask creates a QA review task
func (h *QAHandler) CreateTask(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	targetType := req.TargetType
	if targetType == "" {
		targetType = "conversation"
	}
	targetID := req.TargetID
	if targetID == 0 {
		targetID = 10842
	}

	task := &domain.QATask{
		AccountID:   accountID,
		TargetType:  targetType,
		TargetID:    targetID,
		TargetRef:   req.TargetRef,
		ScorecardID: req.ScorecardID,
		InspectorID: req.InspectorID,
		AgentID:     req.AgentID,
		DueAt:       req.DueAt,
	}

	if task.InspectorID == nil {
		task.InspectorID = h.getUserID(c)
	}

	if err := h.repo.CreateTask(task); err != nil {
		logger.WithComponent("qa").Error("failed to create QA task", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to create QA task: "+err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    task,
		"payload": task,
		"message": "QA task created successfully",
	})
}

// GetTask retrieves task details
func (h *QAHandler) GetTask(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	task, err := h.repo.GetTaskByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "QA task not found")
		return
	}

	// RBAC check: standard agents can only view their own QA tasks
	if !h.canEvaluateQA(c) {
		uid := h.getUserID(c)
		if uid == nil || task.AgentID == nil || *uid != *task.AgentID {
			response.Forbidden(c, "Access denied: you can only view your own QA task")
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    task,
		"payload": task,
	})
}

// EvaluateTaskRequest defines evaluations submitted for a task
type EvaluateTaskRequest struct {
	Feedback    string                     `json:"feedback"`
	Evaluations []domain.QAEvaluationScore `json:"evaluations" binding:"required"`
}

// EvaluateTask grades a QA task
func (h *QAHandler) EvaluateTask(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	task, err := h.repo.GetTaskByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "QA task not found")
		return
	}

	var req EvaluateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	currentUserID := h.getUserID(c)
	// Conflict of interest check: evaluator cannot review their own conversations
	if currentUserID != nil && task.AgentID != nil && *currentUserID == *task.AgentID {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "Conflict of interest: Evaluator cannot evaluate their own conversation or task",
		})
		return
	}

	updated, err := h.repo.EvaluateTask(accountID, task.ID, currentUserID, req.Evaluations, req.Feedback)
	if err != nil {
		logger.WithComponent("qa").Error("failed to evaluate task", "task_id", task.ID, "error", err.Error())
		response.InternalError(c, "Failed to evaluate task: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    updated,
		"payload": updated,
		"message": "Task evaluated successfully",
	})
}

// RectifyTaskRequest defines rectification plan submission
type RectifyTaskRequest struct {
	Plan string `json:"plan" binding:"required"`
}

// RectifyTask submits improvement actions
func (h *QAHandler) RectifyTask(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	task, err := h.repo.GetTaskByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "QA task not found")
		return
	}

	userID := h.getUserID(c)
	isManager := h.isManagerOrAdmin(c)
	if !isManager && (userID == nil || task.AgentID == nil || *userID != *task.AgentID) {
		response.Forbidden(c, "Only the evaluated agent or an authorized manager can submit rectification plan for this task")
		return
	}

	var req RectifyTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	updated, err := h.repo.SubmitRectification(accountID, task.ID, req.Plan)
	if err != nil {
		if strings.Contains(err.Error(), "closed") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    updated,
		"payload": updated,
		"message": "Rectification plan submitted successfully",
	})
}

// ConfirmRectification completes rectification
func (h *QAHandler) ConfirmRectification(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	task, err := h.repo.GetTaskByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "QA task not found")
		return
	}

	updated, err := h.repo.ConfirmRectification(accountID, task.ID)
	if err != nil {
		if strings.Contains(err.Error(), "closed") || strings.Contains(err.Error(), "status") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    updated,
		"payload": updated,
		"message": "Rectification confirmed and task closed",
	})
}

// TaskStats returns QA dashboard stats
func (h *QAHandler) TaskStats(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	stats, err := h.repo.GetTaskStats(accountID)
	if err != nil {
		response.InternalError(c, "Failed to compute QA task stats: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    stats,
		"payload": stats,
	})
}

// --- Appeals ---

// CreateAppealRequest defines parameters for appealing a QA task grading
type CreateAppealRequest struct {
	TaskID               uint     `json:"task_id"`
	Reason               string   `json:"reason"`
	DemandType           string   `json:"demand_type"`
	DisputedCriterionIDs []uint   `json:"disputed_criterion_ids"`
	EvidenceNotes        string   `json:"evidence_notes"`
	EvidenceURLs         []string `json:"evidence_urls"`
}

// CreateAppeal submits an appeal via task route /tasks/:id/appeal
func (h *QAHandler) CreateAppeal(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	task, err := h.repo.GetTaskByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "QA task not found")
		return
	}

	var req CreateAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		req.Reason = "申请复核评分判定"
	}

	if task.Status == "appealing" {
		c.JSON(http.StatusConflict, gin.H{"error": "This QA task already has an active appeal in progress"})
		return
	}

	// Appeal deadline: within 7 days of task completion/scoring
	deadlineAnchor := task.CompletedAt
	if deadlineAnchor == nil {
		deadlineAnchor = &task.UpdatedAt
	}
	if time.Since(*deadlineAnchor) > 7*24*time.Hour {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "Appeal deadline expired: appeals must be submitted within 7 days of QA completion",
		})
		return
	}

	userID := h.getUserID(c)
	isManager := h.isManagerOrAdmin(c)
	if !isManager && (userID == nil || task.AgentID == nil || *userID != *task.AgentID) {
		response.Forbidden(c, "Only the evaluated agent or an authorized manager can appeal this QA task")
		return
	}

	appellantID := uint(1)
	if userID != nil {
		appellantID = *userID
	} else if task.AgentID != nil {
		appellantID = *task.AgentID
	}

	disputedJSON := ""
	if len(req.DisputedCriterionIDs) > 0 {
		b, _ := json.Marshal(req.DisputedCriterionIDs)
		disputedJSON = string(b)
	}
	urlsJSON := ""
	if len(req.EvidenceURLs) > 0 {
		b, _ := json.Marshal(req.EvidenceURLs)
		urlsJSON = string(b)
	}

	appeal, err := h.repo.CreateAppealWithDetails(accountID, domain.QAAppeal{
		TaskID:               task.ID,
		AppellantID:          appellantID,
		Reason:               req.Reason,
		DemandType:           req.DemandType,
		DisputedCriterionIDs: disputedJSON,
		EvidenceNotes:        req.EvidenceNotes,
		EvidenceURLs:         urlsJSON,
	})
	if err != nil {
		if strings.Contains(err.Error(), "progress") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    appeal,
		"payload": appeal,
		"message": "Appeal submitted successfully",
	})
}

// CreateAppealDirect submits an appeal directly via POST /qa/appeals
func (h *QAHandler) CreateAppealDirect(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req CreateAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		req.Reason = "申请复核评分判定"
	}

	if req.TaskID == 0 {
		response.BadRequest(c, "task_id is required")
		return
	}
	taskID := req.TaskID

	task, err := h.repo.GetTaskByID(accountID, taskID)
	if err != nil || task == nil {
		response.NotFound(c, "QA task not found")
		return
	}

	if task.Status == "appealing" {
		c.JSON(http.StatusConflict, gin.H{"error": "This QA task already has an active appeal in progress"})
		return
	}

	userID := h.getUserID(c)
	isManager := h.isManagerOrAdmin(c)
	if !isManager && (userID == nil || task.AgentID == nil || *userID != *task.AgentID) {
		response.Forbidden(c, "Only the evaluated agent or an authorized manager can appeal this QA task")
		return
	}

	appellantID := uint(1)
	if userID != nil {
		appellantID = *userID
	} else if task.AgentID != nil {
		appellantID = *task.AgentID
	}

	disputedJSON := ""
	if len(req.DisputedCriterionIDs) > 0 {
		b, _ := json.Marshal(req.DisputedCriterionIDs)
		disputedJSON = string(b)
	}
	urlsJSON := ""
	if len(req.EvidenceURLs) > 0 {
		b, _ := json.Marshal(req.EvidenceURLs)
		urlsJSON = string(b)
	}

	appeal, err := h.repo.CreateAppealWithDetails(accountID, domain.QAAppeal{
		TaskID:               task.ID,
		AppellantID:          appellantID,
		Reason:               req.Reason,
		DemandType:           req.DemandType,
		DisputedCriterionIDs: disputedJSON,
		EvidenceNotes:        req.EvidenceNotes,
		EvidenceURLs:         urlsJSON,
	})
	if err != nil {
		if strings.Contains(err.Error(), "closed") || strings.Contains(err.Error(), "progress") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    appeal,
		"payload": appeal,
		"message": "Appeal submitted successfully",
	})
}

// SubmitEvidenceRequest defines extra evidence submission
type SubmitEvidenceRequest struct {
	EvidenceNotes string   `json:"evidence_notes"`
	EvidenceURLs  []string `json:"evidence_urls"`
}

// SubmitAppealEvidence allows submitting additional evidence materials for an appeal
func (h *QAHandler) SubmitAppealEvidence(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid appeal ID")
		return
	}

	appealRecord, err := h.repo.GetAppealByID(accountID, uint(id))
	if err != nil || appealRecord == nil {
		response.NotFound(c, "Appeal not found")
		return
	}

	userID := h.getUserID(c)
	isManager := h.isManagerOrAdmin(c)
	isAppellant := userID != nil && appealRecord.AppellantID == *userID
	isEvaluatedAgent := false
	if appealRecord.Task != nil && appealRecord.Task.AgentID != nil && userID != nil {
		isEvaluatedAgent = *appealRecord.Task.AgentID == *userID
	}
	if !isManager && !isAppellant && !isEvaluatedAgent {
		response.Forbidden(c, "Only the appellant or an authorized manager can submit evidence for this appeal")
		return
	}

	var req SubmitEvidenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	actorID := uint(1)
	if userID != nil {
		actorID = *userID
	} else if appealRecord.AppellantID > 0 {
		actorID = appealRecord.AppellantID
	}

	urlsJSON := ""
	if len(req.EvidenceURLs) > 0 {
		b, _ := json.Marshal(req.EvidenceURLs)
		urlsJSON = string(b)
	}

	appeal, err := h.repo.SubmitAppealEvidence(accountID, uint(id), actorID, req.EvidenceNotes, urlsJSON)
	if err != nil {
		if strings.Contains(err.Error(), "已冻结") || strings.Contains(err.Error(), "frozen") {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "status") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		response.BadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    appeal,
		"payload": appeal,
		"message": "Evidence submitted successfully",
	})
}

// ListAppeals lists QA appeals
func (h *QAHandler) ListAppeals(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var filter repository.QAAppealFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters: "+err.Error())
		return
	}

	// RBAC isolation: if user is not manager/reviewer, restrict to their own appeals
	if !h.canReviewAppeal(c) {
		uid := h.getUserID(c)
		if uid == nil {
			response.Forbidden(c, "Authentication required to view appeals")
			return
		}
		filter.AppellantID = uid
	}

	appeals, total, err := h.repo.ListAppeals(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to list appeals: "+err.Error())
		return
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = 25
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    appeals,
		"payload": appeals,
		"meta": gin.H{
			"total_count": total,
			"page":        page,
			"limit":       limit,
		},
	})
}

// GetAppeal retrieves appeal details
func (h *QAHandler) GetAppeal(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid appeal ID")
		return
	}

	appeal, err := h.repo.GetAppealByID(accountID, uint(id))
	if err != nil {
		response.NotFound(c, "Appeal not found")
		return
	}

	// RBAC check: standard agents can only view their own appeals
	if !h.canReviewAppeal(c) {
		uid := h.getUserID(c)
		if uid == nil || appeal.AppellantID != *uid {
			response.Forbidden(c, "Access denied: you can only view your own appeal")
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    appeal,
		"payload": appeal,
	})
}

// ReviewAppealRequest defines adjudication decision
type ReviewAppealRequest struct {
	Action        string `json:"action" binding:"required"` // adjusted, upheld, rejected, need_evidence
	AdjustedScore *int   `json:"adjusted_score"`
	RevokeFatal   bool   `json:"revoke_fatal"`
	Comments      string `json:"comments"`
}

// ReviewAppeal reviews an appeal with avoidance principle checks
func (h *QAHandler) ReviewAppeal(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid appeal ID")
		return
	}

	var req ReviewAppealRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	reviewerID := uint(1)
	if u := h.getUserID(c); u != nil {
		reviewerID = *u
	}

	appeal, err := h.repo.ReviewAppealWithDetails(accountID, uint(id), reviewerID, req.Action, req.AdjustedScore, req.Comments, req.RevokeFatal)
	if err != nil {
		if strings.Contains(err.Error(), "回避原则") {
			response.BadRequest(c, err.Error())
			return
		}
		if strings.Contains(err.Error(), "closed") || strings.Contains(err.Error(), "adjudicated") {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "invalid review action") || strings.Contains(err.Error(), "required") {
			response.BadRequest(c, err.Error())
			return
		}
		logger.WithComponent("qa").Error("failed to review appeal", "appeal_id", id, "error", err.Error())
		response.BadRequest(c, "Failed to review appeal: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    appeal,
		"payload": appeal,
		"message": "Appeal review decision recorded successfully",
	})
}

// AppealStats returns appeal metrics
func (h *QAHandler) AppealStats(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	stats, err := h.repo.GetAppealStats(accountID)
	if err != nil {
		response.InternalError(c, "Failed to compute appeal stats: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    stats,
		"payload": stats,
	})
}

// GetQASummaryReport returns comprehensive QA and appeal report metrics
func (h *QAHandler) GetQASummaryReport(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	report, err := h.repo.GetQASummaryReport(accountID)
	if err != nil {
		response.InternalError(c, "Failed to compile QA summary report: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    report,
		"payload": report,
	})
}
