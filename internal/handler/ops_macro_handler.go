package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type MacroNotificationHandler struct {
	macroRepo        *repository.MacroRepository
	notificationRepo *repository.NotificationRepository
	csatRepo         *repository.CSATRepository
}

func NewMacroNotificationHandler(m *repository.MacroRepository, n *repository.NotificationRepository, csat *repository.CSATRepository) *MacroNotificationHandler {
	return &MacroNotificationHandler{
		macroRepo:        m,
		notificationRepo: n,
		csatRepo:         csat,
	}
}

type CreateMacroRequest struct {
	Name       string `json:"name" binding:"required"`
	Visibility string `json:"visibility"`
	Actions    any    `json:"actions" binding:"required"`
}

func (h *MacroNotificationHandler) CreateMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req CreateMacroRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var actionsStr string
	if s, ok := req.Actions.(string); ok {
		actionsStr = s
	} else if req.Actions != nil {
		b, _ := json.Marshal(req.Actions)
		actionsStr = string(b)
	}

	userID := c.GetUint("user_id")
	macro := domain.Macro{
		AccountID:  uint(accID),
		Name:       req.Name,
		Visibility: req.Visibility,
		CreatedBy:  userID,
		Actions:    actionsStr,
	}
	if macro.Visibility == "" {
		macro.Visibility = "personal"
	}

	if err := h.macroRepo.Create(c.Request.Context(), &macro); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, macro)
}

func (h *MacroNotificationHandler) ListMacros(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macros, err := h.macroRepo.List(c.Request.Context(), uint(accID))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, macros)
}

type ExecuteMacroRequest struct {
	ConversationIDs []uint `json:"conversation_ids"`
	ConversationID  uint   `json:"conversation_id"`
}

func (h *MacroNotificationHandler) ExecuteMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macroID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var req ExecuteMacroRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	convIDs := req.ConversationIDs
	if len(convIDs) == 0 && req.ConversationID > 0 {
		convIDs = []uint{req.ConversationID}
	}
	if len(convIDs) == 0 {
		response.BadRequest(c, "conversation_ids or conversation_id is required")
		return
	}

	res, err := h.macroRepo.Execute(c.Request.Context(), uint(accID), uint(macroID), convIDs)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, res)
}

func (h *MacroNotificationHandler) DeleteMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macroID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	if err := h.macroRepo.Delete(c.Request.Context(), uint(accID), uint(macroID)); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

type UpdateMacroRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	Actions    string `json:"actions"`
}

func (h *MacroNotificationHandler) UpdateMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macroID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	macro, err := h.macroRepo.GetByID(c.Request.Context(), uint(accID), uint(macroID))
	if err != nil || macro == nil {
		response.NotFound(c, "macro not found")
		return
	}

	var req UpdateMacroRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Name != "" {
		macro.Name = req.Name
	}
	if req.Visibility != "" {
		macro.Visibility = req.Visibility
	}
	if req.Actions != "" {
		macro.Actions = req.Actions
	}

	if err := h.macroRepo.Update(c.Request.Context(), macro); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, macro)
}

// ----------------- Notifications -----------------

func (h *MacroNotificationHandler) ListNotifications(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	notifications, err := h.notificationRepo.List(c.Request.Context(), uint(accID), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, notifications)
}

func (h *MacroNotificationHandler) MarkAllNotificationsRead(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	if err := h.notificationRepo.MarkAllRead(c.Request.Context(), uint(accID), userID); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{"status": "ok"})
}

// ----------------- CSAT -----------------

type SubmitCSATRequest struct {
	AccountID    uint   `json:"account_id" binding:"required"`
	Rating       int    `json:"rating" binding:"required,min=1,max=5"`
	FeedbackText string `json:"feedback_text"`
}

func (h *MacroNotificationHandler) SubmitCSAT(c *gin.Context) {
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req SubmitCSATRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	survey := domain.CSATSurvey{
		AccountID:      req.AccountID,
		ConversationID: uint(convID),
		Rating:         req.Rating,
		FeedbackText:   req.FeedbackText,
	}

	if err := h.csatRepo.Create(c.Request.Context(), &survey); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, survey)
}

func (h *MacroNotificationHandler) GetCSATReport(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	report, err := h.csatRepo.GetMetrics(c.Request.Context(), uint(accID))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, report)
}
