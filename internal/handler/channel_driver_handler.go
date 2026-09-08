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
