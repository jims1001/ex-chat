package handler

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// CSATHandler manages CSAT survey details, moderation, report generation and lifecycle
type CSATHandler struct {
	csatRepo    *repository.CSATExtensionRepository
	msgRepo     *repository.MessageRepository
	convRepo    *repository.ConversationRepository
	inboxRepo   *repository.InboxRepository
	contactRepo *repository.ContactRepository
	accountRepo *repository.AccountRepository
	jwtSecret   string
}

// NewCSATHandler creates a new handler instance
func NewCSATHandler(csatRepo *repository.CSATExtensionRepository, msgRepo *repository.MessageRepository, convRepo *repository.ConversationRepository, inboxRepo *repository.InboxRepository, contactRepo *repository.ContactRepository, accountRepo *repository.AccountRepository) *CSATHandler {
	return &CSATHandler{csatRepo: csatRepo, msgRepo: msgRepo, convRepo: convRepo, inboxRepo: inboxRepo, contactRepo: contactRepo, accountRepo: accountRepo}
}

type publicCSATContext struct {
	Conversation domain.Conversation
	Survey       domain.CSATSurvey
	Message      domain.Message
	Inbox        domain.Inbox
	HasSurvey    bool
	HasMessage   bool
}

func (h *CSATHandler) resolvePublicContext(identifier string, accountID uint) (*publicCSATContext, error) {
	ctx := &publicCSATContext{}
	isUUID := len(identifier) >= 32 && (strings.Contains(identifier, "-") || len(identifier) == 32)
	var conv *domain.Conversation
	var err error
	if isUUID {
		conv, err = h.convRepo.FindByUUID(identifier)
	} else {
		id, parseErr := strconv.ParseUint(identifier, 10, 32)
		if parseErr != nil || id == 0 {
			return nil, fmt.Errorf("invalid identifier")
		}
		if accountID > 0 {
			conv, err = h.convRepo.FindByID(accountID, uint(id))
		} else {
			conv, err = h.convRepo.FindGlobalByID(uint(id))
		}
		if err != nil || conv == nil {
			survey, surveyErr := h.csatRepo.FindGlobalSurvey(uint(id))
			if surveyErr != nil {
				return nil, surveyErr
			}
			ctx.Survey, ctx.HasSurvey = *survey, true
			conv, err = h.convRepo.FindGlobalByID(survey.ConversationID)
		}
	}
	if err != nil || conv == nil {
		return nil, err
	}
	ctx.Conversation = *conv
	if !ctx.HasSurvey {
		if survey, findErr := h.csatRepo.FindSurveyByConversation(conv.AccountID, conv.ID); findErr == nil && survey != nil {
			ctx.Survey, ctx.HasSurvey = *survey, true
		}
	}
	if message, findErr := h.msgRepo.FindCSATMessage(conv.AccountID, conv.ID); findErr == nil && message != nil {
		ctx.Message, ctx.HasMessage = *message, true
	}
	if inbox, findErr := h.inboxRepo.FindByID(conv.AccountID, conv.InboxID); findErr == nil && inbox != nil {
		ctx.Inbox = *inbox
	}
	return ctx, nil
}

// SetJWTSecret sets the secret key used for signing visitor tokens
func (h *CSATHandler) SetJWTSecret(secret string) {
	h.jwtSecret = secret
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
	return 0
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
	if reviewerID == 0 {
		reviewerID = 1
	}
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
	if reviewerID == 0 {
		reviewerID = 1
	}
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

	conv, err := h.convRepo.FindByID(accountID, conversationID)
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}
	survey, err := h.csatRepo.TriggerSurveyForConversation(accountID, conversationID, conv.AssigneeID)
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

	conv, err := h.convRepo.FindByID(accountID, conversationID)
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}
	survey, err := h.csatRepo.SubmitOrUpdateSurvey(accountID, conversationID, conv.AssigneeID, req.Rating, strings.TrimSpace(req.FeedbackText))
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

// verifyPublicCSATAccess verifies whether the requester has legitimate access to the conversation's CSAT survey.
// When accessed via numeric ID, the caller MUST present valid proof of ownership:
// 1. Logged-in user in context or Authorization Bearer JWT belonging to the conversation's account.
// 2. Visitor credential matching the conversation:
//   - query param: token, visitor_token, contact_token, uuid
//   - header: X-Auth-Token, X-Contact-Token, X-Visitor-Token
//     Matching:
//   - conversation.UUID
//   - inbox.WebsiteToken
//   - contact.PubsubToken
//   - contact_inbox.SourceID
func (h *CSATHandler) verifyPublicCSATAccess(c *gin.Context, conv *domain.Conversation) bool {
	if conv == nil || conv.ID == 0 {
		return false
	}

	// 1. Check logged-in user in context
	if uid := h.getUserID(c); uid > 0 {
		if membership, _ := h.accountRepo.GetMembership(conv.AccountID, uid); membership != nil {
			return true
		}
	}

	// 2. Check Authorization header
	authHeader := c.GetHeader("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if tokenStr != "" {
			if claims, err := auth.ValidateToken(tokenStr, h.jwtSecret); err == nil && claims != nil {
				if claims.UserID > 0 {
					if membership, _ := h.accountRepo.GetMembership(conv.AccountID, claims.UserID); membership != nil {
						return true
					}
				}
			}
		}
	}

	// 3. Check visitor credentials from query or headers
	visitorToken := strings.TrimSpace(c.Query("token"))
	if visitorToken == "" {
		visitorToken = strings.TrimSpace(c.Query("visitor_token"))
	}
	if visitorToken == "" {
		visitorToken = strings.TrimSpace(c.Query("contact_token"))
	}
	if visitorToken == "" {
		visitorToken = strings.TrimSpace(c.Query("uuid"))
	}
	if visitorToken == "" {
		visitorToken = strings.TrimSpace(c.GetHeader("X-Auth-Token"))
	}
	if visitorToken == "" {
		visitorToken = strings.TrimSpace(c.GetHeader("X-Contact-Token"))
	}
	if visitorToken == "" {
		visitorToken = strings.TrimSpace(c.GetHeader("X-Visitor-Token"))
	}
	if visitorToken == "" {
		visitorToken = strings.TrimSpace(c.GetHeader("X-Visitor-Session"))
	}

	if visitorToken != "" {
		// A. Matches conversation UUID directly
		if conv.UUID != "" && strings.EqualFold(visitorToken, conv.UUID) {
			return true
		}

		// B. Validates signed visitor token
		if h.jwtSecret != "" {
			if claims, err := auth.ParseVisitorToken(visitorToken, h.jwtSecret); err == nil && claims != nil {
				if claims.InboxID == conv.InboxID && (claims.ContactID == 0 || claims.ContactID == conv.ContactID) {
					return true
				}
			}
		}

		// C. Matches contact pubsub_token or contact_inbox source_id belonging to this specific conversation
		if conv.ContactID > 0 && h.contactRepo.VisitorTokenMatches(conv.AccountID, conv.ContactID, conv.InboxID, visitorToken) {
			return true
		}
	}

	return false
}

// GetPublicCSATSurvey handles GET /public/api/v1/csat_survey/:id
func (h *CSATHandler) GetPublicCSATSurvey(c *gin.Context) {
	paramID := strings.TrimSpace(c.Param("id"))
	if paramID == "" {
		response.NotFound(c, "CSAT survey not found")
		return
	}

	// 1. Resolve conversation and its public CSAT projection through the owning repository.
	isUUID := len(paramID) >= 32 && (strings.Contains(paramID, "-") || len(paramID) == 32)
	publicCtx, err := h.resolvePublicContext(paramID, h.getAccountID(c))
	if err != nil {
		response.NotFound(c, "CSAT survey not found")
		return
	}
	conv, survey, csatMsg := publicCtx.Conversation, publicCtx.Survey, publicCtx.Message

	// Enforce visitor credential verification when accessing via numeric ID
	if !isUUID && !h.verifyPublicCSATAccess(c, &conv) {
		response.Forbidden(c, "Access denied: valid visitor token or account authentication required for numeric conversation ID")
		return
	}

	// 2. Check for CSAT survey and input_csat message
	hasSurvey, hasMsg := publicCtx.HasSurvey, publicCtx.HasMessage

	// If neither exists, matching Chatwoot spec: return not found for open conversation without CSAT
	if !hasSurvey && !hasMsg {
		response.NotFound(c, "CSAT survey not found for this conversation")
		return
	}

	// 3. Resolve inbox for display attributes
	inbox := publicCtx.Inbox

	var csatSurveyResp any
	if hasSurvey && survey.Rating > 0 {
		csatSurveyResp = gin.H{
			"id":               survey.ID,
			"conversation_id":  conv.ID,
			"rating":           survey.Rating,
			"feedback_message": survey.FeedbackText,
		}
	}

	msgID := conv.ID
	if hasMsg {
		msgID = csatMsg.ID
	} else if hasSurvey {
		msgID = survey.ID
	}

	resData := gin.H{
		"id":                   msgID,
		"conversation_id":      conv.ID,
		"csat_survey_response": csatSurveyResp,
		"display_type":         "emoji",
		"content":              "How satisfied were you with our support?",
		"inbox_avatar_url":     "",
		"inbox_name":           inbox.Name,
		"locale":               "zh_CN",
		"created_at":           conv.CreatedAt,
	}

	c.JSON(http.StatusOK, gin.H{
		"success":              true,
		"id":                   resData["id"],
		"conversation_id":      resData["conversation_id"],
		"csat_survey_response": resData["csat_survey_response"],
		"display_type":         resData["display_type"],
		"content":              resData["content"],
		"inbox_avatar_url":     resData["inbox_avatar_url"],
		"inbox_name":           resData["inbox_name"],
		"locale":               resData["locale"],
		"created_at":           resData["created_at"],
		"data":                 resData,
	})
}

// UpdatePublicCSATSurvey handles PATCH / PUT / POST /public/api/v1/csat_survey/:id
func (h *CSATHandler) UpdatePublicCSATSurvey(c *gin.Context) {
	paramID := strings.TrimSpace(c.Param("id"))
	if paramID == "" {
		response.NotFound(c, "CSAT survey not found")
		return
	}

	var rawBody map[string]any
	if err := c.ShouldBindJSON(&rawBody); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	rating := 0
	feedback := ""
	var accID uint

	if rawAcc, ok := rawBody["account_id"].(float64); ok && rawAcc > 0 {
		accID = uint(rawAcc)
	}

	// Try extracting from message.submitted_values.csat_survey_response
	if msgMap, ok := rawBody["message"].(map[string]any); ok {
		if subMap, ok := msgMap["submitted_values"].(map[string]any); ok {
			if respMap, ok := subMap["csat_survey_response"].(map[string]any); ok {
				if r, ok := respMap["rating"].(float64); ok {
					rating = int(r)
				}
				if fb, ok := respMap["feedback_message"].(string); ok {
					feedback = fb
				}
			}
		}
	}

	// Try extracting from submitted_values.csat_survey_response
	if rating == 0 {
		if subMap, ok := rawBody["submitted_values"].(map[string]any); ok {
			if respMap, ok := subMap["csat_survey_response"].(map[string]any); ok {
				if r, ok := respMap["rating"].(float64); ok {
					rating = int(r)
				}
				if fb, ok := respMap["feedback_message"].(string); ok {
					feedback = fb
				}
			}
		}
	}

	// Try extracting from csat_survey_response
	if rating == 0 {
		if respMap, ok := rawBody["csat_survey_response"].(map[string]any); ok {
			if r, ok := respMap["rating"].(float64); ok {
				rating = int(r)
			}
			if fb, ok := respMap["feedback_message"].(string); ok {
				feedback = fb
			}
		}
	}

	// Try extracting directly from root
	if rating == 0 {
		if r, ok := rawBody["rating"].(float64); ok {
			rating = int(r)
		}
		if fb, ok := rawBody["feedback_message"].(string); ok && fb != "" {
			feedback = fb
		} else if fbText, ok := rawBody["feedback_text"].(string); ok && fbText != "" {
			feedback = fbText
		}
	}

	if rating < 1 || rating > 5 {
		response.BadRequest(c, "Rating must be between 1 and 5")
		return
	}

	// 1. Resolve conversation and its public CSAT projection through the owning repository.
	isUUID := len(paramID) >= 32 && (strings.Contains(paramID, "-") || len(paramID) == 32)
	publicCtx, err := h.resolvePublicContext(paramID, accID)
	if err != nil {
		response.NotFound(c, "Conversation or CSAT survey not found")
		return
	}
	conv := publicCtx.Conversation

	if accID > 0 && conv.AccountID != accID {
		response.Forbidden(c, "Account ID mismatch for this conversation")
		return
	}

	// Enforce visitor credential verification when accessing via numeric ID
	if !isUUID && !h.verifyPublicCSATAccess(c, &conv) {
		response.Forbidden(c, "Access denied: valid visitor token or account authentication required for numeric conversation ID")
		return
	}

	// 2. Check CSAT lock: cannot update after 14 days
	csatMsg, survey := publicCtx.Message, publicCtx.Survey
	hasMsg, hasSurvey := publicCtx.HasMessage, publicCtx.HasSurvey

	var refTime time.Time
	if hasMsg && !csatMsg.CreatedAt.IsZero() {
		refTime = csatMsg.CreatedAt
	} else if hasSurvey && !survey.CreatedAt.IsZero() {
		refTime = survey.CreatedAt
	}

	if !refTime.IsZero() && time.Since(refTime) > 14*24*time.Hour {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"error":   "You cannot update the CSAT survey after 14 days",
		})
		return
	}

	// 2.2 Check one-time submission lock:
	// If survey has already been submitted (Rating > 0) and the submission was more than 10 minutes ago,
	// or survey has been reviewed/locked, reject modification with 422
	if hasSurvey && survey.Rating > 0 {
		if survey.ReviewStatus == "approved" || survey.ReviewStatus == "rejected" || survey.ReviewStatus == "locked" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"success": false,
				"error":   "CSAT survey response has been reviewed and locked, cannot be modified",
			})
			return
		}
		if !survey.UpdatedAt.IsZero() && time.Since(survey.UpdatedAt) > 10*time.Minute {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"success": false,
				"error":   "CSAT survey response has been locked and cannot be modified",
			})
			return
		}
	}

	// 3. Save / Update survey
	if hasSurvey {
		survey.Rating = rating
		survey.FeedbackText = feedback
		survey.UpdatedAt = time.Now().UTC()
		_ = h.csatRepo.SavePublicSurvey(&survey)
	} else {
		survey = domain.CSATSurvey{
			AccountID:       conv.AccountID,
			ConversationID:  conv.ID,
			Rating:          rating,
			FeedbackText:    feedback,
			AssignedAgentID: conv.AssigneeID,
			ReviewStatus:    "pending",
		}
		_ = h.csatRepo.SavePublicSurvey(&survey)
	}

	// 4. If input_csat message exists, update its content_attributes with submitted values
	if hasMsg {
		subVals := map[string]any{
			"submitted_values": map[string]any{
				"csat_survey_response": map[string]any{
					"rating":           rating,
					"feedback_message": feedback,
				},
			},
		}
		subBytes, _ := json.Marshal(subVals)
		csatMsg.ContentAttributes = string(subBytes)
		_ = h.msgRepo.UpdateContentAttributes(conv.AccountID, conv.ID, csatMsg.ID, csatMsg.ContentAttributes)
	}

	// 5. Construct return response
	inbox := publicCtx.Inbox

	msgID := conv.ID
	if hasMsg {
		msgID = csatMsg.ID
	} else {
		msgID = survey.ID
	}

	csatSurveyResp := gin.H{
		"id":               survey.ID,
		"conversation_id":  conv.ID,
		"rating":           survey.Rating,
		"feedback_message": survey.FeedbackText,
	}

	resData := gin.H{
		"id":                   msgID,
		"conversation_id":      conv.ID,
		"csat_survey_response": csatSurveyResp,
		"display_type":         "emoji",
		"content":              "How satisfied were you with our support?",
		"inbox_avatar_url":     "",
		"inbox_name":           inbox.Name,
		"locale":               "zh_CN",
		"created_at":           survey.CreatedAt,
	}

	statusCode := http.StatusOK
	if c.Request.Method == http.MethodPost {
		statusCode = http.StatusCreated
	}

	c.JSON(statusCode, gin.H{
		"success":              true,
		"id":                   resData["id"],
		"conversation_id":      resData["conversation_id"],
		"csat_survey_response": resData["csat_survey_response"],
		"display_type":         resData["display_type"],
		"content":              resData["content"],
		"inbox_avatar_url":     resData["inbox_avatar_url"],
		"inbox_name":           resData["inbox_name"],
		"locale":               resData["locale"],
		"created_at":           resData["created_at"],
		"data":                 resData,
	})
}
