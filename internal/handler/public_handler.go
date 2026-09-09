package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PublicHandler struct {
	db                *gorm.DB
	inboxRepo         *repository.InboxRepository
	contactRepo       *repository.ContactRepository
	convRepo          *repository.ConversationRepository
	msgRepo           *repository.MessageRepository
	automationService *service.AutomationService
	webhookService    *service.WebhookService
	pushService       *service.PushService
	hub               *ws.Hub
}

func NewPublicHandler(
	db *gorm.DB,
	i *repository.InboxRepository,
	c *repository.ContactRepository,
	cv *repository.ConversationRepository,
	m *repository.MessageRepository,
) *PublicHandler {
	return &PublicHandler{
		db:          db,
		inboxRepo:   i,
		contactRepo: c,
		convRepo:    cv,
		msgRepo:     m,
	}
}

func (h *PublicHandler) SetAutomationAndWebhook(as *service.AutomationService, ws *service.WebhookService) {
	h.automationService = as
	h.webhookService = ws
}

func (h *PublicHandler) SetPushAndHub(ps *service.PushService, hub *ws.Hub) {
	h.pushService = ps
	h.hub = hub
}

// GetPublicInbox handles OPEN-01: retrieves public inbox details without admin credentials
func (h *PublicHandler) GetPublicInbox(c *gin.Context) {
	identifier := c.Param("identifier")
	var inbox domain.Inbox
	err := h.db.WithContext(c.Request.Context()).
		Where("website_token = ? OR id = ?", identifier, identifier).
		First(&inbox).Error

	if err != nil {
		response.NotFound(c, "inbox not found")
		return
	}

	response.Success(c, gin.H{
		"id":           inbox.ID,
		"name":         inbox.Name,
		"channel_type": inbox.ChannelType,
		"greeting":     inbox.GreetingMessage,
	})
}

type PublicCreateContactReq struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
	Identifier  string `json:"identifier"`
}

// CreateContact handles OPEN-02: creates or finds contact
func (h *PublicHandler) CreateContact(c *gin.Context) {
	identifier := c.Param("identifier")
	var inbox domain.Inbox
	if err := h.db.WithContext(c.Request.Context()).Where("website_token = ? OR id = ?", identifier, identifier).First(&inbox).Error; err != nil {
		response.NotFound(c, "inbox not found")
		return
	}

	var req PublicCreateContactReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	contact := domain.Contact{
		AccountID:   inbox.AccountID,
		Name:        req.Name,
		Email:       req.Email,
		PhoneNumber: req.PhoneNumber,
		Identifier:  req.Identifier,
	}

	if err := h.contactRepo.Create(&contact); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	sourceID := req.Identifier
	if sourceID == "" {
		sourceID = fmt.Sprintf("pub_%d_%d", contact.ID, time.Now().UnixNano())
	}

	_, _ = h.contactRepo.FindOrCreateContactInbox(contact.ID, inbox.ID, sourceID)

	response.Created(c, contact)
}

// CreateConversation handles OPEN-04: creates a conversation for the public contact
func (h *PublicHandler) CreateConversation(c *gin.Context) {
	identifier := c.Param("identifier")
	contactID, _ := strconv.ParseUint(c.Param("contact_id"), 10, 32)

	var inbox domain.Inbox
	if err := h.db.WithContext(c.Request.Context()).Where("website_token = ? OR id = ?", identifier, identifier).First(&inbox).Error; err != nil {
		response.NotFound(c, "inbox not found")
		return
	}

	// Verify contact belongs to this inbox's account
	var contact domain.Contact
	if err := h.db.WithContext(c.Request.Context()).Where("id = ? AND account_id = ?", contactID, inbox.AccountID).First(&contact).Error; err != nil {
		response.NotFound(c, "contact not found in this account")
		return
	}

	conv := domain.Conversation{
		AccountID: inbox.AccountID,
		InboxID:   inbox.ID,
		ContactID: uint(contactID),
		Status:    domain.ConversationStatusOpen,
		Priority:  domain.PriorityMedium,
	}

	if err := h.convRepo.Create(&conv); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, conv)
}

type PublicCreateMessageReq struct {
	Content string `json:"content" binding:"required"`
	EchoID  string `json:"echo_id"`
}

// CreateMessage handles OPEN-05 & CONS-05: posts message with echo_id idempotency and multi-layer ownership validation
func (h *PublicHandler) CreateMessage(c *gin.Context) {
	identifier := c.Param("identifier")
	convID, _ := strconv.ParseUint(c.Param("conversation_id"), 10, 32)
	contactID, _ := strconv.ParseUint(c.Param("contact_id"), 10, 32)

	var req PublicCreateMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// 1. Verify Inbox
	var inbox domain.Inbox
	if err := h.db.WithContext(c.Request.Context()).Where("website_token = ? OR id = ?", identifier, identifier).First(&inbox).Error; err != nil {
		response.NotFound(c, "inbox not found")
		return
	}

	// 2. Verify Contact belongs to Inbox's Account
	var contact domain.Contact
	if err := h.db.WithContext(c.Request.Context()).Where("id = ? AND account_id = ?", contactID, inbox.AccountID).First(&contact).Error; err != nil {
		response.NotFound(c, "contact does not belong to inbox account")
		return
	}

	// 3. Verify Conversation strictly belongs to this Account, this Inbox, and this Contact
	var conv domain.Conversation
	if err := h.db.WithContext(c.Request.Context()).
		Where("id = ? AND account_id = ? AND inbox_id = ? AND contact_id = ?", convID, inbox.AccountID, inbox.ID, contactID).
		First(&conv).Error; err != nil {
		response.NotFound(c, "conversation not found or access denied for this contact/inbox")
		return
	}

	// Idempotency check with echo_id
	if req.EchoID != "" {
		var existing domain.Message
		if err := h.db.WithContext(c.Request.Context()).Where("conversation_id = ? AND echo_id = ?", convID, req.EchoID).First(&existing).Error; err == nil {
			// Return already created message
			response.Success(c, existing)
			return
		}
	}

	msg := domain.Message{
		AccountID:      conv.AccountID,
		ConversationID: conv.ID,
		SenderType:     domain.SenderTypeContact,
		SenderID:       uint(contactID),
		MessageType:    domain.MessageTypeIncoming,
		ContentType:    domain.ContentTypeText,
		Content:        req.Content,
		EchoID:         req.EchoID,
		Status:         domain.MessageStatusSent,
	}

	if err := h.msgRepo.Create(&msg); err != nil {
		logger.WithComponent("message").Error("failed to create public message",
			"conversation_id", conv.ID,
			"account_id", conv.AccountID,
			"contact_id", contactID,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("message").Info("public message created",
		"message_id", msg.ID,
		"conversation_id", conv.ID,
		"account_id", conv.AccountID,
		"contact_id", contactID,
		"echo_id", msg.EchoID,
	)

	// Re-open conversation if it was resolved
	if conv.Status == domain.ConversationStatusResolved {
		_ = h.convRepo.UpdateStatus(conv.AccountID, conv.ID, domain.ConversationStatusOpen, nil)
		conv.Status = domain.ConversationStatusOpen
	} else {
		_ = h.convRepo.TouchActivity(conv.AccountID, conv.ID)
	}

	// Trigger automation rules, webhooks, push notifications and WebSocket broadcasts
	if h.automationService != nil {
		h.automationService.HandleMessageCreated(&conv, &msg)
	}
	if h.webhookService != nil {
		h.webhookService.Dispatch(conv.AccountID, "message_created", msg)
	}
	if h.pushService != nil && conv.AssigneeID != nil {
		go h.pushService.Dispatch(context.Background(), *conv.AssigneeID, conv.AccountID, service.PushPayload{
			Title:        "New Customer Message",
			Body:         msg.Content,
			AccountID:    conv.AccountID,
			ResourceID:   conv.ID,
			ResourceType: "conversation",
		})
	}
	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      conv.AccountID,
			ConversationID: conv.ID,
			Data:           msg,
		})
	}

	response.Created(c, msg)
}

// UpdateLastSeen updates contact read position for a public conversation
func (h *PublicHandler) UpdateLastSeen(c *gin.Context) {
	identifier := c.Param("identifier")
	var inbox domain.Inbox
	if err := h.db.WithContext(c.Request.Context()).Where("website_token = ? OR id = ?", identifier, identifier).First(&inbox).Error; err != nil {
		response.NotFound(c, "Inbox not found")
		return
	}

	contactID, err := strconv.ParseUint(c.Param("contact_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	conversationID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var conv domain.Conversation
	if err := h.db.Where("id = ? AND account_id = ? AND inbox_id = ? AND contact_id = ?", conversationID, inbox.AccountID, inbox.ID, contactID).First(&conv).Error; err != nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	var req struct {
		ContactLastSeenAt *time.Time `json:"contact_last_seen_at"`
	}
	_ = c.ShouldBindJSON(&req)

	seenAt := time.Now()
	if req.ContactLastSeenAt != nil && !req.ContactLastSeenAt.IsZero() {
		seenAt = *req.ContactLastSeenAt
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

