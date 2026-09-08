package router

import (
	"net/http"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetupRouter(cfg *config.Config, db *gorm.DB, hub *ws.Hub) *gin.Engine {
	r := gin.Default()

	// CORS middleware
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Auth-Token")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// Repositories
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	contactRepo := repository.NewContactRepository(db)
	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	labelRepo := repository.NewLabelRepository(db)
	cannedRepo := repository.NewCannedResponseRepository(db)

	// Services
	routingService := service.NewRoutingService(db, convRepo, hub)
	reportService := service.NewReportService(db)

	// Handlers
	authHandler := handler.NewAuthHandler(cfg, userRepo, accountRepo)
	accountHandler := handler.NewAccountHandler(accountRepo, userRepo)
	inboxHandler := handler.NewInboxHandler(inboxRepo, userRepo)
	contactHandler := handler.NewContactHandler(contactRepo, inboxRepo)
	convHandler := handler.NewConversationHandler(convRepo, msgRepo, inboxRepo, contactRepo, routingService, hub)
	opsHandler := handler.NewOpsHandler(labelRepo, cannedRepo, convRepo)
	reportHandler := handler.NewReportHandler(reportService)

	// 1. Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "ex-chat"})
	})

	// 2. Real-Time WebSocket endpoint
	r.GET("/cable", ws.ServeWS(hub, cfg, userRepo, accountRepo, inboxRepo))
	r.GET("/ws", ws.ServeWS(hub, cfg, userRepo, accountRepo, inboxRepo))

	// 3. Public Widget Endpoints
	widget := r.Group("/api/v1/widget")
	{
		widget.GET("/config", inboxHandler.WidgetConfig)
		widget.POST("/contact", contactHandler.WidgetIdentify)
		widget.POST("/conversations", convHandler.WidgetCreateConversation)
		widget.GET("/messages", convHandler.WidgetListMessages)
		widget.POST("/messages", convHandler.WidgetCreateMessage)
	}

	// 4. Public Auth Endpoints
	authGroup := r.Group("/auth")
	{
		authGroup.POST("/sign_up", authHandler.SignUp)
		authGroup.POST("/sign_in", authHandler.SignIn)
	}

	// 5. Authenticated Endpoints
	api := r.Group("/")
	api.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		api.GET("/api/v1/profile", authHandler.Profile)
		api.POST("/api/v1/profile/availability", authHandler.UpdateAvailability)
		api.POST("/api/v1/accounts", accountHandler.CreateAccount)

		// Tenant-scoped endpoints
		tenant := api.Group("/api/v1/accounts/:account_id")
		tenant.Use(middleware.TenantMiddleware(accountRepo))
		{
			// Account & Agent Management
			tenant.GET("", accountHandler.GetAccount)
			tenant.PUT("", accountHandler.UpdateAccount)
			tenant.GET("/agents", accountHandler.ListAgents)
			tenant.POST("/agents", accountHandler.AddAgent)

			// Inboxes
			tenant.GET("/inboxes", inboxHandler.ListInboxes)
			tenant.POST("/inboxes", inboxHandler.CreateInbox)
			tenant.GET("/inboxes/:id", inboxHandler.GetInbox)
			tenant.PUT("/inboxes/:id", inboxHandler.UpdateInbox)
			tenant.DELETE("/inboxes/:id", inboxHandler.DeleteInbox)
			tenant.GET("/inboxes/:id/members", inboxHandler.ListInboxMembers)
			tenant.POST("/inboxes/:id/members", inboxHandler.AddInboxMembers)

			// Contacts
			tenant.GET("/contacts", contactHandler.ListContacts)
			tenant.POST("/contacts", contactHandler.CreateContact)
			tenant.GET("/contacts/:id", contactHandler.GetContact)
			tenant.PUT("/contacts/:id", contactHandler.UpdateContact)
			tenant.DELETE("/contacts/:id", contactHandler.DeleteContact)
			tenant.POST("/actions/contact_merge", contactHandler.MergeContact)

			// Conversations & Messaging
			tenant.GET("/conversations", convHandler.ListConversations)
			tenant.POST("/conversations", convHandler.CreateConversation)
			tenant.GET("/conversations/:id", convHandler.GetConversation)
			tenant.POST("/conversations/:id/toggle_status", convHandler.ToggleStatus)
			tenant.POST("/conversations/:id/assignments", convHandler.Assign)
			tenant.GET("/conversations/:id/messages", convHandler.ListMessages)
			tenant.POST("/conversations/:id/messages", convHandler.CreateMessage)

			// Canned Responses
			tenant.GET("/canned_responses", opsHandler.ListCannedResponses)
			tenant.POST("/canned_responses", opsHandler.CreateCannedResponse)
			tenant.PUT("/canned_responses/:id", opsHandler.UpdateCannedResponse)
			tenant.DELETE("/canned_responses/:id", opsHandler.DeleteCannedResponse)

			// Labels
			tenant.GET("/labels", opsHandler.ListLabels)
			tenant.POST("/labels", opsHandler.CreateLabel)
			tenant.DELETE("/labels/:id", opsHandler.DeleteLabel)
			tenant.POST("/conversations/:id/labels", opsHandler.AttachConversationLabels)
			tenant.GET("/conversations/:id/labels", opsHandler.GetConversationLabels)
		}

		// Reports (V2 API)
		reports := api.Group("/api/v2/accounts/:account_id/reports")
		reports.Use(middleware.TenantMiddleware(accountRepo))
		{
			reports.GET("/summary", reportHandler.GetSummary)
			reports.GET("/agents", reportHandler.GetAgentMetrics)
		}
	}

	return r
}
