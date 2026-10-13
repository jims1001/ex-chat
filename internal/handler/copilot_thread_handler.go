package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CopilotThreadHandler handles HTTP requests for Copilot multi-turn threads and messages
type CopilotThreadHandler struct {
	db          *gorm.DB
	threadRepo  *repository.CopilotThreadRepository
	convRepo    *repository.ConversationRepository
	msgRepo     *repository.MessageRepository
	captainRepo *repository.CaptainRepository
}

// NewCopilotThreadHandler creates a new instance of CopilotThreadHandler
func NewCopilotThreadHandler(
	db *gorm.DB,
	threadRepo *repository.CopilotThreadRepository,
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	captainRepo *repository.CaptainRepository,
) *CopilotThreadHandler {
	return &CopilotThreadHandler{
		db:          db,
		threadRepo:  threadRepo,
		convRepo:    convRepo,
		msgRepo:     msgRepo,
		captainRepo: captainRepo,
	}
}

func (h *CopilotThreadHandler) getAccountID(c *gin.Context) uint {
	if accVal, exists := c.Get("account_id"); exists {
		if id, ok := accVal.(uint); ok {
			return id
		}
	}
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	return uint(accID)
}

func (h *CopilotThreadHandler) getUserID(c *gin.Context) uint {
	if userVal, exists := c.Get("user_id"); exists {
		if id, ok := userVal.(uint); ok {
			return id
		}
	}
	return 1
}

// ----------------- Thread Endpoints -----------------

// ListThreads handles GET /copilot/threads
func (h *CopilotThreadHandler) ListThreads(c *gin.Context) {
	accountID := h.getAccountID(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	status := c.Query("status")
	query := c.Query("q")

	var convIDPtr *uint
	if convStr := c.Query("conversation_id"); convStr != "" {
		if id, err := strconv.ParseUint(convStr, 10, 32); err == nil {
			u := uint(id)
			convIDPtr = &u
		}
	}

	var assistantIDPtr *uint
	if asstStr := c.Query("assistant_id"); asstStr != "" {
		if id, err := strconv.ParseUint(asstStr, 10, 32); err == nil {
			u := uint(id)
			assistantIDPtr = &u
		}
	}

	var userIDPtr *uint
	if uStr := c.Query("user_id"); uStr != "" {
		if id, err := strconv.ParseUint(uStr, 10, 32); err == nil {
			u := uint(id)
			userIDPtr = &u
		}
	}

	filter := repository.CopilotThreadFilter{
		ConversationID: convIDPtr,
		AssistantID:    assistantIDPtr,
		UserID:         userIDPtr,
		Status:         status,
		Query:          query,
		Page:           page,
		PageSize:       pageSize,
	}

	threads, total, err := h.threadRepo.ListThreads(accountID, filter)
	if err != nil {
		logger.WithComponent("copilot").Error("failed to list copilot threads",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list Copilot threads")
		return
	}

	logger.WithComponent("copilot").Info("listed copilot threads",
		"account_id", accountID,
		"count", len(threads),
		"total", total,
	)

	response.Success(c, gin.H{
		"threads": threads,
		"meta": gin.H{
			"total":    total,
			"page":     page,
			"per_page": pageSize,
		},
	})
}

// CreateThreadReq defines payload for creating a Copilot thread
type CreateThreadReq struct {
	ConversationID *uint  `json:"conversation_id"`
	AssistantID    *uint  `json:"assistant_id"`
	Title          string `json:"title"`
	Context        string `json:"context"`
	Metadata       string `json:"metadata"`
}

// CreateThread handles POST /copilot/threads
func (h *CopilotThreadHandler) CreateThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	userID := h.getUserID(c)

	var req CreateThreadReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, err.Error())
		return
	}

	if !h.validateCopilotReferences(c, accountID, req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		if req.ConversationID != nil {
			title = fmt.Sprintf("会话 #%d 辅助分析", *req.ConversationID)
		} else {
			title = fmt.Sprintf("Copilot 智能问答 %s", time.Now().Format("01-02 15:04"))
		}
	}

	thread := domain.CopilotThread{
		AccountID:      accountID,
		UserID:         userID,
		ConversationID: req.ConversationID,
		AssistantID:    req.AssistantID,
		Title:          title,
		Status:         domain.CopilotThreadStatusActive,
		Context:        req.Context,
		Metadata:       req.Metadata,
	}

	if err := h.threadRepo.CreateThread(&thread); err != nil {
		logger.WithComponent("copilot").Error("failed to create copilot thread",
			"account_id", accountID,
			"user_id", userID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create Copilot thread")
		return
	}

	logger.WithComponent("copilot").Info("created copilot thread",
		"account_id", accountID,
		"user_id", userID,
		"thread_id", thread.ID,
		"title", thread.Title,
	)

	response.Success(c, thread)
}

// GetThread handles GET /copilot/threads/:id
func (h *CopilotThreadHandler) GetThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	response.Success(c, thread)
}

// UpdateThreadReq defines fields modifiable on a Copilot thread
type UpdateThreadReq struct {
	Title    *string `json:"title"`
	Status   *string `json:"status"`
	Context  *string `json:"context"`
	Metadata *string `json:"metadata"`
}

// UpdateThread handles PUT/PATCH /copilot/threads/:id
func (h *CopilotThreadHandler) UpdateThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	var req UpdateThreadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Title != nil {
		thread.Title = *req.Title
	}
	if req.Status != nil {
		thread.Status = *req.Status
	}
	if req.Context != nil {
		thread.Context = *req.Context
	}
	if req.Metadata != nil {
		thread.Metadata = *req.Metadata
	}

	if err := h.threadRepo.UpdateThread(thread); err != nil {
		logger.WithComponent("copilot").Error("failed to update copilot thread",
			"account_id", accountID,
			"thread_id", threadID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update Copilot thread")
		return
	}

	logger.WithComponent("copilot").Info("updated copilot thread",
		"account_id", accountID,
		"thread_id", threadID,
		"status", thread.Status,
	)

	response.Success(c, thread)
}

// DeleteThread handles DELETE /copilot/threads/:id
func (h *CopilotThreadHandler) DeleteThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	if err := h.threadRepo.DeleteThread(accountID, uint(threadID)); err != nil {
		if err == gorm.ErrRecordNotFound {
			response.NotFound(c, "Copilot thread not found")
			return
		}
		logger.WithComponent("copilot").Error("failed to delete copilot thread",
			"account_id", accountID,
			"thread_id", threadID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete Copilot thread")
		return
	}

	logger.WithComponent("copilot").Info("deleted copilot thread",
		"account_id", accountID,
		"thread_id", threadID,
	)

	response.Success(c, gin.H{"deleted": true, "thread_id": threadID})
}

// GetMetrics handles GET /copilot/threads/metrics
func (h *CopilotThreadHandler) GetMetrics(c *gin.Context) {
	accountID := h.getAccountID(c)

	metrics, err := h.threadRepo.GetThreadMetrics(accountID)
	if err != nil {
		logger.WithComponent("copilot").Error("failed to get copilot metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get Copilot metrics")
		return
	}

	response.Success(c, metrics)
}

// ListConversationThreads handles GET /conversations/:id/copilot/threads
func (h *CopilotThreadHandler) ListConversationThreads(c *gin.Context) {
	accountID := h.getAccountID(c)
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	uConv := uint(convID)
	filter := repository.CopilotThreadFilter{
		ConversationID: &uConv,
		Page:           1,
		PageSize:       50,
	}

	threads, total, err := h.threadRepo.ListThreads(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to list conversation threads")
		return
	}

	response.Success(c, gin.H{"threads": threads, "total": total})
}

// CreateConversationThread handles POST /conversations/:id/copilot/threads
func (h *CopilotThreadHandler) CreateConversationThread(c *gin.Context) {
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req CreateThreadReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid thread payload")
		return
	}
	uConv := uint(convID)
	req.ConversationID = &uConv

	// Reuse CreateThread with modified context
	accountID := h.getAccountID(c)
	userID := h.getUserID(c)

	if !h.validateCopilotReferences(c, accountID, req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("会话 #%d 专属辅助", convID)
	}

	thread := domain.CopilotThread{
		AccountID:      accountID,
		UserID:         userID,
		ConversationID: &uConv,
		AssistantID:    req.AssistantID,
		Title:          title,
		Status:         domain.CopilotThreadStatusActive,
		Context:        req.Context,
		Metadata:       req.Metadata,
	}

	if err := h.threadRepo.CreateThread(&thread); err != nil {
		response.InternalError(c, "Failed to create conversation Copilot thread")
		return
	}

	response.Success(c, thread)
}

// ----------------- Message & Multi-turn Chat Endpoints -----------------

// ListMessages handles GET /copilot/threads/:id/messages
func (h *CopilotThreadHandler) ListMessages(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	// Verify thread exists
	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("per_page", "50"))
	order := c.DefaultQuery("order", "asc")
	orderAsc := order == "asc"

	messages, total, err := h.threadRepo.ListMessages(accountID, uint(threadID), page, pageSize, orderAsc)
	if err != nil {
		response.InternalError(c, "Failed to list thread messages")
		return
	}

	response.Success(c, gin.H{
		"messages": messages,
		"meta": gin.H{
			"total":     total,
			"page":      page,
			"per_page":  pageSize,
			"thread_id": threadID,
		},
	})
}

// SendMessageReq defines the incoming user prompt in a thread
type SendMessageReq struct {
	Content          string `json:"content" binding:"required"`
	Role             string `json:"role"`
	Citations        string `json:"citations"`
	SuggestedActions string `json:"suggested_actions"`
	NoGenerateReply  bool   `json:"no_generate_reply"`
}

// SendMessage handles POST /copilot/threads/:id/messages & POST /copilot/threads/:id/chat
// GetMessage handles GET /copilot/threads/:id/messages/:msg_id
func (h *CopilotThreadHandler) GetMessage(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	msgID, err2 := strconv.ParseUint(c.Param("msg_id"), 10, 32)
	if err != nil || err2 != nil {
		response.BadRequest(c, "Invalid thread ID or message ID")
		return
	}

	msg, err := h.threadRepo.GetMessage(accountID, uint(threadID), uint(msgID))
	if err != nil || msg == nil {
		response.NotFound(c, "Message not found")
		return
	}

	response.Success(c, msg)
}

// DeleteMessage handles DELETE /copilot/threads/:id/messages/:msg_id
func (h *CopilotThreadHandler) DeleteMessage(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	msgID, err2 := strconv.ParseUint(c.Param("msg_id"), 10, 32)
	if err != nil || err2 != nil {
		response.BadRequest(c, "Invalid thread ID or message ID")
		return
	}

	if err := h.threadRepo.DeleteMessage(accountID, uint(threadID), uint(msgID)); err != nil {
		if err == gorm.ErrRecordNotFound {
			response.NotFound(c, "Message not found")
			return
		}
		response.InternalError(c, "Failed to delete message")
		return
	}

	logger.WithComponent("copilot").Info("deleted thread message",
		"account_id", accountID,
		"thread_id", threadID,
		"message_id", msgID,
	)

	response.Success(c, gin.H{"deleted": true, "message_id": msgID})
}

// ClearMessages handles DELETE /copilot/threads/:id/messages
func (h *CopilotThreadHandler) ClearMessages(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	// Verify thread exists
	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	if err := h.threadRepo.ClearMessages(accountID, uint(threadID)); err != nil {
		response.InternalError(c, "Failed to clear messages")
		return
	}

	logger.WithComponent("copilot").Info("cleared all messages in copilot thread",
		"account_id", accountID,
		"thread_id", threadID,
	)

	response.Success(c, gin.H{"cleared": true, "thread_id": threadID})
}

// FeedbackReq defines the rating/comment on a Copilot message
type FeedbackReq struct {
	Feedback string `json:"feedback" binding:"required"` // thumbs_up, thumbs_down, none
	Notes    string `json:"notes"`
}

// UpdateFeedback handles POST/PUT /copilot/threads/:id/messages/:msg_id/feedback
func (h *CopilotThreadHandler) UpdateFeedback(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	msgID, err2 := strconv.ParseUint(c.Param("msg_id"), 10, 32)
	if err != nil || err2 != nil {
		response.BadRequest(c, "Invalid thread ID or message ID")
		return
	}

	var req FeedbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Feedback != domain.CopilotFeedbackThumbsUp &&
		req.Feedback != domain.CopilotFeedbackThumbsDown &&
		req.Feedback != domain.CopilotFeedbackNone {
		response.BadRequest(c, "Invalid feedback option. Must be thumbs_up, thumbs_down, or none")
		return
	}

	if err := h.threadRepo.UpdateFeedback(accountID, uint(threadID), uint(msgID), req.Feedback, req.Notes); err != nil {
		response.InternalError(c, "Failed to update feedback")
		return
	}

	logger.WithComponent("copilot").Info("copilot message feedback updated",
		"account_id", accountID,
		"thread_id", threadID,
		"message_id", msgID,
		"feedback", req.Feedback,
	)

	response.Success(c, gin.H{
		"message_id": msgID,
		"feedback":   req.Feedback,
		"notes":      req.Notes,
	})
}
