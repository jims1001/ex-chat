package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type ConversationHandler struct {
	convRepo          *repository.ConversationRepository
	msgRepo           *repository.MessageRepository
	inboxRepo         *repository.InboxRepository
	contactRepo       *repository.ContactRepository
	notificationRepo  *repository.NotificationRepository
	routingService    *service.RoutingService
	automationService *service.AutomationService
	webhookService    *service.WebhookService
	pushService       *service.PushService
	slaService        *service.SLAService
	emailService      *service.EmailService
	campaignService   *service.CampaignService
	hub               *ws.Hub
}

func NewConversationHandler(
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	inboxRepo *repository.InboxRepository,
	contactRepo *repository.ContactRepository,
	routingService *service.RoutingService,
	hub *ws.Hub,
) *ConversationHandler {
	return &ConversationHandler{
		convRepo:       convRepo,
		msgRepo:        msgRepo,
		inboxRepo:      inboxRepo,
		contactRepo:    contactRepo,
		routingService: routingService,
		hub:            hub,
	}
}

func (h *ConversationHandler) SetAutomationAndWebhook(as *service.AutomationService, ws *service.WebhookService) {
	h.automationService = as
	h.webhookService = ws
}

func (h *ConversationHandler) SetPushService(ps *service.PushService) {
	h.pushService = ps
}

func (h *ConversationHandler) SetEmailService(es *service.EmailService) {
	h.emailService = es
}

func (h *ConversationHandler) SetNotificationRepo(nr *repository.NotificationRepository) {
	h.notificationRepo = nr
}

func (h *ConversationHandler) SetSLAService(ss *service.SLAService) {
	h.slaService = ss
}

func (h *ConversationHandler) SetCampaignService(cs *service.CampaignService) {
	h.campaignService = cs
}

type CreateConversationRequest struct {
	InboxID          uint   `json:"inbox_id" binding:"required"`
	ContactID        uint   `json:"contact_id" binding:"required"`
	AssigneeID       *uint  `json:"assignee_id"`
	TeamID           *uint  `json:"team_id"`
	Priority         string `json:"priority"`
	CustomAttributes string `json:"custom_attributes"`
}

type ToggleStatusRequest struct {
	Status       string     `json:"status" binding:"required"`
	SnoozedUntil *time.Time `json:"snoozed_until"`
}

type AssignmentRequest struct {
	AssigneeID *uint `json:"assignee_id"`
	TeamID     *uint `json:"team_id"`
}

type CreateMessageRequest struct {
	Content     string `json:"content" binding:"required"`
	ContentType string `json:"content_type"`
	Private     bool   `json:"private"`
	PrivateNote bool   `json:"private_note"`
	IsPrivate   bool   `json:"is_private"`
	MessageType string `json:"message_type"`
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

	if req.AssigneeID != nil {
		var capPolicy domain.CapacityPolicy
		db := h.convRepo.GetDB()
		found := false
		if err := db.Where("user_id = ?", *req.AssigneeID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
			found = true
		} else if accountID > 0 {
			if err := db.Where("account_id = ? AND user_id = 0", accountID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				found = true
			}
		}
		if found && capPolicy.ConversationLimit > 0 {
			var currentCount int64
			db.Model(&domain.Conversation{}).
				Where("assignee_id = ? AND status != ?", *req.AssigneeID, domain.ConversationStatusResolved).
				Count(&currentCount)
			if currentCount >= int64(capPolicy.ConversationLimit) {
				response.BadRequest(c, "Agent has reached maximum conversation capacity limit")
				return
			}
		}
	}

	if req.TeamID != nil {
		var team domain.Team
		if err := h.convRepo.GetDB().Where("account_id = ? AND id = ?", accountID, *req.TeamID).First(&team).Error; err != nil {
			response.BadRequest(c, "Invalid team ID")
			return
		}
	}

	conv := domain.Conversation{
		AccountID:        accountID,
		InboxID:          req.InboxID,
		ContactID:        req.ContactID,
		AssigneeID:       req.AssigneeID,
		TeamID:           req.TeamID,
		Status:           domain.ConversationStatusOpen,
		Priority:         priority,
		CustomAttributes: req.CustomAttributes,
	}

	if err := h.convRepo.Create(&conv); err != nil {
		response.InternalError(c, "Failed to create conversation")
		return
	}

	if conv.AssigneeID == nil && h.routingService != nil {
		_, _ = h.routingService.AutoAssign(&conv)
	}

	fullConv, _ := h.convRepo.FindByID(accountID, conv.ID)
	logger.WithComponent("conversation").Info("conversation created",
		"conversation_id", conv.ID,
		"account_id", accountID,
		"inbox_id", req.InboxID,
		"contact_id", req.ContactID,
		"priority", priority,
	)
	if h.automationService != nil {
		h.automationService.HandleConversationCreated(&conv)
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "conversation_created", fullConv)
	}
	if h.campaignService != nil {
		_, _ = h.campaignService.TriggerOngoingCampaignForContact(accountID, conv.InboxID, conv.ContactID, conv.ID)
	}
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

	if rawUserID, exists := c.Get(middleware.ContextUserID); exists && rawUserID != nil {
		if userID, ok := rawUserID.(uint); ok && userID > 0 {
			now := time.Now()
			_ = h.convRepo.UpdateLastSeen(accountID, conv.ID, userID, now)
			if h.notificationRepo != nil {
				_ = h.notificationRepo.MarkConversationNotificationsRead(c.Request.Context(), accountID, userID, conv.ID)
			}
			if updatedConv, err := h.convRepo.FindByID(accountID, conv.ID); err == nil && updatedConv != nil {
				conv = updatedConv
			}
		}
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
		logger.WithComponent("conversation").Error("failed to update conversation status",
			"conversation_id", id,
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update conversation status")
		return
	}

	logger.WithComponent("conversation").Info("conversation status updated",
		"conversation_id", id,
		"account_id", accountID,
		"new_status", req.Status,
	)

	conv, _ := h.convRepo.FindByID(accountID, uint(id))
	if h.slaService != nil && conv != nil {
		_, _ = h.slaService.EvaluateConversation(conv)
		if updated, err := h.convRepo.FindByID(accountID, uint(id)); err == nil && updated != nil {
			conv = updated
		}
	}
	if h.automationService != nil && conv != nil {
		h.automationService.HandleConversationUpdated(conv)
	}
	if h.webhookService != nil && conv != nil {
		h.webhookService.Dispatch(accountID, "conversation_status_changed", conv)
	}
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

	if req.AssigneeID != nil {
		var capPolicy domain.CapacityPolicy
		db := h.convRepo.GetDB()
		found := false
		if err := db.Where("user_id = ?", *req.AssigneeID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
			found = true
		} else if accountID > 0 {
			if err := db.Where("account_id = ? AND user_id = 0", accountID).First(&capPolicy).Error; err == nil && capPolicy.ID > 0 {
				found = true
			}
		}
		if found && capPolicy.ConversationLimit > 0 {
			var currentCount int64
			db.Model(&domain.Conversation{}).
				Where("assignee_id = ? AND status != ? AND id != ?", *req.AssigneeID, domain.ConversationStatusResolved, id).
				Count(&currentCount)
			if currentCount >= int64(capPolicy.ConversationLimit) {
				response.BadRequest(c, "Agent has reached maximum conversation capacity limit")
				return
			}
		}
	}

	if req.TeamID != nil {
		var team domain.Team
		if err := h.convRepo.GetDB().Where("account_id = ? AND id = ?", accountID, *req.TeamID).First(&team).Error; err != nil {
			response.BadRequest(c, "Invalid team ID")
			return
		}
	}

	if err := h.convRepo.AssignWithTeam(accountID, uint(id), req.AssigneeID, req.TeamID); err != nil {
		logger.WithComponent("conversation").Error("failed to assign conversation",
			"conversation_id", id,
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to assign conversation")
		return
	}

	logger.WithComponent("conversation").Info("conversation assigned",
		"conversation_id", id,
		"account_id", accountID,
		"assignee_id", req.AssigneeID,
		"team_id", req.TeamID,
	)

	conv, _ := h.convRepo.FindByID(accountID, uint(id))
	if h.automationService != nil && conv != nil {
		h.automationService.HandleConversationUpdated(conv)
	}
	if h.webhookService != nil && conv != nil {
		h.webhookService.Dispatch(accountID, "conversation_status_changed", conv)
	}
	if h.pushService != nil && req.AssigneeID != nil {
		go h.pushService.Dispatch(context.Background(), *req.AssigneeID, accountID, service.PushPayload{
			Title:        "Conversation Assigned",
			Body:         fmt.Sprintf("Conversation #%d has been assigned to you", id),
			AccountID:    accountID,
			ResourceID:   uint(id),
			ResourceType: "conversation",
		})
	}
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

	isPrivate := req.Private || req.PrivateNote || req.IsPrivate || req.MessageType == "activity" || req.ContentType == "activity" || req.ContentType == "internal_note"
	msgType := domain.MessageTypeOutgoing
	if isPrivate {
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
		Private:        isPrivate,
		Status:         domain.MessageStatusSent,
	}

	if err := h.msgRepo.Create(&msg); err != nil {
		logger.WithComponent("message").Error("failed to create message",
			"conversation_id", conv.ID,
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create message")
		return
	}

	logger.WithComponent("message").Info("message created",
		"message_id", msg.ID,
		"conversation_id", conv.ID,
		"account_id", accountID,
		"sender_type", msg.SenderType,
		"sender_id", msg.SenderID,
		"private", msg.Private,
	)

	_ = h.convRepo.TouchActivity(accountID, conv.ID)

	if h.automationService != nil {
		h.automationService.HandleMessageCreated(conv, &msg)
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "message_created", msg)
	}
	if h.pushService != nil && conv.AssigneeID != nil {
		go h.pushService.Dispatch(context.Background(), *conv.AssigneeID, accountID, service.PushPayload{
			Title:        "New Customer Message",
			Body:         msg.Content,
			AccountID:    accountID,
			ResourceID:   conv.ID,
			ResourceType: "conversation",
		})
	}

	if h.slaService != nil && msg.MessageType == domain.MessageTypeOutgoing && !isPrivate {
		_, _ = h.slaService.EvaluateConversation(conv)
	}

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
		logger.WithComponent("message").Error("failed to create initial widget message",
			"conversation_id", conv.ID,
			"account_id", inbox.AccountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create initial message")
		return
	}

	logger.WithComponent("message").Info("initial widget message created",
		"message_id", msg.ID,
		"conversation_id", conv.ID,
		"account_id", inbox.AccountID,
		"sender_type", msg.SenderType,
		"sender_id", msg.SenderID,
	)

	if h.routingService != nil {
		_, _ = h.routingService.AutoAssign(&conv)
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      inbox.AccountID,
			ConversationID: conv.ID,
			Data:           msg,
		})
	}

	fullConv, _ := h.convRepo.FindByID(inbox.AccountID, conv.ID)
	if h.automationService != nil {
		h.automationService.HandleConversationCreated(&conv)
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(inbox.AccountID, "conversation_created", fullConv)
	}
	if h.campaignService != nil {
		_, _ = h.campaignService.TriggerOngoingCampaignForContact(inbox.AccountID, conv.InboxID, conv.ContactID, conv.ID)
	}
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
		logger.WithComponent("message").Error("failed to send widget message",
			"conversation_id", conv.ID,
			"account_id", inbox.AccountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to send message")
		return
	}

	logger.WithComponent("message").Info("widget message sent",
		"message_id", msg.ID,
		"conversation_id", conv.ID,
		"account_id", inbox.AccountID,
		"sender_type", msg.SenderType,
		"sender_id", msg.SenderID,
	)

	// Re-open conversation if it was resolved
	if conv.Status == domain.ConversationStatusResolved {
		_ = h.convRepo.UpdateStatus(inbox.AccountID, conv.ID, domain.ConversationStatusOpen, nil)
	} else {
		_ = h.convRepo.TouchActivity(inbox.AccountID, conv.ID)
	}

	if h.automationService != nil {
		h.automationService.HandleMessageCreated(conv, &msg)
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(inbox.AccountID, "message_created", msg)
	}
	if h.pushService != nil && conv.AssigneeID != nil {
		go h.pushService.Dispatch(context.Background(), *conv.AssigneeID, inbox.AccountID, service.PushPayload{
			Title:        "New Customer Message",
			Body:         msg.Content,
			AccountID:    inbox.AccountID,
			ResourceID:   conv.ID,
			ResourceType: "conversation",
		})
	}

	response.Created(c, msg)
}

// UpdateLastSeen updates agent read position and clears unread count
func (h *ConversationHandler) UpdateLastSeen(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req struct {
		AgentLastSeenAt *time.Time `json:"agent_last_seen_at"`
	}
	_ = c.ShouldBindJSON(&req)

	seenAt := time.Now()
	if req.AgentLastSeenAt != nil && !req.AgentLastSeenAt.IsZero() {
		seenAt = *req.AgentLastSeenAt
	}

	var userID uint
	if rawUserID, exists := c.Get(middleware.ContextUserID); exists && rawUserID != nil {
		if uid, ok := rawUserID.(uint); ok {
			userID = uid
		}
	}

	if err := h.convRepo.UpdateLastSeen(accountID, uint(id), userID, seenAt); err != nil {
		response.InternalError(c, "Failed to update last seen: "+err.Error())
		return
	}

	if h.notificationRepo != nil && userID > 0 {
		_ = h.notificationRepo.MarkConversationNotificationsRead(c.Request.Context(), accountID, userID, uint(id))
	}

	conv, _ := h.convRepo.FindByID(accountID, uint(id))
	response.Success(c, conv)
}

// MarkUnread marks a conversation as unread by rewinding read position
func (h *ConversationHandler) MarkUnread(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	if err := h.convRepo.MarkUnread(accountID, uint(id)); err != nil {
		response.InternalError(c, "Failed to mark unread: "+err.Error())
		return
	}

	conv, _ := h.convRepo.FindByID(accountID, uint(id))
	response.Success(c, conv)
}

// MuteConversation mutes notifications/events for the conversation
func (h *ConversationHandler) MuteConversation(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	if err := h.convRepo.ToggleMute(accountID, uint(id), true); err != nil {
		response.InternalError(c, "Failed to mute conversation")
		return
	}

	response.Success(c, gin.H{"id": uint(id), "muted": true})
}

// UnmuteConversation unmutes the conversation
func (h *ConversationHandler) UnmuteConversation(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	if err := h.convRepo.ToggleMute(accountID, uint(id), false); err != nil {
		response.InternalError(c, "Failed to unmute conversation")
		return
	}

	response.Success(c, gin.H{"id": uint(id), "muted": false})
}

// ToggleTypingStatus broadcasts agent typing indicator via websocket
func (h *ConversationHandler) ToggleTypingStatus(c *gin.Context) {
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

	var req struct {
		TypingStatus string `json:"typing_status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "typing_status is required ('on' or 'off')")
		return
	}

	var userID uint
	if rawUserID, exists := c.Get(middleware.ContextUserID); exists && rawUserID != nil {
		if uid, ok := rawUserID.(uint); ok {
			userID = uid
		}
	}

	var userName string
	if userID > 0 {
		var user domain.User
		if err := h.convRepo.GetDB().Where("id = ?", userID).First(&user).Error; err == nil {
			userName = user.Name
		}
	}

	eventName := "conversation.typing_on"
	if req.TypingStatus == "off" {
		eventName = "conversation.typing_off"
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           eventName,
			AccountID:      accountID,
			ConversationID: conv.ID,
			Data: gin.H{
				"conversation_id": conv.ID,
				"user": gin.H{
					"id":   userID,
					"name": userName,
				},
				"typing_status": req.TypingStatus,
			},
		})
	}

	response.Success(c, gin.H{"status": "ok", "typing_status": req.TypingStatus})
}

// SendTranscript sends a transcript of the conversation to an email address
func (h *ConversationHandler) SendTranscript(c *gin.Context) {
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

	var req struct {
		Email string `json:"email"`
	}
	_ = c.ShouldBindJSON(&req)

	targetEmail := req.Email
	if targetEmail == "" && conv.Contact != nil {
		targetEmail = conv.Contact.Email
	}
	if targetEmail == "" {
		response.BadRequest(c, "Email is required to send transcript")
		return
	}

	messages, _, _ := h.msgRepo.ListByConversation(accountID, conv.ID, true, 1, 200)

	if h.emailService != nil {
		if err := h.emailService.SendTranscript(c.Request.Context(), accountID, conv, messages, targetEmail); err != nil {
			logger.WithComponent("conversation").Error("failed to send transcript email",
				"conversation_id", conv.ID,
				"account_id", accountID,
				"target_email", targetEmail,
				"error", err.Error(),
			)
			response.InternalError(c, "Failed to send transcript email: "+err.Error())
			return
		}
	}

	logger.WithComponent("conversation").Info("transcript email sent successfully",
		"conversation_id", conv.ID,
		"account_id", accountID,
		"target_email", targetEmail,
		"messages_count", len(messages),
	)

	response.Success(c, gin.H{
		"message":        "Transcript sent successfully",
		"email":          targetEmail,
		"messages_count": len(messages),
	})
}

// WidgetUpdateLastSeen updates the contact's read position from web widget
func (h *ConversationHandler) WidgetUpdateLastSeen(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	var req struct {
		WebsiteToken   string     `json:"website_token"`
		SourceID       string     `json:"source_id"`
		ConversationID uint       `json:"conversation_id"`
		LastSeenAt     *time.Time `json:"last_seen_at"`
	}
	_ = c.ShouldBindJSON(&req)
	if websiteToken == "" {
		websiteToken = req.WebsiteToken
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	convID := req.ConversationID
	if convID == 0 {
		if cid, err := strconv.ParseUint(c.Query("conversation_id"), 10, 64); err == nil {
			convID = uint(cid)
		}
	}
	if convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.convRepo.FindByID(inbox.AccountID, convID)
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	seenAt := time.Now()
	if req.LastSeenAt != nil && !req.LastSeenAt.IsZero() {
		seenAt = *req.LastSeenAt
	}

	if err := h.convRepo.UpdateContactLastSeen(inbox.AccountID, conv.ID, seenAt); err != nil {
		response.InternalError(c, "Failed to update contact last seen")
		return
	}

	response.Success(c, gin.H{
		"id":                   conv.ID,
		"contact_last_seen_at": seenAt,
	})
}

// WidgetToggleTyping broadcasts typing status from the widget visitor
func (h *ConversationHandler) WidgetToggleTyping(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	var req struct {
		WebsiteToken   string `json:"website_token"`
		SourceID       string `json:"source_id"`
		ConversationID uint   `json:"conversation_id"`
		TypingStatus   string `json:"typing_status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}
	if websiteToken == "" {
		websiteToken = req.WebsiteToken
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	conv, err := h.convRepo.FindByID(inbox.AccountID, req.ConversationID)
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	var contactName string
	if req.SourceID != "" {
		contact, _ := h.contactRepo.FindContactBySourceID(inbox.ID, req.SourceID)
		if contact != nil {
			contactName = contact.Name
		}
	}

	eventName := "conversation.typing_on"
	if req.TypingStatus == "off" {
		eventName = "conversation.typing_off"
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           eventName,
			AccountID:      inbox.AccountID,
			ConversationID: conv.ID,
			Data: gin.H{
				"conversation_id": conv.ID,
				"contact": gin.H{
					"source_id": req.SourceID,
					"name":      contactName,
				},
				"typing_status": req.TypingStatus,
			},
		})
	}

	response.Success(c, gin.H{"status": "ok", "typing_status": req.TypingStatus})
}

// WidgetSendTranscript sends transcript from the widget visitor
func (h *ConversationHandler) WidgetSendTranscript(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	var req struct {
		WebsiteToken   string `json:"website_token"`
		ConversationID uint   `json:"conversation_id"`
		Email          string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}
	if websiteToken == "" {
		websiteToken = req.WebsiteToken
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	conv, err := h.convRepo.FindByID(inbox.AccountID, req.ConversationID)
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	targetEmail := req.Email
	if targetEmail == "" && conv.Contact != nil {
		targetEmail = conv.Contact.Email
	}
	if targetEmail == "" {
		response.BadRequest(c, "Email is required to send transcript")
		return
	}

	messages, _, _ := h.msgRepo.ListByConversation(inbox.AccountID, conv.ID, false, 1, 100)

	if conv.Inbox == nil {
		conv.Inbox = inbox
	}

	if h.emailService != nil {
		if err := h.emailService.SendTranscript(c.Request.Context(), inbox.AccountID, conv, messages, targetEmail); err != nil {
			response.InternalError(c, "Failed to send transcript email: "+err.Error())
			return
		}
	}

	response.Success(c, gin.H{
		"message":        "Transcript sent successfully",
		"email":          targetEmail,
		"messages_count": len(messages),
	})
}

// UpdateMessage edits an existing message's content
func (h *ConversationHandler) UpdateMessage(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	convID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	msgID, err := strconv.ParseUint(c.Param("message_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid message ID")
		return
	}

	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if strings.TrimSpace(req.Content) == "" {
		response.BadRequest(c, "Message content cannot be empty")
		return
	}

	msg, err := h.msgRepo.FindByIDAndConversation(accountID, uint(convID), uint(msgID))
	if err != nil || msg == nil {
		response.NotFound(c, "Message not found in this conversation")
		return
	}

	if msg.Deleted {
		response.BadRequest(c, "Cannot edit a deleted message")
		return
	}

	updatedMsg, err := h.msgRepo.UpdateContent(accountID, uint(convID), uint(msgID), req.Content)
	if err != nil {
		response.InternalError(c, "Failed to update message: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageUpdated,
			AccountID:      accountID,
			ConversationID: uint(convID),
			Data:           updatedMsg,
		})
	}

	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "message_updated", updatedMsg)
	}

	response.Success(c, updatedMsg)
}

// DeleteMessage soft-deletes a message from a conversation
func (h *ConversationHandler) DeleteMessage(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	convID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	msgID, err := strconv.ParseUint(c.Param("message_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid message ID")
		return
	}

	msg, err := h.msgRepo.FindByIDAndConversation(accountID, uint(convID), uint(msgID))
	if err != nil || msg == nil || msg.Deleted {
		response.NotFound(c, "Message not found in this conversation")
		return
	}

	deletedMsg, err := h.msgRepo.DeleteMessage(accountID, uint(convID), uint(msgID))
	if err != nil {
		response.InternalError(c, "Failed to delete message: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageDeleted,
			AccountID:      accountID,
			ConversationID: uint(convID),
			Data:           deletedMsg,
		})
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageUpdated,
			AccountID:      accountID,
			ConversationID: uint(convID),
			Data:           deletedMsg,
		})
	}

	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "message_deleted", deletedMsg)
	}

	response.Success(c, deletedMsg)
}

// RetryMessage re-attempts delivery of a failed message
func (h *ConversationHandler) RetryMessage(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	convID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	msgID, err := strconv.ParseUint(c.Param("message_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid message ID")
		return
	}

	msg, err := h.msgRepo.FindByIDAndConversation(accountID, uint(convID), uint(msgID))
	if err != nil || msg == nil {
		response.NotFound(c, "Message not found in this conversation")
		return
	}

	if msg.Deleted {
		response.BadRequest(c, "Cannot retry a deleted message")
		return
	}

	if msg.Status != domain.MessageStatusFailed {
		response.BadRequest(c, "Only failed messages can be retried")
		return
	}

	retriedMsg, err := h.msgRepo.RetryMessage(accountID, uint(convID), uint(msgID))
	if err != nil {
		response.InternalError(c, "Failed to retry message: "+err.Error())
		return
	}

	conv, _ := h.convRepo.FindByID(accountID, uint(convID))

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageUpdated,
			AccountID:      accountID,
			ConversationID: uint(convID),
			Data:           retriedMsg,
		})
	}

	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "message_created", retriedMsg)
	}

	if h.pushService != nil && conv != nil && conv.AssigneeID != nil {
		go h.pushService.Dispatch(context.Background(), *conv.AssigneeID, accountID, service.PushPayload{
			Title:        "Customer Message Retried",
			Body:         retriedMsg.Content,
			AccountID:    accountID,
			ResourceID:   conv.ID,
			ResourceType: "conversation",
		})
	}

	response.Success(c, retriedMsg)
}


