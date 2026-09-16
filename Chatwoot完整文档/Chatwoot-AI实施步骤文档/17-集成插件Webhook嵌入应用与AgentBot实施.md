# 集成、插件、Webhook、嵌入应用与 Agent Bot 实施

> 批次：EXT，共 23 项。基线来源：[10](../Chatwoot产品与API文档/10-集成Webhook-AgentBot与插件API.md)、[19](../Chatwoot产品与API文档/19-Webhook与实时事件字段字典.md)、[27](../Chatwoot产品与API文档/27-第三方集成完整字段与授权API.md)、[40](../Chatwoot产品与API文档/40-业务动作事件副作用与跨入口一致性.md)、[46](../Chatwoot产品与API文档/46-webchat-AI模型与插件体系补齐规范.md)和[30-第三方真实环境与最终接口冻结表](30-第三方真实环境与最终接口冻结表.md)。

## 1. 目标

建立统一可安装扩展体系，覆盖 Integration App、账号安装实例 Hook、授权、事件订阅、Webhook 投递、Dashboard App、Agent Bot 和标准第三方集成。

## 2. 平台对象任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| EXT-01 | App 目录 | 返回扩展名称、说明、图标、能力、授权方式、可用性和版本 | `/integrations/apps`；id、name、description、enabled | Feature 和配置关闭项不可安装 |
| EXT-02 | App 能力声明 | 声明所需权限、事件、页面、动作、回调和数据范围 | permissions、subscriptions、scopes | 安装前可确认全部访问范围 |
| EXT-03 | Hook 创建 | 选择 App、Inbox 或 Account 范围；完成授权；保存设置 | `/integrations/hooks`；app_id、inbox_id、settings | 一项安装属于唯一 Account |
| EXT-04 | Hook 查询更新 | 返回状态、范围、非敏感设置；支持重新配置 | hook_id、status | 敏感凭证不返回 |
| EXT-05 | Hook 停用删除 | 停止新事件；撤销授权；处理运行任务和历史关联 | status、revoked_at | 删除后不能继续调用外部系统 |
| EXT-06 | 授权生命周期 | 处理 state、Scope、Token、到期、刷新、撤销和重新授权 | token_status、expires_at、scopes | 授权异常触发健康状态和通知 |
| EXT-07 | 插件权限 | 先 Account，再角色、Inbox、声明权限、Feature 和对象状态 | permissions | 不能通过插件绕过普通权限 |
| EXT-08 | 插件审计 | 记录安装、授权、设置、调用、停用和删除 | actor、app_id、action | 关键动作可追踪 |

## 3. Webhook、嵌入应用与 Bot 任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| EXT-09 | Webhook 配置 | 创建、列表、更新、删除名称、URL、Inbox 和订阅事件 | `/webhooks`；name、url、subscriptions | URL 和事件组合有效 |
| EXT-10 | Payload | 按事件提供公共字段、Account、对象快照和变化信息 | event、id、account、changed_attributes | 与 19 分册字段一致 |
| EXT-11 | 签名 | 为投递生成签名和时间信息，接收方可验证重放窗口 | signature、timestamp | 密钥可轮换且不回显 |
| EXT-12 | 投递与重试 | 建立 Delivery；发送；记录状态、响应、耗时；指数退避 | delivery_id、attempt、status | 超时和非成功结果可追踪 |
| EXT-13 | 停止与恢复 | 达到失败阈值暂停；通知；测试；恢复后继续新事件 | status、failure_count | 不产生无限重试风暴 |
| EXT-14 | Dashboard App | 创建、查询、更新和删除嵌入页面，限制 URL 与可见上下文 | `/dashboard_apps`；title、content.type、content.url | 不能读取未授权会话或身份信息 |
| EXT-15 | Agent Bot | 创建、列表、更新、删除 Bot 与外发地址 | `/agent_bots`；name、outgoing_url、bot_type | Account 或平台范围明确 |
| EXT-16 | Bot 绑定 | 将 Bot 绑定 Inbox，定义接管、转人工和解除 | agent_bot_id、inbox_id | 同一会话的 Bot 与人工状态确定 |
| EXT-17 | Bot 事件处理 | 发送会话和消息事件；接收 Bot 动作；校验幂等和权限 | event_id、conversation_id、action | Bot 失败不阻塞客户消息 |

## 4. 标准集成任务

| 编号 | 集成 | 逐步实施 | 完成判定 |
|---|---|---|---|
| EXT-18 | Slack | 授权工作区；选择频道；同步指定会话消息和动作；处理撤销 | 消息、线程和会话关联正确 |
| EXT-19 | Linear | 授权；读取团队项目；创建 Issue；保存关联；同步状态 | 一次动作不重复创建 Issue |
| EXT-20 | Shopify | 授权店铺；匹配联系人；读取订单；处理隐私删除回调 | 店铺隔离、订单查询和 redact 通过 |
| EXT-21 | Notion | 授权空间；创建 Hook；读取允许内容；处理 Token 和版本变化 | Scope、版本和撤销通过 |
| EXT-22 | Dialogflow、Translate、OpenAI 旧式、LeadSquared | 按 27 分册分别完成配置、范围、请求、失败和停用 | 每项适用集成都有真实闭环 |
| EXT-23 | 会议集成 | 配置 RealtimeKit/Dyte；创建会议；加入；结束；处理授权失败 | 会话关联、参与状态和最终结果正确 |

## 5. 插件安装流程

1. 从目录读取能力、权限、Feature 和配置要求；
2. 校验当前账号和操作权限；
3. 展示所需数据范围、事件和第三方 Scope；
4. 完成外部授权并校验 state；
5. 创建 Hook，安全保存凭证；
6. 验证连接和必要回调；
7. 启用订阅、页面或业务动作；
8. 执行真实业务闭环；
9. 记录事件、响应、失败、用量和审计；
10. Token 到期时进入 warning 或 disconnected；
11. 重新授权后确认恢复；
12. 停用或删除时撤销访问并停止新调用。

## 6. 必测场景

- App 已关闭、缺少安装配置或账号无 Feature；
- 授权 state 不匹配、Scope 缺失、Token 过期和撤销；
- Hook 跨 Account 或引用无权 Inbox；
- Webhook 接收端超时、非成功、重复投递和签名轮换；
- Dashboard App 尝试加载不允许域名或读取不可见会话；
- Bot 超时、返回无效动作、重复动作和转人工；
- 标准集成对象被外部删除或版本变化；
- 停用和删除后仍到达的延迟回调；
- 插件失败不回滚已成功保存的客户消息。

## 7. 完成条件

- [ ] EXT-01 至 EXT-23 全部关闭或取得批准 N/A；
- [ ] App、Hook、授权、权限、审计和生命周期完整；
- [ ] Webhook 签名、投递、重试、停用和恢复闭环；
- [ ] Dashboard App 与 Agent Bot 不绕过权限；
- [ ] 每个适用标准集成都有真实授权和业务回执；
- [ ] webchat 既有外部机器人和定点外部查询能力已迁移。
- [ ] 每个适用插件、集成、Webhook 接收方和 Bot 实例已在 30 分册取得真实授权、调用、失败恢复与停用证据。
