package handler

import (
	"errors"
	"github.com/OracleBetX-Projects/ex-chat/internal/channel"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"strconv"
)

func (h *ConversationHandler) ReconcileMessage(c *gin.Context) {
	accountID := c.GetUint("account_id")
	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || conversationID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}
	messageID, err := strconv.ParseUint(c.Param("message_id"), 10, 32)
	if err != nil || messageID == 0 {
		response.BadRequest(c, "Invalid message ID")
		return
	}
	msg, err := h.channelOutbound.ReconcileMessage(c.Request.Context(), accountID, uint(conversationID), uint(messageID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.NotFound(c, "Message not found")
		return
	}
	if err != nil {
		channelError(c, err)
		return
	}
	if msg == nil {
		response.NotFound(c, "Message not found")
		return
	}
	response.Success(c, msg)
}

func (h *ConversationHandler) SetChannelOutboundService(s *service.ChannelOutboundService) {
	h.channelOutbound = s
}

func (h *ConversationHandler) createOrdinaryMessage(c *gin.Context, conv *domain.Conversation, msg *domain.Message) (bool, error) {
	if msg.Private || msg.MessageType != domain.MessageTypeOutgoing {
		msg.Status = domain.MessageStatusSaved
		return true, h.msgRepo.Create(msg)
	}
	inbox, err := h.inboxRepo.FindSummaryByID(msg.AccountID, conv.InboxID)
	if err != nil {
		return false, err
	}
	if inbox == nil {
		return false, channel.ErrInvalid
	}
	if service.SupportedTextChannel(inbox.ChannelType) {
		if h.channelOutbound == nil {
			return false, channel.ErrNotConfigured
		}
		keys := c.Request.Header.Values("Idempotency-Key")
		if len(keys) > 1 {
			return false, channel.ErrInvalid
		}
		return h.channelOutbound.CreateText(c.Request.Context(), msg, c.GetHeader("Idempotency-Key"))
	}
	// Website/API conversations retain their existing local real-time path.
	// Other providers cannot silently claim a send through this endpoint.
	if !localRealtimeChannel(inbox.ChannelType) {
		return false, channel.ErrUnsupported
	}
	return true, h.msgRepo.Create(msg)
}

func (h *ConversationHandler) retryOrdinaryMessage(c *gin.Context, accountID, conversationID, messageID uint, msg *domain.Message) (*domain.Message, bool, error) {
	if msg.Private || msg.MessageType != domain.MessageTypeOutgoing {
		return h.msgRepo.RetryMessageAccepted(c.Request.Context(), accountID, conversationID, messageID)
	}
	conv, err := h.convRepo.FindByID(accountID, conversationID)
	if err != nil {
		return nil, false, err
	}
	if conv == nil {
		return nil, false, channel.ErrInvalid
	}
	inbox, err := h.inboxRepo.FindSummaryByID(accountID, conv.InboxID)
	if err != nil {
		return nil, false, err
	}
	if inbox == nil {
		return nil, false, channel.ErrInvalid
	}
	if service.SupportedTextChannel(inbox.ChannelType) {
		if h.channelOutbound == nil {
			return nil, false, channel.ErrNotConfigured
		}
		return h.channelOutbound.RetryText(c.Request.Context(), accountID, conversationID, messageID)
	}
	if !localRealtimeChannel(inbox.ChannelType) {
		return nil, false, channel.ErrUnsupported
	}
	return h.msgRepo.RetryMessageAccepted(c.Request.Context(), accountID, conversationID, messageID)
}

func localRealtimeChannel(kind string) bool {
	return kind == "Channel::WebWidget" || kind == "Channel::Api" || kind == "Channel::Website" || kind == "web_widget"
}
