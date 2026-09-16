# 报表、搜索、SLA、CSAT 与指标口径实施

> 批次：RPT，共 20 项。基线来源：[08](../Chatwoot产品与API文档/08-报表SLA帮助中心与搜索API.md)、[20](../Chatwoot产品与API文档/20-报表指标口径筛选与返回字典.md)、[39](../Chatwoot产品与API文档/39-列表查询分页排序筛选与容量边界.md)、[45](../Chatwoot产品与API文档/45-webchat自动化SLA帮助中心与通知补齐规范.md)。

## 1. 目标

以统一会话和消息事件为事实来源，完成趋势、汇总、实时、导出、搜索、SLA 和满意度，并保留 webchat 现有客服、排队、渠道、访客、质检和自定义报表能力。

## 2. 报表与搜索任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| RPT-01 | 趋势报表 | 按时间桶汇总会话量、消息量和响应指标 | `/api/v2/accounts/{id}/reports`；since、until、type | 时区、边界和空桶正确 |
| RPT-02 | 汇总报表 | 按 Agent、Team、Inbox、Label、Channel 汇总 | `/summary_reports`；group_by、id | 各分组之和与允许口径一致 |
| RPT-03 | 实时报表 | 统计当前 open、unassigned、队列、在线成员等 | `/live_reports`；metric、group_by | 与当前业务对象在允许延迟内一致 |
| RPT-04 | 成员指标 | 首次响应、平均响应、解决、处理量和在线状态 | agent_id | 转接和协作归属按冻结口径 |
| RPT-05 | Team 与 Inbox 指标 | 汇总 Team、Inbox、渠道和工作时间口径 | team_id、inbox_id、business_hours | 跨组、跨 Inbox 不重复计算 |
| RPT-06 | 会话指标 | 进入、重开、解决、积压、等待和处理时间 | timestamps、status events | 状态往返计算正确 |
| RPT-07 | 消息指标 | 入站、出站、私有、Bot、失败和送达 | message_type、status | 活动消息不误计为客户消息 |
| RPT-08 | 机器人与 AI 指标 | 解决量、转人工、建议采用、用量和失败 | assistant_id、model、feature | 与 AI 用量和会话记录可对账 |
| RPT-09 | 专项分析 | 支持 SLA、CSAT、活动、渠道、质检等专项入口 | report_type、filters | 筛选和口径有明确说明 |
| RPT-10 | CSV 导出 | 固定字段、时间范围、异步状态和下载权限 | export status、download_url | 文件与同条件报表可对账 |
| RPT-11 | Year in Review | 按适用 Feature 汇总年度指标和展示状态 | year、status | 不适用或数据不足结果明确 |
| RPT-12 | 全局搜索 | 搜索会话、消息、联系人、公司和文章，应用权限范围 | `/search`；q、type、page | 私有、跨账号和不可见 Inbox 不泄露 |

## 3. SLA 与 CSAT 任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| RPT-13 | SLA Policy | 创建、查询、更新、删除首响、次响和解决阈值 | `/sla_policies`；thresholds、business_hours | 阈值、范围和状态校验完整 |
| RPT-14 | SLA 应用 | 会话进入适用范围时绑定策略并冻结必要快照 | policy_id、conversation_id | 策略后改不静默改变历史结果 |
| RPT-15 | SLA 计时 | 按营业时间、暂停状态和目标事件累计时间 | started_at、due_at、paused_at | 跨时区、节假日和状态往返正确 |
| RPT-16 | SLA 违约 | 到期形成 breach、事件、通知和时间线 | breach、metric、breached_at | 只违约一次且可追踪 |
| RPT-17 | Applied SLA 查询 | 按会话、状态、时间和指标分页查询 | `/applied_slas` | 详情、列表和报表一致 |
| RPT-18 | CSAT 触发 | 会话解决后按渠道和设置生成评价入口 | conversation_id、survey link | 重复解决不重复发送未允许问卷 |
| RPT-19 | CSAT 提交 | 接收评分和反馈；验证令牌、范围和重复提交 | rating、feedback_message | 首次、更新或拒绝规则明确 |
| RPT-20 | CSAT 报表 | 按 Agent、Team、Inbox、标签和时间汇总 | rating、filters | 与原始评价逐项可对账 |

## 4. 指标口径冻结

每个指标必须登记：

- 业务定义；
- 事实来源和使用的状态事件；
- 开始、停止、暂停和重置时点；
- Account 时区或 UTC 解释；
- 是否只计算营业时间；
- 分配变化后的归属；
- 删除、合并和历史缺失字段的处理；
- 实时与离线汇总允许延迟；
- 空值、无数据和除零结果；
- 导出、趋势、汇总和详情的对应关系。

## 5. 必测场景

- 会话多次 open/resolved、成员多次转接和 Team 变化；
- 跨午夜、周末、节假日和夏令时边界；
- 消息入站后首次响应与 Bot 首次回复的区别；
- SLA 运行时策略被更新、停用或删除；
- 会话 pending、snoozed 和 resolved 对计时的影响；
- CSAT 链接重复提交、过期、跨会话和匿名访问；
- 实时报表与事实对象的允许延迟；
- CSV 导出与接口相同筛选的逐项对账；
- 搜索私有备注、已删除客户和不可见 Inbox；
- 无数据、单条数据和大时间范围。

## 6. 完成条件

- [ ] RPT-01 至 RPT-20 全部关闭；
- [ ] 20 分册每个指标都有事实来源和边界规则；
- [ ] 趋势、汇总、实时、详情和导出可对账；
- [ ] SLA 从策略、应用、计时、违约到通知形成闭环；
- [ ] CSAT 从触发、提交到报表形成闭环；
- [ ] webchat 原有报表、质检指标和自定义报表无回退。
