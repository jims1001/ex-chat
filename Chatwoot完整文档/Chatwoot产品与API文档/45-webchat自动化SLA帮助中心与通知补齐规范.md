# webchat 自动化、SLA、帮助中心与通知补齐规范

> 对照基线：03、07、08、14、18、20、28、31、33、36、39 和 40 分册
> 目标：补齐 webchat 当前缺少的通用自动化、Macro、Campaign、通知、Assignment Policy、SLA 和公开帮助中心。
> 边界：本文只描述功能、业务规则、接口、字段、状态和验收，不包含代码实现。

## 1. 补齐范围

| 模块 | webchat 已有能力 | 需要补齐的完整能力 |
|---|---|---|
| Assignment Policy | 技能、轮转、容量、历史客服优先 | 可命名策略、适用 Inbox、分配顺序、优先级和启停 |
| Automation | 超时、机器人和部分定时规则 | 通用事件、条件树、操作符、动作、执行记录和防循环 |
| Macro | 快捷回复 | 一次执行多个会话动作、可见范围和执行审计 |
| Campaign | 通知和部分短信发送 | 受众、渠道、计划时间、一次性或持续活动、退订和统计 |
| Notification | 站内通知、短信和部分移动推送 | 邮件、浏览器、Android、iOS、偏好、已读和设备管理 |
| SLA | 响应超时监控、服务时长报表 | SLA 策略、应用结果、首次响应、下一次响应、解决时限和违约 |
| Help Center | 知识库、常见问题、导航表单 | Portal、Category、Article、多语言、公开搜索、域名和发布流程 |
| Data Operation | 导入导出和部分批量操作 | 保存筛选、异步导入、批量动作、进度、错误和补偿 |

## 2. Assignment Policy

### 2.1 功能设计

Assignment Policy 把现有分散的排队和分配配置组合成可复用策略。一个 Inbox 同时只能绑定一个有效策略；同一策略可以绑定多个 Inbox。

策略依次完成：

1. 筛选属于 Account 且有权访问 Inbox 的成员；
2. 排除 offline、不可用、已达到容量或被暂停分配的成员；
3. 按技能、团队和会话条件进一步筛选；
4. 根据 assignment_order 排序；
5. 根据 conversation_priority 处理紧急或高优先级会话；
6. 原子地确定唯一 assignee；
7. 写入分配活动、实时事件、通知、Webhook、报表和审计。

### 2.2 字段

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 策略标识 | Account 内唯一 |
| name | 策略名称 | 必填且 Account 内不可重复 |
| description | 策略说明 | 可为空 |
| enabled | 是否启用 | 关闭后已分配会话不自动撤回 |
| assignment_order | 分配顺序 | round_robin、least_loaded、longest_idle、historical_agent |
| conversation_priority | 是否优先高优先级会话 | 布尔值 |
| capacity_policy_id | 容量策略 | 可为空，空时使用账号默认值 |
| team_ids | 允许团队 | 必须属于同一 Account |
| inbox_ids | 绑定 Inbox | Inbox 同时只能绑定一个有效策略 |
| conditions | 会话筛选条件 | 使用自动化条件结构 |
| created_at | 创建时间 | 只读 |
| updated_at | 更新时间 | 只读 |

### 2.3 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/assignment_policies | 查询或创建策略 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/assignment_policies/{policy_id} | 详情、更新或删除 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignment_policy | 绑定策略 |
| DELETE | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignment_policy | 解除绑定 |
| GET | /api/v1/accounts/{account_id}/agent_capacity_policies | 查询容量策略 |
| POST | /api/v1/accounts/{account_id}/agent_capacity_policies | 创建容量策略 |
| PATCH、DELETE | /api/v1/accounts/{account_id}/agent_capacity_policies/{policy_id} | 更新或删除容量策略 |

## 3. Automation Rule

### 3.1 功能设计

Automation Rule 使用“事件 → 条件 → 动作”结构。现有欢迎语、超时、敏感词、机器人转人工等专用规则可以保留，但跨模块业务规则应统一进入自动化体系。

### 3.2 触发事件

至少支持：

- conversation_created；
- conversation_updated；
- conversation_opened；
- conversation_resolved；
- conversation_pending；
- conversation_snoozed；
- message_created；
- message_updated；
- assignment_changed；
- priority_changed；
- label_added、label_removed；
- sla_missed；
- contact_created、contact_updated；
- webhook_received；
- scheduled_time_reached。

### 3.3 条件字段

| 条件对象 | 常用字段 |
|---|---|
| Conversation | status、priority、inbox_id、team_id、assignee_id、labels、custom_attributes、waiting_since |
| Contact | name、email、phone_number、identifier、labels、custom_attributes、country、language |
| Message | message_type、content_type、content、private、status、sender_type |
| Inbox | id、channel_type、working_hours、health_status |
| SLA | policy_id、metric、breach、remaining_time |
| Time | hour、weekday、account_timezone、business_hours |

### 3.4 操作符

| 类型 | 操作符 |
|---|---|
| 文本 | equal_to、not_equal_to、contains、does_not_contain、starts_with、ends_with、is_present、is_not_present |
| 列表 | contains、does_not_contain、contains_any、contains_all、is_empty、is_not_empty |
| 数字 | equal_to、not_equal_to、greater_than、less_than、greater_than_or_equal、less_than_or_equal |
| 时间 | before、after、older_than、newer_than、within_business_hours、outside_business_hours |
| 布尔 | is_true、is_false |

### 3.5 动作

至少支持：

- assign_agent、assign_team、clear_assignment；
- add_label、remove_label；
- change_status、change_priority、snooze_until；
- send_message、send_template、add_private_note；
- send_email_notification、send_push_notification；
- trigger_webhook、run_integration；
- handoff_to_bot、handoff_to_assistant、handoff_to_human；
- create_work_order、update_contact_attribute；
- start_sla、stop_sla；
- close_conversation。

### 3.6 字段

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 规则标识 | Account 内唯一 |
| name | 规则名称 | 必填 |
| description | 说明 | 可为空 |
| event_name | 触发事件 | 必须是允许事件 |
| conditions | 条件树 | 支持 all 和 any 组合 |
| actions | 动作列表 | 按顺序执行 |
| active | 是否启用 | 关闭后不再触发新执行 |
| execution_order | 同事件执行顺序 | 数值越小越先执行 |
| stop_on_error | 动作失败后是否停止 | 默认按基线规则 |
| last_executed_at | 最近执行时间 | 只读 |
| created_at | 创建时间 | 只读 |
| updated_at | 更新时间 | 只读 |

### 3.7 防循环和幂等

- 同一 event_id、rule_id 和 resource_id 只能形成一个有效执行实例；
- 规则自身产生的变化再次触发同一规则时，必须受最大深度和重复动作限制；
- 同一字段被多条规则修改时，按执行顺序决定，全部变化写入执行记录；
- Webhook、消息和工单等外部副作用必须有独立幂等键；
- 规则关闭、删除或条件变化不应修改已经完成的历史执行；
- 失败执行必须记录失败动作、是否可重试、尝试次数和最终状态。

### 3.8 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/automation_rules | 查询或创建规则 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/automation_rules/{rule_id} | 详情、更新或删除 |
| POST | /api/v1/accounts/{account_id}/automation_rules/{rule_id}/clone | 复制规则 |

规则测试结果和执行记录属于补齐后的运行证据；Chatwoot 4.16.0 基线未把它们定义为独立公开路径，应通过对象变化、审计、Webhook 和报表联合核对。

## 4. Macro

### 4.1 设计

Macro 是由人工在会话中主动执行的多动作模板。快捷回复只负责插入文本；Macro 可以同时发送消息、添加标签、分配团队、改变优先级和状态。

| 字段 | 含义 |
|---|---|
| id | Macro 标识 |
| name | 名称 |
| actions | 有序动作列表 |
| visibility | personal、team、account |
| owner_id | 个人可见时的拥有者 |
| team_id | 团队可见时的团队 |
| enabled | 是否可执行 |
| created_at、updated_at | 时间 |

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/macros | 查询或创建 Macro |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/macros/{macro_id} | 详情、更新或删除 |
| POST | /api/v1/accounts/{account_id}/macros/{macro_id}/execute | 对指定会话执行 Macro |

执行必须返回每个动作的结果；部分失败时不得伪装为全部成功。重复执行需要明确确认，或由 idempotency_key 阻止重复外部副作用。

## 5. Campaign

### 5.1 类型

| 类型 | 功能 |
|---|---|
| ongoing | 当客户满足条件时持续触发，例如首次访问或完成指定事件 |
| one_off | 对固定受众在计划时间发送一次 |
| transactional | 由明确业务事件触发且与具体客户或会话相关 |

### 5.2 字段

| 字段 | 含义 | 规则 |
|---|---|---|
| id | 活动标识 | Account 内唯一 |
| title | 活动名称 | 必填 |
| campaign_type | ongoing、one_off、transactional | 创建后不宜修改 |
| inbox_id | 发送渠道 | 必须属于同一 Account |
| message | 消息内容 | 与模板二选一或按渠道规则组合 |
| template_id | 渠道模板 | 仅支持模板的渠道使用 |
| audience | 受众条件或固定客户集合 | 必须可审计和重算 |
| scheduled_at | 计划时间 | 按 Account 时区解释 |
| enabled | 是否启用 | 关闭后不创建新投递 |
| status | draft、scheduled、running、completed、cancelled、failed | 只读或命令式变更 |
| sent_count | 已发送数量 | 只读 |
| delivered_count | 已送达数量 | 只读 |
| failed_count | 失败数量 | 只读 |
| created_at、updated_at | 时间 | 只读 |

### 5.3 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/campaigns | 查询或创建活动 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/campaigns/{campaign_id} | 详情、更新或删除 |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/campaigns | 查询 Inbox 活动 |

计划、取消和投递统计通过 Campaign 创建或更新字段、对象状态及异步结果表达；不得虚构基线中不存在的独立公开路径。

活动必须处理客户退订、阻止状态、渠道窗口期、模板审核、时区、重复受众、限流和取消边界。

## 6. Notification

### 6.1 通知类型

- 新会话和新消息；
- 会话分配、转接和参与者变化；
- 提及、私有备注和团队协作；
- SLA 即将到期和已经违约；
- 工单、质检、申诉和复核；
- 自动化、Webhook、渠道和 AI 失败；
- 系统公告、安全和账号状态变化。

### 6.2 通知对象字段

| 字段 | 含义 |
|---|---|
| id | 通知标识 |
| notification_type | 类型 |
| primary_actor | 主要触发者 |
| primary_actor_type | 触发者类型 |
| resource | 目标资源摘要 |
| read_at | 已读时间，可为空 |
| snoozed_until | 通知延后时间，可为空 |
| created_at | 创建时间 |

### 6.3 偏好字段

| 字段组 | 示例 |
|---|---|
| email_flags | assignment、mention、new_message、sla、system |
| push_flags | assignment、mention、new_message、sla、system |
| browser_flags | assignment、mention、new_message、sla、system |
| quiet_hours | start、end、timezone、enabled |

### 6.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/notifications | 查询通知 |
| PATCH | /api/v1/accounts/{account_id}/notifications/{notification_id} | 标记已读或延后 |
| POST | /api/v1/accounts/{account_id}/notifications/read_all | 全部已读 |
| GET、PATCH | /api/v1/accounts/{account_id}/notification_settings | 查询或更新偏好 |
| POST | /api/v1/notification_subscriptions | 注册 Browser Push 或 FCM 订阅 |
| DELETE | /api/v1/notification_subscriptions | 删除当前用户的设备订阅 |

设备订阅测试和诊断使用 47 分册记录的 Super Admin 推送诊断入口，不作为普通 Profile 接口公开。

通知正文不得在权限被移除或资源删除后继续泄露敏感内容。重复业务事件不得产生重复通知。

## 7. SLA

### 7.1 指标

| 指标 | 起点 | 终点 |
|---|---|---|
| first_response_time | 会话首条客户消息 | 首条有效人工公开回复 |
| next_response_time | 客户等待人工回复的最新消息 | 下一条有效人工公开回复 |
| resolution_time | 会话开始或重新打开 | 会话 resolved |

私有备注、系统消息、机器人提示和发送失败消息不应被误算为有效人工回复，除非固定基线明确允许。

### 7.2 SLA Policy 字段

| 字段 | 含义 |
|---|---|
| id | 策略标识 |
| name | 名称 |
| description | 说明 |
| thresholds | 首次响应、下一次响应和解决时限 |
| business_hours_id | 工作时间 |
| only_during_business_hours | 是否只计算工作时间 |
| conditions | 适用 Inbox、标签、优先级或客户条件 |
| enabled | 是否启用 |
| created_at、updated_at | 时间 |

### 7.3 Applied SLA 字段

| 字段 | 含义 |
|---|---|
| id | 应用结果标识 |
| conversation_id | 会话 |
| sla_policy_id | 策略 |
| metric | 指标 |
| target_at | 目标时间 |
| completed_at | 实际完成时间 |
| breach | 是否违约 |
| breach_at | 违约时间 |
| paused_duration | 暂停累计时间 |
| status | active、met、breached、cancelled |

### 7.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/sla_policies | 查询或创建 SLA |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/sla_policies/{sla_id} | 详情、更新或删除 |
| GET | /api/v1/accounts/{account_id}/applied_slas | 查询应用结果 |
| GET | /api/v1/accounts/{account_id}/applied_slas/metrics | 查询 SLA 指标 |
| GET | /api/v1/accounts/{account_id}/applied_slas/download | 下载 SLA 数据 |

SLA 策略变化只影响之后的应用结果，历史结果必须保留当时的策略快照。

## 8. Help Center

### 8.1 对象

| 对象 | 功能 |
|---|---|
| Portal | 一个公开帮助中心站点，包含品牌、域名和默认语言 |
| Category | 文章分类，属于 Portal 和语言 |
| Article | 帮助文章，具有草稿、已发布和归档状态 |
| Locale | Portal 支持语言和默认回退规则 |

### 8.2 Portal 字段

| 字段 | 含义 |
|---|---|
| id | Portal 标识 |
| name | 名称 |
| slug | 公开路径，Account 内唯一 |
| custom_domain | 自定义域名，可为空 |
| default_locale | 默认语言 |
| supported_locales | 支持语言 |
| color、logo、header_text | 品牌展示 |
| status | draft、published、archived |
| created_at、updated_at | 时间 |

### 8.3 Category 和 Article 字段

| 对象 | 主要字段 |
|---|---|
| Category | id、portal_id、name、slug、description、locale、position、status |
| Article | id、portal_id、category_id、title、slug、content、description、locale、status、author_id、published_at、views |

### 8.4 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/portals | 查询或创建 Portal |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/portals/{portal_id} | 详情、更新或删除 |
| GET、POST | /api/v1/accounts/{account_id}/portals/{portal_id}/categories | 查询或创建分类 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/categories/{category_id} | 维护分类 |
| GET、POST | /api/v1/accounts/{account_id}/portals/{portal_id}/articles | 查询或创建文章 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/{article_id} | 维护文章 |
| PATCH | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/update_status | 批量更新发布状态 |
| PATCH | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/update_category | 批量移动分类 |
| GET | /api/v1/accounts/{account_id}/search/articles | 账号内搜索帮助文章 |
| GET | /hc/{portal_slug}/{locale}/articles | 公开文章列表 |

公开接口只能返回 published 内容；草稿、归档和内部知识不得通过搜索或缓存泄露。

## 9. 保存筛选、批量操作和导入

### 9.1 保存筛选

| 字段 | 含义 |
|---|---|
| id | 筛选标识 |
| name | 名称 |
| filter_type | conversation、contact、report |
| query | 条件树 |
| visibility | personal、team、account |
| owner_id、team_id | 可见范围 |

接口：GET、POST `/api/v1/accounts/{account_id}/custom_filters`；GET、PATCH、DELETE `/api/v1/accounts/{account_id}/custom_filters/{filter_id}`。

### 9.2 批量操作

批量对象至少包括 Contact、Conversation、Article、Notification 和 Campaign Delivery。请求字段包括 type、ids 或 filter、fields、labels、status 和 idempotency_key。响应必须提供任务标识、总数、成功数、失败数、跳过数和错误下载入口。

会话和联系人批量操作使用 POST `/api/v1/accounts/{account_id}/bulk_actions`；帮助中心文章使用 8.4 节列出的专项批量路径。

### 9.3 数据导入

| 状态 | 含义 |
|---|---|
| pending | 已创建，等待处理 |
| validating | 校验来源和字段 |
| processing | 正在写入 |
| completed | 全部完成或按规则允许部分失败 |
| failed | 无法完成 |
| cancelled | 已取消 |

接口：

- POST `/api/v1/accounts/{account_id}/data_imports`：创建导入；
- POST `/api/v1/accounts/{account_id}/data_imports/validate_source`：校验数据源；
- GET `/api/v1/accounts/{account_id}/data_imports/{import_id}`：查询进度；
- POST `/api/v1/accounts/{account_id}/data_imports/{import_id}/start`：开始处理；
- POST `/api/v1/accounts/{account_id}/data_imports/{import_id}/abandon`：放弃未完成导入；
- GET `/api/v1/accounts/{account_id}/data_imports/{import_id}/error_logs`：下载错误记录；
- GET `/api/v1/accounts/{account_id}/data_imports/{import_id}/skip_logs`：下载跳过记录。

## 10. 报表补充

现有 webchat 报表、质检和导出能力继续保留，并新增以下统一指标：

- open、pending、snoozed、resolved 会话数量；
- 首次响应、下一次响应和解决时长；
- SLA 达标率和违约数量；
- 自动化触发、成功、失败和节省动作数；
- Macro 使用次数和结果；
- Campaign 发送、送达、阅读、失败和退订；
- 通知发送、送达、点击和失效订阅；
- Help Center 文章浏览、搜索、无结果搜索和转人工；
- Bot、Assistant 和人工处理占比。

报表必须统一 Account 时区、工作时间、筛选条件、去重口径和延迟说明。

## 11. 验收清单

### 11.1 自动化与工作流

- Assignment Policy 在容量、技能、团队和并发竞争下只产生一个最终 assignee；
- Automation 的事件、条件、动作、顺序和防循环均可验证；
- Macro 一次执行多个动作，并返回每个动作的结果；
- Campaign 对重复受众、退订、计划时间、取消和失败有确定结果；
- 批量和导入任务均能到达明确终态并查询失败项。

### 11.2 SLA 与报表

- 工作时间、节假日、暂停和跨时区计算正确；
- 首次响应、下一次响应和解决时限不会被系统消息或私有备注误计；
- 策略变更不改写历史应用结果；
- 会话、SLA、通知和报表最终对账一致。

### 11.3 帮助中心和通知

- 草稿、发布、归档、多语言、搜索和自定义域名形成闭环；
- 公开入口不泄露内部知识或草稿；
- Email、Browser、Android 和 iOS 偏好分别生效；
- 无效设备 Token 被停用，Token 轮换不产生重复订阅；
- 权限移除或资源删除后，旧通知不继续暴露敏感正文。
