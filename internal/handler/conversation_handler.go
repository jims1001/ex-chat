package handler

import (
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type ConversationHandler struct {
	convRepo    *repository.ConversationRepository
	msgRepo     *repository.MessageRepository
	inboxRepo   *repository.InboxRepository
	contactRepo *repository.ContactRepository
}

func NewConversationHandler(
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	inboxRepo *repository.InboxRepository,
	contactRepo *repository.ContactRepository,
) *ConversationHandler {
	return &ConversationHandler{
		convRepo:    convRepo,
		msgRepo:     msgRepo,
		inboxRepo:   inboxRepo,
		contactRepo: contactRepo,
	}
}

type CreateConversationRequest struct {
	InboxID          uint   `json:"inbox_id" binding:"required"`
	ContactID        uint   `json:"contact_id" binding:"required"`
	AssigneeID       *uint  `json:"assignee_id"`
	Priority         string `json:"priority"`
	CustomAttributes string `json:"custom_attributes"`
}

type ToggleStatusRequest struct {
	Status       string     `json:"status" binding:"required"`
	SnoozedUntil *time.Time `json:"snoozed_until"`
}

type AssignmentRequest struct {
	AssigneeID *uint `json:"assignee_id"`
}

type CreateMessageRequest struct {
	Content     string `json:"content" binding:"required"`
	ContentType string `json:"content_type"`
	Private     bool   `json:"private"`
}

type WidgetCreateConversationRequest struct {
	SourceID string `json:"source_id" binding:"required"`
	Message  string `json:"message" binding:"required"`
}

type WidgetCreateMessageRequest struct {
	SourceID       string `json:"source_id" binding:"required"`
	ConversationID uint   `json:"conversation_id" binding:"required"`
	Content        string `json:"content" binding:"required"`
}

// ----------------- Agent Endpoints -----------------

func (h *ConversationHandler) ListConversations(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	status := c.Query("status")
	priority := c.Query("priority")

	var assigneeID *uint
	if aStr := c.Query("assignee_id"); aStr != "" {
		if id, err := strconv.ParseUint(aStr, 10, 64); err == nil {
			uid := uint(id)
			assigneeID = &uid
		}
	}

	var inboxID *uint
	if iStr := c.Query("inbox_id"); iStr != "" {
		if id, err := strconv.ParseUint(iStr, 10, 64); err == nil {
			iid := uint(id)
			inboxID = &iid
		}
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}

	conversations, total, err := h.convRepo.List(accountID, status, priority, assigneeID, inboxID, page, pageSize)
	if err != nil {
		response.InternalError(c, "Failed to list conversations")
		return
	}

	response.Paginated(c, conversations, total, page, pageSize)
}

func (h *ConversationHandler) CreateConversation(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	priority := req.Priority
	if priority == "" {
		priority = domain.PriorityMedium
	}

	conv := domain.Conversation{
		AccountID:        accountID,
		InboxID:          req.InboxID,
		ContactID:        req.ContactID,
		AssigneeID:       req.AssigneeID,
		Status:           domain.ConversationStatusOpen,
		Priority:         priority,
		CustomAttributes: req.CustomAttributes,
	}

	if err := h.convRepo.Create(&conv); err != nil {
		response.InternalError(c, "Failed to create conversation")
		return
	}

	fullConv, _ := h.convRepo.FindByID(accountID, conv.ID)
	response.Created(c, fullConv)
}

func (h *ConversationHandler) GetConversation(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.convRepo.FindByID(accountID, uint(id))
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	response.Success(c, conv)
}

func (h *ConversationHandler) ToggleStatus(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req ToggleStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Status != domain.ConversationStatusOpen &&
		req.Status != domain.ConversationStatusResolved &&
		req.Status != domain.ConversationStatusPending &&
		req.Status != domain.ConversationStatusSnoozed {
		response.BadRequest(c, "Invalid status value")
		return
	}

	if err := h.convRepo.UpdateStatus(accountID, uint(id), req.Status, req.SnoozedUntil); err != nil {
		response.InternalError(c, "Failed to update conversation status")
		return
	}

	conv, _ := h.convRepo.FindByID(accountID, uint(id))
	response.Success(c, conv)
}

func (h *ConversationHandler) Assign(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req AssignmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if err := h.convRepo.Assign(accountID, uint(id), req.AssigneeID); err != nil {
		response.InternalError(c, "Failed to assign conversation")
		return
	}

	conv, _ := h.convRepo.FindByID(accountID, uint(id))
	response.Success(c, conv)
}

func (h *ConversationHandler) ListMessages(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	convID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}

	messages, total, err := h.msgRepo.ListByConversation(accountID, uint(convID), true, page, pageSize)
	if err != nil {
		response.InternalError(c, "Failed to list messages")
		return
	}

	response.Paginated(c, messages, total, page, pageSize)
}

func (h *ConversationHandler) CreateMessage(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	rawUserID, _ := c.Get(middleware.ContextUserID)
	userID := rawUserID.(uint)

	convID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.convRepo.FindByID(accountID, uint(convID))
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	var req CreateMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	contentType := req.ContentType
	if contentType == "" {
		contentType = domain.ContentTypeText
	}

	msgType := domain.MessageTypeOutgoing
	if req.Private {
		msgType = domain.MessageTypeActivity
	}

	msg := domain.Message{
		AccountID:      accountID,
		ConversationID: conv.ID,
		SenderType:     domain.SenderTypeUser,
		SenderID:       userID,
		MessageType:    msgType,
		ContentType:    contentType,
		Content:        req.Content,
		Private:        req.Private,
		Status:         domain.MessageStatusSent,
	}

	if err := h.msgRepo.Create(&msg); err != nil {
		response.InternalError(c, "Failed to create message")
		return
	}

	_ = h.convRepo.TouchActivity(accountID, conv.ID)

	response.Created(c, msg)
}

// ----------------- Public Widget Endpoints -----------------

func (h *ConversationHandler) WidgetCreateConversation(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	var req WidgetCreateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	contact, err := h.contactRepo.FindContactBySourceID(inbox.ID, req.SourceID)
	if err != nil || contact == nil {
		newContact := domain.Contact{
			AccountID: inbox.AccountID,
			Name:      "Visitor " + req.SourceID[:min(8, len(req.SourceID))],
		}
		if err := h.contactRepo.Create(&newContact); err != nil {
			response.InternalError(c, "Failed to create contact")
			return
		}
		_, _ = h.contactRepo.FindOrCreateContactInbox(newContact.ID, inbox.ID, req.SourceID)
		contact = &newContact
	}

	conv := domain.Conversation{
		AccountID: inbox.AccountID,
		InboxID:   inbox.ID,
		ContactID: contact.ID,
		Status:    domain.ConversationStatusOpen,
		Priority:  domain.PriorityMedium,
	}

	if err := h.convRepo.Create(&conv); err != nil {
		response.InternalError(c, "Failed to start conversation")
		return
	}

	msg := domain.Message{
		AccountID:      inbox.AccountID,
		ConversationID: conv.ID,
		SenderType:     domain.SenderTypeContact,
		SenderID:       contact.ID,
		MessageType:    domain.MessageTypeIncoming,
		ContentType:    domain.ContentTypeText,
		Content:        req.Message,
		Private:        false,
		Status:         domain.MessageStatusSent,
	}

	if err := h.msgRepo.Create(&msg); err != nil {
		response.InternalError(c, "Failed to create initial message")
		return
	}

	fullConv, _ := h.convRepo.FindByID(inbox.AccountID, conv.ID)
	response.Created(c, gin.H{
		"conversation": fullConv,
		"message":      msg,
	})
}

func (h *ConversationHandler) WidgetListMessages(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	convID, err := strconv.ParseUint(c.Query("conversation_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))

	// Notice: includePrivate is false so visitors NEVER see private agent notes!
	messages, total, err := h.msgRepo.ListByConversation(inbox.AccountID, uint(convID), false, page, pageSize)
	if err != nil {
		response.InternalError(c, "Failed to list messages")
		return
	}

	response.Paginated(c, messages, total, page, pageSize)
}

func (h *ConversationHandler) WidgetCreateMessage(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	var req WidgetCreateMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	contact, err := h.contactRepo.FindContactBySourceID(inbox.ID, req.SourceID)
	if err != nil || contact == nil {
		response.NotFound(c, "Contact not found for this source_id")
		return
	}

	conv, err := h.convRepo.FindByID(inbox.AccountID, req.ConversationID)
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	msg := domain.Message{
		AccountID:      inbox.AccountID,
		ConversationID: conv.ID,
		SenderType:     domain.SenderTypeContact,
		SenderID:       contact.ID,
		MessageType:    domain.MessageTypeIncoming,
		ContentType:    domain.ContentTypeText,
		Content:        req.Content,
		Private:        false,
		Status:         domain.MessageStatusSent,
	}

	if err := h.msgRepo.Create(&msg); err != nil {
		response.InternalError(c, "Failed to send message")
		return
	}

	// Re-open conversation if it was resolved
	if conv.Status == domain.ConversationStatusResolved {
		_ = h.convRepo.UpdateStatus(inbox.AccountID, conv.ID, domain.ConversationStatusOpen, nil)
	} else {
		_ = h.convRepo.TouchActivity(inbox.AccountID, conv.ID)
	}

	response.Created(c, msg)
}
