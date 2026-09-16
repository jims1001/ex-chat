# Public、Platform 与实时事件 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. Public Client API

Public Client API 允许自定义客户聊天界面访问某个 API Inbox。客户只能访问自己的 Contact、Conversation 和 Message。

## 2. Public Inbox 与 Contact API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /public/api/v1/inboxes/{inbox_identifier} | 获取 Inbox 公共信息 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts | 创建客户身份 |
| GET | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier} | 客户详情 |
| PATCH | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier} | 更新客户信息 |

### 2.1 Public Contact 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| identifier | string | 外部客户标识 |
| identifier_hash | string 或 null | 强制 HMAC 时的签名 |
| name | string 或 null | 姓名 |
| email | string 或 null | 邮箱 |
| phone_number | string 或 null | 电话 |
| custom_attributes | object | 允许提交的客户字段 |
| pubsub_token | string | 实时订阅令牌，按响应提供 |

## 3. Public Conversation API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations | 会话列表 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations | 创建会话 |
| GET | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id} | 会话详情 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/toggle_status | 切换客户允许的状态 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/toggle_typing | 输入状态 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/update_last_seen | 更新已读 |

### 3.1 Public Conversation 创建字段

| 字段 | 类型 | 说明 |
|---|---|---|
| custom_attributes | object | 会话自定义字段 |
| additional_attributes | object | 客户端扩展信息 |
| message | object 或 null | 可选首条消息，按接口支持 |

## 4. Public Message API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/messages | 客户可见消息 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/messages | 发送客户消息 |
| PATCH | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/messages/{message_id} | 更新允许修改的互动消息 |

Public 响应不应包含私有备注、内部通知、其他 Contact 或内部权限信息。

## 5. Public CSAT API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /public/api/v1/csat_survey/{id} | 获取满意度问卷 |
| PATCH | /public/api/v1/csat_survey/{id} | 提交评分和反馈 |

| 字段 | 类型 | 说明 |
|---|---|---|
| rating | integer | 评分 |
| feedback_message | string 或 null | 反馈说明 |

问卷标识应视为敏感链接，不应允许修改其他会话的 CSAT。

## 6. Platform User API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /platform/api/v1/users | 创建平台用户 |
| GET | /platform/api/v1/users/{id} | 用户详情 |
| PATCH | /platform/api/v1/users/{id} | 更新用户 |
| DELETE | /platform/api/v1/users/{id} | 删除用户 |
| GET | /platform/api/v1/users/{id}/login | 获取登录入口 |
| POST | /platform/api/v1/users/{id}/token | 生成或取得用户 Token，按授权策略 |

### 6.1 Platform User 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | User ID |
| name | string | 姓名 |
| email | string | 邮箱 |
| password | string | 创建时可用，响应不返回 |
| avatar_url | string 或 null | 头像来源 |
| custom_attributes | object | 平台自定义信息 |

## 7. Platform Account API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /platform/api/v1/accounts | 账号列表 |
| POST | /platform/api/v1/accounts | 创建账号 |
| GET | /platform/api/v1/accounts/{id} | 账号详情 |
| PATCH | /platform/api/v1/accounts/{id} | 更新账号 |
| DELETE | /platform/api/v1/accounts/{id} | 删除账号 |
| GET | /platform/api/v1/accounts/{id}/account_users | 账号成员 |
| POST | /platform/api/v1/accounts/{id}/account_users | 添加账号成员 |
| DELETE | /platform/api/v1/accounts/{id}/account_users | 移除账号成员 |

### 7.1 Account User 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| user_id | integer | User ID |
| account_id | integer | Account ID |
| role | enum | agent 或 administrator |
| custom_role_id | integer 或 null | 自定义角色 |

## 8. Platform Agent Bot API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /platform/api/v1/agent_bots | 当前 Platform App 的 Bot 列表 |
| POST | /platform/api/v1/agent_bots | 创建 Bot |
| GET | /platform/api/v1/agent_bots/{id} | Bot 详情 |
| PATCH | /platform/api/v1/agent_bots/{id} | 更新 Bot |
| DELETE | /platform/api/v1/agent_bots/{id} | 删除 Bot |
| DELETE | /platform/api/v1/agent_bots/{id}/avatar | 删除头像 |

Platform App 只能访问已授权给自身的 Bot。

## 9. 实时连接

实时接口用于向 Dashboard、Widget 或 Public Client 推送消息和状态变化。连接时需要用户会话或 PubSub Token。

### 9.1 订阅字段

| 字段 | 类型 | 说明 |
|---|---|---|
| pubsub_token | string | 客户或用户订阅令牌 |
| account_id | integer | Dashboard Account 范围，按连接方式提供 |
| user_id | integer | 当前用户，按连接方式提供 |

Token 只允许订阅对应用户、Account 或 Contact 的事件。

## 10. 常见实时事件

| 事件 | 主要对象 | 用途 |
|---|---|---|
| message.created | Message、Conversation | 新消息 |
| message.updated | Message | 送达、已读、失败或内容更新 |
| conversation.created | Conversation | 新会话 |
| conversation.updated | Conversation | 会话属性更新 |
| conversation.status_changed | Conversation | 状态变化 |
| conversation.typing_on | Conversation、User/Contact | 开始输入 |
| conversation.typing_off | Conversation、User/Contact | 停止输入 |
| contact.created | Contact | 新客户 |
| contact.updated | Contact | 客户更新 |
| notification.created | Notification | 新通知 |
| notification.updated | Notification | 通知状态变化 |
| assignee.changed | Conversation、Agent/Team | 分配变化，实际事件名按目标版本 |

## 11. 实时事件公共字段

| 字段 | 类型 | 说明 |
|---|---|---|
| event | string | 事件名称 |
| data | object | 事件对象 |
| account_id | integer 或缺失 | Account 范围 |
| conversation_id | integer 或缺失 | 会话范围 |
| echo_id | string 或 null | 本地消息匹配标识 |

具体事件可能直接返回对象而不是统一 data 外层。使用方应按订阅事件分别解析。

## 12. REST 与实时事件配合

| 场景 | 建议处理 |
|---|---|
| 创建消息 | 先展示本地 pending 状态，再用 REST 或实时返回的 id/echo_id 替换 |
| 收到未知会话事件 | 通过 Conversation 详情重新获取完整状态 |
| 断线重连 | 重新获取会话列表、未读数和当前消息页 |
| 重复事件 | 按资源 ID 更新，不重复追加 |
| 乱序事件 | 以最新资源状态和时间字段收敛 |
| 无法判断是否遗漏 | 使用 REST 重新同步，不根据事件数量推断历史 |

实时事件用于及时更新，REST 详情仍是恢复完整状态的主要接口。

## 13. 权限规则

- Public Client 只能访问当前 Contact；
- Platform App 只能访问已授权资源；
- Dashboard 用户只接收其 Account 和权限范围内的事件；
- 客户事件不能包含私有备注；
- PubSub Token 泄露后应立即失效或重建客户身份；
- 断线恢复必须重新验证身份，不能继续使用过期 Token。

完整实时事件、接收范围、Payload 和断线恢复规则详见：[Webhook 与实时事件字段字典](19-Webhook与实时事件字段字典.md)。Platform Email Channel Migration 详见：[审计、企业设置、计费与平台迁移 API](16-审计企业设置计费与平台迁移API.md)。
