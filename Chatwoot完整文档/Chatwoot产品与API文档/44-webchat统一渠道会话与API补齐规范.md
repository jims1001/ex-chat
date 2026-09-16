# webchat 统一渠道、会话与 API 补齐规范

> 对照基线：04、05、06、11、14、15、17、19、26、29、30、33、34、39 和 40 分册
> 目标：把 webchat 现有网站、微信、短信和语音能力统一到 Account、Inbox、Contact、Conversation 和 Message 合同，并补齐缺失渠道。
> 边界：本文只规定功能、业务流程、接口、字段、状态和验收，不包含代码实现。

## 1. 目标体验

无论客户来自网站、Email、WhatsApp、Facebook、Instagram、Telegram、LINE、TikTok、X、微信、短信、语音还是自建客户端，均应进入统一 Inbox 体系。客服在同一工作台查看客户、会话、消息、附件、分配和历史，不需要按渠道切换独立业务系统。

## 2. 渠道范围

| 渠道 | 当前状态 | 补齐目标 | 关键依赖 |
|---|---|---|---|
| Website Widget | 已有，需统一 | 保留外观、表单和排队能力，兼容 Widget 身份和消息合同 | website_token、允许域名 |
| API Channel | 部分已有 | 支持自建客户界面和服务端消息 | Inbox identifier、Contact identifier |
| 微信公众号 | 已有，需统一 | 归入 Inbox、ContactInbox、Conversation 和统一发送状态 | 微信授权和回调 |
| 微信小程序 | 已有，需统一 | 与公众号共享对象语义，保留小程序身份 | 小程序授权和回调 |
| SMS | 部分已有 | 形成双向消息、状态回调和会话归属 | Provider 账号、号码 |
| Voice | 部分已有 | 通话、会议、录音、转写和会话消息形成闭环 | Voice Provider |
| Email | 未覆盖 | 入站邮件、出站回复、线程、附件、抄送和失败状态 | 邮箱或邮件 Provider |
| WhatsApp | 未覆盖 | 嵌入式授权、模板、窗口期、状态回调和语音能力 | Meta 账号和审核 |
| Facebook | 未覆盖 | Page 授权、Messenger 消息和状态恢复 | Meta Page 权限 |
| Instagram | 未覆盖 | 账号授权、私信、媒体和回复限制 | Instagram Business 权限 |
| Telegram | 未覆盖 | Bot Token、聊天身份、附件和发送状态 | Telegram Bot |
| LINE | 未覆盖 | Channel 授权、用户身份、消息和回调 | LINE Channel |
| TikTok | 未覆盖 | 业务账号授权、私信和回调 | TikTok 业务权限 |
| X | 未覆盖 | 私信、媒体和授权恢复 | X API 权限 |
| Twilio | 未覆盖 | SMS、WhatsApp 或 Voice 的标准 Provider 合同 | Twilio 账号 |

## 3. 统一对象设计

### 3.1 Account

Account 是最高业务数据边界。

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 账号标识 | 全局唯一，不允许客户端自行修改 |
| name | 账号名称 | 必填 |
| locale | 默认语言 | 影响系统提示和默认内容 |
| timezone | 默认时区 | 报表、工作时间、SLA 和计划任务统一使用 |
| settings | 账号设置 | 只允许已定义设置项 |
| feature_flags | 功能开关 | 读取为主，变更受授权限制 |
| status | 账号状态 | active、suspended、pending_deletion、deleted |

### 3.2 Inbox

Inbox 表示一条具体客户渠道或业务入口。

| 字段 | 含义 | 规则 |
|---|---|---|
| id | Inbox 标识 | Account 内唯一 |
| name | 展示名称 | 必填 |
| channel_type | 渠道类型 | 创建后原则上不可跨类型修改 |
| status | 业务状态 | active、inactive、archived |
| health_status | 渠道健康 | healthy、degraded、disconnected、reauth_required |
| greeting_enabled | 是否发送欢迎语 | 仅渠道支持时生效 |
| greeting_message | 欢迎语 | greeting_enabled 为 true 时使用 |
| working_hours_enabled | 是否启用工作时间 | 启用后非工作时间按规则处理 |
| out_of_office_message | 非工作时间提示 | 可为空 |
| enable_auto_assignment | 是否自动分配 | 与 Assignment Policy 联合判断 |
| member_ids | Inbox 成员 | 成员必须属于同一 Account |
| assignment_policy_id | 分配策略 | 一个 Inbox 同时只绑定一个有效策略 |
| provider | 渠道服务商 | 与 channel_type 组合校验 |
| provider_config | Provider 配置 | 敏感字段写入后不得完整返回 |
| created_at | 创建时间 | 只读 |
| updated_at | 更新时间 | 只读 |

### 3.3 Contact

Contact 是 Account 内统一客户主体。

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 客户标识 | Account 内唯一 |
| identifier | 外部稳定标识 | Account 内唯一，可为空但不能重复非空值 |
| name | 客户名称 | 可为空 |
| email | 邮箱 | 规范化后参与匹配，不单独作为绝对唯一依据 |
| phone_number | 电话 | 按国家区号规范化 |
| company_id | 公司 | 必须属于同一 Account |
| custom_attributes | 自定义属性 | 只允许已定义字段或明确开放的扩展字段 |
| additional_attributes | 渠道或系统补充信息 | 读取和写入范围分别控制 |
| labels | 客户标签 | 标签必须属于同一 Account |
| blocked | 是否阻止沟通 | 阻止后保留历史，不再创建正常出站消息 |
| created_at | 创建时间 | 只读 |
| updated_at | 更新时间 | 只读 |

### 3.4 ContactInbox

ContactInbox 表示客户在一个 Inbox 中的具体身份。

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 渠道身份标识 | 只读 |
| contact_id | Contact | 必须属于同一 Account |
| inbox_id | Inbox | 必须属于同一 Account |
| source_id | Provider 或 Widget 客户标识 | 同一 Inbox 内唯一 |
| pubsub_token | 客户实时订阅 Token | 仅在需要时返回，必须可撤销 |
| verified | 身份是否已验证 | 不同渠道按各自验证规则设置 |
| last_seen_at | 最后活跃时间 | 按节流规则更新 |

### 3.5 Conversation

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 内部标识 | 只读 |
| display_id | Account 内显示编号 | Account 内唯一且递增 |
| account_id | Account | 只读 |
| inbox_id | Inbox | 必须与 ContactInbox 一致 |
| contact_id | Contact | 必须与 ContactInbox 一致 |
| contact_inbox_id | 渠道身份 | 必填 |
| status | 会话状态 | open、resolved、pending、snoozed |
| priority | 优先级 | urgent、high、medium、low、none |
| assignee_id | 当前客服 | 可为空，必须为允许访问该 Inbox 的成员 |
| team_id | 当前团队 | 可为空，必须属于同一 Account |
| snoozed_until | 延后结束时间 | status 为 snoozed 时使用 |
| muted | 是否静音 | 只影响通知，不阻断新消息 |
| labels | 会话标签 | 标签必须属于同一 Account |
| custom_attributes | 会话扩展属性 | 受字段定义限制 |
| first_reply_created_at | 首次人工回复时间 | 只读 |
| last_activity_at | 最后活动时间 | 由有效消息或业务活动更新 |
| waiting_since | 当前等待起点 | 用于实时监控和 SLA |
| created_at | 创建时间 | 只读 |
| updated_at | 更新时间 | 只读 |

### 3.6 Message

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 消息标识 | 只读 |
| conversation_id | 所属会话 | 必填 |
| sender_type | 发送者类型 | Contact、User、AgentBot、Assistant、System |
| sender_id | 发送者标识 | 必须与 sender_type 匹配 |
| message_type | 消息方向 | incoming、outgoing、activity、template |
| content | 消息正文 | 可与附件组合，是否允许空值由 content_type 决定 |
| content_type | 内容类型 | text、input、cards、form、location、file 等 |
| private | 是否私有备注 | 私有消息不得发送给客户或 Provider |
| status | 发送状态 | queued、accepted、sent、delivered、read、failed |
| external_source_id | Provider 消息标识 | Provider 范围内去重 |
| echo_id | 调用方幂等标识 | 同一会话内不可重复生成新消息 |
| reply_to | 被引用消息 | 必须属于同一会话 |
| error | 失败详情 | 失败时返回可处理分类，不返回敏感凭证 |
| created_at | 创建时间 | 只读 |
| updated_at | 更新时间 | 只读 |

### 3.7 Attachment

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 附件标识 | 只读 |
| file_type | 类型 | image、audio、video、file、location |
| data_url | 下载地址 | 必须受访问权限和有效期限制 |
| file_name | 文件名 | 保留原始名称并进行安全展示 |
| file_size | 字节数 | 受渠道和账号上限限制 |
| content_type | MIME 类型 | 必须与实际内容一致 |
| status | 处理状态 | uploaded、processing、ready、failed、quarantined |
| coordinates | 位置信息 | 仅 location 类型使用 |

## 4. 核心 API 补齐清单

### 4.1 Account 与 Inbox

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id} | 获取账号信息 |
| PATCH | /api/v1/accounts/{account_id} | 更新允许修改的账号设置 |
| GET | /api/v1/accounts/{account_id}/inboxes | 查询 Inbox |
| POST | /api/v1/accounts/{account_id}/inboxes | 创建 Inbox |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id} | 获取 Inbox 详情 |
| PATCH | /api/v1/accounts/{account_id}/inboxes/{inbox_id} | 更新 Inbox |
| DELETE | /api/v1/accounts/{account_id}/inboxes/{inbox_id} | 删除或归档 Inbox |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/health | 查询 WhatsApp Cloud Inbox 健康 |
| POST | /api/v1/accounts/{account_id}/callbacks/reauthorize_page | 重新授权 Facebook Page；其他 Provider 使用 15、26 分册记录的授权入口 |
| GET | /api/v1/accounts/{account_id}/inbox_members | 查询 Inbox 成员 |
| POST | /api/v1/accounts/{account_id}/inbox_members | 设置成员 |
| DELETE | /api/v1/accounts/{account_id}/inbox_members | 移除成员 |

### 4.2 Widget

| 方法 | 路径 | 功能 | 主要字段 |
|---|---|---|---|
| POST | /api/v1/widget/config | 初始化并获取 Widget 配置 | website_token |
| GET、PATCH、PUT | /api/v1/widget/contact | 获取或更新当前客户 | name、email、phone_number、custom_attributes |
| GET | /api/v1/widget/conversations | 查询当前客户会话 | status、page |
| POST | /api/v1/widget/conversations | 创建会话 | custom_attributes |
| GET | /api/v1/widget/messages | 查询当前 Widget 会话消息 | before、after、page |
| POST | /api/v1/widget/messages | 发送当前 Widget 会话消息 | content、echo_id、attachments |
| GET | /api/v1/widget/conversations/toggle_status | 切换当前 Widget 会话状态 | status |
| POST | /api/v1/widget/events | 上报客户行为 | event_name、event_data |

### 4.3 Contact、Company 和标签

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/contacts | 查询或创建客户 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/contacts/{contact_id} | 查看、更新或删除客户 |
| POST | /api/v1/accounts/{account_id}/actions/contact_merge | 合并客户 |
| POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/contact_inboxes | 创建渠道身份 |
| GET、POST | /api/v1/accounts/{account_id}/companies | 查询或创建公司 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/companies/{company_id} | 公司详情和维护 |
| GET、POST | /api/v1/accounts/{account_id}/labels | 查询或创建标签 |
| PATCH、DELETE | /api/v1/accounts/{account_id}/labels/{label_id} | 更新或删除标签 |
| GET、POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes | 查询或新增客户备注 |

### 4.4 Conversation 与 Message

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/conversations | 查询或创建会话 |
| GET、PATCH | /api/v1/accounts/{account_id}/conversations/{conversation_id} | 获取或更新会话 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/toggle_status | 改变 open、resolved、pending、snoozed |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/assignments | 设置客服或团队 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/toggle_priority | 设置优先级 |
| GET、POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/labels | 查询或替换会话标签 |
| GET、POST、PATCH、PUT、DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id}/participants | 管理参与者 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/mute | 静音会话通知 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/unmute | 恢复通知 |
| GET、POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages | 查询或发送消息 |
| DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages/{message_id} | 删除允许删除的消息 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages/{message_id}/retry | 重试可重试失败消息 |
| GET、PATCH、PUT、DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id}/draft_messages | 管理服务端草稿 |

## 5. 会话状态设计

| 当前状态 | 可进入状态 | 主要触发 |
|---|---|---|
| open | resolved、pending、snoozed | 人工解决、转机器人等待、延后处理 |
| resolved | open | 客户新消息、人工重新打开或自动化 |
| pending | open、resolved | 人工接管、机器人完成或超时 |
| snoozed | open、resolved | 到达时间、客户新消息、人工恢复或解决 |

规则：

- 客户在 resolved 会话中发送新消息时，按 Inbox 配置重新打开或创建新会话；
- snoozed 到期必须只触发一次恢复；
- pending 表示等待 Bot、Assistant 或外部处理，不等于客户排队；
- 状态变化必须生成活动消息、实时事件、Webhook、审计和报表副作用；
- 同一状态重复提交应返回当前结果，不重复生成副作用。

## 6. 分配和转接规则

1. 自动分配前先检查 Account、Inbox、成员关系、客服可用状态和容量；
2. 技能组、Team 和 Inbox 成员必须在统一关系中表达；
3. 历史客服优先只能在客服仍可用且未超容量时生效；
4. 人工分配和自动分配竞争时，只允许一个最终 assignee；
5. 转接应保留原客服、目标客服、原因、发起时间、接受时间和最终结果；
6. 拒绝或超时的转接不能丢失会话，也不能产生两个同时处理者；
7. 分配变化必须更新实时工作台、通知、Webhook、审计和报表；
8. 现有质检、工单和会话历史仍以最终会话标识关联。

## 7. 入站消息流程

1. 校验 Provider 请求来源、签名、时间和重放窗口；
2. 通过 Inbox 和 source_id 查找 ContactInbox；
3. 不存在时创建 Contact 与 ContactInbox，存在时更新允许更新的信息；
4. 查找可复用 Conversation，或按渠道规则创建新 Conversation；
5. 使用 Provider 消息标识执行去重；
6. 保存 incoming Message 和 Attachment；
7. 更新 Conversation 的 last_activity_at 和 waiting_since；
8. 执行分配、自动化、Bot 或 Assistant；
9. 发送实时事件、通知和 Webhook；
10. 更新搜索、报表、SLA 和审计。

## 8. 出站消息流程

1. 校验发送者身份、Inbox 权限、会话状态和客户阻止状态；
2. 校验内容类型、附件、模板、窗口期和渠道限制；
3. 使用 echo_id 或幂等键避免重复创建；
4. 创建 queued 或 accepted 消息；
5. Provider 接受后更新为 sent；
6. 根据回调更新 delivered、read 或 failed；
7. 失败时记录分类、可重试性、尝试次数和下一次尝试时间；
8. 每次有效状态变化发送相应实时事件和 Webhook；
9. 终态不得被迟到的低级状态覆盖。

## 9. Provider 状态与错误

| 状态 | 含义 | 是否终态 |
|---|---|---:|
| queued | 已创建，等待投递 | 否 |
| accepted | 已被内部发送流程接受 | 否 |
| sent | Provider 已接受 | 否 |
| delivered | 已送达客户设备或渠道 | 否 |
| read | 客户已读 | 是 |
| failed | 明确失败 | 是，允许按错误类型重新尝试 |

| 错误类别 | 处理原则 |
|---|---|
| authentication | 标记 Inbox 需要重新授权，暂停自动重试 |
| permission | 提示权限或审核不足，保留消息失败状态 |
| rate_limit | 尊重 Retry-After，延迟重试 |
| temporary_provider | 有上限地退避重试 |
| invalid_recipient | 不自动重试，提示客户身份无效 |
| invalid_content | 不自动重试，提示内容或附件不符合渠道要求 |
| outside_window | 要求使用允许的模板或重新获得客户消息窗口 |
| duplicate | 关联已有消息，不创建重复消息 |
| unknown | 查询 Provider 最终状态后再决定是否重试 |

## 10. 实时事件

至少补齐以下事件：

- contact.created、contact.updated、contact.merged；
- conversation.created、conversation.updated、conversation.status_changed；
- conversation.assignment_changed、conversation.priority_changed；
- message.created、message.updated、message.deleted；
- message.sent、message.delivered、message.read、message.failed；
- typing.on、typing.off；
- presence.update；
- inbox.health_changed、inbox.authorization_changed。

每个事件必须包含 event、account_id、resource、changed_fields、occurred_at 和 event_id。重复 event_id 不得产生重复处理；断线重连后必须能够重新读取最终对象状态。

## 11. 旧数据兼容

| 现有概念 | 目标概念 | 迁移规则 |
|---|---|---|
| 租户或组织标识 | Account | 保持一对一映射并固定时区和语言 |
| 网站渠道或微信账号 | Inbox | 每个实际入口创建独立 Inbox |
| 访客、客户或微信用户 | Contact + ContactInbox | 先确定统一客户，再保留渠道 source_id |
| 客服服务记录 | Conversation | 保留开始、分配、结束、满意度和质检关联 |
| 聊天记录 | Message | 保留方向、类型、时间、发送者和附件 |
| 接待组或技能组 | Team / Assignment Policy | 分离组织归属和分配规则 |
| 客服最大接待数 | Capacity Policy | 账号默认值和客服例外值分别表达 |
| 会话标签 | Label | 转换为 Account 内可复用标签 |

迁移后不得改变原始消息时间、发送方向、客户归属、客服归属、满意度、质检结果或工单关联。无法可靠映射的字段进入受控扩展属性，并记录来源和转换说明。

## 12. 验收清单

### 12.1 核心闭环

- 每种渠道都能创建或识别客户、建立会话、接收入站消息和发送出站消息；
- 工作台能在统一列表中显示全部渠道；
- 分配、转接、状态、优先级、标签、私有备注和附件均能正常使用；
- resolved、pending、snoozed 和重新打开的状态变化符合规则；
- 客户再次进入时不会无规则地创建重复 Contact 或 Conversation；
- 现有工单、质检、满意度和报表关联不丢失。

### 12.2 渠道闭环

- 授权成功、取消、权限不足、Token 过期和重新授权均有明确状态；
- 文本、图片、音频、视频、文件和渠道支持的互动类型均按能力处理；
- sent、delivered、read、failed 状态按回调推进；
- 重复、乱序、回声、限流和断网恢复不会生成重复消息；
- 模板、窗口期和渠道特有规则有明确错误和恢复动作。

### 12.3 接口闭环

- Account、Widget、Public、Provider Callback 和 Realtime 身份不能越权；
- 列表分页没有重复或遗漏，排序稳定；
- 重复创建请求不会产生重复客户、会话和消息；
- 新旧入口对相同动作产生相同对象状态、事件、通知、报表和审计；
- 所有异步发送最终进入明确终态；
- 任何跨 Account 资源 ID 均返回不可访问结果，不泄露资源是否存在。
