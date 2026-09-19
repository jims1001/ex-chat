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
	optionalEnterpriseRepo ...repository.ChannelAuthEnterpriseRepository,
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
				if revoked, err := optionalEnterpriseRepo[0].IsTokenRevoked(token); err == nil && revoked {
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

			if cStr := c.Query("conversation_id"); cStr != "" {
				if id, err := strconv.ParseUint(cStr, 10, 64); err == nil {
					targetConvID := uint(id)
					// Verify conversation belongs to this inbox and account
					conv, err := convRepo.FindForInbox(inbox.AccountID, inbox.ID, targetConvID)
					if err != nil || conv == nil {
						log.Warn("websocket visitor auth failed: conversation does not belong to inbox",
							"conversation_id", targetConvID,
							"inbox_id", inbox.ID,
							"client_ip", c.ClientIP(),
						)
						c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "conversation not found for this inbox"})
						return
					}

					// Verify visitor token or contact token ownership if provided
					visToken := c.Query("visitor_token")
					if visToken == "" {
						visToken = c.Query("contact_token")
					}
					if visToken != "" {
						claims, err := auth.ParseVisitorToken(visToken, cfg.JWTSecret)
						if err == nil && claims != nil {
							if claims.InboxID != inbox.ID || (claims.ContactID > 0 && claims.ContactID != conv.ContactID) {
								log.Warn("websocket visitor auth failed: visitor token mismatch",
									"conv_contact_id", conv.ContactID,
									"token_contact_id", claims.ContactID,
									"client_ip", c.ClientIP(),
								)
								c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden: visitor token mismatch"})
								return
							}
						}
					}

					conversationID = targetConvID
				}
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
