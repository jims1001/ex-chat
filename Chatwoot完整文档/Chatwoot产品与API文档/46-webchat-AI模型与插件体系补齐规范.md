# webchat AI 模型与插件体系补齐规范

> 对照基线：09、10、19、22、27、29、31、33、35、36、40 和 41 分册
> 目标：在保留 webchat 传统知识机器人能力的基础上，补齐多模型 AI、Assistant、Copilot、知识文档、Custom Tool、Webhook 和可安装插件体系。
> 边界：本文只描述功能、接口、字段、状态、安全和验收，不包含模型训练代码、插件代码或其他源码细节。

## 1. 能力边界

### 1.1 继续保留的能力

- 知识分类、知识问答、关键词和相似问题；
- 热点问题、场景流程和多轮问答；
- 未命中转人工、答案评价和人工纠正；
- 批量导入导出知识；
- 外部机器人地址和外部知识查询；
- 机器人触发工单、短信、分配或转人工。

### 1.2 不能视为已经具备的能力

| 现有能力 | 不能直接等同于 | 原因 |
|---|---|---|
| 外部机器人地址 | AI Provider 管理 | 缺少模型、能力、额度、健康和切换合同 |
| 知识库搜索 | Assistant Document | 缺少处理状态、版本、引用和 Assistant 归属 |
| 快捷问答 | Copilot | 缺少客服会话线程、上下文和任务类型 |
| 场景流程 | Custom Tool | 缺少参数模式、认证、确认、超时和副作用 |
| 聊天记录外发 | Webhook 平台 | 缺少订阅、签名、投递记录和重放 |
| 定点外部地址 | 插件平台 | 缺少应用目录、安装实例、权限和生命周期 |

## 2. AI 总体对象

| 对象 | 功能 |
|---|---|
| AI Provider | 描述一个模型服务来源及其连接状态 |
| AI Model | 描述具体模型、能力、上下文和可用状态 |
| AI Preference | Account 对 Provider、Model 和 Feature 的选择 |
| Assistant | 面向客户或客服的助手实例 |
| Inbox Binding | Assistant 与 Inbox 的绑定关系 |
| Scenario | Assistant 的业务目标、指令和工具组合 |
| Document | 可检索知识文档及其处理状态 |
| FAQ | 结构化问题和答案 |
| Copilot Thread | 客服与 AI 的辅助会话 |
| AI Task | 摘要、改写、翻译等一次性任务 |
| Custom Tool | 允许 Assistant 调用的外部业务动作 |
| AI Usage | 用量、额度、费用和错误统计 |
| AI Memory | 允许保留的客户或会话记忆 |

## 3. Provider 与 Model

### 3.1 Provider 字段

| 字段 | 含义 | 规则 |
|---|---|---|
| id | Provider 标识 | 稳定唯一 |
| name | 展示名称 | 例如 OpenAI、Azure OpenAI 或自定义兼容服务 |
| provider_type | Provider 类型 | 与认证和模型列表联合校验 |
| base_url | 服务地址 | 自定义服务使用，受允许域名策略限制 |
| auth_type | 认证类型 | api_key、oauth、managed、none |
| credential_status | 凭证状态 | missing、valid、invalid、expired |
| health_status | 健康状态 | healthy、degraded、unavailable |
| supported_features | 支持能力 | chat、embedding、vision、audio、tool_call、rerank |
| enabled | 是否允许使用 | 关闭后停止新任务，历史结果保留 |
| last_checked_at | 最近健康检查时间 | 只读 |

凭证写入后不得完整返回；读取只返回是否已配置、更新时间和必要的掩码摘要。

### 3.2 Model 字段

| 字段 | 含义 | 规则 |
|---|---|---|
| id | Model ID | Provider 范围内唯一 |
| provider_id | Provider | 必填 |
| name | 展示名称 | 必填 |
| model_identifier | Provider 模型标识 | 调用时使用 |
| capabilities | 能力 | chat、embedding、vision、audio、tool_call 等 |
| context_window | 上下文容量 | 只读或配置快照 |
| max_output_tokens | 最大输出 | 不得超过 Provider 限制 |
| status | 模型状态 | available、deprecated、disabled、unavailable |
| version | 模型版本 | 记录能力漂移和验收基线 |
| pricing | 计量信息 | 可为空，存在时包含输入和输出单位 |

### 3.3 Preference 字段

| 字段 | 含义 |
|---|---|
| default_chat_model | 默认对话模型 |
| default_embedding_model | 默认向量模型 |
| default_rerank_model | 默认重排模型 |
| fallback_models | 按顺序使用的备用模型 |
| captain_features | 已启用的 AI 功能 |
| temperature | 默认生成随机度 |
| max_output_tokens | 默认最大输出 |
| language | 默认输出语言或自动识别 |
| data_retention | Provider 数据保留策略 |

### 3.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、PATCH | /api/v1/accounts/{account_id}/captain/preferences | 查询或更新 AI 偏好 |

Chatwoot 4.16.0 通过 Preferences 返回 Provider、Model 与 Feature 可用性，没有独立公开的 Provider 或 Model 增删改路径。webchat 如增加自定义 Provider 管理入口，必须标记为扩展接口，不能计入 Chatwoot 基线兼容动作。

## 4. Assistant

### 4.1 功能

Assistant 是 AI 资源的拥有者。知识文档、FAQ、Scenario、Tool 和 Inbox Binding 必须属于某个 Assistant，不能只作为 Account 全局散落配置。

### 4.2 字段

| 字段 | 含义 | 规则 |
|---|---|---|
| id | Assistant 标识 | Account 内唯一 |
| name | 名称 | 必填 |
| description | 说明 | 可为空 |
| status | 状态 | draft、active、paused、archived |
| model_id | 默认模型 | 必须在 Account 可用 |
| fallback_model_ids | 备用模型 | 有序列表 |
| config | 回复、检索和转人工设置 | 只允许定义项 |
| guardrails | 内容、隐私和工具安全规则 | 必填默认规则 |
| welcome_message | 助手欢迎语 | 可为空 |
| handoff_message | 转人工提示 | 可为空 |
| unresolved_action | 无答案动作 | handoff、ask_clarification、no_answer |
| created_at、updated_at | 时间 | 只读 |

### 4.3 Inbox Binding 字段

| 字段 | 含义 |
|---|---|
| assistant_id | Assistant |
| inbox_id | Inbox |
| enabled | 是否启用 |
| takeover_mode | automatic、pending、manual |
| working_hours_only | 是否只在工作时间启用 |
| human_handoff_team_id | 默认转人工团队 |
| confidence_threshold | 自动回答最低可信阈值 |

一个 Inbox 同时可以配置主要 Assistant 和明确的备用处理方式，但不能让多个 Assistant 对同一客户消息重复发送答案。

### 4.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/captain/assistants | 查询或创建 Assistant |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id} | 详情、更新或删除 |
| GET、POST | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/inboxes | 查询或绑定 Inbox |
| DELETE | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/inboxes/{inbox_id} | 解除绑定 |
| POST | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/playground | 使用隔离会话测试 |

## 5. Document 与 FAQ

### 5.1 Document 来源

- 直接文本；
- 网页 URL；
- PDF；
- 允许的办公文档；
- 已发布帮助中心文章；
- 经过授权的外部知识源。

### 5.2 Document 字段

| 字段 | 含义 |
|---|---|
| id | 文档标识 |
| assistant_id | 所属 Assistant |
| name | 名称 |
| source_type | text、url、pdf、file、portal、external |
| external_link | 来源地址，可为空 |
| file | 上传文件，可为空 |
| status | pending、processing、ready、failed、stale、deleted |
| version | 当前处理版本 |
| checksum | 内容变化判定摘要 |
| chunk_count | 已处理知识片段数 |
| last_synced_at | 最近同步时间 |
| error | 失败分类和可处理信息 |
| created_at、updated_at | 时间 |

### 5.3 FAQ 字段

| 字段 | 含义 |
|---|---|
| id | FAQ 标识 |
| assistant_id | 所属 Assistant |
| question | 问题 |
| answer | 答案 |
| status | draft、approved、published、archived |
| source_document_ids | 来源文档 |
| locale | 语言 |
| created_by | 创建来源 |
| approved_by | 审核者，可为空 |
| created_at、updated_at | 时间 |

### 5.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/captain/documents | 查询或创建 Document |
| GET、DELETE | /api/v1/accounts/{account_id}/captain/documents/{document_id} | 详情或删除 |
| POST | /api/v1/accounts/{account_id}/captain/documents/{document_id}/sync | 重新同步 |
| GET、POST | /api/v1/accounts/{account_id}/captain/assistant_responses | 查询或创建 FAQ |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/captain/assistant_responses/{response_id} | 维护 FAQ |
| POST | /api/v1/accounts/{account_id}/captain/bulk_actions | 批量管理 FAQ、Document 等 Captain 资源 |

Document 删除后不得继续参与新检索；历史 AI 回复保留当时引用快照。同步任务重复提交只能保留一个有效处理版本。

## 6. Scenario

Scenario 描述 Assistant 在特定业务目标下如何对话、使用知识和调用工具。

| 字段 | 含义 |
|---|---|
| id | Scenario 标识 |
| assistant_id | 所属 Assistant |
| title | 名称 |
| description | 说明 |
| trigger | 触发条件 |
| instruction | 业务指令 |
| tools | 允许工具列表 |
| required_information | 执行前必须收集的信息 |
| success_condition | 完成条件 |
| failure_action | 失败时继续询问、停止或转人工 |
| enabled | 是否启用 |
| version | 当前版本 |

接口：GET、POST `/api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/scenarios`；GET、PATCH、DELETE `/api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/scenarios/{scenario_id}`。

## 7. Copilot 与 AI Task

### 7.1 Copilot 功能

- 会话摘要；
- 建议回复；
- 改写、扩写、缩写和调整语气；
- 翻译；
- 提取客户意图、关键信息和待办；
- 根据知识库回答客服问题；
- 生成工单或客户备注草稿；
- 对历史会话进行结构化总结。

Copilot 默认只向当前有权访问会话的客服展示，不直接把内容发送给客户。只有明确确认后的公开回复才进入 Conversation Message。

### 7.2 Copilot Thread 字段

| 字段 | 含义 |
|---|---|
| id | Thread 标识 |
| assistant_id | 使用的 Assistant |
| conversation_id | 关联会话，可为空 |
| user_id | 发起用户 |
| status | active、completed、failed、archived |
| messages | Copilot 消息摘要或独立查询入口 |
| created_at、updated_at | 时间 |

### 7.3 AI Task 字段

| 字段 | 含义 |
|---|---|
| id | 任务标识 |
| operation | summarize、rewrite、expand、shorten、translate、reply_suggestion、extract |
| content | 输入内容 |
| conversation_display_id | 可选会话 |
| model_id | 实际模型 |
| status | pending、processing、completed、failed、cancelled |
| result | 完成结果 |
| usage | 输入、输出和费用计量 |
| error | 失败信息 |

### 7.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/captain/copilot_threads | 查询或创建 Thread |
| GET、POST | /api/v1/accounts/{account_id}/captain/copilot_threads/{thread_id}/copilot_messages | 查询或发送 Copilot 消息 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/rewrite | 改写文本 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/summarize | 总结文本或会话 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/reply_suggestion | 生成回复建议 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/label_suggestion | 生成标签建议 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/follow_up | 生成跟进建议 |

## 8. Custom Tool

### 8.1 功能

Custom Tool 允许 Assistant 查询订单、创建工单、预约、退款申请或执行其他外部业务动作。它不是任意网络请求入口，必须明确允许地址、方法、参数、认证、确认和返回模式。

### 8.2 字段

| 字段 | 含义 | 规则 |
|---|---|---|
| id | Tool 标识 | Account 内唯一 |
| name | 工具名称 | Assistant 使用的稳定名称 |
| description | 使用场景 | 必须足够明确，避免误调用 |
| endpoint_url | 调用地址 | 必须符合允许域名策略 |
| http_method | 方法 | GET、POST、PUT、PATCH、DELETE |
| auth_type | 认证 | none、api_key、basic、bearer、oauth |
| auth_config | 认证配置 | 敏感字段不得完整返回 |
| param_schema | 参数模式 | 定义名称、类型、是否必填和说明 |
| response_schema | 响应模式 | 定义可读取字段 |
| timeout_seconds | 超时时间 | 有明确上限 |
| retry_policy | 重试策略 | 只允许安全或具备幂等性的动作自动重试 |
| requires_confirmation | 是否需要人工或客户确认 | 产生重要副作用时必须启用 |
| allowed_assistant_ids | 可用 Assistant | 必须同 Account |
| enabled | 是否启用 | 关闭后停止新调用 |

### 8.3 调用状态

pending_confirmation、queued、running、succeeded、failed、timed_out、cancelled。

每次调用必须记录 tool_id、assistant_id、conversation_id、input、masked_auth、status、attempt、external_request_id、result_summary、error、started_at 和 finished_at。涉及敏感字段时，日志只保留脱敏摘要。

### 8.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/captain/custom_tools | 查询或创建 Tool |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/captain/custom_tools/{tool_id} | 详情、更新或删除 |
| POST | /api/v1/accounts/{account_id}/captain/custom_tools/test | 隔离测试 Tool 配置 |

Tool Call 的确认、取消和运行记录属于目标安全合同；Chatwoot 4.16.0 未将其列为独立公开路径，应通过 Assistant 行为、会话活动、审计和外部副作用联合验证。

## 9. AI 用量、额度和安全

### 9.1 用量字段

| 字段 | 含义 |
|---|---|
| account_id | Account |
| provider_id、model_id | Provider 和 Model |
| feature | assistant、copilot、task、embedding、tool |
| input_units | 输入计量 |
| output_units | 输出计量 |
| request_count | 请求数 |
| success_count、failure_count | 成功和失败数 |
| estimated_cost | 估算费用 |
| period_start、period_end | 统计周期 |
| quota、remaining | 配额和剩余量 |

### 9.2 安全规则

- Account 数据、知识、Thread、Tool 和用量必须完全隔离；
- Provider 凭证、客户敏感字段和 Tool 认证信息不得进入普通提示或日志；
- 文档内容和客户输入都视为不可信内容，不能覆盖系统权限和 Tool 规则；
- 高风险 Tool 必须确认，重复确认或网络未知结果不得产生重复副作用；
- Assistant 不能读取无权访问的会话、客户、工单或附件；
- Copilot 结果必须标识为 AI 建议，未经确认不得成为公开回复；
- 达到额度后停止新的计费动作，历史结果仍可读取；
- 模型不可用时按允许的 fallback 顺序处理，并记录实际使用模型；
- 模型版本变化后，关键功能重新进入待验收状态。

## 10. 插件平台对象

| 对象 | 功能 |
|---|---|
| Integration App | 应用目录中的能力声明 |
| Integration Hook | Account 对应用的一次安装或连接实例 |
| Webhook | Account 级通用事件订阅 |
| Webhook Delivery | 一次事件投递及其重试记录 |
| Dashboard App | 工作台中的受控外部页面或上下文工具 |
| Agent Bot | 通过事件和 API 参与会话处理的外部 Bot |

## 11. Integration App 与 Hook

### 11.1 App 字段

| 字段 | 含义 |
|---|---|
| id | App 标识 |
| name | 名称 |
| description | 说明 |
| category | CRM、commerce、collaboration、AI、analytics 等 |
| icon | 图标 |
| capabilities | 支持能力 |
| required_scopes | 所需权限 |
| auth_type | oauth、api_key、basic、custom |
| settings_schema | 安装配置字段 |
| webhook_events | 可订阅事件 |
| enabled | 当前实例是否允许安装 |
| version | 应用版本 |

### 11.2 Hook 字段

| 字段 | 含义 |
|---|---|
| id | 安装实例标识 |
| app_id | App |
| account_id | Account |
| inbox_id | 可选 Inbox 范围 |
| status | pending、active、degraded、reauth_required、disabled、error |
| settings | 非敏感配置 |
| credential_status | 凭证状态 |
| subscribed_events | 订阅事件 |
| last_success_at、last_error_at | 最近状态 |
| error | 可处理错误摘要 |
| created_at、updated_at | 时间 |

### 11.3 生命周期

目录查看 → 开始安装 → 授权或填写配置 → 验证 → active → 运行和健康检查 → 重新授权或更新 → disabled → uninstall。

卸载后停止新事件和动作，撤销本地凭证并保留必要审计；外部系统已经产生的对象是否删除必须由各集成单独说明。

### 11.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/integrations/apps | 查询应用目录 |
| GET | /api/v1/accounts/{account_id}/integrations/apps/{app_id} | 获取应用能力 |
| POST | /api/v1/accounts/{account_id}/integrations/hooks | 创建安装实例 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id} | 详情、更新或卸载 |
| POST | /api/v1/accounts/{account_id}/integrations/hooks/{hook_id}/process_event | 请求 Hook 处理事件 |

授权、重新授权和连接验证按具体 App 的 OAuth 或配置流程执行；不能为所有 Hook 假设一个基线中不存在的通用重新授权路径。

## 12. Webhook

### 12.1 Webhook 字段

| 字段 | 含义 |
|---|---|
| id | Webhook 标识 |
| name | 名称 |
| url | HTTPS 地址 |
| inbox_id | 可选 Inbox 范围 |
| subscriptions | 订阅事件列表 |
| secret_status | 签名密钥是否已配置 |
| active | 是否启用 |
| failure_count | 连续失败次数 |
| last_delivery_at | 最近投递时间 |
| status | active、paused、disabled |

### 12.2 Delivery 字段

| 字段 | 含义 |
|---|---|
| id | 投递标识 |
| webhook_id | Webhook |
| event_id | 业务事件标识 |
| event | 事件名称 |
| status | pending、delivering、succeeded、failed、dead |
| attempt | 尝试次数 |
| response_status | 外部响应状态 |
| response_time_ms | 响应时间 |
| next_retry_at | 下次重试时间 |
| error | 失败摘要 |
| created_at、updated_at | 时间 |

### 12.3 Payload 公共字段

event、event_id、account、resource、actor、changed_fields、occurred_at、delivery_id 和 schema_version。

签名至少绑定请求正文、时间和 Webhook Secret。接收方可以使用 event_id 去重；时间超出允许窗口或签名错误的请求应拒绝。

### 12.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/webhooks | 查询或创建 Webhook |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/webhooks/{webhook_id} | 详情、更新或删除 |

Secret 更新、测试、Delivery 查询和人工重放属于补齐后的运行管理要求；Chatwoot 4.16.0 基线公开路径只规定 Webhook 列表、创建、更新和删除，不应把这些运行要求记为基线 API。

## 13. Dashboard App 与 Agent Bot

### 13.1 Dashboard App

Dashboard App 可以在客服工作台显示订单、会员、物流或其他上下文，但必须限制可访问域名、会话上下文、用户权限和消息通信范围。

主要字段：id、title、content.type、content.url、allowed_origins、scopes、enabled、created_at、updated_at。

接口：GET、POST `/api/v1/accounts/{account_id}/dashboard_apps`；GET、PATCH、DELETE `/api/v1/accounts/{account_id}/dashboard_apps/{app_id}`。

### 13.2 Agent Bot

| 字段 | 含义 |
|---|---|
| id | Bot 标识 |
| name | 名称 |
| description | 说明 |
| outgoing_url | 事件接收地址 |
| bot_type | webhook、integration、assistant_bridge |
| status | active、inactive、error |
| inbox_ids | 绑定 Inbox |
| subscribed_events | 订阅事件 |
| created_at、updated_at | 时间 |

Agent Bot 必须遵守会话分配、pending、人工接管和幂等规则。人工接管后，Bot 不得继续发送基于旧上下文的过期回复。

接口：GET、POST `/api/v1/accounts/{account_id}/agent_bots`；GET、PATCH、PUT、DELETE `/api/v1/accounts/{account_id}/agent_bots/{bot_id}`。

## 14. 标准集成范围

至少为以下类别提供独立授权、字段、状态和卸载说明：

- Slack 或 Teams 协作；
- Shopify 等电商订单；
- Linear 等问题管理；
- Notion 等知识来源；
- Dialogflow 或其他机器人；
- Google Translate 或其他翻译；
- CRM；
- 视频会议；
- 自定义 Webhook 和 Dashboard App。

每个集成必须说明外部标识、Account/Inbox 范围、授权 Scope、Token 过期、重复回调、限流、删除差异和历史兼容。

## 15. 验收清单

### 15.1 AI

- Provider 凭证、Model 列表、健康检查、主模型和备用模型形成闭环；
- Assistant、Inbox Binding、Scenario、Document、FAQ 和 Tool 均受 Account 权限限制；
- Document 对文本、URL、PDF、失败、更新、删除和重新同步有明确终态；
- Copilot 的摘要、改写、翻译和建议回复不会未经确认直接发送；
- Custom Tool 的参数、认证、确认、超时、重试和未知结果不会产生重复副作用；
- 达到额度、模型不可用、模型弃用和 Provider 失效均有可恢复状态；
- 人工接管后 Assistant 停止发送过期回答；
- 所有 AI 结果记录实际 Provider、Model、版本、引用和用量。

### 15.2 插件和 Webhook

- App 目录、安装、授权、验证、运行、重新授权、停用和卸载形成闭环；
- Hook、Webhook、Dashboard App 和 Agent Bot 的权限范围不会越过 Account 或 Inbox；
- Webhook Payload、签名、重试、暂停、恢复和人工重放可验证；
- 同一 event_id 重放不会产生重复消息、工单或外部对象；
- 第三方 401、403、404、409、429、5xx、超时和断网有明确状态；
- 卸载后凭证撤销、事件停止、历史保留和外部残留边界可确认；
- 插件版本或 Scope 变化后，相关能力重新进入待验收状态。
