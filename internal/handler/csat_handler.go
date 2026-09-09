package handler

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// CSATHandler manages CSAT survey details, moderation, report generation and lifecycle
type CSATHandler struct {
	csatRepo *repository.CSATExtensionRepository
}

// NewCSATHandler creates a new handler instance
func NewCSATHandler(csatRepo *repository.CSATExtensionRepository) *CSATHandler {
	return &CSATHandler{csatRepo: csatRepo}
}

func (h *CSATHandler) getAccountID(c *gin.Context) uint {
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

func (h *CSATHandler) getUserID(c *gin.Context) uint {
	if raw, exists := c.Get(middleware.ContextUserID); exists {
		if u, ok := raw.(uint); ok && u > 0 {
			return u
		}
	}
	return 1
}

// ListSurveys lists CSAT surveys with filtering and pagination
func (h *CSATHandler) ListSurveys(c *gin.Context) {
	accountID := h.getAccountID(c)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", c.DefaultQuery("limit", "25")))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}

	filter := repository.CSATFilter{
		ReviewStatus: strings.TrimSpace(c.Query("review_status")),
		Search:       strings.TrimSpace(c.DefaultQuery("q", c.Query("search"))),
		Page:         page,
		PageSize:     pageSize,
	}

	if rawRating := c.Query("rating"); rawRating != "" {
		if r, err := strconv.Atoi(rawRating); err == nil && r > 0 {
			filter.Rating = &r
		}
	}

	if rawAgentID := c.DefaultQuery("assigned_agent_id", c.Query("user_id")); rawAgentID != "" {
		if id, err := strconv.ParseUint(rawAgentID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.AssignedAgentID = &u
		}
	}

	if rawInboxID := c.Query("inbox_id"); rawInboxID != "" {
		if id, err := strconv.ParseUint(rawInboxID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.InboxID = &u
		}
	}

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

	surveys, total, err := h.csatRepo.ListSurveys(accountID, filter)
	if err != nil {
		logger.WithComponent("csat").Error("failed to list csat surveys",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list csat surveys")
		return
	}

	logger.WithComponent("csat").Info("csat surveys listed successfully",
		"account_id", accountID,
		"review_status", filter.ReviewStatus,
		"returned_count", len(surveys),
		"total_count", total,
		"page", page,
		"page_size", pageSize,
	)

	response.Paginated(c, surveys, total, page, pageSize)
}

// GetSurvey retrieves a single survey response with full relationship details
func (h *CSATHandler) GetSurvey(c *gin.Context) {
	accountID := h.getAccountID(c)
	surveyID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || surveyID == 0 {
		response.BadRequest(c, "Invalid survey ID")
		return
	}

	survey, err := h.csatRepo.GetSurvey(accountID, uint(surveyID))
	if err != nil || survey == nil {
		logger.WithComponent("csat").Warn("csat survey not found or access denied",
			"account_id", accountID,
			"survey_id", surveyID,
		)
		response.NotFound(c, "CSAT survey not found")
		return
	}

	logger.WithComponent("csat").Info("csat survey retrieved successfully",
		"account_id", accountID,
		"survey_id", surveyID,
		"rating", survey.Rating,
		"review_status", survey.ReviewStatus,
	)

	response.Success(c, survey)
}

// ReviewSurveyRequest defines review input
type ReviewSurveyRequest struct {
	ReviewStatus string `json:"review_status" binding:"required"` // approved, rejected, flagged, pending
	ReviewNotes  string `json:"review_notes"`
}

// ReviewSurvey sets moderation status on a CSAT response
func (h *CSATHandler) ReviewSurvey(c *gin.Context) {
	accountID := h.getAccountID(c)
	surveyID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || surveyID == 0 {
		response.BadRequest(c, "Invalid survey ID")
		return
	}

	var req ReviewSurveyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid review payload: review_status is required")
		return
	}

	reviewerID := h.getUserID(c)
	updated, err := h.csatRepo.ReviewSurvey(accountID, uint(surveyID), reviewerID, req.ReviewStatus, strings.TrimSpace(req.ReviewNotes))
	if err != nil || updated == nil {
		logger.WithComponent("csat").Warn("review csat survey rejected: survey not found or access denied",
			"account_id", accountID,
			"survey_id", surveyID,
		)
		response.NotFound(c, "CSAT survey not found")
		return
	}

	logger.WithComponent("csat").Info("csat survey reviewed successfully",
		"account_id", accountID,
		"survey_id", surveyID,
		"reviewer_id", reviewerID,
		"review_status", updated.ReviewStatus,
	)

	response.Success(c, updated)
}

// BulkReviewRequest defines bulk moderation input
type BulkReviewRequest struct {
	IDs          []uint `json:"ids" binding:"required,min=1"`
	ReviewStatus string `json:"review_status" binding:"required"`
	ReviewNotes  string `json:"review_notes"`
}

// BulkReviewSurveys reviews multiple CSAT surveys at once
func (h *CSATHandler) BulkReviewSurveys(c *gin.Context) {
	accountID := h.getAccountID(c)

	var req BulkReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid bulk review payload")
		return
	}

	reviewerID := h.getUserID(c)
	affected, err := h.csatRepo.BulkReviewSurveys(accountID, reviewerID, req.IDs, req.ReviewStatus, strings.TrimSpace(req.ReviewNotes))
	if err != nil {
		logger.WithComponent("csat").Error("failed to bulk review csat surveys",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to bulk review csat surveys")
		return
	}

	logger.WithComponent("csat").Info("csat surveys bulk reviewed successfully",
		"account_id", accountID,
		"reviewer_id", reviewerID,
		"review_status", req.ReviewStatus,
		"affected_count", affected,
	)

	response.Success(c, gin.H{
		"updated_count": affected,
		"review_status": req.ReviewStatus,
	})
}

// TriggerSurveyRequest defines manual or automated survey trigger
type TriggerSurveyRequest struct {
	ConversationID uint `json:"conversation_id"`
}

// TriggerSurvey initiates a CSAT survey for a conversation
func (h *CSATHandler) TriggerSurvey(c *gin.Context) {
	accountID := h.getAccountID(c)

	var conversationID uint
	if rawConvID := c.Param("id"); rawConvID != "" {
		if id, err := strconv.ParseUint(rawConvID, 10, 32); err == nil && id > 0 {
			conversationID = uint(id)
		}
	}
	if conversationID == 0 {
		var req TriggerSurveyRequest
		if err := c.ShouldBindJSON(&req); err == nil && req.ConversationID > 0 {
			conversationID = req.ConversationID
		}
	}

	if conversationID == 0 {
		response.BadRequest(c, "Missing or invalid conversation ID")
		return
	}

	survey, err := h.csatRepo.TriggerSurveyForConversation(accountID, conversationID)
	if err != nil {
		logger.WithComponent("csat").Warn("failed to trigger csat survey: conversation not found",
			"account_id", accountID,
			"conversation_id", conversationID,
			"error", err.Error(),
		)
		response.NotFound(c, "Conversation not found or access denied")
		return
	}

	logger.WithComponent("csat").Info("csat survey triggered successfully",
		"account_id", accountID,
		"conversation_id", conversationID,
		"survey_id", survey.ID,
	)

	response.Created(c, survey)
}

// SubmitSurveyRequest represents customer or agent survey response
type SubmitSurveyRequest struct {
	ConversationID uint   `json:"conversation_id"`
	Rating         int    `json:"rating" binding:"required,min=1,max=5"`
	FeedbackText   string `json:"feedback_text"`
}

// SubmitSurvey saves a customer satisfaction rating
func (h *CSATHandler) SubmitSurvey(c *gin.Context) {
	accountID := h.getAccountID(c)

	var conversationID uint
	if rawConvID := c.Param("id"); rawConvID != "" {
		if id, err := strconv.ParseUint(rawConvID, 10, 32); err == nil && id > 0 {
			conversationID = uint(id)
		}
	}

	var req SubmitSurveyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Rating must be between 1 and 5")
		return
	}

	if conversationID == 0 {
		conversationID = req.ConversationID
	}
	if conversationID == 0 {
		response.BadRequest(c, "Missing conversation ID")
		return
	}

	survey, err := h.csatRepo.SubmitOrUpdateSurvey(accountID, conversationID, req.Rating, strings.TrimSpace(req.FeedbackText))
	if err != nil {
		logger.WithComponent("csat").Warn("failed to submit csat survey: conversation not found or access denied",
			"account_id", accountID,
			"conversation_id", conversationID,
			"error", err.Error(),
		)
		response.NotFound(c, "Conversation not found or access denied")
		return
	}

	logger.WithComponent("csat").Info("csat survey submitted successfully",
		"account_id", accountID,
		"conversation_id", conversationID,
		"survey_id", survey.ID,
		"rating", req.Rating,
	)

	response.Created(c, survey)
}

// DownloadSurveys streams CSV report with UTF-8 BOM encoding
func (h *CSATHandler) DownloadSurveys(c *gin.Context) {
	accountID := h.getAccountID(c)

	filter := repository.CSATFilter{
		ReviewStatus: strings.TrimSpace(c.Query("review_status")),
		Search:       strings.TrimSpace(c.DefaultQuery("q", c.Query("search"))),
		Page:         1,
		PageSize:     10000,
	}

	if rawRating := c.Query("rating"); rawRating != "" {
		if r, err := strconv.Atoi(rawRating); err == nil && r > 0 {
			filter.Rating = &r
		}
	}
	if rawAgentID := c.DefaultQuery("assigned_agent_id", c.Query("user_id")); rawAgentID != "" {
		if id, err := strconv.ParseUint(rawAgentID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.AssignedAgentID = &u
		}
	}
	if rawInboxID := c.Query("inbox_id"); rawInboxID != "" {
		if id, err := strconv.ParseUint(rawInboxID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			filter.InboxID = &u
		}
	}
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

	surveys, _, err := h.csatRepo.ListSurveys(accountID, filter)
	if err != nil {
		logger.WithComponent("csat").Error("failed to generate csat csv report",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to generate CSV export")
		return
	}

	filename := fmt.Sprintf("csat_responses_%s.csv", time.Now().Format("20060102_150405"))
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.Header("Content-Type", "text/csv; charset=utf-8")

	// UTF-8 BOM bytes to ensure Windows Excel correctly renders Chinese/UTF-8 text
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(c.Writer)
	headers := []string{
		"ID",
		"Conversation ID",
		"Contact Name",
		"Contact Email",
		"Assigned Agent",
		"Rating",
		"Feedback Text",
		"Review Status",
		"Reviewer",
		"Review Notes",
		"Reviewed At",
		"Created At",
	}
	_ = writer.Write(headers)

	for _, s := range surveys {
		contactName := ""
		contactEmail := ""
		if s.Conversation != nil && s.Conversation.Contact != nil {
			contactName = s.Conversation.Contact.Name
			contactEmail = s.Conversation.Contact.Email
		}
		agentName := ""
		if s.AssignedAgent != nil {
			agentName = s.AssignedAgent.Name
		}
		reviewerName := ""
		if s.Reviewer != nil {
			reviewerName = s.Reviewer.Name
		}
		reviewedAtStr := ""
		if s.ReviewedAt != nil {
			reviewedAtStr = s.ReviewedAt.Format(time.RFC3339)
		}

		row := []string{
			strconv.Itoa(int(s.ID)),
			strconv.Itoa(int(s.ConversationID)),
			contactName,
			contactEmail,
			agentName,
			strconv.Itoa(s.Rating),
			s.FeedbackText,
			s.ReviewStatus,
			reviewerName,
			s.ReviewNotes,
			reviewedAtStr,
			s.CreatedAt.Format(time.RFC3339),
		}
		_ = writer.Write(row)
	}

	writer.Flush()

	logger.WithComponent("csat").Info("csat report downloaded successfully",
		"account_id", accountID,
		"records_count", len(surveys),
		"filename", filename,
	)
}

// GetMetrics returns comprehensive CSAT analytics and agent rankings
func (h *CSATHandler) GetMetrics(c *gin.Context) {
	accountID := h.getAccountID(c)

	var inboxID *uint
	if rawInboxID := c.Query("inbox_id"); rawInboxID != "" {
		if id, err := strconv.ParseUint(rawInboxID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			inboxID = &u
		}
	}

	var agentID *uint
	if rawAgentID := c.DefaultQuery("assigned_agent_id", c.Query("user_id")); rawAgentID != "" {
		if id, err := strconv.ParseUint(rawAgentID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			agentID = &u
		}
	}

	var since *time.Time
	if rawSince := c.DefaultQuery("since", c.Query("from")); rawSince != "" {
		if t, err := time.Parse(time.RFC3339, rawSince); err == nil {
			since = &t
		}
	}

	var until *time.Time
	if rawUntil := c.DefaultQuery("until", c.Query("to")); rawUntil != "" {
		if t, err := time.Parse(time.RFC3339, rawUntil); err == nil {
			until = &t
		}
	}

	metrics, err := h.csatRepo.GetAdvancedMetrics(accountID, inboxID, agentID, since, until)
	if err != nil {
		logger.WithComponent("csat").Error("failed to get csat metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get csat metrics")
		return
	}

	logger.WithComponent("csat").Info("csat metrics retrieved successfully",
		"account_id", accountID,
		"total_responses", metrics.TotalResponses,
		"average_rating", metrics.AverageRating,
		"satisfaction_rate", metrics.SatisfactionRate,
	)

	response.Success(c, metrics)
}

// DeleteSurvey removes a survey record
func (h *CSATHandler) DeleteSurvey(c *gin.Context) {
	accountID := h.getAccountID(c)
	surveyID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || surveyID == 0 {
		response.BadRequest(c, "Invalid survey ID")
		return
	}

	if err := h.csatRepo.DeleteSurvey(accountID, uint(surveyID)); err != nil {
		logger.WithComponent("csat").Warn("delete csat survey rejected: survey not found or access denied",
			"account_id", accountID,
			"survey_id", surveyID,
		)
		response.NotFound(c, "CSAT survey not found")
		return
	}

	logger.WithComponent("csat").Info("csat survey deleted successfully",
		"account_id", accountID,
		"survey_id", surveyID,
	)

	response.Success(c, gin.H{"deleted": true, "survey_id": surveyID})
}
