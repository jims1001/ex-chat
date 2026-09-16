# 审计、企业设置、计费与平台迁移 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

本模块补充 Audit Log、Reporting Event、SAML 设置、账号引导、品牌邮件模板、Cloud 额度与计费，以及 Platform Email Channel Migration。不同功能分别受 Administrator、Enterprise、Cloud、Feature 和 Platform App 权限限制。

## 2. Audit Log API（Enterprise）

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/audit_logs | 账号审计记录 | Administrator |

### 2.1 Query 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| page | integer | 页码，每页 25 条 |

### 2.2 Audit Log 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| per_page | integer | 每页数量 |
| total_entries | integer | 总记录数 |
| current_page | integer | 当前页 |
| audit_logs | array | 审计记录 |
| audit_logs.id | integer | Audit ID |
| audit_logs.auditable_id | integer 或 null | 被操作对象 ID |
| audit_logs.auditable_type | string | 被操作对象类型 |
| audit_logs.auditable | object 或 null | 对象摘要，按资源提供 |
| audit_logs.associated_id | integer 或 null | 关联对象 ID |
| audit_logs.associated_type | string 或 null | 关联对象类型 |
| audit_logs.user_id | integer 或 null | 操作用户 ID |
| audit_logs.user_type | string 或 null | 操作身份类型 |
| audit_logs.username | string 或 null | 操作人名称 |
| audit_logs.action | string | create、update、destroy 等动作 |
| audit_logs.audited_changes | object | 发生变化的字段 |
| audit_logs.version | integer | 对象审计版本 |
| audit_logs.comment | string 或 null | 补充说明 |
| audit_logs.request_uuid | string 或 null | 请求关联标识 |
| audit_logs.remote_address | string 或 null | 请求来源地址 |
| audit_logs.created_at | integer | 记录时间 |

审计记录可能包含旧值、新值和身份信息，应限制导出和查看范围。未启用 audit_logs Feature 时列表为空。

该入口在完整系统中作为 [L0 日志基础层](../Chatwoot日志审计与变更追踪设计文档/README.md) 的兼容业务视图，不建立第二套审计数据。完整数据变化查询、对象时间线、流程时间线、受控导出和完整性验证接口见 [日志查询 API、权限、导出与展示](../Chatwoot日志审计与变更追踪设计文档/04-日志查询API权限导出与展示.md)。

## 3. Reporting Event API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/reporting_events | 账号范围服务指标事件 |
| GET | /api/v1/accounts/{account_id}/conversations/{conversation_id}/reporting_events | 单个会话的指标事件 |

### 3.1 Query 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| since | integer | 起始 Unix 时间 |
| until | integer | 结束 Unix 时间 |
| inbox_id | integer | 按 Inbox 筛选 |
| user_id | integer | 按成员筛选 |
| name | string | 按事件名称筛选 |
| page | integer | 页码，每页 25 条 |

### 3.2 Reporting Event 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Event ID |
| name | string | 指标事件名称 |
| value | number | 指标值 |
| value_in_business_hours | number 或 null | 仅工作时间内的指标值 |
| event_start_time | time | 计时开始 |
| event_end_time | time | 计时结束 |
| account_id | integer | Account ID |
| inbox_id | integer | Inbox ID |
| user_id | integer 或 null | 相关成员 |
| conversation_id | integer | 内部 Conversation ID |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

Reporting Event 是报表明细基础，不应直接等同于最终汇总报表。工作时间配置变化可能影响 value_in_business_hours 的解释。

## 4. SAML Settings API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/saml_settings | 获取 SAML 配置 |
| POST | /api/v1/accounts/{account_id}/saml_settings | 创建 SAML 配置 |
| PATCH | /api/v1/accounts/{account_id}/saml_settings | 更新 SAML 配置 |
| DELETE | /api/v1/accounts/{account_id}/saml_settings | 删除 SAML 配置 |

### 4.1 SAML 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Settings ID |
| account_id | integer | Account ID |
| sso_url | string | Identity Provider 登录地址 |
| certificate | string | Identity Provider 证书 |
| fingerprint | string | 证书指纹，响应字段 |
| idp_entity_id | string | Identity Provider Entity ID |
| sp_entity_id | string | Service Provider Entity ID |
| role_mappings | object | Identity Provider 角色到 Account 角色的映射 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

SAML 需要全局启用和当前 Account 的 saml Feature。删除或错误更新配置可能导致成员无法通过 SSO 登录，变更前应保留可用的其他 Administrator 登录方式。

## 5. Onboarding API

| 方法 | 路径 | 功能 |
|---|---|---|
| PATCH | /api/v1/accounts/{account_id}/onboarding | 完成当前引导步骤 |
| GET | /api/v1/accounts/{account_id}/onboarding/help_center_generation | 获取帮助中心生成状态 |

### 5.1 Onboarding 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| onboarding_step | enum | account_details 或 inbox_setup |
| name | string | Account 名称 |
| locale | string | 默认语言 |
| industry | string 或 null | 行业 |
| company_size | string 或 null | 公司规模 |
| timezone | string 或 null | 时区 |
| referral_source | string 或 null | 来源渠道 |
| user_role | string 或 null | 当前用户职责 |
| website | string 或 null | 网站 |

### 5.2 Help Center Generation 状态字段

| 字段 | 类型 | 说明 |
|---|---|---|
| generation_id | string 或 null | 生成任务标识 |
| state | string 或 null | 生成状态 |
| articles_count | integer | 已生成文章数 |
| categories_count | integer | 已生成分类数 |

引导步骤要求按当前 Account 状态顺序完成。重复提交已经完成的旧步骤不应被当作新的引导流程。

## 6. Branded Email Layout API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/branded_email_layout | 获取账号邮件布局 |
| PATCH | /api/v1/accounts/{account_id}/branded_email_layout | 更新或清除账号邮件布局 |

| 字段 | 类型 | 说明 |
|---|---|---|
| branded_email_layout | string 或 null | 邮件布局内容；null 表示清除 Account 覆盖 |

布局必须保留邮件正文插入位置。该 Account 布局作为默认值，Email Inbox 还可以具有自身布局。功能需要 branded_email_templates Feature。

## 7. Cloud Account Billing API

所有接口位于 /enterprise/api/v1/accounts/{account_id}，主要用于 Chatwoot Cloud。

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /subscription | 创建或确认订阅客户 |
| POST | /select_billing_currency | 选择计费币种 |
| POST | /checkout | 获取计费管理地址 |
| GET | /limits | 获取账号额度和用量 |
| POST | /toggle_deletion | 标记或取消账号删除 |
| POST | /topup_checkout | 创建额度充值结算 |
| GET | /topup_options | 获取可选充值额度 |

### 7.1 Currency 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| currency | string | 选择的受支持币种 |
| currency_selection_required | boolean | 是否必须先选择币种 |
| currency_options | string array | 可选币种 |
| suggested_currency | string | 根据 Account locale 建议的币种 |

币种一旦建立计费客户或开始创建客户后即锁定，不能通过重复请求切换。

### 7.2 Limits 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Account ID |
| limits | object | 各资源额度 |
| limits.{resource}.allowed | integer 或 null | 允许数量 |
| limits.{resource}.consumed | integer 或 null | 已使用数量 |
| limits.agents | object | 成员额度 |
| limits.conversation | object | 会话额度 |
| limits.non_web_inboxes | object | 非 Web Inbox 额度 |
| limits.captain | object | Captain 额度，按套餐提供 |

### 7.3 Checkout 与 Top-up 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| redirect_url | string | 计费管理地址 |
| credits | integer | 充值额度，必须为正数并符合可选范围 |
| currency | string | 当前计费币种 |
| options | array | 可购买额度选项 |
| custom_attributes | object | 充值后更新的账号计费信息，按响应提供 |

### 7.4 Account Deletion 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| action_type | enum | delete 或 undelete |
| message | string | 标记结果 |

标记删除可能同时影响 Cloud 订阅。它不是普通资源删除操作，应在展示 Account、订阅和数据影响后再执行。

## 8. Platform Email Channel Migration API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /platform/api/v1/accounts/{account_id}/email_channel_migrations | 批量迁移 Google 或 Microsoft Email Channel |

### 8.1 Migration 请求字段

单次最多接受 25 个 migrations 条目。

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| migrations | array | 是 | 迁移条目 |
| migrations[].email | string | 是 | Email Channel 邮箱 |
| migrations[].provider | enum | 是 | google 或 microsoft |
| migrations[].inbox_name | string | 否 | Inbox 名称 |
| migrations[].provider_config | object | 按 Provider | Provider 凭证和设置 |
| migrations[].imap_enabled | boolean | 否 | 是否启用 IMAP，默认 true |
| migrations[].imap_address | string | 否 | IMAP 地址，省略时使用 Provider 默认值 |
| migrations[].imap_port | integer | 否 | IMAP 端口，默认 993 |
| migrations[].imap_login | string | 否 | IMAP 登录名，默认使用 email |
| migrations[].imap_enable_ssl | boolean | 否 | 是否启用 SSL，默认 true |

### 8.2 Migration 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| results | array | 每条迁移结果 |
| results[].email | string | 邮箱 |
| results[].status | enum | success 或 error |
| results[].inbox_id | integer 或 null | 成功创建的 Inbox ID |
| results[].channel_id | integer 或 null | 成功创建的 Channel ID |
| results[].message | string 或 null | 失败原因 |

每个条目独立返回成功或失败，部分成功是正常结果。Platform App 必须拥有目标 Account 权限，同时全局 Email Channel Migration 开关必须启用。

## 9. 企业回调入口

| 方法 | 路径 | 功能 | 校验重点 |
|---|---|---|---|
| POST | /enterprise/webhooks/stripe | 接收订阅、付款和客户状态事件 | Stripe-Signature 与安装 Webhook Secret |
| POST | /enterprise/webhooks/firecrawl | 接收 Captain 网页抓取页面结果 | assistant_id、token 和事件类型 |

Stripe 回调使用原始请求体和签名校验，验证失败返回 400，不能修改 Account 计费状态。重复事件应按 Stripe Event ID 保持幂等。

Firecrawl 回调主要处理 type=crawl.page。请求字段包括 type、assistant_id、token、success、id、metadata、format、firecrawl，以及 data[].markdown 和 data[].metadata。token 必须与目标 Assistant 和 Account 匹配；其他事件类型可以确认接收，但不生成知识页面。

两类回调都不使用 Account API Token，也不向第三方返回 Account Secret、AI API Key 或完整客户资料。

## 10. 管理规则

- Audit、SAML、Billing 和 Migration 都属于高权限能力；
- certificate、provider_config、计费信息和审计变化内容可能包含敏感数据；
- 空列表不一定代表没有历史数据，也可能是 Feature 未启用；
- Cloud Billing 接口不能假设在自托管环境可用；
- Platform Migration 的单条失败不应回滚其他成功条目；
- SAML 和账号删除变更前应确认恢复路径和替代登录方式。
