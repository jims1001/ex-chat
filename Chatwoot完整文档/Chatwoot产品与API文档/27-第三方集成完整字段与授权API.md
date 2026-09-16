# Chatwoot 第三方集成完整字段与授权 API

> 导航：[集成、Webhook、Agent Bot 与插件 API](10-集成Webhook-AgentBot与插件API.md)｜[渠道状态、错误、重新授权与回调字典](26-渠道状态错误重新授权与回调字典.md)

## 1. 集成模型

第三方集成分为 App 和 Hook 两层：

| 对象 | 作用 |
|---|---|
| Integration App | 描述一种可安装能力、所需参数、授权动作和可见条件 |
| Integration Hook | 表示该 App 在某个 Account 或 Inbox 中的一次实际连接 |

App 目录是能力清单，Hook 才保存 Account 的授权结果和设置。一个 App 可以允许多个 Hook，也可以限制为 Account 或 Inbox 级单连接。

## 2. 集成目录 API

| 方法 | 路径 | 功能 | 权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/integrations/apps | 返回当前 Account 可用的集成目录 | Account 成员可读，管理字段仅 Administrator 可见 |
| GET | /api/v1/accounts/{account_id}/integrations/apps/{app_id} | 获取指定集成及当前连接 | Account 成员可读，管理字段仅 Administrator 可见 |

### 2.1 App 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | App Key |
| name | string | 展示名称 |
| description | string | 完整功能说明 |
| short_description | string | 列表摘要 |
| enabled | boolean | 当前 Account 是否已经存在该 App 的连接；Webhook 和 Dashboard App 则表示是否已有对应对象 |
| action | string | 授权或安装动作标识 |
| button | string | 安装按钮文案 |
| hooks | array | 当前 Account 已建立的 Hook |

目录只返回当前 Account 满足 Feature 和安装凭证前置条件的 App。Administrator 响应还会附带 action、button、hook_type、allow_multiple_hooks、设置字段定义和可见字段定义；普通成员不获得这些管理信息。enabled 为 false 表示尚未连接，不等于该 App 不可安装。

## 3. 当前 11 类集成

| App Key | 功能 | Hook 范围 | 主要前置条件 |
|---|---|---|---|
| webhook | 把会话事件发送到外部 URL | Account 或 Inbox | api_and_webhooks、HTTPS URL |
| dashboard_apps | 在会话侧栏嵌入业务页面 | Account | Dashboard App 配置 |
| openai | 旧式标签建议入口 | Account | API Key；新 AI 能力优先使用 Captain |
| linear | 创建并关联 Linear Issue | Account | linear_integration、OAuth Client |
| notion | 连接 Notion Workspace | Account | notion_integration、OAuth Client |
| slack | 把指定 Inbox 的会话发送到 Slack 并从 Slack 回复 | Account 单连接，关联一个 Inbox | Slack OAuth Client |
| dialogflow | 使用 Dialogflow 处理客户消息 | Inbox | Google Cloud 项目与凭证 |
| google_translate | 翻译会话消息 | Account | Google Cloud 项目与凭证 |
| dyte | 创建 Cloudflare RealtimeKit 会议 | Account | Account ID、App ID、API Token |
| shopify | 在联系人侧查看 Shopify 订单 | Account | shopify_integration、Partner App |
| leadsquared | 同步 CRM 活动和客户信息 | Account | crm_integration、LeadSquared 凭证 |

Dashboard App 的实体和接口详见 10 分册；Webhook 订阅和 Payload 详见 19 分册。

## 4. Integration Hook API

| 方法 | 路径 | 功能 | 权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id} | 读取连接 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/integrations/hooks | 创建连接 | Administrator |
| PATCH | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id} | 更新状态或设置 | Administrator |
| DELETE | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id} | 删除连接 | Administrator |
| POST | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id}/process_event | 请求集成处理事件 | Administrator 或受支持的业务入口 |

### 4.1 创建和更新字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| app_id | string | 创建时是 | App Key |
| inbox_id | integer | Inbox Hook 是 | 连接的 Inbox ID |
| status | enum | 否 | enabled 或 disabled |
| settings | object | 视 App 而定 | 该 App 的授权结果或配置 |

### 4.2 Hook 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Hook ID |
| app_id | string | App Key |
| status | boolean | 是否启用 |
| hook_type | enum | account 或 inbox |
| account_id | integer | 所属 Account |
| inbox | object、null | id 和 name；Account Hook 可为空 |
| settings | object | 只返回该 App 允许展示的设置 |
| reference_id | string、null | 第三方资源标识 |

access_token、api_key、credentials、secret_key 和 api_token 不应出现在 Hook 响应。

## 5. Hook 生命周期设计

| 状态 | 说明 |
|---|---|
| 未连接 | 目录可见，但没有 Hook |
| 已授权 | 已取得第三方授权结果并创建 Hook |
| enabled | Hook 参与事件处理 |
| disabled | 保留设置但暂停处理 |
| expired | Token 或第三方授权失效，需要重新授权 |
| deleted | 移除 Hook，并按集成要求撤销第三方订阅 |

disabled 适合临时停用；删除适合彻底断开。删除后是否同时撤销第三方授权取决于 Provider，不能假设仅删除 Chatwoot 连接就会删除第三方应用授权。

## 6. OpenAI 旧式集成

### 6.1 设置字段

| 字段 | 必填 | 是否可回显 | 说明 |
|---|---:|---:|---|
| api_key | 是 | 否 | OpenAI API Key |
| label_suggestion | 否 | 是 | 是否启用标签建议 |

此集成主要保留旧式标签建议配置。当前版本的改写、回复建议、摘要、Assistant、Copilot、FAQ、音频转写和语义检索统一由 Captain Preferences 管理，模型和 Provider 见 22 分册。

同一 Account 不应同时把旧式 label_suggestion 和 Captain 标签建议当作两个独立结果源，否则可能出现重复建议和成本口径不一致。

## 7. Dialogflow

### 7.1 Hook 范围

Dialogflow 是 Inbox 级集成，可为不同 Inbox 使用不同项目、区域和语言。

### 7.2 设置字段

| 字段 | 必填 | 默认值 | 说明 |
|---|---:|---|---|
| project_id | 是 | 无 | Google Cloud 项目 ID |
| credentials | 是 | 无 | 服务账号凭证，敏感，不回显 |
| region | 否 | global | Dialogflow 区域 |
| language_code | 否 | en-US | 识别和响应语言；auto 表示自动判断 |

### 7.3 language_code 可选值

en-US、en-GB、es-ES、es-419、fr-FR、de-DE、pt-BR、pt-PT、it-IT、ja-JP、ko-KR、zh-CN、zh-TW、hi-IN、ar、ru-RU、nl-NL、pl-PL、tr-TR、th-TH、vi-VN、id-ID 和 auto。

### 7.4 设计规则

- 客户消息先归属到正确 Conversation，再交由 Inbox 的 Dialogflow Hook；
- private 消息和系统活动不作为客户意图输入；
- Dialogflow 无法回答时应转人工或保持会话可处理；
- 凭证失效时停用 Hook，不能持续产生空回复；
- region 必须与第三方项目实际区域一致。

## 8. Google Translate

### 8.1 设置字段

| 字段 | 必填 | 是否可回显 | 说明 |
|---|---:|---:|---|
| project_id | 是 | 是 | Google Cloud 项目 ID |
| credentials | 是 | 否 | 服务账号凭证 |

该 Hook 为 Account 级连接，供消息翻译入口使用。翻译接口返回建议文本，不修改原始消息；用户应能区分原文和译文。

## 9. Slack

### 9.1 接口

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/integrations/slack | 使用 OAuth code 创建 Inbox 连接 |
| PATCH | /api/v1/accounts/{account_id}/integrations/slack | 更新已选择频道 |
| GET | /api/v1/accounts/{account_id}/integrations/slack/list_all_channels | 列出可选择频道 |
| DELETE | /api/v1/accounts/{account_id}/integrations/slack | 移除 Slack 连接 |

### 9.2 创建字段

| 字段 | 必填 | 说明 |
|---|---:|---|
| code | 是 | Slack OAuth 一次性 code |
| inbox_id | 是 | 要连接的 Chatwoot Inbox |

### 9.3 更新和频道字段

| 字段 | 说明 |
|---|---|
| reference_id | 选择的 Slack channel_id |
| channel_name | Hook 响应中可见的频道名称 |
| channels[].id | Slack Channel ID |
| channels[].name | Slack Channel 名称 |

Slack 在 Account 中只允许一个连接，创建时通过 inbox_id 关联目标 Inbox。消息同步必须保留 Conversation 与 Thread 的稳定映射；重复事件不能重复创建 Chatwoot 消息。删除连接后应停止从 Slack 回复客户。

## 10. Linear

### 10.1 授权与目录

| 方法 | 路径 | 功能 |
|---|---|---|
| 外部跳转 | https://linear.app/oauth/authorize | 使用目录 action 发起 Linear OAuth |
| GET | /linear/callback | 保存授权结果 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/teams | 获取团队 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/team_entities | 获取团队项目、成员、状态和标签 |

team_entities 使用 team_id 作为 Query 字段。

### 10.2 创建 Issue

| 方法 | 路径 |
|---|---|
| POST | /api/v1/accounts/{account_id}/integrations/linear/create_issue |

| Body 字段 | 必填 | 说明 |
|---|---:|---|
| team_id | 是 | Linear Team ID |
| conversation_id | 是 | Chatwoot Conversation ID |
| title | 是 | Issue 标题 |
| description | 否 | Issue 描述，可包含会话摘要 |
| project_id | 否 | Linear Project ID |
| assignee_id | 否 | Linear Assignee ID |
| priority | 否 | Linear Priority |
| state_id | 否 | Linear Workflow State ID |
| label_ids | 否 | Linear Label ID 数组 |

### 10.3 Issue 关联

| 方法 | 路径 | 主要字段 |
|---|---|---|
| POST | /integrations/linear/link_issue | conversation_id、issue_id、title |
| POST | /integrations/linear/unlink_issue | conversation_id、link_id、issue_id |
| GET | /integrations/linear/linked_issues | conversation_id |
| GET | /integrations/linear/search_issue | q |

创建 Issue 和关联已有 Issue 是两个动作。创建成功后应保存关联；关联失败时不能再次盲目创建 Issue。unlink 只解除 Chatwoot 会话关系，不删除 Linear Issue。

## 11. Shopify

### 11.1 授权接口

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/integrations/shopify/auth | 生成商店授权地址 |
| GET | /shopify/callback | 交换授权并创建 Hook |
| DELETE | /api/v1/accounts/{account_id}/integrations/shopify | 删除连接 |

| 字段 | 必填 | 说明 |
|---|---:|---|
| shop_domain | 是 | Shopify 商店域名，应规范化并限制为合法商店域 |

成功返回 redirect_url。回调成功后 Hook 的 reference_id 为商店标识，settings 可以包含授权 scope。

### 11.2 联系人订单

| 方法 | 路径 | Query |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/integrations/shopify/orders | contact_id |

| 订单字段 | 说明 |
|---|---|
| id | Shopify Order ID |
| email | 下单邮箱 |
| created_at | 下单时间 |
| total_price | 订单总额 |
| currency | 币种 |
| fulfillment_status | 履约状态 |
| financial_status | 支付状态 |
| admin_url | Shopify 后台订单地址 |

订单匹配主要依赖 Contact 的邮箱或已知客户身份。匹配不到订单不应自动创建联系人或修改客户邮箱。

## 12. Notion

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/notion/authorization | 返回授权 URL |
| GET | /notion/callback | 保存 Workspace 授权 |
| DELETE | /api/v1/accounts/{account_id}/integrations/notion | 断开连接 |

### 12.1 Hook 可见字段

| 字段 | 说明 |
|---|---|
| token_type | 授权 Token 类型 |
| workspace_name | Workspace 名称 |
| workspace_id | Workspace ID |
| workspace_icon | Workspace 图标 |
| bot_id | Notion Bot ID |
| owner | 授权所有者摘要 |

访问 Token 不回显。当前 Account 入口主要覆盖连接管理，不应据此推断存在任意 Notion 页面读写 API。

## 13. Cloudflare RealtimeKit 会议

集成目录中的 dyte 表示会议能力，当前产品名称可显示为 Cloudflare RealtimeKit。

### 13.1 设置字段

| 字段 | 必填 | 是否可回显 | 说明 |
|---|---:|---:|---|
| account_id | 是 | 是 | Provider Account ID |
| app_id | 是 | 是 | Provider App ID |
| api_token | 是 | 否 | Provider API Token |

### 13.2 业务动作

| 动作 | 主要字段 | 结果 |
|---|---|---|
| create_a_meeting | conversation_id | 创建会议并保存到会话上下文 |
| add_participant_to_meeting | message_id | 为会议消息创建参与者访问数据 |
| Widget 加入会议 | 会议消息和客户身份 | 返回客户参与会议所需信息 |

会议创建成功后应通过会话消息共享，不应把 Provider API Token 发送给客户。参与者 Token 必须只对应当前会议和当前身份。

## 14. LeadSquared CRM

### 14.1 设置字段

| 字段 | 必填 | 说明 |
|---|---:|---|
| access_key | 是 | LeadSquared Access Key，敏感 |
| secret_key | 是 | LeadSquared Secret Key，敏感 |
| endpoint_url | 否 | 自定义 API Endpoint |
| app_url | 否 | CRM 页面地址 |
| timezone | 否 | CRM 时间解释时区 |
| enable_conversation_activity | 否 | 是否写入会话活动 |
| enable_transcript_activity | 否 | 是否写入会话记录活动 |
| conversation_activity_score | 否 | 会话创建活动分值，默认 0 |
| transcript_activity_score | 否 | 会话记录活动分值，默认 0 |
| conversation_activity_code | 否 | 会话活动类型标识 |
| transcript_activity_code | 否 | 会话记录活动类型标识 |

CRM 同步必须明确客户唯一键、活动幂等键和时区。Conversation 关闭或 Transcript 发送可能重复触发，外部 CRM 需要按稳定标识去重。

## 15. Webhook 集成

Webhook 可以按 Account 或 Inbox 接收 Chatwoot 事件。主要字段如下：

| 字段 | 说明 |
|---|---|
| name | Webhook 名称 |
| url | HTTPS 接收地址 |
| inbox_id | 可选；限制到某个 Inbox |
| subscriptions | 订阅事件数组 |

12 个订阅事件、Payload、签名和重试规则见 19 分册。Integration Hook 中的 webhook 与 /webhooks 管理入口表达同类外发能力，业务方案应选择一个清晰的管理入口，避免同一 URL 重复订阅同一事件。

## 16. Dashboard App

Dashboard App 在会话侧边栏嵌入外部页面，常用字段为 title 和 content，其中 content.type 表示内容类型，content.url 表示加载地址。

嵌入页面只能获得明确传递的会话上下文。它不自动继承 Super Admin、Account Token 或客户身份凭证。外部页面需要自己的登录、域名信任和权限判断。

## 17. 可用性判断

| 集成 | enabled 的关键条件 |
|---|---|
| Slack | SLACK_CLIENT_SECRET 等 OAuth 配置存在 |
| Linear | linear_integration 开启且 LINEAR_CLIENT_ID 存在 |
| Shopify | shopify_integration 开启且 SHOPIFY_CLIENT_ID 存在 |
| Notion | notion_integration 开启且 NOTION_CLIENT_ID 存在 |
| LeadSquared | crm_integration 开启 |
| OpenAI | integrations 可用并能提交 API Key；新版能力还看 Captain |
| Dialogflow、Translate、RealtimeKit | integrations 可用，Hook 字段校验通过 |

目录 enabled 只表示允许尝试安装；第三方 Token、Scope、资源状态和网络仍可能导致 Hook 不可用。

## 18. 授权安全规则

- OAuth code 只使用一次，不能保存后重复交换；
- state 必须绑定 Account、当前 User、Provider 和过期时间；
- Secret、API Key、Credentials 和 Access Token 只写入 settings，不在响应中回显；
- redirect_url 只允许已配置的回调地址和可信域名；
- shop_domain、endpoint_url 和外部 URL 必须校验协议和主机；
- 删除 Hook 时清理 Chatwoot 侧映射，并按 Provider 能力撤销订阅；
- 第三方资源 ID 只能在当前 Hook 授权范围内使用；
- Agent 可以使用已授权的会话动作，不等于可以读取或更换集成凭证。

## 19. 错误模型

| 错误 | 含义 | 处理 |
|---|---|---|
| app_not_enabled | App 当前不可安装 | 检查 Feature、计划和安装凭证 |
| hook_not_found | Hook 不属于当前 Account | 检查 account_id 和 hook_id |
| invalid_settings | 必填设置缺失或格式错误 | 按 App 字段字典修正 |
| authorization_failed | OAuth 交换失败 | 重新发起授权，不能复用 code |
| insufficient_scope | 第三方权限不足 | 补充 Scope 后重新授权 |
| resource_not_found | Team、Channel、Shop 或 Workspace 不存在 | 刷新第三方资源目录 |
| rate_limited | 第三方限流 | 按第三方建议等待，不进行高频重试 |
| token_expired | Token 失效 | 重新授权或刷新 Token |
| duplicate_hook | 违反单连接限制 | 更新已有 Hook，不创建重复连接 |

## 20. 验收矩阵

| 场景 | 预期结果 |
|---|---|
| Agent 读取集成目录 | 可见 App 基本信息，不可见 Secret 参数值 |
| Agent 尝试更新 Hook | 被权限拒绝 |
| 创建 Inbox Hook 未传 inbox_id | 返回字段校验错误 |
| OAuth state 过期 | 不创建 Hook，要求重新授权 |
| 更新 status=false | 停止处理事件但保留连接设置 |
| 删除 Hook | 当前 Account 不再使用该连接 |
| Slack 频道刷新 | 只返回当前 OAuth 范围可访问频道 |
| Linear 创建 Issue 后关联 | 会话可查询到 linked issue，不重复创建 |
| Shopify 订单查询 | 只按当前 Account Contact 查询，不跨 Account 返回 |
| Notion Hook 响应 | 返回 Workspace 摘要，不返回 Access Token |
| Dialogflow 凭证失效 | 不生成空客户回复，允许转人工 |
| RealtimeKit 加入会议 | 客户只得到当前会议的参与者信息 |
