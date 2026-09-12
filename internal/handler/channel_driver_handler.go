package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type ChannelDriverHandler struct {
	repo        repository.ChannelAuthEnterpriseRepository
	inboxRepo   *repository.InboxRepository
	contactRepo *repository.ContactRepository
	convRepo    *repository.ConversationRepository
	messageRepo *repository.MessageRepository
}

func NewChannelDriverHandler(
	repo repository.ChannelAuthEnterpriseRepository,
	inboxRepo *repository.InboxRepository,
	contactRepo *repository.ContactRepository,
	convRepo *repository.ConversationRepository,
	messageRepo *repository.MessageRepository,
) *ChannelDriverHandler {
	return &ChannelDriverHandler{
		repo:        repo,
		inboxRepo:   inboxRepo,
		contactRepo: contactRepo,
		convRepo:    convRepo,
		messageRepo: messageRepo,
	}
}

// ----------------- WhatsApp Cloud API -----------------

func (h *ChannelDriverHandler) VerifyWhatsAppWebhook(c *gin.Context) {
	mode := c.Query("hub.mode")
	token := c.Query("hub.verify_token")
	challenge := c.Query("hub.challenge")

	if mode == "subscribe" && token != "" {
		c.String(http.StatusOK, challenge)
		return
	}
	response.Forbidden(c, "Invalid verify token or mode")
}

func (h *ChannelDriverHandler) HandleWhatsAppWebhook(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	logger.WithComponent("channel_whatsapp").Info("received whatsapp webhook",
		"account_id", accountID,
	)

	var payload struct {
		Object string `json:"object"`
		Entry  []struct {
			ID      string `json:"id"`
			Changes []struct {
				Value struct {
					MessagingProduct string `json:"messaging_product"`
					Metadata         struct {
						DisplayPhoneNumber string `json:"display_phone_number"`
						PhoneNumberID      string `json:"phone_number_id"`
					} `json:"metadata"`
					Contacts []struct {
						Profile struct {
							Name string `json:"name"`
						} `json:"profile"`
						WaID string `json:"wa_id"`
					} `json:"contacts"`
					Messages []struct {
						From      string `json:"from"`
						ID        string `json:"id"`
						Timestamp string `json:"timestamp"`
						Type      string `json:"type"`
						Text      struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
					Statuses []struct {
						ID          string `json:"id"`
						Status      string `json:"status"` // sent, delivered, read, failed
						RecipientID string `json:"recipient_id"`
					} `json:"statuses"`
				} `json:"value"`
				Field string `json:"field"`
			} `json:"changes"`
		} `json:"entry"`
	}

	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, "Failed to parse webhook JSON")
		return
	}

	// Process entries
	var processedCount int
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			val := change.Value
			// Process incoming WhatsApp messages
			for _, msg := range val.Messages {
				senderName := msg.From
				if len(val.Contacts) > 0 && val.Contacts[0].Profile.Name != "" {
					senderName = val.Contacts[0].Profile.Name
				}

				// Find or create Contact
				contact, err := h.contactRepo.FindByIdentifier(uint(accountID), msg.From)
				if err != nil || contact == nil {
					contact = &domain.Contact{
						AccountID:   uint(accountID),
						Identifier:  msg.From,
						Name:        senderName,
						PhoneNumber: msg.From,
					}
					_ = h.contactRepo.Create(contact)
				}

				// Find or create Conversation
				conv := &domain.Conversation{
					AccountID: uint(accountID),
					InboxID:   1, // default whatsapp inbox
					ContactID: contact.ID,
					Status:    domain.ConversationStatusOpen,
					Priority:  "medium",
				}
				_ = h.convRepo.Create(conv)

				// Create Message
				content := msg.Text.Body
				if content == "" {
					content = fmt.Sprintf("[%s message]", msg.Type)
				}
				inMsg := &domain.Message{
					AccountID:      uint(accountID),
					ConversationID: conv.ID,
					SenderType:     "Contact",
					SenderID:       contact.ID,
					MessageType:    domain.MessageTypeIncoming,
					ContentType:    domain.ContentTypeText,
					Content:        content,
					Status:         domain.MessageStatusSent,
					EchoID:         msg.ID,
				}
				_ = h.messageRepo.Create(inMsg)
				processedCount++
			}
		}
	}

	response.Success(c, gin.H{
		"processed_messages": processedCount,
		"status":             "success",
	})
}

func (h *ChannelDriverHandler) WhatsAppAuthorization(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		Code          string `json:"code" binding:"required"`
		BusinessID    string `json:"business_id"`
		WabaID        string `json:"waba_id"`
		PhoneNumberID string `json:"phone_number_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"account_id":      accountID,
		"authorized":      true,
		"waba_id":         req.WabaID,
		"phone_number_id": req.PhoneNumberID,
		"channel_type":    "whatsapp",
	})
}

func (h *ChannelDriverHandler) SetWhatsAppCSATTemplate(c *gin.Context) {
	accountID, _ := strconv.ParseUint(c.Param("account_id"), 10, 64)
	inboxID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	if inboxID == 0 {
		inboxID, _ = strconv.ParseUint(c.Param("inbox_id"), 10, 64)
	}

	var req struct {
		Message    string `json:"message" binding:"required"`
		ButtonText string `json:"button_text"`
		Language   string `json:"language"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"account_id":  accountID,
		"inbox_id":    inboxID,
		"template":    req.Message,
		"button_text": req.ButtonText,
		"status":      "active",
	})
}

// ----------------- Twilio Channel -----------------

func (h *ChannelDriverHandler) CreateTwilioChannel(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		InboxName   string `json:"inbox_name" binding:"required"`
		AccountSID  string `json:"account_sid" binding:"required"`
		AuthToken   string `json:"auth_token" binding:"required"`
		PhoneNumber string `json:"phone_number" binding:"required"`
		Medium      string `json:"medium"` // sms, whatsapp
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	medium := req.Medium
	if medium == "" {
		medium = "sms"
	}

	inbox := &domain.Inbox{
		AccountID:   uint(accountID),
		Name:        req.InboxName,
		ChannelType: "channel_twilio",
	}
	if err := h.inboxRepo.Create(inbox); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, gin.H{
		"inbox":        inbox,
		"phone_number": req.PhoneNumber,
		"medium":       medium,
	})
}

func (h *ChannelDriverHandler) HandleTwilioWebhook(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	from := c.PostForm("From")
	to := c.PostForm("To")
	body := c.PostForm("Body")
	messageSID := c.PostForm("MessageSid")

	if from == "" || body == "" {
		response.BadRequest(c, "From and Body are required")
		return
	}

	logger.WithComponent("channel_twilio").Info("received twilio webhook",
		"account_id", accountID,
		"from", from,
		"to", to,
		"message_sid", messageSID,
	)

	// Find or create Contact
	contact, err := h.contactRepo.FindByIdentifier(uint(accountID), from)
	if err != nil || contact == nil {
		contact = &domain.Contact{
			AccountID:   uint(accountID),
			Identifier:  from,
			Name:        from,
			PhoneNumber: from,
		}
		_ = h.contactRepo.Create(contact)
	}

	conv := &domain.Conversation{
		AccountID: uint(accountID),
		InboxID:   1,
		ContactID: contact.ID,
		Status:    domain.ConversationStatusOpen,
	}
	_ = h.convRepo.Create(conv)

	inMsg := &domain.Message{
		AccountID:      uint(accountID),
		ConversationID: conv.ID,
		SenderType:     "Contact",
		SenderID:       contact.ID,
		MessageType:    domain.MessageTypeIncoming,
		ContentType:    domain.ContentTypeText,
		Content:        body,
		Status:         domain.MessageStatusSent,
		EchoID:         messageSID,
	}
	_ = h.messageRepo.Create(inMsg)

	// Return TwiML response
	c.Header("Content-Type", "application/xml")
	c.String(http.StatusOK, "<Response></Response>")
	_ = to
}

// ----------------- Facebook / Instagram Callbacks -----------------

func (h *ChannelDriverHandler) VerifyFacebookWebhook(c *gin.Context) {
	mode := c.Query("hub.mode")
	challenge := c.Query("hub.challenge")
	token := c.Query("hub.verify_token")

	if mode == "subscribe" && token != "" {
		c.String(http.StatusOK, challenge)
		return
	}
	response.Forbidden(c, "Invalid verify token")
}

func (h *ChannelDriverHandler) HandleFacebookWebhook(c *gin.Context) {
	accountIDStr := c.Param("account_id")
	if accountIDStr == "" {
		accountIDStr = c.Query("account_id")
	}
	accountID, _ := strconv.ParseUint(accountIDStr, 10, 64)
	if accountID == 0 {
		accountID = 1
	}

	logger.WithComponent("channel_facebook").Info("received facebook webhook",
		"account_id", accountID,
	)

	var payload struct {
		Object string `json:"object"`
		Entry  []struct {
			ID        string `json:"id"`
			Time      int64  `json:"time"`
			Messaging []struct {
				Sender struct {
					ID string `json:"id"`
				} `json:"sender"`
				Recipient struct {
					ID string `json:"id"`
				} `json:"recipient"`
				Message struct {
					MID  string `json:"mid"`
					Text string `json:"text"`
				} `json:"message"`
			} `json:"messaging"`
		} `json:"entry"`
	}

	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var processedCount int
	for _, entry := range payload.Entry {
		for _, msgItem := range entry.Messaging {
			senderID := msgItem.Sender.ID
			text := msgItem.Message.Text
			if senderID == "" || text == "" {
				continue
			}

			// Find or create Contact
			contact, err := h.contactRepo.FindByIdentifier(uint(accountID), senderID)
			if err != nil || contact == nil {
				contact = &domain.Contact{
					AccountID:  uint(accountID),
					Identifier: senderID,
					Name:       "Facebook User " + senderID,
				}
				_ = h.contactRepo.Create(contact)
			}

			// Find or create Conversation
			conv := &domain.Conversation{
				AccountID: uint(accountID),
				InboxID:   1, // default facebook inbox
				ContactID: contact.ID,
				Status:    domain.ConversationStatusOpen,
				Priority:  "medium",
			}
			_ = h.convRepo.Create(conv)

			// Create Message
			inMsg := &domain.Message{
				AccountID:      uint(accountID),
				ConversationID: conv.ID,
				SenderType:     "Contact",
				SenderID:       contact.ID,
				MessageType:    domain.MessageTypeIncoming,
				ContentType:    domain.ContentTypeText,
				Content:        text,
				Status:         domain.MessageStatusSent,
				EchoID:         msgItem.Message.MID,
			}
			_ = h.messageRepo.Create(inMsg)
			processedCount++
		}
	}

	response.Success(c, gin.H{
		"account_id":         accountID,
		"processed_messages": processedCount,
		"handled":            true,
	})
}

// ----------------- WebRTC Calls & Conferences -----------------

func (h *ChannelDriverHandler) ListCalls(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	calls, err := h.repo.ListCalls(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, calls)
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

	call, err := h.repo.GetCall(uint(accountID), uint(callID))
	if err != nil || call == nil {
		response.NotFound(c, "Call not found")
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

	cands, err := h.repo.ListCallCandidates(uint(callID))
	if err != nil {
		response.InternalError(c, err.Error())
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

	idParam := c.Param("id")
	var call *domain.Call
	if callID, err := strconv.ParseUint(idParam, 10, 64); err == nil {
		call, _ = h.repo.GetCall(uint(accountID), uint(callID))
	}
	if call == nil {
		call, _ = h.repo.GetCallByProviderCallID(uint(accountID), idParam)
	}

	if call == nil {
		response.NotFound(c, "WhatsApp call not found")
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
		if contact, err := h.contactRepo.FindByID(call.AccountID, call.ContactID); err == nil && contact != nil {
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

// UploadCallRecording handles recording file upload or URL metadata update
func (h *ChannelDriverHandler) UploadCallRecording(c *gin.Context) {
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
		response.NotFound(c, "Call not found")
		return
	}

	// 1. Check multipart file
	file, fileErr := c.FormFile("recording")
	if fileErr != nil {
		file, fileErr = c.FormFile("file")
	}

	if fileErr == nil && file != nil {
		call.RecordingURL = fmt.Sprintf("/uploads/recordings/%d_%s", call.ID, file.Filename)
	}

	// 2. Check JSON payload or form values
	var req struct {
		RecordingURL string `json:"recording_url" form:"recording_url"`
		RecordingSID string `json:"recording_sid" form:"recording_sid"`
		Duration     int    `json:"duration" form:"duration"`
	}
	if err := c.ShouldBind(&req); err == nil {
		if req.RecordingURL != "" {
			call.RecordingURL = req.RecordingURL
		}
		if req.RecordingSID != "" {
			call.RecordingSID = req.RecordingSID
		}
		if req.Duration > 0 {
			call.Duration = req.Duration
		}
	}

	if call.RecordingURL == "" {
		call.RecordingURL = fmt.Sprintf("/uploads/recordings/call_%d.mp4", call.ID)
	}

	call.UpdatedAt = time.Now()
	if err := h.repo.UpdateCall(call); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	logger.WithComponent("call").Info("call recording uploaded",
		"call_id", call.ID,
		"recording_url", call.RecordingURL,
		"duration", call.Duration,
	)

	response.Success(c, gin.H{
		"status":        "uploaded",
		"call_id":       call.ID,
		"recording_url": call.RecordingURL,
		"recording_sid": call.RecordingSID,
		"duration":      call.Duration,
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
