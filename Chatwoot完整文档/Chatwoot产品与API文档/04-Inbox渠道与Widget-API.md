# Inbox、渠道与 Widget API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

Inbox 表示一个客户入口或业务渠道。不同 Inbox 可以使用网站聊天、Email、WhatsApp、社交平台、SMS 或 API Channel，并分别配置成员、工作时间、自动分配、欢迎语和 Bot。

## 2. Inbox API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/inboxes | Inbox 列表 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/inboxes | 创建 Inbox | Administrator |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id} | Inbox 详情 | 有权访问该 Inbox 的成员 |
| PATCH | /api/v1/accounts/{account_id}/inboxes/{inbox_id} | 更新 Inbox | Administrator |
| DELETE | /api/v1/accounts/{account_id}/inboxes/{inbox_id} | 删除 Inbox | Administrator |

## 3. Inbox 公共字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| id | integer | 否 | Inbox ID |
| name | string | 是 | Inbox 名称 |
| channel_id | integer | 否 | Channel ID |
| channel_type | string | 否 | 渠道类型 |
| avatar_url | string 或 null | 否 | Inbox 头像 |
| enable_auto_assignment | boolean | 是 | 是否自动分配 |
| working_hours_enabled | boolean | 是 | 是否启用工作时间 |
| timezone | string | 是 | IANA 时区 |
| out_of_office_message | string 或 null | 是 | 非工作时间提示 |
| csat_survey_enabled | boolean | 是 | 是否发送满意度调查 |
| greeting_enabled | boolean | 是 | 是否发送欢迎语 |
| greeting_message | string 或 null | 是 | 欢迎语 |
| enable_email_collect | boolean | 按渠道 | 是否收集邮箱 |
| auto_assignment_config | object | 是 | 自动分配参数 |
| selected_feature_flags | string array | 受限 | Inbox 功能开关 |
| channel | object | 否 | 渠道专属配置 |

channel_type 是展示字段，不应通过普通 Inbox 更新接口随意改变。更换渠道通常需要创建新的 Inbox。

## 4. Inbox 业务命令

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignable_agents | 可分配客服 |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/campaigns | Inbox 活动 |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/agent_bot | 当前 Agent Bot |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/set_agent_bot | 绑定或更新 Agent Bot |
| DELETE | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/avatar | 删除头像 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/sync_templates | 同步渠道模板 |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/health | 获取渠道健康状态 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/register_webhook | 注册 Provider 回调 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/reset_secret | 重置 Inbox Secret |

并非所有命令适用于所有渠道。不适用时可能返回 404、422 或空结果。

## 5. Working Hours

### 5.1 工作时间字段

| 字段 | 类型 | 说明 |
|---|---|---|
| day_of_week | integer | 0 至 6，0 表示星期日 |
| closed_all_day | boolean | 全天关闭 |
| open_all_day | boolean | 全天开放 |
| open_hour | integer | 开始小时，0 至 23 |
| open_minutes | integer | 开始分钟，0 至 59 |
| close_hour | integer | 结束小时，0 至 23 |
| close_minutes | integer | 结束分钟，0 至 59 |

工作时间按 Inbox timezone 解释。全天开放、全天关闭和具体时间段不能互相冲突。

## 6. Web Widget 字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| website_url | string | 是 | 使用 Widget 的网站地址 |
| website_token | string | 否 | Widget 公共标识 |
| widget_color | string | 是 | 主题颜色 |
| welcome_title | string 或 null | 是 | 欢迎标题 |
| welcome_tagline | string 或 null | 是 | 欢迎说明 |
| greeting_enabled | boolean | 是 | 欢迎消息开关 |
| greeting_message | string 或 null | 是 | 欢迎消息 |
| enable_email_collect | boolean | 是 | 是否收集邮箱 |
| hmac_mandatory | boolean | 是 | 是否强制客户身份 HMAC |
| allowed_domains | string array | 按版本 | 允许嵌入的域名 |
| reply_time | string | 是 | 预计回复时间展示 |

## 7. Widget 客户接口

Widget 使用 website_token 定位 Inbox，并通过客户令牌访问自己的资源。当前 Widget API 以当前客户最近会话为上下文，因此部分会话命令是集合路径，不在路径中携带 conversation_id。

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/widget/config | 获取 Widget 配置并创建或恢复客户身份 |
| GET | /api/v1/widget/contact | 获取当前客户 |
| PATCH | /api/v1/widget/contact | 更新当前客户 |
| PATCH | /api/v1/widget/contact/set_user | 绑定已验证客户身份 |
| POST | /api/v1/widget/contact/destroy_custom_attributes | 删除客户自定义字段值 |
| GET | /api/v1/widget/conversations | 获取当前会话 |
| POST | /api/v1/widget/conversations | 创建会话并发送首条消息 |
| GET | /api/v1/widget/conversations/toggle_status | 结束当前会话 |
| POST | /api/v1/widget/conversations/toggle_typing | 输入状态 |
| POST | /api/v1/widget/conversations/update_last_seen | 更新已读位置 |
| POST | /api/v1/widget/conversations/transcript | 请求会话记录 |
| POST | /api/v1/widget/conversations/set_custom_attributes | 设置会话自定义字段 |
| POST | /api/v1/widget/conversations/destroy_custom_attributes | 删除会话自定义字段值 |
| GET | /api/v1/widget/messages | 获取当前会话消息 |
| POST | /api/v1/widget/messages | 发送消息；没有会话时可同时创建会话 |
| PATCH | /api/v1/widget/messages/{message_id} | 提交互动消息内容 |
| POST | /api/v1/widget/direct_uploads | 创建附件直传信息 |
| GET | /api/v1/widget/campaigns | 获取可展示活动 |
| POST | /api/v1/widget/events | 上报允许的 Widget 事件 |
| GET | /api/v1/widget/inbox_members | 获取 Widget Inbox 可展示成员 |
| POST | /api/v1/widget/labels | 为当前会话添加已存在标签 |
| DELETE | /api/v1/widget/labels/{label} | 从当前会话移除标签 |

### 7.1 Widget Contact 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| name | string | 否 | 客户姓名 |
| email | string | 否 | 邮箱 |
| phone_number | string | 否 | E.164 电话 |
| identifier | string | 登录客户建议 | 外部客户唯一标识 |
| identifier_hash | string | 强制 HMAC 时 | identifier 的签名结果 |
| custom_attributes | object | 否 | 允许客户提交的自定义字段 |

### 7.2 Widget Conversation 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 会话显示 ID |
| uuid | string | 会话 UUID |
| status | enum | open、resolved、pending、snoozed |
| messages | array | 客户可见消息，按响应提供 |
| contact | object | 当前客户 |
| inbox | object | Widget 公共信息 |
| unread_count | integer | 未读数量 |
| last_activity_at | time | 最后活动时间 |

私有备注、内部活动和其他客户数据不能出现在 Widget 响应中。

### 7.3 Widget Inbox Member 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| payload | array | Inbox Member 列表 |
| id | integer | User ID |
| name | string | 成员名称 |
| avatar_url | string 或 null | 头像地址 |
| availability_status | enum | online、busy、offline |

Widget Label 创建字段为 label，值必须是当前 Account 已存在的标签名称。Label 接口只操作当前客户最近会话。

## 8. 渠道类型

| 渠道 | 主要前置条件 | 关键业务限制 |
|---|---|---|
| Web Widget | 网站地址、Widget 配置 | 可启用身份 HMAC 和允许域名 |
| Email | 发件/收件配置 | 线程识别、收件地址和发送状态 |
| WhatsApp | Provider 账号和模板权限 | 会话窗口、模板和回执 |
| Facebook/Instagram | Meta 授权 | 页面权限、订阅和 Token 有效期 |
| Telegram/LINE/TikTok/X | 对应平台凭证 | 平台事件和发送限制 |
| SMS | Twilio 或兼容 Provider | 电话号码、费用和送达状态 |
| API Channel | Inbox Identifier、Webhook URL | 外部系统负责渠道送达和状态回写 |

## 9. Inbox API 验收

- Administrator 可以创建和更新 Inbox；
- 普通 Agent 不能修改渠道配置；
- Agent 只能读取授权 Inbox；
- 工作时间按配置时区生效；
- 不支持的渠道命令返回明确错误；
- HMAC 强制开启后，未验证客户不能冒用 identifier；
- 删除 Inbox 前明确会话、联系人身份和第三方订阅的影响；
- Secret 重置后旧 Secret 不再被继续使用。

各渠道创建、更新和响应字段详见：[各渠道创建与更新字段字典](17-各渠道创建更新字段字典.md)。OAuth、WhatsApp、Twilio、模板、通话和 Conference 详见：[渠道授权、通话、会议与高级 Inbox API](15-渠道授权通话会议与高级Inbox-API.md)。
