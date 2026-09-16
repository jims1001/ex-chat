# Chatwoot、webchat 补齐与 AI 实施文档总览

> 适用版本：Chatwoot 4.16.0
> 内容范围：全系统基础层、Chatwoot 产品功能、API、字段、权限、枚举、错误、业务规则和完整一致性验收，以及 webchat 缺口、模块化架构、L0 日志基础层、补齐合同、迁移兼容、AI 逐项实施任务和验收。

## 文档入口

- [全体功能与 API 总表](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/00-全体功能与API总表.md)：查看所有模块、功能、接口入口和主要字段；
- [完整文档目录](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/README.md)：按基础、核心业务、运营、智能扩展、开放接口和企业管理阅读。
- [全系统基础层规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot全系统基础层规范/README.md)：后续全部模块、接口、数据、插件、AI、Provider、迁移和验收的最高公共基线；
- [模块化架构设计](/Users/tong/o/act/Chatwoot完整文档/Chatwoot模块化架构设计文档/README.md)：查看模块边界、数据所有权、无环依赖、插件、对外接口和独立运行要求；
- [日志基础层设计](/Users/tong/o/act/Chatwoot完整文档/Chatwoot日志审计与变更追踪设计文档/README.md)：查看每次数据变化的原子记录、字段差异、不可变账本、查询权限、隐私、告警和 26 个模块覆盖；
- [AI 实施步骤总入口](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/README.md)：按依赖顺序执行全部功能任务；
- [AI 全任务状态记录](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/28-AI全任务状态记录.md)：逐项记录 482 个任务的状态、证据和更新时间。
- [实际基线参数与量化目标](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/29-实际基线参数与量化目标冻结表.md)：冻结容量档位、可用性、延迟、RTO/RPO 和成本目标；
- [第三方真实环境与接口冻结](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/30-第三方真实环境与最终接口冻结表.md)：登记真实 Provider、版本、合同、回执和证据有效期；
- [模块拓扑拆分合并演练](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/31-模块运行拓扑拆分合并灰度回退演练表.md)：执行 26 模块实际拓扑、影子、灰度、回退和重新合并；
- [全局证据与量化评分](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/32-全局证据包索引量化评分与完成台账.md)：汇总 482 项任务和六项目标的实际证据。

## 覆盖基线

| 检查项 | 当前结果 |
|---|---:|
| Chatwoot 基线编号分册 | 00 至 42，共 43 册 |
| webchat 补齐分册 | 43 至 48，共 6 册 |
| 全部编号分册 | 00 至 48，共 49 册 |
| 功能基线文档 | 分册目录 50 份，含 README |
| 全系统基础层分册 | 00 至 10，共 11 册；另含 README |
| 模块化架构分册 | 00 至 10，共 11 册；另含 README |
| 日志基础层分册 | 00 至 07，共 8 册；另含 README |
| AI 实施分册 | 00 至 32，共 33 册；另含 README |
| AI 实施任务 | 482 项，按 26 个架构、基础层及功能批次执行 |
| 全部文档文件 | 五个目录共 117 份；加本总览共 118 份 |
| 逐动作路径 | 430 条路径记录，展开为 651 个 HTTP 方法动作 |
| 公开接口字段 | 123 类对象、766 个字段出现位置、273 / 273 个唯一字段名称 |
| Account Feature | 68 / 68 |
| 安装配置 | 102 / 102 |
| 第三方入站回调 | 15 / 15 类 |
| Account Webhook | 12 / 12 类订阅 |
| 业务变化名称 | 37 / 37 |
| 一致性专项 | 基线、并发、默认值、生命周期、权限优先级、异常、限流和全量验收 |
| 完整一致通过门槛 | 适用案例 100%，fail 0，未解释 blocked 0，未批准差异 0 |

该基线按 Chatwoot 4.16.0 固定；第三方 Provider 后续新增但当前产品未使用的字段，不计入当前功能覆盖。

## 文档分组

| 分组 | 模块 |
|---|---|
| 基础规范 | 产品功能、认证、Profile、MFA、登录设备、错误、公共字段、权限矩阵和全接口覆盖矩阵 |
| 核心业务 | 账号权限、Inbox 渠道、渠道字段、联系人、会话消息、语音和会议 |
| 运营能力 | 自动化字典、活动、通知、报表指标、SLA、导入、筛选和批量管理 |
| 智能与扩展 | Captain、Copilot、AI 模型矩阵、Webhook 事件、第三方集成、插件、Agent Bot |
| 开放接口 | Public API、Platform API、实时事件 |
| 企业管理 | Audit、Reporting Event、SAML、Cloud 计费、平台迁移、Feature、安装配置和 Super Admin |
| 客户端 | Browser Push、FCM、移动深链、Android/iOS 域名关联和推送诊断 |
| 一致性还原 | 版本基线、并发幂等、字段生命周期、权限优先级、异常重试、字段收口、分页容量、业务副作用、Provider 版本和全量对照验收 |
| webchat 补齐 | 现状差距、统一渠道会话、自动化、SLA、帮助中心、AI、插件、安全、开放平台、迁移兼容和关门验收 |
| 全系统基础层 | 权威顺序、上下文、身份、对象、Command/Query/Event、API、数据一致性、安全、扩展、可靠性和接入治理 |
| 模块化架构 | 26 个模块边界、数据所有权、无环依赖、接口事件、插件、对外接口、流程编排、独立运行、去重和生命周期 |
| 日志基础层 | L0 原子采集、统一变更合同、本地待投递、集中不可变账本、完整性、查询、隐私、告警、恢复和 26 模块覆盖 |
| AI 实施 | 基线、公共合同、全部功能域、既有能力保留、迁移、验证、任务状态、量化目标、第三方实证、拓扑演练、证据评分和完成声明 |

## 快速入口

| 目标 | 文档 |
|---|---|
| 后续所有工作先遵守 | [全系统基础层规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot全系统基础层规范/README.md)、[模块接入与基础层验收](/Users/tong/o/act/Chatwoot完整文档/Chatwoot全系统基础层规范/09-模块接入基础清单变更治理与验收.md) |
| 查看目标系统架构 | [可扩展、简单实用、可跟踪、可量化、可分可合目标架构](/Users/tong/o/act/Chatwoot完整文档/Chatwoot模块化架构设计文档/10-可扩展简单实用可跟踪可量化可分可合目标架构.md) |
| 查看自动化与宏设计 | [自动化规则、宏与统一动作能力合同](/Users/tong/o/act/Chatwoot完整文档/Chatwoot全系统基础层规范/10-自动化规则宏与统一动作能力合同.md)、[自动化条件与动作字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/18-自动化条件操作符与动作字典.md) |
| 查看模块化总体要求 | [全系统模块边界设计](/Users/tong/o/act/Chatwoot完整文档/Chatwoot模块化架构设计文档/00-全系统模块边界设计.md)、[模块数据所有权表](/Users/tong/o/act/Chatwoot完整文档/Chatwoot模块化架构设计文档/01-模块数据所有权表.md)、[无循环依赖矩阵](/Users/tong/o/act/Chatwoot完整文档/Chatwoot模块化架构设计文档/02-无循环依赖矩阵.md) |
| 查看日志基础层 | [日志基础层总体架构](/Users/tong/o/act/Chatwoot完整文档/Chatwoot日志审计与变更追踪设计文档/00-日志基础层总体架构与分类.md)、[数据变更字段合同](/Users/tong/o/act/Chatwoot完整文档/Chatwoot日志审计与变更追踪设计文档/01-数据变更记录字段与差异合同.md)、[全模块覆盖矩阵](/Users/tong/o/act/Chatwoot完整文档/Chatwoot日志审计与变更追踪设计文档/07-全模块数据变更覆盖矩阵与验收.md) |
| 查看插件与对外能力 | [插件扩展点设计](/Users/tong/o/act/Chatwoot完整文档/Chatwoot模块化架构设计文档/04-插件扩展点设计.md)、[对外接口分层设计](/Users/tong/o/act/Chatwoot完整文档/Chatwoot模块化架构设计文档/05-对外接口分层设计.md) |
| 让 AI 从头逐项实施 | [AI 实施总控](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/00-AI实施总控与完成协议.md)、[总任务台账](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/01-总任务台账与依赖顺序.md)、[逐任务状态记录](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/28-AI全任务状态记录.md) |
| 查看功能到实施任务映射 | [功能基线到实施任务追踪矩阵](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/27-功能基线到实施任务追踪矩阵.md) |
| 查看 AI 每轮执行格式 | [AI 单任务执行、阻断与续跑模板](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/26-AI单任务执行回报阻断与续跑模板.md) |
| 冻结实际容量与量化目标 | [实际基线参数与量化目标冻结表](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/29-实际基线参数与量化目标冻结表.md) |
| 登记第三方真实合同与回执 | [第三方真实环境与最终接口冻结表](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/30-第三方真实环境与最终接口冻结表.md) |
| 执行模块拆分合并和回退 | [模块运行拓扑拆分合并灰度回退演练表](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/31-模块运行拓扑拆分合并灰度回退演练表.md) |
| 查看全局证据和完成评分 | [全局证据包索引量化评分与完成台账](/Users/tong/o/act/Chatwoot完整文档/Chatwoot-AI实施步骤文档/32-全局证据包索引量化评分与完成台账.md) |
| 核心客服功能 | [Inbox、渠道与 Widget API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/04-Inbox渠道与Widget-API.md)、[各渠道创建与更新字段字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/17-各渠道创建更新字段字典.md)、[会话、消息与分配 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/06-会话消息与分配API.md) |
| 客户资料 | [联系人、公司与标签 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/05-联系人公司与标签API.md) |
| 自动化 | [自动化、活动、通知与模板 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/07-自动化活动通知与模板API.md)、[自动化条件、操作符与动作字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/18-自动化条件操作符与动作字典.md) |
| 报表 | [报表、SLA、帮助中心与搜索 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/08-报表SLA帮助中心与搜索API.md)、[报表指标口径、筛选与返回字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/20-报表指标口径筛选与返回字典.md) |
| AI 模型 | [Captain、Copilot 与 AI API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/09-Captain-Copilot与AI-API.md)、[AI 模型、Provider 与 Feature 能力矩阵](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/22-AI模型Provider与Feature能力矩阵.md) |
| 插件和外部系统 | [集成、Webhook、Agent Bot 与插件 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/10-集成Webhook-AgentBot与插件API.md)、[第三方集成完整字段与授权 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/27-第三方集成完整字段与授权API.md)、[Webhook 与实时事件字段字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/19-Webhook与实时事件字段字典.md) |
| 客户接口和平台接口 | [Public、Platform 与实时事件 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/11-Public-Platform与实时事件API.md) |
| 字段和错误 | [公共字段、枚举、错误与验收](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/12-公共字段枚举错误与验收.md) |
| 登录与安全 | [身份、个人资料、安全与登录会话 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/13-身份资料安全与登录会话API.md) |
| 分配、导入和批量管理 | [分配策略、筛选、导入与批量操作 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/14-分配策略筛选导入与批量操作API.md) |
| 渠道授权和语音 | [渠道授权、通话、会议与高级 Inbox API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/15-渠道授权通话会议与高级Inbox-API.md)、[渠道状态、错误、重新授权与回调字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/26-渠道状态错误重新授权与回调字典.md) |
| 审计、企业设置和计费 | [审计、企业设置、计费与平台迁移 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/16-审计企业设置计费与平台迁移API.md) |
| 全角色权限 | [角色权限与接口可用性矩阵](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/21-角色权限与接口可用性矩阵.md) |
| 全接口反查 | [全接口功能与文档覆盖矩阵](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/23-全接口功能与文档覆盖矩阵.md) |
| 逐动作路径 | [全 API 逐动作路径索引](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/30-全API逐动作路径索引.md) |
| Feature 和安装配置 | [Feature 开关、版本与安装配置字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/24-Feature开关版本与安装配置字典.md) |
| 最高管理入口 | [Super Admin 与安装级管理功能](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/25-SuperAdmin与安装级管理功能.md) |
| 渠道故障和重新授权 | [渠道状态、错误、重新授权与回调字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/26-渠道状态错误重新授权与回调字典.md) |
| 第三方集成字段 | [第三方集成完整字段与授权 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/27-第三方集成完整字段与授权API.md) |
| 移动端和推送 | [移动端、推送诊断与客户端入口 API](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/28-移动端推送诊断与客户端入口API.md) |
| 第三方入站回调 | [第三方入站 Webhook 完整字段字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/29-第三方入站Webhook完整字段字典.md) |
| 异步任务状态 | [异步任务、进度、终态与补偿规则](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/31-异步任务进度终态与补偿规则.md) |
| 一致性目标和固定基线 | [完整系统一致性还原目标与基线](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/32-完整系统一致性还原目标与基线.md) |
| 并发、幂等和状态竞争 | [并发、原子性、幂等与状态竞争规则](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/33-并发原子性幂等与状态竞争规则.md) |
| 默认值、约束和生命周期 | [字段默认、空值、唯一约束与数据生命周期](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/34-字段默认空值唯一约束与数据生命周期.md) |
| 权限、Feature 和兼容优先级 | [权限、Feature、套餐、配置与兼容优先级](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/35-权限Feature套餐配置与兼容优先级.md) |
| 异常、重试、限流和第三方差异 | [异常、超时、重试、限流与第三方差异规则](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/36-异常超时重试限流与第三方差异规则.md) |
| 完整系统对照验收 | [完整系统一致性对照验收规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/37-完整系统一致性对照验收规范.md) |
| 公开接口遗漏字段 | [API 请求与响应遗漏字段补充字典](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/38-API请求响应遗漏字段补充字典.md) |
| 分页、查询和容量边界 | [列表查询、分页、排序、筛选与容量边界](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/39-列表查询分页排序筛选与容量边界.md) |
| 业务动作全部副作用 | [业务动作、事件、副作用与跨入口一致性](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/40-业务动作事件副作用与跨入口一致性.md) |
| Provider 版本和历史兼容 | [第三方 API 版本、能力漂移与历史兼容](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/41-第三方API版本能力漂移与历史兼容.md) |
| 覆盖收口和未决条件 | [全功能覆盖收口与未决条件清单](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/42-全功能覆盖收口与未决条件清单.md) |
| webchat 现状与缺口 | [webchat 现状覆盖与缺失功能总表](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/43-webchat现状覆盖与缺失功能总表.md) |
| webchat 渠道、会话与核心 API | [webchat 统一渠道、会话与 API 补齐规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/44-webchat统一渠道会话与API补齐规范.md) |
| webchat 自动化、SLA、通知与帮助中心 | [webchat 自动化、SLA、帮助中心与通知补齐规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/45-webchat自动化SLA帮助中心与通知补齐规范.md) |
| webchat AI 模型与插件 | [webchat AI 模型与插件体系补齐规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/46-webchat-AI模型与插件体系补齐规范.md) |
| webchat 安全、开放平台与企业能力 | [webchat 开放平台、安全、审计与企业能力补齐规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/47-webchat开放平台安全审计与企业能力补齐规范.md) |
| webchat 迁移兼容与最终验收 | [webchat 迁移、兼容与完整验收规范](/Users/tong/o/act/Chatwoot完整文档/Chatwoot产品与API文档/48-webchat迁移兼容与完整验收规范.md) |
