# Captain、Copilot 与 AI API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

Captain 提供面向客户的 AI Assistant、知识库、FAQ、Scenario 和工具；Copilot 面向 Account 成员提供会话辅助。相关能力通常需要 Enterprise、Account Feature、模型凭证和可用额度。

## 2. AI Preferences API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/preferences | 获取 Provider、Model 和 Feature 偏好 | Account 成员 |
| PATCH | /api/v1/accounts/{account_id}/captain/preferences | 更新模型和功能偏好 | Administrator |

### 2.1 Preference 请求字段

| 字段 | 类型 | 说明 |
|---|---|---|
| captain_models | object | Feature 到 Model ID 的映射 |
| captain_features | object | Feature 到启用状态的映射 |

当前 Feature：editor、assistant、copilot、label_suggestion、document_faq_generation、conversation_faq_generation、pdf_faq_generation、help_center_article_generation、onboarding_content_generation、help_center_query_translation、audio_transcription、help_center_search。

### 2.2 Preference 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| providers | object | Provider Key 到展示信息的目录 |
| models | object | Model ID 到模型元数据的目录 |
| features | object | 各 Feature 的模型和启用状态 |
| features.{key}.models | array | 该 Feature 可选模型 |
| features.{key}.default | string | 默认模型 |
| features.{key}.enabled | boolean | 是否启用 |
| features.{key}.selected | string | 当前选择 |
| features.{key}.provider | string | 当前 Provider |
| features.{key}.source | enum | default 或 account_override |

## 3. AI Task API

所有路径位于 /api/v1/accounts/{account_id}/captain/tasks 下。

| 方法 | 路径 | 功能 | 请求字段 |
|---|---|---|---|
| POST | /rewrite | 改写文本 | content、operation、conversation_display_id 可选 |
| POST | /summarize | 总结会话 | conversation_display_id |
| POST | /reply_suggestion | 生成回复建议 | conversation_display_id |
| POST | /label_suggestion | 生成标签建议 | conversation_display_id |
| POST | /follow_up | 继续 AI 任务 | follow_up_context、message、conversation_display_id 可选 |

### 3.1 Rewrite operation

| 值 | 含义 |
|---|---|
| improve | 改善表达 |
| fix_spelling_grammar | 修正拼写和语法 |
| make_friendly | 更友好 |
| make_formal | 更正式 |
| simplify | 简化 |
| expand | 扩写 |
| shorten | 缩短 |
| change_tone | 按产品支持的语气调整 |

具体可选值以界面或接口返回为准。

### 3.2 Task 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| message | string | 生成文本 |
| follow_up_context | object 或 null | 后续任务上下文 |
| error | string 或 null | 失败说明 |

Task 失败通常表现为业务校验错误，使用方应展示 error，不应自动把失败文本作为客户回复。

## 4. Assistant API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/assistants | Assistant 列表 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/captain/assistants | 创建 Assistant | Administrator |
| GET | /api/v1/accounts/{account_id}/captain/assistants/{id} | Assistant 详情 | Account 成员 |
| PATCH | /api/v1/accounts/{account_id}/captain/assistants/{id} | 更新 Assistant | Administrator |
| DELETE | /api/v1/accounts/{account_id}/captain/assistants/{id} | 删除 Assistant | Administrator |
| POST | /api/v1/accounts/{account_id}/captain/assistants/{id}/playground | 测试对话 | Account 成员 |
| GET | /api/v1/accounts/{account_id}/captain/assistants/{id}/stats | 统计 | Account 成员 |
| GET | /api/v1/accounts/{account_id}/captain/assistants/{id}/summary | AI 统计总结 | Account 成员 |
| GET | /api/v1/accounts/{account_id}/captain/assistants/{id}/drilldown | 指标明细 | Administrator |
| GET | /api/v1/accounts/{account_id}/captain/assistants/tools | 可用工具 | Administrator |

### 4.1 Assistant 字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| id | integer | 否 | Assistant ID |
| name | string | 是 | 名称 |
| description | string 或 null | 是 | 业务说明 |
| config | object | 是 | 功能配置 |
| response_guidelines | array | 是 | 回复要求 |
| guardrails | array | 是 | 禁止事项和安全约束 |
| account_id | integer | 否 | 所属 Account |
| created_at | time | 否 | 创建时间 |
| updated_at | time | 否 | 更新时间 |

### 4.2 config 常用字段

| 字段 | 类型 | 说明 |
|---|---|---|
| product_name | string | 产品或服务名称 |
| instructions | string | Assistant 业务说明 |
| temperature | number | 生成随机度，范围由产品限制 |
| feature_faq | boolean | 会话结束后生成 FAQ 候选 |
| feature_memory | boolean | 生成联系人记忆 |
| feature_citation | boolean | 回复中提供知识引用 |
| feature_contact_attributes | boolean | 联系人属性相关能力；实际可用性按版本确认 |
| welcome_message | string 或 null | 欢迎消息 |
| handoff_message | string 或 null | 转人工消息 |
| resolution_message | string 或 null | 结束消息 |

## 5. Assistant Inbox API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/inboxes | 已绑定 Inbox |
| POST | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/inboxes | 绑定 Inbox |
| DELETE | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/inboxes/{inbox_id} | 解绑 Inbox |
| GET | /api/v1/accounts/{account_id}/conversations/{conversation_id}/inbox_assistant | 获取当前会话 Inbox 绑定的 Assistant |

| 字段 | 类型 | 说明 |
|---|---|---|
| inbox_id | integer | Inbox ID |
| assistant_id | integer | Assistant ID，通常由路径提供 |

一个 Inbox 同一时间只能绑定一个 Captain Assistant。

inbox_assistant 响应字段为 assistant；已绑定时包含 id 和 name，未绑定时为 null。

## 6. Scenario API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/scenarios | 列表或创建 |
| GET/PATCH/DELETE | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/scenarios/{id} | 查看、更新或删除 |

### 6.1 Scenario 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Scenario ID |
| title | string | 名称 |
| description | string 或 null | 说明 |
| instruction | string | 业务处理要求 |
| enabled | boolean | 是否启用 |
| tools | array | 允许使用的工具 |
| assistant_id | integer | Assistant ID |

Scenario 只可使用当前 Assistant 允许且已启用的工具。

## 7. Assistant Response API

Assistant Response 表示可审核的 FAQ 问答。

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/assistant_responses | FAQ 列表 |
| POST | /api/v1/accounts/{account_id}/captain/assistant_responses | 创建 FAQ |
| GET | /api/v1/accounts/{account_id}/captain/assistant_responses/{id} | FAQ 详情 |
| PATCH | /api/v1/accounts/{account_id}/captain/assistant_responses/{id} | 更新 FAQ |
| DELETE | /api/v1/accounts/{account_id}/captain/assistant_responses/{id} | 删除 FAQ |

### 7.1 FAQ 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | FAQ ID |
| question | string | 问题 |
| answer | string | 答案 |
| status | enum | pending、approved |
| assistant_id | integer | Assistant ID |
| document_id | integer 或 null | 来源 Document，按响应提供 |
| conversation_id | integer 或 null | 来源会话，按响应提供 |
| created_at | time | 创建时间 |

只有 approved FAQ 应参与正式回答。会话自动生成的 FAQ 通常先进入 pending。

## 8. Document API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/documents | 文档列表 |
| POST | /api/v1/accounts/{account_id}/captain/documents | 创建网页或 PDF 文档 |
| GET | /api/v1/accounts/{account_id}/captain/documents/{id} | 文档详情 |
| DELETE | /api/v1/accounts/{account_id}/captain/documents/{id} | 删除文档 |
| POST | /api/v1/accounts/{account_id}/captain/documents/{id}/sync | 手动同步网页文档 |

### 8.1 Document 创建字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| assistant_id | integer | 是 | Assistant ID |
| name | string | 否 | 文档名称 |
| external_link | string | 网页文档是 | 网页地址 |
| pdf_file | file | PDF 文档是 | PDF 文件，大小受产品限制 |

### 8.2 Document 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Document ID |
| name | string | 名称 |
| external_link | string | 来源地址或 PDF 标识 |
| status | enum | in_progress、available |
| sync_status | enum 或 null | syncing、synced、failed |
| sync_step | string 或 null | 当前同步阶段 |
| last_synced_at | time 或 null | 最后成功同步时间 |
| last_sync_attempted_at | time 或 null | 最后尝试时间 |
| last_sync_error_code | string 或 null | 同步错误代码 |
| responses_count | integer | 生成的 FAQ 数量，按列表提供 |
| assistant_id | integer | Assistant ID |

sync 返回 202 表示已接受处理，不表示内容已经更新完成。PDF 通常不支持网页式同步。

## 9. Bulk Action API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/captain/bulk_actions | 批量管理 FAQ 或 Document |

| 字段 | 类型 | 说明 |
|---|---|---|
| type | enum | AssistantResponse 或 AssistantDocument |
| ids | integer array | 资源 ID |
| fields.status | enum | approve、delete、sync |

允许组合：FAQ approve/delete，Document delete/sync。响应中的 count 或 ids 表示已接受或已处理的资源范围。

## 10. Message Report API（Cloud）

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/captain/message_reports | 举报 Captain 回答 |

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| message_id | integer | 是 | Captain Message ID |
| report_reason | enum | 是 | 举报原因 |
| description | string | 否 | 补充说明 |

report_reason：incorrect_information、inappropriate_response、incomplete_response、outdated_information、other。

该接口记录质量反馈，不代表自动修改知识或模型。

## 11. Copilot API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/copilot_threads | 当前用户 Thread 列表 |
| POST | /api/v1/accounts/{account_id}/captain/copilot_threads | 创建 Thread |
| GET | /api/v1/accounts/{account_id}/captain/copilot_threads/{thread_id}/copilot_messages | Thread 消息 |
| POST | /api/v1/accounts/{account_id}/captain/copilot_threads/{thread_id}/copilot_messages | 追加问题 |

### 11.1 Thread 创建字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| message | string | 是 | 首条问题 |
| assistant_id | integer | 是 | Assistant ID |
| conversation_id | integer | 否 | 当前会话上下文 |

### 11.2 Copilot Message 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Message ID |
| content | string | 消息正文 |
| reasoning | string 或 null | 可展示的过程说明，按配置提供 |
| function_name | string 或 null | 使用的功能名称 |
| reply_suggestion | string 或 null | 建议回复 |
| message_type | string | 用户或 Copilot 消息类型 |
| created_at | time | 创建时间 |

Thread 只属于创建它的当前用户，其他 Account 成员不应读取该历史。

## 12. Custom Tool API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/custom_tools | 工具列表 |
| POST | /api/v1/accounts/{account_id}/captain/custom_tools | 创建工具 |
| GET | /api/v1/accounts/{account_id}/captain/custom_tools/{id} | 工具详情 |
| PATCH | /api/v1/accounts/{account_id}/captain/custom_tools/{id} | 更新工具 |
| DELETE | /api/v1/accounts/{account_id}/captain/custom_tools/{id} | 删除工具 |
| POST | /api/v1/accounts/{account_id}/captain/custom_tools/test | 测试工具连接 |

### 12.1 Custom Tool 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Tool ID |
| title | string | 名称 |
| description | string | 使用说明 |
| endpoint_url | string | HTTPS 目标地址 |
| http_method | enum | GET 或 POST |
| request_template | string 或 null | 请求内容模板 |
| response_template | string 或 null | 响应内容提取模板 |
| auth_type | enum | none、bearer、basic、api_key |
| auth_config | object | 认证配置；敏感值不应回显 |
| param_schema | array | 参数定义 |
| enabled | boolean | 是否启用 |

### 12.2 Parameter 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| name | string | 参数名，必填 |
| description | string | 参数用途，必填 |
| type | string | 参数的业务类型说明，必填 |
| required | boolean | 是否必填 |

测试接口会真实访问 endpoint_url。测试成功不代表工具在所有会话、权限和参数下均可安全使用。

## 13. AI 业务规则

- 模型、Feature、额度和凭证都可影响功能可用性；
- Assistant、Document、FAQ、Scenario 和 Tool 必须属于同一 Account；
- 自动回答只应在符合 Inbox 和会话状态的情况下运行；
- 人工接管后不得发送过期 AI 回答；
- 未批准 FAQ 不用于正式知识回答；
- 文档创建和同步是异步过程；
- 无可靠答案、模型失败或工具失败时应转人工；
- PDF 处理能力可能与普通文本模型能力不同；
- Custom Tool 返回内容不代表可信事实，关键操作仍需要业务确认；
- Copilot 建议必须由使用者确认后再发送给客户。

当前 Provider、模型目录、12 个 Feature、默认值、可选模型、凭证和失败回退详见：[AI 模型、Provider 与 Feature 能力矩阵](22-AI模型Provider与Feature能力矩阵.md)。
