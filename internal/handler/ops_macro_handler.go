package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	status := c.Query("status")
	includeSnoozed := c.Query("include_snoozed") == "true"

	notifications, err := h.notificationRepo.ListWithFilter(c.Request.Context(), uint(accID), userID, status, includeSnoozed)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, notifications)
}

func (h *MacroNotificationHandler) GetUnreadCount(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	unreadCount, totalCount, snoozedCount, err := h.notificationRepo.GetUnreadCount(c.Request.Context(), uint(accID), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{
		"unread_count":  unreadCount,
		"count":         totalCount,
		"snoozed_count": snoozedCount,
	})
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

func (h *MacroNotificationHandler) MarkNotificationRead(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	notifID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	if err := h.notificationRepo.MarkRead(c.Request.Context(), uint(accID), userID, uint(notifID)); err != nil {
		response.Error(c, http.StatusNotFound, "Notification not found")
		return
	}
	notif, _ := h.notificationRepo.FindByID(c.Request.Context(), uint(accID), userID, uint(notifID))
	response.Success(c, notif)
}

func (h *MacroNotificationHandler) MarkNotificationUnread(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	notifID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	if err := h.notificationRepo.MarkUnread(c.Request.Context(), uint(accID), userID, uint(notifID)); err != nil {
		response.Error(c, http.StatusNotFound, "Notification not found")
		return
	}
	notif, _ := h.notificationRepo.FindByID(c.Request.Context(), uint(accID), userID, uint(notifID))
	response.Success(c, notif)
}

type SnoozeNotificationRequest struct {
	SnoozedUntil *time.Time `json:"snoozed_until"`
	Duration     string     `json:"duration"` // e.g. "20m", "1h", "3h", "tomorrow", "24h"
}

func (h *MacroNotificationHandler) SnoozeNotification(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	notifID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var req SnoozeNotificationRequest
	_ = c.ShouldBindJSON(&req)

	targetTime := time.Now().UTC()
	if req.SnoozedUntil != nil && req.SnoozedUntil.After(targetTime) {
		targetTime = *req.SnoozedUntil
	} else {
		switch strings.ToLower(strings.TrimSpace(req.Duration)) {
		case "20m", "20min", "20_minutes":
			targetTime = targetTime.Add(20 * time.Minute)
		case "3h", "3_hours":
			targetTime = targetTime.Add(3 * time.Hour)
		case "tomorrow", "24h", "1d":
			targetTime = targetTime.Add(24 * time.Hour)
		default:
			targetTime = targetTime.Add(1 * time.Hour)
		}
	}

	if err := h.notificationRepo.Snooze(c.Request.Context(), uint(accID), userID, uint(notifID), &targetTime); err != nil {
		response.Error(c, http.StatusNotFound, "Notification not found")
		return
	}
	notif, _ := h.notificationRepo.FindByID(c.Request.Context(), uint(accID), userID, uint(notifID))
	response.Success(c, notif)
}

func (h *MacroNotificationHandler) UnsnoozeNotification(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	notifID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	if err := h.notificationRepo.Unsnooze(c.Request.Context(), uint(accID), userID, uint(notifID)); err != nil {
		response.Error(c, http.StatusNotFound, "Notification not found")
		return
	}
	notif, _ := h.notificationRepo.FindByID(c.Request.Context(), uint(accID), userID, uint(notifID))
	response.Success(c, notif)
}

type UpdateNotificationRequest struct {
	Read         *bool      `json:"read"`
	ReadAt       *time.Time `json:"read_at"`
	SnoozedUntil *time.Time `json:"snoozed_until"`
}

func (h *MacroNotificationHandler) UpdateNotification(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	notifID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var req UpdateNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload")
		return
	}

	if req.Read != nil {
		if *req.Read {
			_ = h.notificationRepo.MarkRead(c.Request.Context(), uint(accID), userID, uint(notifID))
		} else {
			_ = h.notificationRepo.MarkUnread(c.Request.Context(), uint(accID), userID, uint(notifID))
		}
	} else if req.ReadAt != nil {
		_ = h.notificationRepo.MarkRead(c.Request.Context(), uint(accID), userID, uint(notifID))
	}

	if req.SnoozedUntil != nil {
		_ = h.notificationRepo.Snooze(c.Request.Context(), uint(accID), userID, uint(notifID), req.SnoozedUntil)
	}

	notif, err := h.notificationRepo.FindByID(c.Request.Context(), uint(accID), userID, uint(notifID))
	if err != nil {
		response.Error(c, http.StatusNotFound, "Notification not found")
		return
	}
	response.Success(c, notif)
}

func (h *MacroNotificationHandler) DeleteNotification(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	notifID, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	if err := h.notificationRepo.Delete(c.Request.Context(), uint(accID), userID, uint(notifID)); err != nil {
		response.Error(c, http.StatusNotFound, "Notification not found")
		return
	}
	response.Success(c, gin.H{"deleted": true, "id": notifID})
}

type BatchDeleteNotificationRequest struct {
	IDs      []uint `json:"ids"`
	OnlyRead bool   `json:"only_read"`
}

func (h *MacroNotificationHandler) BatchDeleteNotifications(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	var req BatchDeleteNotificationRequest
	_ = c.ShouldBindJSON(&req)

	deletedCount, err := h.notificationRepo.BatchDelete(c.Request.Context(), uint(accID), userID, req.IDs, req.OnlyRead)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{
		"deleted_count": deletedCount,
		"status":        "ok",
	})
}

// ----------------- Notification Settings / Preferences -----------------

func (h *MacroNotificationHandler) GetNotificationSettings(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	settings, err := h.notificationRepo.GetNotificationSetting(c.Request.Context(), uint(accID), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, settings)
}

type UpdateNotificationSettingsRequest struct {
	SelectedEmailFlags any     `json:"selected_email_flags"`
	SelectedPushFlags  any     `json:"selected_push_flags"`
	SelectedInAppFlags any     `json:"selected_in_app_flags"`
	Muted              *bool   `json:"muted"`
	QuietHoursEnabled  *bool   `json:"quiet_hours_enabled"`
	QuietHoursStart    *string `json:"quiet_hours_start"`
	QuietHoursEnd      *string `json:"quiet_hours_end"`
}

func formatNotificationFlags(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	default:
		b, err := json.Marshal(v)
		if err == nil {
			return string(b)
		}
		return "[]"
	}
}

func (h *MacroNotificationHandler) UpdateNotificationSettings(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	var req UpdateNotificationSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid request payload")
		return
	}

	current, err := h.notificationRepo.GetNotificationSetting(c.Request.Context(), uint(accID), userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	if req.SelectedEmailFlags != nil {
		current.SelectedEmailFlags = formatNotificationFlags(req.SelectedEmailFlags)
	}
	if req.SelectedPushFlags != nil {
		current.SelectedPushFlags = formatNotificationFlags(req.SelectedPushFlags)
	}
	if req.SelectedInAppFlags != nil {
		current.SelectedInAppFlags = formatNotificationFlags(req.SelectedInAppFlags)
	}
	if req.Muted != nil {
		current.Muted = *req.Muted
	}
	if req.QuietHoursEnabled != nil {
		current.QuietHoursEnabled = *req.QuietHoursEnabled
	}
	if req.QuietHoursStart != nil {
		current.QuietHoursStart = *req.QuietHoursStart
	}
	if req.QuietHoursEnd != nil {
		current.QuietHoursEnd = *req.QuietHoursEnd
	}

	if err := h.notificationRepo.UpsertNotificationSetting(c.Request.Context(), current); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, current)
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
