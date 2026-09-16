# Macro、快捷回复、活动与统一通知实施

> 批次：OPS，共 18 项。基线来源：[自动化与宏基础合同](../Chatwoot全系统基础层规范/10-自动化规则宏与统一动作能力合同.md)、[07](../Chatwoot产品与API文档/07-自动化活动通知与模板API.md)、[18](../Chatwoot产品与API文档/18-自动化条件操作符与动作字典.md)、[31](../Chatwoot产品与API文档/31-异步任务进度终态与补偿规则.md)、[45](../Chatwoot产品与API文档/45-webchat自动化SLA帮助中心与通知补齐规范.md)。

## 1. 目标

完成坐席主动执行的 Macro、文本快捷回复、持续或一次性 Campaign，以及站内、邮件、浏览器和移动端统一通知。

## 2. Macro 与快捷回复任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| OPS-01 | Macro 创建 | 设置名称、可见范围和有序动作；校验每个动作 | `/macros`；name、actions、visibility | 不把纯文本快捷回复误当完整 Macro |
| OPS-02 | Macro 管理 | 完成列表、详情、更新、复制、删除和排序 | macro_id | 个人、团队和账号可见性正确 |
| OPS-03 | Macro 执行 | 冻结 MacroVersion；预览会话和权限；通过统一 Action Capability 按顺序执行；记录每项结果 | `/macros/{id}/execute`；conversation_ids、execution_id | 不在 OPS 重复业务规则，与自动化和手工动作使用同一 Command |
| OPS-04 | Macro 批量执行 | 对多个会话逐项执行并返回成功失败明细 | ids、execution results | 单项失败不静默，幂等规则明确 |
| OPS-05 | 快捷回复管理 | 创建、查询、更新、删除短码和内容 | `/canned_responses`；short_code、content | 短码范围和唯一规则明确 |
| OPS-06 | 快捷回复使用 | 搜索短码、插入变量、预览后发送 | short_code、resolved_content | 插入不直接发送，变量失败可见 |

## 3. Campaign 任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| OPS-07 | Campaign 创建 | 选择 ongoing 或 one_off、Inbox、受众、消息和时间 | `/campaigns`；campaign_type、message、inbox_id、scheduled_at | 类型与字段组合有效 |
| OPS-08 | 受众规则 | 用标签、属性、语言、Inbox 和客户同意状态筛选 | audience、filters | 发送前可确认目标数量 |
| OPS-09 | 持续活动 | 在客户进入条件时判断频次、展示和退出条件 | trigger、frequency | 同一客户不重复超频触达 |
| OPS-10 | 一次性活动 | 到计划时间冻结受众、创建发送任务并记录逐条结果 | scheduled_at、status | 时区和计划变更正确 |
| OPS-11 | 活动状态 | 支持草稿、计划、运行、暂停、完成、失败和取消 | status | 受理与最终发送结果分开 |
| OPS-12 | 退订与合规 | 校验同意、退订、渠道模板和发送时段 | opt_in、unsubscribed | 已退订客户不会被发送 |
| OPS-13 | 活动统计 | 汇总目标、已发送、送达、阅读、回复、失败和退订 | counts、rates | 统计可追溯到消息终态 |

## 4. Notification 任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| OPS-14 | 通知创建与类型 | 根据分配、提及、新消息、SLA、授权等事件生成通知 | notification_type、primary_actor、resource | 同一事件不重复生成 |
| OPS-15 | 通知列表详情 | 支持未读、已读、类型、时间、分页和 Account 范围 | `/notifications`；read_at、snoozed_until | 数量与列表一致 |
| OPS-16 | 通知状态 | 标记单条或全部已读、未读、延后和删除 | read_at、snoozed_until | 多端状态及时同步 |
| OPS-17 | 通知偏好 | 按 Account、事件和通道保存邮件、浏览器、Android、iOS 偏好 | `/notification_settings`；email_flags、push_flags | 未启用通道不发送 |
| OPS-18 | 多通道投递 | 选择通道、生成内容、发送、处理失败和失效设备 | channel、delivery_status | 站内记录与外部投递结果可区分 |

## 5. 业务流程重点

### 5.1 Macro

1. 读取当前会话状态和权限；
2. 展示即将执行的动作；
3. 逐项执行并复用普通业务入口规则；
4. 对每项结果记录成功、跳过或失败；
5. 触发对应事件、通知和审计；
6. 形成整体执行结果，不掩盖部分失败。

### 5.2 Campaign

1. 验证 Inbox、渠道能力、受众和模板；
2. 以 Account 时区解释计划时间；
3. 在发送前再次核对退订和渠道状态；
4. 为每个目标创建可追踪发送项；
5. 通过消息状态汇总最终结果；
6. 暂停或取消只影响未发送项；
7. 重启后不重复已受理项。

## 6. 完成条件

- [ ] OPS-01 至 OPS-18 全部关闭；
- [ ] Macro、快捷回复、持续活动和一次性活动边界清楚；
- [ ] Macro 与 Automation 复用统一 Action Capability，AUT 和 OPS 之间没有运行时依赖；
- [ ] 活动受众、计划、退订、终态和统计可追踪；
- [ ] 通知的站内状态、偏好和多端投递结果一致；
- [ ] 批量和异步操作不隐藏部分失败。
