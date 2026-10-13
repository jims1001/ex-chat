package handler

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *ChannelDriverHandler) SetChannelIngressService(s *service.ChannelIngressService) {
	h.channelIngress = s
}

func channelAccount(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("account_id"), 10, 32)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return uint(id), true
}

func channelError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, channel.ErrNotConfigured):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Channel integration is not configured"})
	case errors.Is(err, channel.ErrUnauthorized):
		response.Forbidden(c, "Channel authentication failed")
	case errors.Is(err, channel.ErrUnsupported):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Only supported text channel operations are accepted"})
	case errors.Is(err, channel.ErrInvalid):
		response.BadRequest(c, "Invalid channel request")
	case errors.Is(err, repository.ErrOutboundConflict), errors.Is(err, repository.ErrOutboundClaimLost):
		c.JSON(http.StatusConflict, gin.H{"error": "Outbound message conflicts with its frozen send intent"})
	case errors.Is(err, repository.ErrChannelReplayConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "Provider event conflicts with an existing receipt"})
	case errors.Is(err, channel.ErrProvider):
		c.JSON(http.StatusBadGateway, gin.H{"error": "Provider verification failed"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Channel persistence unavailable; retry later"})
	}
}

func readChannelBody(c *gin.Context, contentType string) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != contentType || c.Request.URL.RawQuery != "" {
		response.BadRequest(c, "Invalid callback content type or query")
		return nil, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, channel.MaxWebhookBytes)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Invalid or oversized channel request"})
		return nil, false
	}
	return raw, true
}

func (h *ChannelDriverHandler) VerifyWhatsAppWebhook(c *gin.Context) {
	accountID, ok := channelAccount(c)
	if !ok {
		return
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	for _, name := range []string{"hub.mode", "hub.verify_token", "hub.challenge"} {
		if len(values[name]) != 1 {
			err = channel.ErrUnauthorized
		}
	}
	if err == nil {
		err = h.channelIngress.VerifyWhatsApp(c.Request.Context(), accountID, c.Query("hub.mode"), c.Query("hub.verify_token"), c.Query("hub.challenge"))
	}
	if err != nil {
		channelError(c, err)
		return
	}
	c.String(http.StatusOK, c.Query("hub.challenge"))
}

func (h *ChannelDriverHandler) HandleWhatsAppWebhook(c *gin.Context) {
	accountID, ok := channelAccount(c)
	if !ok {
		return
	}
	if err := h.channelIngress.Ready("whatsapp"); err != nil {
		channelError(c, err)
		return
	}
	if len(c.Request.Header.Values("X-Hub-Signature-256")) != 1 {
		channelError(c, channel.ErrUnauthorized)
		return
	}
	raw, ok := readChannelBody(c, "application/json")
	if !ok {
		return
	}
	if hasWhatsAppStatuses(raw) {
		if err := h.channelOutbound.IngestWhatsAppStatus(c.Request.Context(), accountID, c.GetHeader("X-Hub-Signature-256"), raw); err != nil {
			channelError(c, err)
			return
		}
		response.Success(c, gin.H{"status": "success"})
		return
	}
	count, err := h.channelIngress.IngestWhatsApp(c.Request.Context(), accountID, c.GetHeader("X-Hub-Signature-256"), raw)
	if err != nil {
		channelError(c, err)
		return
	}
	response.Success(c, gin.H{"processed_messages": count, "status": "success"})
}

func (h *ChannelDriverHandler) HandleTwilioWebhook(c *gin.Context) {
	accountID, ok := channelAccount(c)
	if !ok {
		return
	}
	if err := h.channelIngress.Ready("twilio"); err != nil {
		channelError(c, err)
		return
	}
	if len(c.Request.Header.Values("X-Twilio-Signature")) != 1 {
		channelError(c, channel.ErrUnauthorized)
		return
	}
	raw, ok := readChannelBody(c, "application/x-www-form-urlencoded")
	if !ok {
		return
	}
	_, err := h.channelIngress.IngestTwilio(c.Request.Context(), accountID, c.GetHeader("X-Twilio-Signature"), raw)
	if err != nil {
		channelError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/xml", []byte("<Response></Response>"))
}

func (h *ChannelDriverHandler) WhatsAppAuthorization(c *gin.Context) {
	accountID, ok := channelAccount(c)
	if !ok {
		return
	}
	if err := h.channelIngress.Ready("whatsapp"); err != nil {
		channelError(c, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32768)
	var req service.WhatsAppSetup
	if err := c.ShouldBindJSON(&req); err != nil {
		channelError(c, channel.ErrInvalid)
		return
	}
	result, err := h.channelIngress.AuthorizeWhatsApp(c.Request.Context(), accountID, req)
	if err != nil {
		channelError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *ChannelDriverHandler) CreateTwilioChannel(c *gin.Context) {
	accountID, ok := channelAccount(c)
	if !ok {
		return
	}
	if err := h.channelIngress.Ready("twilio"); err != nil {
		channelError(c, err)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32768)
	var req service.TwilioSetup
	if err := c.ShouldBindJSON(&req); err != nil {
		channelError(c, channel.ErrInvalid)
		return
	}
	result, err := h.channelIngress.CreateTwilio(c.Request.Context(), accountID, req)
	if err != nil {
		channelError(c, err)
		return
	}
	response.Created(c, result)
}
