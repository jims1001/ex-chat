package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// ContactExtensionHandler provides extended CRM endpoints for contacts
type ContactExtensionHandler struct {
	contactRepo   *repository.ContactRepository
	extensionRepo *repository.ContactExtensionRepository
}

// NewContactExtensionHandler creates a new handler instance
func NewContactExtensionHandler(
	contactRepo *repository.ContactRepository,
	extensionRepo *repository.ContactExtensionRepository,
) *ContactExtensionHandler {
	return &ContactExtensionHandler{
		contactRepo:   contactRepo,
		extensionRepo: extensionRepo,
	}
}

func (h *ContactExtensionHandler) getAccountID(c *gin.Context) uint {
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

func (h *ContactExtensionHandler) getContactID(c *gin.Context) (uint, error) {
	raw := c.Param("id")
	if raw == "" {
		raw = c.Param("contact_id")
	}
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid contact id")
	}
	return uint(id), nil
}

// ListContactAttachments retrieves all message attachments belonging to a contact
func (h *ContactExtensionHandler) ListContactAttachments(c *gin.Context) {
	accountID := h.getAccountID(c)
	contactID, err := h.getContactID(c)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		logger.WithComponent("contact_extension").Warn("contact attachments query rejected: contact not found or access denied",
			"account_id", accountID,
			"contact_id", contactID,
		)
		response.NotFound(c, "Contact not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", c.DefaultQuery("limit", "25")))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}

	filter := repository.ContactAttachmentFilter{
		FileType:   strings.TrimSpace(c.DefaultQuery("file_type", c.Query("type"))),
		SenderType: strings.TrimSpace(c.Query("sender_type")),
		Search:     strings.TrimSpace(c.DefaultQuery("q", c.Query("search"))),
		Page:       page,
		PageSize:   pageSize,
	}

	if rawConvID := c.Query("conversation_id"); rawConvID != "" {
		if cid, err := strconv.ParseUint(rawConvID, 10, 32); err == nil && cid > 0 {
			u := uint(cid)
			filter.ConversationID = &u
		}
	}

	items, total, err := h.extensionRepo.ListContactAttachments(accountID, contactID, filter)
	if err != nil {
		logger.WithComponent("contact_extension").Error("failed to list contact attachments",
			"account_id", accountID,
			"contact_id", contactID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list contact attachments")
		return
	}

	logger.WithComponent("contact_extension").Info("contact attachments listed successfully",
		"account_id", accountID,
		"contact_id", contactID,
		"file_type", filter.FileType,
		"returned_count", len(items),
		"total_count", total,
		"page", page,
		"page_size", pageSize,
	)

	response.Paginated(c, items, total, page, pageSize)
}

// GetContactableInboxes returns all inboxes reachable for the contact
func (h *ContactExtensionHandler) GetContactableInboxes(c *gin.Context) {
	accountID := h.getAccountID(c)
	contactID, err := h.getContactID(c)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		logger.WithComponent("contact_extension").Warn("contactable inboxes query rejected: contact not found or access denied",
			"account_id", accountID,
			"contact_id", contactID,
		)
		response.NotFound(c, "Contact not found")
		return
	}

	inboxes, err := h.extensionRepo.GetContactableInboxes(accountID, contactID)
	if err != nil {
		logger.WithComponent("contact_extension").Error("failed to get contactable inboxes",
			"account_id", accountID,
			"contact_id", contactID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get contactable inboxes")
		return
	}

	logger.WithComponent("contact_extension").Info("contactable inboxes retrieved successfully",
		"account_id", accountID,
		"contact_id", contactID,
		"count", len(inboxes),
	)

	response.Success(c, inboxes)
}

// ListContactInboxes lists all existing ContactInbox records for a contact
func (h *ContactExtensionHandler) ListContactInboxes(c *gin.Context) {
	accountID := h.getAccountID(c)
	contactID, err := h.getContactID(c)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		logger.WithComponent("contact_extension").Warn("list contact inboxes rejected: contact not found or access denied",
			"account_id", accountID,
			"contact_id", contactID,
		)
		response.NotFound(c, "Contact not found")
		return
	}

	inboxes, err := h.extensionRepo.ListContactInboxes(accountID, contactID)
	if err != nil {
		logger.WithComponent("contact_extension").Error("failed to list contact inboxes",
			"account_id", accountID,
			"contact_id", contactID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list contact inboxes")
		return
	}

	logger.WithComponent("contact_extension").Info("contact inboxes listed successfully",
		"account_id", accountID,
		"contact_id", contactID,
		"count", len(inboxes),
	)

	response.Success(c, inboxes)
}

// CreateContactInboxReq represents request body for creating a ContactInbox
type CreateContactInboxReq struct {
	InboxID  uint   `json:"inbox_id" binding:"required"`
	SourceID string `json:"source_id"`
}

// CreateContactInbox links a new inbox identity to a contact
func (h *ContactExtensionHandler) CreateContactInbox(c *gin.Context) {
	accountID := h.getAccountID(c)
	contactID, err := h.getContactID(c)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		logger.WithComponent("contact_extension").Warn("create contact inbox rejected: contact not found or access denied",
			"account_id", accountID,
			"contact_id", contactID,
		)
		response.NotFound(c, "Contact not found")
		return
	}

	var req CreateContactInboxReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid parameters: inbox_id is required")
		return
	}

	ci, err := h.extensionRepo.CreateContactInbox(accountID, contactID, req.InboxID, strings.TrimSpace(req.SourceID))
	if err != nil {
		logger.WithComponent("contact_extension").Error("failed to create contact inbox",
			"account_id", accountID,
			"contact_id", contactID,
			"inbox_id", req.InboxID,
			"error", err.Error(),
		)
		response.BadRequest(c, fmt.Sprintf("Failed to link contact inbox: %v", err))
		return
	}

	logger.WithComponent("contact_extension").Info("contact inbox linked successfully",
		"account_id", accountID,
		"contact_id", contactID,
		"inbox_id", req.InboxID,
		"source_id", ci.SourceID,
		"contact_inbox_id", ci.ID,
	)

	response.Success(c, ci)
}

// DeleteContactInbox removes an inbox association from a contact
func (h *ContactExtensionHandler) DeleteContactInbox(c *gin.Context) {
	accountID := h.getAccountID(c)
	contactID, err := h.getContactID(c)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		logger.WithComponent("contact_extension").Warn("delete contact inbox rejected: contact not found or access denied",
			"account_id", accountID,
			"contact_id", contactID,
		)
		response.NotFound(c, "Contact not found")
		return
	}

	// 1. By contact_inbox_id
	if rawCIID := c.Param("contact_inbox_id"); rawCIID != "" {
		ciID, err := strconv.ParseUint(rawCIID, 10, 32)
		if err != nil || ciID == 0 {
			response.BadRequest(c, "Invalid contact inbox ID")
			return
		}
		if err := h.extensionRepo.DeleteContactInbox(accountID, contactID, uint(ciID)); err != nil {
			logger.WithComponent("contact_extension").Warn("failed to delete contact inbox by id",
				"account_id", accountID,
				"contact_id", contactID,
				"contact_inbox_id", ciID,
				"error", err.Error(),
			)
			response.NotFound(c, "Contact inbox not found")
			return
		}
		logger.WithComponent("contact_extension").Info("contact inbox deleted successfully by id",
			"account_id", accountID,
			"contact_id", contactID,
			"contact_inbox_id", ciID,
		)
		response.Success(c, gin.H{"deleted": true, "contact_inbox_id": ciID})
		return
	}

	// 2. By inbox_id
	if rawInboxID := c.Param("inbox_id"); rawInboxID != "" {
		inboxID, err := strconv.ParseUint(rawInboxID, 10, 32)
		if err != nil || inboxID == 0 {
			response.BadRequest(c, "Invalid inbox ID")
			return
		}
		if err := h.extensionRepo.DeleteContactInboxByInboxID(accountID, contactID, uint(inboxID)); err != nil {
			logger.WithComponent("contact_extension").Warn("failed to delete contact inbox by inbox_id",
				"account_id", accountID,
				"contact_id", contactID,
				"inbox_id", inboxID,
				"error", err.Error(),
			)
			response.NotFound(c, "Contact inbox not found for this inbox")
			return
		}
		logger.WithComponent("contact_extension").Info("contact inbox deleted successfully by inbox_id",
			"account_id", accountID,
			"contact_id", contactID,
			"inbox_id", inboxID,
		)
		response.Success(c, gin.H{"deleted": true, "inbox_id": inboxID})
		return
	}

	response.BadRequest(c, "Missing contact_inbox_id or inbox_id")
}

// ListContactConversations lists all conversation threads of a contact
func (h *ContactExtensionHandler) ListContactConversations(c *gin.Context) {
	accountID := h.getAccountID(c)
	contactID, err := h.getContactID(c)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		logger.WithComponent("contact_extension").Warn("list contact conversations rejected: contact not found or access denied",
			"account_id", accountID,
			"contact_id", contactID,
		)
		response.NotFound(c, "Contact not found")
		return
	}

	status := strings.TrimSpace(c.Query("status"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}

	var inboxID *uint
	if rawInboxID := c.Query("inbox_id"); rawInboxID != "" {
		if id, err := strconv.ParseUint(rawInboxID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			inboxID = &u
		}
	}

	conversations, total, err := h.extensionRepo.ListContactConversations(accountID, contactID, status, inboxID, page, pageSize)
	if err != nil {
		logger.WithComponent("contact_extension").Error("failed to list contact conversations",
			"account_id", accountID,
			"contact_id", contactID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list contact conversations")
		return
	}

	logger.WithComponent("contact_extension").Info("contact conversations listed successfully",
		"account_id", accountID,
		"contact_id", contactID,
		"count", len(conversations),
		"total", total,
		"page", page,
		"page_size", pageSize,
	)

	response.Paginated(c, conversations, total, page, pageSize)
}

// GetContactStats returns interaction and communication statistics for a contact
func (h *ContactExtensionHandler) GetContactStats(c *gin.Context) {
	accountID := h.getAccountID(c)
	contactID, err := h.getContactID(c)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	stats, err := h.extensionRepo.GetContactStats(accountID, contactID)
	if err != nil {
		logger.WithComponent("contact_extension").Error("failed to get contact stats",
			"account_id", accountID,
			"contact_id", contactID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get contact stats")
		return
	}
	if stats == nil {
		logger.WithComponent("contact_extension").Warn("contact stats rejected: contact not found or access denied",
			"account_id", accountID,
			"contact_id", contactID,
		)
		response.NotFound(c, "Contact not found")
		return
	}

	logger.WithComponent("contact_extension").Info("contact stats retrieved successfully",
		"account_id", accountID,
		"contact_id", contactID,
		"conversations_count", stats.ConversationsCount,
		"messages_count", stats.MessagesCount,
		"attachments_count", stats.AttachmentsCount,
		"contact_inboxes_count", stats.ContactInboxesCount,
	)

	c.JSON(http.StatusOK, response.Response{
		Success: true,
		Data:    stats,
	})
}
