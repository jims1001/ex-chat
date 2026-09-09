package handler

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type InboxHandler struct {
	inboxRepo *repository.InboxRepository
	userRepo  *repository.UserRepository
}

func NewInboxHandler(inboxRepo *repository.InboxRepository, userRepo *repository.UserRepository) *InboxHandler {
	return &InboxHandler{
		inboxRepo: inboxRepo,
		userRepo:  userRepo,
	}
}

type CreateInboxRequest struct {
	Name                string `json:"name" binding:"required"`
	ChannelType         string `json:"channel_type"`
	GreetingMessage     string `json:"greeting_message"`
	GreetingEnabled     *bool  `json:"greeting_enabled"`
	WorkingHoursEnabled *bool  `json:"working_hours_enabled"`
	OutOfOfficeMessage  string `json:"out_of_office_message"`
	Timezone            string `json:"timezone"`
	WorkingHours        string `json:"working_hours"`
	AssignmentPolicyID  *uint  `json:"assignment_policy_id"`
}

type UpdateInboxRequest struct {
	Name                string `json:"name"`
	GreetingMessage     string `json:"greeting_message"`
	GreetingEnabled     *bool  `json:"greeting_enabled"`
	WorkingHoursEnabled *bool  `json:"working_hours_enabled"`
	OutOfOfficeMessage  string `json:"out_of_office_message"`
	Timezone            string `json:"timezone"`
	WorkingHours        string `json:"working_hours"`
	AssignmentPolicyID  *uint  `json:"assignment_policy_id"`
}

type AddInboxMembersRequest struct {
	UserIDs []uint `json:"user_ids" binding:"required"`
}

func generateToken(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *InboxHandler) ListInboxes(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	inboxes, err := h.inboxRepo.ListByAccount(accountID)
	if err != nil {
		response.InternalError(c, "Failed to fetch inboxes")
		return
	}

	response.Success(c, inboxes)
}

func (h *InboxHandler) CreateInbox(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreateInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	channelType := req.ChannelType
	if channelType == "" {
		channelType = domain.ChannelWebWidget
	}

	greetingEnabled := true
	if req.GreetingEnabled != nil {
		greetingEnabled = *req.GreetingEnabled
	}

	workingHoursEnabled := false
	if req.WorkingHoursEnabled != nil {
		workingHoursEnabled = *req.WorkingHoursEnabled
	}

	inbox := domain.Inbox{
		AccountID:           accountID,
		Name:                req.Name,
		ChannelType:         channelType,
		WebsiteToken:        generateToken(16),
		GreetingMessage:     req.GreetingMessage,
		GreetingEnabled:     greetingEnabled,
		WorkingHoursEnabled: workingHoursEnabled,
		OutOfOfficeMessage:  req.OutOfOfficeMessage,
		Timezone:            req.Timezone,
		WorkingHours:        req.WorkingHours,
		AssignmentPolicyID:  req.AssignmentPolicyID,
	}

	if err := h.inboxRepo.Create(&inbox); err != nil {
		logger.WithComponent("inbox").Error("failed to create inbox",
			"account_id", accountID,
			"name", req.Name,
			"channel_type", channelType,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create inbox")
		return
	}

	logger.WithComponent("inbox").Info("inbox created",
		"account_id", accountID,
		"inbox_id", inbox.ID,
		"name", inbox.Name,
		"channel_type", inbox.ChannelType,
	)

	response.Created(c, inbox)
}

func (h *InboxHandler) GetInbox(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	inboxID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}

	inbox, err := h.inboxRepo.FindByID(accountID, uint(inboxID))
	if err != nil || inbox == nil {
		response.NotFound(c, "Inbox not found")
		return
	}

	response.Success(c, inbox)
}

func (h *InboxHandler) UpdateInbox(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	inboxID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}

	inbox, err := h.inboxRepo.FindByID(accountID, uint(inboxID))
	if err != nil || inbox == nil {
		response.NotFound(c, "Inbox not found")
		return
	}

	var req UpdateInboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Name != "" {
		inbox.Name = req.Name
	}
	if req.GreetingMessage != "" {
		inbox.GreetingMessage = req.GreetingMessage
	}
	if req.GreetingEnabled != nil {
		inbox.GreetingEnabled = *req.GreetingEnabled
	}
	if req.WorkingHoursEnabled != nil {
		inbox.WorkingHoursEnabled = *req.WorkingHoursEnabled
	}
	if req.OutOfOfficeMessage != "" {
		inbox.OutOfOfficeMessage = req.OutOfOfficeMessage
	}
	if req.Timezone != "" {
		inbox.Timezone = req.Timezone
	}
	if req.WorkingHours != "" {
		inbox.WorkingHours = req.WorkingHours
	}
	if req.AssignmentPolicyID != nil {
		inbox.AssignmentPolicyID = req.AssignmentPolicyID
	}

	if err := h.inboxRepo.Update(inbox); err != nil {
		logger.WithComponent("inbox").Error("failed to update inbox",
			"account_id", accountID,
			"inbox_id", inbox.ID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update inbox")
		return
	}

	logger.WithComponent("inbox").Info("inbox updated",
		"account_id", accountID,
		"inbox_id", inbox.ID,
		"name", inbox.Name,
	)

	response.Success(c, inbox)
}

func (h *InboxHandler) BindAssignmentPolicy(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	inboxID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}

	inbox, err := h.inboxRepo.FindByID(accountID, uint(inboxID))
	if err != nil || inbox == nil {
		response.NotFound(c, "Inbox not found")
		return
	}

	var req struct {
		AssignmentPolicyID *uint `json:"assignment_policy_id"`
		PolicyID           *uint `json:"policy_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	policyID := req.AssignmentPolicyID
	if policyID == nil && req.PolicyID != nil {
		policyID = req.PolicyID
	}

	if err := h.inboxRepo.BindAssignmentPolicy(accountID, uint(inboxID), policyID); err != nil {
		logger.WithComponent("inbox").Error("failed to bind assignment policy",
			"account_id", accountID,
			"inbox_id", inboxID,
			"policy_id", policyID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to bind assignment policy: "+err.Error())
		return
	}

	logger.WithComponent("inbox").Info("assignment policy bound to inbox",
		"account_id", accountID,
		"inbox_id", inboxID,
		"policy_id", policyID,
	)

	updated, _ := h.inboxRepo.FindByID(accountID, uint(inboxID))
	response.Success(c, updated)
}

func (h *InboxHandler) DeleteInbox(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	inboxID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}

	if err := h.inboxRepo.Delete(accountID, uint(inboxID)); err != nil {
		logger.WithComponent("inbox").Error("failed to delete inbox",
			"account_id", accountID,
			"inbox_id", inboxID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete inbox")
		return
	}

	logger.WithComponent("inbox").Info("inbox deleted",
		"account_id", accountID,
		"inbox_id", inboxID,
	)

	response.Success(c, gin.H{"deleted": true})
}

func (h *InboxHandler) ListInboxMembers(c *gin.Context) {
	inboxID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}

	members, err := h.inboxRepo.ListMembers(uint(inboxID))
	if err != nil {
		response.InternalError(c, "Failed to list inbox members")
		return
	}

	response.Success(c, members)
}

func (h *InboxHandler) AddInboxMembers(c *gin.Context) {
	inboxID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}

	var req AddInboxMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	for _, uid := range req.UserIDs {
		_ = h.inboxRepo.AddMember(uint(inboxID), uid)
	}

	logger.WithComponent("inbox").Info("inbox members added",
		"inbox_id", inboxID,
		"user_ids", req.UserIDs,
	)

	members, _ := h.inboxRepo.ListMembers(uint(inboxID))
	response.Success(c, members)
}

// WidgetConfig handles public widget initialization
func (h *InboxHandler) WidgetConfig(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}

	if websiteToken == "" {
		response.BadRequest(c, "website_token query parameter or header is required")
		return
	}

	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}

	response.Success(c, gin.H{
		"inbox_id":              inbox.ID,
		"account_id":            inbox.AccountID,
		"name":                  inbox.Name,
		"channel_type":          inbox.ChannelType,
		"website_token":         inbox.WebsiteToken,
		"greeting_enabled":      inbox.GreetingEnabled,
		"greeting_message":      inbox.GreetingMessage,
		"working_hours_enabled": inbox.WorkingHoursEnabled,
	})
}
