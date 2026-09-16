# webchat 开放平台、安全、审计与企业能力补齐规范

> 对照基线：02、03、11、13、16、21、24、25、28、33、35、36 和 39 分册
> 目标：补齐统一认证、MFA、登录设备、OAuth、SAML、Public API、Platform API、审计、Feature、移动推送、套餐和迁移能力。
> 边界：本文只描述功能、接口、字段、状态和验收，不包含代码实现。

## 1. 当前能力与补齐方向

| 功能域 | webchat 当前能力 | 目标能力 |
|---|---|---|
| 登录 | 用户名密码、微信登录和部分验证码 | 统一登录、Token 验证、退出、密码重置和会话撤销 |
| MFA | 动态令牌 | 启用、确认、备用恢复码、禁用和恢复流程 |
| 登录控制 | IP 白名单、登录审批、自动锁定、密码周期 | 登录设备、风险记录、全部退出和企业 SSO |
| 角色权限 | 组织、角色和菜单权限 | Account 成员、Administrator、Agent、Custom Role 和对象范围 |
| API | 自有业务接口和密钥 | Account、Widget、Public、Platform、Webhook 和 Provider 身份分离 |
| 审计 | 用户、登录、请求和系统日志 | 业务对象变更审计、查询、导出和保留策略 |
| 多租户 | 租户和租户账号 | Platform App 管理 Account、User、成员关系和生命周期 |
| 推送 | 部分 Android 推送 | Browser、Android、iOS、Token 轮换、偏好和深链 |
| Feature | 版本授权、配置和租户类型 | Account Feature、安装配置、套餐、额度和联合可用性 |
| 迁移 | 导入导出和租户管理 | Email、账号和历史数据迁移状态与回滚边界 |

## 2. 认证类型

| 身份 | 使用范围 | 不允许访问 |
|---|---|---|
| Account Member Token | Account 工作台和管理接口 | 无成员关系的 Account、Platform 管理接口 |
| Widget Session | 当前网站 Inbox 和 Contact | Account 管理、其他 Contact 和其他 Inbox |
| Public Client Identity | 指定 Inbox 下的公开客户资源 | Account 管理和其他客户资源 |
| Platform App Token | Platform App 授权的账号和用户 | 未授权 Platform App 或普通成员资源 |
| Webhook Signature | 验证业务事件来源 | 不能代替任何 REST 身份 |
| Provider Signature | 验证第三方回调 | 不能读取 Account API |
| Realtime Token | 当前账号或客户的事件订阅 | 不得提升到 REST 写权限 |

相同 Header 名称不得让不同身份自动互换。认证成功只证明身份有效，仍必须继续判断 Account、角色、Inbox 成员、对象归属、Feature、套餐和第三方状态。

## 3. 登录与 Token

### 3.1 登录流程

邮箱或用户名登录 → 密码校验 → 风险和锁定检查 → MFA 或登录审批 → 创建设备会话 → 返回认证信息 → 获取 Profile 和 Account 列表。

### 3.2 接口

| 方法 | 路径 | 功能 | 主要字段 |
|---|---|---|---|
| POST | /auth/sign_in | 登录 | email、password、device_name |
| DELETE | /auth/sign_out | 退出当前会话 | 认证 Header |
| GET | /auth/validate_token | 验证当前 Token | access-token、client、uid |
| POST | /auth/password | 请求密码重置 | email、redirect_url |
| PUT | /auth/password | 设置新密码 | reset_password_token、password、password_confirmation |
| GET | /api/v1/profile | 获取个人资料 | 无 |
| PATCH | /api/v1/profile | 更新个人资料 | name、display_name、avatar、ui_settings |
| POST | /api/v1/profile/availability | 更新账号内可用状态 | account_id、availability |

### 3.3 Token 和会话字段

| 字段 | 含义 |
|---|---|
| access-token | 当前访问 Token |
| client | 当前设备或客户端标识 |
| uid | 用户稳定身份 |
| expiry | 到期时间 |
| token_type | Token 类型 |
| session_id | 登录设备会话标识 |
| issued_at | 签发时间 |

Token 轮换后旧 Token 的宽限和失效时间必须明确；退出、改密、账号锁定和管理员撤销应按安全策略使相关会话失效。

## 4. Profile、MFA 与登录设备

### 4.1 Profile 字段

| 字段 | 含义 |
|---|---|
| id | 用户标识 |
| name | 姓名 |
| display_name | 对外展示名 |
| email | 登录邮箱 |
| avatar_url | 头像 |
| availability | 可用状态 |
| ui_settings | 个人界面设置 |
| accounts | 所属 Account 摘要 |
| mfa_enabled | MFA 是否启用 |
| confirmed | 身份是否确认 |

### 4.2 MFA 接口

| 方法 | 路径 | 功能 | 主要字段 |
|---|---|---|---|
| GET、POST | /api/v1/profile/mfa | 获取配置或启用 MFA | password、otp_code |
| POST | /api/v1/profile/mfa/verify | 确认 OTP | otp_code |
| POST | /api/v1/profile/mfa/backup_codes | 重新生成备用码 | password 或 otp_code |
| DELETE | /api/v1/profile/mfa | 禁用 MFA | password、otp_code |

备用码只在生成时完整显示一次；每个备用码只能使用一次。禁用 MFA、重新生成密钥或恢复账号必须生成安全审计记录。

### 4.3 登录设备字段与接口

| 字段 | 含义 |
|---|---|
| id | Session 标识 |
| device_name | 设备名称 |
| platform | Web、Android、iOS 或其他 |
| ip_address | 最近 IP |
| user_agent | 客户端摘要 |
| location | 估算位置，可为空 |
| last_activity_at | 最近活动时间 |
| current | 是否当前会话 |
| created_at | 创建时间 |

接口：GET `/api/v1/profile/sessions`；DELETE `/api/v1/profile/sessions/{session_id}`；DELETE `/api/v1/profile/sessions` 退出其他设备。

## 5. OAuth 与 SAML

### 5.1 OAuth

Chatwoot 4.16.0 基线包含 Google OAuth 登录；Microsoft 在本套基线中用于 Email 渠道授权，不应直接写成已经存在的登录 Provider。webchat 可以保留微信登录，其他登录 Provider 必须单独标记为扩展能力。

主要流程：开始授权 → 保存 state 和目标地址 → Provider 回调 → 校验 state、code 和邮箱 → 关联或创建允许的用户 → 返回短期 SSO Token → 建立本地会话。

接口：

- GET `/omniauth/google_oauth2/callback`：处理 Google OAuth 登录回调；
- POST `/api/v1/auth/saml_login`：发起 SAML 登录；
- GET 或 POST `/auth/saml`：进入 SAML Provider 认证；
- GET `/omniauth/saml/callback`：处理 SAML 回调。

OAuth 不能只依赖邮箱字符串自动合并高权限账号；账号关联必须有明确确认和审计。

### 5.2 SAML 设置字段

| 字段 | 含义 |
|---|---|
| enabled | 是否启用 |
| sso_url | IdP 登录地址 |
| certificate | IdP 证书，读取时脱敏或只返回摘要 |
| entity_id | SP 实体标识 |
| name_identifier_format | NameID 格式 |
| email_attribute | 邮箱属性 |
| name_attribute | 姓名属性 |
| role_mappings | IdP 角色到 Account 角色映射 |
| allow_account_creation | 是否允许自动创建成员 |
| default_role | 自动创建时默认角色 |

接口：GET、POST、PATCH、PUT、DELETE `/api/v1/accounts/{account_id}/saml_settings`；POST `/api/v1/auth/saml_login`；GET 或 POST `/auth/saml`；GET `/omniauth/saml/callback`。

SAML 响应必须校验签名、Audience、Recipient、时间和重放。角色降级、成员移除和 IdP 禁用后不能继续使用旧授权访问。

## 6. 账号成员、角色与权限

### 6.1 标准角色

| 角色 | 主要能力 |
|---|---|
| Administrator | 管理 Account、成员、Inbox、自动化、AI、集成和设置 |
| Agent | 处理授权 Inbox 中的会话和客户 |
| Custom Role | 按自定义权限访问指定模块和动作 |
| Platform App | 在授权平台范围创建和管理 Account、User 和成员关系 |
| Contact | 只访问自己的 Widget 或 Public 资源 |

### 6.2 权限判断顺序

1. 身份类型是否允许访问该接口族；
2. 用户或应用是否属于目标 Account；
3. 角色是否允许该功能；
4. Inbox 成员或 Team 范围是否允许访问；
5. 资源是否确实属于同一 Account；
6. Account Feature、套餐和安装配置是否启用；
7. 第三方连接和 Provider 健康是否允许执行；
8. 对象当前状态是否允许该动作。

### 6.3 主要 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET、POST | /api/v1/accounts/{account_id}/agents | 查询或邀请成员 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/agents/{agent_id} | 详情、角色、状态和移除 |
| GET、POST | /api/v1/accounts/{account_id}/teams | 查询或创建 Team |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/teams/{team_id} | 维护 Team |
| GET、POST | /api/v1/accounts/{account_id}/custom_roles | 查询或创建自定义角色 |
| GET、PATCH、DELETE | /api/v1/accounts/{account_id}/custom_roles/{role_id} | 维护自定义角色 |
| GET、POST、DELETE | /api/v1/accounts/{account_id}/inbox_members | 管理 Inbox 成员 |

## 7. Public API

### 7.1 使用范围

Public API 用于自建客户界面或外部客户应用，不代表 Account 管理权限。每个请求只能操作指定 Inbox 和指定 Contact 的资源。

### 7.2 主要接口

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts | 创建或识别客户 |
| GET、PATCH | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier} | 获取或更新客户 |
| GET、POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations | 查询或创建会话 |
| GET | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{conversation_id} | 会话详情 |
| GET、POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{conversation_id}/messages | 查询或发送消息 |
| GET、PATCH、PUT | /public/api/v1/csat_survey/{survey_id} | 获取或提交满意度 |

创建字段包括 identifier、name、email、phone_number 和 custom_attributes；消息字段包括 content、echo_id 和 attachments。所有公开标识必须稳定、可撤销并受限流保护。

## 8. Platform API

### 8.1 对象

| 对象 | 功能 |
|---|---|
| Platform App | 平台控制面身份和授权范围 |
| Platform User | 跨账号用户主体 |
| Platform Account | 平台创建的租户账号 |
| Account User | User 与 Account 的成员关系 |
| Platform Token | 平台应用访问凭证 |

### 8.2 主要接口

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /platform/api/v1/users | 创建用户 |
| GET、PATCH、DELETE | /platform/api/v1/users/{user_id} | 用户详情、更新或删除 |
| GET、POST | /platform/api/v1/accounts | 查询或创建 Account |
| GET、PATCH、DELETE | /platform/api/v1/accounts/{account_id} | Account 详情、更新或生命周期动作 |
| GET、POST、DELETE | /platform/api/v1/accounts/{account_id}/account_users | 查询、建立或移除成员关系 |
| GET、POST | /platform/api/v1/agent_bots | 查询或创建全局 Bot |

Chatwoot 4.16.0 基线未提供独立的 Platform API Channel Inbox 路径；平台侧 API Inbox 如需扩展，必须单独标记为 webchat 扩展合同。

Platform App 不能读取未授权平台范围外的账号。删除 User 或 Account 必须先判断成员关系、历史数据、计费和等待期。

## 9. API 通用合同

### 9.1 分页

列表统一支持 page、per_page；需要游标的高频对象支持 cursor。响应提供 meta.current_page、meta.total_pages、meta.total_count 或 next_cursor。排序必须稳定，翻页期间不能因相同时间值产生重复或遗漏。

### 9.2 错误结构

| 字段 | 含义 |
|---|---|
| error.code | 稳定错误码 |
| error.message | 可读说明 |
| error.details | 字段级或业务级详情 |
| error.request_id | 请求追踪标识 |
| error.retryable | 是否可重试 |
| error.retry_after | 建议重试时间，可为空 |

主要错误类型：authentication_failed、permission_denied、resource_not_found、validation_failed、conflict、feature_disabled、quota_exceeded、rate_limited、provider_error、temporary_unavailable。

### 9.3 幂等

创建 Contact、Conversation、Message、Campaign、Import、Tool Call 和支付相关动作支持 Idempotency-Key 或业务幂等字段。相同身份、相同键和相同请求应返回已有结果；同键不同请求应返回冲突。

## 10. 审计与 Reporting Event

### 10.1 Audit Log 字段

| 字段 | 含义 |
|---|---|
| id | 审计记录标识 |
| account_id | Account |
| action | create、update、delete、login、export、execute 等 |
| auditable_type | 资源类型 |
| auditable_id | 资源标识 |
| user_id | 操作者，可为空 |
| username | 当时显示名 |
| ip_address | 来源 IP |
| user_agent | 客户端摘要 |
| audited_changes | 允许记录的前后变化 |
| request_id | 请求追踪标识 |
| created_at | 时间 |

敏感凭证、密码、Token、完整认证 Header、MFA Secret 和 Provider Secret 不得进入审计变化值。

### 10.2 必审计动作

- 登录、退出、MFA、密码、设备会话和 SSO；
- 成员、角色、Team、Inbox 成员和权限；
- Inbox、渠道授权、Provider 凭证和重新授权；
- Automation、Macro、SLA、Campaign 和 Help Center 发布；
- Assistant、Document、Tool、AI Provider 和额度；
- Plugin 安装、配置、重新授权和卸载；
- Webhook 创建、Secret 轮换、停用和重放；
- 批量更新、导入、导出、删除和联系人合并；
- 套餐、Feature、额度、计费和迁移。

### 10.3 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/audit_logs | 查询审计 |
| GET | /api/v1/accounts/{account_id}/reporting_events | 查询 Account 指标事件 |
| GET | /api/v1/accounts/{account_id}/conversations/{conversation_id}/reporting_events | 查询会话指标事件 |

审计记录原则上不可修改。保留期、归档、导出和删除例外必须有明确策略。

## 11. 移动推送和深链

### 11.1 Subscription 字段

| 字段 | 含义 |
|---|---|
| id | 订阅标识 |
| user_id | 用户 |
| subscription_type | browser、fcm、apns |
| push_token | 推送 Token，读取时脱敏 |
| device_id | 设备稳定标识 |
| platform | web、android、ios |
| app_version | 应用版本 |
| locale | 语言 |
| enabled | 是否启用 |
| last_success_at | 最近成功 |
| failure_count | 连续失败次数 |
| created_at、updated_at | 时间 |

### 11.2 规则

- 同一 Provider Token 只能存在一个有效归属；
- Token 轮换后旧 Token 停用，新 Token 继承允许的偏好；
- Provider 明确返回无效 Token 时停止继续发送；
- 推送包含最少必要信息，敏感正文是否显示由账号和设备策略决定；
- 深链必须携带 Account 和资源标识，并在打开时重新校验权限；
- 已退出、权限移除、会话删除或消息删除后，旧推送不能绕过当前权限。

### 11.3 诊断接口

POST `/api/v1/notification_subscriptions` 注册 Browser Push 或 FCM，DELETE `/api/v1/notification_subscriptions` 删除当前用户订阅；推送测试和诊断使用 GET、POST `/super_admin/push_diagnostics`，失效订阅清理使用 POST `/super_admin/push_diagnostics/destroy_subscriptions`。

## 12. Feature、安装配置和套餐

### 12.1 联合可用性

一个功能可用必须同时满足：

1. 当前版本支持；
2. Account Feature 已启用；
3. 安装配置允许；
4. 套餐包含该功能；
5. 当前用量未超过额度；
6. 角色和对象范围允许；
7. Provider 或外部服务可用；
8. 对象状态允许执行。

### 12.2 Feature 字段

| 字段 | 含义 |
|---|---|
| feature_key | 稳定功能键 |
| enabled | 是否启用 |
| source | default、plan、account_override、trial |
| effective_at | 生效时间 |
| expires_at | 到期时间，可为空 |
| configuration | 功能特有配置 |

### 12.3 套餐和额度字段

| 字段 | 含义 |
|---|---|
| plan_id、plan_name | 套餐 |
| billing_cycle | 月、年或其他周期 |
| currency | 币种 |
| seats_allowed、seats_used | 成员额度 |
| contacts_allowed、contacts_used | 联系人额度 |
| ai_units_allowed、ai_units_used | AI 额度 |
| channels_allowed、channels_used | 渠道额度 |
| storage_allowed、storage_used | 存储额度 |
| period_start、period_end | 当前周期 |
| status | trial、active、past_due、cancelled、expired |

额度耗尽时应阻止新增或高成本动作，不得阻止读取历史数据和导出允许的数据。

## 13. Cloud 计费与生命周期

如目标形态包含 Cloud，至少支持：

- 新订阅、试用、升级、降级和取消；
- 席位、联系人、AI、渠道和存储额度；
- Top-up、支付成功、失败、退款和未知结果；
- 支付事件去重和账务对账；
- Account 计划删除、等待期、取消删除和最终清理；
- 多币种、金额精度、舍入和税务展示。

接口以 `/enterprise/api/v1/accounts/{account_id}` 为前缀，至少包括 POST `/checkout`、POST `/subscription`、POST `/select_billing_currency`、GET `/limits`、POST `/toggle_deletion`、POST `/topup_checkout` 和 GET `/topup_options`。

## 14. 平台迁移

### 14.1 迁移对象

- Account、User 和成员关系；
- Inbox 和渠道授权；
- Contact、ContactInbox、Company 和 Label；
- Conversation、Message、Attachment 和 Satisfaction；
- 工单、质检、申诉和报表关联；
- Automation、SLA、Macro、Help Center、Assistant 和 Plugin；
- 登录、审计、推送订阅和 Feature 配置。

### 14.2 迁移状态

draft、validating、ready、running、partially_completed、completed、failed、cancelled、rolled_back。

每个迁移任务必须返回总数、已处理、成功、失败、跳过、错误文件、开始时间、结束时间和是否可重试。第三方凭证不能通过普通导出明文迁移；必须重新授权或使用受控密钥迁移流程。

Chatwoot 4.16.0 的公开迁移入口包括 POST `/platform/api/v1/accounts/{account_id}/email_channel_migrations`；其他对象迁移如果增加新接口，必须标记为 webchat 扩展合同。

## 15. 验收清单

### 15.1 身份与安全

- 登录、退出、Token 验证、改密和密码重置形成闭环；
- MFA 启用、备用码、禁用、恢复和错误次数限制可验证；
- 登录设备可以查询、单独撤销和全部撤销；
- OAuth 和 SAML 的 state、签名、时间、Audience、角色和重放规则有效；
- IP 白名单、登录审批、自动锁定和密码周期继续有效；
- 跨 Account、跨 Inbox 和跨身份接口访问全部被拒绝且不泄露资源存在性。

### 15.2 开放平台

- Public API 只能访问当前 Contact 和 Inbox；
- Platform API 只能管理授权平台范围；
- Account、Widget、Public、Platform、Webhook 和 Realtime 身份不能混用；
- 分页、排序、筛选、错误和幂等符合统一合同；
- Account 和 User 删除、停用、恢复和迁移的状态可确认。

### 15.3 审计、推送与套餐

- 必审计动作均有不可修改记录，敏感字段不会被写入；
- Browser、Android、iOS Token 创建、轮换、失效、测试和删除形成闭环；
- 深链在打开时重新校验 Account、角色和资源；
- Feature、配置、套餐、额度和 Provider 状态联合判断一致；
- 升降级、额度耗尽、支付事件重放、账号删除等待和恢复均有确定结果；
- 迁移完成后主对象、搜索、报表、导出和审计最终对账一致。
