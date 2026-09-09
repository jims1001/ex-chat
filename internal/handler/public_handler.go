package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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

// PublicHandler manages customer-facing Public API endpoints
type PublicHandler struct {
	db                *gorm.DB
	inboxRepo         *repository.InboxRepository
	contactRepo       *repository.ContactRepository
	convRepo          *repository.ConversationRepository
	msgRepo           *repository.MessageRepository
	publicRepo        *repository.PublicRepository
	automationService *service.AutomationService
	webhookService    *service.WebhookService
	pushService       *service.PushService
	hub               *ws.Hub
}

// NewPublicHandler creates a new PublicHandler instance
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
		publicRepo:  repository.NewPublicRepository(db),
	}
}

// SetPublicRepository sets the public repository
func (h *PublicHandler) SetPublicRepository(pr *repository.PublicRepository) {
	h.publicRepo = pr
}

// SetAutomationAndWebhook injects automation and webhook services
func (h *PublicHandler) SetAutomationAndWebhook(as *service.AutomationService, ws *service.WebhookService) {
	h.automationService = as
	h.webhookService = ws
}

// SetPushAndHub injects push service and websocket hub
func (h *PublicHandler) SetPushAndHub(ps *service.PushService, hub *ws.Hub) {
	h.pushService = ps
	h.hub = hub
}

// ----------------- Helper Methods -----------------

// resolveInboxAndContact verifies inbox and contact identity
func (h *PublicHandler) resolveInboxAndContact(c *gin.Context) (*domain.Inbox, *domain.Contact, error) {
	identifier := c.Param("identifier")
	inbox, err := h.publicRepo.FindInboxByIdentifier(c.Request.Context(), identifier)
	if err != nil || inbox == nil {
		response.NotFound(c, "inbox not found")
		return nil, nil, gorm.ErrRecordNotFound
	}

	contactParam := c.Param("contact_id")
	if contactParam == "" {
		contactParam = c.Param("source_id")
	}

	contact, _, err := h.publicRepo.ResolveContact(c.Request.Context(), inbox.ID, inbox.AccountID, contactParam)
	if err != nil || contact == nil {
		response.NotFound(c, "contact not found in this inbox or account")
		return nil, nil, gorm.ErrRecordNotFound
	}

	return inbox, contact, nil
}

// ----------------- Inbox & Contact Endpoints -----------------

// GetPublicInbox handles OPEN-01: retrieves public inbox details without admin credentials
func (h *PublicHandler) GetPublicInbox(c *gin.Context) {
	identifier := c.Param("identifier")
	inbox, err := h.publicRepo.FindInboxByIdentifier(c.Request.Context(), identifier)
	if err != nil || inbox == nil {
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

// PublicCreateContactReq defines payload for creating a contact
type PublicCreateContactReq struct {
	Name             string         `json:"name"`
	Email            string         `json:"email"`
	PhoneNumber      string         `json:"phone_number"`
	Identifier       string         `json:"identifier"`
	CustomAttributes map[string]any `json:"custom_attributes"`
}

// CreateContact handles OPEN-02: creates or finds contact
func (h *PublicHandler) CreateContact(c *gin.Context) {
	identifier := c.Param("identifier")
	inbox, err := h.publicRepo.FindInboxByIdentifier(c.Request.Context(), identifier)
	if err != nil || inbox == nil {
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
		Email:       strings.ToLower(strings.TrimSpace(req.Email)),
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

	if len(req.CustomAttributes) > 0 {
		_, _ = h.publicRepo.UpdateContact(c.Request.Context(), inbox.AccountID, contact.ID, nil, req.CustomAttributes)
		_ = h.db.WithContext(c.Request.Context()).Where("id = ?", contact.ID).First(&contact).Error
	}

	response.Created(c, contact)
}

// GetContact retrieves contact details for a public visitor
func (h *PublicHandler) GetContact(c *gin.Context) {
	_, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	logger.WithComponent("public_api").Info("public contact retrieved",
		"contact_id", contact.ID, "account_id", contact.AccountID)

	response.Success(c, contact)
}

// PublicUpdateContactReq defines partial update fields for contact
type PublicUpdateContactReq struct {
	Name             string         `json:"name"`
	Email            string         `json:"email"`
	PhoneNumber      string         `json:"phone_number"`
	AvatarURL        string         `json:"avatar_url"`
	Identifier       string         `json:"identifier"`
	CustomAttributes map[string]any `json:"custom_attributes"`
}

// UpdateContact partially updates customer profile and merges custom attributes
func (h *PublicHandler) UpdateContact(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	var req PublicUpdateContactReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	updates := make(map[string]any)
	if strings.TrimSpace(req.Name) != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Email) != "" {
		updates["email"] = strings.ToLower(strings.TrimSpace(req.Email))
	}
	if strings.TrimSpace(req.PhoneNumber) != "" {
		updates["phone_number"] = strings.TrimSpace(req.PhoneNumber)
	}
	if strings.TrimSpace(req.AvatarURL) != "" {
		updates["avatar_url"] = strings.TrimSpace(req.AvatarURL)
	}
	if strings.TrimSpace(req.Identifier) != "" {
		updates["identifier"] = strings.TrimSpace(req.Identifier)
	}

	updatedContact, err := h.publicRepo.UpdateContact(c.Request.Context(), inbox.AccountID, contact.ID, updates, req.CustomAttributes)
	if err != nil {
		logger.WithComponent("public_api").Error("failed to update public contact",
			"contact_id", contact.ID, "error", err.Error())
		response.InternalError(c, "Failed to update contact profile")
		return
	}

	logger.WithComponent("public_api").Info("public contact updated",
		"contact_id", updatedContact.ID, "account_id", inbox.AccountID)

	response.Success(c, updatedContact)
}

// ----------------- Conversation Endpoints -----------------

// CreateConversation handles OPEN-04: creates a conversation for the public contact
func (h *PublicHandler) CreateConversation(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	conv := domain.Conversation{
		AccountID: inbox.AccountID,
		InboxID:   inbox.ID,
		ContactID: contact.ID,
		Status:    domain.ConversationStatusOpen,
		Priority:  domain.PriorityMedium,
	}

	if err := h.convRepo.Create(&conv); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("public_api").Info("public conversation created",
		"conversation_id", conv.ID, "contact_id", contact.ID, "inbox_id", inbox.ID)

	response.Created(c, conv)
}

// ListConversations lists all conversations for the public contact in this inbox
func (h *PublicHandler) ListConversations(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))

	convs, total, err := h.publicRepo.ListContactConversations(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, status, page, pageSize)
	if err != nil {
		logger.WithComponent("public_api").Error("failed to list public conversations",
			"contact_id", contact.ID, "inbox_id", inbox.ID, "error", err.Error())
		response.InternalError(c, "Failed to retrieve conversations")
		return
	}

	logger.WithComponent("public_api").Info("public conversations listed",
		"contact_id", contact.ID, "inbox_id", inbox.ID, "count", len(convs), "total", total)

	response.Success(c, gin.H{
		"conversations": convs,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
	})
}

// GetConversation retrieves a single conversation detail strictly verifying ownership
func (h *PublicHandler) GetConversation(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	convID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.publicRepo.GetContactConversation(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convID))
	if err != nil {
		logger.WithComponent("public_api").Error("failed to load public conversation",
			"conversation_id", convID, "error", err.Error())
		response.InternalError(c, "Error loading conversation")
		return
	}
	if conv == nil {
		response.NotFound(c, "conversation not found or access denied")
		return
	}

	response.Success(c, conv)
}

// ----------------- Message Endpoints -----------------

// PublicCreateMessageReq defines payload for posting a message
type PublicCreateMessageReq struct {
	Content string `json:"content" binding:"required"`
	EchoID  string `json:"echo_id"`
}

// CreateMessage handles OPEN-05 & CONS-05: posts message with echo_id idempotency and multi-layer ownership validation
func (h *PublicHandler) CreateMessage(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	convID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req PublicCreateMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Verify conversation strictly belongs to this contact, inbox, and account
	conv, err := h.publicRepo.GetContactConversation(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convID))
	if err != nil || conv == nil {
		response.NotFound(c, "conversation not found or access denied for this contact/inbox")
		return
	}

	// Idempotency check with echo_id
	if req.EchoID != "" {
		var existing domain.Message
		if err := h.db.WithContext(c.Request.Context()).Where("conversation_id = ? AND echo_id = ?", conv.ID, req.EchoID).First(&existing).Error; err == nil {
			response.Success(c, existing)
			return
		}
	}

	msg := domain.Message{
		AccountID:      conv.AccountID,
		ConversationID: conv.ID,
		SenderType:     domain.SenderTypeContact,
		SenderID:       contact.ID,
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
			"contact_id", contact.ID,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("message").Info("public message created",
		"message_id", msg.ID,
		"conversation_id", conv.ID,
		"account_id", conv.AccountID,
		"contact_id", contact.ID,
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
		h.automationService.HandleMessageCreated(conv, &msg)
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

// ListMessages reads message history for a public conversation, strictly filtering out internal private notes
func (h *PublicHandler) ListMessages(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	convID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.publicRepo.GetContactConversation(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convID))
	if err != nil || conv == nil {
		response.NotFound(c, "conversation not found or access denied")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	beforeID, _ := strconv.ParseUint(c.Query("before"), 10, 64)
	afterID, _ := strconv.ParseUint(c.Query("after"), 10, 64)

	msgs, total, err := h.publicRepo.ListConversationMessages(c.Request.Context(), inbox.AccountID, conv.ID, uint(beforeID), uint(afterID), page, pageSize)
	if err != nil {
		logger.WithComponent("public_api").Error("failed to list public messages",
			"conversation_id", conv.ID, "error", err.Error())
		response.InternalError(c, "Failed to load messages")
		return
	}

	logger.WithComponent("public_api").Info("public messages listed",
		"conversation_id", conv.ID, "count", len(msgs), "total", total)

	response.Success(c, gin.H{
		"messages":  msgs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// UpdateLastSeen updates contact read position for a public conversation
func (h *PublicHandler) UpdateLastSeen(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	convID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.publicRepo.GetContactConversation(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convID))
	if err != nil || conv == nil {
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

// ----------------- Status & Collaboration Endpoints -----------------

// PublicToggleStatusReq defines optional target status
type PublicToggleStatusReq struct {
	Status string `json:"status"`
}

// ToggleConversationStatus toggles conversation between open and resolved or sets explicit status
func (h *PublicHandler) ToggleConversationStatus(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	convID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.publicRepo.GetContactConversation(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convID))
	if err != nil || conv == nil {
		response.NotFound(c, "conversation not found or access denied")
		return
	}

	var req PublicToggleStatusReq
	_ = c.ShouldBindJSON(&req)

	targetStatus := strings.TrimSpace(req.Status)
	if targetStatus == "" {
		if conv.Status == domain.ConversationStatusResolved {
			targetStatus = domain.ConversationStatusOpen
		} else {
			targetStatus = domain.ConversationStatusResolved
		}
	}

	// Validate allowed statuses
	allowed := map[string]bool{
		domain.ConversationStatusOpen:     true,
		domain.ConversationStatusResolved: true,
		domain.ConversationStatusPending:  true,
		domain.ConversationStatusSnoozed:  true,
	}
	if !allowed[targetStatus] {
		response.BadRequest(c, "Invalid conversation status: "+targetStatus)
		return
	}

	updatedConv, err := h.publicRepo.UpdateConversationStatus(c.Request.Context(), inbox.AccountID, conv.ID, targetStatus)
	if err != nil {
		logger.WithComponent("public_api").Error("failed to toggle conversation status",
			"conversation_id", conv.ID, "error", err.Error())
		response.InternalError(c, "Failed to update conversation status")
		return
	}

	if h.webhookService != nil {
		h.webhookService.Dispatch(inbox.AccountID, "conversation_status_changed", updatedConv)
	}
	if h.hub != nil {
		h.hub.Broadcast(&ws.Event{
			Name:           ws.EventConversationUpdated,
			AccountID:      inbox.AccountID,
			ConversationID: updatedConv.ID,
			Data:           updatedConv,
		})
	}

	logger.WithComponent("public_api").Info("conversation status updated via public api",
		"conversation_id", updatedConv.ID, "status", updatedConv.Status)

	response.Success(c, updatedConv)
}

// PublicToggleTypingReq defines typing status payload
type PublicToggleTypingReq struct {
	TypingStatus string `json:"typing_status"`
}

// ToggleTyping broadcasts visitor typing state to agent dashboard
func (h *PublicHandler) ToggleTyping(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	convID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.publicRepo.GetContactConversation(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convID))
	if err != nil || conv == nil {
		response.NotFound(c, "conversation not found or access denied")
		return
	}

	var req PublicToggleTypingReq
	_ = c.ShouldBindJSON(&req)
	typingStatus := strings.ToLower(strings.TrimSpace(req.TypingStatus))
	if typingStatus == "" {
		typingStatus = "on"
	}

	eventName := "conversation.typing_on"
	if typingStatus == "off" {
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
					"id":   contact.ID,
					"name": contact.Name,
				},
				"typing_status": typingStatus,
			},
		})
	}

	response.Success(c, gin.H{
		"status":        "ok",
		"typing_status": typingStatus,
	})
}

// UpdateConversationCustomAttributes shallow-merges custom attributes onto conversation
func (h *PublicHandler) UpdateConversationCustomAttributes(c *gin.Context) {
	inbox, contact, err := h.resolveInboxAndContact(c)
	if err != nil {
		return
	}

	convID, err := strconv.ParseUint(c.Param("conversation_id"), 10, 64)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.publicRepo.GetContactConversation(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convID))
	if err != nil || conv == nil {
		response.NotFound(c, "conversation not found or access denied")
		return
	}

	var req struct {
		CustomAttributes map[string]any `json:"custom_attributes" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid payload: "+err.Error())
		return
	}

	merged, err := h.publicRepo.MergeConversationCustomAttributes(c.Request.Context(), inbox.AccountID, conv.ID, req.CustomAttributes)
	if err != nil {
		logger.WithComponent("public_api").Error("failed to merge conversation custom attributes",
			"conversation_id", conv.ID, "error", err.Error())
		response.InternalError(c, "Failed to update custom attributes")
		return
	}

	logger.WithComponent("public_api").Info("conversation custom attributes updated via public api",
		"conversation_id", conv.ID)

	response.Success(c, gin.H{
		"custom_attributes": merged,
	})
}
