package router

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
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
	r := gin.Default()

	// 1. 全局拦截器：统一上下文与全链路跟踪 (API-03 规范)
	r.Use(foundation.UnifiedContextMiddleware())

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
	campaignService := service.NewCampaignService(db, convRepo, msgRepo, contactRepo)

	// API 处理器 (Handlers)
	authHandler := handler.NewAuthHandler(cfg, userRepo, accountRepo)
	accountHandler := handler.NewAccountHandler(accountRepo, userRepo)
	inboxHandler := handler.NewInboxHandler(inboxRepo, userRepo)
	contactHandler := handler.NewContactHandler(contactRepo, inboxRepo)
	convHandler := handler.NewConversationHandler(convRepo, msgRepo, inboxRepo, contactRepo, routingService, hub)
	convHandler.SetAutomationAndWebhook(automationService, webhookService)
	convHandler.SetPushService(pushService)
	opsHandler := handler.NewOpsHandler(labelRepo, cannedRepo, convRepo)
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
	publicHandler := handler.NewPublicHandler(db, inboxRepo, contactRepo, convRepo, msgRepo)
	deviceHandler := handler.NewDeviceHandler(deviceRepo)
	advancedHandler := handler.NewAdvancedHandler(db, companyRepo, campaignRepo, slaRepo, agentBotRepo, attachmentRepo)
	advancedHandler.SetServices(campaignService, slaService)
	if globalAdvancedHTTPClient != nil {
		advancedHandler.SetHTTPClient(globalAdvancedHTTPClient)
	}
	channelDriverHandler := handler.NewChannelDriverHandler(channelEnterpriseRepo, inboxRepo, contactRepo, convRepo, msgRepo)
	authEnterpriseHandler := handler.NewAuthEnterpriseHandler(channelEnterpriseRepo, userRepo, accountRepo, portalRepo, contactRepo, cfg)
	authHandler.SetEnterpriseRepo(channelEnterpriseRepo)
	copilotHandler := handler.NewCopilotHandler(db, convRepo, msgRepo, portalRepo, cannedRepo)
	searchHandler := handler.NewSearchHandler(db)

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
		widget.POST("/contact", contactHandler.WidgetIdentify)
		widget.POST("/conversations", convHandler.WidgetCreateConversation)
		widget.GET("/messages", convHandler.WidgetListMessages)
		widget.POST("/messages", convHandler.WidgetCreateMessage)
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
		publicGroup.POST("/inboxes/:identifier/contacts", publicHandler.CreateContact)
		publicGroup.POST("/inboxes/:identifier/contacts/:contact_id/conversations", publicHandler.CreateConversation)
		publicGroup.POST("/inboxes/:identifier/contacts/:contact_id/conversations/:conversation_id/messages", publicHandler.CreateMessage)
		publicGroup.POST("/csat_survey/:id", macroHandler.SubmitCSAT)
		publicGroup.GET("/channels/facebook/webhook", channelDriverHandler.VerifyFacebookWebhook)
		publicGroup.POST("/channels/facebook/webhook", channelDriverHandler.HandleFacebookWebhook)
		publicGroup.POST("/subscription/webhook", authEnterpriseHandler.HandleSubscriptionWebhook)
	}

	// 4.2 平台管理开放接口 (Platform API)
	platformGroup := r.Group("/platform/api/v1")
	platformGroup.Use(platformHandler.PlatformAuthMiddleware())
	{
		platformGroup.POST("/accounts", platformHandler.CreateAccount)
		platformGroup.GET("/accounts/:id", platformHandler.GetAccount)
		platformGroup.POST("/users", platformHandler.CreateUser)
		platformGroup.POST("/accounts/:id/account_users", platformHandler.AddAccountUser)
		platformGroup.POST("/accounts/:id/email_channel_migrations", authEnterpriseHandler.EmailChannelMigration)
		platformGroup.GET("/accounts/:id/data_changes", auditHandler.PlatformListDataChanges)
	}

	// 5. 身份登录与注册接口 (IAM / AUTH)
	authGroup := r.Group("/auth")
	{
		authGroup.POST("/sign_up", authHandler.SignUp)
		authGroup.POST("/sign_in", authHandler.SignIn)
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
	api.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		api.GET("/api/v1/profile", authHandler.Profile)
		api.PUT("/api/v1/profile", authHandler.UpdateProfile)
		api.POST("/api/v1/profile/availability", authHandler.UpdateAvailability)
		api.GET("/api/v1/profile/mfa", authEnterpriseHandler.GetMFAProfile)
		api.POST("/api/v1/profile/mfa", authEnterpriseHandler.EnableMFA)
		api.DELETE("/api/v1/profile/mfa", authEnterpriseHandler.DisableMFA)
		api.GET("/api/v1/profile/sessions", authEnterpriseHandler.ListSessions)
		api.DELETE("/api/v1/profile/sessions/:session_id", authEnterpriseHandler.DeleteSession)
		api.POST("/api/v1/accounts", accountHandler.CreateAccount)

		// 超级管理员安装级配置 (SYS / ENT)
		api.GET("/api/v1/super_admin/configs", enterpriseHandler.ListSystemConfigs)
		api.POST("/api/v1/super_admin/configs", enterpriseHandler.SetSystemConfig)

		// 租户空间路由组 (严格多租户数据隔离)
		tenant := api.Group("/api/v1/accounts/:account_id")
		tenant.Use(middleware.TenantMiddleware(accountRepo))
		{
			// 账号与坐席 (ACC)
			tenant.GET("", accountHandler.GetAccount)
			tenant.PUT("", accountHandler.UpdateAccount)
			tenant.GET("/agents", accountHandler.ListAgents)
			tenant.POST("/agents", accountHandler.AddAgent)
			tenant.PUT("/agents/:id", accountHandler.UpdateAgent)
			tenant.DELETE("/agents/:id", accountHandler.RemoveAgent)

			// 自定义角色 (Custom Roles)
			tenant.GET("/custom_roles", accountHandler.ListCustomRoles)
			tenant.POST("/custom_roles", accountHandler.CreateCustomRole)
			tenant.GET("/custom_roles/:id", accountHandler.GetCustomRole)
			tenant.PUT("/custom_roles/:id", accountHandler.UpdateCustomRole)
			tenant.DELETE("/custom_roles/:id", accountHandler.DeleteCustomRole)

			// 团队管理 (Teams)
			tenant.GET("/teams", teamHandler.ListTeams)
			tenant.POST("/teams", teamHandler.CreateTeam)
			tenant.GET("/teams/:id", teamHandler.GetTeam)
			tenant.PUT("/teams/:id", teamHandler.UpdateTeam)
			tenant.DELETE("/teams/:id", teamHandler.DeleteTeam)
			tenant.POST("/teams/:id/members", teamHandler.AddMembers)
			tenant.GET("/teams/:id/members", teamHandler.ListMembers)
			tenant.DELETE("/teams/:id/members/:member_id", teamHandler.RemoveMember)

			// 渠道与收件箱 (CHN / INB)
			tenant.GET("/inboxes", inboxHandler.ListInboxes)
			tenant.POST("/inboxes", inboxHandler.CreateInbox)
			tenant.GET("/inboxes/:id", inboxHandler.GetInbox)
			tenant.PUT("/inboxes/:id", inboxHandler.UpdateInbox)
			tenant.DELETE("/inboxes/:id", inboxHandler.DeleteInbox)
			tenant.GET("/inboxes/:id/members", inboxHandler.ListInboxMembers)
			tenant.POST("/inboxes/:id/members", inboxHandler.AddInboxMembers)

			// 客户档案与合并 (CRM / CUS)
			tenant.GET("/contacts", contactHandler.ListContacts)
			tenant.POST("/contacts", contactHandler.CreateContact)
			tenant.GET("/contacts/:id", contactHandler.GetContact)
			tenant.PUT("/contacts/:id", contactHandler.UpdateContact)
			tenant.DELETE("/contacts/:id", contactHandler.DeleteContact)
			tenant.POST("/actions/contact_merge", contactHandler.MergeContact)

			// 自定义字段定义 (Custom Attributes)
			tenant.GET("/custom_attribute_definitions", customAttrHandler.List)
			tenant.POST("/custom_attribute_definitions", customAttrHandler.Create)
			tenant.DELETE("/custom_attribute_definitions/:id", customAttrHandler.Delete)

			// 会话与消息状态机 (CON / MSG)
			tenant.GET("/conversations", convHandler.ListConversations)
			tenant.POST("/conversations", convHandler.CreateConversation)
			tenant.GET("/conversations/:id", convHandler.GetConversation)
			tenant.POST("/conversations/:id/toggle_status", convHandler.ToggleStatus)
			tenant.POST("/conversations/:id/assignments", convHandler.Assign)
			tenant.GET("/conversations/:id/messages", convHandler.ListMessages)
			tenant.POST("/conversations/:id/messages", convHandler.CreateMessage)

			// 快捷回复 (OPS)
			tenant.GET("/canned_responses", opsHandler.ListCannedResponses)
			tenant.POST("/canned_responses", opsHandler.CreateCannedResponse)
			tenant.PUT("/canned_responses/:id", opsHandler.UpdateCannedResponse)
			tenant.DELETE("/canned_responses/:id", opsHandler.DeleteCannedResponse)

			// 宏执行与批量处理 (OPS-01 ~ 04)
			tenant.GET("/macros", macroHandler.ListMacros)
			tenant.POST("/macros", macroHandler.CreateMacro)
			tenant.PUT("/macros/:id", macroHandler.UpdateMacro)
			tenant.POST("/macros/:id/execute", macroHandler.ExecuteMacro)
			tenant.DELETE("/macros/:id", macroHandler.DeleteMacro)

			// 站内通知与多端状态 (OPS-14 ~ 18)
			tenant.GET("/notifications", macroHandler.ListNotifications)
			tenant.POST("/notifications/read_all", macroHandler.MarkAllNotificationsRead)

			// 移动推送与设备订阅 (MOB-01 ~ 05)
			tenant.POST("/notification_subscriptions", deviceHandler.RegisterSubscription)
			tenant.DELETE("/notification_subscriptions", deviceHandler.DeleteSubscription)

			// 标签体系 (Labels)
			tenant.GET("/labels", opsHandler.ListLabels)
			tenant.POST("/labels", opsHandler.CreateLabel)
			tenant.PUT("/labels/:id", opsHandler.UpdateLabel)
			tenant.DELETE("/labels/:id", opsHandler.DeleteLabel)
			tenant.POST("/conversations/:id/labels", opsHandler.AttachConversationLabels)
			tenant.GET("/conversations/:id/labels", opsHandler.GetConversationLabels)

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
			tenant.GET("/webhooks", webhookHandler.List)
			tenant.POST("/webhooks", webhookHandler.Create)
			tenant.PUT("/webhooks/:id", webhookHandler.Update)
			tenant.DELETE("/webhooks/:id", webhookHandler.Delete)
			tenant.POST("/webhooks/deliveries/:id/retry", webhookHandler.RetryDelivery)
			tenant.POST("/webhooks/deliveries/retry", webhookHandler.RetryDelivery)

			// 企业能力与 Feature 开关 (ENT)
			tenant.GET("/features", enterpriseHandler.GetAccountFeatures)
			tenant.POST("/features", enterpriseHandler.SetAccountFeature)

			// 公司与组织客户 (CRM / Companies)
			tenant.GET("/companies", advancedHandler.ListCompanies)
			tenant.POST("/companies", advancedHandler.CreateCompany)
			tenant.GET("/companies/:id", advancedHandler.GetCompany)
			tenant.PUT("/companies/:id", advancedHandler.UpdateCompany)
			tenant.DELETE("/companies/:id", advancedHandler.DeleteCompany)

			// 营销活动 (OPS / Campaigns)
			tenant.GET("/campaigns", advancedHandler.ListCampaigns)
			tenant.POST("/campaigns", advancedHandler.CreateCampaign)
			tenant.POST("/campaigns/:id/trigger", advancedHandler.TriggerCampaign)

			// 自动化规则 (OPS / Automation Rules)
			tenant.GET("/automation_rules", advancedHandler.ListAutomationRules)
			tenant.POST("/automation_rules", advancedHandler.CreateAutomationRule)
			tenant.GET("/automation_rules/:id", advancedHandler.GetAutomationRule)
			tenant.PUT("/automation_rules/:id", advancedHandler.UpdateAutomationRule)
			tenant.DELETE("/automation_rules/:id", advancedHandler.DeleteAutomationRule)

			// 服务水平协议 (RPT / SLA)
			tenant.GET("/sla_policies", advancedHandler.ListSLAPolicies)
			tenant.POST("/sla_policies", advancedHandler.CreateSLAPolicy)
			tenant.PUT("/sla_policies/:id", advancedHandler.UpdateSLAPolicy)
			tenant.POST("/sla_policies/process", advancedHandler.ProcessSLA)
			tenant.POST("/sla/process", advancedHandler.ProcessSLA)
			tenant.POST("/slas/evaluate", advancedHandler.ProcessSLA)
			tenant.POST("/sla/evaluate", advancedHandler.ProcessSLA)
			tenant.GET("/sla_policies/breaches", advancedHandler.ListSLABreaches)
			tenant.GET("/sla_breaches", advancedHandler.ListSLABreaches)
			tenant.GET("/slas/breaches", advancedHandler.ListSLABreaches)

			// 机器人与集成 (EXT / AgentBots)
			tenant.GET("/agent_bots", advancedHandler.ListAgentBots)
			tenant.POST("/agent_bots", advancedHandler.CreateAgentBot)

			// 消息附件上传 (CONV / Attachments)
			tenant.POST("/conversations/:id/attachments", advancedHandler.UploadAttachment)

			// 会话草稿 (CONV / Draft Messages)
			tenant.GET("/conversations/:id/draft_messages", advancedHandler.GetDraft)
			tenant.POST("/conversations/:id/draft_messages", advancedHandler.SaveDraft)

			// 会话协作参与者 (CONV / Participants)
			tenant.GET("/conversations/:id/participants", advancedHandler.ListParticipants)
			tenant.POST("/conversations/:id/participants", advancedHandler.AddParticipants)

			// 联系人内部备注 (CRM / Contact Notes)
			tenant.GET("/contacts/:id/notes", advancedHandler.ListContactNotes)
			tenant.POST("/contacts/:id/notes", advancedHandler.CreateContactNote)

			// 自定义筛选器 (OPS / Custom Filters)
			tenant.GET("/custom_filters", advancedHandler.ListCustomFilters)
			tenant.POST("/custom_filters", advancedHandler.CreateCustomFilter)
			tenant.DELETE("/custom_filters/:id", advancedHandler.DeleteCustomFilter)

			// 坐席容量策略 (ACC / Capacity Policies)
			tenant.GET("/agent_capacity_policies", advancedHandler.ListCapacityPolicies)
			tenant.POST("/agent_capacity_policies", advancedHandler.SetCapacityPolicy)
			tenant.GET("/capacity_policies", advancedHandler.ListCapacityPolicies)
			tenant.POST("/capacity_policies", advancedHandler.SetCapacityPolicy)

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
			tenant.GET("/captain/assistants", copilotHandler.ListAssistants)
			tenant.POST("/captain/assistants", copilotHandler.CreateAssistant)
			tenant.GET("/captain/knowledge_bases", copilotHandler.ListKnowledgeDocs)
			tenant.POST("/captain/knowledge_bases", copilotHandler.CreateKnowledgeDoc)
			tenant.GET("/captain/knowledge_docs", copilotHandler.ListKnowledgeDocs)
			tenant.POST("/captain/knowledge_docs", copilotHandler.CreateKnowledgeDoc)
			tenant.GET("/captain/knowledge_chunks", copilotHandler.SearchKnowledgeChunks)
			tenant.GET("/captain/scenarios", copilotHandler.ListScenarios)
			tenant.POST("/captain/scenarios", copilotHandler.CreateScenario)
			tenant.PUT("/captain/scenarios/:id", copilotHandler.UpdateScenario)
			tenant.DELETE("/captain/scenarios/:id", copilotHandler.DeleteScenario)
			tenant.POST("/captain/tools/execute", copilotHandler.ExecuteAITool)
			tenant.GET("/captain/quota", copilotHandler.GetAIQuota)

			// 变更追踪与审计日志 (LOG / AUD: LOG-01 ~ LOG-08)
			tenant.GET("/audit_logs", auditHandler.ListAuditLogs)
			tenant.GET("/audit_logs/:id", auditHandler.GetAuditLog)
			tenant.GET("/data_changes", auditHandler.ListDataChanges)
			tenant.GET("/data_changes/:change_id", auditHandler.GetDataChange)
			tenant.POST("/data_changes/:change_id/corrections", auditHandler.CreateDataChangeCorrection)
			tenant.GET("/audit_objects/:object_type/:object_id/timeline", auditHandler.GetObjectTimeline)
			tenant.GET("/audit_processes/:correlation_id/timeline", auditHandler.GetProcessTimeline)
			tenant.GET("/security_audit_logs", auditHandler.ListSecurityAuditLogs)
			tenant.POST("/audit_exports", auditHandler.CreateAuditExport)
			tenant.GET("/audit_exports/:export_id", auditHandler.GetAuditExport)
			tenant.POST("/audit_integrity/verifications", auditHandler.CreateIntegrityVerification)
			tenant.GET("/audit_integrity/verifications/:verification_id", auditHandler.GetIntegrityVerification)

			// 外部渠道命令与授权 (CHAN)
			tenant.POST("/whatsapp/authorization", channelDriverHandler.WhatsAppAuthorization)
			tenant.POST("/inboxes/:id/csat_template", channelDriverHandler.SetWhatsAppCSATTemplate)
			tenant.POST("/channels/twilio_channel", channelDriverHandler.CreateTwilioChannel)

			// 语音呼叫与 WebRTC 会议 (CHAN / Calls & Conference)
			tenant.GET("/calls", channelDriverHandler.ListCalls)
			tenant.POST("/contacts/:id/call", channelDriverHandler.InitiateContactCall)
			tenant.POST("/calls/:id/accept", channelDriverHandler.AcceptCall)
			tenant.POST("/calls/:id/reject", channelDriverHandler.RejectCall)
			tenant.POST("/calls/:id/end", channelDriverHandler.EndCall)
			tenant.POST("/calls/:id/candidates", channelDriverHandler.AddCallCandidate)
			tenant.GET("/calls/:id/candidates", channelDriverHandler.ListCallCandidates)
			tenant.POST("/inboxes/:id/conference", channelDriverHandler.CreateConference)
			tenant.POST("/whatsapp_calls", channelDriverHandler.HandleWhatsAppCallSDP)

			// 企业单点登录设置 (SAML)
			tenant.GET("/saml_settings", authEnterpriseHandler.GetSAMLSetting)
			tenant.POST("/saml_settings", authEnterpriseHandler.SaveSAMLSetting)

			// 数据导入与批量迁移 (ROUTE & MIG)
			tenant.GET("/data_imports", authEnterpriseHandler.ListDataImports)
			tenant.POST("/data_imports", authEnterpriseHandler.CreateDataImport)
			tenant.GET("/migration_jobs", authEnterpriseHandler.ListMigrationJobs)
			tenant.POST("/migration_jobs", authEnterpriseHandler.CreateMigrationJob)
			tenant.POST("/email_channel_migration", authEnterpriseHandler.EmailChannelMigration)
			tenant.GET("/subscription/plans", authEnterpriseHandler.ListSubscriptionPlans)
			tenant.GET("/subscription", authEnterpriseHandler.GetSubscription)
			tenant.POST("/subscription", authEnterpriseHandler.CreateOrUpdateSubscription)
			tenant.POST("/portals/:id/articles/bulk_actions", authEnterpriseHandler.BulkArticleActions)

			// 自定义业务指标与事件上报 (RPT / Reporting Events)
			tenant.GET("/reporting_events", authEnterpriseHandler.ListReportingEvents)
			tenant.POST("/reporting_events", authEnterpriseHandler.CreateReportingEvent)

			// 账号引导与白标邮件布局 (ENT)
			tenant.GET("/onboarding", authEnterpriseHandler.GetOnboarding)
			tenant.POST("/onboarding", authEnterpriseHandler.SaveOnboarding)
			tenant.GET("/branded_email_layout", authEnterpriseHandler.GetBrandedEmailLayout)
			tenant.POST("/branded_email_layout", authEnterpriseHandler.SaveBrandedEmailLayout)

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
		{
			reports.GET("/summary", reportHandler.GetSummary)
			reports.GET("/agents", reportHandler.GetAgentMetrics)
			reports.GET("/csat", macroHandler.GetCSATReport)
			reports.GET("/trends", reportHandler.GetTrends)
			reports.GET("/teams", reportHandler.GetTeamMetrics)
			reports.GET("/inboxes", reportHandler.GetInboxMetrics)
			reports.GET("/labels", reportHandler.GetLabelMetrics)
			reports.GET("/first_response", reportHandler.GetFirstResponseDistribution)
			reports.GET("/first_response_distribution", reportHandler.GetFirstResponseDistribution)
			reports.GET("/first_response_time_distribution", reportHandler.GetFirstResponseDistribution)
			reports.GET("/conversations/export", reportHandler.ExportConversationsCSV)
		}

		// 企业配额与计费结算 (ENT / Limits & Billing)
		enterpriseAPI := api.Group("/enterprise/api/v1/accounts/:account_id")
		enterpriseAPI.Use(middleware.TenantMiddleware(accountRepo))
		{
			enterpriseAPI.GET("/limits", authEnterpriseHandler.GetLimits)
			enterpriseAPI.POST("/limits", authEnterpriseHandler.UpdateLimits)
			enterpriseAPI.GET("/billing", authEnterpriseHandler.ListBillings)
			enterpriseAPI.POST("/billing", authEnterpriseHandler.RecordBilling)
			enterpriseAPI.POST("/email_channel_migration", authEnterpriseHandler.EmailChannelMigration)
			enterpriseAPI.GET("/subscription/plans", authEnterpriseHandler.ListSubscriptionPlans)
			enterpriseAPI.GET("/subscription", authEnterpriseHandler.GetSubscription)
			enterpriseAPI.POST("/subscription", authEnterpriseHandler.CreateOrUpdateSubscription)
		}
	}

	return r
}
