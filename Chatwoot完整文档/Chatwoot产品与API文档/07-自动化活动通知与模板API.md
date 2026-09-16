# 自动化、活动、通知与模板 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

自动化和 Macro 在目标架构中的模块边界、统一动作能力、执行跟踪、防循环和量化规则见：[自动化规则、宏与统一动作能力合同](../Chatwoot全系统基础层规范/10-自动化规则宏与统一动作能力合同.md)。

## 1. 功能范围

本模块用于自动执行会话动作、保存可重复使用的操作组合、维护快捷回复、发送主动活动，以及管理个人通知。

## 2. Automation Rule API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/automation_rules | 规则列表 | Administrator |
| POST | /api/v1/accounts/{account_id}/automation_rules | 创建规则 | Administrator |
| GET | /api/v1/accounts/{account_id}/automation_rules/{id} | 规则详情 | Administrator |
| PATCH | /api/v1/accounts/{account_id}/automation_rules/{id} | 更新规则 | Administrator |
| DELETE | /api/v1/accounts/{account_id}/automation_rules/{id} | 删除规则 | Administrator |
| POST | /api/v1/accounts/{account_id}/automation_rules/{id}/clone | 复制规则 | Administrator |

### 2.1 Rule 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| id | integer | 响应 | Rule ID |
| name | string | 是 | 规则名称 |
| description | string 或 null | 否 | 规则说明 |
| event_name | enum | 是 | 触发事件 |
| active | boolean | 否 | 是否启用 |
| conditions | array | 是 | 条件列表 |
| actions | array | 是 | 动作列表 |
| account_id | integer | 响应 | 所属 Account |
| created_at | time | 响应 | 创建时间 |

### 2.2 Condition 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| attribute_key | string | 条件字段 |
| filter_operator | enum | 比较方式 |
| values | array | 比较值 |
| query_operator | enum 或 null | 与前一条件的 AND/OR 关系，按接口格式使用 |

常见 filter_operator：equal_to、not_equal_to、contains、does_not_contain、starts_with、is_present、is_not_present、is_greater_than、is_less_than、days_before。attribute_changed 主要用于已有兼容规则，当前普通创建接口不建议新增。

### 2.3 Action 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| action_name | enum | 动作名称 |
| action_params | array 或 object | 动作参数 |

常见动作：assign_agent、assign_team、add_label、remove_label、send_message、send_email_to_team、send_email_transcript、add_private_note、change_priority、mute_conversation、snooze_conversation、resolve_conversation、send_webhook_event。

### 2.4 Event 枚举

| 事件 | 含义 |
|---|---|
| conversation_created | 创建会话 |
| conversation_updated | 会话内容或属性更新 |
| conversation_opened | 会话进入 open |
| conversation_resolved | 会话解决 |
| message_created | 创建消息 |
当前 Automation Rule 仅使用以上 5 个事件。contact_created 和 contact_updated 属于 Webhook 事件，不属于当前自动化触发事件。

## 3. Macro API

Macro 是由人工主动执行的一组预定义动作。

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/macros | Macro 列表 |
| POST | /api/v1/accounts/{account_id}/macros | 创建 Macro |
| GET | /api/v1/accounts/{account_id}/macros/{id} | Macro 详情 |
| PATCH | /api/v1/accounts/{account_id}/macros/{id} | 更新 Macro |
| DELETE | /api/v1/accounts/{account_id}/macros/{id} | 删除 Macro |
| POST | /api/v1/accounts/{account_id}/macros/{id}/execute | 对会话执行 Macro |

### 3.1 Macro 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Macro ID |
| name | string | 名称 |
| actions | array | 动作列表 |
| visibility | enum | personal 或 global，按账号功能提供 |
| created_by | object | 创建人摘要 |

### 3.2 Execute 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| conversation_ids | integer array | 是 | 目标会话列表 |
| custom_params | object | 否 | Macro 支持的附加参数 |

执行前应重新确认会话状态。批量执行可能部分成功，结果应按每个会话确认。

## 4. Canned Response API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/canned_responses | 快捷回复列表 |
| POST | /api/v1/accounts/{account_id}/canned_responses | 创建快捷回复 |
| PATCH | /api/v1/accounts/{account_id}/canned_responses/{id} | 更新快捷回复 |
| DELETE | /api/v1/accounts/{account_id}/canned_responses/{id} | 删除快捷回复 |

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| id | integer | 响应 | 快捷回复 ID |
| short_code | string | 是 | 唯一短代码 |
| content | string | 是 | 回复内容 |
| account_id | integer | 响应 | 所属 Account |
| created_at | time | 响应 | 创建时间 |

short_code 在 Account 内应保持唯一。快捷回复只是内容模板，发送前仍应由使用者确认收件人和上下文。

## 5. Campaign API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/campaigns | 活动列表 | 有活动权限的成员 |
| POST | /api/v1/accounts/{account_id}/campaigns | 创建活动 | Administrator 或允许角色 |
| GET | /api/v1/accounts/{account_id}/campaigns/{id} | 活动详情 | 有活动权限的成员 |
| PATCH | /api/v1/accounts/{account_id}/campaigns/{id} | 更新活动 | Administrator 或允许角色 |
| DELETE | /api/v1/accounts/{account_id}/campaigns/{id} | 删除活动 | Administrator 或允许角色 |

### 5.1 Campaign 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Campaign ID |
| title | string | 活动名称 |
| description | string 或 null | 活动说明 |
| campaign_type | enum | ongoing 或 one_off |
| message | string | 消息内容 |
| inbox_id | integer | 目标 Inbox |
| enabled | boolean | 是否启用 |
| scheduled_at | time 或 null | 计划发送时间 |
| sender_id | integer 或 null | 发送人 |
| audience | array 或 object | 目标条件 |
| trigger_rules | object | 持续活动触发规则 |
| created_at | time | 创建时间 |

### 5.2 Campaign 规则

- one_off 按目标集合和计划时间发送一次；
- ongoing 在满足 Widget 或客户条件时触发；
- Inbox 必须支持相应消息类型；
- 主动消息应满足渠道窗口、模板和用户同意规则；
- 停用活动只影响后续触发，不代表撤回已发送消息。

## 6. Notification API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/notifications | 通知列表 |
| PATCH | /api/v1/accounts/{account_id}/notifications/{id} | 更新通知状态 |
| DELETE | /api/v1/accounts/{account_id}/notifications/{id} | 删除通知 |
| POST | /api/v1/accounts/{account_id}/notifications/read_all | 全部标记已读 |
| GET | /api/v1/accounts/{account_id}/notifications/unread_count | 未读数量 |
| POST | /api/v1/accounts/{account_id}/notifications/destroy_all | 删除全部通知 |
| POST | /api/v1/accounts/{account_id}/notifications/{id}/snooze | 延后通知 |
| POST | /api/v1/accounts/{account_id}/notifications/{id}/unread | 标记未读 |

### 6.1 Notification 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Notification ID |
| notification_type | string | 通知类型 |
| primary_actor | object 或 null | 主要触发对象 |
| secondary_actor | object 或 null | 相关对象 |
| meta | object | 会话、消息或业务元数据 |
| read_at | time 或 null | 已读时间 |
| snoozed_until | time 或 null | 延后时间 |
| created_at | time | 创建时间 |

## 7. Notification Settings API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/notification_settings | 当前用户通知偏好 |
| PATCH | /api/v1/accounts/{account_id}/notification_settings | 更新偏好 |

常见设置字段按通知类型区分 email、push 和 dashboard。更新时只提交需要改变的字段，避免覆盖目标版本新增设置。

## 8. 业务边界

- 自动化规则由系统事件触发，Macro 由人工主动执行；
- 条件和动作只能使用允许的字段与枚举；
- 规则启用前应使用样本会话检查影响范围；
- Webhook 动作可能向外部发送客户数据，应限制目标地址和接收权限；
- Campaign 必须遵守渠道模板、时间窗口和客户同意要求；
- 通知属于当前用户，不得读取其他用户的通知；
- 重复事件不应造成重复发送、重复标签或重复外部操作。
- 自动化和 Macro 必须复用 Action Capability，目标动作与手工入口进入同一业务 Command；
- AutomationExecution 和 MacroExecution 必须能查询逐动作、逐目标状态、change_id 和失败原因。

完整条件字段、操作符、事件动作矩阵和参数详见：[自动化条件、操作符与动作字典](18-自动化条件操作符与动作字典.md)。
