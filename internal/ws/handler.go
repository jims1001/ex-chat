package ws

import (
	"net/http"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow cross-origin for chat widgets and dashboards
	},
}

func ServeWS(
	hub *Hub,
	cfg *config.Config,
	userRepo *repository.UserRepository,
	accountRepo *repository.AccountRepository,
	inboxRepo *repository.InboxRepository,
	convRepo *repository.ConversationRepository,
	contactRepo *repository.ContactRepository,
	optionalEnterpriseRepo ...repository.ChannelIAMRepository,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		log := logger.WithComponent("websocket")
		token := c.Query("token")
		websiteToken := c.Query("website_token")

		var isAgent bool
		var accountID uint
		var userID uint
		var conversationID uint

		if token != "" {
			if len(optionalEnterpriseRepo) > 0 && optionalEnterpriseRepo[0] != nil {
				revoked, err := optionalEnterpriseRepo[0].IsTokenRevoked(token)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication unavailable"})
					return
				}
				if revoked {
					log.Warn("websocket agent auth failed: token revoked", "client_ip", c.ClientIP())
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
					return
				}
			}

			claims, err := auth.ValidateToken(token, cfg.JWTSecret)
			if err != nil {
				log.Warn("websocket agent auth failed", "error", err.Error(), "client_ip", c.ClientIP())
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
				return
			}
			if userRepo == nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication unavailable"})
				return
			}
			user, err := userRepo.FindByID(claims.UserID)
			if err != nil || !auth.ClaimsMatchUser(claims, user) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user no longer exists"})
				return
			}
			isAgent = true
			userID = claims.UserID

			// Fetch accounts this user belongs to for tenant isolation
			userAccounts, err := accountRepo.ListAccountsForUser(userID)
			if err != nil || len(userAccounts) == 0 {
				log.Warn("websocket agent auth failed: user does not belong to any account", "user_id", userID, "client_ip", c.ClientIP())
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user does not belong to any account"})
				return
			}

			if aStr := c.Query("account_id"); aStr != "" {
				if id, err := strconv.ParseUint(aStr, 10, 64); err == nil {
					targetAccID := uint(id)
					var belongs bool
					for _, a := range userAccounts {
						if a.ID == targetAccID {
							belongs = true
							break
						}
					}
					if !belongs {
						log.Warn("websocket agent auth failed: user does not belong to requested account",
							"user_id", userID,
							"requested_account_id", targetAccID,
							"client_ip", c.ClientIP(),
						)
						c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden: user does not belong to account"})
						return
					}
					accountID = targetAccID
				} else {
					accountID = userAccounts[0].ID
				}
			} else {
				accountID = userAccounts[0].ID
			}
		} else if websiteToken != "" {
			inbox, err := inboxRepo.FindByWebsiteToken(websiteToken)
			if err != nil || inbox == nil {
				log.Warn("websocket visitor auth failed: invalid website token", "website_token", websiteToken, "client_ip", c.ClientIP())
				c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "invalid website token"})
				return
			}
			isAgent = false
			accountID = inbox.AccountID

			// Visitor subscriptions always require a session credential. Upload
			// capabilities and legacy source IDs are not subscription credentials.
			visToken := ""
			found := false
			for _, name := range []string{"visitor_session", "visitor_token"} {
				if values, exists := c.Request.URL.Query()[name]; exists {
					found = true
					if len(values) > 0 {
						visToken = values[0]
					}
					break
				}
			}
			if !found {
				for _, name := range []string{"X-Visitor-Session", "X-Visitor-Token"} {
					if values, exists := c.Request.Header[http.CanonicalHeaderKey(name)]; exists {
						found = true
						if len(values) > 0 {
							visToken = values[0]
						}
						break
					}
				}
			}
			if !found {
				visToken = c.Query("contact_token")
			}

			claims, err := auth.ParseVisitorToken(visToken, cfg.JWTSecret)
			if err != nil || claims.InboxID != inbox.ID {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "valid visitor session required"})
				return
			}

			if contactRepo == nil || len(optionalEnterpriseRepo) == 0 || optionalEnterpriseRepo[0] == nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "visitor authentication unavailable"})
				return
			}
			revoked, err := optionalEnterpriseRepo[0].IsTokenRevoked(visToken)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "visitor authentication unavailable"})
				return
			}
			if revoked {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "visitor session revoked"})
				return
			}
			contact, err := contactRepo.FindContactBySourceID(inbox.ID, claims.SourceID)
			if err != nil || contact == nil || contact.ID != claims.ContactID || contact.AccountID != inbox.AccountID {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "visitor session does not match current contact"})
				return
			}

			if cStr := c.Query("conversation_id"); cStr != "" {
				id, err := strconv.ParseUint(cStr, 10, 64)
				if err != nil || id == 0 || uint64(uint(id)) != id {
					c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
					return
				}
				targetConvID := uint(id)
				conv, err := convRepo.FindForInbox(inbox.AccountID, inbox.ID, targetConvID)
				if err != nil || conv == nil {
					c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "conversation not found for this inbox"})
					return
				}
				if claims.ContactID != conv.ContactID {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden: visitor token mismatch"})
					return
				}
				conversationID = targetConvID
			}
		} else {
			log.Warn("websocket connection rejected: token or website_token required", "client_ip", c.ClientIP())
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "token or website_token required"})
			return
		}

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Error("websocket upgrade failed", "error", err.Error(), "client_ip", c.ClientIP())
			return
		}

		log.Info("websocket client connected",
			"is_agent", isAgent,
			"account_id", accountID,
			"user_id", userID,
			"conversation_id", conversationID,
			"client_ip", c.ClientIP(),
		)

		client := NewClient(hub, conn, isAgent, accountID, userID, conversationID)
		hub.register <- client

		go client.WritePump()
		go client.ReadPump()
	}
}
