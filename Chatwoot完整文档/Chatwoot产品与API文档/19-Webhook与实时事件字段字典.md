# Webhook 与实时事件字段字典

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[集成、Webhook、Agent Bot 与插件 API](10-集成Webhook-AgentBot与插件API.md)｜[Public、Platform 与实时事件 API](11-Public-Platform与实时事件API.md)

## 1. 两类事件的区别

| 类型 | 事件名格式 | 接收方式 | 主要用途 |
|---|---|---|---|
| Account Webhook | conversation_created 等下划线名称 | HTTP POST | 把业务变化发送到外部系统 |
| 实时事件 | conversation.created 等点号名称 | /cable 长连接 | 即时刷新工作台或客户聊天界面 |

两类事件名称、可选范围和字段不完全相同。不能把实时事件名称直接填入 Webhook subscriptions，也不能假设所有实时事件都提供外部 Webhook。

## 2. Account Webhook API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/webhooks | Webhook 列表 | Administrator |
| POST | /api/v1/accounts/{account_id}/webhooks | 创建 Webhook | Administrator |
| PATCH | /api/v1/accounts/{account_id}/webhooks/{id} | 更新地址或订阅 | Administrator |
| DELETE | /api/v1/accounts/{account_id}/webhooks/{id} | 删除 Webhook | Administrator |

## 3. Webhook 配置字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| name | string | 否 | Webhook 名称 |
| url | string | 是 | HTTP 或 HTTPS 接收地址，在 Account 内保持唯一 |
| subscriptions | string array | 是 | 至少一个允许的事件 |
| inbox_id | integer 或 null | 否 | 可关联的 Inbox ID |
| id | integer | 响应 | Webhook ID |
| account_id | integer | 响应 | 所属 Account |
| inbox | object 或缺失 | 响应 | 已关联 Inbox 时返回 id、name |
| secret | string | 响应 | 用于签名验证，需作为敏感信息保存 |

API Channel 自身的 channel.webhook_url 不属于 Account Webhook 列表，但接收的会话与消息 Payload 结构相近。

## 4. 可订阅 Webhook 事件

| subscription | 触发条件 | Payload 重点 |
|---|---|---|
| conversation_created | 新会话建立 | 会话、Account、最近消息、联系人和分配摘要 |
| conversation_updated | 会话属性更新且有变化 | 会话字段和 changed_attributes |
| conversation_status_changed | 会话状态变化 | 会话字段和 changed_attributes |
| conversation_typing_on | 客户或成员开始输入 | user、conversation、is_private |
| conversation_typing_off | 客户或成员停止输入 | user、conversation、is_private |
| message_created | 新建可发送的消息 | 消息、会话、Inbox、发送者、附件 |
| message_updated | 可发送消息被更新 | 更新后的消息字段 |
| contact_created | 新联系人建立 | 联系人和 Account |
| contact_updated | 联系人字段确有变化 | 联系人和 changed_attributes |
| inbox_created | 新 Inbox 建立 | Inbox 公共配置、channel 摘要、Account |
| inbox_updated | Inbox 字段确有变化 | Inbox 公共配置和 changed_attributes |
| webwidget_triggered | Widget 上报允许的客户事件 | ContactInbox、当前会话和 event_info |

message_created 和 message_updated 只发送符合 Webhook 发送条件的消息。私有、内部活动或渠道不应外发的内容可能被过滤。

## 5. 会话事件字段

conversation_created、conversation_updated 和 conversation_status_changed 的主体为会话对象。

| 字段 | 类型 | 说明 |
|---|---|---|
| event | string | 当前 Webhook 事件名 |
| id | integer | Account 内会话显示 ID |
| account | object | id、name |
| inbox_id | integer | Inbox ID |
| channel | string | 渠道类型 |
| contact_inbox | object | 联系人与 Inbox 的渠道身份摘要 |
| messages | array | 最近一条可外发聊天消息摘要 |
| labels | string array | 会话标签 |
| meta.sender | object | 联系人摘要 |
| meta.assignee | object 或 null | 当前负责人 |
| meta.assignee_type | string 或 null | 分配对象类型 |
| meta.team | object 或 null | 当前团队 |
| meta.hmac_verified | boolean | 渠道身份是否通过 HMAC |
| status | enum | open、resolved、pending、snoozed |
| priority | enum 或 null | urgent、high、medium、low |
| custom_attributes | object | 会话自定义字段 |
| additional_attributes | object | 渠道和会话附加信息 |
| can_reply | boolean | 当前是否允许回复 |
| unread_count | integer | 未读客户消息数 |
| snoozed_until | time 或 null | 延后结束时间 |
| waiting_since | integer | 当前等待起点的 Unix 时间 |
| created_at、updated_at、last_activity_at、timestamp | time 或 number | 会话时间字段 |
| changed_attributes | object | 更新事件的旧值和新值，仅变化事件提供 |

changed_attributes 中每个字段通常表示前值和后值。接收端应按字段名处理，不依赖字段顺序。

## 6. 消息事件字段

| 字段 | 类型 | 说明 |
|---|---|---|
| event | string | message_created 或 message_updated |
| id | integer | Message ID，可作为去重主键 |
| account | object | id、name |
| inbox | object | id、name |
| conversation | object | 完整会话 Webhook 摘要 |
| content | string 或 null | 面向外部接收端规范化后的正文 |
| content_type | string | text、input_select、form 等内容类型 |
| content_attributes | object | 内容类型附加字段 |
| additional_attributes | object | 渠道附加字段 |
| message_type | enum | incoming、outgoing、activity、template 等 |
| private | boolean | 是否私有消息 |
| sender | object 或 null | Contact、User 或 Bot 摘要 |
| source_id | string 或 null | 渠道侧消息 ID |
| attachments | array | 附件摘要，存在附件时提供 |
| created_at | time | 创建时间 |

接收端应优先用 Message id 去重，用 source_id 与第三方平台消息建立映射。content 可能为空，例如纯附件或结构化消息。

## 7. 联系人、Inbox 与 Widget 字段

### 7.1 Contact Payload

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Contact ID |
| account | object | id、name |
| identifier | string 或 null | 外部客户标识 |
| name、email、phone_number | string 或 null | 客户资料 |
| avatar、thumbnail | string 或 null | 头像地址 |
| blocked | boolean | 是否阻止客户消息 |
| custom_attributes | object | 自定义字段 |
| additional_attributes | object | 国家、城市、公司等附加资料 |
| changed_attributes | object | contact_updated 时提供 |

### 7.2 Inbox Payload

包括 Account、营业时间、自动分配、欢迎语、CSAT、Email 展示字段、允许解决后发消息、channel 摘要以及 created_at、updated_at。敏感渠道凭证不应依赖该事件返回。

### 7.3 Web Widget Payload

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | ContactInbox ID |
| contact | object | 当前客户 |
| inbox | object | Inbox 摘要 |
| account | object | Account 摘要 |
| current_conversation | object 或 null | 最近会话 |
| source_id | string | 客户在 Inbox 中的渠道标识 |
| event_info | object | Widget 上报的事件信息 |

## 8. Webhook 请求头与签名

| Header | 说明 |
|---|---|
| Content-Type | application/json |
| Accept | application/json |
| X-Chatwoot-Delivery | 本次投递的唯一标识 |
| X-Chatwoot-Timestamp | 生成签名时使用的 Unix 时间 |
| X-Chatwoot-Signature | 使用 Webhook Secret、时间戳和原始请求体生成的 SHA-256 HMAC |

验签时必须使用未经重新格式化的原始请求体，并校验时间戳是否在允许窗口内。Delivery ID 用于投递去重，Message ID 或 Conversation ID 用于业务去重，两者用途不同。

## 9. Webhook 投递规则

- 使用 HTTP POST 和 JSON；
- 默认请求超时较短，接收端应快速返回成功，再异步处理业务；
- Account Webhook 和 API Channel Webhook 不应依赖必然自动重试；
- 单个实体的多个事件可能紧邻到达，不能假设不同 URL 或不同事件之间全局有序；
- 重复事件必须安全处理，更新操作应根据对象 ID 和更新时间决定是否覆盖；
- changed_attributes 为空的 contact_updated 或 inbox_updated 不会产生有效投递；
- URL 变更、Secret 轮换和订阅调整后应进行一次签名与事件回归测试。

## 10. 实时连接

实时事件通过 /cable 接收。Account 成员通常使用用户 pubsub_token 和 Account 范围，Widget 或 Public Contact 使用 ContactInbox pubsub_token。事件外层包含 event 和 data，data 通常带 account_id。

## 11. 实时事件字典

| event | 接收范围 | data 重点 |
|---|---|---|
| message.created | Inbox 成员和可见客户 | Message、Conversation 摘要、sender、attachments |
| message.updated | Inbox 成员和可见客户 | 更新后的 Message、previous_changes |
| first.reply.created | Inbox 成员 | 首次人工回复消息 |
| conversation.created | Inbox 成员和当前客户 | Conversation 完整实时摘要 |
| conversation.read | Inbox 成员 | 会话已读位置变化 |
| conversation.status_changed | Inbox 成员和当前客户 | 最新 status 和会话摘要 |
| conversation.updated | Inbox 成员和当前客户 | 最新会话摘要 |
| conversation.unread_count_changed | 有权成员 | 提示重新获取筛选未读数 |
| conversation.typing_on | 除发起方外的可见成员和客户 | conversation、user、is_private |
| conversation.typing_off | 除发起方外的可见成员和客户 | conversation、user、is_private |
| assignee.changed | Inbox 成员 | 最新 assignee 和会话摘要 |
| team.changed | Inbox 成员 | 最新 team 和会话摘要 |
| conversation.contact_changed | Inbox 成员 | 联系人替换后的会话摘要 |
| conversation.mentioned | 被提及成员 | 会话摘要 |
| contact.created | Account 范围 | Contact 摘要 |
| contact.updated | Account 范围 | Contact 摘要 |
| contact.merged | Account 范围 | 合并后的 Contact 摘要 |
| contact.deleted | Account 范围 | 被删除联系人 ID 和 Account ID |
| notification.created | 目标成员 | Notification、unread_count、count |
| notification.updated | 目标成员 | Notification、unread_count、count |
| notification.deleted | 目标成员 | Notification ID、unread_count、count |
| account.cache_invalidated | Account 成员 | cache_keys，提示刷新指定缓存 |
| copilot.message.created | 当前 Copilot 使用者 | Copilot Message 摘要 |
| presence.update | 当前房间订阅者 | 成员在线状态集合 |

## 12. 实时事件处理规则

- 使用 event 决定处理分支，不用 data 的字段形状猜测事件类型；
- 使用 account_id 阻止跨 Account 更新界面状态；
- message.created 以 Message id 去重；
- message.updated 应合并最新字段，并参考 previous_changes 更新局部状态；
- conversation.unread_count_changed 不携带完整统计，应重新读取未读数量；
- typing 事件是短暂状态，断线或超时后应自动清除；
- cache_invalidated 表示指定缓存需要刷新，不代表所有 Account 数据都已改变；
- 断线重连后应重新获取会话、消息和未读数，不能只依赖断线期间的事件补发。

## 13. Webhook 与实时事件验收

- 订阅列表只包含允许的 12 个 Webhook 事件；
- 每次 Webhook 均校验 Delivery、Timestamp 和 Signature；
- 同一 Message 重复投递不会重复创建外部消息；
- conversation_updated 能正确解释 changed_attributes；
- 实时事件按 Account 和可见 Inbox 过滤；
- 私有备注不会发送给客户实时连接；
- 断线重连后通过查询接口恢复最终状态；
- URL 或 Secret 轮换后旧签名不再被接受。

同一动作还可能产生 Notification、Automation、Agent Bot、Integration Hook、Reporting Event、CSAT 和 Captain 结果，完整对照见 [业务动作、事件、副作用与跨入口一致性](40-业务动作事件副作用与跨入口一致性.md)。
