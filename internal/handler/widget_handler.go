package handler

import (
	"context"
	"encoding/json"
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

// WidgetHandler manages visitor-facing widget endpoints
type WidgetHandler struct {
	db          *gorm.DB
	widgetRepo  *repository.WidgetRepository
	convRepo    *repository.ConversationRepository
	contactRepo *repository.ContactRepository
}

// NewWidgetHandler creates a new WidgetHandler instance
func NewWidgetHandler(
	db *gorm.DB,
	widgetRepo *repository.WidgetRepository,
	convRepo *repository.ConversationRepository,
	contactRepo *repository.ContactRepository,
) *WidgetHandler {
	return &WidgetHandler{
		db:          db,
		widgetRepo:  widgetRepo,
		convRepo:    convRepo,
		contactRepo: contactRepo,
	}
}

// ----------------- Request Payloads -----------------

// WidgetEventRequest represents an event tracking payload
type WidgetEventRequest struct {
	Name           string         `json:"name" binding:"required"`
	SourceID       string         `json:"source_id"`
	ContactToken   string         `json:"contact_token"`
	ConversationID *uint          `json:"conversation_id"`
	URL            string         `json:"url"`
	Title          string         `json:"title"`
	Properties     map[string]any `json:"properties"`
}

// WidgetLabelsRequest represents a label attach/remove payload
type WidgetLabelsRequest struct {
	SourceID       string   `json:"source_id"`
	ContactToken   string   `json:"contact_token"`
	ConversationID *uint    `json:"conversation_id"`
	Labels         []string `json:"labels" binding:"required"`
}

// WidgetUpdateContactRequest represents partial contact update payload
type WidgetUpdateContactRequest struct {
	SourceID         string         `json:"source_id"`
	ContactToken     string         `json:"contact_token"`
	Name             string         `json:"name"`
	Email            string         `json:"email"`
	PhoneNumber      string         `json:"phone_number"`
	Identifier       string         `json:"identifier"`
	CustomAttributes map[string]any `json:"custom_attributes"`
}

// WidgetMergeCustomAttributesRequest represents custom attributes merge payload
type WidgetMergeCustomAttributesRequest struct {
	SourceID         string         `json:"source_id"`
	ContactToken     string         `json:"contact_token"`
	CustomAttributes map[string]any `json:"custom_attributes" binding:"required"`
}

// ----------------- Helper Methods -----------------

// extractWebsiteToken retrieves the website token from query or headers
func (h *WidgetHandler) extractWebsiteToken(c *gin.Context) string {
	token := c.Query("website_token")
	if token == "" {
		token = c.GetHeader("X-Auth-Token")
	}
	if token == "" {
		token = c.GetHeader("X-Website-Token")
	}
	return strings.TrimSpace(token)
}

// extractSourceID retrieves the visitor source_id or contact_token
func (h *WidgetHandler) extractSourceID(c *gin.Context) string {
	sourceID := c.Query("source_id")
	if sourceID == "" {
		sourceID = c.Query("contact_token")
	}
	if sourceID == "" {
		sourceID = c.GetHeader("X-Contact-Token")
	}
	return strings.TrimSpace(sourceID)
}

// resolveInbox retrieves the inbox from the website token
func (h *WidgetHandler) resolveInbox(c *gin.Context) (*domain.Inbox, error) {
	token := h.extractWebsiteToken(c)
	if token == "" {
		response.BadRequest(c, "website_token is required")
		return nil, gorm.ErrRecordNotFound
	}

	inbox, err := h.widgetRepo.GetInboxByWebsiteToken(c.Request.Context(), token)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return nil, gorm.ErrRecordNotFound
	}
	return inbox, nil
}

// resolveContact retrieves existing contact or creates a placeholder if requested
func (h *WidgetHandler) resolveContact(ctx context.Context, inbox *domain.Inbox, sourceID string, autoCreate bool) (*domain.Contact, error) {
	if strings.TrimSpace(sourceID) == "" {
		return nil, nil
	}

	contact, err := h.widgetRepo.FindContactBySourceID(ctx, inbox.ID, sourceID)
	if err != nil {
		return nil, err
	}
	if contact != nil {
		return contact, nil
	}

	if !autoCreate {
		return nil, nil
	}

	// Auto-create visitor contact
	name := "Visitor "
	if len(sourceID) > 8 {
		name += sourceID[:8]
	} else {
		name += sourceID
	}

	newContact := domain.Contact{
		AccountID: inbox.AccountID,
		Name:      name,
	}
	if err := h.contactRepo.Create(&newContact); err != nil {
		return nil, err
	}
	_, _ = h.contactRepo.FindOrCreateContactInbox(newContact.ID, inbox.ID, sourceID)
	return &newContact, nil
}

// ----------------- Conversation History & Details -----------------

// ListConversations lists all conversations for the current visitor in this inbox
func (h *WidgetHandler) ListConversations(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	sourceID := h.extractSourceID(c)
	if sourceID == "" {
		response.Success(c, gin.H{
			"conversations": []domain.Conversation{},
			"total":         0,
			"page":          1,
			"page_size":     25,
		})
		return
	}

	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, false)
	if err != nil {
		logger.WithComponent("widget").Error("failed to find contact for conversation history",
			"inbox_id", inbox.ID, "source_id", sourceID, "error", err.Error())
		response.InternalError(c, "Error finding visitor profile")
		return
	}

	if contact == nil {
		response.Success(c, gin.H{
			"conversations": []domain.Conversation{},
			"total":         0,
			"page":          1,
			"page_size":     25,
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))

	convs, total, err := h.widgetRepo.ListContactConversations(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, page, pageSize)
	if err != nil {
		logger.WithComponent("widget").Error("failed to list visitor conversations",
			"inbox_id", inbox.ID, "contact_id", contact.ID, "error", err.Error())
		response.InternalError(c, "Failed to load conversation history")
		return
	}

	logger.WithComponent("widget").Info("visitor conversation history queried",
		"inbox_id", inbox.ID, "contact_id", contact.ID, "count", len(convs), "total", total)

	response.Success(c, gin.H{
		"conversations": convs,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
	})
}

// GetConversation retrieves conversation details including messages for a visitor
func (h *WidgetHandler) GetConversation(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	convIDStr := c.Param("id")
	convIDUint, err := strconv.ParseUint(convIDStr, 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	sourceID := h.extractSourceID(c)
	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, false)
	if err != nil {
		response.InternalError(c, "Error finding visitor profile")
		return
	}
	if contact == nil {
		response.NotFound(c, "Visitor profile not found")
		return
	}

	conv, err := h.widgetRepo.GetConversationDetail(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convIDUint))
	if err != nil {
		logger.WithComponent("widget").Error("error loading visitor conversation detail",
			"inbox_id", inbox.ID, "conv_id", convIDUint, "error", err.Error())
		response.InternalError(c, "Error fetching conversation")
		return
	}
	if conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	logger.WithComponent("widget").Info("visitor conversation detail retrieved",
		"inbox_id", inbox.ID, "contact_id", contact.ID, "conversation_id", conv.ID)

	response.Success(c, conv)
}

// ----------------- Ongoing Campaigns / Activities -----------------

// ListCampaigns lists active ongoing campaigns targeted for this widget inbox
func (h *WidgetHandler) ListCampaigns(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	campaigns, err := h.widgetRepo.ListActiveCampaigns(c.Request.Context(), inbox.AccountID, inbox.ID)
	if err != nil {
		logger.WithComponent("widget").Error("failed to list active widget campaigns",
			"inbox_id", inbox.ID, "error", err.Error())
		response.InternalError(c, "Failed to load campaigns")
		return
	}

	logger.WithComponent("widget").Info("widget active campaigns retrieved",
		"inbox_id", inbox.ID, "campaign_count", len(campaigns))

	response.Success(c, gin.H{
		"campaigns": campaigns,
	})
}

// ----------------- Event Tracking -----------------

// RecordEvent records a visitor telemetry event (page_view, widget_opened, etc.)
func (h *WidgetHandler) RecordEvent(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	var req WidgetEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid event payload: "+err.Error())
		return
	}

	sourceID := req.SourceID
	if sourceID == "" {
		sourceID = req.ContactToken
	}
	if sourceID == "" {
		sourceID = h.extractSourceID(c)
	}

	var contactID *uint
	if sourceID != "" {
		contact, _ := h.resolveContact(c.Request.Context(), inbox, sourceID, false)
		if contact != nil {
			contactID = &contact.ID
		}
	}

	var propsJSON string
	if len(req.Properties) > 0 {
		if b, err := json.Marshal(req.Properties); err == nil {
			propsJSON = string(b)
		}
	}

	event := domain.WidgetEvent{
		AccountID:      inbox.AccountID,
		InboxID:        inbox.ID,
		ContactID:      contactID,
		ConversationID: req.ConversationID,
		Name:           strings.TrimSpace(req.Name),
		SourceID:       sourceID,
		URL:            req.URL,
		Title:          req.Title,
		Properties:     propsJSON,
		CreatedAt:      time.Now(),
	}

	if err := h.widgetRepo.RecordWidgetEvent(c.Request.Context(), &event); err != nil {
		logger.WithComponent("widget").Error("failed to record widget event",
			"inbox_id", inbox.ID, "event_name", req.Name, "error", err.Error())
		response.InternalError(c, "Failed to record event")
		return
	}

	logger.WithComponent("widget").Info("widget event recorded",
		"inbox_id", inbox.ID,
		"event_id", event.ID,
		"name", event.Name,
		"source_id", sourceID,
		"url", event.URL,
	)

	response.Success(c, gin.H{
		"event": event,
	})
}

// ListEvents queries recorded events for verification, audit or analytics
func (h *WidgetHandler) ListEvents(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	sourceID := h.extractSourceID(c)
	var contactID *uint
	if cidStr := c.Query("contact_id"); cidStr != "" {
		if cid, err := strconv.ParseUint(cidStr, 10, 64); err == nil {
			u := uint(cid)
			contactID = &u
		}
	}

	var convID *uint
	if cvStr := c.Query("conversation_id"); cvStr != "" {
		if cv, err := strconv.ParseUint(cvStr, 10, 64); err == nil {
			u := uint(cv)
			convID = &u
		}
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))

	events, total, err := h.widgetRepo.ListWidgetEvents(c.Request.Context(), inbox.AccountID, inbox.ID, sourceID, contactID, convID, page, pageSize)
	if err != nil {
		logger.WithComponent("widget").Error("failed to list widget events",
			"inbox_id", inbox.ID, "error", err.Error())
		response.InternalError(c, "Failed to list events")
		return
	}

	response.Success(c, gin.H{
		"events":    events,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// ----------------- Visitor Labels -----------------

// GetLabels retrieves visitor labels for contact or conversation
func (h *WidgetHandler) GetLabels(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	sourceID := h.extractSourceID(c)
	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, false)
	if err != nil {
		response.InternalError(c, "Error finding visitor profile")
		return
	}
	if contact == nil {
		response.Success(c, gin.H{
			"labels": []domain.Label{},
		})
		return
	}

	var conversationID *uint
	if convStr := c.Query("conversation_id"); convStr != "" {
		if convID, err := strconv.ParseUint(convStr, 10, 64); err == nil {
			u := uint(convID)
			conversationID = &u
		}
	}

	labels, err := h.widgetRepo.GetVisitorLabels(c.Request.Context(), inbox.AccountID, contact.ID, conversationID)
	if err != nil {
		logger.WithComponent("widget").Error("failed to get visitor labels",
			"inbox_id", inbox.ID, "contact_id", contact.ID, "error", err.Error())
		response.InternalError(c, "Failed to retrieve labels")
		return
	}

	response.Success(c, gin.H{
		"labels": labels,
	})
}

// AddLabels adds labels to a visitor contact and optionally to a conversation
func (h *WidgetHandler) AddLabels(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	var req WidgetLabelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid labels payload: "+err.Error())
		return
	}

	sourceID := req.SourceID
	if sourceID == "" {
		sourceID = req.ContactToken
	}
	if sourceID == "" {
		sourceID = h.extractSourceID(c)
	}

	if sourceID == "" {
		response.BadRequest(c, "source_id or contact_token is required")
		return
	}

	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, true)
	if err != nil {
		logger.WithComponent("widget").Error("failed to resolve contact for label addition",
			"inbox_id", inbox.ID, "source_id", sourceID, "error", err.Error())
		response.InternalError(c, "Failed to resolve visitor profile")
		return
	}

	labels, err := h.widgetRepo.AttachLabels(c.Request.Context(), inbox.AccountID, contact.ID, req.ConversationID, req.Labels)
	if err != nil {
		logger.WithComponent("widget").Error("failed to attach labels",
			"inbox_id", inbox.ID, "contact_id", contact.ID, "error", err.Error())
		response.InternalError(c, "Failed to attach labels")
		return
	}

	logger.WithComponent("widget").Info("visitor labels attached",
		"inbox_id", inbox.ID, "contact_id", contact.ID, "labels_count", len(labels))

	response.Success(c, gin.H{
		"labels":  labels,
		"message": "Labels attached successfully",
	})
}

// RemoveLabels removes labels from a visitor contact or conversation
func (h *WidgetHandler) RemoveLabels(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	var req WidgetLabelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid labels payload: "+err.Error())
		return
	}

	sourceID := req.SourceID
	if sourceID == "" {
		sourceID = req.ContactToken
	}
	if sourceID == "" {
		sourceID = h.extractSourceID(c)
	}

	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, false)
	if err != nil {
		response.InternalError(c, "Error finding visitor profile")
		return
	}
	if contact == nil {
		response.NotFound(c, "Visitor profile not found")
		return
	}

	if err := h.widgetRepo.RemoveLabels(c.Request.Context(), inbox.AccountID, contact.ID, req.ConversationID, req.Labels); err != nil {
		logger.WithComponent("widget").Error("failed to remove labels",
			"inbox_id", inbox.ID, "contact_id", contact.ID, "error", err.Error())
		response.InternalError(c, "Failed to remove labels")
		return
	}

	logger.WithComponent("widget").Info("visitor labels removed",
		"inbox_id", inbox.ID, "contact_id", contact.ID, "labels", req.Labels)

	response.Success(c, gin.H{
		"message": "Labels removed successfully",
	})
}

// ----------------- Partial Custom Attributes -----------------

// UpdateContact partially updates visitor contact info and shallow-merges custom attributes
func (h *WidgetHandler) UpdateContact(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	var req WidgetUpdateContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid update payload: "+err.Error())
		return
	}

	sourceID := req.SourceID
	if sourceID == "" {
		sourceID = req.ContactToken
	}
	if sourceID == "" {
		sourceID = h.extractSourceID(c)
	}

	if sourceID == "" {
		response.BadRequest(c, "source_id or contact_token is required")
		return
	}

	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, true)
	if err != nil {
		logger.WithComponent("widget").Error("failed to resolve contact for update",
			"inbox_id", inbox.ID, "source_id", sourceID, "error", err.Error())
		response.InternalError(c, "Failed to resolve contact profile")
		return
	}

	updatedContact, err := h.widgetRepo.UpdateContactInfo(c.Request.Context(), inbox.AccountID, contact.ID,
		req.Name, req.Email, req.PhoneNumber, req.Identifier, req.CustomAttributes)
	if err != nil {
		logger.WithComponent("widget").Error("failed to update visitor contact info",
			"inbox_id", inbox.ID, "contact_id", contact.ID, "error", err.Error())
		response.InternalError(c, "Failed to update contact info")
		return
	}

	logger.WithComponent("widget").Info("visitor contact updated partially",
		"inbox_id", inbox.ID, "contact_id", contact.ID)

	response.Success(c, updatedContact)
}

// MergeContactCustomAttributes shallow-merges custom attributes onto the visitor contact
func (h *WidgetHandler) MergeContactCustomAttributes(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	var req WidgetMergeCustomAttributesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid custom attributes payload: "+err.Error())
		return
	}

	sourceID := req.SourceID
	if sourceID == "" {
		sourceID = req.ContactToken
	}
	if sourceID == "" {
		sourceID = h.extractSourceID(c)
	}

	if sourceID == "" {
		response.BadRequest(c, "source_id or contact_token is required")
		return
	}

	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, true)
	if err != nil {
		response.InternalError(c, "Failed to resolve visitor profile")
		return
	}

	mergedAttrs, err := h.widgetRepo.MergeContactCustomAttributes(c.Request.Context(), inbox.AccountID, contact.ID, req.CustomAttributes)
	if err != nil {
		logger.WithComponent("widget").Error("failed to merge contact custom attributes",
			"inbox_id", inbox.ID, "contact_id", contact.ID, "error", err.Error())
		response.InternalError(c, "Failed to merge custom attributes")
		return
	}

	logger.WithComponent("widget").Info("visitor contact custom attributes merged",
		"inbox_id", inbox.ID, "contact_id", contact.ID)

	response.Success(c, gin.H{
		"custom_attributes": mergedAttrs,
	})
}

// MergeConversationCustomAttributes shallow-merges custom attributes onto a specific conversation
func (h *WidgetHandler) MergeConversationCustomAttributes(c *gin.Context) {
	inbox, err := h.resolveInbox(c)
	if err != nil {
		return
	}

	convIDStr := c.Param("id")
	convIDUint, err := strconv.ParseUint(convIDStr, 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req WidgetMergeCustomAttributesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid custom attributes payload: "+err.Error())
		return
	}

	sourceID := req.SourceID
	if sourceID == "" {
		sourceID = req.ContactToken
	}
	if sourceID == "" {
		sourceID = h.extractSourceID(c)
	}

	contact, err := h.resolveContact(c.Request.Context(), inbox, sourceID, false)
	if err != nil {
		response.InternalError(c, "Error finding visitor profile")
		return
	}
	if contact == nil {
		response.NotFound(c, "Visitor profile not found")
		return
	}

	// Verify conversation belongs to contact and inbox
	conv, err := h.widgetRepo.GetConversationDetail(c.Request.Context(), inbox.AccountID, inbox.ID, contact.ID, uint(convIDUint))
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found or unauthorized")
		return
	}

	mergedAttrs, err := h.widgetRepo.MergeConversationCustomAttributes(c.Request.Context(), inbox.AccountID, conv.ID, req.CustomAttributes)
	if err != nil {
		logger.WithComponent("widget").Error("failed to merge conversation custom attributes",
			"inbox_id", inbox.ID, "conversation_id", conv.ID, "error", err.Error())
		response.InternalError(c, "Failed to merge conversation custom attributes")
		return
	}

	logger.WithComponent("widget").Info("visitor conversation custom attributes merged",
		"inbox_id", inbox.ID, "conversation_id", conv.ID)

	response.Success(c, gin.H{
		"custom_attributes": mergedAttrs,
	})
}
