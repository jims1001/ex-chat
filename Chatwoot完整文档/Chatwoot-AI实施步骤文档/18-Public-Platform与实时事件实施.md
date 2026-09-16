# Public、Platform 与实时事件实施

> 批次：OPEN，共 18 项。基线来源：[11](../Chatwoot产品与API文档/11-Public-Platform与实时事件API.md)、[19](../Chatwoot产品与API文档/19-Webhook与实时事件字段字典.md)、[35](../Chatwoot产品与API文档/35-权限Feature套餐配置与兼容优先级.md)、[47](../Chatwoot产品与API文档/47-webchat开放平台安全审计与企业能力补齐规范.md)。

## 1. 目标

提供客户侧 Public API、多租户控制面的 Platform API，以及工作台和客户侧实时事件。三类身份必须严格隔离，并与 Account API、Widget、Webhook 的业务结果一致。

## 2. Public API 任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| OPEN-01 | Public Inbox | 用公开 identifier 读取允许的 Inbox 摘要与可用能力 | `/public/api/v1/inboxes/{identifier}` | 不返回管理配置和凭证 |
| OPEN-02 | Public Contact | 创建或恢复客户身份，验证 identifier 和公开令牌 | `/contacts`；identifier、name、email、phone_number | 不能冒充其他客户 |
| OPEN-03 | Public Contact 更新 | 只更新当前客户允许字段和自定义属性 | contact_identifier | Account 和字段范围正确 |
| OPEN-04 | Public Conversation | 创建、列表、详情和状态查询 | `/conversations`；custom_attributes、status | 只能看到当前客户会话 |
| OPEN-05 | Public Message | 创建、列表、附件、echo 幂等和发送状态 | `/messages`；content、echo_id、attachments | 与统一消息对象一致 |
| OPEN-06 | Public CSAT | 验证调查身份并提交评分和反馈 | `/public/api/v1/csat_survey/{id}` | 不能提交其他会话问卷 |

## 3. Platform API 任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| OPEN-07 | Platform App 身份 | 签发、验证、轮换和撤销平台应用 Token | platform token、app_id | 与普通成员 Token 不可混用 |
| OPEN-08 | Platform User | 创建、查询、更新、删除平台管理的 User | `/platform/api/v1/users`；name、email、password | 重复邮箱和生命周期正确 |
| OPEN-09 | Platform Account | 创建、查询、更新和删除 Account | `/platform/api/v1/accounts`；name、locale、timezone | 新账号初始化终态可查询 |
| OPEN-10 | AccountUser | 将 User 加入或移出 Account，设置角色 | account_id、user_id、role | 关系重复和跨平台范围被拒绝 |
| OPEN-11 | Platform Bot | 管理平台范围 Agent Bot 和账号绑定 | `/platform/api/v1/agent_bots` | 平台与账号 Bot 范围清楚 |
| OPEN-12 | Platform 生命周期 | 处理账号初始化、暂停、迁移、计划删除和最终删除 | status、task_id | 异步状态和补偿可追踪 |

## 4. 实时事件任务

| 编号 | 功能 | 逐步实施 | 字段重点 | 完成判定 |
|---|---|---|---|---|
| OPEN-13 | 实时认证 | 用 pubsub_token 或账号订阅身份建立连接 | token、account_id、user_id | 客户与成员订阅范围隔离 |
| OPEN-14 | 订阅范围 | 绑定 Account、Inbox、User 或 Contact 允许频道 | subscription identifier | 无权频道订阅失败 |
| OPEN-15 | 事件字典 | 实施会话、消息、联系人、Inbox、成员、通知和 Copilot 事件 | event、payload、timestamp | 字段与 19 分册一致 |
| OPEN-16 | 顺序与去重 | 提供事件标识和时间；处理重复、乱序和同对象版本 | event_id、version | 使用方可识别重复和过期事件 |
| OPEN-17 | 重连与补读 | 断线后重连；从 REST 或事件位置恢复；处理过期窗口 | last_event_id、cursor | 不以实时流作为唯一事实来源 |
| OPEN-18 | 跨入口一致性 | Account、Widget、Public 和 Provider 动作产生相同业务事件 | object id、changed attributes | 事件、列表和详情最终一致 |

## 5. Public 客户流程

1. 读取公开 Inbox 能力；
2. 创建或恢复 Contact；
3. 保存客户侧认证信息；
4. 创建或恢复 Conversation；
5. 发送带 echo_id 的 Message；
6. 建立实时订阅；
7. 接收消息和状态，使用 REST 补读；
8. 更新已读位置；
9. 完成会话和 CSAT；
10. 身份过期或撤销后不能访问历史客户之外的数据。

## 6. 必测场景

- Widget、Public Contact 和 Account Token 互换；
- 一个 Public Contact 尝试读取另一个客户会话；
- Platform App 尝试访问其管理范围之外账号；
- Platform 创建账号异步失败和重复请求；
- 实时连接断开、Token 过期、重复事件、乱序事件和长时间离线；
- 私有备注、敏感字段和不可见 Inbox 事件不外泄；
- REST 已成功但实时事件丢失时可通过补读恢复；
- 新旧入口对同一消息的事件字段和最终对象一致。

## 7. 完成条件

- [ ] OPEN-01 至 OPEN-18 全部关闭；
- [ ] Public、Platform、Widget 和 Account 身份不能混用；
- [ ] 客户侧联系人、会话、消息和 CSAT 闭环；
- [ ] 平台 User、Account、关系和 Bot 生命周期闭环；
- [ ] 实时认证、范围、事件、去重、重连和补读闭环；
- [ ] REST、实时和 Webhook 对同一动作最终一致。
