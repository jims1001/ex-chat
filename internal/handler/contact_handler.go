package handler

import (
	"bytes"
	"encoding/csv"
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
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ContactHandler struct {
	contactRepo *repository.ContactRepository
	inboxRepo   *repository.InboxRepository
	labelRepo   *repository.LabelRepository
	companyRepo *repository.CompanyRepository
	db          *gorm.DB
}

func NewContactHandler(contactRepo *repository.ContactRepository, inboxRepo *repository.InboxRepository) *ContactHandler {
	return &ContactHandler{
		contactRepo: contactRepo,
		inboxRepo:   inboxRepo,
	}
}

func (h *ContactHandler) SetExtraRepos(labelRepo *repository.LabelRepository, companyRepo *repository.CompanyRepository, db *gorm.DB) {
	h.labelRepo = labelRepo
	h.companyRepo = companyRepo
	h.db = db
}

type CreateContactRequest struct {
	Name             string   `json:"name"`
	Email            string   `json:"email"`
	PhoneNumber      string   `json:"phone_number"`
	Identifier       string   `json:"identifier"`
	CustomAttributes string   `json:"custom_attributes"`
	CompanyID        *uint    `json:"company_id"`
	Labels           []string `json:"labels"`
}

type UpdateContactRequest struct {
	Name             string   `json:"name"`
	Email            string   `json:"email"`
	PhoneNumber      string   `json:"phone_number"`
	Identifier       string   `json:"identifier"`
	CustomAttributes string   `json:"custom_attributes"`
	CompanyID        *uint    `json:"company_id"`
	Labels           []string `json:"labels"`
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
	label := strings.TrimSpace(c.Query("label"))
	var companyID *uint
	if rawCID := c.Query("company_id"); rawCID != "" {
		if cid, err := strconv.ParseUint(rawCID, 10, 32); err == nil && cid > 0 {
			u := uint(cid)
			companyID = &u
		}
	}

	contacts, total, err := h.contactRepo.ListFiltered(accountID, page, pageSize, search, label, companyID)
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

	if req.CompanyID != nil && *req.CompanyID > 0 {
		if h.companyRepo != nil {
			comp, err := h.companyRepo.GetByID(c.Request.Context(), accountID, *req.CompanyID)
			if err != nil || comp == nil {
				response.BadRequest(c, "Company not found")
				return
			}
		}
	}

	contact := domain.Contact{
		AccountID:        accountID,
		Name:             req.Name,
		Email:            req.Email,
		PhoneNumber:      req.PhoneNumber,
		Identifier:       req.Identifier,
		CustomAttributes: req.CustomAttributes,
		CompanyID:        req.CompanyID,
	}

	if err := h.contactRepo.Create(&contact); err != nil {
		logger.WithComponent("contact").Error("failed to create contact",
			"account_id", accountID,
			"name", req.Name,
			"email", req.Email,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create contact")
		return
	}

	if len(req.Labels) > 0 && h.labelRepo != nil {
		_, _ = h.labelRepo.SetContactLabels(accountID, contact.ID, req.Labels)
	}

	fresh, _ := h.contactRepo.FindByID(accountID, contact.ID)
	if fresh != nil {
		contact = *fresh
	}

	logger.WithComponent("contact").Info("contact created",
		"account_id", accountID,
		"contact_id", contact.ID,
		"name", contact.Name,
		"email", contact.Email,
	)

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
	if req.CompanyID != nil {
		if *req.CompanyID > 0 {
			if h.companyRepo != nil {
				comp, err := h.companyRepo.GetByID(c.Request.Context(), accountID, *req.CompanyID)
				if err != nil || comp == nil {
					response.BadRequest(c, "Company not found")
					return
				}
			}
			contact.CompanyID = req.CompanyID
		} else {
			contact.CompanyID = nil
		}
	}

	if err := h.contactRepo.Update(contact); err != nil {
		logger.WithComponent("contact").Error("failed to update contact",
			"account_id", accountID,
			"contact_id", contact.ID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update contact")
		return
	}

	if req.Labels != nil && h.labelRepo != nil {
		_, _ = h.labelRepo.SetContactLabels(accountID, contact.ID, req.Labels)
	}

	fresh, _ := h.contactRepo.FindByID(accountID, contact.ID)
	if fresh != nil {
		contact = fresh
	}

	logger.WithComponent("contact").Info("contact updated",
		"account_id", accountID,
		"contact_id", contact.ID,
	)

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
		logger.WithComponent("contact").Error("failed to delete contact",
			"account_id", accountID,
			"contact_id", contactID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete contact")
		return
	}

	logger.WithComponent("contact").Info("contact deleted",
		"account_id", accountID,
		"contact_id", contactID,
	)

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
		logger.WithComponent("contact").Error("failed to merge contacts",
			"account_id", accountID,
			"base_contact_id", req.BaseContactID,
			"mergee_contact_id", req.MergeeContactID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to merge contacts")
		return
	}

	logger.WithComponent("contact").Info("contacts merged successfully",
		"account_id", accountID,
		"base_contact_id", req.BaseContactID,
		"mergee_contact_id", req.MergeeContactID,
	)

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

// ----------------- Contact Labels -----------------

// GetContactLabels returns all labels attached to a contact
func (h *ContactHandler) GetContactLabels(c *gin.Context) {
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

	if h.labelRepo == nil {
		response.Success(c, contact.Labels)
		return
	}

	labels, err := h.labelRepo.GetContactLabels(contact.ID)
	if err != nil {
		response.InternalError(c, "Failed to get contact labels")
		return
	}

	response.Success(c, labels)
}

type SetContactLabelsRequest struct {
	Labels   []string `json:"labels"`
	LabelIDs []uint   `json:"label_ids"`
}

// SetContactLabels updates or replaces labels on a contact
func (h *ContactHandler) SetContactLabels(c *gin.Context) {
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

	var req SetContactLabelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	var labelNamesOrIDs []string
	for _, l := range req.Labels {
		if strings.TrimSpace(l) != "" {
			labelNamesOrIDs = append(labelNamesOrIDs, strings.TrimSpace(l))
		}
	}
	for _, id := range req.LabelIDs {
		labelNamesOrIDs = append(labelNamesOrIDs, strconv.FormatUint(uint64(id), 10))
	}

	if h.labelRepo != nil {
		labels, err := h.labelRepo.SetContactLabels(accountID, contact.ID, labelNamesOrIDs)
		if err != nil {
			response.InternalError(c, "Failed to set contact labels: "+err.Error())
			return
		}
		response.Success(c, labels)
		return
	}

	response.Success(c, []domain.Label{})
}

// DetachContactLabel removes a specific label from a contact
func (h *ContactHandler) DetachContactLabel(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	contactID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid contact ID")
		return
	}

	labelID, err := strconv.ParseUint(c.Param("label_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid label ID")
		return
	}

	contact, err := h.contactRepo.FindByID(accountID, uint(contactID))
	if err != nil || contact == nil {
		response.NotFound(c, "Contact not found")
		return
	}

	if h.labelRepo != nil {
		_ = h.labelRepo.DetachFromContact(contact.ID, uint(labelID))
	}

	response.Success(c, gin.H{"deleted": true})
}

// ----------------- Contact Export -----------------

type ExportContactsRequest struct {
	Search    string   `json:"q"`
	Label     string   `json:"label"`
	CompanyID *uint    `json:"company_id"`
	Columns   []string `json:"columns"`
	Format    string   `json:"format"`
}

// ExportContacts exports contacts as CSV or JSON with UTF-8 BOM
func (h *ContactHandler) ExportContacts(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req ExportContactsRequest
	if c.Request.Method == http.MethodPost && c.Request.Body != nil && c.Request.ContentLength > 0 {
		_ = c.ShouldBindJSON(&req)
	}

	search := req.Search
	if search == "" {
		search = strings.TrimSpace(c.Query("q"))
	}
	label := req.Label
	if label == "" {
		label = strings.TrimSpace(c.Query("label"))
	}
	companyID := req.CompanyID
	if companyID == nil && c.Query("company_id") != "" {
		if cid, err := strconv.ParseUint(c.Query("company_id"), 10, 32); err == nil && cid > 0 {
			u := uint(cid)
			companyID = &u
		}
	}
	format := req.Format
	if format == "" {
		format = c.Query("format")
	}

	contacts, _, err := h.contactRepo.ListFiltered(accountID, 1, 10000, search, label, companyID)
	if err != nil {
		response.InternalError(c, "Failed to fetch contacts for export")
		return
	}

	columns := req.Columns
	if len(columns) == 0 {
		columns = []string{"id", "name", "email", "phone_number", "identifier", "company", "labels", "created_at"}
	}

	var buf bytes.Buffer
	// UTF-8 BOM for international Excel compatibility
	buf.WriteString("\xEF\xBB\xBF")

	writer := csv.NewWriter(&buf)
	_ = writer.Write(columns)

	for _, ct := range contacts {
		var row []string
		for _, col := range columns {
			switch strings.ToLower(col) {
			case "id":
				row = append(row, strconv.FormatUint(uint64(ct.ID), 10))
			case "name":
				row = append(row, ct.Name)
			case "email":
				row = append(row, ct.Email)
			case "phone_number", "phone":
				row = append(row, ct.PhoneNumber)
			case "identifier":
				row = append(row, ct.Identifier)
			case "company":
				if ct.Company != nil {
					row = append(row, ct.Company.Name)
				} else {
					row = append(row, "")
				}
			case "labels":
				var lNames []string
				for _, l := range ct.Labels {
					lNames = append(lNames, l.Title)
				}
				row = append(row, strings.Join(lNames, "; "))
			case "created_at":
				row = append(row, ct.CreatedAt.Format("2006-01-02 15:04:05"))
			case "custom_attributes":
				row = append(row, ct.CustomAttributes)
			default:
				row = append(row, "")
			}
		}
		_ = writer.Write(row)
	}
	writer.Flush()

	csvContent := buf.String()

	logger.WithComponent("contact").Info("contacts exported",
		"account_id", accountID,
		"count", len(contacts),
		"format", format,
		"search", search,
		"label", label,
	)

	if strings.ToLower(format) == "json" || (c.GetHeader("Accept") == "application/json" && c.Query("download") != "1") {
		c.JSON(http.StatusOK, gin.H{
			"total_count": len(contacts),
			"csv_data":    csvContent,
			"filename":    fmt.Sprintf("contacts_export_%d_%d.csv", accountID, time.Now().Unix()),
		})
		return
	}

	filename := fmt.Sprintf("contacts_export_%d_%d.csv", accountID, time.Now().Unix())
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}
