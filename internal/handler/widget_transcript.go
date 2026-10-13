package handler

import (
	"context"
	"errors"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *ConversationHandler) SetWidgetTranscriptQueue(queue domain.WidgetTranscriptQueue, revocations interface{ IsTokenRevoked(string) (bool, error) }) {
	h.widgetTranscripts, h.widgetTranscriptRevocations = queue, revocations
}

// Recheck the same complete authority as Widget POST, including every supplied
// revocation alias, immediately before private lookups and lost-ack recovery.
// The callback closes over verified identity, never unsigned source hints.
func (h *ConversationHandler) widgetTranscriptAuthority(c *gin.Context, websiteToken string, req domain.WidgetTranscriptRequest) func(context.Context) error {
	tokens := append([]string{}, c.QueryArray("visitor_session")...)
	tokens = append(tokens, c.QueryArray("visitor_token")...)
	tokens = append(tokens, c.Request.Header.Values("X-Visitor-Session")...)
	tokens = append(tokens, c.Request.Header.Values("X-Visitor-Token")...)
	session := visitorSessionToken(c)
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return domain.ErrAccountTranscriptUnavailable
		}
		if h.widgetTranscriptRevocations == nil {
			return domain.ErrAccountTranscriptUnavailable
		}
		for _, token := range tokens {
			if token == "" {
				continue
			}
			if len(token) > 16384 {
				return domain.ErrWidgetTranscriptForbidden
			}
			revoked, err := h.widgetTranscriptRevocations.IsTokenRevoked(token)
			if err != nil {
				return domain.ErrAccountTranscriptUnavailable
			}
			if revoked {
				return domain.ErrWidgetTranscriptForbidden
			}
		}
		claims, err := auth.ParseVisitorToken(session, h.jwtSecret)
		if err != nil || claims.InboxID != req.InboxID || claims.ContactID != req.ContactID || claims.SourceID != req.SourceID {
			return domain.ErrWidgetTranscriptForbidden
		}
		inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
		if err != nil {
			return domain.ErrAccountTranscriptUnavailable
		}
		if inbox == nil || inbox.AccountID != req.AccountID || inbox.ID != req.InboxID {
			return domain.ErrWidgetTranscriptForbidden
		}
		contact, err := h.contactRepo.FindContactBySourceID(inbox.ID, claims.SourceID)
		if err != nil {
			return domain.ErrAccountTranscriptUnavailable
		}
		if contact == nil || contact.ID != req.ContactID || contact.AccountID != req.AccountID {
			return domain.ErrWidgetTranscriptForbidden
		}
		conv, err := h.convRepo.FindByID(req.AccountID, req.ConversationID)
		if err != nil {
			return domain.ErrAccountTranscriptUnavailable
		}
		if conv == nil || conv.InboxID != req.InboxID || conv.ContactID != req.ContactID {
			return domain.ErrAccountTranscriptNotFound
		}
		return ctx.Err()
	}
}

func widgetTranscriptError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrWidgetTranscriptForbidden) {
		response.Forbidden(c, err.Error())
		return
	}
	accountTranscriptError(c, err)
}

// WidgetTranscriptStatus has the exact current visitor authority of POST. A
// guessed log or a valid key without that authority grants no access.
func (h *ConversationHandler) WidgetTranscriptStatus(c *gin.Context) {
	websiteToken := c.Query("website_token")
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Auth-Token")
	}
	if websiteToken == "" {
		websiteToken = c.GetHeader("X-Website-Token")
	}
	inbox, err := h.inboxRepo.FindByWebsiteToken(websiteToken)
	if err != nil || inbox == nil {
		response.NotFound(c, "Invalid website token")
		return
	}
	identity, err := requireWidgetVisitor(c, inbox, h.contactRepo, h.jwtSecret)
	if err != nil {
		return
	}
	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || conversationID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}
	conv, err := h.convRepo.FindByID(inbox.AccountID, uint(conversationID))
	if err != nil || conv == nil || conv.InboxID != inbox.ID || conv.ContactID != identity.contact.ID {
		response.NotFound(c, "Conversation not found")
		return
	}
	logID, err := strconv.ParseUint(c.Param("email_log_id"), 10, 64)
	if err != nil || logID == 0 || len(c.Request.Header.Values("Idempotency-Key")) != 1 {
		accountTranscriptError(c, domain.ErrAccountTranscriptInvalid)
		return
	}
	if h.widgetTranscripts == nil {
		accountTranscriptError(c, domain.ErrAccountTranscriptNotFound)
		return
	}
	req := domain.WidgetTranscriptRequest{AccountID: inbox.AccountID, InboxID: inbox.ID, ContactID: identity.contact.ID, SourceID: identity.claims.SourceID, ConversationID: conv.ID, Key: c.GetHeader("Idempotency-Key")}
	result, err := h.widgetTranscripts.Status(c.Request.Context(), req, uint(logID), h.widgetTranscriptAuthority(c, websiteToken, req))
	if err != nil {
		widgetTranscriptError(c, err)
		return
	}
	response.Success(c, result)
}
