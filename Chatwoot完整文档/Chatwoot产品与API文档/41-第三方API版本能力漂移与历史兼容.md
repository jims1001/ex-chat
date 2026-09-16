# 第三方 API 版本、能力漂移与历史兼容

> 适用版本：Chatwoot 4.16.0
> 本分册固定第三方连接在不同业务动作中实际使用的 API 版本边界，并说明 Provider 升级、权限变化、旧渠道、旧字段和历史数据如何保持可解释。

## 1. 一个 Provider 不一定只有一个 API 版本

同一 Provider 的不同动作可能使用不同版本。例如 WhatsApp 的授权、健康检查、文本发送、附件发送、媒体读取和模板管理并不全部使用同一 Graph API 版本。

因此，Provider 基线必须记录到“业务动作”级别，不能只记录：

- Provider 名称；
- 一个全局版本号；
- 一个 Access Token；
- 一个回调地址。

## 2. 当前第三方版本基线

| Provider | 业务动作 | 当前版本或端点族 | 是否可由安装配置改变 |
|---|---|---|---:|
| Facebook | 登录 SDK、Page 授权和主要 Graph 能力 | FACEBOOK_API_VERSION，默认 v18.0 | 是 |
| Instagram Direct | 消息发送、订阅和身份查询 | v22.0 | 部分动作使用 INSTAGRAM_API_VERSION，默认 v22.0 |
| Instagram via Facebook Page | 历史 Messenger 兼容发送 | Graph v11.0 | 否 |
| WhatsApp Cloud | Embedded Signup、Token、号码、Webhook、健康检查 | WHATSAPP_API_VERSION，默认 v22.0 | 是 |
| WhatsApp Calling | 通话相关 Graph 动作 | 配置版本，缺失时 v22.0 | 是 |
| WhatsApp Cloud | 文本、互动和模板消息发送 | Graph v13.0 路径 | 否 |
| WhatsApp Cloud | 附件消息发送 | Graph v24.0 路径 | 否 |
| WhatsApp Cloud | 媒体文件读取 | Graph v13.0 路径 | 否 |
| WhatsApp Cloud | Business Account 模板读取和校验 | Graph v14.0 路径 | 否 |
| WhatsApp CSAT Template | 创建、删除和状态查询 | Graph v14.0 | 否 |
| TikTok | Business Messaging 授权和消息 | TIKTOK_API_VERSION，默认 v1.3 | 是 |
| Shopify | OAuth 后的 Admin API Session | 2025-01 | 否 |
| Notion | 页面和授权后请求版本 | 2022-06-28 | 是，通过 NOTION_VERSION |
| Microsoft | OAuth 授权和 Token | OAuth 2.0 v2.0 | 否 |
| Microsoft | 当前用户资料 | Microsoft Graph v1.0 | 否 |
| Google | OAuth 登录和邮箱授权 | OAuth 2.0 端点族 | 无单一业务 API 版本字段 |
| LINE | Messaging API | Provider 当前稳定端点族 | 无安装版本字段 |
| Telegram | Bot API | Bot Token 对应端点族 | 无安装版本字段 |
| Twilio | Messaging、Delivery、Voice | Twilio 账号和回调合同 | 由账号能力和 Twilio 端决定 |
| Slack | OAuth、Events 和 Web API | Slack 当前 App 合同 | 无安装版本字段 |
| Linear | OAuth 和 GraphQL | Linear 当前 App 合同 | 无安装版本字段 |
| Stripe | Billing Webhook | Stripe Event 合同 | 当前安装未用统一版本字段控制全部事件 |
| Firecrawl | Crawl 状态回调 | Firecrawl 任务合同 | 由服务端和任务类型决定 |

表中的“不可由安装配置改变”表示当前产品对该动作使用固定兼容版本，不表示 Provider 永久支持该版本。

## 3. Facebook 版本和兼容边界

### 3.1 版本记录

需要记录：

- FACEBOOK_API_VERSION 实际值；
- FB_APP_ID；
- Page ID；
- 授权用户 ID；
- Page Access Token 指纹和过期时间；
- granted scopes；
- Webhook 订阅字段；
- 回调验证 Token 指纹；
- Human Agent 权限是否批准。

### 3.2 版本变化影响

| 能力 | 可能变化 |
|---|---|
| Page 授权 | scope 名称、审核要求和 Token 生命周期 |
| Webhook | 事件字段、签名、订阅字段和回调验证 |
| Messenger 发送 | 消息标签、24 小时窗口和错误码 |
| 附件 | 文件类型、大小、URL 有效期 |
| User Profile | 可返回字段和隐私限制 |

### 3.3 历史字段

IG_VERIFY_TOKEN 是历史 Instagram Webhook 验证项，仍可能存在于旧安装配置中。新 Instagram Direct 接入应优先使用 INSTAGRAM_VERIFY_TOKEN；不能删除历史值后假定旧 Facebook Page 型 Instagram Inbox 自动迁移完成。

## 4. Instagram 两类渠道必须区分

| 类型 | 外部身份 | 发送版本 | 授权和 Token | 不能混用的内容 |
|---|---|---|---|---|
| Instagram Direct | Instagram account / instagram_id | v22.0 | Instagram App Token | Instagram Direct 的订阅、身份和回调 |
| Instagram via Facebook Page | Facebook Page | Graph v11.0 兼容发送 | Page Access Token | Facebook Page 关系和 Messenger 兼容字段 |

迁移或重新授权时必须保留渠道类型。相同客户在两类渠道中的外部 ID 不保证相同，不能只按用户名合并 Contact。

Human Agent 设置也分为：

- ENABLE_INSTAGRAM_CHANNEL_HUMAN_AGENT；
- ENABLE_MESSENGER_CHANNEL_HUMAN_AGENT。

开启配置不代表第三方已经批准对应权限，发送失败仍需读取 Provider 错误。

## 5. WhatsApp 多版本动作矩阵

### 5.1 授权、健康和 Calling

WHATSAPP_API_VERSION 默认 v22.0，主要用于：

- Embedded Signup Token 交换；
- WABA Phone Numbers；
- Token Debug；
- Phone Number Register；
- WABA subscribed_apps；
- Health 查询；
- WhatsApp Calling 配置和通话动作。

### 5.2 消息和媒体

| 动作 | 版本 | 关键字段 |
|---|---|---|
| 文本消息 | v13.0 | messaging_product、to、text、context |
| input_select 互动消息 | v13.0 | interactive、action、button/list |
| Template Message | v13.0 | template、language、components |
| Attachment Message | v24.0 | image/audio/video/document、link、caption、filename |
| Media URL | v13.0 | media_id |

### 5.3 模板和 WABA

| 动作 | 版本 |
|---|---|
| message_templates 查询 | v14.0 |
| phone_numbers 与 WABA 关系校验 | v14.0 |
| CSAT Template 创建、删除、状态 | v14.0 |

### 5.4 一致性要求

- 修改 WHATSAPP_API_VERSION 不会自动改变所有 v13.0、v14.0 和 v24.0 动作；
- 文本发送成功不能证明附件发送成功；
- Health 成功不能证明 Template、Media 和 Calling 全部成功；
- 重新授权后至少验证文本、附件、Template、Media、入站、状态回调和 Calling；
- Provider 停止支持某个固定版本时，应逐动作更新基线和验收；
- 附件的 audio/ogg、audio/opus 和 voice 标记需按 WhatsApp 能力分别验证；
- Template 的批准状态、语言和参数与发送 API 版本是两个独立条件。

## 6. TikTok 版本边界

TIKTOK_API_VERSION 默认 v1.3，用于 Business API 授权和消息能力。

基线至少记录：

- App ID；
- API Version；
- Scope；
- Advertiser 或 Business 资源标识；
- Token 指纹和过期时间；
- Webhook 订阅；
- 会话窗口；
- 消息类型和附件支持范围。

TikTok 回调串行窗口和重试见 33、36 分册。升级版本时应重跑授权、入站、出站、状态、附件、重复事件和乱序事件。

## 7. Shopify 版本边界

Shopify Admin API Session 当前固定使用 2025-01。

需要对照：

- shop domain；
- OAuth scope；
- access token 指纹；
- API version；
- webhook topic 和订阅状态；
- customer、order 等对象字段；
- App 卸载回调；
- Contact 与 Shopify Customer 的映射。

Shopify 新版本可能移除字段或改变权限。Webhook Payload 的版本与主动查询的 Admin API 版本必须分别记录。

## 8. Notion 版本边界

NOTION_VERSION 默认 2022-06-28。请求时还需记录：

- Client ID；
- redirect URI；
- workspace_id；
- bot_id；
- owner 信息；
- duplicated_template_id，如 Provider 返回；
- access token 指纹；
- Notion-Version 实际 Header；
- 可访问 Page/Database 范围。

Notion 授权成功只表示连接建立，不代表所有 Page 均对 Integration 可见。

## 9. Microsoft 历史身份兼容

Microsoft 授权使用 OAuth 2.0 v2.0，用户资料读取使用 Microsoft Graph v1.0。

登录身份字段优先级：

1. preferred_username；
2. preferred_username 缺失时使用历史 upn；
3. 两者都缺失或与授权身份不一致时拒绝建立错误用户关系。

其他需要记录的字段：

- tenant；
- oid / subject；
- email / mail；
- displayName；
- scope；
- token expiry；
- refresh token 可用性。

不能只按 displayName 合并 User。

## 10. Google OAuth 兼容边界

Google 登录和 Google Email Inbox 可能共享 Client ID/Secret，但用途不同：

| 用途 | 主要结果 | 需单独验证 |
|---|---|---|
| Google 登录 | 建立 User 登录会话 | state、邮箱确认、注册策略、Account 关系 |
| Google Email Inbox | 建立邮箱渠道授权 | 邮箱地址、refresh token、收发权限、回调 |

登录成功不能证明 Email Inbox 可收发邮件；邮箱授权成功也不自动开启 Google 登录。

## 11. 无显式版本字段的 Provider

| Provider | 必须替代记录的版本信息 |
|---|---|
| LINE | Messaging API 文档日期、Webhook schema、Channel 类型和权限 |
| Telegram | Bot API 行为日期、Bot 权限、Webhook 状态和支持消息类型 |
| Twilio | Account/Number 能力、Messaging/Voice 产品、回调字段和签名方式 |
| Slack | App manifest、scope、Events subscriptions 和 Web API 字段 |
| Linear | OAuth scope、GraphQL schema 日期和 Team/Issue 字段 |
| Stripe | Event api_version、event type、Webhook endpoint 和签名密钥版本 |
| Firecrawl | Crawl request 类型、状态枚举、回调字段和任务创建时间 |

没有安装版本字段不等于没有版本变化风险。

## 12. Provider 能力漂移分类

| 漂移类型 | 示例 | 影响 |
|---|---|---|
| 字段新增 | 回调增加新对象 | 调用方应忽略未知字段但保留业务对象 |
| 字段删除 | 用户资料字段不再返回 | 旧默认和必填规则可能失败 |
| 字段改名 | username 改为新身份字段 | 需要明确别名优先级 |
| 枚举新增 | 新消息状态或模板状态 | 不能导致整个事件无法读取 |
| 枚举删除 | 旧状态停止返回 | 历史对象仍需可解释 |
| 权限收紧 | 新 scope 或 App Review | 授权成功但动作失败 |
| Token 生命周期变化 | 短期 Token 或 refresh 规则改变 | 渠道突然进入重新授权 |
| 速率限制变化 | 新窗口或配额 | 重试和批量同步失败 |
| 会话窗口变化 | 回复时间窗调整 | 历史可发送逻辑不再成立 |
| 文件限制变化 | 类型、大小、URL 要求变化 | 文本成功但附件失败 |
| 签名变化 | Header 或算法更新 | 合法回调被拒绝 |
| 事件重试变化 | Provider 增加重投 | 去重窗口不足 |

## 13. 版本升级前后对照包

每次改变 Provider 版本，至少保存：

| 类别 | 升级前 | 升级后 |
|---|---|---|
| 授权 | scope、Token 类型、资源列表 | 同项对照 |
| 入站消息 | 文本、附件、回复、互动 | 字段和业务对象对照 |
| 出站消息 | 请求字段、外部 ID、状态 | 成功和错误对照 |
| 状态回调 | sent、delivered、read、failed | 枚举和乱序对照 |
| Webhook | Header、签名、事件 ID | 校验和去重对照 |
| 模板 | 列表、批准状态、语言和参数 | 同项对照 |
| 文件 | 上传、下载、类型、大小和过期 | 同项对照 |
| 错误 | 认证、权限、限流、永久错误 | 错误码映射对照 |
| 重试 | Retry-After、重复事件、超时 | 副作用次数对照 |

## 14. Access Token 和 Scope 变化

### 14.1 Token 轮换

- 新 Token 生效前先验证目标资源；
- 不在普通响应中回显完整 Token；
- 记录旧 Token 失效时间；
- 回调签名密钥和发送 Token 分开轮换；
- 轮换期间是否允许双 Token 必须由 Provider 明确支持；
- 失败后不得自动回退到已经确认泄露或撤销的 Token。

### 14.2 Scope 变化

| 情况 | 结果 |
|---|---|
| 新增 scope 已批准 | 新能力可用，需逐动作验证 |
| 请求 scope 但未批准 | OAuth 可能成功，目标动作仍失败 |
| 用户撤销部分 scope | 只有相关动作失败，不应错误删除整个 Inbox |
| Provider 改名 scope | 旧授权可能需要重新确认 |
| 资源管理员变化 | Token 有效但不再能访问原 Page/Workspace |

## 15. 回调订阅漂移

版本或授权变化后必须重新确认：

- 回调 URL；
- challenge 验证；
- 签名 Header；
- 已订阅事件名称；
- Page、WABA、Workspace 或 Shop 是否仍绑定；
- 回调失败重试策略；
- 事件 ID；
- 回调 Payload 版本；
- 最近成功接收时间。

能够发送消息不等于能够接收回调，二者应独立监测。

## 16. 未知字段和未知事件

| 输入 | 处理原则 |
|---|---|
| 已知事件增加未知字段 | 忽略业务未知字段，但保留原始安全摘要和正常已知处理 |
| 新事件类型 | 按 Provider 约定确认，记录类型，不创建错误对象 |
| 已知状态增加新枚举 | 保留当前稳定终态，记录未知值并告警 |
| 必填定位字段缺失 | 不创建无归属 Contact、Conversation 或 Message |
| 外部 ID 类型改变 | 不强制转为数字，按字符串安全保存 |
| 时间单位改变 | 通过版本基线确认，不能自动猜秒或毫秒 |

## 17. 历史 Feature 兼容

| 历史 Feature | 当前原则 |
|---|---|
| message_reply_to | 已弃用，仅用于解释历史 Account 状态 |
| whatsapp_embedded_signup | 已弃用，新流程使用 whatsapp_embedded_signup_inbox_creation 和 whatsapp_reconfigure |
| reply_mailer_migration | 过渡能力，不能作为永久新功能依赖 |
| captain_integration | Captain v1，需与 captain_integration_v2 区分 |
| crm | 当前联系人能力基础开关 |
| crm_v2 | 新 CRM 体验，不能删除旧 Contact 数据 |

Feature Key 不得复用为新的无关含义。

## 18. 历史 API 输入兼容

### 18.1 Conversation 排序别名

| 历史输入 | 当前等价值 |
|---|---|
| latest | last_activity_at_desc |
| sort_on_created_at | created_at_asc |
| sort_on_priority | priority_desc |
| sort_on_waiting_since | waiting_since_asc |

### 18.2 PATCH 与 PUT

部分更新入口保留 PATCH/PUT 兼容。两种方法同时可用时应得到相同字段结果和副作用；不支持的方法应完整失败，不得只更新部分字段。

### 18.3 标识兼容

| 标识 | 历史风险 | 当前原则 |
|---|---|---|
| id | 不同对象可能重复 | 必须结合资源类型和 Account |
| display_id | 跨 Account 重复 | 只在路径 Account 范围解释 |
| identifier | 可能由旧外部系统提供 | 保持字符串，不自动转数字 |
| source_id | 不同 Inbox 可重复 | 结合 Inbox 使用 |
| provider_message_id | 不同 Provider 命名不同 | 结合 Provider 和 Channel 去重 |

## 19. 历史数据缺失字段

旧对象可能缺少：

- 新增 Feature 对应配置；
- available_name；
- Provider API Version；
- Token expiry；
- sender_type；
- sentiment；
- sla_events；
- identity_validation_enabled；
- message external_source_ids；
- Document sync error 字段；
- Login Session 设备信息。

兼容规则：

1. 缺失字段不阻止对象读取；
2. 可以按明确默认值在响应层表达；
3. 读取不得改变业务状态；
4. 下次更新只写合法目标字段；
5. 缺失安全字段时采用更严格策略；
6. 缺失 Provider 版本时按当前渠道类型的历史基线解释；
7. 不通过空文本伪造未知值；
8. 导出应区分空值和未知。

## 20. AI Provider 和模型漂移

即使模型 ID 不变，Provider 也可能更新模型行为。AI 基线必须记录：

- Provider；
- Model ID；
- Endpoint；
- Feature；
- system instruction 版本；
- temperature 等可见参数；
- 输入语言；
- Token 上限；
- 超时；
- 请求日期；
- 是否使用知识、Tool 和历史 Conversation。

AI 文本不要求逐字相同，但必须保持权限、结构、安全、引用、Tool 副作用和可用性一致。

## 21. Plugin 与 Integration 漂移

插件或第三方集成升级后，需要分别验证：

- 授权入口；
- Hook 字段；
- 支持事件；
- 外部对象 ID；
- 删除和重新连接；
- Webhook 签名；
- 失败重试；
- Conversation、Contact 和 Message 的关联；
- 旧连接是否继续可用；
- 新连接是否使用新字段。

不能因为集成目录仍显示同名 App，就假定授权范围和 Payload 未变化。

## 22. Provider 版本变更判定

| 变化 | 是否必须更新基线 | 是否必须回归 |
|---|---:|---:|
| 安装配置版本号改变 | 是 | 是 |
| 固定动作版本改变 | 是 | 是 |
| Provider 控制台自动升级 Webhook | 是 | 是 |
| Scope 或 App Review 变化 | 是 | 是 |
| Token 轮换但权限不变 | 是 | 授权、发送和回调至少重验 |
| 仅增加未知可选字段 | 是 | Payload 解析和去重重验 |
| Model ID 不变但发布日期变化 | 是 | AI 语义和 Tool 副作用重验 |

## 23. 必测版本与历史场景

| 编号 | 场景 | 通过要求 |
|---|---|---|
| VER-01 | 修改 WHATSAPP_API_VERSION | v22 相关动作变化，固定 v13/v14/v24 动作不被错误认为同步变化 |
| VER-02 | WhatsApp 文本成功、附件失败 | 分别显示结果，不把渠道整体标为成功 |
| VER-03 | Instagram Direct 和 Page 型渠道同账号 | 外部 ID、Token 和发送版本不混用 |
| VER-04 | Microsoft preferred_username 缺失 | 合法 upn 回退，身份仍需匹配 |
| VER-05 | Provider 回调新增字段 | 已知业务正常处理 |
| VER-06 | Provider 新增未知状态 | 不破坏现有终态，记录未知值 |
| VER-07 | OAuth scope 部分撤销 | 相关动作失败并提示重新授权 |
| VER-08 | Webhook 订阅在 Provider 端被移除 | 健康检查可发现，发送能力不掩盖入站故障 |
| VER-09 | 历史 Conversation 使用 latest | 与 last_activity_at_desc 一致 |
| VER-10 | 旧对象缺少 sender_type | 可读取，不伪造错误发送者 |
| VER-11 | 已弃用 Feature 仍存在于旧 Account | 可解释但不作为新功能开启依据 |
| VER-12 | Shopify API version 改变 | Customer、Order、Webhook 和卸载流程全部回归 |
| VER-13 | Notion-Version 改变 | 授权、Page 可见性和字段映射全部回归 |
| VER-14 | AI Model 同名行为变化 | 权限、引用和 Tool 副作用仍满足基线 |
| VER-15 | Token 和签名密钥同时轮换 | 两条链路分别验证，不发生错误回退 |

## 24. 完整性判定

本分册通过必须满足：

1. 每个有明确版本的 Provider 按业务动作记录版本；
2. WhatsApp v13、v14、v22、v24 的用途没有混用；
3. Instagram Direct 与 Facebook Page 兼容型渠道明确区分；
4. 无显式版本字段的 Provider 具备等价行为日期和合同基线；
5. Token、Scope、回调订阅和 API Version 独立验证；
6. 历史字段、Feature、排序别名和缺失字段均有确定兼容结果；
7. 未知字段、事件和枚举不会破坏已知业务；
8. Provider 或模型变化后更新基线并执行相关回归；
9. 旧对象继续可读且不会因读取产生副作用；
10. 第 23 节场景全部通过。
