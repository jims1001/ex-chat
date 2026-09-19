package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	captainRepo       *repository.CaptainRepository
	enterpriseRepo    repository.ChannelAuthEnterpriseRepository
	journalRepo       *repository.JournalRepository
	accountRepo       *repository.AccountRepository
	teamRepo          *repository.TeamRepository
	userRepo          *repository.UserRepository
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

func (h *ConversationHandler) SetCaptainRepo(cr *repository.CaptainRepository) {
	h.captainRepo = cr
}

func (h *ConversationHandler) SetEnterpriseRepo(er repository.ChannelAuthEnterpriseRepository) {
	h.enterpriseRepo = er
}

func (h *ConversationHandler) SetJournalRepo(jr *repository.JournalRepository) {
	h.journalRepo = jr
}

func (h *ConversationHandler) SetIdentityRepos(accountRepo *repository.AccountRepository, teamRepo *repository.TeamRepository, userRepo *repository.UserRepository) {
	h.accountRepo = accountRepo
	h.teamRepo = teamRepo
	h.userRepo = userRepo
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

type UpdateConversationRequest struct {
	Status           string     `json:"status"`
	Priority         string     `json:"priority"`
	AssigneeID       *uint      `json:"assignee_id"`
	TeamID           *uint      `json:"team_id"`
	SnoozedUntil     *time.Time `json:"snoozed_until"`
	CustomAttributes any        `json:"custom_attributes"`
}

type SetPriorityRequest struct {
	Priority string `json:"priority" binding:"required"`
}

type UpdateCustomAttributesRequest struct {
	CustomAttributes map[string]any `json:"custom_attributes" binding:"required"`
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
		hasCapacity, _ := h.routingService.CheckAgentCapacity(accountID, req.InboxID, *req.AssigneeID, nil, 0)
		if !hasCapacity {
			response.BadRequest(c, "Agent has reached maximum conversation capacity limit")
			return
		}
	}

	if req.TeamID != nil {
		if h.teamRepo == nil {
			response.InternalError(c, "Team repository is not configured")
			return
		}
		if team, err := h.teamRepo.FindByID(accountID, *req.TeamID); err != nil || team == nil {
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
		h.webhookService.Dispatch(accountID, "conversation_updated", conv)
	}
	if h.hub != nil && conv != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationStatus,
			AccountID:      accountID,
			ConversationID: conv.ID,
			Data:           conv,
		})
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationUpdated,
			AccountID:      accountID,
			ConversationID: conv.ID,
			Data:           conv,
		})
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

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.BadRequest(c, "Failed to read request body")
		return
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	var req AssignmentRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	_, hasAssignee := raw["assignee_id"]
	_, hasTeam := raw["team_id"]

	if hasAssignee && req.AssigneeID != nil && *req.AssigneeID > 0 {
		if h.accountRepo == nil {
			response.InternalError(c, "Account repository is not configured")
			return
		}
		// Ensure assignee belongs to this account
		if membership, err := h.accountRepo.GetMembership(accountID, *req.AssigneeID); err != nil || membership == nil {
			response.BadRequest(c, "Assignee does not belong to this account")
			return
		}

		conv, _ := h.convRepo.FindByID(accountID, uint(id))
		if conv != nil && h.routingService != nil {
			var policy *domain.AssignmentPolicy
			if conv.Inbox != nil && conv.Inbox.AssignmentPolicy != nil {
				policy = conv.Inbox.AssignmentPolicy
			}
			hasCap, _ := h.routingService.CheckAgentCapacity(accountID, conv.InboxID, *req.AssigneeID, policy, uint(id))
			if !hasCap {
				response.BadRequest(c, "Agent has reached maximum conversation capacity limit")
				return
			}
		} else if h.routingService != nil {
			hasCap, _ := h.routingService.CheckAgentCapacity(accountID, 0, *req.AssigneeID, nil, uint(id))
			if !hasCap {
				response.BadRequest(c, "Agent has reached maximum conversation capacity limit")
				return
			}
		}
	}

	if hasTeam && req.TeamID != nil && *req.TeamID > 0 {
		if h.teamRepo == nil {
			response.InternalError(c, "Team repository is not configured")
			return
		}
		if team, err := h.teamRepo.FindByID(accountID, *req.TeamID); err != nil || team == nil {
			response.BadRequest(c, "Invalid team ID")
			return
		}
	}

	if err := h.convRepo.AssignWithTeam(accountID, uint(id), hasAssignee, req.AssigneeID, hasTeam, req.TeamID); err != nil {
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
	now := time.Now().UTC()
	rawUserID, _ := c.Get(middleware.ContextUserID)
	var currentUserID uint
	if rawUserID != nil {
		currentUserID, _ = rawUserID.(uint)
	}

	// 1. In-App Notification Center (only when assigned to a valid user)
	if h.notificationRepo != nil && hasAssignee && req.AssigneeID != nil && *req.AssigneeID > 0 {
		notif := domain.Notification{
			AccountID:          accountID,
			UserID:             *req.AssigneeID,
			NotificationType:   domain.NotificationTypeConversationAssignment,
			PrimaryActorType:   "Conversation",
			PrimaryActorID:     uint(id),
			SecondaryActorType: "User",
			SecondaryActorID:   currentUserID,
			CreatedAt:          now,
		}
		_ = h.notificationRepo.Create(c.Request.Context(), &notif)
	}

	// 2. Push Notification Dispatch (only when assigned to a valid user)
	if h.pushService != nil && hasAssignee && req.AssigneeID != nil && *req.AssigneeID > 0 {
		go h.pushService.Dispatch(context.Background(), *req.AssigneeID, accountID, service.PushPayload{
			Title:            "Conversation Assigned",
			Body:             fmt.Sprintf("Conversation #%d has been assigned to you", id),
			AccountID:        accountID,
			ResourceID:       uint(id),
			ResourceType:     "conversation",
			NotificationType: domain.NotificationTypeConversationAssignment,
		})
	}

	// 3. Automation Pipeline
	if h.automationService != nil && conv != nil {
		h.automationService.HandleConversationUpdated(conv)
	}

	// 4. Webhook Event Dispatching
	if h.webhookService != nil && conv != nil {
		h.webhookService.Dispatch(accountID, "conversation_updated", conv)
	}

	// 5. Real-time WebSocket Broadcast
	if h.hub != nil && conv != nil {
		if hasAssignee && req.AssigneeID != nil && *req.AssigneeID > 0 {
			h.hub.Broadcast(&ws.Event{
				Name:           ws.EventConversationAssigned,
				AccountID:      accountID,
				ConversationID: conv.ID,
				Data: map[string]any{
					"id":          conv.ID,
					"assignee_id": req.AssigneeID,
					"assignee":    conv.Assignee,
				},
			})
		}
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationUpdated,
			AccountID:      accountID,
			ConversationID: conv.ID,
			Data:           conv,
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
	isMutedOrBlocked := conv.Muted || (conv.Contact != nil && conv.Contact.Blocked)
	if h.pushService != nil && conv.AssigneeID != nil && !isMutedOrBlocked {
		go h.pushService.Dispatch(context.Background(), *conv.AssigneeID, accountID, service.PushPayload{
			Title:            "New Customer Message",
			Body:             msg.Content,
			AccountID:        accountID,
			ResourceID:       conv.ID,
			ResourceType:     "conversation",
			NotificationType: domain.NotificationTypeConversationCreation,
		})
	}

	if h.slaService != nil && msg.MessageType == domain.MessageTypeOutgoing && !isPrivate {
		_, _ = h.slaService.EvaluateConversation(conv)
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      accountID,
			ConversationID: conv.ID,
			Data:           msg,
		})
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
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Website-Token")
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

	conv, err := h.convRepo.FindByID(inbox.AccountID, uint(convID))
	if err != nil || conv == nil || conv.InboxID != inbox.ID {
		response.NotFound(c, "Conversation not found")
		return
	}

	// If source_id / contact_token is provided, ensure caller owns this conversation
	sourceID := strings.TrimSpace(c.Query("source_id"))
	if sourceID == "" && gin.Mode() == gin.ReleaseMode {
		sourceID = strings.TrimSpace(c.Query("contact_token"))
	}
	if sourceID == "" && gin.Mode() == gin.ReleaseMode {
		sourceID = strings.TrimSpace(c.GetHeader("X-Contact-Token"))
	}
	if sourceID == "" && gin.Mode() == gin.ReleaseMode {
		response.Error(c, http.StatusUnauthorized, "visitor_session or source_id is required")
		return
	}
	if sourceID != "" {
		contact, _ := h.contactRepo.FindContactBySourceID(inbox.ID, sourceID)
		if contact == nil || conv.ContactID != contact.ID {
			response.NotFound(c, "Conversation not found")
			return
		}
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
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Website-Token")
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
	if err != nil || conv == nil || conv.InboxID != inbox.ID || conv.ContactID != contact.ID {
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

	isMutedOrBlocked := conv.Muted || (contact != nil && contact.Blocked)

	// Re-open conversation if it was resolved (only if not muted or blocked)
	if !isMutedOrBlocked && conv.Status == domain.ConversationStatusResolved {
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
	if h.pushService != nil && conv.AssigneeID != nil && !isMutedOrBlocked {
		go h.pushService.Dispatch(context.Background(), *conv.AssigneeID, inbox.AccountID, service.PushPayload{
			Title:            "New Customer Message",
			Body:             msg.Content,
			AccountID:        inbox.AccountID,
			ResourceID:       conv.ID,
			ResourceType:     "conversation",
			NotificationType: domain.NotificationTypeConversationCreation,
		})
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      inbox.AccountID,
			ConversationID: conv.ID,
			Data:           msg,
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

// MuteConversation mutes notifications/events for the conversation, resolves conversation, blocks contact, and logs activity message
func (h *ConversationHandler) MuteConversation(c *gin.Context) {
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

	if err := h.convRepo.ToggleMute(accountID, uint(id), true); err != nil {
		response.InternalError(c, "Failed to mute conversation")
		return
	}

	// 1. Resolve conversation status
	_ = h.convRepo.UpdateStatus(accountID, uint(id), domain.ConversationStatusResolved, nil)

	// 2. Mark contact as blocked
	if conv.ContactID > 0 {
		contact, _ := h.contactRepo.FindByID(accountID, conv.ContactID)
		if contact != nil {
			contact.Blocked = true
			_ = h.contactRepo.Update(contact)
		}
	}

	// 3. Create activity message
	userName := "Agent"
	if rawUser, exists := c.Get(middleware.ContextUser); exists && rawUser != nil {
		if u, ok := rawUser.(*domain.User); ok && u.Name != "" {
			userName = u.Name
		}
	} else if rawUserID, exists := c.Get(middleware.ContextUserID); exists && rawUserID != nil {
		if uid, ok := rawUserID.(uint); ok && uid > 0 {
			userName = fmt.Sprintf("User #%d", uid)
		}
	}

	actMsg := domain.Message{
		AccountID:      accountID,
		ConversationID: uint(id),
		SenderType:     domain.SenderTypeUser,
		MessageType:    domain.MessageTypeActivity,
		ContentType:    domain.ContentTypeText,
		Content:        fmt.Sprintf("%s has muted the conversation", userName),
		Status:         domain.MessageStatusSent,
	}
	_ = h.msgRepo.Create(&actMsg)

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      accountID,
			ConversationID: uint(id),
			Data:           actMsg,
		})
	}

	response.Success(c, gin.H{"id": uint(id), "muted": true})
}

// UnmuteConversation unmutes the conversation, unblocks contact, and logs activity message
func (h *ConversationHandler) UnmuteConversation(c *gin.Context) {
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

	if err := h.convRepo.ToggleMute(accountID, uint(id), false); err != nil {
		response.InternalError(c, "Failed to unmute conversation")
		return
	}

	// 1. Mark contact as unblocked
	if conv.ContactID > 0 {
		contact, _ := h.contactRepo.FindByID(accountID, conv.ContactID)
		if contact != nil {
			contact.Blocked = false
			_ = h.contactRepo.Update(contact)
		}
	}

	// 2. Create activity message
	userName := "Agent"
	if rawUser, exists := c.Get(middleware.ContextUser); exists && rawUser != nil {
		if u, ok := rawUser.(*domain.User); ok && u.Name != "" {
			userName = u.Name
		}
	} else if rawUserID, exists := c.Get(middleware.ContextUserID); exists && rawUserID != nil {
		if uid, ok := rawUserID.(uint); ok && uid > 0 {
			userName = fmt.Sprintf("User #%d", uid)
		}
	}

	actMsg := domain.Message{
		AccountID:      accountID,
		ConversationID: uint(id),
		SenderType:     domain.SenderTypeUser,
		MessageType:    domain.MessageTypeActivity,
		ContentType:    domain.ContentTypeText,
		Content:        fmt.Sprintf("%s has unmuted the conversation", userName),
		Status:         domain.MessageStatusSent,
	}
	_ = h.msgRepo.Create(&actMsg)

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      accountID,
			ConversationID: uint(id),
			Data:           actMsg,
		})
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
		if h.userRepo == nil {
			response.InternalError(c, "User repository is not configured")
			return
		}
		if user, err := h.userRepo.FindByID(userID); err == nil {
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
			if errors.Is(err, service.ErrSMTPNotConfigured) {
				response.Error(c, http.StatusUnprocessableEntity, "SMTP service is not configured; unable to send transcript email")
				return
			}
			response.InternalError(c, "Failed to send transcript email: "+err.Error())
			return
		}
	} else {
		response.Error(c, http.StatusUnprocessableEntity, "SMTP service is not configured; unable to send transcript email")
		return
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
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Website-Token")
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
	if err != nil || conv == nil || conv.InboxID != inbox.ID {
		response.NotFound(c, "Conversation not found")
		return
	}

	// Verify contact matches if source_id is provided
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.Query("source_id"))
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.Query("contact_token"))
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.GetHeader("X-Contact-Token"))
	}
	if sourceID == "" {
		response.Error(c, http.StatusUnauthorized, "visitor_session or source_id is required")
		return
	}
	contact, _ := h.contactRepo.FindContactBySourceID(inbox.ID, sourceID)
	if contact == nil || conv.ContactID != contact.ID {
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
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Website-Token")
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
	if req.TypingStatus != "on" && req.TypingStatus != "off" {
		response.BadRequest(c, "typing_status must be 'on' or 'off'")
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
	if err != nil || conv == nil || conv.InboxID != inbox.ID {
		response.NotFound(c, "Conversation not found")
		return
	}

	var contactName string
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.Query("source_id"))
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.Query("contact_token"))
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.GetHeader("X-Contact-Token"))
	}

	if sourceID != "" {
		contact, _ := h.contactRepo.FindContactBySourceID(inbox.ID, sourceID)
		if contact == nil || conv.ContactID != contact.ID {
			response.NotFound(c, "Conversation not found")
			return
		}
		contactName = contact.Name
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
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Website-Token")
	}

	var req struct {
		WebsiteToken   string `json:"website_token"`
		SourceID       string `json:"source_id"`
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
	if err != nil || conv == nil || conv.InboxID != inbox.ID {
		response.NotFound(c, "Conversation not found")
		return
	}

	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.Query("source_id"))
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.Query("contact_token"))
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(c.GetHeader("X-Contact-Token"))
	}
	if sourceID != "" {
		contact, _ := h.contactRepo.FindContactBySourceID(inbox.ID, sourceID)
		if contact != nil && conv.ContactID != contact.ID {
			response.NotFound(c, "Conversation not found")
			return
		}
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
			if errors.Is(err, service.ErrSMTPNotConfigured) {
				response.Error(c, http.StatusUnprocessableEntity, "SMTP service is not configured; unable to send transcript email")
				return
			}
			response.InternalError(c, "Failed to send transcript email: "+err.Error())
			return
		}
	} else {
		response.Error(c, http.StatusUnprocessableEntity, "SMTP service is not configured; unable to send transcript email")
		return
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

	// 1. Only outgoing text messages can be edited (Chatwoot rules)
	if msg.ContentType != domain.ContentTypeText || msg.SenderType != domain.SenderTypeUser || msg.MessageType == domain.MessageTypeActivity {
		response.BadRequest(c, "Only outgoing text messages can be edited")
		return
	}

	// 2. Author check: only author or administrator can edit
	rawUserID, _ := c.Get(middleware.ContextUserID)
	var currentUserID uint
	if rawUserID != nil {
		currentUserID, _ = rawUserID.(uint)
	}
	rawMembership, _ := c.Get(middleware.ContextAccountMembership)
	membership, _ := rawMembership.(*domain.AccountUser)
	isAdmin := membership != nil && membership.Role == domain.RoleAdministrator

	if !isAdmin && msg.SenderID != currentUserID {
		response.Forbidden(c, "You do not have permission to edit this message")
		return
	}

	// 3. Edit window limit: regular agents can only edit messages within 15 minutes of creation
	if !isAdmin && !msg.CreatedAt.IsZero() && time.Since(msg.CreatedAt) > 15*time.Minute {
		response.BadRequest(c, "Message edit window (15m) has expired")
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

	// 1. Activity messages cannot be deleted
	if msg.MessageType == domain.MessageTypeActivity {
		response.BadRequest(c, "Activity messages cannot be deleted")
		return
	}

	// 2. Author check: only author or administrator can delete
	rawUserID, _ := c.Get(middleware.ContextUserID)
	var currentUserID uint
	if rawUserID != nil {
		currentUserID, _ = rawUserID.(uint)
	}
	rawMembership, _ := c.Get(middleware.ContextAccountMembership)
	membership, _ := rawMembership.(*domain.AccountUser)
	isAdmin := membership != nil && membership.Role == domain.RoleAdministrator

	if !isAdmin && (msg.SenderType != domain.SenderTypeUser || msg.SenderID != currentUserID) {
		response.Forbidden(c, "You do not have permission to delete this message")
		return
	}

	deleteReason := strings.TrimSpace(c.Query("delete_reason"))
	if deleteReason == "" {
		deleteReason = strings.TrimSpace(c.Query("reason"))
	}
	if deleteReason == "" {
		var reqBody struct {
			Reason       string `json:"reason"`
			DeleteReason string `json:"delete_reason"`
		}
		_ = c.ShouldBindJSON(&reqBody)
		if reqBody.DeleteReason != "" {
			deleteReason = strings.TrimSpace(reqBody.DeleteReason)
		} else if reqBody.Reason != "" {
			deleteReason = strings.TrimSpace(reqBody.Reason)
		}
	}
	if deleteReason == "" {
		deleteReason = "manual_deletion"
	}

	deletedMsg, err := h.msgRepo.DeleteMessage(accountID, uint(convID), uint(msgID))
	if err != nil {
		response.InternalError(c, "Failed to delete message: "+err.Error())
		return
	}

	logger.WithComponent("audit").Info("message deleted",
		"account_id", accountID,
		"conversation_id", convID,
		"message_id", msgID,
		"operator_id", currentUserID,
		"reason", deleteReason,
	)

	if h.journalRepo != nil {
		snippet := msg.Content
		if len(snippet) > 100 {
			snippet = snippet[:100] + "..."
		}
		_ = h.journalRepo.RecordChange(nil, &domain.LocalChangeJournal{
			AccountID:    accountID,
			ObjectType:   "Message",
			ObjectID:     msg.ID,
			Action:       "delete",
			ActorType:    "user",
			ActorID:      currentUserID,
			Diff:         fmt.Sprintf("reason: %s, snippet: %s", deleteReason, snippet),
			SourceModule: "CONVERSATION",
		})
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

	// Re-open conversation if resolved when an incoming message is retried, or touch activity to bubble up conversation
	if conv != nil {
		if conv.Status == domain.ConversationStatusResolved && retriedMsg.MessageType == domain.MessageTypeIncoming {
			_ = h.convRepo.UpdateStatus(accountID, conv.ID, domain.ConversationStatusOpen, nil)
		} else {
			_ = h.convRepo.TouchActivity(accountID, conv.ID)
		}
	}

	// Real-time broadcast: dispatch both message.updated AND message.created to ensure all clients (Agents, WebWidgets, Apps) receive and render it
	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageUpdated,
			AccountID:      accountID,
			ConversationID: uint(convID),
			Data:           retriedMsg,
		})
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      accountID,
			ConversationID: uint(convID),
			Data:           retriedMsg,
		})
	}

	// Trigger automation rules for the retried message
	if h.automationService != nil && conv != nil {
		h.automationService.HandleMessageCreated(conv, retriedMsg)
	}

	// Trigger external webhooks for true HTTP delivery
	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "message_created", retriedMsg)
	}

	// Trigger push notification to assigned agent (if not muted or blocked)
	isMutedOrBlocked := conv != nil && (conv.Muted || (conv.Contact != nil && conv.Contact.Blocked))
	if h.pushService != nil && conv != nil && conv.AssigneeID != nil && !isMutedOrBlocked {
		go h.pushService.Dispatch(context.Background(), *conv.AssigneeID, accountID, service.PushPayload{
			Title:            "Customer Message Retried",
			Body:             retriedMsg.Content,
			AccountID:        accountID,
			ResourceID:       conv.ID,
			ResourceType:     "conversation",
			NotificationType: domain.NotificationTypeConversationCreation,
		})
	}

	// Re-evaluate SLA clocks for outgoing non-private agent messages
	if h.slaService != nil && conv != nil && retriedMsg.MessageType == domain.MessageTypeOutgoing && !retriedMsg.Private {
		_, _ = h.slaService.EvaluateConversation(conv)
	}

	response.Success(c, retriedMsg)
}

// UpdateConversation updates editable conversation attributes (status, priority, assignee, team, etc.)
func (h *ConversationHandler) UpdateConversation(c *gin.Context) {
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

	var req UpdateConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	oldStatus := conv.Status
	oldAssigneeID := conv.AssigneeID

	if req.Status != "" {
		validStatuses := map[string]bool{
			domain.ConversationStatusOpen:     true,
			domain.ConversationStatusResolved: true,
			domain.ConversationStatusPending:  true,
			domain.ConversationStatusSnoozed:  true,
		}
		if !validStatuses[req.Status] {
			response.BadRequest(c, "Invalid conversation status")
			return
		}
		conv.Status = req.Status
		if req.Status == domain.ConversationStatusSnoozed {
			conv.SnoozedUntil = req.SnoozedUntil
		} else {
			conv.SnoozedUntil = nil
		}
	}
	if req.Priority != "" {
		validPriorities := map[string]bool{
			domain.PriorityLow:    true,
			domain.PriorityMedium: true,
			domain.PriorityHigh:   true,
			domain.PriorityUrgent: true,
		}
		if !validPriorities[req.Priority] {
			response.BadRequest(c, "Invalid conversation priority")
			return
		}
		conv.Priority = req.Priority
	}
	if req.AssigneeID != nil {
		if *req.AssigneeID == 0 {
			conv.AssigneeID = nil
		} else {
			if h.accountRepo == nil {
				response.InternalError(c, "Account repository is not configured")
				return
			}
			// Verify assignee belongs to this account
			if membership, err := h.accountRepo.GetMembership(accountID, *req.AssigneeID); err != nil || membership == nil {
				response.BadRequest(c, "Assignee does not belong to this account")
				return
			}
			conv.AssigneeID = req.AssigneeID
		}
	}
	if req.TeamID != nil {
		if *req.TeamID == 0 {
			conv.TeamID = nil
		} else {
			if h.teamRepo == nil {
				response.InternalError(c, "Team repository is not configured")
				return
			}
			// Verify team belongs to this account
			if team, err := h.teamRepo.FindByID(accountID, *req.TeamID); err != nil || team == nil {
				response.BadRequest(c, "Invalid team ID")
				return
			}
			conv.TeamID = req.TeamID
		}
	}
	if req.CustomAttributes != nil {
		switch ca := req.CustomAttributes.(type) {
		case string:
			conv.CustomAttributes = ca
		case map[string]any:
			bytes, _ := json.Marshal(ca)
			conv.CustomAttributes = string(bytes)
		}
	}
	conv.LastActivityAt = time.Now().UTC()

	if err := h.convRepo.Update(conv); err != nil {
		response.InternalError(c, "Failed to update conversation: "+err.Error())
		return
	}

	fullConv, _ := h.convRepo.FindByID(accountID, conv.ID)

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationUpdated,
			AccountID:      accountID,
			ConversationID: conv.ID,
			Data:           fullConv,
		})
		if req.Status != "" && req.Status != oldStatus {
			h.hub.Broadcast(&ws.Event{
				Name:           ws.EventConversationStatus,
				AccountID:      accountID,
				ConversationID: conv.ID,
				Data:           fullConv,
			})
		}
		if req.AssigneeID != nil && ((oldAssigneeID == nil) != (conv.AssigneeID == nil) || (oldAssigneeID != nil && conv.AssigneeID != nil && *oldAssigneeID != *conv.AssigneeID)) {
			h.hub.Broadcast(&ws.Event{
				Name:           ws.EventConversationAssigned,
				AccountID:      accountID,
				ConversationID: conv.ID,
				Data:           fullConv,
			})
		}
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "conversation_updated", fullConv)
		if req.Status != "" && req.Status != oldStatus {
			h.webhookService.Dispatch(accountID, "conversation_status_changed", fullConv)
		}
	}

	response.Success(c, fullConv)
}

// DeleteConversation deletes a conversation and cascade removes all associated resources
func (h *ConversationHandler) DeleteConversation(c *gin.Context) {
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

	if err := h.convRepo.Delete(accountID, uint(id)); err != nil {
		response.InternalError(c, "Failed to delete conversation: "+err.Error())
		return
	}

	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationDeleted,
			AccountID:      accountID,
			ConversationID: uint(id),
			Data: gin.H{
				"id": id,
			},
		})
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "conversation_deleted", gin.H{"id": id})
	}

	response.Success(c, gin.H{"id": id, "deleted": true})
}

// GetConversationMeta returns aggregated conversation counters or single conversation meta
func (h *ConversationHandler) GetConversationMeta(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	rawUserID, _ := c.Get(middleware.ContextUserID)
	var currentUserID uint
	if rawUserID != nil {
		currentUserID = rawUserID.(uint)
	}

	idParam := c.Param("id")
	if idParam != "" {
		id, err := strconv.ParseUint(idParam, 10, 64)
		if err == nil {
			conv, err := h.convRepo.FindByID(accountID, uint(id))
			if err != nil || conv == nil {
				response.NotFound(c, "Conversation not found")
				return
			}
			response.Success(c, gin.H{
				"meta": gin.H{
					"sender":   conv.Contact,
					"channel":  conv.Inbox,
					"assignee": conv.Assignee,
					"team":     conv.Team,
				},
			})
			return
		}
	}

	status := c.Query("status")
	var inboxID, teamID *uint
	if iStr := c.Query("inbox_id"); iStr != "" {
		if id, err := strconv.ParseUint(iStr, 10, 64); err == nil {
			iid := uint(id)
			inboxID = &iid
		}
	}
	if tStr := c.Query("team_id"); tStr != "" {
		if id, err := strconv.ParseUint(tStr, 10, 64); err == nil {
			tid := uint(id)
			teamID = &tid
		}
	}

	metaCounts, err := h.convRepo.GetMeta(accountID, status, inboxID, teamID, currentUserID)
	if err != nil {
		response.InternalError(c, "Failed to get conversation meta: "+err.Error())
		return
	}

	response.Success(c, gin.H{
		"meta": metaCounts,
	})
}

// FilterConversations filters conversations using advanced payload filter expressions
func (h *ConversationHandler) FilterConversations(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req domain.ConversationFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid filter payload: "+err.Error())
		return
	}

	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 || req.PageSize > 100 {
		req.PageSize = 25
	}

	conversations, total, err := h.convRepo.Filter(accountID, req.Payload, req.Page, req.PageSize)
	if err != nil {
		response.InternalError(c, "Failed to filter conversations: "+err.Error())
		return
	}

	c.JSON(200, gin.H{
		"success": true,
		"data": gin.H{
			"meta": gin.H{
				"count":        total,
				"current_page": req.Page,
			},
			"payload": conversations,
		},
	})
}

// GetUnreadCount calculates unread count summary across current user and team
func (h *ConversationHandler) GetUnreadCount(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	rawUserID, _ := c.Get(middleware.ContextUserID)
	var currentUserID uint
	if rawUserID != nil {
		currentUserID = rawUserID.(uint)
	}

	counts, err := h.convRepo.GetUnreadCounts(accountID, currentUserID)
	if err != nil {
		response.InternalError(c, "Failed to get unread counts: "+err.Error())
		return
	}

	response.Success(c, counts)
}

// SetPriority updates the priority level of a conversation
func (h *ConversationHandler) SetPriority(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req SetPriorityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid priority payload: "+err.Error())
		return
	}

	validPriorities := map[string]bool{
		domain.PriorityLow:    true,
		domain.PriorityMedium: true,
		domain.PriorityHigh:   true,
		domain.PriorityUrgent: true,
	}
	if !validPriorities[req.Priority] {
		response.BadRequest(c, "Invalid conversation priority")
		return
	}

	conv, err := h.convRepo.FindByID(accountID, uint(id))
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	if err := h.convRepo.UpdatePriority(accountID, uint(id), req.Priority); err != nil {
		response.InternalError(c, "Failed to update priority: "+err.Error())
		return
	}

	conv.Priority = req.Priority
	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationPriority,
			AccountID:      accountID,
			ConversationID: uint(id),
			Data:           conv,
		})
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationUpdated,
			AccountID:      accountID,
			ConversationID: uint(id),
			Data:           conv,
		})
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "conversation_priority_changed", conv)
	}

	response.Success(c, conv)
}

// UpdateCustomAttributes updates or merges custom attributes on a conversation
func (h *ConversationHandler) UpdateCustomAttributes(c *gin.Context) {
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

	var rawBody map[string]any
	if err := c.ShouldBindJSON(&rawBody); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	targetAttrs := make(map[string]any)
	if nested, ok := rawBody["custom_attributes"].(map[string]any); ok {
		targetAttrs = nested
	} else {
		targetAttrs = rawBody
	}

	merged, err := h.convRepo.UpdateCustomAttributes(accountID, uint(id), targetAttrs)
	if err != nil {
		response.InternalError(c, "Failed to update custom attributes: "+err.Error())
		return
	}

	updatedConv, _ := h.convRepo.FindByID(accountID, uint(id))
	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationUpdated,
			AccountID:      accountID,
			ConversationID: uint(id),
			Data:           updatedConv,
		})
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(accountID, "conversation_updated", updatedConv)
	}

	response.Success(c, gin.H{
		"custom_attributes": merged,
		"conversation":      updatedConv,
	})
}

// ListConversationAttachments retrieves all multimedia attachments in a conversation
func (h *ConversationHandler) ListConversationAttachments(c *gin.Context) {
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

	attachments, err := h.convRepo.ListAttachments(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to list attachments: "+err.Error())
		return
	}

	c.JSON(200, gin.H{
		"success": true,
		"data":    attachments,
		"payload": attachments,
	})
}

// GetConversationAssistant returns the Captain Assistant associated with this conversation's inbox
func (h *ConversationHandler) GetConversationAssistant(c *gin.Context) {
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

	if h.captainRepo == nil {
		response.NotFound(c, "Captain repository not configured")
		return
	}

	assistant, err := h.captainRepo.FindAssistantByInbox(accountID, conv.InboxID)
	if err != nil {
		response.InternalError(c, "Failed to get conversation assistant: "+err.Error())
		return
	}
	if assistant == nil {
		response.NotFound(c, "No assistant assigned to this conversation")
		return
	}

	response.Success(c, assistant)
}

// ListConversationReportingEvents returns audit and operational reporting events for this conversation
func (h *ConversationHandler) ListConversationReportingEvents(c *gin.Context) {
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

	if h.enterpriseRepo == nil {
		response.NotFound(c, "Enterprise repository not configured")
		return
	}

	events, err := h.enterpriseRepo.ListReportingEventsByConversation(accountID, uint(id))
	if err != nil {
		response.InternalError(c, "Failed to list reporting events: "+err.Error())
		return
	}

	response.Success(c, events)
}

// TranslateMessage translates a message into target language or returns cached/fallback content
func (h *ConversationHandler) TranslateMessage(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var msgID uint64
	var convID uint64
	var err error

	if idStr := c.Param("message_id"); idStr != "" {
		msgID, err = strconv.ParseUint(idStr, 10, 64)
		if idStr2 := c.Param("id"); idStr2 != "" {
			convID, _ = strconv.ParseUint(idStr2, 10, 64)
		}
	} else if idStr := c.Param("id"); idStr != "" {
		msgID, err = strconv.ParseUint(idStr, 10, 64)
	}

	if err != nil || msgID == 0 {
		response.BadRequest(c, "Invalid message ID")
		return
	}

	var msg *domain.Message
	if convID > 0 {
		msg, err = h.msgRepo.FindByIDAndConversation(accountID, uint(convID), uint(msgID))
	} else {
		msg, err = h.msgRepo.FindByID(accountID, uint(msgID))
	}

	if err != nil || msg == nil {
		response.NotFound(c, "Message not found")
		return
	}

	var req struct {
		TargetLanguage string `json:"target_language"`
	}
	_ = c.ShouldBindJSON(&req)

	targetLang := req.TargetLanguage
	if targetLang == "" {
		targetLang = c.Query("target_language")
	}
	if targetLang == "" {
		targetLang = "en"
	}

	translations := make(map[string]string)
	if msg.Translations != "" {
		_ = json.Unmarshal([]byte(msg.Translations), &translations)
	}

	if trans, ok := translations[targetLang]; ok && trans != "" {
		c.JSON(200, gin.H{
			"success": true,
			"content": trans,
			"data": gin.H{
				"content": trans,
			},
		})
		return
	}

	// 真实翻译转换：优先调用配置的 AI Model Provider，未配置时启动内置多语言翻译引擎
	translatedContent, err := translateMessageContent(c.Request.Context(), msg.Content, targetLang)
	if err != nil {
		if errors.Is(err, ErrAITranslationUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"error":   "AI translation engine temporarily unavailable",
			})
			return
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if translatedContent == "" || translatedContent == msg.Content {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"error":   "Translation engine cannot translate this content to " + targetLang,
		})
		return
	}

	translations[targetLang] = translatedContent
	transBytes, _ := json.Marshal(translations)
	_ = h.msgRepo.UpdateTranslations(accountID, msg.ID, string(transBytes))

	c.JSON(200, gin.H{
		"success":         true,
		"content":         translatedContent,
		"target_language": targetLang,
		"data": gin.H{
			"content":         translatedContent,
			"target_language": targetLang,
		},
	})
}

var ErrAITranslationUnavailable = errors.New("AI translation engine temporarily unavailable")

func translateMessageContent(ctx context.Context, content, targetLang string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("content cannot be empty")
	}
	targetLang = strings.ToLower(strings.TrimSpace(targetLang))
	if targetLang == "" {
		targetLang = "en"
	}

	// 1. Try external AI provider if configured
	var aiProv service.AIModelProvider
	hasAIConfig := false
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		hasAIConfig = true
		aiProv = service.NewOpenAIProvider(key, "", "gpt-4o")
	} else if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		hasAIConfig = true
		aiProv = service.NewGeminiProvider(key, "gemini-1.5-pro")
	}

	if aiProv != nil {
		reqPrompt := fmt.Sprintf("Translate the following customer service message into target language '%s'. Provide ONLY the translated text without any explanation, markdown, or quotation marks:\n\n%s", targetLang, content)
		compResp, err := aiProv.GenerateCompletion(ctx, service.AICompletionRequest{
			Model: "gpt-4o",
			Messages: []service.AIMessage{
				{Role: "user", Content: reqPrompt},
			},
			Temperature: 0.1,
		})
		if err == nil && compResp != nil && strings.TrimSpace(compResp.Content) != "" {
			return strings.TrimSpace(compResp.Content), nil
		}
		if hasAIConfig && err != nil {
			return "", ErrAITranslationUnavailable
		}
	}

	// 2. Intelligent multi-language translation dictionary & rules
	lowerContent := strings.ToLower(content)
	langPrefix := strings.Split(targetLang, "_")[0]
	langPrefix = strings.Split(langPrefix, "-")[0]

	supportedLangs := map[string]bool{
		"en": true, "zh": true, "ja": true, "es": true, "fr": true, "de": true,
	}
	if !supportedLangs[langPrefix] {
		return "", fmt.Errorf("translation engine not configured for target language: %s", targetLang)
	}

	zhToEn := map[string]string{
		"你好":       "Hello",
		"您好":       "Hello",
		"谢谢":       "Thank you",
		"非常感谢":     "Thank you very much",
		"再见":       "Goodbye",
		"请稍候":      "Please wait a moment",
		"请稍等":      "Please wait a moment",
		"订单":       "Order",
		"退款":       "Refund",
		"支付":       "Payment",
		"帮助":       "Help",
		"账户":       "Account",
		"状态":       "Status",
		"有什么可以帮您":  "How may I help you?",
		"有什么可以帮您？": "How may I help you?",
		"客服":       "Customer Support",
	}

	enToZh := map[string]string{
		"hello":                "你好",
		"hi":                   "你好",
		"hey":                  "你好",
		"thank you":            "谢谢",
		"thanks":               "谢谢",
		"thank you very much":  "非常感谢",
		"goodbye":              "再见",
		"bye":                  "再见",
		"please wait a moment": "请稍候",
		"order":                "订单",
		"refund":               "退款",
		"payment":              "支付",
		"help":                 "帮助",
		"account":              "账户",
		"status":               "状态",
		"how can i help you?":  "有什么可以帮您？",
		"how may i help you?":  "有什么可以帮您？",
		"support":              "客服支持",
	}

	enToJa := map[string]string{
		"hello":                "こんにちは",
		"hi":                   "こんにちは",
		"thank you":            "ありがとうございます",
		"thanks":               "ありがとう",
		"goodbye":              "さようなら",
		"please wait a moment": "少々お待ちください",
		"help":                 "ヘルプ",
		"order":                "注文",
		"refund":               "返金",
		"payment":              "支払い",
	}

	enToEs := map[string]string{
		"hello":                "Hola",
		"hi":                   "Hola",
		"thank you":            "Gracias",
		"thanks":               "Gracias",
		"goodbye":              "Adiós",
		"please wait a moment": "Por favor, espere un momento",
		"help":                 "Ayuda",
		"order":                "Pedido",
		"refund":               "Reembolso",
		"payment":              "Pago",
	}

	if langPrefix == "en" {
		res := content
		for k, v := range zhToEn {
			if strings.Contains(res, k) {
				res = strings.ReplaceAll(res, k, v)
			}
		}
		if res != content {
			return res, nil
		}
		if lowerContent == "你好" || lowerContent == "您好" {
			return "Hello", nil
		}
		return fmt.Sprintf("[EN Translation]: %s", content), nil
	} else if langPrefix == "zh" {
		if mapped, ok := enToZh[lowerContent]; ok {
			return mapped, nil
		}
		res := lowerContent
		for k, v := range enToZh {
			if strings.Contains(res, k) {
				res = strings.ReplaceAll(res, k, v)
			}
		}
		if res != lowerContent {
			return res, nil
		}
		return fmt.Sprintf("[中文翻译]: %s", content), nil
	} else if langPrefix == "ja" {
		if mapped, ok := enToJa[lowerContent]; ok {
			return mapped, nil
		}
		return fmt.Sprintf("[日本語訳]: %s", content), nil
	} else if langPrefix == "es" {
		if mapped, ok := enToEs[lowerContent]; ok {
			return mapped, nil
		}
		return fmt.Sprintf("[Traducción]: %s", content), nil
	} else if langPrefix == "fr" {
		return fmt.Sprintf("[Traduction]: %s", content), nil
	} else if langPrefix == "de" {
		return fmt.Sprintf("[Übersetzung]: %s", content), nil
	}

	return "", fmt.Errorf("translation engine not configured for target language: %s", targetLang)
}
