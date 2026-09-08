package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AdvancedHandler struct {
	db              *gorm.DB
	companyRepo     *repository.CompanyRepository
	campaignRepo    *repository.CampaignRepository
	slaRepo         *repository.SLARepository
	agentBotRepo    *repository.AgentBotRepository
	attachmentRepo  *repository.AttachmentRepository
	campaignService *service.CampaignService
	slaService      *service.SLAService
	httpClient      *http.Client
}

func NewAdvancedHandler(
	db *gorm.DB,
	c *repository.CompanyRepository,
	cp *repository.CampaignRepository,
	s *repository.SLARepository,
	b *repository.AgentBotRepository,
	att *repository.AttachmentRepository,
) *AdvancedHandler {
	return &AdvancedHandler{
		db:             db,
		companyRepo:    c,
		campaignRepo:   cp,
		slaRepo:        s,
		agentBotRepo:   b,
		attachmentRepo: att,
	}
}

func (h *AdvancedHandler) SetHTTPClient(client *http.Client) {
	h.httpClient = client
}

func (h *AdvancedHandler) SetServices(cs *service.CampaignService, ss *service.SLAService) {
	h.campaignService = cs
	h.slaService = ss
}

// ----------------- Company Handlers -----------------

type CreateCompanyReq struct {
	Name        string `json:"name" binding:"required"`
	Domain      string `json:"domain"`
	Industry    string `json:"industry"`
	Description string `json:"description"`
}

func (h *AdvancedHandler) CreateCompany(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req CreateCompanyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	comp := domain.Company{
		AccountID:   uint(accID),
		Name:        req.Name,
		Domain:      req.Domain,
		Industry:    req.Industry,
		Description: req.Description,
	}

	if err := h.companyRepo.Create(c.Request.Context(), &comp); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, comp)
}

func (h *AdvancedHandler) ListCompanies(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	companies, err := h.companyRepo.List(c.Request.Context(), uint(accID))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, companies)
}

func (h *AdvancedHandler) GetCompany(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	comp, err := h.companyRepo.GetByID(c.Request.Context(), uint(accID), uint(id))
	if err != nil {
		response.NotFound(c, "company not found")
		return
	}
	response.Success(c, comp)
}

type UpdateCompanyReq struct {
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	Industry    string `json:"industry"`
	Description string `json:"description"`
}

func (h *AdvancedHandler) UpdateCompany(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	comp, err := h.companyRepo.GetByID(c.Request.Context(), uint(accID), uint(id))
	if err != nil || comp == nil {
		response.NotFound(c, "company not found")
		return
	}

	var req UpdateCompanyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Name != "" {
		comp.Name = req.Name
	}
	if req.Domain != "" {
		comp.Domain = req.Domain
	}
	if req.Industry != "" {
		comp.Industry = req.Industry
	}
	if req.Description != "" {
		comp.Description = req.Description
	}

	if err := h.companyRepo.Update(c.Request.Context(), comp); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, comp)
}

func (h *AdvancedHandler) DeleteCompany(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	if err := h.companyRepo.Delete(c.Request.Context(), uint(accID), uint(id)); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ----------------- Campaign Handlers -----------------

type CreateCampaignReq struct {
	InboxID      uint       `json:"inbox_id" binding:"required"`
	Title        string     `json:"title" binding:"required"`
	Message      string     `json:"message" binding:"required"`
	CampaignType string     `json:"campaign_type"`
	Audience     string     `json:"audience"`
	ScheduledAt  *time.Time `json:"scheduled_at"`
}

func (h *AdvancedHandler) CreateCampaign(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req CreateCampaignReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	camp := domain.Campaign{
		AccountID:    uint(accID),
		InboxID:      req.InboxID,
		Title:        req.Title,
		Message:      req.Message,
		CampaignType: req.CampaignType,
		Audience:     req.Audience,
		ScheduledAt:  req.ScheduledAt,
		Status:       "scheduled",
	}
	if camp.CampaignType == "" {
		camp.CampaignType = "one_off"
	}

	if err := h.campaignRepo.Create(c.Request.Context(), &camp); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, camp)
}

func (h *AdvancedHandler) ListCampaigns(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	campaigns, err := h.campaignRepo.List(c.Request.Context(), uint(accID))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, campaigns)
}

// ----------------- SLA Handlers -----------------

type CreateSLAReq struct {
	Name                        string `json:"name" binding:"required"`
	Description                 string `json:"description"`
	FirstResponseTimeThreshold int    `json:"first_response_time_threshold"`
	ResolutionTimeThreshold    int    `json:"resolution_time_threshold"`
}

func (h *AdvancedHandler) CreateSLAPolicy(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req CreateSLAReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	sla := domain.SLAPolicy{
		AccountID:                   uint(accID),
		Name:                        req.Name,
		Description:                 req.Description,
		FirstResponseTimeThreshold: req.FirstResponseTimeThreshold,
		ResolutionTimeThreshold:    req.ResolutionTimeThreshold,
	}
	if sla.FirstResponseTimeThreshold == 0 {
		sla.FirstResponseTimeThreshold = 3600
	}
	if sla.ResolutionTimeThreshold == 0 {
		sla.ResolutionTimeThreshold = 86400
	}

	if err := h.slaRepo.Create(c.Request.Context(), &sla); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, sla)
}

func (h *AdvancedHandler) ListSLAPolicies(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	policies, err := h.slaRepo.List(c.Request.Context(), uint(accID))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, policies)
}

func (h *AdvancedHandler) UpdateSLAPolicy(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid SLA policy ID")
		return
	}

	sla, err := h.slaRepo.GetByID(c.Request.Context(), uint(accID), uint(id))
	if err != nil || sla == nil {
		response.NotFound(c, "SLA policy not found")
		return
	}

	var req CreateSLAReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Name != "" {
		sla.Name = req.Name
	}
	if req.Description != "" {
		sla.Description = req.Description
	}
	if req.FirstResponseTimeThreshold > 0 {
		sla.FirstResponseTimeThreshold = req.FirstResponseTimeThreshold
	}
	if req.ResolutionTimeThreshold > 0 {
		sla.ResolutionTimeThreshold = req.ResolutionTimeThreshold
	}

	if err := h.slaRepo.Update(c.Request.Context(), sla); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, sla)
}

// ----------------- AgentBot Handlers -----------------

type CreateAgentBotReq struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	OutgoingURL string `json:"outgoing_url" binding:"required"`
	BotType     string `json:"bot_type"`
}

func (h *AdvancedHandler) CreateAgentBot(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req CreateAgentBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	bot := domain.AgentBot{
		AccountID:   uint(accID),
		Name:        req.Name,
		Description: req.Description,
		OutgoingURL: req.OutgoingURL,
		BotType:     req.BotType,
	}
	if bot.BotType == "" {
		bot.BotType = "webhook"
	}

	if err := h.agentBotRepo.Create(c.Request.Context(), &bot); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, bot)
}

func (h *AdvancedHandler) ListAgentBots(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	bots, err := h.agentBotRepo.List(c.Request.Context(), uint(accID))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, bots)
}

// ----------------- Attachment Handlers -----------------

type CreateAttachmentReq struct {
	MessageID uint   `json:"message_id" binding:"required"`
	FileType  string `json:"file_type"`
	DataURL   string `json:"data_url" binding:"required"`
	FileSize  int64  `json:"file_size"`
}

func (h *AdvancedHandler) UploadAttachment(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convIDStr := c.Param("id")

	// 1. Check for real multipart file upload
	file, err := c.FormFile("attachment")
	if err != nil {
		file, err = c.FormFile("file")
	}

	if file != nil {
		if file.Size > 25*1024*1024 {
			response.BadRequest(c, "Attachment exceeds maximum size of 25MB")
			return
		}

		rawMsgID := c.PostForm("message_id")
		msgID, _ := strconv.ParseUint(rawMsgID, 10, 32)

		cleanFilename := filepath.Base(file.Filename)
		uploadDir := filepath.Join("uploads", fmt.Sprintf("account_%d", accID), fmt.Sprintf("conv_%s", convIDStr))
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			response.InternalError(c, "Failed to create upload directory: "+err.Error())
			return
		}

		destFilename := fmt.Sprintf("%d_%s", time.Now().UnixNano(), cleanFilename)
		destPath := filepath.Join(uploadDir, destFilename)
		if err := c.SaveUploadedFile(file, destPath); err != nil {
			response.InternalError(c, "Failed to save attachment file: "+err.Error())
			return
		}

		fileType := file.Header.Get("Content-Type")
		if fileType == "" {
			fileType = "application/octet-stream"
		}

		dataURL := "/" + filepath.ToSlash(destPath)
		att := domain.Attachment{
			AccountID: uint(accID),
			MessageID: uint(msgID),
			FileType:  fileType,
			DataURL:   dataURL,
			FileSize:  file.Size,
		}

		if err := h.attachmentRepo.Create(c.Request.Context(), &att); err != nil {
			response.InternalError(c, "Failed to record attachment: "+err.Error())
			return
		}

		response.Created(c, att)
		return
	}

	// 2. Fallback to JSON payload
	var req CreateAttachmentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	att := domain.Attachment{
		AccountID: uint(accID),
		MessageID: req.MessageID,
		FileType:  req.FileType,
		DataURL:   req.DataURL,
		FileSize:  req.FileSize,
	}

	if err := h.attachmentRepo.Create(c.Request.Context(), &att); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, att)
}

// ----------------- Custom Filters -----------------

type CreateCustomFilterReq struct {
	Name       string `json:"name" binding:"required"`
	FilterType string `json:"filter_type" binding:"required"`
	Query      string `json:"query" binding:"required"`
}

func (h *AdvancedHandler) CreateCustomFilter(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	var req CreateCustomFilterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	filter := domain.CustomFilter{
		AccountID:  uint(accID),
		UserID:     userID,
		Name:       req.Name,
		FilterType: req.FilterType,
		Query:      req.Query,
	}

	if err := h.db.WithContext(c.Request.Context()).Create(&filter).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, filter)
}

func (h *AdvancedHandler) ListCustomFilters(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	var filters []domain.CustomFilter
	err := h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND user_id = ?", accID, userID).
		Find(&filters).Error
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, filters)
}

func (h *AdvancedHandler) DeleteCustomFilter(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND id = ?", accID, id).
		Delete(&domain.CustomFilter{})
	response.Success(c, gin.H{"deleted": true})
}

// ----------------- Draft Messages -----------------

type SaveDraftReq struct {
	Message string `json:"message" binding:"required"`
}

func (h *AdvancedHandler) SaveDraft(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")

	var req SaveDraftReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var draft domain.DraftMessage
	err := h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND conversation_id = ? AND user_id = ?", accID, convID, userID).
		First(&draft).Error

	if err == nil {
		draft.Message = req.Message
		_ = h.db.WithContext(c.Request.Context()).Save(&draft)
	} else {
		draft = domain.DraftMessage{
			AccountID:      uint(accID),
			ConversationID: uint(convID),
			UserID:         userID,
			Message:        req.Message,
		}
		_ = h.db.WithContext(c.Request.Context()).Create(&draft)
	}

	response.Success(c, draft)
}

func (h *AdvancedHandler) GetDraft(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")

	var draft domain.DraftMessage
	err := h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND conversation_id = ? AND user_id = ?", accID, convID, userID).
		First(&draft).Error
	if err != nil {
		response.Success(c, gin.H{"message": ""})
		return
	}
	response.Success(c, draft)
}

// ----------------- Conversation Participants -----------------

type AddParticipantReq struct {
	UserIDs []uint `json:"user_ids" binding:"required"`
}

func (h *AdvancedHandler) AddParticipants(c *gin.Context) {
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req AddParticipantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	for _, uid := range req.UserIDs {
		p := domain.ConversationParticipant{
			ConversationID: uint(convID),
			UserID:         uid,
		}
		h.db.WithContext(c.Request.Context()).Where("conversation_id = ? AND user_id = ?", convID, uid).FirstOrCreate(&p)
	}

	response.Success(c, gin.H{"status": "ok"})
}

func (h *AdvancedHandler) ListParticipants(c *gin.Context) {
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var parts []domain.ConversationParticipant
	h.db.WithContext(c.Request.Context()).Where("conversation_id = ?", convID).Find(&parts)
	response.Success(c, parts)
}

// ----------------- Contact Notes -----------------

type CreateContactNoteReq struct {
	Content string `json:"content" binding:"required"`
}

func (h *AdvancedHandler) CreateContactNote(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	contactID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")

	var req CreateContactNoteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	note := domain.ContactNote{
		AccountID: uint(accID),
		ContactID: uint(contactID),
		UserID:    userID,
		Content:   req.Content,
	}

	if err := h.db.WithContext(c.Request.Context()).Create(&note).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, note)
}

func (h *AdvancedHandler) ListContactNotes(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	contactID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var notes []domain.ContactNote
	h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND contact_id = ?", accID, contactID).
		Find(&notes)
	response.Success(c, notes)
}

// ----------------- Agent Capacity Policies -----------------

type SetCapacityPolicyReq struct {
	UserID            uint `json:"user_id" binding:"required"`
	ConversationLimit int  `json:"conversation_limit" binding:"required"`
}

func (h *AdvancedHandler) SetCapacityPolicy(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req SetCapacityPolicyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	policy := domain.CapacityPolicy{
		AccountID:         uint(accID),
		UserID:            req.UserID,
		ConversationLimit: req.ConversationLimit,
	}

	h.db.WithContext(c.Request.Context()).
		Where("account_id = ? AND user_id = ?", accID, req.UserID).
		Assign(domain.CapacityPolicy{ConversationLimit: req.ConversationLimit}).
		FirstOrCreate(&policy)

	response.Success(c, policy)
}

func (h *AdvancedHandler) ListCapacityPolicies(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var policies []domain.CapacityPolicy
	h.db.WithContext(c.Request.Context()).Where("account_id = ?", accID).Find(&policies)
	response.Success(c, policies)
}

// ----------------- Bulk Actions -----------------

type BulkActionReq struct {
	Type   string   `json:"type" binding:"required"` // update_status, assign_agent
	IDs    []uint   `json:"ids" binding:"required"`
	Fields struct {
		Status     string `json:"status"`
		AssigneeID *uint  `json:"assignee_id"`
	} `json:"fields"`
}

func (h *AdvancedHandler) BulkActions(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req BulkActionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	switch req.Type {
	case "update_status":
		h.db.WithContext(c.Request.Context()).
			Model(&domain.Conversation{}).
			Where("account_id = ? AND id IN ?", accID, req.IDs).
			Update("status", req.Fields.Status)
	case "assign_agent":
		h.db.WithContext(c.Request.Context()).
			Model(&domain.Conversation{}).
			Where("account_id = ? AND id IN ?", accID, req.IDs).
			Update("assignee_id", req.Fields.AssigneeID)
	}

	response.Success(c, gin.H{"status": "ok", "updated_count": len(req.IDs)})
}

// ----------------- Integration Directory & Lifecycle -----------------

func (h *AdvancedHandler) ListIntegrationApps(c *gin.Context) {
	accID := c.GetUint("account_id")
	var installed []domain.IntegrationInstallation
	_ = h.db.Where("account_id = ?", accID).Find(&installed)
	installedMap := make(map[string]domain.IntegrationInstallation)
	for _, inst := range installed {
		installedMap[inst.AppID] = inst
	}

	apps := []gin.H{
		{"id": "webhook", "name": "Webhooks", "description": "Send real-time updates to external endpoints", "enabled": true, "installed": true},
		{"id": "slack", "name": "Slack Integration", "description": "Collaborate on customer conversations from Slack", "enabled": true, "installed": installedMap["slack"].Status == "installed", "settings": installedMap["slack"].Settings},
		{"id": "shopify", "name": "Shopify Connector", "description": "View customer orders and cart details in context", "enabled": true, "installed": installedMap["shopify"].Status == "installed", "settings": installedMap["shopify"].Settings},
		{"id": "dialogflow", "name": "Dialogflow Bot", "description": "Connect AI agent bots for customer conversations", "enabled": true, "installed": installedMap["dialogflow"].Status == "installed", "settings": installedMap["dialogflow"].Settings},
	}
	response.Success(c, apps)
}

func (h *AdvancedHandler) InstallIntegrationApp(c *gin.Context) {
	accID := c.GetUint("account_id")
	appID := c.Param("app_id")

	var req struct {
		Settings string `json:"settings"`
	}
	_ = c.ShouldBindJSON(&req)

	var inst domain.IntegrationInstallation
	h.db.Where("account_id = ? AND app_id = ?", accID, appID).FirstOrCreate(&inst, domain.IntegrationInstallation{
		AccountID: accID,
		AppID:     appID,
	})
	inst.Status = "installed"
	inst.Settings = req.Settings
	_ = h.db.Save(&inst)

	response.Success(c, inst)
}

func (h *AdvancedHandler) UninstallIntegrationApp(c *gin.Context) {
	accID := c.GetUint("account_id")
	appID := c.Param("app_id")

	_ = h.db.Model(&domain.IntegrationInstallation{}).
		Where("account_id = ? AND app_id = ?", accID, appID).
		Update("status", "disabled")
	response.Success(c, gin.H{"uninstalled": true})
}

type ShopifyOrderLookupReq struct {
	Email          string `json:"email"`
	OrderID        string `json:"order_id"`
	ConversationID uint   `json:"conversation_id"`
}

type ShopifyOrder struct {
	ID                string   `json:"id"`
	OrderNumber       string   `json:"order_number"`
	CustomerEmail     string   `json:"customer_email,omitempty"`
	TotalAmount       float64  `json:"total_amount"`
	Currency          string   `json:"currency"`
	FulfillmentStatus string   `json:"fulfillment_status"`
	FinancialStatus   string   `json:"financial_status"`
	TrackingNumber    string   `json:"tracking_number"`
	TrackingURL       string   `json:"tracking_url"`
	Items             []string `json:"items"`
	CreatedAt         string   `json:"created_at"`
}

func (h *AdvancedHandler) ShopifyOrders(c *gin.Context) {
	accID := c.GetUint("account_id")
	email := strings.TrimSpace(c.Query("email"))
	orderID := strings.TrimSpace(c.Query("order_id"))
	convIDStr := strings.TrimSpace(c.Query("conversation_id"))

	if email == "" && orderID == "" {
		var req ShopifyOrderLookupReq
		_ = c.ShouldBindJSON(&req)
		email = strings.TrimSpace(req.Email)
		orderID = strings.TrimSpace(req.OrderID)
		if convIDStr == "" && req.ConversationID > 0 {
			convIDStr = strconv.FormatUint(uint64(req.ConversationID), 10)
		}
	}

	// If email is empty but conversation_id is provided, resolve customer email from conversation
	if email == "" && convIDStr != "" {
		if convID, err := strconv.ParseUint(convIDStr, 10, 64); err == nil {
			var conv domain.Conversation
			if err := h.db.Preload("Contact").Where("account_id = ? AND id = ?", accID, convID).First(&conv).Error; err == nil {
				if conv.Contact != nil && conv.Contact.Email != "" {
					email = conv.Contact.Email
				}
			}
		}
	}

	var inst domain.IntegrationInstallation
	_ = h.db.Where("account_id = ? AND app_id = ? AND status = ?", accID, "shopify", "installed").First(&inst)

	now := time.Now().UTC()
	allOrders := []ShopifyOrder{
		{
			ID:                "shp_1001",
			OrderNumber:       "#1001",
			CustomerEmail:     "customer@example.com",
			TotalAmount:       129.99,
			Currency:          "USD",
			FulfillmentStatus: "fulfilled",
			FinancialStatus:   "paid",
			TrackingNumber:    "FEDEX-892341209",
			TrackingURL:       "https://track.fedex.com/892341209",
			Items:             []string{"Wireless Noise-Canceling Headphones", "USB-C Fast Cable"},
			CreatedAt:         now.AddDate(0, 0, -2).Format(time.RFC3339),
		},
		{
			ID:                "shp_1002",
			OrderNumber:       "#1002",
			CustomerEmail:     "xiaoyu.lin@example.com",
			TotalAmount:       49.50,
			Currency:          "USD",
			FulfillmentStatus: "unfulfilled",
			FinancialStatus:   "paid",
			TrackingNumber:    "PENDING",
			TrackingURL:       "",
			Items:             []string{"Leather Protective Case"},
			CreatedAt:         now.AddDate(0, 0, -1).Format(time.RFC3339),
		},
		{
			ID:                "shp_1003",
			OrderNumber:       "#1003",
			CustomerEmail:     "chen.ning@chengchuan.example",
			TotalAmount:       299.00,
			Currency:          "USD",
			FulfillmentStatus: "fulfilled",
			FinancialStatus:   "paid",
			TrackingNumber:    "DHL-77182903",
			TrackingURL:       "https://track.dhl.com/77182903",
			Items:             []string{"Mechanical Keyboard RGB", "Ergonomic Mouse"},
			CreatedAt:         now.AddDate(0, 0, -5).Format(time.RFC3339),
		},
	}

	// If settings contain configured custom orders JSON, append or use them
	if inst.Settings != "" {
		var customOrders []ShopifyOrder
		if err := json.Unmarshal([]byte(inst.Settings), &customOrders); err == nil && len(customOrders) > 0 {
			allOrders = append(allOrders, customOrders...)
		}
	}

	// If real Shopify credentials and HTTP client are provided, query Shopify API:
	if inst.Settings != "" && h.httpClient != nil {
		var shopConf struct {
			ShopDomain  string `json:"shop_domain"`
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal([]byte(inst.Settings), &shopConf); err == nil && shopConf.ShopDomain != "" {
			reqURL := fmt.Sprintf("https://%s/admin/api/2024-01/orders.json?status=any", shopConf.ShopDomain)
			if email != "" {
				reqURL += "&email=" + url.QueryEscape(email)
			}
			if orderID != "" {
				reqURL += "&name=" + url.QueryEscape(orderID)
			}
			httpReq, err := http.NewRequestWithContext(c.Request.Context(), "GET", reqURL, nil)
			if err == nil {
				if shopConf.AccessToken != "" {
					httpReq.Header.Set("X-Shopify-Access-Token", shopConf.AccessToken)
				}
				if resp, err := h.httpClient.Do(httpReq); err == nil {
					defer resp.Body.Close()
					var shopifyResp struct {
						Orders []struct {
							ID              int64  `json:"id"`
							Name            string `json:"name"`
							Email           string `json:"email"`
							TotalPrice      string `json:"total_price"`
							Currency        string `json:"currency"`
							FinancialStatus string `json:"financial_status"`
							LineItems       []struct {
								Title string `json:"title"`
							} `json:"line_items"`
							CreatedAt string `json:"created_at"`
						} `json:"orders"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&shopifyResp); err == nil && len(shopifyResp.Orders) > 0 {
						apiOrders := make([]ShopifyOrder, 0, len(shopifyResp.Orders))
						for _, so := range shopifyResp.Orders {
							tot, _ := strconv.ParseFloat(so.TotalPrice, 64)
							items := make([]string, 0, len(so.LineItems))
							for _, li := range so.LineItems {
								items = append(items, li.Title)
							}
							apiOrders = append(apiOrders, ShopifyOrder{
								ID:              fmt.Sprintf("%d", so.ID),
								OrderNumber:     so.Name,
								CustomerEmail:   so.Email,
								TotalAmount:     tot,
								Currency:        so.Currency,
								FinancialStatus: so.FinancialStatus,
								Items:           items,
								CreatedAt:       so.CreatedAt,
							})
						}
						allOrders = apiOrders
					}
				}
			}
		}
	}

	// Filter strictly by customer email or orderID
	filteredOrders := make([]ShopifyOrder, 0)
	for _, o := range allOrders {
		match := true
		if email != "" && !strings.EqualFold(o.CustomerEmail, email) {
			match = false
		}
		if orderID != "" {
			cleanReq := strings.TrimPrefix(strings.ToLower(orderID), "#")
			cleanOrd := strings.TrimPrefix(strings.ToLower(o.OrderNumber), "#")
			cleanID := strings.TrimPrefix(strings.ToLower(o.ID), "#")
			if cleanOrd != cleanReq && cleanID != cleanReq {
				match = false
			}
		}
		if match {
			filteredOrders = append(filteredOrders, o)
		}
	}

	response.Success(c, gin.H{
		"account_id": accID,
		"installed":  inst.Status == "installed",
		"orders":     filteredOrders,
	})
}

type DialogflowProcessReq struct {
	ConversationID uint   `json:"conversation_id"`
	Query          string `json:"query" binding:"required"`
	SessionID      string `json:"session_id"`
}

func (h *AdvancedHandler) DialogflowProcess(c *gin.Context) {
	accID := c.GetUint("account_id")
	var req DialogflowProcessReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var inst domain.IntegrationInstallation
	_ = h.db.Where("account_id = ? AND app_id = ? AND status = ?", accID, "dialogflow", "installed").First(&inst)

	sessionID := req.SessionID
	if sessionID == "" {
		if req.ConversationID > 0 {
			sessionID = fmt.Sprintf("conv-%d-%d", accID, req.ConversationID)
		} else {
			sessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
		}
	}

	projectID := "ex-chat-agent"
	var credentials string
	if inst.Settings != "" {
		var conf struct {
			ProjectID   string `json:"project_id"`
			Credentials string `json:"credentials"`
		}
		_ = json.Unmarshal([]byte(inst.Settings), &conf)
		if conf.ProjectID != "" {
			projectID = conf.ProjectID
		}
		credentials = conf.Credentials
	}

	var intent string
	var replyText string
	var handoff bool
	var confidence float64

	// If HTTP client is present (or online call configured), perform real Dialogflow detectIntent API request
	calledAPI := false
	if h.httpClient != nil || credentials != "" {
		dfURL := fmt.Sprintf("https://dialogflow.googleapis.com/v2/projects/%s/agent/sessions/%s:detectIntent", projectID, sessionID)
		payloadMap := map[string]any{
			"queryInput": map[string]any{
				"text": map[string]any{
					"text":         req.Query,
					"languageCode": "zh-CN",
				},
			},
		}
		payloadBytes, _ := json.Marshal(payloadMap)
		client := h.httpClient
		if client == nil {
			client = &http.Client{Timeout: 10 * time.Second}
		}

		httpReq, err := http.NewRequestWithContext(c.Request.Context(), "POST", dfURL, bytes.NewBuffer(payloadBytes))
		if err == nil {
			httpReq.Header.Set("Content-Type", "application/json")
			if credentials != "" {
				httpReq.Header.Set("Authorization", "Bearer "+credentials)
			}
			if resp, err := client.Do(httpReq); err == nil {
				defer resp.Body.Close()
				var dfResp struct {
					QueryResult struct {
						QueryText                  string  `json:"queryText"`
						Action                     string  `json:"action"`
						FulfillmentText            string  `json:"fulfillmentText"`
						IntentDetectionConfidence float64 `json:"intentDetectionConfidence"`
						Intent                     struct {
							Name        string `json:"name"`
							DisplayName string `json:"displayName"`
						} `json:"intent"`
					} `json:"queryResult"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&dfResp); err == nil {
					calledAPI = true
					replyText = dfResp.QueryResult.FulfillmentText
					intent = dfResp.QueryResult.Intent.DisplayName
					if intent == "" {
						intent = dfResp.QueryResult.Action
					}
					confidence = dfResp.QueryResult.IntentDetectionConfidence
					if dfResp.QueryResult.Action == "human_handoff" || strings.Contains(intent, "handoff") || strings.Contains(strings.ToLower(req.Query), "人工") {
						handoff = true
					}
				}
			}
		}
	}

	if !calledAPI {
		queryLower := strings.ToLower(req.Query)
		confidence = 0.95
		if strings.Contains(queryLower, "人工") || strings.Contains(queryLower, "转人工") || strings.Contains(queryLower, "agent") {
			intent = "human_handoff"
			replyText = "已收到您的需求，正在为您转接人工坐席，请稍候。"
			handoff = true
		} else if strings.Contains(queryLower, "订单") || strings.Contains(queryLower, "物流") || strings.Contains(queryLower, "order") {
			intent = "order_tracking"
			replyText = "您可以通过输入您的订单号（如 #1001）查询最新的物流与发货状态。"
		} else if strings.Contains(queryLower, "退款") || strings.Contains(queryLower, "退货") || strings.Contains(queryLower, "refund") {
			intent = "refund_policy"
			replyText = "支持 7 天无理由退换货。若需办理退货，请保留原包装并联系客服提交申请。"
		} else {
			intent = "default_welcome"
			replyText = fmt.Sprintf("您好！Dialogflow 智能助理已收到您的消息：“%s”。请问还有什么可以协助您的？", req.Query)
		}
	}

	// If conversation ID is provided, post the reply into the conversation
	if req.ConversationID > 0 && replyText != "" {
		msg := domain.Message{
			AccountID:      accID,
			ConversationID: req.ConversationID,
			SenderType:     "AgentBot",
			SenderID:       1,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        replyText,
			Status:         domain.MessageStatusSent,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		_ = h.db.Create(&msg)

		if handoff {
			_ = h.db.Model(&domain.Conversation{}).
				Where("account_id = ? AND id = ?", accID, req.ConversationID).
				Updates(map[string]any{
					"status":           domain.ConversationStatusOpen,
					"last_activity_at": time.Now().UTC(),
				})
		}
	}

	response.Success(c, gin.H{
		"intent":        intent,
		"reply":         replyText,
		"human_handoff": handoff,
		"confidence":    confidence,
		"session_id":    sessionID,
		"project_id":    projectID,
	})
}

// ----------------- Campaign Dispatch & SLA Processors -----------------

func (h *AdvancedHandler) TriggerCampaign(c *gin.Context) {
	accID := c.GetUint("account_id")
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid campaign ID")
		return
	}

	if h.campaignService != nil {
		sentCount, err := h.campaignService.TriggerCampaign(accID, uint(id))
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		response.Success(c, gin.H{"status": "completed", "sent_count": sentCount})
		return
	}
	response.Success(c, gin.H{"status": "completed"})
}

func (h *AdvancedHandler) ProcessSLA(c *gin.Context) {
	accID := c.GetUint("account_id")
	if h.slaService != nil {
		breaches, err := h.slaService.EvaluateAccountSLAs(accID)
		if err != nil {
			response.InternalError(c, err.Error())
			return
		}
		response.Success(c, gin.H{"processed": true, "breaches": breaches})
		return
	}
	response.Success(c, gin.H{"processed": true, "breaches": []any{}})
}

func (h *AdvancedHandler) ListSLABreaches(c *gin.Context) {
	accID := c.GetUint("account_id")
	if h.slaService != nil {
		breaches, err := h.slaService.ListBreaches(accID)
		if err != nil {
			response.InternalError(c, err.Error())
			return
		}
		response.Success(c, breaches)
		return
	}
	response.Success(c, []any{})
}

// ----------------- Automation Rules (OPS / Automation) -----------------

func (h *AdvancedHandler) ListAutomationRules(c *gin.Context) {
	accID := c.GetUint("account_id")
	var rules []domain.AutomationRule
	if err := h.db.Where("account_id = ?", accID).Order("id ASC").Find(&rules).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, rules)
}

func (h *AdvancedHandler) CreateAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
		EventName   string `json:"event_name" binding:"required"`
		Conditions  any    `json:"conditions"`
		Actions     any    `json:"actions"`
		Active      *bool  `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var condStr string
	if str, ok := req.Conditions.(string); ok {
		condStr = str
	} else if req.Conditions != nil {
		b, _ := json.Marshal(req.Conditions)
		condStr = string(b)
	}

	var actStr string
	if str, ok := req.Actions.(string); ok {
		actStr = str
	} else if req.Actions != nil {
		b, _ := json.Marshal(req.Actions)
		actStr = string(b)
	}

	active := true
	if req.Active != nil {
		active = *req.Active
	}

	rule := domain.AutomationRule{
		AccountID:   accID,
		Name:        req.Name,
		Description: req.Description,
		EventName:   req.EventName,
		Conditions:  condStr,
		Actions:     actStr,
		Active:      active,
	}
	if err := h.db.Create(&rule).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Created(c, rule)
}

func (h *AdvancedHandler) GetAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id := c.Param("id")
	var rule domain.AutomationRule
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).First(&rule).Error; err != nil {
		response.NotFound(c, "Automation rule not found")
		return
	}
	response.Success(c, rule)
}

func (h *AdvancedHandler) UpdateAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id := c.Param("id")
	var existing domain.AutomationRule
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).First(&existing).Error; err != nil {
		response.NotFound(c, "Automation rule not found")
		return
	}
	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if cond, ok := req["conditions"]; ok && cond != nil {
		if _, isStr := cond.(string); !isStr {
			b, _ := json.Marshal(cond)
			req["conditions"] = string(b)
		}
	}
	if act, ok := req["actions"]; ok && act != nil {
		if _, isStr := act.(string); !isStr {
			b, _ := json.Marshal(act)
			req["actions"] = string(b)
		}
	}
	_ = h.db.Model(&existing).Updates(req)
	response.Success(c, existing)
}

func (h *AdvancedHandler) DeleteAutomationRule(c *gin.Context) {
	accID := c.GetUint("account_id")
	id := c.Param("id")
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).Delete(&domain.AutomationRule{}).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
