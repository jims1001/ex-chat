# 分配策略、筛选、导入与批量操作 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

本模块补充会话分配策略、保存筛选条件、联系人和会话批量操作、Intercom 数据导入、会话草稿、直传附件以及帮助中心批量操作。这些能力用于提高大量数据和大量会话场景下的管理效率。

## 2. Assignment Policy API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/assignment_policies | 策略列表 |
| POST | /api/v1/accounts/{account_id}/assignment_policies | 创建策略 |
| GET | /api/v1/accounts/{account_id}/assignment_policies/{id} | 策略详情 |
| PATCH | /api/v1/accounts/{account_id}/assignment_policies/{id} | 更新策略 |
| DELETE | /api/v1/accounts/{account_id}/assignment_policies/{id} | 删除策略 |
| GET | /api/v1/accounts/{account_id}/assignment_policies/{id}/inboxes | 使用该策略的 Inbox |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignment_policy | 查看 Inbox 当前策略 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignment_policy | 为 Inbox 绑定策略 |
| DELETE | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignment_policy | 解除 Inbox 策略 |

### 2.1 Assignment Policy 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 策略 ID |
| name | string | 策略名称，同一 Account 中唯一 |
| description | string 或 null | 策略说明 |
| assignment_order | enum | round_robin；Enterprise 还可提供 balanced |
| conversation_priority | enum | earliest_created 或 longest_waiting |
| fair_distribution_limit | integer | 公平分配数量上限，必须大于 0 |
| fair_distribution_window | integer | 公平分配统计窗口，必须大于 0 |
| exclude_older_than_hours | integer 或 null | 排除过旧会话的小时数 |
| enabled | boolean | 是否启用 |
| assigned_inbox_count | integer | 已绑定 Inbox 数量 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

### 2.2 Inbox 绑定字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| assignment_policy_id | integer | 是 | 需要绑定的策略 ID |
| inbox_id | integer | 路径 | Inbox ID |

一个 Inbox 同一时间只能绑定一个 Assignment Policy。绑定新策略会替换旧策略。策略、Inbox 和成员必须属于同一 Account。

## 3. Assignment Policy 与 Capacity Policy 的区别

| 能力 | Assignment Policy | Agent Capacity Policy |
|---|---|---|
| 解决问题 | 决定会话如何选择成员 | 限制成员最多承担多少会话 |
| 主要维度 | 分配顺序、会话优先级、公平窗口 | User、Inbox、conversation_limit |
| 绑定对象 | Inbox | User 和 Inbox Limit |
| 达到限制后的结果 | 根据策略选择其他候选成员 | 可能阻止继续分配 |

实际分配同时受到 Inbox 成员关系、成员在线状态、容量策略和分配策略影响。

## 4. Custom Filter API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/custom_filters | 当前用户保存的筛选列表 |
| POST | /api/v1/accounts/{account_id}/custom_filters | 保存筛选 |
| GET | /api/v1/accounts/{account_id}/custom_filters/{id} | 筛选详情 |
| PATCH | /api/v1/accounts/{account_id}/custom_filters/{id} | 更新筛选 |
| DELETE | /api/v1/accounts/{account_id}/custom_filters/{id} | 删除筛选 |

### 4.1 Custom Filter 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Filter ID |
| name | string | 显示名称 |
| filter_type | enum | conversation、contact、report |
| query | object | 筛选条件对象 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

列表接口可通过 filter_type 只读取对应类型。保存的筛选属于创建它的当前用户，不是整个 Account 的公共筛选。

### 4.2 query 通用结构

| 字段 | 类型 | 说明 |
|---|---|---|
| query.payload | array | 条件列表 |
| query.payload[].attribute_key | string | 筛选字段 |
| query.payload[].attribute_model | string 或 null | standard 或自定义字段所属对象 |
| query.payload[].filter_operator | string | 比较方式 |
| query.payload[].values | array | 比较值 |
| query.payload[].query_operator | enum 或 null | and 或 or，与下一条件的关系 |
| query.payload[].custom_attribute_type | string 或 null | 自定义字段类型 |

可用字段、操作符和值类型取决于 filter_type 和目标字段定义。读取时应保留服务端返回的完整 query 对象。

## 5. Account Bulk Action API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/bulk_actions | 对 Conversation 或 Contact 执行批量操作 |

### 5.1 公共请求字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| type | enum | 是 | Conversation 或 Contact |
| ids | integer array | 是 | Conversation 使用 display_id，Contact 使用 Contact ID |
| action_name | string | 按操作 | Contact 删除时使用 delete |
| labels.add | string array | 否 | 批量添加标签 |
| labels.remove | string array | 否 | 批量移除标签 |

### 5.2 Conversation 批量字段

| 字段 | 类型 | 说明 |
|---|---|---|
| fields.status | enum | open、resolved、pending、snoozed |
| fields.assignee_id | integer 或 null | 分配或取消 Agent |
| fields.team_id | integer、0 或 null | 分配 Team；0 或 null 用于取消，按接口结果确认 |
| snoozed_until | time 或 null | status 为 snoozed 时的恢复时间 |

### 5.3 执行结果

批量接口成功表示已接受处理，不代表所有对象已经完成更新。应重新读取资源或等待对应实时事件。无权访问、已删除或不属于当前 Account 的对象不会被当作可操作对象。

Contact 删除需要更高权限；标签添加和移除只对当前 Account 已存在的标签生效。

## 6. Data Import API

当前批量导入接口用于受 Feature 控制的 Intercom 数据迁移。

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/data_imports | 导入任务列表 |
| POST | /api/v1/accounts/{account_id}/data_imports/validate_source | 验证来源凭证并统计可导入数量 |
| POST | /api/v1/accounts/{account_id}/data_imports | 创建导入任务 |
| GET | /api/v1/accounts/{account_id}/data_imports/{id} | 导入详情和错误 |
| POST | /api/v1/accounts/{account_id}/data_imports/{id}/start | 重新开始可恢复任务 |
| POST | /api/v1/accounts/{account_id}/data_imports/{id}/abandon | 放弃进行中的任务 |
| GET | /api/v1/accounts/{account_id}/data_imports/{id}/error_logs | 下载错误记录 |
| GET | /api/v1/accounts/{account_id}/data_imports/{id}/skip_logs | 下载跳过记录 |

### 6.1 创建与验证字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| name | string | 否 | 导入任务名称 |
| source_provider | enum | 是 | 当前为 intercom |
| access_token | string | 是 | 来源系统 Access Token |
| import_types | string array | 否 | contacts、conversations；省略时使用默认范围 |

### 6.2 Source Validation 响应

| 字段 | 类型 | 说明 |
|---|---|---|
| valid | boolean | 来源凭证和导入范围是否有效 |
| totals | object | 各类可导入对象数量 |
| message | string 或 null | 验证失败原因 |

### 6.3 Data Import 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 导入任务 ID |
| name | string 或 null | 名称 |
| data_type | string | 导入数据类型 |
| source_type | string 或 null | 来源类型 |
| source_provider | string | 来源系统 |
| import_types | string array | 导入范围 |
| status | enum | pending、processing、completed、failed、completed_with_errors、abandoned |
| total_records | integer 或 null | 总记录数 |
| processed_records | integer 或 null | 已处理数 |
| stats | object | 分类统计 |
| cursor | object | 当前处理位置摘要 |
| import_errors_count | integer | 错误数量 |
| skip_logs_count | integer | 跳过数量 |
| initiated_by | object 或 null | 发起人摘要 |
| started_at | time 或 null | 开始时间 |
| completed_at | time 或 null | 完成时间 |
| abandoned_at | time 或 null | 放弃时间 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

### 6.4 导入错误字段

| 字段 | 类型 | 说明 |
|---|---|---|
| error_code | string | 错误代码 |
| message | string | 错误说明 |
| source_object_type | string | 来源对象类型 |
| source_object_id | string 或 null | 来源对象 ID |
| details | object | 补充信息 |
| kind | string 或 null | 跳过记录分类 |
| created_at | time | 记录时间 |

同一 Account 同时只允许一个活动导入任务。Access Token 不应出现在导入详情、日志或长期展示中。

## 7. Conversation Draft Message API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/conversations/{conversation_id}/draft_messages | 获取当前会话草稿 |
| PATCH | /api/v1/accounts/{account_id}/conversations/{conversation_id}/draft_messages | 保存草稿 |
| DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id}/draft_messages | 删除草稿 |

| 字段 | 类型 | 说明 |
|---|---|---|
| draft_message.message | string | 草稿内容 |
| has_draft | boolean | 是否存在草稿 |
| message | string | 已保存内容 |

草稿不等同于 Message，不会发送给客户，也不会进入普通消息列表。

## 8. Direct Upload API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/direct_uploads | 创建会话附件直传信息 |
| POST | /api/v1/accounts/{account_id}/upload | 创建账号范围上传资源 |

直传接口通常返回上传地址、签名标识和文件元数据。完成文件上传后，仍需在 Message 或对应业务对象中引用已上传资源。

## 9. Help Center Article Bulk Action API

| 方法 | 路径 | 功能 | 请求字段 |
|---|---|---|---|
| PATCH | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/update_status | 批量更新文章状态 | ids、status |
| PATCH | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/update_category | 批量移动分类 | ids、category_id |
| DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/delete_articles | 批量删除文章 | ids |
| POST | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/translate | 翻译入口 | 当前版本未提供完成结果 |

portal_id 使用 Portal slug。category_id 必须属于同一 Portal。批量删除不可依赖列表中的旧缓存，应在操作后重新读取文章列表。

## 10. 批量与异步规则

- 批量操作前应固定目标 ID 集合，避免筛选条件变化影响范围；
- 导入、批量更新和附件处理可能异步完成；
- 操作成功、任务完成和全部对象成功是不同状态；
- 大批量结果应通过任务状态、资源重查或实时事件确认；
- 错误日志可能包含客户业务数据，应按敏感数据管理；
- 删除、放弃和覆盖策略前应确认目标 Account、Inbox 和资源 ID。
