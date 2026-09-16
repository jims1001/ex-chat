package handler

import (
	"encoding/json"
	"errors"
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
	"github.com/OracleBetX-Projects/ex-chat/pkg/security"
	"github.com/gin-gonic/gin"
)

// TicketHandler manages HTTP endpoints for service tickets, comments, assignment and status lifecycle
type TicketHandler struct {
	repo *repository.TicketRepository
}

// NewTicketHandler creates a new TicketHandler instance
func NewTicketHandler(repo *repository.TicketRepository) *TicketHandler {
	return &TicketHandler{repo: repo}
}

func (h *TicketHandler) getAccountID(c *gin.Context) uint {
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

func (h *TicketHandler) getUserID(c *gin.Context) *uint {
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

// CreateTicketRequest defines parameters for creating a new ticket
type CreateTicketRequest struct {
	Title            string     `json:"title" binding:"required"`
	Description      string     `json:"description"`
	Priority         string     `json:"priority"` // low, medium, high, urgent
	AssignedGroup    string     `json:"assigned_group"`
	AssigneeID       *uint      `json:"assignee_id"`
	ContactID        *uint      `json:"contact_id"`
	ConversationID   *uint      `json:"conversation_id"`
	SLAPolicyID      *uint      `json:"sla_policy_id"`
	DueAt            *time.Time `json:"due_at"`
	WaitingReason    string     `json:"waiting_reason"`
	CustomAttributes string     `json:"custom_attributes"`
}

// UpdateTicketRequest defines parameters for updating an existing ticket
type UpdateTicketRequest struct {
	Title            *string    `json:"title"`
	Description      *string    `json:"description"`
	Priority         *string    `json:"priority"`
	Status           *string    `json:"status"`
	AssignedGroup    *string    `json:"assigned_group"`
	AssigneeID       *uint      `json:"assignee_id"`
	ContactID        *uint      `json:"contact_id"`
	ConversationID   *uint      `json:"conversation_id"`
	SLAPolicyID      *uint      `json:"sla_policy_id"`
	DueAt            *time.Time `json:"due_at"`
	CustomAttributes *string    `json:"custom_attributes"`
	Version          *int       `json:"version"` // 乐观锁版本号
}

// UpdateTicketStatusRequest defines status transition parameters
type UpdateTicketStatusRequest struct {
	Status        string     `json:"status" binding:"required"` // open, pending, resolved, closed
	Reason        string     `json:"reason"`
	WaitingReason string     `json:"waiting_reason"`
	ResumeAt      *time.Time `json:"resume_at"`
	AutoCloseAt   *time.Time `json:"auto_close_at"`
	Comment       string     `json:"comment"`
}

// WaitTicketRequest defines parameters for putting a ticket into waiting state
type WaitTicketRequest struct {
	WaitingReason string     `json:"waiting_reason"`
	ResumeAt      *time.Time `json:"resume_at"`
	AutoCloseAt   *time.Time `json:"auto_close_at"`
	Reason        string     `json:"reason"`
	Comment       string     `json:"comment"`
}

// ResumeTicketRequest defines parameters for resuming a ticket
type ResumeTicketRequest struct {
	Reason  string `json:"reason"`
	Comment string `json:"comment"`
}

// RemindTicketRequest defines parameters for reminding a ticket
type RemindTicketRequest struct {
	Message string `json:"message"`
}

// BatchRemindTicketsRequest defines parameters for batch reminding tickets
type BatchRemindTicketsRequest struct {
	TicketIDs []uint `json:"ticket_ids"`
	Message   string `json:"message"`
}

// AssignTicketRequest defines agent/team assignment parameters
type AssignTicketRequest struct {
	AssigneeID    *uint  `json:"assignee_id"`
	AssignedGroup string `json:"assigned_group"`
}

// CreateTicketCommentRequest defines parameters for adding a comment/internal note
type CreateTicketCommentRequest struct {
	Content   string `json:"content" binding:"required"`
	IsPrivate *bool  `json:"is_private"`
}

// List returns a list of tickets matching filters
func (h *TicketHandler) List(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var filter repository.TicketFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		response.BadRequest(c, "Invalid query parameters: "+err.Error())
		return
	}

	tickets, total, err := h.repo.List(accountID, filter)
	if err != nil {
		logger.WithComponent("ticket").Error("failed to list tickets", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to retrieve tickets: "+err.Error())
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
		"data":    tickets,
		"payload": tickets,
		"meta": gin.H{
			"total_count": total,
			"page":        page,
			"limit":       limit,
		},
	})
}

// Get retrieves details of a single ticket by numeric ID or ticket number
func (h *TicketHandler) Get(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	if idOrNumber == "" {
		response.BadRequest(c, "Ticket identifier is required")
		return
	}

	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    ticket,
		"payload": ticket,
	})
}

// Create handles creating a new ticket
func (h *TicketHandler) Create(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req CreateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if err := h.repo.ValidateReferences(accountID, req.AssigneeID, req.ContactID, req.ConversationID, req.SLAPolicyID); err != nil {
		response.BadRequest(c, "Invalid ticket reference: "+err.Error())
		return
	}

	ticket := &domain.Ticket{
		AccountID:        accountID,
		Title:            strings.TrimSpace(req.Title),
		Description:      req.Description,
		Priority:         req.Priority,
		Status:           "open",
		AssignedGroup:    req.AssignedGroup,
		AssigneeID:       req.AssigneeID,
		ContactID:        req.ContactID,
		ConversationID:   req.ConversationID,
		CreatorID:        h.getUserID(c),
		CustomAttributes: req.CustomAttributes,
		SLAPolicyID:      req.SLAPolicyID,
		DueAt:            req.DueAt,
	}

	if err := h.repo.Create(ticket); err != nil {
		logger.WithComponent("ticket").Error("failed to create ticket", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to create ticket: "+err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    ticket,
		"payload": ticket,
		"message": "Ticket created successfully",
	})
}

// Update modifies an existing ticket
func (h *TicketHandler) Update(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req UpdateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if err := h.repo.ValidateReferences(accountID, req.AssigneeID, req.ContactID, req.ConversationID, req.SLAPolicyID); err != nil {
		response.BadRequest(c, "Invalid ticket reference: "+err.Error())
		return
	}

	changes := make(map[string]interface{})
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		changes["title"] = *req.Title
		ticket.Title = *req.Title
	}
	if req.Description != nil {
		changes["description"] = *req.Description
		ticket.Description = *req.Description
	}
	if req.Priority != nil && *req.Priority != "" {
		changes["priority"] = *req.Priority
		ticket.Priority = *req.Priority
	}
	if req.Status != nil && *req.Status != "" {
		changes["status"] = *req.Status
		ticket.Status = *req.Status
	}
	if req.AssignedGroup != nil {
		changes["assigned_group"] = *req.AssignedGroup
		ticket.AssignedGroup = *req.AssignedGroup
	}
	if req.AssigneeID != nil {
		changes["assignee_id"] = *req.AssigneeID
		ticket.AssigneeID = req.AssigneeID
	}
	if req.ContactID != nil {
		changes["contact_id"] = *req.ContactID
		ticket.ContactID = req.ContactID
	}
	if req.ConversationID != nil {
		changes["conversation_id"] = *req.ConversationID
		ticket.ConversationID = req.ConversationID
	}
	if req.SLAPolicyID != nil {
		changes["sla_policy_id"] = *req.SLAPolicyID
		ticket.SLAPolicyID = req.SLAPolicyID
	}
	if req.DueAt != nil {
		changes["due_at"] = *req.DueAt
		ticket.DueAt = req.DueAt
	}
	if req.CustomAttributes != nil {
		changes["custom_attributes"] = *req.CustomAttributes
		ticket.CustomAttributes = *req.CustomAttributes
	}

	if req.Version != nil {
		if err := h.repo.UpdateWithVersion(ticket, *req.Version, h.getUserID(c)); err != nil {
			if errors.Is(err, repository.ErrOptimisticLockConflict) {
				c.JSON(http.StatusConflict, gin.H{
					"error":   "Optimistic lock conflict: ticket has been modified by another operator",
					"message": "Ticket has been modified by another user. Please refresh and try again.",
				})
				return
			}
			logger.WithComponent("ticket").Error("failed to update ticket with version", "ticket_id", ticket.ID, "error", err.Error())
			response.InternalError(c, "Failed to update ticket: "+err.Error())
			return
		}
	} else {
		if err := h.repo.UpdateWithActor(ticket, h.getUserID(c)); err != nil {
			logger.WithComponent("ticket").Error("failed to update ticket", "ticket_id", ticket.ID, "error", err.Error())
			response.InternalError(c, "Failed to update ticket: "+err.Error())
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    ticket,
		"payload": ticket,
		"message": "Ticket updated successfully",
	})
}

// Delete removes a ticket
func (h *TicketHandler) Delete(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	if err := h.repo.Delete(accountID, ticket.ID); err != nil {
		logger.WithComponent("ticket").Error("failed to delete ticket", "ticket_id", ticket.ID, "error", err.Error())
		response.InternalError(c, "Failed to delete ticket: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Ticket deleted successfully",
		"id":      ticket.ID,
	})
}

// UpdateStatus performs a state transition on the ticket
func (h *TicketHandler) UpdateStatus(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req UpdateTicketStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	newStatus := strings.ToLower(strings.TrimSpace(req.Status))
	validStatuses := map[string]bool{"open": true, "pending": true, "resolved": true, "closed": true}
	if !validStatuses[newStatus] {
		response.BadRequest(c, "Invalid ticket status; must be one of open, pending, resolved, closed")
		return
	}

	err = h.repo.TransitionStatus(accountID, ticket, repository.TransitionParams{
		ToStatus:      newStatus,
		Reason:        req.Reason,
		WaitingReason: req.WaitingReason,
		ResumeAt:      req.ResumeAt,
		AutoCloseAt:   req.AutoCloseAt,
		UserID:        h.getUserID(c),
		Comment:       req.Comment,
	})
	if err != nil {
		logger.WithComponent("ticket").Error("failed to update ticket status", "ticket_id", ticket.ID, "error", err.Error())
		response.InternalError(c, "Failed to update ticket status: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    ticket,
		"payload": ticket,
		"message": "Ticket status updated successfully",
	})
}

// Wait puts a ticket into waiting state
func (h *TicketHandler) Wait(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req WaitTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	updated, err := h.repo.Wait(accountID, ticket.ID, h.getUserID(c), req.WaitingReason, req.ResumeAt, req.AutoCloseAt, req.Reason, req.Comment)
	if err != nil {
		logger.WithComponent("ticket").Error("failed to put ticket into waiting state", "ticket_id", ticket.ID, "error", err.Error())
		response.InternalError(c, "Failed to wait ticket: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    updated,
		"payload": updated,
		"message": "Ticket is now waiting",
	})
}

// Resume wakes up a waiting ticket back to open
func (h *TicketHandler) Resume(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req ResumeTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	updated, err := h.repo.Resume(accountID, ticket.ID, h.getUserID(c), req.Reason, req.Comment)
	if err != nil {
		logger.WithComponent("ticket").Error("failed to resume ticket", "ticket_id", ticket.ID, "error", err.Error())
		response.InternalError(c, "Failed to resume ticket: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    updated,
		"payload": updated,
		"message": "Ticket resumed to open",
	})
}

// Remind triggers a reminder for a waiting ticket
func (h *TicketHandler) Remind(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req RemindTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	updated, err := h.repo.Remind(accountID, ticket.ID, h.getUserID(c), req.Message)
	if err != nil {
		logger.WithComponent("ticket").Error("failed to remind ticket", "ticket_id", ticket.ID, "error", err.Error())
		response.InternalError(c, "Failed to remind ticket: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":           updated,
		"payload":        updated,
		"reminder_count": updated.ReminderCount,
		"message":        "Ticket reminder sent successfully",
	})
}

// BatchRemind sends reminders to pending/waiting tickets in bulk
func (h *TicketHandler) BatchRemind(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req BatchRemindTicketsRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	remindedIDs, err := h.repo.BatchRemind(accountID, req.TicketIDs, h.getUserID(c), req.Message)
	if err != nil {
		logger.WithComponent("ticket").Error("failed to batch remind tickets", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to batch remind tickets: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"reminded_count": len(remindedIDs),
		"count":          len(remindedIDs),
		"ticket_ids":     remindedIDs,
		"message":        "Batch reminders sent successfully",
	})
}

// ListStatusHistory returns the formal status audit records for a ticket
func (h *TicketHandler) ListStatusHistory(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	histories, err := h.repo.ListStatusHistory(accountID, ticket.ID)
	if err != nil {
		response.InternalError(c, "Failed to retrieve status history: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    histories,
		"payload": histories,
	})
}

// Assign updates ticket assignee and team/group
func (h *TicketHandler) Assign(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req AssignTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	ticket.AssigneeID = req.AssigneeID
	if strings.TrimSpace(req.AssignedGroup) != "" {
		ticket.AssignedGroup = req.AssignedGroup
	}

	if err := h.repo.Update(ticket); err != nil {
		logger.WithComponent("ticket").Error("failed to assign ticket", "ticket_id", ticket.ID, "error", err.Error())
		response.InternalError(c, "Failed to assign ticket: "+err.Error())
		return
	}

	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"assignee_id":    ticket.AssigneeID,
		"assigned_group": ticket.AssignedGroup,
	})
	_ = h.repo.AddActivity(&domain.TicketActivity{
		AccountID: accountID,
		TicketID:  ticket.ID,
		UserID:    h.getUserID(c),
		Action:    "assigned",
		Details:   string(detailsJSON),
	})

	c.JSON(http.StatusOK, gin.H{
		"data":    ticket,
		"payload": ticket,
		"message": "Ticket assigned successfully",
	})
}

// ListComments retrieves comments of a ticket
func (h *TicketHandler) ListComments(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	comments, err := h.repo.ListComments(accountID, ticket.ID)
	if err != nil {
		response.InternalError(c, "Failed to retrieve comments: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    comments,
		"payload": comments,
	})
}

// CreateComment adds a comment/internal note to a ticket
func (h *TicketHandler) CreateComment(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req CreateTicketCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}

	isPrivate := true
	if req.IsPrivate != nil {
		isPrivate = *req.IsPrivate
	}

	comment := &domain.TicketComment{
		AccountID: accountID,
		TicketID:  ticket.ID,
		UserID:    h.getUserID(c),
		Content:   strings.TrimSpace(req.Content),
		IsPrivate: isPrivate,
	}

	if err := h.repo.AddComment(comment); err != nil {
		logger.WithComponent("ticket").Error("failed to add comment", "ticket_id", ticket.ID, "error", err.Error())
		response.InternalError(c, "Failed to add comment: "+err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    comment,
		"payload": comment,
		"message": "Comment added successfully",
	})
}

// ListActivities returns timeline history for a ticket
func (h *TicketHandler) ListActivities(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	activities, err := h.repo.ListActivities(accountID, ticket.ID)
	if err != nil {
		response.InternalError(c, "Failed to retrieve activities: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    activities,
		"payload": activities,
	})
}

// Stats returns dashboard summary metrics for tickets
func (h *TicketHandler) Stats(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	stats, err := h.repo.GetStats(accountID)
	if err != nil {
		response.InternalError(c, "Failed to compute ticket stats: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    stats,
		"payload": stats,
	})
}

// Search performs full text search on tickets
func (h *TicketHandler) Search(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	q := c.Query("q")
	limitStr := c.DefaultQuery("limit", "25")
	limit, _ := strconv.Atoi(limitStr)

	tickets, err := h.repo.Search(accountID, q, limit)
	if err != nil {
		response.InternalError(c, "Failed to search tickets: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    tickets,
		"payload": tickets,
		"total":   len(tickets),
	})
}

// CreateAttachmentRequest defines parameters for uploading an attachment
type CreateAttachmentRequest struct {
	FileName string `json:"file_name" binding:"required"`
	FileType string `json:"file_type"`
	FileSize int64  `json:"file_size"`
	DataURL  string `json:"data_url" binding:"required"`
}

// CreateAttachment attaches a file to a ticket
func (h *TicketHandler) CreateAttachment(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	var req CreateAttachmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid attachment payload: "+err.Error())
		return
	}

	userID := h.getUserID(c)
	attachment, err := h.repo.CreateAttachment(accountID, ticket.ID, req.FileName, req.FileType, req.FileSize, req.DataURL, userID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    attachment,
		"payload": attachment,
		"message": "Attachment uploaded successfully",
	})
}

// ListAttachments returns all attachments for a ticket
func (h *TicketHandler) ListAttachments(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	attachments, err := h.repo.ListAttachments(accountID, ticket.ID)
	if err != nil {
		response.InternalError(c, "Failed to retrieve attachments: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    attachments,
		"payload": attachments,
	})
}

// DeleteAttachment removes an attachment from a ticket
func (h *TicketHandler) DeleteAttachment(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	idOrNumber := c.Param("id")
	ticket, err := h.repo.FindByIDOrNumber(accountID, idOrNumber)
	if err != nil {
		response.NotFound(c, "Ticket not found")
		return
	}

	attachmentIDStr := c.Param("attachment_id")
	attID, err := strconv.ParseUint(attachmentIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid attachment ID")
		return
	}

	userID := h.getUserID(c)
	if err := h.repo.DeleteAttachment(accountID, ticket.ID, uint(attID), userID); err != nil {
		response.InternalError(c, "Failed to delete attachment: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Attachment deleted successfully",
	})
}

// ListByConversation retrieves tickets linked to a conversation
func (h *TicketHandler) ListByConversation(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	convIDStr := c.Param("id")
	if convIDStr == "" {
		convIDStr = c.Param("conversation_id")
	}
	convID, err := strconv.ParseUint(convIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	tickets, err := h.repo.ListByConversation(accountID, uint(convID))
	if err != nil {
		response.InternalError(c, "Failed to retrieve conversation tickets: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    tickets,
		"payload": tickets,
		"total":   len(tickets),
	})
}

// CreateForConversation creates a new ticket linked to a specific conversation
func (h *TicketHandler) CreateForConversation(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	convIDStr := c.Param("id")
	if convIDStr == "" {
		convIDStr = c.Param("conversation_id")
	}
	convID, err := strconv.ParseUint(convIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req CreateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Validation failed: "+err.Error())
		return
	}

	cid := uint(convID)
	priority := req.Priority
	if priority == "" {
		priority = "high"
	}
	assignedGroup := req.AssignedGroup
	if assignedGroup == "" {
		assignedGroup = "服务团队"
	}

	ticket := &domain.Ticket{
		AccountID:        accountID,
		Title:            strings.TrimSpace(req.Title),
		Description:      req.Description,
		Priority:         priority,
		Status:           "open",
		AssignedGroup:    assignedGroup,
		AssigneeID:       req.AssigneeID,
		ContactID:        req.ContactID,
		ConversationID:   &cid,
		CreatorID:        h.getUserID(c),
		CustomAttributes: req.CustomAttributes,
		SLAPolicyID:      req.SLAPolicyID,
		DueAt:            req.DueAt,
		WaitingReason:    req.WaitingReason,
	}

	if err := h.repo.Create(ticket); err != nil {
		response.InternalError(c, "Failed to create ticket for conversation: "+err.Error())
		return
	}

	logger.WithComponent("ticket_handler").Info("ticket created from conversation",
		"ticket_id", ticket.ID,
		"ticket_number", ticket.TicketNumber,
		"conversation_id", convID,
	)

	c.JSON(http.StatusCreated, gin.H{
		"data":    ticket,
		"payload": ticket,
		"message": "Ticket created successfully for conversation",
	})
}

// ListByContact retrieves all tickets for a customer contact
func (h *TicketHandler) ListByContact(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	contactIDStr := c.Param("id")
	if contactIDStr == "" {
		contactIDStr = c.Param("contact_id")
	}
	contactID, err := strconv.ParseUint(contactIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	tickets, err := h.repo.ListByContact(accountID, uint(contactID))
	if err != nil {
		response.InternalError(c, "Failed to retrieve contact tickets: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    tickets,
		"payload": tickets,
		"total":   len(tickets),
	})
}

// CreateForContact creates a ticket directly for a contact
func (h *TicketHandler) CreateForContact(c *gin.Context) {
	accountID := h.getAccountID(c)
	if accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	contactIDStr := c.Param("id")
	if contactIDStr == "" {
		contactIDStr = c.Param("contact_id")
	}
	contactID, err := strconv.ParseUint(contactIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	var req CreateTicketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Validation failed: "+err.Error())
		return
	}

	cntID := uint(contactID)
	priority := req.Priority
	if priority == "" {
		priority = "high"
	}
	assignedGroup := req.AssignedGroup
	if assignedGroup == "" {
		assignedGroup = "服务团队"
	}

	ticket := &domain.Ticket{
		AccountID:        accountID,
		Title:            strings.TrimSpace(req.Title),
		Description:      req.Description,
		Priority:         priority,
		Status:           "open",
		AssignedGroup:    assignedGroup,
		AssigneeID:       req.AssigneeID,
		ContactID:        &cntID,
		ConversationID:   req.ConversationID,
		CreatorID:        h.getUserID(c),
		CustomAttributes: req.CustomAttributes,
		SLAPolicyID:      req.SLAPolicyID,
		DueAt:            req.DueAt,
		WaitingReason:    req.WaitingReason,
	}

	if err := h.repo.Create(ticket); err != nil {
		response.InternalError(c, "Failed to create ticket for contact: "+err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"data":    ticket,
		"payload": ticket,
		"message": "Ticket created successfully for contact",
	})
}

// Watcher handlers
func (h *TicketHandler) AddWatcher(c *gin.Context) {
	accountID := h.getAccountID(c)
	ticketIDStr := c.Param("id")
	tid, _ := strconv.ParseUint(ticketIDStr, 10, 32)
	var req struct {
		UserID uint `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.repo.AddWatcher(accountID, uint(tid), req.UserID); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"status": "ok", "message": "Watcher added"})
}

func (h *TicketHandler) RemoveWatcher(c *gin.Context) {
	accountID := h.getAccountID(c)
	ticketIDStr := c.Param("id")
	tid, _ := strconv.ParseUint(ticketIDStr, 10, 32)
	userIDStr := c.Param("user_id")
	uid, _ := strconv.ParseUint(userIDStr, 10, 32)
	if err := h.repo.RemoveWatcher(accountID, uint(tid), uint(uid)); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"status": "ok", "message": "Watcher removed"})
}

func (h *TicketHandler) ListWatchers(c *gin.Context) {
	accountID := h.getAccountID(c)
	ticketIDStr := c.Param("id")
	tid, _ := strconv.ParseUint(ticketIDStr, 10, 32)
	watchers, err := h.repo.ListWatchers(accountID, uint(tid))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, watchers)
}

// BulkUpdateTickets updates multiple tickets at once
func (h *TicketHandler) BulkUpdateTickets(c *gin.Context) {
	accountID := h.getAccountID(c)
	var req struct {
		TicketIDs     []uint `json:"ticket_ids" binding:"required"`
		Status        string `json:"status,omitempty"`
		Priority      string `json:"priority,omitempty"`
		AssignedGroup string `json:"assigned_group,omitempty"`
		AssigneeID    *uint  `json:"assignee_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(req.TicketIDs) == 0 {
		response.BadRequest(c, "ticket_ids cannot be empty")
		return
	}

	updates := make(map[string]interface{})
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Priority != "" {
		updates["priority"] = req.Priority
	}
	if req.AssignedGroup != "" {
		updates["assigned_group"] = req.AssignedGroup
	}
	if req.AssigneeID != nil {
		updates["assignee_id"] = req.AssigneeID
	}

	affected, err := h.repo.BulkUpdate(accountID, req.TicketIDs, updates, h.getUserID(c))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{
		"status":        "ok",
		"rows_affected": affected,
	})
}

// ExportTickets exports tickets matching filter as CSV
func (h *TicketHandler) ExportTickets(c *gin.Context) {
	accountID := h.getAccountID(c)
	var filter repository.TicketFilter
	_ = c.ShouldBindQuery(&filter)
	filter.Limit = 1000

	tickets, _, err := h.repo.List(accountID, filter)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	c.Header("Content-Disposition", "attachment; filename=tickets_export.csv")
	c.Header("Content-Type", "text/csv; charset=utf-8")

	csvData := "Ticket Number,Title,Status,Priority,Assigned Group,Created At\n"
	for _, t := range tickets {
		csvData += fmt.Sprintf("\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\"\n",
			strings.ReplaceAll(security.SanitizeCSVCell(t.TicketNumber), "\"", "\"\""),
			strings.ReplaceAll(security.SanitizeCSVCell(t.Title), "\"", "\"\""),
			security.SanitizeCSVCell(t.Status),
			security.SanitizeCSVCell(t.Priority),
			strings.ReplaceAll(security.SanitizeCSVCell(t.AssignedGroup), "\"", "\"\""),
			t.CreatedAt.Format(time.RFC3339),
		)
	}
	c.String(http.StatusOK, csvData)
}

// StartTicketBackgroundWorker starts a background periodic scanner for auto-close and waiting recovery
func (h *TicketHandler) StartTicketBackgroundWorker(interval time.Duration) chan struct{} {
	stopChan := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopChan:
				return
			case t := <-ticker.C:
				closed, resumed, _ := h.repo.ProcessAutoCloseAndResume(t.UTC())
				if closed > 0 || resumed > 0 {
					logger.WithComponent("ticket_worker").Info("processed ticket auto-close and resume",
						"auto_closed", closed,
						"auto_resumed", resumed,
					)
				}
			}
		}
	}()
	return stopChan
}
