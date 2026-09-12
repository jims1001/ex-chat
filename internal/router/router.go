package router

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var globalWebhookHTTPClient *http.Client
var globalAdvancedHTTPClient *http.Client

func SetWebhookHTTPClient(client *http.Client) {
	globalWebhookHTTPClient = client
}

func SetAdvancedHTTPClient(client *http.Client) {
	globalAdvancedHTTPClient = client
}

func SetupRouter(cfg *config.Config, db *gorm.DB, hub *ws.Hub) *gin.Engine {
	r := gin.New()

	// 1. 全局拦截器：统一上下文与全链路跟踪 (API-03 规范)
	r.Use(foundation.UnifiedContextMiddleware())
	r.Use(middleware.RequestLogger())
	r.Use(middleware.RecoveryLogger())

	// 2. CORS 跨域治理
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Auth-Token, X-Request-Id, X-Correlation-Id, Idempotency-Key")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// 基础仓储 (Repositories)
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	contactRepo := repository.NewContactRepository(db)
	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	labelRepo := repository.NewLabelRepository(db)
	cannedRepo := repository.NewCannedResponseRepository(db)
	journalRepo := repository.NewJournalRepository(db)
	teamRepo := repository.NewTeamRepository(db)
	customAttrRepo := repository.NewCustomAttributeRepository(db)
	portalRepo := repository.NewPortalRepository(db)
	webhookRepo := repository.NewWebhookRepository(db)
	enterpriseRepo := repository.NewEnterpriseRepository(db)
	macroRepo := repository.NewMacroRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)
	csatRepo := repository.NewCSATRepository(db)
	platformRepo := repository.NewPlatformRepository(db)
	deviceRepo := repository.NewDeviceRepository(db)
	companyRepo := repository.NewCompanyRepository(db)
	campaignRepo := repository.NewCampaignRepository(db)
	slaRepo := repository.NewSLARepository(db)
	agentBotRepo := repository.NewAgentBotRepository(db)
	attachmentRepo := repository.NewAttachmentRepository(db)
	channelEnterpriseRepo := repository.NewChannelAuthEnterpriseRepository(db)
	assignmentPolicyRepo := repository.NewAssignmentPolicyRepository(db)
	agentCapacityPolicyRepo := repository.NewAgentCapacityPolicyRepository(db)
	contactExtensionRepo := repository.NewContactExtensionRepository(db)
	companyExtensionRepo := repository.NewCompanyExtensionRepository(db)
	csatExtensionRepo := repository.NewCSATExtensionRepository(db)
	appliedSLARepo := repository.NewAppliedSLARepository(db)
	copilotThreadRepo := repository.NewCopilotThreadRepository(db)
	captainRepo := repository.NewCaptainRepository(db)
	aiCustomToolRepo := repository.NewAICustomToolRepository(db)
	widgetRepo := repository.NewWidgetRepository(db)
	publicRepo := repository.NewPublicRepository(db)
	dashboardAppRepo := repository.NewDashboardAppRepository(db)

	// 核心业务服务 (Services)
	routingService := service.NewRoutingService(db, convRepo, hub)
	reportService := service.NewReportService(db)
	pushService := service.NewPushService(db, deviceRepo)
	automationService := service.NewAutomationService(db, convRepo, msgRepo)
	webhookService := service.NewWebhookService(db, webhookRepo)
	if globalWebhookHTTPClient != nil {
		webhookService.SetHTTPClient(globalWebhookHTTPClient)
	}
	slaService := service.NewSLAService(db)
	slaService.SetAppliedSLARepo(appliedSLARepo)
	campaignService := service.NewCampaignService(db, convRepo, msgRepo, contactRepo)
	campaignService.SetHub(hub)
	emailService := service.NewEmailService(db)

	// API 处理器 (Handlers)
	authHandler := handler.NewAuthHandler(cfg, userRepo, accountRepo)
	accountHandler := handler.NewAccountHandler(accountRepo, userRepo)
	inboxHandler := handler.NewInboxHandler(inboxRepo, userRepo)
	inboxHandler.SetCaptainRepo(captainRepo)
	inboxHandler.SetCampaignRepo(campaignRepo)
	inboxHandler.SetAgentBotRepo(agentBotRepo)
	inboxHandler.SetContactRepo(contactRepo)
	contactHandler := handler.NewContactHandler(contactRepo, inboxRepo)
	contactHandler.SetExtraRepos(labelRepo, companyRepo, db)
	contactExtensionHandler := handler.NewContactExtensionHandler(contactRepo, contactExtensionRepo)
	companyExtensionHandler := handler.NewCompanyExtensionHandler(companyRepo, companyExtensionRepo)
	convHandler := handler.NewConversationHandler(convRepo, msgRepo, inboxRepo, contactRepo, routingService, hub)
	convHandler.SetAutomationAndWebhook(automationService, webhookService)
	convHandler.SetPushService(pushService)
	convHandler.SetNotificationRepo(notificationRepo)
	convHandler.SetSLAService(slaService)
	convHandler.SetEmailService(emailService)
	convHandler.SetCampaignService(campaignService)
	convHandler.SetCaptainRepo(captainRepo)
	convHandler.SetEnterpriseRepo(channelEnterpriseRepo)
	opsHandler := handler.NewOpsHandler(labelRepo, cannedRepo, convRepo)
	opsHandler.SetEventServices(automationService, webhookService, hub)
	macroHandler := handler.NewMacroNotificationHandler(macroRepo, notificationRepo, csatRepo)
	reportHandler := handler.NewReportHandler(reportService)
	auditHandler := handler.NewAuditHandler(journalRepo)
	teamHandler := handler.NewTeamHandler(teamRepo)
	customAttrHandler := handler.NewCustomAttributeHandler(customAttrRepo)
	portalHandler := handler.NewPortalHandler(portalRepo)
	webhookHandler := handler.NewWebhookHandler(webhookRepo)
	webhookHandler.SetWebhookService(webhookService)
	enterpriseHandler := handler.NewEnterpriseHandler(enterpriseRepo)
	platformHandler := handler.NewPlatformHandler(platformRepo, userRepo)
	platformHandler.SetJWTSecret(cfg.JWTSecret)
	publicHandler := handler.NewPublicHandler(db, inboxRepo, contactRepo, convRepo, msgRepo)
	publicHandler.SetPublicRepository(publicRepo)
	publicHandler.SetAutomationAndWebhook(automationService, webhookService)
	publicHandler.SetPushAndHub(pushService, hub)
	publicHandler.SetCampaignService(campaignService)
	deviceHandler := handler.NewDeviceHandler(deviceRepo)
	advancedHandler := handler.NewAdvancedHandler(db, companyRepo, campaignRepo, slaRepo, agentBotRepo, attachmentRepo)
	advancedHandler.SetServices(campaignService, slaService)
	advancedHandler.SetEventServices(convRepo, routingService, automationService, webhookService, pushService, notificationRepo, hub)
	if globalAdvancedHTTPClient != nil {
		advancedHandler.SetHTTPClient(globalAdvancedHTTPClient)
	}
	channelDriverHandler := handler.NewChannelDriverHandler(channelEnterpriseRepo, inboxRepo, contactRepo, convRepo, msgRepo)
	authEnterpriseHandler := handler.NewAuthEnterpriseHandler(channelEnterpriseRepo, userRepo, accountRepo, portalRepo, contactRepo, cfg)
	migrationService := service.NewMigrationService(db)
	authEnterpriseHandler.SetMigrationService(migrationService)
	dataImportService := service.NewDataImportService(db)
	authEnterpriseHandler.SetDataImportService(dataImportService)
	authHandler.SetEnterpriseRepo(channelEnterpriseRepo)
	copilotHandler := handler.NewCopilotHandler(db, convRepo, msgRepo, portalRepo, cannedRepo)
	searchHandler := handler.NewSearchHandler(db)
	assignmentPolicyHandler := handler.NewAssignmentPolicyHandler(assignmentPolicyRepo)
	agentCapacityPolicyHandler := handler.NewAgentCapacityPolicyHandler(agentCapacityPolicyRepo)
	csatHandler := handler.NewCSATHandler(csatExtensionRepo)
	appliedSLAHandler := handler.NewAppliedSLAHandler(db, appliedSLARepo, slaService)
	copilotThreadHandler := handler.NewCopilotThreadHandler(db, copilotThreadRepo, convRepo, msgRepo, captainRepo)
	aiCustomToolHandler := handler.NewAICustomToolHandler(db, aiCustomToolRepo)
	widgetHandler := handler.NewWidgetHandler(db, widgetRepo, convRepo, contactRepo)
	widgetHandler.SetHub(hub)
	dashboardAppHandler := handler.NewDashboardAppHandler(dashboardAppRepo)

	// 后台周期巡检与调度引擎 (Background Schedulers)
	campaignService.StartScheduledCampaignWorker(context.Background(), 1*time.Minute)
	slaService.StartSLAScheduler(context.Background(), 1*time.Minute)

	// 静态上传资源挂载 (Uploads)
	_ = os.MkdirAll("uploads", 0755)
	r.Static("/uploads", "./uploads")

	// 1. 健康检查与就绪探针
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":             "healthy",
			"service":            "ex-chat",
			"foundation_version": foundation.CurrentFoundationVersion,
		})
	})

	// 1.1 Well-known 移动端深链与系统状态
	r.GET("/.well-known/assetlinks.json", func(c *gin.Context) {
		c.JSON(http.StatusOK, []gin.H{
			{
				"relation": []string{"delegate_permission/common.handle_all_urls"},
				"target": gin.H{
					"namespace":                "android_app",
					"package_name":             "com.chatwoot.app",
					"sha256_cert_fingerprints": []string{"14:6D:E9:83:C5:73:06:50:D8:EE:B9:95:2F:34:FC:64:16:A0:83:42:E6:1D:BE:A8:8A:04:96:B2:3F:CF:44:E5"},
				},
			},
		})
	})
	r.GET("/.well-known/apple-app-site-association", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"applinks": gin.H{
				"apps": []string{},
				"details": []gin.H{
					{
						"appID": "CHATWOOT.com.chatwoot.app",
						"paths": []string{"/app/*"},
					},
				},
			},
		})
	})
	r.GET("/super_admin/instance_status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":   "online",
			"version":  "4.16.0",
			"database": "sqlite",
		})
	})

	// 2. 实时通信 WebSocket 长连接 (RTM)
	r.GET("/cable", ws.ServeWS(hub, cfg, userRepo, accountRepo, inboxRepo))
	r.GET("/ws", ws.ServeWS(hub, cfg, userRepo, accountRepo, inboxRepo))

	// 3. 访客侧 Public Widget 接口
	widget := r.Group("/api/v1/widget")
	{
		widget.GET("/config", inboxHandler.WidgetConfig)
		widget.POST("/config", inboxHandler.WidgetConfigCreate)

		// 访客联系人识别与读取
		widget.GET("/contact", widgetHandler.GetContact)
		widget.POST("/contact", contactHandler.WidgetIdentify)
		widget.PATCH("/contact", widgetHandler.UpdateContact)
		widget.POST("/contact/set_user", widgetHandler.SetUser)
		widget.POST("/set_user", widgetHandler.SetUser)
		widget.POST("/contact/custom_attributes", widgetHandler.MergeContactCustomAttributes)
		widget.POST("/contact/destroy_custom_attributes", widgetHandler.DestroyContactCustomAttributes)
		widget.DELETE("/contact/custom_attributes", widgetHandler.DestroyContactCustomAttributes)

		// 访客会话历史列表与单条会话详情
		widget.GET("/conversations", widgetHandler.ListConversations)
		widget.POST("/conversations", convHandler.WidgetCreateConversation)
		widget.GET("/conversations/:id", widgetHandler.GetConversation)
		widget.PATCH("/conversations/:id/custom_attributes", widgetHandler.MergeConversationCustomAttributes)

		// 消息与实时互动、直接上传
		widget.GET("/messages", convHandler.WidgetListMessages)
		widget.POST("/messages", convHandler.WidgetCreateMessage)
		widget.PATCH("/messages/:id", widgetHandler.UpdateMessage)
		widget.PUT("/messages/:id", widgetHandler.UpdateMessage)
		widget.POST("/direct_uploads", widgetHandler.DirectUpload)
		widget.POST("/conversations/update_last_seen", convHandler.WidgetUpdateLastSeen)
		widget.POST("/conversations/toggle_typing", convHandler.WidgetToggleTyping)
		widget.POST("/conversations/transcript", convHandler.WidgetSendTranscript)

		// 收件箱成员列表
		widget.GET("/inbox_members", widgetHandler.ListInboxMembers)

		// 正在运行的主动营销活动
		widget.GET("/campaigns", widgetHandler.ListCampaigns)

		// 访客行为事件上报与查询
		widget.POST("/events", widgetHandler.RecordEvent)
		widget.GET("/events", widgetHandler.ListEvents)

		// 访客/会话标签管理
		widget.GET("/labels", widgetHandler.GetLabels)
		widget.POST("/labels", widgetHandler.AddLabels)
		widget.DELETE("/labels", widgetHandler.RemoveLabels)
	}

	// 4. 公开帮助中心门户接口 (HELP)
	publicPortals := r.Group("/public/api/v1/portals")
	{
		publicPortals.GET("/:slug", portalHandler.PublicGetPortal)
		publicPortals.GET("/:slug/articles", portalHandler.PublicListArticles)
		publicPortals.GET("/:slug/articles/:article_slug", portalHandler.PublicGetArticle)
	}

	// 4.1 客户侧公开接口 (Public API & CSAT)
	publicGroup := r.Group("/public/api/v1")
	{
		publicGroup.GET("/inboxes/:identifier", publicHandler.GetPublicInbox)

		// 客户联系人详情与更新
		publicGroup.POST("/inboxes/:identifier/contacts", publicHandler.CreateContact)
		publicGroup.GET("/inboxes/:identifier/contacts/:contact_id", publicHandler.GetContact)
		publicGroup.PATCH("/inboxes/:identifier/contacts/:contact_id", publicHandler.UpdateContact)
		publicGroup.PUT("/inboxes/:identifier/contacts/:contact_id", publicHandler.UpdateContact)

		// 会话列表与单条会话详情
		publicGroup.GET("/inboxes/:identifier/contacts/:contact_id/conversations", publicHandler.ListConversations)
		publicGroup.POST("/inboxes/:identifier/contacts/:contact_id/conversations", publicHandler.CreateConversation)
		publicGroup.GET("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id", publicHandler.GetConversation)

		// 历史消息读取与发送
		publicGroup.GET("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id/messages", publicHandler.ListMessages)
		publicGroup.POST("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id/messages", publicHandler.CreateMessage)
		publicGroup.POST("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id/update_last_seen", publicHandler.UpdateLastSeen)

		// 会话状态切换、打字指示与自定义属性更新
		publicGroup.POST("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id/toggle_status", publicHandler.ToggleConversationStatus)
		publicGroup.POST("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id/toggle_typing", publicHandler.ToggleTyping)
		publicGroup.PATCH("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id/custom_attributes", publicHandler.UpdateConversationCustomAttributes)

		// 外部满意度调查与 Webhook 驱动
		publicGroup.POST("/csat_survey/:id", macroHandler.SubmitCSAT)
		publicGroup.GET("/channels/facebook/webhook", channelDriverHandler.VerifyFacebookWebhook)
		publicGroup.POST("/channels/facebook/webhook", channelDriverHandler.HandleFacebookWebhook)
		publicGroup.POST("/subscription/webhook", authEnterpriseHandler.HandleSubscriptionWebhook)
	}

	// 4.2 平台管理开放接口 (Platform API)
	platformGroup := r.Group("/platform/api/v1")
	platformGroup.Use(platformHandler.PlatformAuthMiddleware())
	{
		// 账号全生命周期治理
		platformGroup.GET("/accounts", platformHandler.ListAccounts)
		platformGroup.POST("/accounts", platformHandler.CreateAccount)
		platformGroup.GET("/accounts/:id", platformHandler.GetAccount)
		platformGroup.PATCH("/accounts/:id", platformHandler.UpdateAccount)
		platformGroup.PUT("/accounts/:id", platformHandler.UpdateAccount)
		platformGroup.DELETE("/accounts/:id", platformHandler.DeleteAccount)
		platformGroup.GET("/accounts/:id/status", platformHandler.GetAccountStatus)

		// 用户全生命周期治理
		platformGroup.GET("/users", platformHandler.ListUsers)
		platformGroup.POST("/users", platformHandler.CreateUser)
		platformGroup.GET("/users/:id", platformHandler.GetUser)
		platformGroup.PATCH("/users/:id", platformHandler.UpdateUser)
		platformGroup.PUT("/users/:id", platformHandler.UpdateUser)
		platformGroup.DELETE("/users/:id", platformHandler.DeleteUser)
		platformGroup.GET("/users/:id/login", platformHandler.GetUserLoginToken)
		platformGroup.POST("/users/:id/token", platformHandler.GetUserToken)
		platformGroup.GET("/users/:id/token", platformHandler.GetUserToken)

		// 账号成员关系管理与解绑
		platformGroup.GET("/accounts/:id/account_users", platformHandler.ListAccountUsers)
		platformGroup.POST("/accounts/:id/account_users", platformHandler.AddAccountUser)
		platformGroup.DELETE("/accounts/:id/account_users", platformHandler.DeleteAccountUser)
		platformGroup.DELETE("/accounts/:id/account_users/:user_id", platformHandler.DeleteAccountUser)

		// 平台级与租户级 Bot 治理
		platformGroup.GET("/agent_bots", platformHandler.ListAgentBots)
		platformGroup.POST("/agent_bots", platformHandler.CreateAgentBot)
		platformGroup.GET("/agent_bots/:id", platformHandler.GetAgentBot)
		platformGroup.PATCH("/agent_bots/:id", platformHandler.UpdateAgentBot)
		platformGroup.PUT("/agent_bots/:id", platformHandler.UpdateAgentBot)
		platformGroup.DELETE("/agent_bots/:id", platformHandler.DeleteAgentBot)
		platformGroup.DELETE("/agent_bots/:id/avatar", platformHandler.DeleteAgentBotAvatar)
		platformGroup.GET("/accounts/:id/agent_bots", platformHandler.ListAccountAgentBots)
		platformGroup.POST("/accounts/:id/agent_bots", platformHandler.CreateAccountAgentBot)
		platformGroup.DELETE("/accounts/:id/agent_bots/:agent_bot_id", platformHandler.DeleteAccountAgentBot)

		// 企业级与审计扩展
		platformGroup.POST("/accounts/:id/email_channel_migrations", authEnterpriseHandler.EmailChannelMigration)
		platformGroup.GET("/accounts/:id/data_changes", auditHandler.PlatformListDataChanges)
	}

	// 5. 身份登录与注册接口 (IAM / AUTH)
	authGroup := r.Group("/auth")
	{
		authGroup.POST("/sign_up", authHandler.SignUp)
		authGroup.POST("/sign_in", authHandler.SignIn)
		authGroup.DELETE("/sign_out", authHandler.SignOut)
		authGroup.POST("/sign_out", authHandler.SignOut)
		authGroup.GET("/validate_token", middleware.AuthMiddleware(cfg.JWTSecret, userRepo, channelEnterpriseRepo), authHandler.ValidateToken)
		authGroup.GET("/confirmation", authHandler.ConfirmEmail)
		authGroup.POST("/confirmation", authHandler.ConfirmEmail)
		authGroup.POST("/confirmation/resend", authHandler.ResendConfirmation)
		authGroup.POST("/mfa/verify", authHandler.VerifyMFAForLogin)
		authGroup.POST("/password", authEnterpriseHandler.RequestPasswordReset)
		authGroup.PUT("/password", authEnterpriseHandler.ResetPassword)
	}

	// 5.1 第三方单点登录与 OAuth 回调 (SSO / OAuth)
	r.GET("/omniauth/google_oauth2/callback", authEnterpriseHandler.GoogleOAuthCallback)
	r.POST("/api/v1/auth/saml_login", authEnterpriseHandler.InitiateSAMLLogin)
	r.POST("/omniauth/saml/callback", authEnterpriseHandler.SAMLCallback)

	// 5.2 外部渠道异步回调与握手 (WhatsApp, Twilio, Facebook)
	channelWebhook := r.Group("/api/v1/accounts/:account_id")
	{
		channelWebhook.GET("/whatsapp/callback", channelDriverHandler.VerifyWhatsAppWebhook)
		channelWebhook.POST("/whatsapp/callback", channelDriverHandler.HandleWhatsAppWebhook)
		channelWebhook.POST("/twilio/callback", channelDriverHandler.HandleTwilioWebhook)
		channelWebhook.GET("/callbacks", channelDriverHandler.VerifyFacebookWebhook)
		channelWebhook.POST("/callbacks", channelDriverHandler.HandleFacebookWebhook)
	}

	// 6. 鉴权与租户保护接口
	api := r.Group("/")
	api.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo, channelEnterpriseRepo))
	{
		api.GET("/api/v1/profile", authHandler.Profile)
		api.PUT("/api/v1/profile", authHandler.UpdateProfile)
		api.DELETE("/api/v1/profile/sign_out", authHandler.SignOut)
		api.POST("/api/v1/profile/avatar", authHandler.UploadAvatar)
		api.DELETE("/api/v1/profile/avatar", authHandler.DeleteAvatar)
		api.POST("/api/v1/profile/resend_confirmation", authHandler.ResendConfirmation)
		api.POST("/api/v1/profile/availability", authHandler.UpdateAvailability)
		api.GET("/api/v1/profile/mfa", authEnterpriseHandler.GetMFAProfile)
		api.POST("/api/v1/profile/mfa", authEnterpriseHandler.EnableMFA)
		api.POST("/api/v1/profile/mfa/verify", authEnterpriseHandler.VerifyMFA)
		api.POST("/api/v1/profile/mfa/backup_codes", authEnterpriseHandler.GenerateBackupCodes)
		api.DELETE("/api/v1/profile/mfa", authEnterpriseHandler.DisableMFA)
		api.GET("/api/v1/profile/sessions", authEnterpriseHandler.ListSessions)
		api.DELETE("/api/v1/profile/sessions/:session_id", authEnterpriseHandler.DeleteSession)
		api.POST("/api/v1/accounts", accountHandler.CreateAccount)

		// 超级管理员安装级配置 (SYS / ENT)
		superAdmin := api.Group("/api/v1/super_admin")
		superAdmin.Use(middleware.RequireSuperAdmin(cfg))
		{
			superAdmin.GET("/configs", enterpriseHandler.ListSystemConfigs)
			superAdmin.POST("/configs", enterpriseHandler.SetSystemConfig)
		}

		// 租户空间路由组 (严格多租户数据隔离)
		tenant := api.Group("/api/v1/accounts/:account_id")
		tenant.Use(middleware.TenantMiddleware(accountRepo))
		reqPerm := func(permission string) gin.HandlerFunc {
			return middleware.RequirePermission(accountRepo, permission)
		}
		{
			// 账号与坐席 (ACC)
			tenant.GET("", accountHandler.GetAccount)
			tenant.PUT("", reqPerm(domain.PermissionSettingsManage), accountHandler.UpdateAccount)
			tenant.GET("/agents", accountHandler.ListAgents)
			tenant.POST("/agents", reqPerm(domain.PermissionAgentManage), accountHandler.AddAgent)
			tenant.PUT("/agents/:id", reqPerm(domain.PermissionAgentManage), accountHandler.UpdateAgent)
			tenant.DELETE("/agents/:id", reqPerm(domain.PermissionAgentManage), accountHandler.RemoveAgent)

			// 自定义角色 (Custom Roles)
			tenant.GET("/custom_roles", reqPerm(domain.PermissionCustomRoleManage), accountHandler.ListCustomRoles)
			tenant.POST("/custom_roles", reqPerm(domain.PermissionCustomRoleManage), accountHandler.CreateCustomRole)
			tenant.GET("/custom_roles/:id", reqPerm(domain.PermissionCustomRoleManage), accountHandler.GetCustomRole)
			tenant.PUT("/custom_roles/:id", reqPerm(domain.PermissionCustomRoleManage), accountHandler.UpdateCustomRole)
			tenant.DELETE("/custom_roles/:id", reqPerm(domain.PermissionCustomRoleManage), accountHandler.DeleteCustomRole)

			// 团队管理 (Teams)
			tenant.GET("/teams", teamHandler.ListTeams)
			tenant.POST("/teams", reqPerm(domain.PermissionTeamManage), teamHandler.CreateTeam)
			tenant.GET("/teams/:id", teamHandler.GetTeam)
			tenant.PUT("/teams/:id", reqPerm(domain.PermissionTeamManage), teamHandler.UpdateTeam)
			tenant.DELETE("/teams/:id", reqPerm(domain.PermissionTeamManage), teamHandler.DeleteTeam)
			tenant.POST("/teams/:id/members", reqPerm(domain.PermissionTeamManage), teamHandler.AddMembers)
			tenant.GET("/teams/:id/members", teamHandler.ListMembers)
			tenant.DELETE("/teams/:id/members/:member_id", reqPerm(domain.PermissionTeamManage), teamHandler.RemoveMember)

			// 渠道与收件箱 (CHN / INB)
			tenant.GET("/inboxes", inboxHandler.ListInboxes)
			tenant.POST("/inboxes", reqPerm(domain.PermissionInboxManage), inboxHandler.CreateInbox)
			tenant.GET("/inboxes/:id", inboxHandler.GetInbox)
			tenant.PUT("/inboxes/:id", reqPerm(domain.PermissionInboxManage), inboxHandler.UpdateInbox)
			tenant.DELETE("/inboxes/:id", reqPerm(domain.PermissionInboxManage), inboxHandler.DeleteInbox)
			tenant.GET("/inboxes/:id/members", inboxHandler.ListInboxMembers)
			tenant.POST("/inboxes/:id/members", reqPerm(domain.PermissionInboxManage), inboxHandler.AddInboxMembers)
			tenant.PATCH("/inboxes/:id/members", reqPerm(domain.PermissionInboxManage), inboxHandler.UpdateMembers)
			tenant.DELETE("/inboxes/:id/members", reqPerm(domain.PermissionInboxManage), inboxHandler.RemoveMembers)
			tenant.PATCH("/inbox_members", reqPerm(domain.PermissionInboxManage), inboxHandler.UpdateMembers)
			tenant.DELETE("/inbox_members", reqPerm(domain.PermissionInboxManage), inboxHandler.RemoveMembers)
			tenant.POST("/inboxes/:id/assignment_policy", reqPerm(domain.PermissionInboxManage), inboxHandler.BindAssignmentPolicy)
			tenant.GET("/inboxes/:id/assistant", inboxHandler.GetInboxAssistant)
			tenant.GET("/inboxes/:id/assignable_agents", inboxHandler.GetAssignableAgents)
			tenant.GET("/inboxes/:id/campaigns", inboxHandler.ListInboxCampaigns)
			tenant.GET("/inboxes/:id/agent_bot", inboxHandler.GetInboxAgentBot)
			tenant.POST("/inboxes/:id/set_agent_bot", reqPerm(domain.PermissionInboxManage), inboxHandler.SetInboxAgentBot)
			tenant.POST("/inboxes/:id/agent_bot", reqPerm(domain.PermissionInboxManage), inboxHandler.SetInboxAgentBot)
			tenant.DELETE("/inboxes/:id/agent_bot", reqPerm(domain.PermissionInboxManage), inboxHandler.UnsetInboxAgentBot)
			tenant.DELETE("/inboxes/:id/avatar", reqPerm(domain.PermissionInboxManage), inboxHandler.DeleteAvatar)
			tenant.GET("/inboxes/:id/message_templates", inboxHandler.ListMessageTemplates)
			tenant.POST("/inboxes/:id/sync_templates", reqPerm(domain.PermissionInboxManage), inboxHandler.SyncMessageTemplates)
			tenant.GET("/inboxes/:id/health", inboxHandler.GetChannelHealth)
			tenant.POST("/inboxes/:id/register_webhook", reqPerm(domain.PermissionInboxManage), inboxHandler.RegisterChannelWebhook)
			tenant.POST("/inboxes/:id/reset_secret", reqPerm(domain.PermissionInboxManage), inboxHandler.ResetSecret)
			tenant.POST("/inboxes/:id/rotate_hmac_token", reqPerm(domain.PermissionInboxManage), inboxHandler.RotateHMACToken)
			tenant.POST("/inboxes/:id/enable_whatsapp_calling", reqPerm(domain.PermissionInboxManage), inboxHandler.EnableWhatsAppCalling)
			tenant.POST("/inboxes/:id/disable_whatsapp_calling", reqPerm(domain.PermissionInboxManage), inboxHandler.DisableWhatsAppCalling)
			tenant.POST("/inboxes/:id/set_inbound_calls", reqPerm(domain.PermissionInboxManage), inboxHandler.SetInboundCalls)
			tenant.POST("/inboxes/:id/set_call_recording", reqPerm(domain.PermissionInboxManage), inboxHandler.SetCallRecording)
			tenant.GET("/inboxes/:id/csat_template", inboxHandler.GetCSATTemplate)
			tenant.POST("/inboxes/:id/csat_template", reqPerm(domain.PermissionInboxManage), inboxHandler.SaveCSATTemplate)
			tenant.POST("/inboxes/:id/csat_template/analyze", inboxHandler.AnalyzeCSATTemplate)

			// 独立分配策略 (Assignment Policies)
			tenant.GET("/assignment_policies", assignmentPolicyHandler.List)
			tenant.POST("/assignment_policies", reqPerm(domain.PermissionInboxManage), assignmentPolicyHandler.Create)
			tenant.GET("/assignment_policies/:id", assignmentPolicyHandler.Get)
			tenant.PUT("/assignment_policies/:id", reqPerm(domain.PermissionInboxManage), assignmentPolicyHandler.Update)
			tenant.DELETE("/assignment_policies/:id", reqPerm(domain.PermissionInboxManage), assignmentPolicyHandler.Delete)

			// 客服容量策略 (Agent Capacity Policies - Chatwoot Enterprise)
			tenant.GET("/agent_capacity_policies", agentCapacityPolicyHandler.List)
			tenant.POST("/agent_capacity_policies", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.Create)
			tenant.GET("/agent_capacity_policies/:id", agentCapacityPolicyHandler.Show)
			tenant.PUT("/agent_capacity_policies/:id", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.Update)
			tenant.DELETE("/agent_capacity_policies/:id", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.Delete)
			tenant.GET("/agent_capacity_policies/:id/users", agentCapacityPolicyHandler.ListUsers)
			tenant.POST("/agent_capacity_policies/:id/users", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.AddUser)
			tenant.DELETE("/agent_capacity_policies/:id/users/:user_id", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.RemoveUser)
			tenant.POST("/agent_capacity_policies/:id/inbox_limits", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.CreateInboxLimit)
			tenant.PUT("/agent_capacity_policies/:id/inbox_limits/:limit_id", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.UpdateInboxLimit)
			tenant.DELETE("/agent_capacity_policies/:id/inbox_limits/:limit_id", reqPerm(domain.PermissionInboxManage), agentCapacityPolicyHandler.DeleteInboxLimit)

			// 客户档案与合并 (CRM / CUS)
			tenant.GET("/contacts", contactHandler.ListContacts)
			tenant.POST("/contacts", contactHandler.CreateContact)
			tenant.GET("/contacts/active", contactHandler.ListActiveContacts)
			tenant.GET("/contacts/search", contactHandler.SearchContacts)
			tenant.POST("/contacts/filter", contactHandler.FilterContacts)
			tenant.POST("/contacts/import", reqPerm(domain.PermissionContactManage), contactHandler.ImportContacts)
			tenant.GET("/contacts/export", contactHandler.ExportContacts)
			tenant.POST("/contacts/export", contactHandler.ExportContacts)
			tenant.POST("/contacts/:id/destroy_custom_attributes", reqPerm(domain.PermissionContactManage), contactHandler.DestroyCustomAttributes)
			tenant.POST("/contacts/destroy_custom_attributes", reqPerm(domain.PermissionContactManage), contactHandler.DestroyCustomAttributes)
			tenant.DELETE("/contacts/:id/avatar", reqPerm(domain.PermissionContactManage), contactHandler.DeleteAvatar)
			tenant.GET("/contacts/:id", contactHandler.GetContact)
			tenant.PUT("/contacts/:id", contactHandler.UpdateContact)
			tenant.DELETE("/contacts/:id", contactHandler.DeleteContact)
			tenant.POST("/actions/contact_merge", contactHandler.MergeContact)

			// 联系人标签 (CRM / Contact Labels)
			tenant.GET("/contacts/:id/labels", contactHandler.GetContactLabels)
			tenant.POST("/contacts/:id/labels", contactHandler.SetContactLabels)
			tenant.DELETE("/contacts/:id/labels/:label_id", contactHandler.DetachContactLabel)

			// 联系人扩展 (CRM / Contact Extensions: Attachments, Contactable Inboxes, Channels, Conversations & Stats)
			tenant.GET("/contacts/:id/attachments", contactExtensionHandler.ListContactAttachments)
			tenant.GET("/contacts/:id/contactable_inboxes", contactExtensionHandler.GetContactableInboxes)
			tenant.GET("/contacts/:id/inboxes", contactExtensionHandler.GetContactableInboxes)
			tenant.GET("/contacts/:id/contact_inboxes", contactExtensionHandler.ListContactInboxes)
			tenant.POST("/contacts/:id/contact_inboxes", contactExtensionHandler.CreateContactInbox)
			tenant.DELETE("/contacts/:id/contact_inboxes/:contact_inbox_id", contactExtensionHandler.DeleteContactInbox)
			tenant.DELETE("/contacts/:id/inboxes/:inbox_id", contactExtensionHandler.DeleteContactInbox)
			tenant.GET("/contacts/:id/conversations", contactExtensionHandler.ListContactConversations)
			tenant.GET("/contacts/:id/stats", contactExtensionHandler.GetContactStats)

			// 自定义字段定义 (Custom Attributes)
			tenant.GET("/custom_attribute_definitions", customAttrHandler.List)
			tenant.POST("/custom_attribute_definitions", customAttrHandler.Create)
			tenant.GET("/custom_attribute_definitions/:id", customAttrHandler.Get)
			tenant.PUT("/custom_attribute_definitions/:id", customAttrHandler.Update)
			tenant.PATCH("/custom_attribute_definitions/:id", customAttrHandler.Update)
			tenant.DELETE("/custom_attribute_definitions/:id", customAttrHandler.Delete)

			// 会话与消息状态机 (CON / MSG)
			tenant.GET("/conversations", convHandler.ListConversations)
			tenant.POST("/conversations", convHandler.CreateConversation)
			tenant.GET("/conversations/meta", convHandler.GetConversationMeta)
			tenant.GET("/conversations/unread_count", convHandler.GetUnreadCount)
			tenant.POST("/conversations/filter", convHandler.FilterConversations)
			tenant.GET("/conversations/:id", convHandler.GetConversation)
			tenant.PUT("/conversations/:id", convHandler.UpdateConversation)
			tenant.PATCH("/conversations/:id", convHandler.UpdateConversation)
			tenant.DELETE("/conversations/:id", convHandler.DeleteConversation)
			tenant.GET("/conversations/:id/meta", convHandler.GetConversationMeta)
			tenant.POST("/conversations/:id/priority", convHandler.SetPriority)
			tenant.PUT("/conversations/:id/priority", convHandler.SetPriority)
			tenant.POST("/conversations/:id/custom_attributes", convHandler.UpdateCustomAttributes)
			tenant.PATCH("/conversations/:id/custom_attributes", convHandler.UpdateCustomAttributes)
			tenant.GET("/conversations/:id/attachments", convHandler.ListConversationAttachments)
			tenant.GET("/conversations/:id/assistant", convHandler.GetConversationAssistant)
			tenant.GET("/conversations/:id/reporting_events", convHandler.ListConversationReportingEvents)
			tenant.POST("/conversations/:id/toggle_status", convHandler.ToggleStatus)
			tenant.POST("/conversations/:id/assignments", convHandler.Assign)
			tenant.GET("/conversations/:id/messages", convHandler.ListMessages)
			tenant.POST("/conversations/:id/messages", convHandler.CreateMessage)
			tenant.PUT("/conversations/:id/messages/:message_id", convHandler.UpdateMessage)
			tenant.DELETE("/conversations/:id/messages/:message_id", convHandler.DeleteMessage)
			tenant.POST("/conversations/:id/messages/:message_id/retry", convHandler.RetryMessage)
			tenant.POST("/conversations/:id/messages/:message_id/translate", convHandler.TranslateMessage)
			tenant.POST("/messages/:id/translate", convHandler.TranslateMessage)
			tenant.POST("/conversations/:id/update_last_seen", convHandler.UpdateLastSeen)
			tenant.POST("/conversations/:id/unread", convHandler.MarkUnread)
			tenant.POST("/conversations/:id/mute", convHandler.MuteConversation)
			tenant.POST("/conversations/:id/unmute", convHandler.UnmuteConversation)
			tenant.POST("/conversations/:id/toggle_typing_status", convHandler.ToggleTypingStatus)
			tenant.POST("/conversations/:id/transcript", convHandler.SendTranscript)

			// 快捷回复 (OPS)
			tenant.GET("/canned_responses", opsHandler.ListCannedResponses)
			tenant.POST("/canned_responses", opsHandler.CreateCannedResponse)
			tenant.PUT("/canned_responses/:id", opsHandler.UpdateCannedResponse)
			tenant.DELETE("/canned_responses/:id", opsHandler.DeleteCannedResponse)

			// 宏执行与批量处理 (OPS-01 ~ 04)
			tenant.GET("/macros", macroHandler.ListMacros)
			tenant.GET("/macros/:id", macroHandler.GetMacro)
			tenant.POST("/macros", reqPerm(domain.PermissionAutomationManage), macroHandler.CreateMacro)
			tenant.PUT("/macros/:id", reqPerm(domain.PermissionAutomationManage), macroHandler.UpdateMacro)
			tenant.POST("/macros/:id/execute", macroHandler.ExecuteMacro)
			tenant.DELETE("/macros/:id", reqPerm(domain.PermissionAutomationManage), macroHandler.DeleteMacro)

			// 站内通知与多端状态 (OPS-14 ~ 18)
			tenant.GET("/notifications", macroHandler.ListNotifications)
			tenant.GET("/notifications/unread_count", macroHandler.GetUnreadCount)
			tenant.POST("/notifications/read_all", macroHandler.MarkAllNotificationsRead)
			tenant.POST("/notifications/:id/read", macroHandler.MarkNotificationRead)
			tenant.POST("/notifications/:id/unread", macroHandler.MarkNotificationUnread)
			tenant.POST("/notifications/:id/snooze", macroHandler.SnoozeNotification)
			tenant.POST("/notifications/:id/unsnooze", macroHandler.UnsnoozeNotification)
			tenant.PATCH("/notifications/:id", macroHandler.UpdateNotification)
			tenant.DELETE("/notifications/:id", macroHandler.DeleteNotification)
			tenant.POST("/notifications/batch_destroy", macroHandler.BatchDeleteNotifications)
			tenant.DELETE("/notifications", macroHandler.BatchDeleteNotifications)

			// 通知偏好与渠道开关
			tenant.GET("/notification_settings", macroHandler.GetNotificationSettings)
			tenant.PUT("/notification_settings", macroHandler.UpdateNotificationSettings)
			tenant.PATCH("/notification_settings", macroHandler.UpdateNotificationSettings)

			// 移动推送与设备订阅 (MOB-01 ~ 05)
			tenant.POST("/notification_subscriptions", deviceHandler.RegisterSubscription)
			tenant.DELETE("/notification_subscriptions", deviceHandler.DeleteSubscription)

			// 标签体系 (Labels)
			tenant.GET("/labels", opsHandler.ListLabels)
			tenant.GET("/labels/:id", opsHandler.GetLabel)
			tenant.POST("/labels", opsHandler.CreateLabel)
			tenant.PUT("/labels/:id", opsHandler.UpdateLabel)
			tenant.DELETE("/labels/:id", opsHandler.DeleteLabel)
			tenant.POST("/conversations/:id/labels", opsHandler.AttachConversationLabels)
			tenant.GET("/conversations/:id/labels", opsHandler.GetConversationLabels)
			tenant.DELETE("/conversations/:id/labels/:label_id", opsHandler.DetachConversationLabel)

			// 帮助中心管理 (HELP)
			tenant.GET("/portals", portalHandler.ListPortals)
			tenant.POST("/portals", portalHandler.CreatePortal)
			tenant.GET("/portals/:id", portalHandler.GetPortal)
			tenant.PUT("/portals/:id", portalHandler.UpdatePortal)
			tenant.DELETE("/portals/:id", portalHandler.DeletePortal)
			tenant.GET("/portals/:id/categories", portalHandler.ListCategories)
			tenant.POST("/portals/:id/categories", portalHandler.CreateCategory)
			tenant.GET("/portals/:id/categories/:category_id", portalHandler.GetCategory)
			tenant.PUT("/portals/:id/categories/:category_id", portalHandler.UpdateCategory)
			tenant.DELETE("/portals/:id/categories/:category_id", portalHandler.DeleteCategory)
			tenant.GET("/portals/:id/articles", portalHandler.ListArticles)
			tenant.POST("/portals/:id/articles", portalHandler.CreateArticle)
			tenant.GET("/portals/:id/articles/:article_id", portalHandler.GetArticle)
			tenant.PUT("/portals/:id/articles/:article_id", portalHandler.UpdateArticle)
			tenant.DELETE("/portals/:id/articles/:article_id", portalHandler.DeleteArticle)

			// Webhook 管理 (EXT)
			tenant.GET("/webhooks", reqPerm(domain.PermissionSettingsManage), webhookHandler.List)
			tenant.POST("/webhooks", reqPerm(domain.PermissionSettingsManage), webhookHandler.Create)
			tenant.PUT("/webhooks/:id", reqPerm(domain.PermissionSettingsManage), webhookHandler.Update)
			tenant.DELETE("/webhooks/:id", reqPerm(domain.PermissionSettingsManage), webhookHandler.Delete)
			tenant.POST("/webhooks/deliveries/:id/retry", middleware.RequirePermission(accountRepo, domain.PermissionSettingsManage, domain.RoleAgent), webhookHandler.RetryDelivery)
			tenant.POST("/webhooks/deliveries/retry", middleware.RequirePermission(accountRepo, domain.PermissionSettingsManage, domain.RoleAgent), webhookHandler.RetryDelivery)

			// 仪表盘应用 (Dashboard Apps)
			tenant.GET("/dashboard_apps", dashboardAppHandler.List)
			tenant.GET("/dashboard_apps/:id", dashboardAppHandler.Get)
			tenant.POST("/dashboard_apps", reqPerm(domain.PermissionSettingsManage), dashboardAppHandler.Create)
			tenant.PUT("/dashboard_apps/:id", reqPerm(domain.PermissionSettingsManage), dashboardAppHandler.Update)
			tenant.PATCH("/dashboard_apps/:id", reqPerm(domain.PermissionSettingsManage), dashboardAppHandler.Update)
			tenant.DELETE("/dashboard_apps/:id", reqPerm(domain.PermissionSettingsManage), dashboardAppHandler.Delete)

			// 企业能力与 Feature 开关 (ENT)
			tenant.GET("/features", enterpriseHandler.GetAccountFeatures)
			tenant.POST("/features", reqPerm(domain.PermissionSettingsManage), enterpriseHandler.SetAccountFeature)

			// 公司与组织客户 (CRM / Companies)
			tenant.GET("/companies/search", advancedHandler.SearchCompanies)
			tenant.POST("/companies/destroy_custom_attributes", reqPerm(domain.PermissionContactManage), advancedHandler.DestroyCompanyCustomAttributes)
			tenant.GET("/companies", advancedHandler.ListCompanies)
			tenant.POST("/companies", advancedHandler.CreateCompany)
			tenant.GET("/companies/:id", advancedHandler.GetCompany)
			tenant.PUT("/companies/:id", advancedHandler.UpdateCompany)
			tenant.DELETE("/companies/:id", advancedHandler.DeleteCompany)
			tenant.POST("/companies/:id/destroy_custom_attributes", reqPerm(domain.PermissionContactManage), advancedHandler.DestroyCompanyCustomAttributes)
			tenant.DELETE("/companies/:id/avatar", reqPerm(domain.PermissionContactManage), advancedHandler.DeleteCompanyAvatar)
			tenant.GET("/companies/:id/contacts/search", advancedHandler.SearchCompanyContacts)
			tenant.GET("/companies/:id/contacts", advancedHandler.ListCompanyContacts)
			tenant.POST("/companies/:id/contacts", advancedHandler.AddCompanyContacts)
			tenant.DELETE("/companies/:id/contacts/:contact_id", advancedHandler.RemoveCompanyContact)

			// 公司扩展 (CRM / Company Extensions: Conversations, Notes Aggregation, Stats & Attachments)
			tenant.GET("/companies/:id/conversations", companyExtensionHandler.ListCompanyConversations)
			tenant.GET("/companies/:id/notes", companyExtensionHandler.ListCompanyNotes)
			tenant.GET("/companies/:id/notes/summary", companyExtensionHandler.GetCompanyNotesSummary)
			tenant.POST("/companies/:id/notes", companyExtensionHandler.CreateCompanyNote)
			tenant.GET("/companies/:id/notes/:note_id", companyExtensionHandler.GetCompanyNote)
			tenant.PUT("/companies/:id/notes/:note_id", companyExtensionHandler.UpdateCompanyNote)
			tenant.PATCH("/companies/:id/notes/:note_id", companyExtensionHandler.UpdateCompanyNote)
			tenant.DELETE("/companies/:id/notes/:note_id", companyExtensionHandler.DeleteCompanyNote)
			tenant.GET("/companies/:id/stats", companyExtensionHandler.GetCompanyStats)
			tenant.GET("/companies/:id/attachments", companyExtensionHandler.ListCompanyAttachments)

			// 营销活动 (OPS / Campaigns)
			tenant.GET("/campaigns", advancedHandler.ListCampaigns)
			tenant.POST("/campaigns", reqPerm(domain.PermissionAutomationManage), advancedHandler.CreateCampaign)
			tenant.GET("/campaigns/:id", advancedHandler.GetCampaign)
			tenant.PUT("/campaigns/:id", reqPerm(domain.PermissionAutomationManage), advancedHandler.UpdateCampaign)
			tenant.PATCH("/campaigns/:id", reqPerm(domain.PermissionAutomationManage), advancedHandler.UpdateCampaign)
			tenant.DELETE("/campaigns/:id", reqPerm(domain.PermissionAutomationManage), advancedHandler.DeleteCampaign)
			tenant.POST("/campaigns/:id/trigger", reqPerm(domain.PermissionAutomationManage), advancedHandler.TriggerCampaign)
			tenant.POST("/campaigns/:id/pause", reqPerm(domain.PermissionAutomationManage), advancedHandler.PauseCampaign)
			tenant.POST("/campaigns/:id/resume", reqPerm(domain.PermissionAutomationManage), advancedHandler.ResumeCampaign)
			tenant.POST("/campaigns/:id/stop", reqPerm(domain.PermissionAutomationManage), advancedHandler.StopCampaign)
			tenant.POST("/campaigns/:id/cancel", reqPerm(domain.PermissionAutomationManage), advancedHandler.CancelCampaign)
			tenant.GET("/campaigns/:id/deliveries", advancedHandler.ListCampaignDeliveries)
			tenant.GET("/campaigns/:id/metrics", advancedHandler.GetCampaignMetrics)

			// 自动化规则 (OPS / Automation Rules)
			tenant.GET("/automation_rules", advancedHandler.ListAutomationRules)
			tenant.POST("/automation_rules", reqPerm(domain.PermissionAutomationManage), advancedHandler.CreateAutomationRule)
			tenant.GET("/automation_rules/:id", advancedHandler.GetAutomationRule)
			tenant.PUT("/automation_rules/:id", reqPerm(domain.PermissionAutomationManage), advancedHandler.UpdateAutomationRule)
			tenant.DELETE("/automation_rules/:id", reqPerm(domain.PermissionAutomationManage), advancedHandler.DeleteAutomationRule)
			tenant.POST("/automation_rules/:id/clone", reqPerm(domain.PermissionAutomationManage), advancedHandler.CloneAutomationRule)
			tenant.POST("/automation_rules/:id/duplicate", reqPerm(domain.PermissionAutomationManage), advancedHandler.CloneAutomationRule)

			// 服务水平协议与应用记录 (RPT / SLA & Applied SLA)
			tenant.GET("/sla_policies", advancedHandler.ListSLAPolicies)
			tenant.POST("/sla_policies", reqPerm(domain.PermissionSLAManage), advancedHandler.CreateSLAPolicy)
			tenant.GET("/sla_policies/:id", advancedHandler.GetSLAPolicy)
			tenant.PUT("/sla_policies/:id", reqPerm(domain.PermissionSLAManage), advancedHandler.UpdateSLAPolicy)
			tenant.DELETE("/sla_policies/:id", reqPerm(domain.PermissionSLAManage), advancedHandler.DeleteSLAPolicy)
			tenant.GET("/conversations/:id/sla", appliedSLAHandler.GetConversationAppliedSLA)
			tenant.POST("/conversations/:id/sla", appliedSLAHandler.ApplySLA)
			tenant.DELETE("/conversations/:id/sla", appliedSLAHandler.RemoveSLA)
			tenant.GET("/conversations/:id/applied_sla", appliedSLAHandler.GetConversationAppliedSLA)
			tenant.POST("/conversations/:id/applied_sla", appliedSLAHandler.ApplySLA)
			tenant.DELETE("/conversations/:id/applied_sla", appliedSLAHandler.RemoveSLA)
			tenant.GET("/applied_slas", appliedSLAHandler.ListAppliedSLAs)
			tenant.GET("/applied_slas/metrics", appliedSLAHandler.GetMetrics)
			tenant.GET("/applied_slas/download", appliedSLAHandler.Download)
			tenant.POST("/sla_policies/process", reqPerm(domain.PermissionSLAManage), advancedHandler.ProcessSLA)
			tenant.POST("/sla/process", reqPerm(domain.PermissionSLAManage), advancedHandler.ProcessSLA)
			tenant.POST("/slas/evaluate", reqPerm(domain.PermissionSLAManage), advancedHandler.ProcessSLA)
			tenant.POST("/sla/evaluate", reqPerm(domain.PermissionSLAManage), advancedHandler.ProcessSLA)
			tenant.GET("/sla_policies/breaches", advancedHandler.ListSLABreaches)
			tenant.GET("/sla_breaches", advancedHandler.ListSLABreaches)
			tenant.GET("/slas/breaches", advancedHandler.ListSLABreaches)

			// 机器人与集成 (EXT / AgentBots)
			tenant.GET("/agent_bots", advancedHandler.ListAgentBots)
			tenant.POST("/agent_bots", reqPerm(domain.PermissionSettingsManage), advancedHandler.CreateAgentBot)
			tenant.GET("/agent_bots/:id", advancedHandler.GetAgentBot)
			tenant.PUT("/agent_bots/:id", reqPerm(domain.PermissionSettingsManage), advancedHandler.UpdateAgentBot)
			tenant.PATCH("/agent_bots/:id", reqPerm(domain.PermissionSettingsManage), advancedHandler.UpdateAgentBot)
			tenant.DELETE("/agent_bots/:id", reqPerm(domain.PermissionSettingsManage), advancedHandler.DeleteAgentBot)
			tenant.DELETE("/agent_bots/:id/avatar", reqPerm(domain.PermissionSettingsManage), advancedHandler.DeleteAgentBotAvatar)

			// Agent Bot 与 Inbox 关联管理
			tenant.GET("/agent_bots/:id/inboxes", advancedHandler.GetAgentBotInboxes)
			tenant.POST("/agent_bots/:id/inboxes", reqPerm(domain.PermissionInboxManage), advancedHandler.ConnectAgentBotInbox)
			tenant.POST("/agent_bots/:id/inbox", reqPerm(domain.PermissionInboxManage), advancedHandler.ConnectAgentBotInbox)
			tenant.DELETE("/agent_bots/:id/inboxes/:inbox_id", reqPerm(domain.PermissionInboxManage), advancedHandler.DisconnectAgentBotInbox)
			tenant.DELETE("/agent_bots/:id/inbox/:inbox_id", reqPerm(domain.PermissionInboxManage), advancedHandler.DisconnectAgentBotInbox)

			// 消息附件上传 (CONV / Attachments)
			tenant.POST("/conversations/:id/attachments", advancedHandler.UploadAttachment)

			// 会话草稿 (CONV / Draft Messages)
			tenant.GET("/conversations/:id/draft_messages", advancedHandler.GetDraft)
			tenant.POST("/conversations/:id/draft_messages", advancedHandler.SaveDraft)
			tenant.DELETE("/conversations/:id/draft_messages", advancedHandler.DeleteDraft)

			// 会话协作参与者 (CONV / Participants)
			tenant.GET("/conversations/:id/participants", advancedHandler.ListParticipants)
			tenant.POST("/conversations/:id/participants", advancedHandler.AddParticipants)
			tenant.PUT("/conversations/:id/participants", advancedHandler.UpdateParticipants)
			tenant.PATCH("/conversations/:id/participants", advancedHandler.UpdateParticipants)
			tenant.DELETE("/conversations/:id/participants", advancedHandler.RemoveParticipants)
			tenant.DELETE("/conversations/:id/participants/:user_id", advancedHandler.RemoveParticipant)
			tenant.POST("/conversations/:id/csat_survey", csatHandler.TriggerSurvey)

			// 客户满意度评价管理与质检审核 (CSAT Lifecycle & Moderation - Section 27)
			tenant.GET("/csat_surveys", csatHandler.ListSurveys)
			tenant.GET("/csat_survey_responses", csatHandler.ListSurveys)
			tenant.GET("/csat_surveys/download", csatHandler.DownloadSurveys)
			tenant.GET("/csat_surveys/export", csatHandler.DownloadSurveys)
			tenant.GET("/csat_surveys/metrics", csatHandler.GetMetrics)
			tenant.POST("/csat_surveys/bulk_review", csatHandler.BulkReviewSurveys)
			tenant.POST("/csat_surveys", csatHandler.SubmitSurvey)
			tenant.GET("/csat_surveys/:id", csatHandler.GetSurvey)
			tenant.GET("/csat_survey_responses/:id", csatHandler.GetSurvey)
			tenant.POST("/csat_surveys/:id/review", csatHandler.ReviewSurvey)
			tenant.PUT("/csat_surveys/:id/review", csatHandler.ReviewSurvey)
			tenant.PATCH("/csat_surveys/:id/review", csatHandler.ReviewSurvey)
			tenant.DELETE("/csat_surveys/:id", csatHandler.DeleteSurvey)

			// 联系人内部备注 (CRM / Contact Notes)
			tenant.GET("/contacts/:id/notes", advancedHandler.ListContactNotes)
			tenant.GET("/contacts/:id/notes/:note_id", advancedHandler.GetContactNote)
			tenant.POST("/contacts/:id/notes", advancedHandler.CreateContactNote)
			tenant.PUT("/contacts/:id/notes/:note_id", advancedHandler.UpdateContactNote)
			tenant.PATCH("/contacts/:id/notes/:note_id", advancedHandler.UpdateContactNote)
			tenant.DELETE("/contacts/:id/notes/:note_id", advancedHandler.DeleteContactNote)

			// 自定义筛选器 (OPS / Custom Filters)
			tenant.GET("/custom_filters", advancedHandler.ListCustomFilters)
			tenant.POST("/custom_filters", advancedHandler.CreateCustomFilter)
			tenant.GET("/custom_filters/:id", advancedHandler.GetCustomFilter)
			tenant.PUT("/custom_filters/:id", advancedHandler.UpdateCustomFilter)
			tenant.PATCH("/custom_filters/:id", advancedHandler.UpdateCustomFilter)
			tenant.DELETE("/custom_filters/:id", advancedHandler.DeleteCustomFilter)

			// 坐席容量策略兼容 (ACC / Legacy Capacity Policies)
			tenant.GET("/capacity_policies", advancedHandler.ListCapacityPolicies)
			tenant.POST("/capacity_policies", reqPerm(domain.PermissionInboxManage), advancedHandler.SetCapacityPolicy)

			// 批量操作 (OPS / Bulk Actions)
			tenant.POST("/bulk_actions", advancedHandler.BulkActions)

			// 集成应用目录与业务调用 (EXT / Integrations Directory)
			tenant.GET("/integrations/apps", advancedHandler.ListIntegrationApps)
			tenant.POST("/integrations/apps/:app_id", advancedHandler.InstallIntegrationApp)
			tenant.DELETE("/integrations/apps/:app_id", advancedHandler.UninstallIntegrationApp)
			tenant.GET("/integrations/shopify/orders", advancedHandler.ShopifyOrders)
			tenant.POST("/integrations/shopify/orders", advancedHandler.ShopifyOrders)
			tenant.POST("/integrations/dialogflow/process", advancedHandler.DialogflowProcess)

			// AI 知识库与智能助手 (AI / Copilot / Captain)
			tenant.POST("/conversations/:id/copilot/reply_suggestions", copilotHandler.ReplySuggestions)
			tenant.POST("/conversations/:id/copilot/suggestions", copilotHandler.ReplySuggestions)
			tenant.POST("/conversations/:id/copilot/summarize", copilotHandler.SummarizeConversation)
			tenant.POST("/conversations/:id/copilot/rephrase", copilotHandler.RephraseText)
			tenant.POST("/copilot/rephrase", copilotHandler.RephraseText)
			tenant.POST("/copilot/completions", copilotHandler.GenerateCompletion)

			// Copilot 多轮 Thread 与消息管理
			tenant.GET("/copilot/threads", copilotThreadHandler.ListThreads)
			tenant.POST("/copilot/threads", copilotThreadHandler.CreateThread)
			tenant.GET("/copilot/threads/metrics", copilotThreadHandler.GetMetrics)
			tenant.GET("/copilot/threads/:id", copilotThreadHandler.GetThread)
			tenant.PUT("/copilot/threads/:id", copilotThreadHandler.UpdateThread)
			tenant.PATCH("/copilot/threads/:id", copilotThreadHandler.UpdateThread)
			tenant.DELETE("/copilot/threads/:id", copilotThreadHandler.DeleteThread)

			tenant.GET("/copilot/threads/:id/messages", copilotThreadHandler.ListMessages)
			tenant.POST("/copilot/threads/:id/messages", copilotThreadHandler.SendMessage)
			tenant.POST("/copilot/threads/:id/chat", copilotThreadHandler.SendMessage)
			tenant.DELETE("/copilot/threads/:id/messages", copilotThreadHandler.ClearMessages)
			tenant.GET("/copilot/threads/:id/messages/:msg_id", copilotThreadHandler.GetMessage)
			tenant.DELETE("/copilot/threads/:id/messages/:msg_id", copilotThreadHandler.DeleteMessage)
			tenant.POST("/copilot/threads/:id/messages/:msg_id/feedback", copilotThreadHandler.UpdateFeedback)
			tenant.PUT("/copilot/threads/:id/messages/:msg_id/feedback", copilotThreadHandler.UpdateFeedback)

			// 会话级 Copilot Thread 快捷端点
			tenant.GET("/conversations/:id/copilot/threads", copilotThreadHandler.ListConversationThreads)
			tenant.POST("/conversations/:id/copilot/threads", copilotThreadHandler.CreateConversationThread)

			// Captain 助手完整管理 (CRUD)
			tenant.GET("/captain/assistants", copilotHandler.ListAssistants)
			tenant.POST("/captain/assistants", reqPerm(domain.PermissionAIManage), copilotHandler.CreateAssistant)
			tenant.GET("/captain/assistants/:id", copilotHandler.GetAssistant)
			tenant.PUT("/captain/assistants/:id", reqPerm(domain.PermissionAIManage), copilotHandler.UpdateAssistant)
			tenant.PATCH("/captain/assistants/:id", reqPerm(domain.PermissionAIManage), copilotHandler.UpdateAssistant)
			tenant.DELETE("/captain/assistants/:id", reqPerm(domain.PermissionAIManage), copilotHandler.DeleteAssistant)

			// Captain 助手收件箱绑定 (Inboxes)
			tenant.GET("/captain/assistants/:id/inboxes", copilotHandler.ListAssistantInboxes)
			tenant.POST("/captain/assistants/:id/inboxes", reqPerm(domain.PermissionAIManage), copilotHandler.BindAssistantInbox)
			tenant.DELETE("/captain/assistants/:id/inboxes/:inbox_id", reqPerm(domain.PermissionAIManage), copilotHandler.UnbindAssistantInbox)

			// Captain Playground 交互测试演练场
			tenant.POST("/captain/assistants/:id/playground", copilotHandler.Playground)

			// Captain FAQ / 常见问答管理 (Assistant Responses)
			tenant.GET("/captain/assistant_responses", copilotHandler.ListAssistantResponses)
			tenant.POST("/captain/assistant_responses", reqPerm(domain.PermissionAIManage), copilotHandler.CreateAssistantResponse)
			tenant.GET("/captain/assistant_responses/:id", copilotHandler.GetAssistantResponse)
			tenant.PUT("/captain/assistant_responses/:id", reqPerm(domain.PermissionAIManage), copilotHandler.UpdateAssistantResponse)
			tenant.PATCH("/captain/assistant_responses/:id", reqPerm(domain.PermissionAIManage), copilotHandler.UpdateAssistantResponse)
			tenant.DELETE("/captain/assistant_responses/:id", reqPerm(domain.PermissionAIManage), copilotHandler.DeleteAssistantResponse)
			// FAQ 别名支持
			tenant.GET("/captain/faqs", copilotHandler.ListAssistantResponses)
			tenant.POST("/captain/faqs", reqPerm(domain.PermissionAIManage), copilotHandler.CreateAssistantResponse)
			tenant.GET("/captain/faqs/:id", copilotHandler.GetAssistantResponse)
			tenant.PUT("/captain/faqs/:id", reqPerm(domain.PermissionAIManage), copilotHandler.UpdateAssistantResponse)
			tenant.PATCH("/captain/faqs/:id", reqPerm(domain.PermissionAIManage), copilotHandler.UpdateAssistantResponse)
			tenant.DELETE("/captain/faqs/:id", reqPerm(domain.PermissionAIManage), copilotHandler.DeleteAssistantResponse)
			tenant.GET("/captain/assistants/:id/faqs", copilotHandler.ListAssistantResponses)
			tenant.POST("/captain/assistants/:id/faqs", reqPerm(domain.PermissionAIManage), copilotHandler.CreateAssistantResponse)

			// Captain 统计大盘、运行摘要及指标下钻
			tenant.GET("/captain/assistants/:id/stats", copilotHandler.GetAssistantStats)
			tenant.GET("/captain/assistants/:id/summary", copilotHandler.GetAssistantSummary)
			tenant.GET("/captain/assistants/:id/drilldown", copilotHandler.GetAssistantDrilldown)

			// 知识库与场景工具
			tenant.GET("/captain/knowledge_bases", copilotHandler.ListKnowledgeDocs)
			tenant.POST("/captain/knowledge_bases", reqPerm(domain.PermissionAIManage), copilotHandler.CreateKnowledgeDoc)
			tenant.GET("/captain/knowledge_docs", copilotHandler.ListKnowledgeDocs)
			tenant.POST("/captain/knowledge_docs", reqPerm(domain.PermissionAIManage), copilotHandler.CreateKnowledgeDoc)
			tenant.GET("/captain/knowledge_chunks", copilotHandler.SearchKnowledgeChunks)
			tenant.GET("/captain/scenarios", copilotHandler.ListScenarios)
			tenant.POST("/captain/scenarios", reqPerm(domain.PermissionAIManage), copilotHandler.CreateScenario)
			tenant.PUT("/captain/scenarios/:id", reqPerm(domain.PermissionAIManage), copilotHandler.UpdateScenario)
			tenant.DELETE("/captain/scenarios/:id", reqPerm(domain.PermissionAIManage), copilotHandler.DeleteScenario)
			tenant.POST("/captain/tools/execute", copilotHandler.ExecuteAITool)
			tenant.GET("/captain/quota", copilotHandler.GetAIQuota)

			// 自定义 AI 工具完整管理与测试流程 (Custom Tools Lifecycle)
			tenant.GET("/captain/tools", aiCustomToolHandler.ListTools)
			tenant.POST("/captain/tools", reqPerm(domain.PermissionAIManage), aiCustomToolHandler.CreateTool)
			tenant.GET("/captain/tools/metrics", aiCustomToolHandler.GetMetrics)
			tenant.POST("/captain/tools/test", reqPerm(domain.PermissionAIManage), aiCustomToolHandler.TestTool)
			tenant.GET("/captain/tools/:id", aiCustomToolHandler.GetTool)
			tenant.PUT("/captain/tools/:id", reqPerm(domain.PermissionAIManage), aiCustomToolHandler.UpdateTool)
			tenant.PATCH("/captain/tools/:id", reqPerm(domain.PermissionAIManage), aiCustomToolHandler.UpdateTool)
			tenant.DELETE("/captain/tools/:id", reqPerm(domain.PermissionAIManage), aiCustomToolHandler.DeleteTool)
			tenant.POST("/captain/tools/:id/test", reqPerm(domain.PermissionAIManage), aiCustomToolHandler.TestTool)
			tenant.GET("/captain/tools/:id/logs", aiCustomToolHandler.ListExecutionLogs)

			// 变更追踪与审计日志 (LOG / AUD: LOG-01 ~ LOG-08)
			tenant.GET("/audit_logs", reqPerm(domain.PermissionAuditManage), auditHandler.ListAuditLogs)
			tenant.GET("/audit_logs/:id", reqPerm(domain.PermissionAuditManage), auditHandler.GetAuditLog)
			tenant.GET("/data_changes", reqPerm(domain.PermissionAuditManage), auditHandler.ListDataChanges)
			tenant.GET("/data_changes/:change_id", reqPerm(domain.PermissionAuditManage), auditHandler.GetDataChange)
			tenant.POST("/data_changes/:change_id/corrections", reqPerm(domain.PermissionAuditManage), auditHandler.CreateDataChangeCorrection)
			tenant.GET("/audit_objects/:object_type/:object_id/timeline", reqPerm(domain.PermissionAuditManage), auditHandler.GetObjectTimeline)
			tenant.GET("/audit_processes/:correlation_id/timeline", reqPerm(domain.PermissionAuditManage), auditHandler.GetProcessTimeline)
			tenant.GET("/security_audit_logs", reqPerm(domain.PermissionAuditManage), auditHandler.ListSecurityAuditLogs)
			tenant.POST("/audit_exports", reqPerm(domain.PermissionAuditManage), auditHandler.CreateAuditExport)
			tenant.GET("/audit_exports/:export_id", reqPerm(domain.PermissionAuditManage), auditHandler.GetAuditExport)
			tenant.POST("/audit_integrity/verifications", reqPerm(domain.PermissionAuditManage), auditHandler.CreateIntegrityVerification)
			tenant.GET("/audit_integrity/verifications/:verification_id", reqPerm(domain.PermissionAuditManage), auditHandler.GetIntegrityVerification)

			// 外部渠道命令与授权 (CHAN)
			tenant.POST("/whatsapp/authorization", channelDriverHandler.WhatsAppAuthorization)
			tenant.POST("/whatsapp/csat_template", channelDriverHandler.SetWhatsAppCSATTemplate)
			tenant.POST("/channels/twilio_channel", channelDriverHandler.CreateTwilioChannel)

			// 语音呼叫与 WebRTC 会议 (CHAN / Calls & Conference)
			tenant.GET("/calls", channelDriverHandler.ListCalls)
			tenant.GET("/calls/:id", channelDriverHandler.GetCall)
			tenant.POST("/contacts/:id/call", channelDriverHandler.InitiateContactCall)
			tenant.POST("/calls/:id/accept", channelDriverHandler.AcceptCall)
			tenant.POST("/calls/:id/reject", channelDriverHandler.RejectCall)
			tenant.POST("/calls/:id/end", channelDriverHandler.EndCall)
			tenant.POST("/calls/:id/terminate", channelDriverHandler.TerminateCall)
			tenant.POST("/calls/:id/recordings", channelDriverHandler.UploadCallRecording)
			tenant.POST("/calls/:id/upload_recording", channelDriverHandler.UploadCallRecording)
			tenant.POST("/calls/:id/recording", channelDriverHandler.UploadCallRecording)
			tenant.POST("/calls/:id/candidates", channelDriverHandler.AddCallCandidate)
			tenant.GET("/calls/:id/candidates", channelDriverHandler.ListCallCandidates)
			tenant.GET("/calls/:id/whatsapp", channelDriverHandler.GetWhatsAppCallDetail)

			// WhatsApp Calls (Chatwoot Enterprise 对齐)
			tenant.GET("/whatsapp_calls/:id", channelDriverHandler.GetWhatsAppCallDetail)
			tenant.POST("/whatsapp_calls/:id/accept", channelDriverHandler.AcceptCall)
			tenant.POST("/whatsapp_calls/:id/reject", channelDriverHandler.RejectCall)
			tenant.POST("/whatsapp_calls/:id/terminate", channelDriverHandler.TerminateCall)
			tenant.POST("/whatsapp_calls/:id/upload_recording", channelDriverHandler.UploadCallRecording)
			tenant.POST("/whatsapp_calls", channelDriverHandler.HandleWhatsAppCallSDP)

			// Conference 路由 (Chatwoot Enterprise 对齐)
			tenant.POST("/inboxes/:id/conference", channelDriverHandler.CreateConference)
			tenant.DELETE("/inboxes/:id/conference", channelDriverHandler.DeleteConference)
			tenant.GET("/inboxes/:id/conference/token", channelDriverHandler.GetConferenceToken)
			tenant.POST("/inboxes/:id/conference/token", channelDriverHandler.GetConferenceToken)
			tenant.DELETE("/conferences/:id", channelDriverHandler.DeleteConference)
			tenant.GET("/conferences/:id/token", channelDriverHandler.GetConferenceToken)
			tenant.POST("/conferences/:id/token", channelDriverHandler.GetConferenceToken)

			// 企业单点登录设置 (SAML - 复数与单数路径全面兼容)
			tenant.GET("/saml_settings", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.GetSAMLSetting)
			tenant.POST("/saml_settings", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveSAMLSetting)
			tenant.PUT("/saml_settings", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveSAMLSetting)
			tenant.PATCH("/saml_settings", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveSAMLSetting)
			tenant.DELETE("/saml_settings", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DeleteSAMLSetting)
			tenant.POST("/saml_settings/disable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DisableSAMLSetting)
			tenant.PUT("/saml_settings/disable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DisableSAMLSetting)
			tenant.POST("/saml_settings/enable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.EnableSAMLSetting)
			tenant.PUT("/saml_settings/enable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.EnableSAMLSetting)

			tenant.GET("/saml_setting", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.GetSAMLSetting)
			tenant.POST("/saml_setting", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveSAMLSetting)
			tenant.PUT("/saml_setting", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveSAMLSetting)
			tenant.PATCH("/saml_setting", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveSAMLSetting)
			tenant.DELETE("/saml_setting", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DeleteSAMLSetting)
			tenant.POST("/saml_setting/disable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DisableSAMLSetting)
			tenant.PUT("/saml_setting/disable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DisableSAMLSetting)
			tenant.POST("/saml_setting/enable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.EnableSAMLSetting)
			tenant.PUT("/saml_setting/enable", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.EnableSAMLSetting)

			// 数据导入与批量迁移 (ROUTE & MIG)
			tenant.GET("/data_imports", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.ListDataImports)
			tenant.POST("/data_imports", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.CreateDataImport)
			tenant.POST("/data_imports/prevalidate", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.PrevalidateDataImport)
			tenant.GET("/data_imports/:id", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.GetDataImport)
			tenant.POST("/data_imports/:id/prevalidate", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.PrevalidateDataImport)
			tenant.POST("/data_imports/:id/start", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.StartDataImport)
			tenant.POST("/data_imports/:id/execute", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.StartDataImport)
			tenant.POST("/data_imports/:id/cancel", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.CancelDataImport)
			tenant.POST("/data_imports/:id/discard", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DiscardDataImport)
			tenant.GET("/data_imports/:id/errors", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.GetImportErrors)
			tenant.GET("/data_imports/:id/errors/download", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DownloadImportErrors)
			tenant.GET("/data_imports/:id/error_report", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DownloadImportErrors)
			tenant.GET("/data_imports/:id/skipped", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.GetImportSkipped)
			tenant.GET("/data_imports/:id/skipped/download", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DownloadSkippedRecords)
			tenant.GET("/data_imports/:id/skipped_records", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.DownloadSkippedRecords)
			tenant.GET("/migration_jobs", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.ListMigrationJobs)
			tenant.POST("/migration_jobs", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.CreateMigrationJob)
			tenant.POST("/email_channel_migration", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.EmailChannelMigration)
			tenant.GET("/subscription/plans", authEnterpriseHandler.ListSubscriptionPlans)
			tenant.GET("/subscription", reqPerm(domain.PermissionBillingManage), authEnterpriseHandler.GetSubscription)
			tenant.POST("/subscription", reqPerm(domain.PermissionBillingManage), authEnterpriseHandler.CreateOrUpdateSubscription)
			tenant.POST("/checkout", reqPerm(domain.PermissionBillingManage), authEnterpriseHandler.Checkout)
			tenant.POST("/select_billing_currency", reqPerm(domain.PermissionBillingManage), authEnterpriseHandler.SelectBillingCurrency)
			tenant.POST("/toggle_deletion", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.ToggleDeletion)
			tenant.GET("/topup_options", reqPerm(domain.PermissionBillingManage), authEnterpriseHandler.TopupOptions)
			tenant.POST("/topup_checkout", reqPerm(domain.PermissionBillingManage), authEnterpriseHandler.TopupCheckout)
			tenant.POST("/portals/:id/articles/bulk_actions", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.BulkArticleActions)

			// 自定义业务指标与事件上报 (RPT / Reporting Events)
			tenant.GET("/reporting_events", authEnterpriseHandler.ListReportingEvents)
			tenant.POST("/reporting_events", authEnterpriseHandler.CreateReportingEvent)

			// 账号引导与白标邮件布局 (ENT)
			tenant.GET("/onboarding", authEnterpriseHandler.GetOnboarding)
			tenant.POST("/onboarding", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveOnboarding)
			tenant.GET("/branded_email_layout", authEnterpriseHandler.GetBrandedEmailLayout)
			tenant.POST("/branded_email_layout", reqPerm(domain.PermissionSettingsManage), authEnterpriseHandler.SaveBrandedEmailLayout)

			// 全局与局部搜索 (Search - Section 15)
			tenant.GET("/search", searchHandler.GlobalSearch)
			tenant.GET("/search/conversations", searchHandler.SearchConversations)
			tenant.GET("/search/contacts", searchHandler.SearchContacts)
			tenant.GET("/search/messages", searchHandler.SearchMessages)
			tenant.GET("/search/articles", searchHandler.SearchArticles)
			tenant.GET("/cache_keys", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"cache_keys": gin.H{"label": "1", "inbox": "1"}})
			})
		}

		// 业务度量报表 (RPT / V2 API)
		reports := api.Group("/api/v2/accounts/:account_id/reports")
		reports.Use(middleware.TenantMiddleware(accountRepo))
		reports.Use(middleware.RequirePermission(accountRepo, domain.PermissionReportView))
		{
			reports.GET("/summary", reportHandler.GetSummary)
			reports.GET("/agents", reportHandler.GetAgentMetrics)
			reports.GET("/csat", macroHandler.GetCSATReport)
			reports.GET("/csat/download", csatHandler.DownloadSurveys)
			reports.GET("/csat/responses", csatHandler.ListSurveys)
			reports.GET("/trends", reportHandler.GetTrends)
			reports.GET("/teams", reportHandler.GetTeamMetrics)
			reports.GET("/inboxes", reportHandler.GetInboxMetrics)
			reports.GET("/labels", reportHandler.GetLabelMetrics)
			reports.GET("/first_response", reportHandler.GetFirstResponseDistribution)
			reports.GET("/first_response_distribution", reportHandler.GetFirstResponseDistribution)
			reports.GET("/first_response_time_distribution", reportHandler.GetFirstResponseDistribution)
			reports.GET("/conversations/export", reportHandler.ExportConversationsCSV)
			reports.GET("/applied_slas", appliedSLAHandler.ListAppliedSLAs)
			reports.GET("/applied_slas/metrics", appliedSLAHandler.GetMetrics)
			reports.GET("/applied_slas/download", appliedSLAHandler.Download)
			reports.GET("/sla/metrics", appliedSLAHandler.GetMetrics)
			reports.GET("/sla/download", appliedSLAHandler.Download)

			// 补齐缺失的 12 项报表 (Chatwoot V2 Alignment)
			reports.GET("/conversations", reportHandler.GetConversationsReport)
			reports.GET("/conversations_summary", reportHandler.GetConversationsSummary)
			reports.GET("/conversation_traffic", reportHandler.GetConversationTraffic)
			reports.GET("/drilldown", reportHandler.GetDrilldown)
			reports.GET("/channel_summary", reportHandler.GetChannelSummary)
			reports.GET("/bot_summary", reportHandler.GetBotSummary)
			reports.GET("/bot_metrics", reportHandler.GetBotMetrics)
			reports.GET("/inbox_label_matrix", reportHandler.GetInboxLabelMatrix)
			reports.GET("/outgoing_messages_count", reportHandler.GetOutgoingMessagesCount)
			reports.GET("/live_conversation_metrics", reportHandler.GetLiveConversationMetrics)
			reports.GET("/grouped_live_metrics", reportHandler.GetGroupedLiveMetrics)
			reports.GET("/year_in_review", reportHandler.GetYearInReview)
		}

		// V2 汇总报表 (Summary Reports: agent, team, inbox, label, channel)
		summaryReports := api.Group("/api/v2/accounts/:account_id/summary_reports")
		summaryReports.Use(middleware.TenantMiddleware(accountRepo))
		summaryReports.Use(middleware.RequirePermission(accountRepo, domain.PermissionReportView))
		{
			summaryReports.GET("/agent", reportHandler.GetAgentMetrics)
			summaryReports.GET("/team", reportHandler.GetTeamMetrics)
			summaryReports.GET("/inbox", reportHandler.GetInboxMetrics)
			summaryReports.GET("/label", reportHandler.GetLabelMetrics)
			summaryReports.GET("/channel", reportHandler.GetChannelSummary)
		}

		// V2 实时会话报表 (Live Reports: conversation_metrics, grouped_conversation_metrics)
		liveReports := api.Group("/api/v2/accounts/:account_id/live_reports")
		liveReports.Use(middleware.TenantMiddleware(accountRepo))
		liveReports.Use(middleware.RequirePermission(accountRepo, domain.PermissionReportView))
		{
			liveReports.GET("/conversation_metrics", reportHandler.GetLiveConversationMetrics)
			liveReports.GET("/grouped_conversation_metrics", reportHandler.GetGroupedLiveMetrics)
		}

		// V2 年度服务总结 (Year In Review)
		yearInReview := api.Group("/api/v2/accounts/:account_id/year_in_review")
		yearInReview.Use(middleware.TenantMiddleware(accountRepo))
		yearInReview.Use(middleware.RequirePermission(accountRepo, domain.PermissionReportView))
		{
			yearInReview.GET("", reportHandler.GetYearInReview)
		}

		// 企业配额与计费结算 (ENT / Limits & Billing)
		enterpriseAPI := api.Group("/enterprise/api/v1/accounts/:account_id")
		enterpriseAPI.Use(middleware.TenantMiddleware(accountRepo))
		enterpriseAPI.Use(middleware.RequirePermission(accountRepo, domain.PermissionBillingManage))
		{
			enterpriseAPI.GET("/limits", authEnterpriseHandler.GetLimits)
			enterpriseAPI.POST("/limits", authEnterpriseHandler.UpdateLimits)
			enterpriseAPI.GET("/billing", authEnterpriseHandler.ListBillings)
			enterpriseAPI.POST("/billing", authEnterpriseHandler.RecordBilling)
			enterpriseAPI.POST("/email_channel_migration", authEnterpriseHandler.EmailChannelMigration)
			enterpriseAPI.GET("/subscription/plans", authEnterpriseHandler.ListSubscriptionPlans)
			enterpriseAPI.GET("/subscription", authEnterpriseHandler.GetSubscription)
			enterpriseAPI.POST("/subscription", authEnterpriseHandler.CreateOrUpdateSubscription)
			enterpriseAPI.POST("/checkout", authEnterpriseHandler.Checkout)
			enterpriseAPI.POST("/select_billing_currency", authEnterpriseHandler.SelectBillingCurrency)
			enterpriseAPI.POST("/toggle_deletion", authEnterpriseHandler.ToggleDeletion)
			enterpriseAPI.GET("/topup_options", authEnterpriseHandler.TopupOptions)
			enterpriseAPI.POST("/topup_checkout", authEnterpriseHandler.TopupCheckout)
		}
	}

	return r
}
