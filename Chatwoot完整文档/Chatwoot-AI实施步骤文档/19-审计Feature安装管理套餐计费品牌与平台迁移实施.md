# 审计、Feature、安装管理、套餐、计费、品牌与平台迁移实施

> 批次：ENT，共 25 项。基线来源：[16](../Chatwoot产品与API文档/16-审计企业设置计费与平台迁移API.md)、[24](../Chatwoot产品与API文档/24-Feature开关版本与安装配置字典.md)、[25](../Chatwoot产品与API文档/25-SuperAdmin与安装级管理功能.md)、[35](../Chatwoot产品与API文档/35-权限Feature套餐配置与兼容优先级.md)、[47](../Chatwoot产品与API文档/47-webchat开放平台安全审计与企业能力补齐规范.md)、[30-第三方真实环境与最终接口冻结表](30-第三方真实环境与最终接口冻结表.md)，并强制遵守 [日志基础层设计](../Chatwoot日志审计与变更追踪设计文档/README.md)。

## 1. 目标

完成安装级管理、Account 企业设置、审计、Reporting Event、Feature、全局配置、品牌、套餐、额度、Cloud 计费、账号生命周期和 Email Channel Migration。

## 2. 审计与企业设置任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| ENT-01 | Audit Log 兼容视图 | 把登录、安全、权限、配置、删除、导出、授权和敏感业务动作映射为 AUD 账本的业务审计视图，不另建权威记录 | `/audit_logs`；action、audited_changes、username、change_id | 兼容语义保持，记录不可修改且能定位到 L0 变更账本 |
| ENT-02 | 审计查询 | 通过 AUD 查询合同按操作者、动作、对象、时间和分页查询；按权限遮蔽敏感字段 | actor、action、auditable_type、since、until、correlation_id | 范围和留存规则正确，不直接读取业务模块存储 |
| ENT-03 | Reporting Event | 创建和查询运营或计费度量事件 | `/reporting_events`；name、value、event_start_time、event_end_time | 重复和时间范围规则明确 |
| ENT-04 | SAML Settings | 管理 SSO URL、证书、角色映射、启停和测试 | `/saml_settings`；sso_url、certificate、role_mappings | 设置与实际登录结果一致 |
| ENT-05 | Onboarding | 保存引导步骤、行业、时区和完成状态 | `/onboarding`；onboarding_step、industry、timezone | 重复提交和跳步规则明确 |
| ENT-06 | 帮助中心生成 | 受理生成任务、查询状态、处理失败和重试 | generation status、error | 受理与最终完成分开 |
| ENT-07 | 品牌邮件布局 | 管理 Account 邮件品牌、预览和恢复默认 | `/branded_email_layout` | 变量、权限和空值处理正确 |

## 3. Feature、安装配置与安装管理任务

| 编号 | 功能 | 逐步实施 | 关键字段 | 完成判定 |
|---|---|---|---|---|
| ENT-08 | Feature 目录 | 登记 24 分册默认启用、关闭、企业和弃用 Feature | feature_key、default、edition | 数量和分类与基线一致 |
| ENT-09 | Feature 生效 | 联合安装默认、Account 覆盖、版本、套餐和依赖判断 | enabled、source、reason | 关闭后入口、接口和任务行为一致 |
| ENT-10 | 配置目录 | 建立 102 项安装配置的分类、类型、默认、敏感和公开属性 | key、value_type、default、sensitive | 24 分册完整性核对通过 |
| ENT-11 | 配置更新生效 | 校验、保存、缓存传播、审计和回退 | config key、value、effective_at | 变更后所有节点结果一致 |
| ENT-12 | 安装总览 | 展示版本、Account/User 数量、系统状态和关键配置摘要 | version、counts、status | 仅安装管理身份可见 |
| ENT-13 | 安装级 Account | 列表、筛选、创建、更新、初始化、暂停、缓存和删除 | account fields、status | 高风险动作有确认和审计 |
| ENT-14 | 安装级 User | 列表、筛选、创建、更新、禁用和删除 | user fields、status | 不绕过账号成员关系规则 |
| ENT-15 | AccountUser 与 Token | 管理账号关系；查看必要 Token 摘要；撤销访问 | account_id、user_id、token status | 敏感 Token 不完整展示 |
| ENT-16 | 全局 Agent Bot 与 Platform App | 管理安装范围 Bot、平台应用、Token 和状态 | bot、app、token_status | 安装范围和账号范围明确 |
| ENT-17 | Banner、实例和版本 | 管理平台横幅、实例状态、版本设置和维护信息 | banner、instance_status、version | 展示、启停和有效期正确 |
| ENT-18 | 后台任务监控 | 查询任务类别、队列、状态、失败和重试，不改变业务终态 | task_id、queue、status | 可定位阻塞但不泄露敏感参数 |

## 4. 套餐、计费与迁移任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| ENT-19 | 套餐 | 定义套餐、Feature、成员数量、渠道和额度 | plan_id、features、limits | 套餐变化传播到实际可用性 |
| ENT-20 | 额度 | 记录 allowed、consumed、reserved、remaining 和周期 | `/limits`；allowed、consumed | 并发消费不超额，释放和重置正确 |
| ENT-21 | Checkout 与 Top-up | 创建结账或充值动作，处理跳转、成功、取消和失败 | currency、amount、action_type | 回调前不提前增加可用额度 |
| ENT-22 | 支付回调 | 验签、去重、更新账单和额度、记录审计 | provider_event_id、status | 重复回调不重复计费 |
| ENT-23 | 账号删除 | 请求、等待、取消、执行和完成账号删除 | deletion_status、scheduled_at | 等待期和不可逆边界明确 |
| ENT-24 | Email Channel Migration | 创建迁移、验证 Provider 配置、执行、查询和失败恢复 | `/email_channel_migrations`；provider、provider_config、status | 邮件收发和历史线程不丢失 |
| ENT-25 | 企业能力联合验收 | 按身份、Feature、配置、套餐、额度和对象状态验证每项能力 | availability reason | 禁用原因可区分且跨入口一致 |

## 5. 安装配置实施顺序

1. 按 24 分册登记所有配置键；
2. 为每个键确定类型、默认值、敏感性和可公开范围；
3. 建立更新校验和相互依赖；
4. 确定立即生效、缓存传播或重启生效；
5. 更新后记录操作者、前后值摘要和生效时间；
6. 验证受影响 Feature、渠道、AI、推送和集成；
7. 失败时恢复上一个有效值；
8. 敏感值只允许替换，不允许普通查询回显；
9. 配置变化后按 24、35、37 分册重开回归任务。

## 6. 必测场景

- Feature 默认值、Account 覆盖、企业版限制和套餐到期组合；
- 配置格式错误、依赖缺失、缓存未同步和回退；
- 普通 Administrator 请求安装管理入口；
- 审计字段包含 Token、证书或密码时的遮蔽；
- 并发消耗最后一份额度、失败后释放和账期重置；
- 支付成功回调重复、乱序、伪造和长时间延迟；
- 账号删除等待期间继续使用、取消和最终执行；
- Email Migration 中断、重复启动和切回旧 Provider；
- Platform App Token 撤销后立即失效。

## 7. 完成条件

- [ ] ENT-01 至 ENT-25 全部关闭或取得批准 N/A；
- [ ] 24 分册 Feature 和 102 项安装配置无遗漏；
- [ ] 审计、SAML、Onboarding、品牌和安装管理闭环；
- [ ] 套餐、额度、支付和账号删除的并发与回调去重通过；
- [ ] Email Channel Migration 有成功、失败和回退证据；
- [ ] 高风险入口不被 Account 普通身份访问。
- [ ] ENT-01、ENT-02 只提供 AUD 账本的兼容视图和受控查询，没有形成第二套审计数据。
