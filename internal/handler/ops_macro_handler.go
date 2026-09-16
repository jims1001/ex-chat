package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
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

var macroAllowedActions = map[string]bool{
	"send_message":          true,
	"send_reply":            true,
	"reply":                 true,
	"add_label":             true,
	"add_labels":            true,
	"assign_team":           true,
	"assign_agent":          true,
	"mute_conversation":     true,
	"change_status":         true,
	"remove_label":          true,
	"remove_labels":         true,
	"remove_assigned_agent": true,
	"remove_assigned_team":  true,
	"resolve_conversation":  true,
	"close_conversation":    true,
	"close":                 true,
	"resolve":               true,
	"open_conversation":     true,
	"open":                  true,
	"snooze_conversation":   true,
	"snooze":                true,
	"change_priority":       true,
	"send_email_transcript": true,
	"send_attachment":       true,
	"add_private_note":      true,
	"private_note":          true,
	"send_webhook_event":    true,
}

func validateMacroActions(rawActions any) error {
	var acts []struct {
		ActionName string `json:"action_name"`
		Name       string `json:"name"`
	}
	switch v := rawActions.(type) {
	case string:
		str := strings.TrimSpace(v)
		if str == "" || str == "[]" {
			return nil
		}
		if err := json.Unmarshal([]byte(str), &acts); err != nil {
			var wrapper struct {
				Actions []struct {
					ActionName string `json:"action_name"`
					Name       string `json:"name"`
				} `json:"actions"`
			}
			if err2 := json.Unmarshal([]byte(str), &wrapper); err2 != nil {
				return fmt.Errorf("invalid macro actions format")
			}
			acts = wrapper.Actions
		}
	case []any:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &acts)
	default:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &acts)
	}

	for _, act := range acts {
		name := act.ActionName
		if name == "" {
			name = act.Name
		}
		if name == "" || !macroAllowedActions[name] {
			return fmt.Errorf("Macro execution action '%s' is not supported", name)
		}
	}
	return nil
}

func isUserAdmin(c *gin.Context) bool {
	if rawMembership, exists := c.Get("account_membership"); exists {
		if membership, ok := rawMembership.(*domain.AccountUser); ok && membership != nil {
			return membership.Role == domain.RoleAdministrator
		}
	}
	return false
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

	if err := validateMacroActions(req.Actions); err != nil {
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
	isAdmin := isUserAdmin(c)
	if macro.Visibility == "" || (!isAdmin && macro.Visibility == "global") {
		macro.Visibility = "personal"
	}

	if err := h.macroRepo.Create(c.Request.Context(), &macro); err != nil {
		logger.WithComponent("macro").Error("failed to create macro",
			"account_id", accID,
			"name", req.Name,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("macro").Info("macro created",
		"account_id", accID,
		"macro_id", macro.ID,
		"name", macro.Name,
		"visibility", macro.Visibility,
	)

	response.Created(c, macro)
}

func (h *MacroNotificationHandler) ListMacros(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")
	isAdmin := isUserAdmin(c)

	macros, err := h.macroRepo.ListForUser(c.Request.Context(), uint(accID), userID, isAdmin)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"payload": macros,
		"data":    macros,
	})
}

func (h *MacroNotificationHandler) GetMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macroID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")
	isAdmin := isUserAdmin(c)

	macro, err := h.macroRepo.GetByIDForUser(c.Request.Context(), uint(accID), uint(macroID), userID, isAdmin)
	if err != nil || macro == nil {
		response.NotFound(c, "macro not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"payload": macro,
		"data":    macro,
	})
}

type ExecuteMacroRequest struct {
	ConversationIDs []uint `json:"conversation_ids"`
	ConversationID  uint   `json:"conversation_id"`
}

func (h *MacroNotificationHandler) ExecuteMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macroID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")
	isAdmin := isUserAdmin(c)

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
	if len(convIDs) > 500 {
		response.BadRequest(c, "Batch macro execution exceeds maximum limit of 500 conversations")
		return
	}

	res, err := h.macroRepo.ExecuteForUser(c.Request.Context(), uint(accID), uint(macroID), userID, isAdmin, convIDs)
	if err != nil {
		logger.WithComponent("macro").Error("failed to execute macro",
			"account_id", accID,
			"macro_id", macroID,
			"conversation_ids", convIDs,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	total := len(convIDs)
	succeeded := 0
	failed := 0
	for _, status := range res {
		if status == "success" {
			succeeded++
		} else {
			failed++
		}
	}

	overallStatus := "success"
	if failed > 0 && succeeded > 0 {
		overallStatus = "partial_failed"
	} else if failed > 0 && succeeded == 0 {
		overallStatus = "failed"
	}

	logger.WithComponent("macro").Info("macro executed",
		"account_id", accID,
		"macro_id", macroID,
		"total", total,
		"succeeded", succeeded,
		"failed", failed,
		"overall_status", overallStatus,
	)

	response.Success(c, gin.H{
		"status":    overallStatus,
		"total":     total,
		"succeeded": succeeded,
		"failed":    failed,
		"results":   res,
	})
}

func (h *MacroNotificationHandler) DeleteMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macroID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")
	isAdmin := isUserAdmin(c)

	macro, err := h.macroRepo.GetByID(c.Request.Context(), uint(accID), uint(macroID))
	if err != nil || macro == nil {
		response.NotFound(c, "macro not found")
		return
	}

	// Permission: global macros require admin; personal macros require creator or admin
	if macro.Visibility == "global" && !isAdmin {
		response.Forbidden(c, "only administrators can delete global macros")
		return
	}
	if macro.Visibility == "personal" && macro.CreatedBy != userID && !isAdmin {
		response.Forbidden(c, "you cannot delete another user's personal macro")
		return
	}

	if err := h.macroRepo.Delete(c.Request.Context(), uint(accID), uint(macroID)); err != nil {
		logger.WithComponent("macro").Error("failed to delete macro",
			"account_id", accID,
			"macro_id", macroID,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("macro").Info("macro deleted",
		"account_id", accID,
		"macro_id", macroID,
	)

	response.Success(c, gin.H{"deleted": true})
}

type UpdateMacroRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	Actions    any    `json:"actions"`
}

func (h *MacroNotificationHandler) UpdateMacro(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	macroID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	userID := c.GetUint("user_id")
	isAdmin := isUserAdmin(c)

	macro, err := h.macroRepo.GetByID(c.Request.Context(), uint(accID), uint(macroID))
	if err != nil || macro == nil {
		response.NotFound(c, "macro not found")
		return
	}

	// Permission: global macros require admin; personal macros require creator or admin
	if macro.Visibility == "global" && !isAdmin {
		response.Forbidden(c, "only administrators can edit global macros")
		return
	}
	if macro.Visibility == "personal" && macro.CreatedBy != userID && !isAdmin {
		response.Forbidden(c, "you cannot edit another user's personal macro")
		return
	}

	var req UpdateMacroRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Actions != nil {
		if err := validateMacroActions(req.Actions); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		if s, ok := req.Actions.(string); ok {
			macro.Actions = s
		} else {
			b, _ := json.Marshal(req.Actions)
			macro.Actions = string(b)
		}
	}

	if req.Name != "" {
		macro.Name = req.Name
	}
	if req.Visibility != "" {
		macro.Visibility = req.Visibility
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

var (
	validNotificationFlags = map[string]bool{
		domain.NotificationTypeConversationAssignment: true,
		domain.NotificationTypeConversationMention:    true,
		domain.NotificationTypeConversationCreation:   true,
		domain.NotificationTypeSLABreach:              true,
		domain.NotificationTypeSystemAlert:            true,
	}
	timeFormatRegex = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

func parseAndValidateNotificationFlags(val any) (string, error) {
	if val == nil {
		return "", nil
	}

	var flags []string
	switch v := val.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "[]", nil
		}
		if err := json.Unmarshal([]byte(v), &flags); err != nil {
			return "", fmt.Errorf("invalid json array for flags: %w", err)
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				flags = append(flags, s)
			} else {
				return "", fmt.Errorf("flag must be a string")
			}
		}
	case []string:
		flags = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("invalid flags format")
		}
		if err := json.Unmarshal(b, &flags); err != nil {
			return "", fmt.Errorf("invalid flags array")
		}
	}

	for _, flag := range flags {
		if !validNotificationFlags[flag] {
			return "", fmt.Errorf("invalid notification flag: %s", flag)
		}
	}

	out, _ := json.Marshal(flags)
	return string(out), nil
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
		formatted, err := parseAndValidateNotificationFlags(req.SelectedEmailFlags)
		if err != nil {
			response.BadRequest(c, "Invalid selected_email_flags: "+err.Error())
			return
		}
		current.SelectedEmailFlags = formatted
	}
	if req.SelectedPushFlags != nil {
		formatted, err := parseAndValidateNotificationFlags(req.SelectedPushFlags)
		if err != nil {
			response.BadRequest(c, "Invalid selected_push_flags: "+err.Error())
			return
		}
		current.SelectedPushFlags = formatted
	}
	if req.SelectedInAppFlags != nil {
		formatted, err := parseAndValidateNotificationFlags(req.SelectedInAppFlags)
		if err != nil {
			response.BadRequest(c, "Invalid selected_in_app_flags: "+err.Error())
			return
		}
		current.SelectedInAppFlags = formatted
	}
	if req.Muted != nil {
		current.Muted = *req.Muted
	}
	if req.QuietHoursEnabled != nil {
		current.QuietHoursEnabled = *req.QuietHoursEnabled
	}
	if req.QuietHoursStart != nil {
		if !timeFormatRegex.MatchString(*req.QuietHoursStart) {
			response.BadRequest(c, "Invalid quiet_hours_start format, must be HH:mm (e.g. 22:00)")
			return
		}
		current.QuietHoursStart = *req.QuietHoursStart
	}
	if req.QuietHoursEnd != nil {
		if !timeFormatRegex.MatchString(*req.QuietHoursEnd) {
			response.BadRequest(c, "Invalid quiet_hours_end format, must be HH:mm (e.g. 08:00)")
			return
		}
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
		logger.WithComponent("csat").Error("failed to submit csat survey",
			"account_id", req.AccountID,
			"conversation_id", convID,
			"rating", req.Rating,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("csat").Info("csat survey submitted",
		"account_id", req.AccountID,
		"conversation_id", convID,
		"rating", req.Rating,
	)

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
