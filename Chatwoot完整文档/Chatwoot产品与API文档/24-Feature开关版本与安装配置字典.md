# Chatwoot Feature 开关、版本与安装配置字典

> 导航：[全接口功能与文档覆盖矩阵](23-全接口功能与文档覆盖矩阵.md)｜[Super Admin 与安装级管理功能](25-SuperAdmin与安装级管理功能.md)

## 1. 配置层次

Chatwoot 的可用功能由四层条件共同决定：

1. 当前版本是否包含该能力；
2. 安装范围是否具备所需凭证、域名和全局参数；
3. Account 是否启用相应 Feature；
4. 当前用户角色、Inbox 类型和第三方连接是否满足条件。

Feature 是 Account 范围开关；安装配置是整个实例共享的能力参数。打开 Feature 不会自动补齐第三方凭证，也不会绕过角色权限或企业授权。

## 2. Feature 管理方式

Feature 主要在 Super Admin 的 Account 页面查看和调整。Account 接口返回的 feature_flags 用于说明当前账号可见能力，不应由普通 Account 更新接口直接覆盖。

| 属性 | 含义 |
|---|---|
| Feature Key | 稳定的能力标识 |
| 展示名称 | 管理页面中的名称 |
| 默认状态 | 新 Account 初始是否启用 |
| 企业能力 | 是否还要求企业授权 |
| 系统管理 | 是否主要供 Cloud、灰度或兼容流程管理 |
| 已弃用 | 为兼容历史状态保留，不建议新 Account 依赖 |

## 3. 默认启用的 27 个 Feature

| Feature Key | 功能 | 设计边界 |
|---|---|---|
| inbound_emails | 接收邮件并转换为会话消息 | 还要求入站邮件域名和邮件渠道配置 |
| channel_email | Email Inbox | 授权邮箱和普通邮箱字段不同 |
| channel_facebook | Facebook Messenger Inbox | 还要求 Facebook 应用凭证和授权 |
| help_center | 帮助中心 | 控制 Portal、Category 和 Article 入口 |
| agent_bots | Agent Bot | Bot 仍需绑定 Inbox 才能处理会话 |
| macros | Macro | 执行动作仍受当前用户权限限制 |
| agent_management | 成员管理 | 管理权限默认属于 Administrator |
| team_management | 团队管理 | Team 只在同一 Account 内有效 |
| inbox_management | Inbox 管理 | 创建不同渠道还需要对应 Feature 和凭证 |
| labels | 标签 | 标签目录属于 Account |
| custom_attributes | 自定义字段 | 只启用定义与使用能力，不自动创建字段 |
| automations | 自动化规则 | 无效授权或连续执行失败时规则可能停用 |
| canned_responses | 快捷回复 | short_code 在 Account 内使用 |
| integrations | 集成目录 | 单项集成还可能有独立 Feature 和凭证 |
| voice_recorder | 语音录制 | 影响录音入口，不等于启用语音电话渠道 |
| channel_website | Web Widget Inbox | 还受域名、身份校验和 Widget 设置影响 |
| campaigns | 活动消息 | 发送能力取决于 Inbox 类型和客户授权状态 |
| reports | 报表 | 指标可见范围受角色和 Inbox 访问范围影响 |
| crm | 联系人资料 | 提供联系人、标签和客户信息能力 |
| auto_resolve_conversations | 自动解决长期无活动会话 | 需配置超时规则 |
| chatwoot_v4 | 4.x 产品体验 | 版本体验标识，不代表全部可选 Feature 已开启 |
| contact_chatwoot_support_team | 联系 Chatwoot 支持 | 系统管理能力，依赖支持 Inbox 配置 |
| channel_instagram | Instagram Inbox | 还要求 Instagram 应用凭证和授权 |
| assignment_v2 | 新分配流程 | 作为当前默认分配能力入口 |
| channel_tiktok | TikTok Inbox | 还要求 TikTok 应用凭证和授权 |
| captain_tasks | Captain 文本任务 | AI 调用仍要求模型凭证、额度和功能偏好 |
| api_and_webhooks | API 与 Webhook | 作为开放接口总开关，不替代接口身份认证 |

## 4. 默认关闭的标准 Feature

| Feature Key | 功能 | 启用前置条件 |
|---|---|---|
| conversation_unread_counts | 会话未读计数 | 系统管理；需确认客户端统计口径 |
| ip_lookup | IP 地理信息查询 | 需提供合法的数据来源并满足隐私要求 |
| email_continuity_on_api_channel | API 渠道邮件连续性 | 需要入站邮件域名及稳定的会话映射 |
| report_rollup | 报表预聚合 | 需要确认历史报表和实时口径一致 |
| custom_reply_email | 自定义回复邮箱 | 需要邮件发送域和回信地址配置 |
| custom_reply_domain | 自定义回复域名 | 需要域名验证和入站邮件路由 |
| branded_email_templates | 品牌化邮件模板 | 需要品牌资源和邮件内容设置 |
| inbox_view | Inbox 视图 | 系统管理；用于特定界面范围 |
| linear_integration | Linear 集成 | LINEAR_CLIENT_ID、LINEAR_CLIENT_SECRET 和授权回调 |
| shopify_integration | Shopify 集成 | 系统管理；还要求 Shopify 凭证 |
| search_with_gin | 特定消息搜索方式 | 系统管理；不改变搜索接口字段 |
| crm_integration | CRM 集成 | 用于 LeadSquared 等 CRM 连接 |
| notion_integration | Notion 集成 | Notion 凭证和授权回调 |
| whatsapp_campaign | WhatsApp Campaign | WhatsApp Inbox、模板和发送资格 |
| crm_v2 | 新 CRM 体验 | 系统管理；应与当前 CRM 使用范围一起评估 |
| reply_mailer_migration | 回复邮件迁移 | 系统管理的过渡能力 |
| unread_count_for_filters | 保存筛选未读计数 | 系统管理；需统一筛选与未读口径 |
| whatsapp_manual_transfer | WhatsApp 手工转移 | 对应 Account 开关和可用的 WhatsApp 号码 |
| data_import | 数据导入 | 还需要上传限制、字段映射和管理员权限 |
| whatsapp_reconfigure | WhatsApp 重新配置 | 用于已有 Inbox 的重新授权流程 |
| whatsapp_embedded_signup_inbox_creation | Embedded Signup 创建 Inbox | WhatsApp 应用、Configuration ID 和授权结果齐全 |

## 5. 默认关闭的企业 Feature

| Feature Key | 功能 | 设计边界 |
|---|---|---|
| disable_branding | 去除品牌标识 | 受企业授权和品牌配置共同控制 |
| audit_logs | 审计日志 | 用于追踪高价值管理行为 |
| custom_tools | Captain 自定义工具 | 允许 AI 调外部 HTTP 服务，必须限制 Endpoint、认证和返回内容 |
| sla | SLA | 启用后仍需创建策略并匹配 Inbox |
| help_center_embedding_search | 帮助中心语义检索 | 系统管理；还要求 Embedding 模型和索引可用 |
| captain_integration | Captain v1 | AI 凭证、额度和 Account 偏好必须可用 |
| custom_roles | 自定义角色 | 权限必须取自允许的权限集合 |
| captain_v1_action_classifier | Captain v1 动作分类 | 系统管理；用于特定 AI 流程 |
| channel_voice | 语音渠道 | 还需号码、Provider、回调和通话资格 |
| captain_integration_v2 | Captain v2 | 控制新版 Assistant 路由和能力 |
| captain_document_auto_sync | Captain 文档自动同步 | 需同步间隔和批量上限配置 |
| advanced_search | 高级搜索 | 还可能要求高级索引已准备完成 |
| saml | SAML SSO | 还需 Account SAML 元数据和安装登录开关 |
| advanced_search_indexing | 高级搜索索引 | 系统管理；用于索引准备过程 |
| companies | Company | 启用后开放公司对象和联系人关联 |
| csat_review_notes | CSAT 审核备注 | 用于管理员复核客户满意度记录 |
| conversation_required_attributes | 会话必填字段 | 需为会话配置有效字段定义 |
| advanced_assignment | 高级分配 | 依赖分配策略、容量策略和 Inbox 绑定 |

## 6. 已弃用 Feature

| Feature Key | 状态 | 使用原则 |
|---|---|---|
| message_reply_to | 已弃用 | 仅为兼容历史 Account 状态保留，不用于新方案 |
| whatsapp_embedded_signup | 已弃用 | 新流程使用 whatsapp_embedded_signup_inbox_creation 和 whatsapp_reconfigure |

Feature 的持久化位置与顺序属于升级兼容约束。管理时只能按 Feature Key 切换，不应重新解释序号或复用已存在的 Key。

## 7. 安装配置访问规则

当前版本定义 102 个安装配置项。未锁定项可以在 Super Admin 安装配置页面维护；锁定项由版本、Cloud、企业授权或实例级参数提供。敏感项在页面和日志中应遮蔽，不能通过 Account API、Webhook 或普通页面返回。

| 类型 | 处理原则 |
|---|---|
| 文本或 URL | 校验格式、域名和回调地址 |
| boolean | 明确 true 与 false，不能把空值自动理解为已开启 |
| number | 校验正数、单位和上限 |
| JSON 对象 | 保存前校验结构和计划 Key |
| secret | 仅允许替换，不应回显完整值 |

## 8. 品牌和公开页面配置（10 项）

| 配置 Key | 功能 | 管理状态 |
|---|---|---|
| INSTALLATION_NAME | Dashboard、标题和测试推送中的实例名称 | 系统管理 |
| LOGO_THUMBNAIL | favicon 和小尺寸品牌图 | 系统管理 |
| LOGO | 默认主题 Logo | 系统管理 |
| LOGO_DARK | 深色主题 Logo | 系统管理 |
| BRAND_URL | 邮件 Powered By 链接 | 系统管理 |
| WIDGET_BRAND_URL | Widget Powered By 链接 | 系统管理 |
| BRAND_NAME | 邮件和 Widget 品牌名 | 系统管理 |
| TERMS_URL | 注册页服务条款 | 系统管理 |
| PRIVACY_URL | 隐私政策 | 系统管理 |
| DISPLAY_MANIFEST | 是否显示默认元数据和升级提示 | 系统管理 |

## 9. 注册、Account、上传和 Webhook（8 项）

| 配置 Key | 功能 | 默认或单位 | 可在安装配置页维护 |
|---|---|---|---|
| ENABLE_ACCOUNT_SIGNUP | 允许公开注册 Account | 默认 false | 是 |
| CREATE_NEW_ACCOUNT_FROM_DASHBOARD | 允许从 Dashboard 新建 Account | 默认 false | 是 |
| HCAPTCHA_SITE_KEY | 注册页面 hCaptcha 站点 Key | 空 | 是 |
| HCAPTCHA_SERVER_KEY | hCaptcha 服务端 Key | 空，敏感 | 是 |
| INSTALLATION_EVENTS_WEBHOOK_URL | 新 Account 等安装事件接收地址 | 空 | 是 |
| WEBHOOK_TIMEOUT | Webhook 等待响应的最长秒数 | 默认 5 秒 | 是 |
| DIRECT_UPLOADS_ENABLED | 是否允许向对象存储直传 | 默认 false | 是 |
| MAXIMUM_FILE_UPLOAD_SIZE | 附件大小上限 | 默认 40 MB | 是 |

## 10. 邮件与发送限制（4 项）

| 配置 Key | 功能 | 管理状态 |
|---|---|---|
| MAILER_INBOUND_EMAIL_DOMAIN | 生成 reply+id@domain 的入站邮件域名 | 可维护 |
| MAILER_SUPPORT_EMAIL | 实例支持邮箱 | 可维护 |
| ACCOUNT_EMAILS_LIMIT | 每个 Account 每日非渠道邮件上限，默认 100 | 可维护 |
| ACCOUNT_EMAILS_PLAN_LIMITS | 按 Cloud 计划设置每日邮件上限 | 系统管理 |

## 11. 社交和消息渠道配置（26 项）

| 渠道 | 配置 Key | 功能 | 管理状态 |
|---|---|---|---|
| Facebook | FB_APP_ID | 应用 ID | 可维护 |
| Facebook | FB_VERIFY_TOKEN | Webhook 验证 Token | 可维护，敏感 |
| Facebook | FB_APP_SECRET | 应用 Secret | 可维护，敏感 |
| Facebook | IG_VERIFY_TOKEN | 旧 Instagram Webhook 验证 Token | 可维护，敏感 |
| Facebook | FACEBOOK_API_VERSION | Graph API 版本，默认 v18.0 | 可维护 |
| Facebook | ENABLE_MESSENGER_CHANNEL_HUMAN_AGENT | 是否使用 Human Agent 扩展回复窗口 | 可维护 |
| WhatsApp | WHATSAPP_APP_ID | WhatsApp 应用 ID | 可维护 |
| WhatsApp | WHATSAPP_CONFIGURATION_ID | Embedded Signup Configuration ID | 可维护 |
| WhatsApp | WHATSAPP_APP_SECRET | Embedded Signup App Secret | 可维护，敏感 |
| WhatsApp | WHATSAPP_API_VERSION | WhatsApp API 版本，默认 v22.0 | 可维护 |
| Microsoft | AZURE_APP_ID | Microsoft 邮箱授权 App ID | 可维护 |
| Microsoft | AZURE_APP_SECRET | Microsoft 邮箱授权 Secret | 可维护，敏感 |
| Instagram | INSTAGRAM_APP_ID | Instagram 应用 ID | 可维护 |
| Instagram | INSTAGRAM_APP_SECRET | Instagram 应用 Secret | 可维护，敏感 |
| Instagram | INSTAGRAM_VERIFY_TOKEN | Instagram Webhook 验证 Token | 可维护，敏感 |
| Instagram | ENABLE_INSTAGRAM_CHANNEL_HUMAN_AGENT | 是否使用 Human Agent 扩展回复窗口 | 可维护 |
| Instagram | INSTAGRAM_API_VERSION | Instagram API 版本，默认 v22.0 | 系统管理 |
| TikTok | TIKTOK_API_VERSION | TikTok API 版本，默认 v1.3 | 可维护 |
| TikTok | TIKTOK_APP_ID | TikTok 应用 ID | 可维护 |
| TikTok | TIKTOK_APP_SECRET | TikTok 应用 Secret | 可维护，敏感 |
| Google | GOOGLE_OAUTH_CLIENT_ID | Google 邮箱和登录 Client ID | 可维护 |
| Google | GOOGLE_OAUTH_CLIENT_SECRET | Google OAuth Secret | 可维护，敏感 |
| Google | GOOGLE_OAUTH_REDIRECT_URI | Google OAuth 回调地址 | 可维护 |
| Google | ENABLE_GOOGLE_OAUTH_LOGIN | 是否在登录页显示 Google 登录 | 默认 true，可维护 |
| SAML | ENABLE_SAML_SSO_LOGIN | 是否显示 SAML SSO 登录 | 默认 true，可维护 |
| WhatsApp | INACTIVE_WHATSAPP_NUMBERS | 明确禁止使用的号码列表 | 系统管理 |

关闭 ENABLE_SAML_SSO_LOGIN 前，需要确认没有用户仍以 SAML 作为登录身份。Human Agent 开关只有在第三方应用取得相应权限时才有效。

## 12. Captain 与 AI 配置（13 项）

| 配置 Key | 功能 | 管理状态 |
|---|---|---|
| CAPTAIN_OPEN_AI_API_KEY | OpenAI 请求凭证 | 可维护，敏感 |
| CAPTAIN_OPEN_AI_MODEL | 安装范围旧默认模型，默认 gpt-4.1-mini | 可维护 |
| CAPTAIN_OPEN_AI_ENDPOINT | 可选的 OpenAI 兼容 Endpoint | 可维护 |
| CAPTAIN_EMBEDDING_MODEL | Embedding 模型，默认 text-embedding-3-small | 可维护 |
| CAPTAIN_FIRECRAWL_API_KEY | 网页知识抓取凭证 | 可维护，敏感 |
| CAPTAIN_CLOUD_PLAN_LIMITS | 各计划 AI 用量限制 | 系统管理 |
| CAPTAIN_DOCUMENT_AUTO_SYNC_INTERVALS | 各计划文档自动同步间隔，单位小时 | 可维护 |
| CAPTAIN_DOCUMENT_AUTO_SYNC_PER_ACCOUNT_BATCH_LIMIT | 单 Account 每轮同步文档上限，默认 50 | 可维护 |
| CAPTAIN_DOCUMENT_AUTO_SYNC_GLOBAL_BATCH_LIMIT | 全实例每轮同步文档上限，默认 1000 | 可维护 |
| CAPTAIN_TOPUP_OPTIONS | 各币种 AI 额度充值包 | 系统管理 |
| CONTEXT_DEV_API_KEY | 引导阶段品牌信息服务凭证 | 系统管理，敏感 |
| OTEL_PROVIDER | LLM 可观测 Provider 标识 | 可维护 |
| LANGFUSE_PUBLIC_KEY、LANGFUSE_SECRET_KEY、LANGFUSE_BASE_URL | Langfuse 认证和区域 Endpoint | 可维护，Key 敏感；默认美国区域地址 |

最后一行包含三个独立配置 Key，因此本组共 15 个配置项；其中 13 类用途覆盖 15 个实际 Key。模型到 Feature 的精确关系见 22 分册。

## 13. 第三方集成配置（13 项）

| 集成 | 配置 Key | 功能 | 管理状态 |
|---|---|---|---|
| Linear | LINEAR_CLIENT_ID、LINEAR_CLIENT_SECRET | OAuth 应用身份 | 可维护，Secret 敏感 |
| Notion | NOTION_CLIENT_ID、NOTION_CLIENT_SECRET、NOTION_VERSION | OAuth 应用和 API 版本，默认 2022-06-28 | 可维护，Secret 敏感 |
| Slack | SLACK_CLIENT_ID、SLACK_CLIENT_SECRET | OAuth 应用身份 | 可维护，Secret 敏感 |
| Shopify | SHOPIFY_CLIENT_ID、SHOPIFY_CLIENT_SECRET | Partner 应用身份 | 可维护，均按敏感项处理 |
| Cloudflare | CLOUDFLARE_API_KEY、CLOUDFLARE_ZONE_ID | 自定义域名和区域管理 | 可维护，API Key 敏感 |
| Open Graph | OG_IMAGE_CDN_URL、OG_IMAGE_CLIENT_REF | OG 图片地址和访问校验 | 可维护，Client Ref 敏感 |

## 14. 推送和移动端配置（3 项）

| 配置 Key | 功能 | 管理状态 |
|---|---|---|
| FIREBASE_PROJECT_ID | FCM 项目标识 | 可维护 |
| FIREBASE_CREDENTIALS | FCM v1 凭证内容 | 可维护，敏感 |
| WIDGET_TOKEN_EXPIRY | Widget Token 有效期 | 默认 180 天，可维护 |

Android 和 iOS 域名关联还需要独立实例参数，详见 28 分册。

## 15. Cloud、计费、分析和系统管理配置（23 项）

| 配置 Key | 功能 | 管理状态 |
|---|---|---|
| CHATWOOT_INBOX_TOKEN | Cloud 支持 Inbox Token | 可维护，敏感 |
| CHATWOOT_INBOX_HMAC_KEY | Cloud 支持 Widget 身份校验 Key | 可维护，敏感 |
| CHATWOOT_CLOUD_PLANS | Cloud 计划目录 | 系统管理 |
| ENABLE_MULTI_CURRENCY_BILLING | 新 Account 是否按本地币种计费 | 可维护 |
| MARKETING_CONVERSION_TRACKING_CONFIG | 注册和计划激活转化追踪配置 | 系统管理 |
| CHATWOOT_CLOUD_PLAN_FEATURES | 计划到 Feature 的映射 | 系统管理 |
| DEPLOYMENT_ENV | cloud 或 self-hosted 环境标识 | 系统管理 |
| CLOUD_ANALYTICS_TOKEN | Cloud 分析 Token | 系统管理，敏感 |
| CLEARBIT_API_KEY | 注册引导资料补全 | 系统管理，敏感 |
| DASHBOARD_SCRIPTS | Dashboard 尾部附加内容 | 系统管理，必须审查来源 |
| BLOCKED_EMAIL_DOMAINS | 禁止注册的邮箱域名规则 | 系统管理 |
| SKIP_INCOMING_BCC_PROCESSING | 跳过入站 BCC 处理的 Account ID | 系统管理 |
| INSTALLATION_PRICING_PLAN | 实例计划，默认 community | 系统管理 |
| INSTALLATION_PRICING_PLAN_QUANTITY | 已购买许可数量 | 系统管理 |
| CHATWOOT_SUPPORT_WEBSITE_TOKEN | 支持 Widget Token | 系统管理，敏感 |
| CHATWOOT_SUPPORT_SCRIPT_URL | 支持 Widget 脚本地址 | 系统管理 |
| CHATWOOT_SUPPORT_IDENTIFIER_HASH | 支持 Widget 身份校验值 | 系统管理，敏感 |
| ACCOUNT_SECURITY_NOTIFICATION_WEBHOOK_URL | Account 滥用或安全分析通知地址 | 系统管理 |
| CHATWOOT_INSTANCE_ADMIN_EMAIL | 合规通知接收邮箱 | 可维护 |
| API_CHANNEL_NAME | API Channel 自定义名称 | 系统管理 |
| API_CHANNEL_THUMBNAIL | API Channel 自定义缩略图 | 系统管理 |
| LOGOUT_REDIRECT_LINK | 退出登录后的目标地址，默认 /app/login | 可维护 |
| DISABLE_USER_PROFILE_UPDATE | 是否隐藏个人资料更新页 | 可维护 |

## 16. 102 项完整性核对

| 分组 | 实际配置项数 |
|---|---:|
| 品牌和公开页面 | 10 |
| 注册、Account、上传和 Webhook | 8 |
| 邮件与发送限制 | 4 |
| 社交和消息渠道 | 26 |
| Captain 与 AI | 15 |
| 第三方集成 | 13 |
| 推送和移动端 | 3 |
| Cloud、计费、分析和系统管理 | 23 |
| 合计 | 102 |

## 17. 配置生效设计

- Account Feature 变更后，只影响该 Account；安装配置变更可能影响全部 Account；
- 凭证类配置是否存在只说明具备发起请求的条件，不保证第三方授权、权限范围和 Webhook 都有效；
- API 版本、回调地址和验证 Token 必须与第三方平台控制台一致；
- Secret 更新应视为凭证轮换，更新后需要验证授权、回调和发送链路；
- 批量上限、超时、上传大小和 Token 有效期必须有明确单位；
- Cloud 计划、许可数量和系统管理配置不应从普通安装配置页面随意修改；
- 安装配置返回空值时表示未配置，不应自动使用其他渠道的凭证替代。

## 18. Feature 与配置联合验收

| 场景 | 必须同时成立 |
|---|---|
| 创建社交 Inbox | 渠道 Feature 开启、安装凭证完整、OAuth 回调匹配、第三方授权成功 |
| 使用 Captain | Captain Feature 开启、AI Feature 偏好开启、模型有效、API Key 可用、额度充足 |
| 使用 SAML | saml 开启、ENABLE_SAML_SSO_LOGIN 开启、Account SAML 元数据有效 |
| 使用语音渠道 | channel_voice 开启、企业授权有效、号码和回调配置完成 |
| 使用高级搜索 | advanced_search 开启、索引已准备、角色具备搜索范围 |
| 使用数据导入 | data_import 开启、Administrator 权限、文件大小和字段校验通过 |
| 使用第三方集成 | integrations 与单项 Feature 开启、安装凭证存在、Account 完成授权 |
| 接收移动推送 | 通知偏好允许、订阅有效、推送凭证或转发通道可用 |

## 19. 变更风险

Feature、凭证、回调、计划和权限是同一可用性链路中的不同环节。只改其中一项可能出现“页面可见但无法连接”“连接成功但收不到事件”“可以接收但无法回复”或“管理员可用但 Agent 不可用”。每次变更后应分别验证读取、写入、第三方回调、异步结果和权限边界。

Feature、套餐、角色、对象归属与配置冲突时的优先级见 [权限、Feature、套餐、配置与兼容优先级](35-权限Feature套餐配置与兼容优先级.md)；全部 68 个 Feature 和 102 个安装配置的通过条件见 [完整系统一致性对照验收规范](37-完整系统一致性对照验收规范.md)。
