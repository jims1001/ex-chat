# 会话、消息与分配 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

Conversation 是客服工作的中心对象，Message 保存客户消息、客服回复、私有备注和活动信息。会话还包括状态、优先级、分配、标签、参与者、未读和附件。

## 2. Conversation API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/conversations | 会话列表 |
| POST | /api/v1/accounts/{account_id}/conversations | 创建会话 |
| GET | /api/v1/accounts/{account_id}/conversations/{conversation_id} | 会话详情 |
| PATCH | /api/v1/accounts/{account_id}/conversations/{conversation_id} | 更新会话 |
| DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id} | 删除会话 |
| GET | /api/v1/accounts/{account_id}/conversations/meta | 列表统计 |
| GET | /api/v1/accounts/{account_id}/conversations/search | 搜索会话 |
| POST | /api/v1/accounts/{account_id}/conversations/filter | 多条件筛选 |
| GET | /api/v1/accounts/{account_id}/conversations/unread_counts | 未读统计 |

## 3. Conversation 创建字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| inbox_id | integer | 是 | Inbox ID |
| contact_id | integer | 条件必填 | 已有 Contact ID |
| source_id | string | 条件必填 | Contact 在 Inbox 中的渠道身份 |
| status | enum | 否 | 默认状态由渠道和业务规则决定 |
| assignee_id | integer 或 null | 否 | 初始 Agent |
| team_id | integer 或 null | 否 | 初始 Team |
| custom_attributes | object | 否 | 会话自定义字段 |
| additional_attributes | object | 否 | 渠道或业务扩展字段 |
| message | object | 否 | 部分创建入口允许同时创建首条消息 |

contact_id 和 source_id 的组合应能定位当前 Inbox 中的客户身份。调用方不应使用其他 Account 的 Contact。

## 4. Conversation 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 通常为 Account 内会话显示 ID |
| uuid | string | 全局会话 UUID |
| account_id | integer 或缺失 | 部分响应返回 |
| inbox_id | integer | Inbox ID |
| status | enum | open、resolved、pending、snoozed |
| priority | enum 或 null | low、medium、high、urgent 或 null |
| assignee | object 或 null | 当前 Agent |
| team | object 或 null | 当前 Team |
| contact | object | 客户摘要 |
| contact_inbox | object | 客户渠道身份 |
| labels | string array | 会话标签 |
| participants | array | 参与者，按接口返回 |
| muted | boolean | 是否静音 |
| snoozed_until | time 或 null | 延后到期时间 |
| can_reply | boolean | 当前是否允许回复 |
| unread_count | integer | 未读数量 |
| first_reply_created_at | time 或 null | 首次回复时间 |
| waiting_since | time 或 null | 当前等待起点 |
| last_activity_at | time | 最后活动时间 |
| custom_attributes | object | 会话自定义字段 |
| additional_attributes | object | 渠道和业务扩展字段 |
| messages | array | 详情中的最近消息 |
| applied_sla | object 或 null | 已应用 SLA |

## 5. Conversation 命令

| 方法 | 路径 | 请求字段 | 功能 |
|---|---|---|---|
| POST | /conversations/{id}/toggle_status | status、snoozed_until | 切换状态 |
| POST | /conversations/{id}/toggle_priority | priority | 设置或取消优先级 |
| POST | /conversations/{id}/mute | 无 | 静音 |
| POST | /conversations/{id}/unmute | 无 | 取消静音 |
| POST | /conversations/{id}/update_last_seen | 无或 last_seen | 更新已读位置 |
| POST | /conversations/{id}/unread | 无 | 标记未读 |
| POST | /conversations/{id}/toggle_typing_status | typing_status、is_private | 输入状态 |
| POST | /conversations/{id}/custom_attributes | custom_attributes | 更新自定义字段 |
| GET | /conversations/{id}/attachments | page | 附件列表 |
| POST | /conversations/{id}/transcript | 可选收件信息 | 发送或生成会话记录 |

以上路径均位于 /api/v1/accounts/{account_id} 下。

### 5.1 状态字段

| status | 业务含义 |
|---|---|
| open | 等待人工处理或正在处理 |
| resolved | 已解决 |
| pending | 等待客户、Bot 或自动流程 |
| snoozed | 延后处理，到期后重新进入处理范围 |

设置 snoozed 时通常需要 snoozed_until。客户新消息、自动规则或到期行为可能改变当前状态。

## 6. Assignment API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/conversations/{id}/assignments | 分配 Agent 或 Team |

| 字段 | 类型 | 说明 |
|---|---|---|
| assignee_id | integer 或 null | Agent ID；null 可用于取消 Agent 分配 |
| team_id | integer 或 null | Team ID；null 可用于取消 Team 分配 |

目标 Agent 必须属于 Account，并在需要时属于 Inbox。容量策略可能阻止分配。

## 7. Conversation Label API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/conversations/{id}/labels | 获取标签 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/labels | 设置标签 |

| 字段 | 类型 | 说明 |
|---|---|---|
| labels | string array | Label title 列表 |

设置操作可能按完整列表替换当前标签，调用前应先读取现有值。

## 8. Participants API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/conversations/{id}/participants | 获取参与者 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/participants | 添加参与者 |
| PATCH | /api/v1/accounts/{account_id}/conversations/{id}/participants | 更新参与者 |
| DELETE | /api/v1/accounts/{account_id}/conversations/{id}/participants | 移除参与者 |

常用字段为 user_ids。参与者不等同于 assignee，参与会话不会自动取得所有分配权限。

## 9. Message API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/conversations/{id}/messages | 消息列表 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/messages | 创建消息 |
| PATCH | /api/v1/accounts/{account_id}/conversations/{id}/messages/{message_id} | 更新允许修改的消息内容 |
| DELETE | /api/v1/accounts/{account_id}/conversations/{id}/messages/{message_id} | 删除消息内容 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/messages/{message_id}/translate | 翻译消息 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/messages/{message_id}/retry | 重试失败发送 |

## 10. Message 创建字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| content | string | 条件必填 | 正文 |
| message_type | enum | 否 | incoming、outgoing、activity、template |
| private | boolean | 否 | 是否为私有备注，默认 false |
| content_type | enum | 否 | 默认 text |
| content_attributes | object | 否 | 引用、邮件、互动内容等 |
| attachments | array | 否 | 附件，最多数量受接口限制 |
| is_voice_message | boolean | 否 | 音频是否作为语音消息 |
| echo_id | string | 否 | 客户端临时消息 ID |
| source_id | string | 否 | 外部消息 ID |
| template_params | object | 否 | 渠道模板参数 |
| cc_emails | string 或 array | Email | 抄送地址 |
| bcc_emails | string 或 array | Email | 密送地址 |
| to_emails | string 或 array | Email | 收件地址 |
| email_html_content | string | Email | 邮件 HTML 正文 |

content 与 attachments 至少应有一项有效内容。private 只对内部成员可见，但可能被已配置的可信 Webhook 或 Bot 接收，应按账号策略确认。

## 11. Message 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Message ID |
| content | string 或 null | 正文 |
| account_id | integer | Account ID，部分响应提供 |
| inbox_id | integer | Inbox ID |
| conversation_id | integer | 会话 ID |
| message_type | enum | 消息方向和类型 |
| content_type | enum | 内容类型 |
| private | boolean | 是否私有 |
| status | enum | sent、delivered、read、failed 等 |
| source_id | string 或 null | 外部消息 ID |
| echo_id | string 或 null | 客户端临时 ID |
| sender | object 或 null | Contact、User、Bot 或 Assistant 摘要 |
| attachments | array | 附件 |
| content_attributes | object | 互动、引用、邮件等属性 |
| additional_attributes | object | 渠道扩展属性 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间，按响应范围提供 |

## 12. content_type 枚举

| 值 | 用途 |
|---|---|
| text | 普通文本 |
| input_text | 单行输入 |
| input_textarea | 多行输入 |
| input_email | 邮箱输入 |
| input_select | 选项输入 |
| cards | 卡片 |
| form | 表单 |
| article | 帮助中心文章 |
| incoming_email | 入站邮件 |
| input_csat | 满意度输入 |
| integrations | 第三方集成内容，例如会议 |
| sticker | 贴纸 |
| voice_call | 语音通话事件 |

## 13. Attachment 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Attachment ID |
| file_type | enum | image、audio、video、file、location、fallback、share 等 |
| account_id | integer | Account ID，按响应提供 |
| extension | string 或 null | 文件扩展名 |
| data_url | string | 文件访问地址 |
| thumb_url | string 或 null | 缩略图 |
| file_size | integer 或 null | 文件大小 |
| width | integer 或 null | 图片或视频宽度 |
| height | integer 或 null | 图片或视频高度 |

附件地址可能是短期地址，不应长期缓存为永久公开链接。

## 14. 消息和会话业务规则

- 只有公开 Outgoing Message 才会尝试发送给客户；
- private Message 是内部备注，不进入客户聊天界面；
- API 接受消息不代表外部渠道已经送达，应继续观察 status；
- failed Message 只有在失败原因允许时才能重试；
- 删除通常不会抹除所有审计关系，客户可见内容和附件会按产品规则处理；
- 会话状态可能被客户新消息、自动化、Bot 或 SLA 流程改变；
- echo_id 和 source_id 应用于重复识别和本地消息匹配；
- 任何消息操作都必须同时满足 Account、Inbox 和 Conversation 权限。

分配策略、批量更新、草稿和 Direct Upload 详见：[分配策略、筛选、导入与批量操作 API](14-分配策略筛选导入与批量操作API.md)。
