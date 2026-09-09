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
			claims, err := auth.ValidateToken(token, cfg.JWTSecret)
			if err != nil {
				log.Warn("websocket agent auth failed", "error", err.Error(), "client_ip", c.ClientIP())
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
				return
			}
			isAgent = true
			userID = claims.UserID

			if aStr := c.Query("account_id"); aStr != "" {
				if id, err := strconv.ParseUint(aStr, 10, 64); err == nil {
					accountID = uint(id)
				}
			} else {
				// Default to first account user belongs to
				accounts, _ := accountRepo.ListAccountsForUser(userID)
				if len(accounts) > 0 {
					accountID = accounts[0].ID
				}
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
					conversationID = uint(id)
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
