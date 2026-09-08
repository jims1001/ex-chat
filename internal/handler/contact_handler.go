package handler

import (
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type ContactHandler struct {
	contactRepo *repository.ContactRepository
	inboxRepo   *repository.InboxRepository
}

func NewContactHandler(contactRepo *repository.ContactRepository, inboxRepo *repository.InboxRepository) *ContactHandler {
	return &ContactHandler{
		contactRepo: contactRepo,
		inboxRepo:   inboxRepo,
	}
}

type CreateContactRequest struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	PhoneNumber      string `json:"phone_number"`
	Identifier       string `json:"identifier"`
	CustomAttributes string `json:"custom_attributes"`
}

type UpdateContactRequest struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	PhoneNumber      string `json:"phone_number"`
	Identifier       string `json:"identifier"`
	CustomAttributes string `json:"custom_attributes"`
}

type MergeContactRequest struct {
	BaseContactID   uint `json:"base_contact_id" binding:"required"`
	MergeeContactID uint `json:"mergee_contact_id" binding:"required"`
}

type WidgetContactRequest struct {
	SourceID         string `json:"source_id" binding:"required"`
	Name             string `json:"name"`
	Email            string `json:"email"`
	PhoneNumber      string `json:"phone_number"`
	Identifier       string `json:"identifier"`
	CustomAttributes string `json:"custom_attributes"`
}

func (h *ContactHandler) ListContacts(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	search := strings.TrimSpace(c.Query("q"))

	contacts, total, err := h.contactRepo.List(accountID, page, pageSize, search)
	if err != nil {
		response.InternalError(c, "Failed to list contacts")
		return
	}

	response.Paginated(c, contacts, total, page, pageSize)
}

func (h *ContactHandler) CreateContact(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if req.Email != "" {
		existing, _ := h.contactRepo.FindByEmail(accountID, req.Email)
		if existing != nil {
			response.BadRequest(c, "Contact with this email already exists")
			return
		}
	}

	contact := domain.Contact{
		AccountID:        accountID,
		Name:             req.Name,
		Email:            req.Email,
		PhoneNumber:      req.PhoneNumber,
		Identifier:       req.Identifier,
		CustomAttributes: req.CustomAttributes,
	}

	if err := h.contactRepo.Create(&contact); err != nil {
		response.InternalError(c, "Failed to create contact")
		return
	}

	response.Created(c, contact)
}

func (h *ContactHandler) GetContact(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	contactID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, uint(contactID))
	if err != nil || contact == nil {
		response.NotFound(c, "Contact not found")
		return
	}

	response.Success(c, contact)
}

func (h *ContactHandler) UpdateContact(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	contactID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, uint(contactID))
	if err != nil || contact == nil {
		response.NotFound(c, "Contact not found")
		return
	}

	var req UpdateContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Name != "" {
		contact.Name = req.Name
	}
	if req.Email != "" {
		contact.Email = strings.ToLower(strings.TrimSpace(req.Email))
	}
	if req.PhoneNumber != "" {
		contact.PhoneNumber = req.PhoneNumber
	}
	if req.Identifier != "" {
		contact.Identifier = req.Identifier
	}
	if req.CustomAttributes != "" {
		contact.CustomAttributes = req.CustomAttributes
	}

	if err := h.contactRepo.Update(contact); err != nil {
		response.InternalError(c, "Failed to update contact")
		return
	}

	response.Success(c, contact)
}

func (h *ContactHandler) DeleteContact(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	contactID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	if err := h.contactRepo.Delete(accountID, uint(contactID)); err != nil {
		response.InternalError(c, "Failed to delete contact")
		return
	}

	response.Success(c, gin.H{"deleted": true})
}

func (h *ContactHandler) MergeContact(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req MergeContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.BaseContactID == req.MergeeContactID {
		response.BadRequest(c, "Base contact and mergee contact cannot be the same")
		return
	}

	baseContact, _ := h.contactRepo.FindByID(accountID, req.BaseContactID)
	if baseContact == nil {
		response.NotFound(c, "Base contact not found")
		return
	}

	mergeeContact, _ := h.contactRepo.FindByID(accountID, req.MergeeContactID)
	if mergeeContact == nil {
		response.NotFound(c, "Mergee contact not found")
		return
	}

	if err := h.contactRepo.MergeContacts(accountID, req.BaseContactID, req.MergeeContactID); err != nil {
		response.InternalError(c, "Failed to merge contacts")
		return
	}

	response.Success(c, baseContact)
}

// WidgetIdentify handles visitor identification from web widget
func (h *ContactHandler) WidgetIdentify(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	var req WidgetContactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	contact, err := h.contactRepo.FindContactBySourceID(inbox.ID, req.SourceID)
	if err != nil {
		response.InternalError(c, "Error looking up contact")
		return
	}

	if contact == nil {
		newContact := domain.Contact{
			AccountID:        inbox.AccountID,
			Name:             req.Name,
			Email:            strings.ToLower(strings.TrimSpace(req.Email)),
			PhoneNumber:      req.PhoneNumber,
			Identifier:       req.Identifier,
			CustomAttributes: req.CustomAttributes,
		}
		if newContact.Name == "" {
			newContact.Name = "Visitor " + req.SourceID[:min(8, len(req.SourceID))]
		}
		if err := h.contactRepo.Create(&newContact); err != nil {
			response.InternalError(c, "Failed to create visitor contact")
			return
		}
		_, _ = h.contactRepo.FindOrCreateContactInbox(newContact.ID, inbox.ID, req.SourceID)
		contact = &newContact
	} else {
		updated := false
		if req.Name != "" && contact.Name != req.Name {
			contact.Name = req.Name
			updated = true
		}
		if req.Email != "" && contact.Email != req.Email {
			contact.Email = strings.ToLower(strings.TrimSpace(req.Email))
			updated = true
		}
		if req.PhoneNumber != "" && contact.PhoneNumber != req.PhoneNumber {
			contact.PhoneNumber = req.PhoneNumber
			updated = true
		}
		if req.CustomAttributes != "" {
			contact.CustomAttributes = req.CustomAttributes
			updated = true
		}
		if updated {
			_ = h.contactRepo.Update(contact)
		}
	}

	response.Success(c, contact)
}
