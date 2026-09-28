package handler

import (
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// ----------------- Conversation Participants -----------------

type AddParticipantReq struct {
	UserIDs []uint `json:"user_ids" binding:"required"`
}

type UpdateParticipantsReq struct {
	UserIDs []uint `json:"user_ids"`
}

type RemoveParticipantReq struct {
	UserID  uint   `json:"user_id"`
	UserIDs []uint `json:"user_ids"`
}

// verifyConversationBelongsToAccount checks cross-tenant boundary and ensures conversation exists
func (h *AdvancedHandler) verifyConversationBelongsToAccount(c *gin.Context, accID, convID, operatorID uint, action string) bool {
	if accID == 0 || convID == 0 {
		response.NotFound(c, "Conversation not found")
		return false
	}
	conv, err := h.convRepo.FindSummaryByID(accID, convID)
	if err != nil || conv == nil {
		logger.WithComponent("conversation_participant").Warn("conversation access denied or not found",
			"action", action,
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
		)
		response.NotFound(c, "Conversation not found")
		return false
	}
	return true
}

// AddParticipants adds one or more agents to the conversation as participants
func (h *AdvancedHandler) AddParticipants(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	operatorID := c.GetUint("user_id")

	var req AddParticipantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.WithComponent("conversation_participant").Warn("add participants failed: invalid json",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
			"error", err.Error(),
		)
		response.BadRequest(c, err.Error())
		return
	}

	if len(req.UserIDs) == 0 {
		logger.WithComponent("conversation_participant").Warn("add participants failed: empty user_ids",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
		)
		response.BadRequest(c, "user_ids must contain at least one user ID")
		return
	}

	// Verify conversation boundary
	if !h.verifyConversationBelongsToAccount(c, uint(accID), uint(convID), operatorID, "add_participants") {
		return
	}

	addedUserIDs, err := h.convRepo.AddParticipants(uint(accID), uint(convID), req.UserIDs)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	addedCount := len(addedUserIDs)

	logger.WithComponent("conversation_participant").Info("conversation participants added",
		"account_id", accID,
		"conversation_id", convID,
		"operator_id", operatorID,
		"requested_user_ids", req.UserIDs,
		"added_count", addedCount,
		"added_user_ids", addedUserIDs,
	)

	response.Success(c, gin.H{
		"status":      "ok",
		"added_count": addedCount,
		"user_ids":    req.UserIDs,
	})
}

// ListParticipants returns all agents participating in the conversation
func (h *AdvancedHandler) ListParticipants(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	operatorID := c.GetUint("user_id")

	// Verify conversation boundary
	if !h.verifyConversationBelongsToAccount(c, uint(accID), uint(convID), operatorID, "list_participants") {
		return
	}

	parts, err := h.convRepo.ListParticipants(uint(accID), uint(convID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	logger.WithComponent("conversation_participant").Info("conversation participants listed",
		"account_id", accID,
		"conversation_id", convID,
		"operator_id", operatorID,
		"participant_count", len(parts),
	)

	response.Success(c, parts)
}

// RemoveParticipant removes a specific agent participant from the conversation
func (h *AdvancedHandler) RemoveParticipant(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	operatorID := c.GetUint("user_id")
	targetUID, _ := strconv.ParseUint(c.Param("user_id"), 10, 32)

	// If user_id not provided in URL path, try reading from request body
	if targetUID == 0 {
		var req RemoveParticipantReq
		if err := c.ShouldBindJSON(&req); err == nil {
			if req.UserID > 0 {
				targetUID = uint64(req.UserID)
			} else if len(req.UserIDs) > 0 {
				h.removeUserIDs(c, uint(accID), uint(convID), operatorID, req.UserIDs)
				return
			}
		}
	}

	if targetUID == 0 {
		logger.WithComponent("conversation_participant").Warn("remove participant failed: missing user_id",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
		)
		response.BadRequest(c, "user_id is required")
		return
	}

	h.removeUserIDs(c, uint(accID), uint(convID), operatorID, []uint{uint(targetUID)})
}

// RemoveParticipants removes multiple agent participants from the conversation via request body
func (h *AdvancedHandler) RemoveParticipants(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	operatorID := c.GetUint("user_id")

	var req RemoveParticipantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.WithComponent("conversation_participant").Warn("remove participants failed: invalid json",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
			"error", err.Error(),
		)
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	uids := req.UserIDs
	if len(uids) == 0 && req.UserID > 0 {
		uids = []uint{req.UserID}
	}

	if len(uids) == 0 {
		logger.WithComponent("conversation_participant").Warn("remove participants failed: empty user_ids",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
		)
		response.BadRequest(c, "At least one user_id is required")
		return
	}

	h.removeUserIDs(c, uint(accID), uint(convID), operatorID, uids)
}

// removeUserIDs is an internal helper for performing participant deletion with validation and structured logging
func (h *AdvancedHandler) removeUserIDs(c *gin.Context, accID, convID, operatorID uint, uids []uint) {
	if !h.verifyConversationBelongsToAccount(c, accID, convID, operatorID, "remove_participants") {
		return
	}

	// Filter valid user IDs
	var validUIDs []uint
	for _, id := range uids {
		if id > 0 {
			validUIDs = append(validUIDs, id)
		}
	}
	if len(validUIDs) == 0 {
		response.BadRequest(c, "No valid user IDs provided")
		return
	}

	removedCount, err := h.convRepo.RemoveParticipants(accID, convID, validUIDs)
	if err != nil {
		logger.WithComponent("conversation_participant").Error("database error removing conversation participants",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
			"target_user_ids", validUIDs,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to remove participants: "+err.Error())
		return
	}

	if removedCount == 0 {
		logger.WithComponent("conversation_participant").Warn("no participants were removed (records not found)",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
			"target_user_ids", validUIDs,
		)
	} else {
		logger.WithComponent("conversation_participant").Info("conversation participants removed successfully",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
			"target_user_ids", validUIDs,
			"removed_count", removedCount,
		)
	}

	response.Success(c, gin.H{
		"status":        "ok",
		"removed_count": removedCount,
		"user_ids":      validUIDs,
	})
}

// UpdateParticipants executes the full update workflow (replace/sync participants list in atomic transaction)
func (h *AdvancedHandler) UpdateParticipants(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	operatorID := c.GetUint("user_id")

	var req UpdateParticipantsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.WithComponent("conversation_participant").Warn("update participants failed: invalid json",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
			"error", err.Error(),
		)
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	// Verify conversation boundary
	if !h.verifyConversationBelongsToAccount(c, uint(accID), uint(convID), operatorID, "update_participants") {
		return
	}

	result, err := h.convRepo.SyncParticipants(uint(accID), uint(convID), req.UserIDs)
	if err != nil {
		logger.WithComponent("conversation_participant").Error("database error during full update of conversation participants",
			"account_id", accID,
			"conversation_id", convID,
			"operator_id", operatorID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update participants: "+err.Error())
		return
	}

	logger.WithComponent("conversation_participant").Info("conversation participants fully updated",
		"account_id", accID,
		"conversation_id", convID,
		"operator_id", operatorID,
		"added_user_ids", result.Added,
		"removed_user_ids", result.Removed,
		"final_count", len(result.Participants),
	)

	response.Success(c, gin.H{
		"status":       "ok",
		"added":        result.Added,
		"removed":      result.Removed,
		"participants": result.Participants,
		"total_count":  len(result.Participants),
	})
}
