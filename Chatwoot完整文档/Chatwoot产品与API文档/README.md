# Chatwoot 产品、API 与 webchat 补齐文档

> 适用版本：Chatwoot 4.16.0
> 文档范围：Chatwoot 产品功能、API 路径、请求字段、响应字段、权限、枚举、错误和业务规则，以及 webchat 对照缺口、补齐合同、迁移和验收规范。

## 1. 总入口

[全体功能与 API 总表](00-全体功能与API总表.md) 汇总全部模块、核心对象、接口入口、主要字段、权限类型和业务流程。需要查具体字段时，再进入对应分册。

后续所有模块、接口、插件、AI 和 Provider 首先遵守 [全系统基础层规范](../Chatwoot全系统基础层规范/README.md)。如需查看模块边界、数据所有权、无环依赖、插件和对外接口设计，请进入 [模块化架构设计文档](../Chatwoot模块化架构设计文档/README.md)。所有数据变更的 L0 采集、变更字段、不可变账本、查询、隐私和全模块验收见 [日志基础层、审计与数据变更追踪设计文档](../Chatwoot日志审计与变更追踪设计文档/README.md)。如需让 AI 逐项实施，请进入 [Chatwoot 与 webchat 全功能 AI 实施步骤文档](../Chatwoot-AI实施步骤文档/README.md)。实施目录包含 482 个任务、26 个批次、依赖顺序、逐任务状态、迁移、验证和完成关门规则。

## 2. 文档目录

### 2.1 基础规范

| 分册 | 内容 |
|---|---|
| [00-全体功能与API总表](00-全体功能与API总表.md) | 全部功能、对象、API 与字段导航 |
| [23-全接口功能与文档覆盖矩阵](23-全接口功能与文档覆盖矩阵.md) | 全部接口族、管理入口、回调入口和对应分册反查 |
| [30-全API逐动作路径索引](30-全API逐动作路径索引.md) | 全部业务动作的方法、完整路径、功能和对应分册 |
| [01-产品功能总览](01-产品功能总览.md) | 产品定位、角色、模块和业务流程 |
| [02-API通用约定与认证](02-API通用约定与认证.md) | 接口类型、认证、分页、时间和错误 |
| [13-身份资料安全与登录会话API](13-身份资料安全与登录会话API.md) | 登录、Google OAuth、SAML、Profile、MFA、登录设备和推送订阅 |
| [21-角色权限与接口可用性矩阵](21-角色权限与接口可用性矩阵.md) | Administrator、Agent、Custom Role、Contact 和 Platform 权限边界 |

### 2.2 核心业务

| 分册 | 内容 |
|---|---|
| [03-账号成员团队与权限API](03-账号成员团队与权限API.md) | Account、Agent、Team、Role 和容量策略 |
| [04-Inbox渠道与Widget-API](04-Inbox渠道与Widget-API.md) | Inbox、渠道、成员、工作时间和网站聊天 |
| [17-各渠道创建更新字段字典](17-各渠道创建更新字段字典.md) | Web Widget、Email、API、WhatsApp、LINE、Telegram、SMS 和授权型渠道字段 |
| [05-联系人公司与标签API](05-联系人公司与标签API.md) | Contact、Company、Label、Note 和自定义字段 |
| [06-会话消息与分配API](06-会话消息与分配API.md) | Conversation、Message、Attachment 和 Assignment |
| [15-渠道授权通话会议与高级Inbox-API](15-渠道授权通话会议与高级Inbox-API.md) | OAuth、Facebook、WhatsApp、Twilio、Call 和 Conference |
| [26-渠道状态错误重新授权与回调字典](26-渠道状态错误重新授权与回调字典.md) | 渠道健康、授权失效、模板状态、消息失败、Provider 回调和恢复流程 |

### 2.3 运营能力

| 分册 | 内容 |
|---|---|
| [07-自动化活动通知与模板API](07-自动化活动通知与模板API.md) | Automation、Macro、Campaign 和 Notification |
| [08-报表SLA帮助中心与搜索API](08-报表SLA帮助中心与搜索API.md) | Report、SLA、CSAT、Portal、Article 和 Search |
| [14-分配策略筛选导入与批量操作API](14-分配策略筛选导入与批量操作API.md) | Assignment Policy、Custom Filter、Data Import 和 Bulk Action |
| [18-自动化条件操作符与动作字典](18-自动化条件操作符与动作字典.md) | 触发事件、Condition、Operator、Action 和防循环规则 |
| [20-报表指标口径筛选与返回字典](20-报表指标口径筛选与返回字典.md) | 指标计算、时间单位、维度、实时指标、Bot 指标和导出 |
| [31-异步任务进度终态与补偿规则](31-异步任务进度终态与补偿规则.md) | Message、Import、Document、Campaign、Template、删除和批量任务最终状态 |

### 2.4 智能与扩展

| 分册 | 内容 |
|---|---|
| [09-Captain-Copilot与AI-API](09-Captain-Copilot与AI-API.md) | 模型偏好、Assistant、知识、Copilot 和 Custom Tool |
| [10-集成Webhook-AgentBot与插件API](10-集成Webhook-AgentBot与插件API.md) | Integration、Webhook、Dashboard App、第三方连接和 Bot |
| [27-第三方集成完整字段与授权API](27-第三方集成完整字段与授权API.md) | 11 类集成、Hook 字段、Slack、Linear、Shopify、Notion、Dialogflow、会议和 CRM |
| [19-Webhook与实时事件字段字典](19-Webhook与实时事件字段字典.md) | 12 个 Webhook 订阅、实时事件、Payload、签名和去重 |
| [29-第三方入站Webhook完整字段字典](29-第三方入站Webhook完整字段字典.md) | 15 类第三方回调的 Header、路径、Payload、校验、去重和状态 |
| [22-AI模型Provider与Feature能力矩阵](22-AI模型Provider与Feature能力矩阵.md) | 12 类 AI Feature、Provider、模型、默认值和可用性 |

### 2.5 开放接口与公共字段

| 分册 | 内容 |
|---|---|
| [11-Public-Platform与实时事件API](11-Public-Platform与实时事件API.md) | 客户接口、平台接口和实时事件 |
| [12-公共字段枚举错误与验收](12-公共字段枚举错误与验收.md) | 公共对象、枚举、错误和验收 |

### 2.6 企业与平台管理

| 分册 | 内容 |
|---|---|
| [16-审计企业设置计费与平台迁移API](16-审计企业设置计费与平台迁移API.md) | Audit、Reporting Event、SAML、Cloud Billing 和 Email Migration |
| [24-Feature开关版本与安装配置字典](24-Feature开关版本与安装配置字典.md) | 68 个 Account Feature、102 个安装配置和联合可用性规则 |
| [25-SuperAdmin与安装级管理功能](25-SuperAdmin与安装级管理功能.md) | Account、User、Token、全局 Bot、Platform App、实例状态和推送诊断 |
| [28-移动端推送诊断与客户端入口API](28-移动端推送诊断与客户端入口API.md) | Browser Push、FCM、设备订阅、深链、Android/iOS 域名关联和诊断 |

### 2.7 完整一致性还原与验收

| 分册 | 内容 |
|---|---|
| [32-完整系统一致性还原目标与基线](32-完整系统一致性还原目标与基线.md) | 一致性层级、版本、配置、数据、时间、第三方基线和证据要求 |
| [33-并发原子性幂等与状态竞争规则](33-并发原子性幂等与状态竞争规则.md) | 并发分配、消息状态、渠道串行、合并、活动、导入和幂等规则 |
| [34-字段默认空值唯一约束与数据生命周期](34-字段默认空值唯一约束与数据生命周期.md) | 创建默认值、空值、合并与替换、唯一范围、删除和延迟清理 |
| [35-权限Feature套餐配置与兼容优先级](35-权限Feature套餐配置与兼容优先级.md) | 十二层可用性判定、身份范围、Feature、套餐、配置传播和历史兼容 |
| [36-异常超时重试限流与第三方差异规则](36-异常超时重试限流与第三方差异规则.md) | 故障分类、超时、重试、去重、Provider 差异、限流和恢复 |
| [37-完整系统一致性对照验收规范](37-完整系统一致性对照验收规范.md) | 全功能验收案例、证据包、通过门槛、长周期观察和一致性声明 |
| [38-API请求响应遗漏字段补充字典](38-API请求响应遗漏字段补充字典.md) | 公开接口此前未单独解释的39个唯一字段及读写、空值和归属规则 |
| [39-列表查询分页排序筛选与容量边界](39-列表查询分页排序筛选与容量边界.md) | 各资源固定页大小、查询、排序、筛选、批量、字段和速率边界 |
| [40-业务动作事件副作用与跨入口一致性](40-业务动作事件副作用与跨入口一致性.md) | 37个业务变化及通知、自动化、Webhook、Bot、集成、报表和AI副作用 |
| [41-第三方API版本能力漂移与历史兼容](41-第三方API版本能力漂移与历史兼容.md) | Provider逐动作版本、Scope、Token、回调漂移和历史数据兼容 |
| [42-全功能覆盖收口与未决条件清单](42-全功能覆盖收口与未决条件清单.md) | 覆盖统计、真实环境未决项、证据要求和完整一致关门条件 |

### 2.8 webchat 对照与功能补齐

| 分册 | 内容 |
|---|---|
| [43-webchat现状覆盖与缺失功能总表](43-webchat现状覆盖与缺失功能总表.md) | webchat 已有能力、保留范围、全模块缺口、对象映射、优先级和完成判定 |
| [44-webchat统一渠道会话与API补齐规范](44-webchat统一渠道会话与API补齐规范.md) | 统一 Account、Inbox、Contact、Conversation、Message，补齐国际渠道和核心 API |
| [45-webchat自动化SLA帮助中心与通知补齐规范](45-webchat自动化SLA帮助中心与通知补齐规范.md) | Assignment Policy、Automation、Macro、Campaign、Notification、SLA、Portal 和导入批量能力 |
| [46-webchat-AI模型与插件体系补齐规范](46-webchat-AI模型与插件体系补齐规范.md) | Provider、Model、Assistant、Copilot、Document、Tool、Integration、Webhook 和 Agent Bot |
| [47-webchat开放平台安全审计与企业能力补齐规范](47-webchat开放平台安全审计与企业能力补齐规范.md) | 登录、MFA、设备、OAuth、SAML、Public、Platform、Audit、Push、Feature、套餐和迁移 |
| [48-webchat迁移兼容与完整验收规范](48-webchat迁移兼容与完整验收规范.md) | 阶段、数据映射、旧接口兼容、第三方、AI、并发、故障、长周期和完整一致关门条件 |

## 3. 阅读方式

| 目标 | 建议顺序 |
|---|---|
| 全面了解产品 | 00 → 01 → 02 → 03 至 42 |
| 核对某个入口是否已覆盖 | 23 → 30 → 对应专题分册 → 12 |
| 接入客服核心功能 | 02 → 03 → 21 → 04 → 17 → 05 → 06 → 14 → 12 |
| 接入登录和身份安全 | 02 → 13 → 16 → 12 |
| 接入自动化和报表 | 02 → 07 → 18 → 08 → 20 → 21 → 12 |
| 接入 AI 模型 | 02 → 09 → 22 → 21 → 12 |
| 接入插件或外部系统 | 02 → 10 → 27 → 19 → 29 → 11 → 12 |
| 接入第三方渠道或语音 | 02 → 04 → 17 → 15 → 26 → 29 → 12 |
| 迁移或批量管理数据 | 02 → 05 → 06 → 14 → 12 |
| 构建客户聊天界面 | 02 → 04 → 11 → 13 → 12 |
| 使用平台管理接口 | 02 → 11 → 16 → 12 |
| 管理安装级能力 | 24 → 25 → 21 → 12 |
| 接入移动端和推送 | 02 → 13 → 28 → 07 → 12 |
| 处理异步结果和失败补偿 | 30 → 31 → 12 |
| 按固定基线还原完整系统 | 32 → 33 → 34 → 35 → 36 → 37 |
| 验证是否达到完整一致 | 23 → 30 → 32 → 37，并按 33 至 36 执行横切场景 |
| 核对全部公开接口字段 | 对应模块 → 12 → 38 |
| 核对分页、查询和容量 | 02 → 对应模块 → 39 |
| 核对动作产生的全部连带结果 | 对应模块 → 19 → 31 → 40 |
| 处理第三方版本和历史兼容 | 26 → 29 → 36 → 41 |
| 查看仍需实际关闭的条件 | 42 → 37 |
| 判断 webchat 当前满足程度 | 43 → 44 至 47 |
| 补齐 webchat 核心渠道和会话 | 43 → 44 → 48 |
| 补齐 webchat 自动化、SLA 和帮助中心 | 43 → 45 → 48 |
| 补齐 webchat AI 模型和插件 | 43 → 46 → 48 |
| 补齐 webchat 安全、开放平台和企业能力 | 43 → 47 → 48 |
| 验证 webchat 是否完成整体补齐 | 43 → 44 → 45 → 46 → 47 → 48，并引用 32 至 42 的基线规则 |

## 4. 接口路径占位符

| 占位符 | 含义 |
|---|---|
| {account_id} | 租户账号 ID |
| {inbox_id} | Inbox ID |
| {contact_id} | Contact ID |
| {conversation_id} | 账号内会话显示 ID |
| {message_id} | Message ID |
| {assistant_id} | Captain Assistant ID |
| {hook_id} | Integration Hook ID |
| {identifier} | Inbox、Contact 或外部资源的公开标识 |
| {provider} | google、microsoft、instagram 等第三方渠道标识 |

## 5. 内容边界

文档统一保留以下内容：

- 产品可以提供的功能；
- 功能适用角色和前置条件；
- HTTP 方法与接口路径；
- Path、Query、Body 和 Header 字段；
- 请求字段、响应对象和事件字段；
- 状态、枚举、分页和错误；
- 权限、账号隔离、幂等和业务规则；
- 开源版、企业版、Cloud 和第三方依赖说明。
- 一致性基线、并发、默认值、生命周期、异常恢复和全量验收标准。
- 公开接口全字段定位、分页容量、业务副作用、第三方逐动作版本和未决条件关门清单。
- webchat 已有功能保留范围、缺失功能目标合同、数据迁移、旧接口兼容和完整验收规则。

## 6. 版本说明

00 至 42 分册固定 Chatwoot 4.16.0 基线；43 至 48 分册描述 webchat 达到该基线所需的补齐范围。实际可用性取决于账号 Feature、企业授权、用户角色、Inbox 类型和第三方平台配置。响应允许增加新字段，不应依赖字段顺序，也不应把当前未返回的字段解释为永久不存在。
