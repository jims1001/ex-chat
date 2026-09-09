package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// CompanyExtensionHandler handles company-level conversations, notes aggregation and stats
type CompanyExtensionHandler struct {
	companyRepo   *repository.CompanyRepository
	extensionRepo *repository.CompanyExtensionRepository
}

// NewCompanyExtensionHandler creates a new handler instance
func NewCompanyExtensionHandler(
	companyRepo *repository.CompanyRepository,
	extensionRepo *repository.CompanyExtensionRepository,
) *CompanyExtensionHandler {
	return &CompanyExtensionHandler{
		companyRepo:   companyRepo,
		extensionRepo: extensionRepo,
	}
}

func (h *CompanyExtensionHandler) getAccountID(c *gin.Context) uint {
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

func (h *CompanyExtensionHandler) getUserID(c *gin.Context) uint {
	if raw, exists := c.Get(middleware.ContextUserID); exists {
		if u, ok := raw.(uint); ok && u > 0 {
			return u
		}
	}
	return 1
}

func (h *CompanyExtensionHandler) getCompanyID(c *gin.Context) (uint, error) {
	raw := c.Param("id")
	if raw == "" {
		raw = c.Param("company_id")
	}
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("invalid company id")
	}
	return uint(id), nil
}

// ListCompanyConversations lists conversations across all contacts in this company
func (h *CompanyExtensionHandler) ListCompanyConversations(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	company, err := h.companyRepo.GetByID(c.Request.Context(), accountID, companyID)
	if err != nil || company == nil {
		logger.WithComponent("company_extension").Warn("company conversations rejected: company not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
		)
		response.NotFound(c, "Company not found")
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

	var contactID *uint
	if rawContactID := c.Query("contact_id"); rawContactID != "" {
		if id, err := strconv.ParseUint(rawContactID, 10, 32); err == nil && id > 0 {
			u := uint(id)
			contactID = &u
		}
	}

	conversations, total, err := h.extensionRepo.ListCompanyConversations(accountID, companyID, status, inboxID, contactID, page, pageSize)
	if err != nil {
		logger.WithComponent("company_extension").Error("failed to list company conversations",
			"account_id", accountID,
			"company_id", companyID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list company conversations")
		return
	}

	logger.WithComponent("company_extension").Info("company conversations listed successfully",
		"account_id", accountID,
		"company_id", companyID,
		"status", status,
		"returned_count", len(conversations),
		"total_count", total,
		"page", page,
		"page_size", pageSize,
	)

	response.Paginated(c, conversations, total, page, pageSize)
}

// ListCompanyNotes lists unified notes for a company (both company-level and contact-level)
func (h *CompanyExtensionHandler) ListCompanyNotes(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	company, err := h.companyRepo.GetByID(c.Request.Context(), accountID, companyID)
	if err != nil || company == nil {
		logger.WithComponent("company_extension").Warn("company notes query rejected: company not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
		)
		response.NotFound(c, "Company not found")
		return
	}

	scope := strings.ToLower(strings.TrimSpace(c.DefaultQuery("scope", "all"))) // all, company, contacts
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}

	notes, total, err := h.extensionRepo.ListUnifiedNotes(accountID, companyID, scope, page, pageSize)
	if err != nil {
		logger.WithComponent("company_extension").Error("failed to list company notes",
			"account_id", accountID,
			"company_id", companyID,
			"scope", scope,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list company notes")
		return
	}

	logger.WithComponent("company_extension").Info("company notes listed successfully",
		"account_id", accountID,
		"company_id", companyID,
		"scope", scope,
		"returned_count", len(notes),
		"total_count", total,
		"page", page,
		"page_size", pageSize,
	)

	response.Paginated(c, notes, total, page, pageSize)
}

// GetCompanyNotesSummary returns note counts and timeline breakdown
func (h *CompanyExtensionHandler) GetCompanyNotesSummary(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	company, err := h.companyRepo.GetByID(c.Request.Context(), accountID, companyID)
	if err != nil || company == nil {
		logger.WithComponent("company_extension").Warn("company notes summary rejected: company not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
		)
		response.NotFound(c, "Company not found")
		return
	}

	summary, err := h.extensionRepo.GetCompanyNotesSummary(accountID, companyID)
	if err != nil {
		logger.WithComponent("company_extension").Error("failed to get company notes summary",
			"account_id", accountID,
			"company_id", companyID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get company notes summary")
		return
	}

	logger.WithComponent("company_extension").Info("company notes summary retrieved successfully",
		"account_id", accountID,
		"company_id", companyID,
		"total_notes", summary.TotalNotes,
		"company_notes_count", summary.CompanyNotesCount,
		"contact_notes_count", summary.ContactNotesCount,
	)

	response.Success(c, summary)
}

// CompanyNoteReq represents input for creating or editing a direct company note
type CompanyNoteReq struct {
	Content string `json:"content" binding:"required"`
}

// CreateCompanyNote creates a direct note for a company
func (h *CompanyExtensionHandler) CreateCompanyNote(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	company, err := h.companyRepo.GetByID(c.Request.Context(), accountID, companyID)
	if err != nil || company == nil {
		logger.WithComponent("company_extension").Warn("create company note rejected: company not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
		)
		response.NotFound(c, "Company not found")
		return
	}

	var req CompanyNoteReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		response.BadRequest(c, "Content is required")
		return
	}

	userID := h.getUserID(c)
	note := domain.CompanyNote{
		AccountID: accountID,
		CompanyID: companyID,
		UserID:    userID,
		Content:   strings.TrimSpace(req.Content),
	}

	if err := h.extensionRepo.CreateCompanyNote(&note); err != nil {
		logger.WithComponent("company_extension").Error("failed to create company note",
			"account_id", accountID,
			"company_id", companyID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create company note")
		return
	}

	logger.WithComponent("company_extension").Info("company note created successfully",
		"account_id", accountID,
		"company_id", companyID,
		"note_id", note.ID,
		"user_id", userID,
	)

	response.Success(c, note)
}

// GetCompanyNote retrieves a single company note
func (h *CompanyExtensionHandler) GetCompanyNote(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	rawNoteID := c.Param("note_id")
	noteID, err := strconv.ParseUint(rawNoteID, 10, 32)
	if err != nil || noteID == 0 {
		response.BadRequest(c, "Invalid note ID")
		return
	}

	note, err := h.extensionRepo.GetCompanyNote(accountID, companyID, uint(noteID))
	if err != nil || note == nil {
		logger.WithComponent("company_extension").Warn("company note not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
			"note_id", noteID,
		)
		response.NotFound(c, "Company note not found")
		return
	}

	logger.WithComponent("company_extension").Info("company note retrieved successfully",
		"account_id", accountID,
		"company_id", companyID,
		"note_id", noteID,
	)

	response.Success(c, note)
}

// UpdateCompanyNote edits an existing company note
func (h *CompanyExtensionHandler) UpdateCompanyNote(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	rawNoteID := c.Param("note_id")
	noteID, err := strconv.ParseUint(rawNoteID, 10, 32)
	if err != nil || noteID == 0 {
		response.BadRequest(c, "Invalid note ID")
		return
	}

	var req CompanyNoteReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		response.BadRequest(c, "Content is required")
		return
	}

	updated, err := h.extensionRepo.UpdateCompanyNote(accountID, companyID, uint(noteID), strings.TrimSpace(req.Content))
	if err != nil || updated == nil {
		logger.WithComponent("company_extension").Warn("update company note rejected: note not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
			"note_id", noteID,
		)
		response.NotFound(c, "Company note not found")
		return
	}

	logger.WithComponent("company_extension").Info("company note updated successfully",
		"account_id", accountID,
		"company_id", companyID,
		"note_id", noteID,
	)

	response.Success(c, updated)
}

// DeleteCompanyNote deletes an existing company note
func (h *CompanyExtensionHandler) DeleteCompanyNote(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	rawNoteID := c.Param("note_id")
	noteID, err := strconv.ParseUint(rawNoteID, 10, 32)
	if err != nil || noteID == 0 {
		response.BadRequest(c, "Invalid note ID")
		return
	}

	if err := h.extensionRepo.DeleteCompanyNote(accountID, companyID, uint(noteID)); err != nil {
		logger.WithComponent("company_extension").Warn("delete company note rejected: note not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
			"note_id", noteID,
		)
		response.NotFound(c, "Company note not found")
		return
	}

	logger.WithComponent("company_extension").Info("company note deleted successfully",
		"account_id", accountID,
		"company_id", companyID,
		"note_id", noteID,
	)

	response.Success(c, gin.H{"deleted": true, "note_id": noteID})
}

// GetCompanyStats returns aggregated interactions across all contacts of a company
func (h *CompanyExtensionHandler) GetCompanyStats(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	stats, err := h.extensionRepo.GetCompanyStats(accountID, companyID)
	if err != nil {
		logger.WithComponent("company_extension").Error("failed to get company stats",
			"account_id", accountID,
			"company_id", companyID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get company stats")
		return
	}
	if stats == nil {
		logger.WithComponent("company_extension").Warn("company stats rejected: company not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
		)
		response.NotFound(c, "Company not found")
		return
	}

	logger.WithComponent("company_extension").Info("company stats retrieved successfully",
		"account_id", accountID,
		"company_id", companyID,
		"contacts_count", stats.ContactsCount,
		"conversations_count", stats.ConversationsCount,
		"messages_count", stats.MessagesCount,
		"notes_count", stats.NotesCount,
	)

	c.JSON(http.StatusOK, response.Response{
		Success: true,
		Data:    stats,
	})
}

// ListCompanyAttachments retrieves all attachments exchanged across all contacts in this company
func (h *CompanyExtensionHandler) ListCompanyAttachments(c *gin.Context) {
	accountID := h.getAccountID(c)
	companyID, err := h.getCompanyID(c)
	if err != nil {
		response.BadRequest(c, "Invalid company ID")
		return
	}

	company, err := h.companyRepo.GetByID(c.Request.Context(), accountID, companyID)
	if err != nil || company == nil {
		logger.WithComponent("company_extension").Warn("company attachments rejected: company not found or access denied",
			"account_id", accountID,
			"company_id", companyID,
		)
		response.NotFound(c, "Company not found")
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

	filter := repository.CompanyAttachmentFilter{
		FileType:   strings.TrimSpace(c.DefaultQuery("file_type", c.Query("type"))),
		SenderType: strings.TrimSpace(c.Query("sender_type")),
		Search:     strings.TrimSpace(c.DefaultQuery("q", c.Query("search"))),
		Page:       page,
		PageSize:   pageSize,
	}

	if rawCID := c.Query("contact_id"); rawCID != "" {
		if cid, err := strconv.ParseUint(rawCID, 10, 32); err == nil && cid > 0 {
			u := uint(cid)
			filter.ContactID = &u
		}
	}
	if rawConvID := c.Query("conversation_id"); rawConvID != "" {
		if cid, err := strconv.ParseUint(rawConvID, 10, 32); err == nil && cid > 0 {
			u := uint(cid)
			filter.ConversationID = &u
		}
	}

	items, total, err := h.extensionRepo.ListCompanyAttachments(accountID, companyID, filter)
	if err != nil {
		logger.WithComponent("company_extension").Error("failed to list company attachments",
			"account_id", accountID,
			"company_id", companyID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list company attachments")
		return
	}

	logger.WithComponent("company_extension").Info("company attachments listed successfully",
		"account_id", accountID,
		"company_id", companyID,
		"returned_count", len(items),
		"total_count", total,
		"page", page,
		"page_size", pageSize,
	)

	response.Paginated(c, items, total, page, pageSize)
}
