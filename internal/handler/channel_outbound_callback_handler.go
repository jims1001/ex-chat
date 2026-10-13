package handler

import (
	"encoding/json"
	"fmt"
	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *ChannelDriverHandler) SetChannelOutboundService(s *service.ChannelOutboundService) {
	h.channelOutbound = s
}

func (h *ChannelDriverHandler) HandleTwilioStatus(c *gin.Context) {
	accountID, ok := channelAccount(c)
	if !ok {
		return
	}
	if c.Request.URL.ForceQuery || c.Request.URL.EscapedPath() != fmt.Sprintf("/api/v1/accounts/%d/twilio/status/%s", accountID, c.Param("attempt_id")) || !service.ValidOutboundStatusPath(c.Request.URL.EscapedPath(), c.Request.URL.RawQuery, c.Param("attempt_id")) || len(c.Request.Header.Values("X-Twilio-Signature")) != 1 {
		channelError(c, channel.ErrUnauthorized)
		return
	}
	raw, ok := readChannelBody(c, "application/x-www-form-urlencoded")
	if !ok {
		return
	}
	if err := h.channelOutbound.IngestTwilioStatus(c.Request.Context(), accountID, c.Param("attempt_id"), c.GetHeader("X-Twilio-Signature"), raw); err != nil {
		channelError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func hasWhatsAppStatuses(raw []byte) bool {
	var body struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Statuses []json.RawMessage `json:"statuses"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	for _, entry := range body.Entry {
		for _, change := range entry.Changes {
			if len(change.Value.Statuses) > 0 {
				return true
			}
		}
	}
	return false
}
