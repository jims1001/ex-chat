# 集成、Webhook、Agent Bot 与插件 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

Chatwoot 通过 Integration App、Integration Hook、Dashboard App、Account Webhook、Agent Bot 和 Captain Custom Tool 连接外部系统。不同机制的权限、数据范围和可靠性不同，不能作为同一种插件协议使用。

## 2. 扩展机制选择

| 需求 | 推荐机制 | 主要数据方向 |
|---|---|---|
| 在工作台显示外部业务页面 | Dashboard App | 双向页面交互 |
| 接收 Chatwoot 业务事件 | Account Webhook | Chatwoot 到外部系统 |
| 外部机器人参与会话 | Agent Bot | 双向 |
| AI 调用外部业务接口 | Captain Custom Tool | AI 到外部接口 |
| Slack、CRM、工单等深度连接 | Integration App + Hook | 双向或事件驱动 |
| 自定义客户聊天界面 | Public Client API | 双向 |
| SaaS 批量管理 Chatwoot | Platform API | 控制平面到 Chatwoot |

## 3. Integration App API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/integrations/apps | 可用集成目录 |
| GET | /api/v1/accounts/{account_id}/integrations/apps/{id} | 集成详情和连接状态 |

### 3.1 App 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | string | 稳定集成标识 |
| name | string | 展示名称 |
| description | string | 功能说明 |
| logo | string | 图标地址 |
| enabled | boolean | 当前安装和 Account 是否可用 |
| action | string 或 null | 授权或配置入口 |
| hooks | array | 当前 Account 已建立的连接 |
| allow_multiple_hooks | boolean | 是否允许多个连接 |

常见 App ID：slack、dialogflow、google_translate、linear、notion、dyte、shopify、leadsquared、openai、webhook、dashboard_apps。

## 4. Integration Hook API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| POST | /api/v1/accounts/{account_id}/integrations/hooks | 创建连接 | Administrator |
| PATCH | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id} | 更新状态或设置 | Administrator |
| DELETE | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id} | 删除连接 | Administrator |
| POST | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id}/process_event | 执行集成支持的显式事件 | Account 成员，按集成限制 |

### 4.1 Hook 字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| id | integer | 否 | Hook ID |
| app_id | string | 创建时 | Integration App ID |
| account_id | integer | 否 | Account ID |
| inbox_id | integer 或 null | 创建时 | Inbox 范围；null 表示 Account 范围 |
| status | enum | 是 | enabled、disabled、reauthorization_required 等 |
| settings | object | 是 | 集成专属配置 |
| reference_id | string 或 null | 受限 | 外部 Workspace、Channel 或 Shop 标识 |
| created_at | time | 否 | 创建时间 |

Secret、API Key 和 Access Token 不应出现在普通响应中。更新 settings 时应保留未回显的敏感值。

## 5. Slack API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/integrations/slack | 使用授权结果建立连接 |
| GET | /api/v1/accounts/{account_id}/integrations/slack/list_all_channels | 获取可选频道 |
| PATCH | /api/v1/accounts/{account_id}/integrations/slack | 选择频道并启用 |
| DELETE | /api/v1/accounts/{account_id}/integrations/slack | 断开连接 |

Slack 还使用 POST /api/v1/integrations/webhooks 接收第三方互动事件。该路径接收 Slack 事件原始字段，属于 Provider 回调入口，不作为普通 Account 资源接口使用。

### 5.1 Slack 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| code | string | OAuth 授权 code |
| reference_id | string | Slack Channel ID |
| channel_name | string | Channel 名称，通常由响应提供 |
| status | enum | 连接状态 |

Slack 将 Chatwoot Conversation 映射到 Slack Thread。断开或 Token 失效后不应继续发送消息。

## 6. Meeting API（Dyte/RealtimeKit）

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/integrations/dyte/create_a_meeting | 为会话创建会议 |
| POST | /api/v1/accounts/{account_id}/integrations/dyte/add_participant_to_meeting | 添加 Account 成员 |
| POST | /api/v1/widget/integrations/dyte/add_participant_to_meeting | 添加客户参与者 |

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| conversation_id | integer | 创建会议是 | 会话显示 ID |
| message_id | integer | 添加参与者是 | 会议消息 ID |
| meeting_id | string | 响应 | 外部会议 ID |
| participant_id | string | 响应 | 参与者 ID |
| auth_token | string | 响应 | 参与会议的短期令牌 |

会议消息的 content_type 为 integrations。只有该会话的授权成员或客户可以取得参与令牌。

## 7. Shopify API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/integrations/shopify/auth | 获取授权地址 |
| GET | /api/v1/accounts/{account_id}/integrations/shopify/orders | 按 Contact 查询订单 |
| DELETE | /api/v1/accounts/{account_id}/integrations/shopify | 断开连接 |

### 7.1 Shopify 字段

| 字段 | 位置 | 类型 | 说明 |
|---|---|---|---|
| shop_domain | Body | string | Shopify 商店域名 |
| redirect_url | Response | string | OAuth 授权地址 |
| contact_id | Query | integer | Chatwoot Contact ID |
| orders | Response | array | 订单摘要 |

### 7.2 Order 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer 或 string | Shopify Order ID |
| email | string 或 null | 订单邮箱 |
| created_at | time | 创建时间 |
| total_price | string 或 number | 总金额 |
| currency | string | 币种 |
| fulfillment_status | string 或 null | 履约状态 |
| financial_status | string 或 null | 支付状态 |
| admin_url | string | Shopify 管理地址 |

当前能力以只读订单查询为主。Contact 没有邮箱或电话时无法匹配 Shopify Customer。

## 8. Linear API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/integrations/linear/teams | Linear Team 列表 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/team_entities | Team 状态、项目、成员等 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/search_issue | 搜索 Issue |
| GET | /api/v1/accounts/{account_id}/integrations/linear/linked_issues | 会话已关联 Issue |
| POST | /api/v1/accounts/{account_id}/integrations/linear/create_issue | 创建 Issue |
| POST | /api/v1/accounts/{account_id}/integrations/linear/link_issue | 关联已有 Issue |
| POST | /api/v1/accounts/{account_id}/integrations/linear/unlink_issue | 解除关联 |
| DELETE | /api/v1/accounts/{account_id}/integrations/linear/destroy | 断开连接 |

### 8.1 Linear 请求字段

| 字段 | 类型 | 使用场景 |
|---|---|---|
| team_id | string | Team 实体、创建 Issue |
| project_id | string 或 null | 创建 Issue |
| conversation_id | integer | 创建、关联、解除、查询已关联 |
| issue_id | string | 关联或解除 |
| link_id | string | 解除关联 |
| title | string | 创建 Issue 或关联标题 |
| description | string 或 null | 创建 Issue |
| assignee_id | string 或 null | 创建 Issue |
| priority | integer 或 string | 创建 Issue |
| state_id | string 或 null | 创建 Issue |
| label_ids | string array | 创建 Issue |
| q | string | 搜索关键词 |

创建、关联和解除成功后，会话中会出现相应活动记录。

## 9. Notion API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/notion/authorization | 获取 Notion 授权地址 |
| DELETE | /api/v1/accounts/{account_id}/integrations/notion/destroy | 断开 Notion |

### 9.1 Authorization 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| success | boolean | 是否成功生成授权地址 |
| url | string 或 null | Notion OAuth 地址 |

连接成功后，Hook 可保存 workspace_name、workspace_id、workspace_icon、bot_id 和 owner 等信息。当前版本只确认连接与断开能力，不把 Notion 页面读取或 Captain 知识同步列为可用功能。

## 10. Dashboard App API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/dashboard_apps | App 列表 |
| POST | /api/v1/accounts/{account_id}/dashboard_apps | 创建 App |
| GET | /api/v1/accounts/{account_id}/dashboard_apps/{id} | App 详情 |
| PATCH | /api/v1/accounts/{account_id}/dashboard_apps/{id} | 更新 App |
| DELETE | /api/v1/accounts/{account_id}/dashboard_apps/{id} | 删除 App |

### 10.1 Dashboard App 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | App ID |
| title | string | 名称 |
| content | string | App 页面地址 |
| account_id | integer | 所属 Account |
| enabled | boolean | 是否启用，按响应提供 |

App 页面可能接收 Conversation、Contact 和当前 Account 的有限上下文。页面地址应使用 HTTPS，并限制允许来源。

## 11. Account Webhook API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/webhooks | Webhook 列表 | Administrator |
| POST | /api/v1/accounts/{account_id}/webhooks | 创建 Webhook | Administrator |
| PATCH | /api/v1/accounts/{account_id}/webhooks/{id} | 更新 Webhook | Administrator |
| DELETE | /api/v1/accounts/{account_id}/webhooks/{id} | 删除 Webhook | Administrator |

### 11.1 Webhook 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Webhook ID |
| name | string 或 null | 名称 |
| url | string | 接收地址，生产建议 HTTPS |
| subscriptions | string array | 订阅事件 |
| secret | string 或 null | HMAC Secret，敏感值不应重复回显 |
| account_id | integer | Account ID |

### 11.2 常见订阅事件

| 事件 | 含义 |
|---|---|
| conversation_created | 创建会话 |
| conversation_status_changed | 会话状态变化 |
| conversation_updated | 会话更新 |
| contact_created | 创建联系人 |
| contact_updated | 联系人更新 |
| message_created | 创建消息 |
| message_updated | 消息更新 |
| webwidget_triggered | Widget 触发活动 |
| conversation_typing_on | 开始输入 |
| conversation_typing_off | 停止输入 |
| inbox_created | 创建 Inbox，是否发送取决于安装设置 |
| inbox_updated | 更新 Inbox，是否发送取决于安装设置 |

### 11.3 Webhook 请求头

| Header | 说明 |
|---|---|
| X-Chatwoot-Delivery | 单次投递 ID，用于去重 |
| X-Chatwoot-Timestamp | Unix 时间戳 |
| X-Chatwoot-Signature | 使用 Secret 对时间戳和原始请求内容生成的 HMAC |

接收方应先检查时间窗口和签名，再解析字段，并以 Delivery ID 防止重复处理。

## 12. Webhook Payload 公共字段

| 字段 | 类型 | 说明 |
|---|---|---|
| event | string | 事件名称 |
| id | integer | 事件主对象 ID，含义按事件类型 |
| account | object | Account 摘要 |
| inbox | object 或 null | Inbox 摘要 |
| conversation | object 或 null | Conversation |
| message | object 或 null | Message |
| contact | object 或 null | Contact |
| sender | object 或 null | 触发者 |

不同事件只提供相关对象。接收方应允许新增字段，并验证所有对象属于预期 Account。

## 13. Agent Bot API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/agent_bots | Bot 列表 |
| POST | /api/v1/accounts/{account_id}/agent_bots | 创建 Bot |
| GET | /api/v1/accounts/{account_id}/agent_bots/{id} | Bot 详情 |
| PATCH | /api/v1/accounts/{account_id}/agent_bots/{id} | 更新 Bot |
| DELETE | /api/v1/accounts/{account_id}/agent_bots/{id} | 删除 Bot |
| DELETE | /api/v1/accounts/{account_id}/agent_bots/{id}/avatar | 删除头像 |
| POST | /api/v1/accounts/{account_id}/agent_bots/{id}/reset_access_token | 重置 API Token |
| POST | /api/v1/accounts/{account_id}/agent_bots/{id}/reset_secret | 重置 Webhook Secret |

### 13.1 Agent Bot 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Bot ID |
| name | string | 名称 |
| description | string 或 null | 说明 |
| outgoing_url | string | Bot 接收事件地址 |
| account_id | integer | Account ID |
| avatar_url | string 或 null | 头像 |
| access_token | string | 创建或重置时返回，需安全保存 |
| secret | string | 创建或重置时返回，需安全保存 |

Bot 通过 Inbox 绑定参与会话。无法继续处理时应明确转人工，不能持续占用 pending 会话。

## 14. 接入规则

- Integration App 目录不代表连接已经建立；
- Hook enabled 才表示连接可以参与业务；
- Token、Secret 和 API Key 不应出现在普通日志或共享页面；
- 外部事件必须验证来源、时间窗口和重复 ID；
- 外部写操作应使用稳定幂等标识；
- 删除连接前应确认 Provider 撤销、历史映射和后续事件影响；
- Dashboard App、Webhook、Agent Bot 和 Custom Tool 的数据范围不同，应按最小需要启用。

12 个可订阅 Webhook、完整 Payload、签名头、投递规则和实时事件详见：[Webhook 与实时事件字段字典](19-Webhook与实时事件字段字典.md)。
