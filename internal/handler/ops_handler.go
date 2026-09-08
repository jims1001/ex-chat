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

type OpsHandler struct {
	labelRepo  *repository.LabelRepository
	cannedRepo *repository.CannedResponseRepository
	convRepo   *repository.ConversationRepository
}

func NewOpsHandler(
	labelRepo *repository.LabelRepository,
	cannedRepo *repository.CannedResponseRepository,
	convRepo *repository.ConversationRepository,
) *OpsHandler {
	return &OpsHandler{
		labelRepo:  labelRepo,
		cannedRepo: cannedRepo,
		convRepo:   convRepo,
	}
}

type CreateCannedResponseRequest struct {
	ShortCode string `json:"short_code" binding:"required"`
	Content   string `json:"content" binding:"required"`
}

type CreateLabelRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

type AttachLabelsRequest struct {
	LabelIDs []uint `json:"label_ids" binding:"required"`
}

// ---------------- Canned Responses ----------------

func (h *OpsHandler) ListCannedResponses(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	search := strings.TrimSpace(c.Query("q"))
	if search == "" {
		search = strings.TrimSpace(c.Query("search"))
	}
	list, err := h.cannedRepo.List(accountID, search)
	if err != nil {
		response.InternalError(c, "Failed to list canned responses")
		return
	}

	response.Success(c, list)
}

func (h *OpsHandler) CreateCannedResponse(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateCannedResponseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	cr := domain.CannedResponse{
		AccountID: accountID,
		ShortCode: strings.TrimSpace(req.ShortCode),
		Content:   req.Content,
	}

	if err := h.cannedRepo.Create(&cr); err != nil {
		response.InternalError(c, "Failed to create canned response")
		return
	}

	response.Created(c, cr)
}

func (h *OpsHandler) UpdateCannedResponse(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	cr, err := h.cannedRepo.FindByID(accountID, uint(id))
	if err != nil || cr == nil {
		response.NotFound(c, "Canned response not found")
		return
	}

	var req CreateCannedResponseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	cr.ShortCode = strings.TrimSpace(req.ShortCode)
	cr.Content = req.Content

	if err := h.cannedRepo.Update(cr); err != nil {
		response.InternalError(c, "Failed to update canned response")
		return
	}

	response.Success(c, cr)
}

func (h *OpsHandler) DeleteCannedResponse(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	if err := h.cannedRepo.Delete(accountID, uint(id)); err != nil {
		response.InternalError(c, "Failed to delete canned response")
		return
	}

	response.Success(c, gin.H{"deleted": true})
}

// ---------------- Labels ----------------

func (h *OpsHandler) ListLabels(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	labels, err := h.labelRepo.List(accountID)
	if err != nil {
		response.InternalError(c, "Failed to list labels")
		return
	}

	response.Success(c, labels)
}

func (h *OpsHandler) CreateLabel(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateLabelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	color := req.Color
	if color == "" {
		color = "#1f93ff"
	}

	label := domain.Label{
		AccountID:   accountID,
		Title:       strings.TrimSpace(req.Title),
		Description: req.Description,
		Color:       color,
	}

	if err := h.labelRepo.Create(&label); err != nil {
		response.InternalError(c, "Failed to create label")
		return
	}

	response.Created(c, label)
}

func (h *OpsHandler) UpdateLabel(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	label, err := h.labelRepo.FindByID(accountID, uint(id))
	if err != nil || label == nil {
		response.NotFound(c, "Label not found")
		return
	}

	var req CreateLabelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Title != "" {
		label.Title = strings.TrimSpace(req.Title)
	}
	if req.Description != "" {
		label.Description = req.Description
	}
	if req.Color != "" {
		label.Color = req.Color
	}

	if err := h.labelRepo.Update(label); err != nil {
		response.InternalError(c, "Failed to update label")
		return
	}

	response.Success(c, label)
}

func (h *OpsHandler) DeleteLabel(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid ID")
		return
	}

	if err := h.labelRepo.Delete(accountID, uint(id)); err != nil {
		response.InternalError(c, "Failed to delete label")
		return
	}

	response.Success(c, gin.H{"deleted": true})
}

func (h *OpsHandler) AttachConversationLabels(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

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

	var req AttachLabelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	for _, labelID := range req.LabelIDs {
		_ = h.labelRepo.AttachToConversation(conv.ID, labelID)
	}

	labels, _ := h.labelRepo.GetConversationLabels(conv.ID)
	response.Success(c, labels)
}

func (h *OpsHandler) GetConversationLabels(c *gin.Context) {
	convID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	labels, err := h.labelRepo.GetConversationLabels(uint(convID))
	if err != nil {
		response.InternalError(c, "Failed to get conversation labels")
		return
	}

	response.Success(c, labels)
}
