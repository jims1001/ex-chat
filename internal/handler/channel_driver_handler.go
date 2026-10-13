package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ChannelDriverHandler struct {
	callReads        *CallReadAuthorization
	channelOutbound  *service.ChannelOutboundService
	channelIngress   *service.ChannelIngressService
	recordingStorage *service.LocalStorageService
	repo             repository.ChannelRTMRepository
	inboxRepo        *repository.InboxRepository
	contactRepo      *repository.ContactRepository
	convRepo         *repository.ConversationRepository
	messageRepo      *repository.MessageRepository
}

func NewChannelDriverHandler(
	repo repository.ChannelRTMRepository,
	inboxRepo *repository.InboxRepository,
	contactRepo *repository.ContactRepository,
	convRepo *repository.ConversationRepository,
	messageRepo *repository.MessageRepository,
) *ChannelDriverHandler {
	return &ChannelDriverHandler{
		recordingStorage: service.NewLocalStorageService("uploads", ""),
		channelIngress:   service.NewChannelIngressService(channel.ConfigFromEnvironment(), nil, inboxRepo, contactRepo, convRepo, messageRepo),
		repo:             repo,
		inboxRepo:        inboxRepo,
		contactRepo:      contactRepo,
		convRepo:         convRepo,
		messageRepo:      messageRepo,
	}
}

// ----------------- WhatsApp Cloud API -----------------

// WhatsApp CSAT provisioning has no implemented provider identity/review contract.
// This account-level alias also has no inbox selector; never infer one or claim
// that a local configuration save created an approved provider template.
func (h *ChannelDriverHandler) SetWhatsAppCSATTemplate(c *gin.Context) {
	whatsAppCSATUnavailable(c)
}

func whatsAppCSATUnavailable(c *gin.Context) {
	response.Error(c, http.StatusServiceUnavailable, "WhatsApp CSAT template provisioning and provider status are currently unavailable")
}

// ----------------- Twilio Channel -----------------

// ----------------- Facebook / Instagram Callbacks -----------------

// Facebook/Instagram callbacks remain disabled until there is a verified
// recipient binding and provider signature implementation. Never synthesize a
// tenant or inbox from an unsigned payload.
func (h *ChannelDriverHandler) VerifyFacebookWebhook(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Facebook/Instagram callbacks are not configured"})
}

func (h *ChannelDriverHandler) HandleFacebookWebhook(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Facebook/Instagram callbacks are not configured"})
}

// ----------------- WebRTC Calls & Conferences -----------------

func (h *ChannelDriverHandler) ListCalls(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}
	actor, ok := h.callReadActor(c, uint(accountID))
	if !ok {
		return
	}
	calls, err := h.repo.ListCalls(uint(accountID))
	if err != nil {
		response.InternalError(c, "Failed to load calls")
		return
	}
	visible := make([]domain.Call, 0, len(calls))
	for i := range calls {
		allowed, err := h.callReads.canRead(actor, &calls[i])
		if err != nil {
			response.InternalError(c, "Failed to verify call access")
			return
		}
		if allowed {
			visible = append(visible, calls[i])
		}
	}
	response.Success(c, visible)
}

func (h *ChannelDriverHandler) GetCall(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}
	callID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Call ID must be numeric")
		return
	}
	actor, ok := h.callReadActor(c, uint(accountID))
	if !ok {
		return
	}
	call, err := h.repo.GetCall(uint(accountID), uint(callID))
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "Failed to load call")
		return
	}
	if !h.allowCallRead(c, actor, call) {
		return
	}
	response.Success(c, call)
}

func (h *ChannelDriverHandler) InitiateContactCall(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}
	contactID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || contactID == 0 {
		contactID, err = strconv.ParseUint(c.Param("contact_id"), 10, 64)
	}
	if err != nil {
		response.BadRequest(c, "Contact ID must be numeric")
		return
	}

	var req struct {
		InboxID        uint   `json:"inbox_id" binding:"required"`
		ConversationID *uint  `json:"conversation_id"`
		SDPOffer       string `json:"sdp_offer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	agentID := c.GetUint("user_id")
	call := &domain.Call{
		AccountID:      uint(accountID),
		ContactID:      uint(contactID),
		InboxID:        req.InboxID,
		ConversationID: req.ConversationID,
		AgentID:        &agentID,
		Status:         "ringing",
		Direction:      "outbound",
		SDPOffer:       req.SDPOffer,
	}

	if err := h.repo.CreateCall(call); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	logger.WithComponent("call").Info("call initiated",
		"call_id", call.ID,
		"account_id", accountID,
		"contact_id", contactID,
		"inbox_id", req.InboxID,
	)

	response.Created(c, call)
}

type AcceptCallReq struct {
	SDPAnswer string `json:"sdp_answer"`
}

func (h *ChannelDriverHandler) AcceptCall(c *gin.Context) {
	accountID, _ := strconv.ParseUint(c.Param("account_id"), 10, 64)
	callID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid call ID")
		return
	}

	call, err := h.repo.GetCall(uint(accountID), uint(callID))
	if err != nil || call == nil {
		response.NotFound(c, "Call not found")
		return
	}

	var req AcceptCallReq
	_ = c.ShouldBindJSON(&req)

	call.Status = "in_progress"
	if req.SDPAnswer != "" {
		call.SDPAnswer = req.SDPAnswer
	}

	if err := h.repo.UpdateCall(call); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, call)
}

func (h *ChannelDriverHandler) RejectCall(c *gin.Context) {
	accountID, _ := strconv.ParseUint(c.Param("account_id"), 10, 64)
	callID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid call ID")
		return
	}

	call, err := h.repo.GetCall(uint(accountID), uint(callID))
	if err != nil || call == nil {
		response.NotFound(c, "Call not found")
		return
	}

	call.Status = "rejected"
	if err := h.repo.UpdateCall(call); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, call)
}

type EndCallReq struct {
	Duration int `json:"duration"`
}

func (h *ChannelDriverHandler) EndCall(c *gin.Context) {
	accountID, _ := strconv.ParseUint(c.Param("account_id"), 10, 64)
	callID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid call ID")
		return
	}

	call, err := h.repo.GetCall(uint(accountID), uint(callID))
	if err != nil || call == nil {
		response.NotFound(c, "Call not found")
		return
	}

	var req EndCallReq
	_ = c.ShouldBindJSON(&req)

	call.Status = "completed"
	if req.Duration > 0 {
		call.Duration = req.Duration
	}

	if err := h.repo.UpdateCall(call); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	logger.WithComponent("call").Info("call ended",
		"call_id", call.ID,
		"account_id", accountID,
		"status", call.Status,
		"duration", call.Duration,
	)

	response.Success(c, call)
}

type AddCandidateReq struct {
	Candidate string `json:"candidate" binding:"required"`
	SDPMid    string `json:"sdp_mid"`
	SDPMLine  int    `json:"sdp_m_line_index"`
}

func (h *ChannelDriverHandler) AddCallCandidate(c *gin.Context) {
	accountID, _ := strconv.ParseUint(c.Param("account_id"), 10, 64)
	callID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid call ID")
		return
	}

	call, err := h.repo.GetCall(uint(accountID), uint(callID))
	if err != nil || call == nil {
		response.NotFound(c, "Call not found")
		return
	}

	var req AddCandidateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	cand := domain.CallICECandidate{
		CallID:    uint(callID),
		Candidate: req.Candidate,
		SDPMid:    req.SDPMid,
		SDPMLine:  req.SDPMLine,
	}

	if err := h.repo.CreateCallCandidate(&cand); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, cand)
}

func (h *ChannelDriverHandler) ListCallCandidates(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}
	callID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid call ID")
		return
	}
	actor, ok := h.callReadActor(c, uint(accountID))
	if !ok {
		return
	}
	call, err := h.repo.GetCall(uint(accountID), uint(callID))
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "Failed to load call")
		return
	}
	if !h.allowCallRead(c, actor, call) {
		return
	}
	cands, err := h.repo.ListCallCandidates(call.ID)
	if err != nil {
		response.InternalError(c, "Failed to load call candidates")
		return
	}
	response.Success(c, cands)
}

func (h *ChannelDriverHandler) CreateConference(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}
	inboxID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || inboxID == 0 {
		inboxID, err = strconv.ParseUint(c.Param("inbox_id"), 10, 64)
	}
	if err != nil {
		response.BadRequest(c, "Inbox ID must be numeric")
		return
	}

	var req struct {
		CallSID        string `json:"call_sid" binding:"required"`
		ConversationID *uint  `json:"conversation_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	conf := &domain.Conference{
		AccountID:      uint(accountID),
		InboxID:        uint(inboxID),
		CallSID:        req.CallSID,
		ConversationID: req.ConversationID,
		Token:          token,
		Status:         "active",
	}

	if err := h.repo.CreateConference(conf); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, conf)
}

func (h *ChannelDriverHandler) HandleWhatsAppCallSDP(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		CallID    uint   `json:"call_id" binding:"required"`
		SDPAnswer string `json:"sdp_answer" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	call, err := h.repo.GetCall(uint(accountID), req.CallID)
	if err != nil {
		response.NotFound(c, "Call session not found")
		return
	}

	call.SDPAnswer = req.SDPAnswer
	call.Status = "in_progress"
	call.UpdatedAt = time.Now()
	_ = h.repo.UpdateCall(call)

	response.Success(c, call)
}

// ----------------- WhatsApp & Voice Call Lifecycle Extensions -----------------

// GetWhatsAppCallDetail returns comprehensive details of a WhatsApp / Voice call
func (h *ChannelDriverHandler) GetWhatsAppCallDetail(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}
	actor, ok := h.callReadActor(c, uint(accountID))
	if !ok {
		return
	}
	idParam := c.Param("id")
	var call *domain.Call
	if callID, parseErr := strconv.ParseUint(idParam, 10, 64); parseErr == nil {
		call, err = h.repo.GetCall(uint(accountID), uint(callID))
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			response.InternalError(c, "Failed to load call")
			return
		}
	}
	if call == nil {
		call, err = h.repo.GetCallByProviderCallID(uint(accountID), idParam)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			response.InternalError(c, "Failed to load call")
			return
		}
	}
	if !h.allowCallRead(c, actor, call) {
		return
	}

	elapsedSeconds := 0
	if call.Duration > 0 {
		elapsedSeconds = call.Duration
	} else if (call.Status == "in_progress" || call.Status == "completed") && !call.CreatedAt.IsZero() {
		elapsedSeconds = int(time.Since(call.CreatedAt).Seconds())
	}

	caller := gin.H{}
	if call.ContactID != 0 && h.contactRepo != nil {
		if contact, err := h.contactRepo.FindCallReadCaller(call.AccountID, call.ContactID); err == nil && contact != nil {
			caller = gin.H{
				"name":   contact.Name,
				"phone":  contact.PhoneNumber,
				"avatar": contact.AvatarURL,
			}
		}
	}

	provider := call.Provider
	if provider == "" {
		provider = "whatsapp"
	}
	providerCallID := call.ProviderCallID
	if providerCallID == "" {
		providerCallID = fmt.Sprintf("call_%d", call.ID)
	}

	response.Success(c, gin.H{
		"id":                   call.ID,
		"call_id":              providerCallID,
		"provider":             provider,
		"status":               call.Status,
		"direction":            call.Direction,
		"conversation_id":      call.ConversationID,
		"inbox_id":             call.InboxID,
		"message_id":           nil,
		"accepted_by_agent_id": call.AgentID,
		"elapsed_seconds":      elapsedSeconds,
		"sdp_offer":            call.SDPOffer,
		"sdp_answer":           call.SDPAnswer,
		"recording_url":        call.RecordingURL,
		"recording_sid":        call.RecordingSID,
		"duration":             call.Duration,
		"terminate_reason":     call.TerminateReason,
		"ice_servers": []gin.H{
			{"urls": "stun:stun.l.google.com:19302"},
		},
		"caller": caller,
	})
}

// DeleteConference tears down a conference session by id or inbox
func (h *ChannelDriverHandler) DeleteConference(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	idParam := c.Param("id")
	if idParam == "" {
		idParam = c.Param("inbox_id")
	}

	var confID uint
	if id, err := strconv.ParseUint(idParam, 10, 64); err == nil && id > 0 {
		// Could be conference ID or inbox ID
		if conf, err := h.repo.GetConferenceByID(uint(accountID), uint(id)); err == nil && conf != nil {
			confID = conf.ID
			_ = h.repo.DeleteConference(uint(accountID), conf.ID)
		} else if conf, err := h.repo.GetConference(uint(accountID), uint(id)); err == nil && conf != nil {
			confID = conf.ID
			_ = h.repo.DeleteConferenceByInbox(uint(accountID), uint(id))
		}
	}

	// Also handle query / body call_sid or conversation_id if specified
	var req struct {
		CallSID        string `json:"call_sid" form:"call_sid"`
		ConversationID uint   `json:"conversation_id" form:"conversation_id"`
	}
	_ = c.ShouldBind(&req)

	if req.CallSID != "" {
		if call, err := h.repo.GetCallByProviderCallID(uint(accountID), req.CallSID); err == nil && call != nil {
			if call.Status == "ringing" && call.AgentID == nil {
				call.Status = "rejected"
				call.TerminateReason = "agent_rejected"
				_ = h.repo.UpdateCall(call)
			}
		}
	}

	logger.WithComponent("conference").Info("conference destroyed",
		"account_id", accountID,
		"id", idParam,
		"conf_id", confID,
	)

	response.Success(c, gin.H{
		"status":  "success",
		"message": "Conference terminated and deleted",
		"id":      confID,
	})
}

// GetConferenceToken generates a WebRTC / signaling conference client access token
func (h *ChannelDriverHandler) GetConferenceToken(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	userID := c.GetUint("user_id")
	if userID == 0 {
		userID = 1
	}

	inboxID := c.Param("id")
	if inboxID == "" {
		inboxID = c.Param("inbox_id")
	}

	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	tokenStr := hex.EncodeToString(tokenBytes)

	response.Success(c, gin.H{
		"token":      tokenStr,
		"identity":   fmt.Sprintf("agent_%d", userID),
		"room":       fmt.Sprintf("conf_acc_%d_inbox_%s", accountID, inboxID),
		"account_id": accountID,
		"ice_servers": []gin.H{
			{"urls": "stun:stun.l.google.com:19302"},
			{"urls": "turn:turn.example.com:3478", "username": "exchat", "credential": "secret"},
		},
	})
}

// TerminateCall executes complete call termination flow
func (h *ChannelDriverHandler) TerminateCall(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	idParam := c.Param("id")
	var call *domain.Call
	if callID, err := strconv.ParseUint(idParam, 10, 64); err == nil {
		call, _ = h.repo.GetCall(uint(accountID), uint(callID))
	}
	if call == nil {
		call, _ = h.repo.GetCallByProviderCallID(uint(accountID), idParam)
	}

	if call == nil {
		response.NotFound(c, "Call session not found")
		return
	}

	var req struct {
		Reason   string `json:"reason" form:"reason"` // completed, busy, canceled, agent_rejected, failed
		Duration int    `json:"duration" form:"duration"`
	}
	_ = c.ShouldBind(&req)

	if req.Reason == "" {
		req.Reason = "completed"
	}

	if req.Reason == "agent_rejected" || req.Reason == "busy" || req.Reason == "rejected" {
		call.Status = "rejected"
	} else {
		call.Status = "completed"
	}

	call.TerminateReason = req.Reason

	if req.Duration > 0 {
		call.Duration = req.Duration
	} else if call.Duration == 0 && !call.CreatedAt.IsZero() {
		call.Duration = int(time.Since(call.CreatedAt).Seconds())
	}

	call.UpdatedAt = time.Now()
	if err := h.repo.UpdateCall(call); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	// Clean up related conference if any
	_ = h.repo.DeleteConferenceByInbox(call.AccountID, call.InboxID)

	logger.WithComponent("call").Info("call terminated",
		"call_id", call.ID,
		"account_id", accountID,
		"status", call.Status,
		"reason", call.TerminateReason,
		"duration", call.Duration,
	)

	response.Success(c, call)
}
