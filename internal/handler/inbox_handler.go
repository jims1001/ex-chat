package handler

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
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
}

type UpdateInboxRequest struct {
	Name                string `json:"name"`
	GreetingMessage     string `json:"greeting_message"`
	GreetingEnabled     *bool  `json:"greeting_enabled"`
	WorkingHoursEnabled *bool  `json:"working_hours_enabled"`
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
	}

	if err := h.inboxRepo.Create(&inbox); err != nil {
		response.InternalError(c, "Failed to create inbox")
		return
	}

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

	if err := h.inboxRepo.Update(inbox); err != nil {
		response.InternalError(c, "Failed to update inbox")
		return
	}

	response.Success(c, inbox)
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
		response.InternalError(c, "Failed to delete inbox")
		return
	}

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
