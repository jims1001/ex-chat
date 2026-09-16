# Chatwoot 全体功能与 API 总表

> 适用版本：Chatwoot 4.16.0
> 内容范围：产品功能、API 路径、字段、权限、枚举、错误和业务规则。

## 1. 文档结构

整套文档按六个功能层次组织。

| 功能层 | 包含内容 | 对应分册 |
|---|---|---|
| 基础规范 | 产品边界、认证、分页、错误、公共字段、身份安全和权限矩阵 | 01、02、12、13、21 |
| 核心业务 | 账号、成员、Inbox、联系人、会话、消息、高级渠道和渠道字段 | 03、04、05、06、15、17 |
| 运营能力 | 自动化、活动、通知、报表、SLA、导入、批量管理和指标口径 | 07、08、14、18、20 |
| 智能与扩展 | Captain、Copilot、AI、Webhook、Bot、第三方集成和事件字典 | 09、10、19、22 |
| 开放接口 | Public API、Platform API、实时事件 | 11、19 |
| 企业与平台管理 | 审计、SAML、Cloud 计费、平台迁移、Feature、安装配置和 Super Admin | 16、24、25 |
| 渠道与集成运行状态 | 授权失效、重新授权、Provider 回调、第三方连接和移动推送 | 26、27、28 |

## 2. 模块总览

| 模块 | 主要功能 | 核心对象 | API 主路径 | 可用性重点 |
|---|---|---|---|---|
| 产品与公共规范 | 角色、流程、认证、分页、错误 | Account、User、Token | /api/v1、/api/v2 | 接口类型和身份必须匹配 |
| 账号与权限 | 账号设置、成员、团队、角色、容量 | Account、Agent、Team、Role | /api/v1/accounts/{account_id} | 受角色、成员关系和企业授权限制 |
| Inbox 与渠道 | 渠道接入、成员、工作时间、Widget | Inbox、Channel、ContactInbox | /api/v1/accounts/{account_id}/inboxes | 字段随渠道类型变化 |
| 联系人与公司 | 客户档案、渠道身份、公司、标签、备注 | Contact、Company、Label、Note | /api/v1/accounts/{account_id}/contacts | identifier 与渠道身份需要稳定 |
| 会话与消息 | 会话创建、状态、分配、消息、附件 | Conversation、Message、Attachment | /api/v1/accounts/{account_id}/conversations | 使用账号内会话显示 ID |
| 自动化与运营 | 规则、Macro、快捷回复、活动、通知 | AutomationRule、Macro、Campaign | /api/v1/accounts/{account_id} | 事件、条件和动作必须匹配 |
| 报表与服务质量 | 报表、实时指标、SLA、CSAT、帮助中心 | Report、SLA、Survey、Portal、Article | /api/v2/accounts/{account_id}、/api/v1/accounts/{account_id} | 时间范围、时区和数据延迟会影响结果 |
| AI 能力 | 模型偏好、AI 任务、Assistant、知识、Copilot | Assistant、Document、FAQ、Scenario、Tool | /api/v1/accounts/{account_id}/captain | 受 Feature、额度、模型和授权限制 |
| 集成与插件 | Integration、Webhook、Dashboard App、Agent Bot | App、Hook、Webhook、AgentBot | /api/v1/accounts/{account_id}/integrations | 第三方凭证和回调状态决定可用性 |
| 开放接口 | 客户侧接口、平台管理、实时订阅 | Public Contact、Platform User、Event | /public/api/v1、/platform/api/v1 | 三类身份不能混用 |
| 身份与安全 | 登录、资料、MFA、登录设备、推送订阅 | Profile、MFA、Session | /auth、/api/v1/profile | 认证头和安全令牌必须妥善保存 |
| 分配与批量管理 | 分配策略、保存筛选、批量更新、数据导入 | AssignmentPolicy、CustomFilter、DataImport | /api/v1/accounts/{account_id} | 批量和导入结果需要再次确认 |
| 高级渠道与通话 | OAuth、WhatsApp、Twilio、模板、Call、Conference | Channel、Call、Conference | /api/v1/accounts/{account_id} | 受 Provider 能力和企业授权限制 |
| 企业设置 | Audit、Reporting Event、SAML、Billing、Migration | AuditLog、SamlSettings、Limit、Migration | /api/v1、/enterprise/api/v1、/platform/api/v1 | 高权限且可能包含敏感信息 |
| 专项字段字典 | 渠道字段、自动化、事件、报表、权限、AI 模型 | Channel、Rule、Event、Metric、Role、Feature | 对应业务主路径 | 用于精确查询字段和边界 |

## 3. 核心对象关系

| 上级对象 | 下级对象 | 关系说明 |
|---|---|---|
| Account | Agent、Team、Inbox、Contact、Conversation | Account 是数据与权限边界 |
| Inbox | ContactInbox、Conversation、Campaign | Inbox 表示一个具体客户渠道 |
| Contact | ContactInbox、Conversation、Company、Label | Contact 是账号内统一客户档案 |
| ContactInbox | Inbox、Contact、source_id | 表示客户在某个渠道中的身份 |
| Conversation | Contact、Inbox、Message、Assignment | 会话连接客户、渠道和处理成员 |
| Message | Conversation、Attachment | 消息属于一个会话，可带附件 |
| Assistant | Inbox、Scenario、Document、FAQ、Custom Tool | AI 资源必须属于同一 Account |
| Integration App | Integration Hook | App 描述能力，Hook 表示账号连接实例 |
| User | Profile、Session、MFA | User 拥有个人资料、认证状态和登录会话 |
| Assignment Policy | Inbox | 一个策略可以绑定多个 Inbox，一个 Inbox 同时只绑定一个策略 |
| Call | Contact、Inbox、Conversation、Message | 通话属于账号中的联系人、渠道和会话 |

## 4. 核心功能与 API 入口

### 4.1 账号、成员与权限

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 账号信息 | /api/v1/accounts/{account_id} | id、name、locale、timezone、settings |
| 成员管理 | /api/v1/accounts/{account_id}/agents | email、name、role、availability |
| 团队管理 | /api/v1/accounts/{account_id}/teams | name、description、allow_auto_assign |
| Inbox 成员 | /api/v1/accounts/{account_id}/inbox_members | inbox_id、user_ids |
| 自定义角色 | /api/v1/accounts/{account_id}/custom_roles | name、permissions |
| 容量策略 | /api/v1/accounts/{account_id}/agent_capacity_policies | user_ids、inbox_id、conversation_limit |

### 4.2 渠道与客户身份

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| Inbox 管理 | /api/v1/accounts/{account_id}/inboxes | name、channel_type、greeting_enabled、working_hours_enabled |
| 渠道命令 | /api/v1/accounts/{account_id}/inboxes/{inbox_id} | agent_bot、health、secret、template |
| Widget 初始化 | /api/v1/widget/config | website_token |
| Widget 客户 | /api/v1/widget/contact | identifier、name、email、custom_attributes |
| Widget 会话 | /api/v1/widget/conversations | status、custom_attributes、last_seen |
| Widget 消息 | /api/v1/widget/messages | content、content_type、attachments |

### 4.3 联系人、公司与标签

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 联系人管理 | /api/v1/accounts/{account_id}/contacts | identifier、name、email、phone_number |
| 渠道身份 | /api/v1/accounts/{account_id}/contacts/{contact_id}/contact_inboxes | inbox_id、source_id |
| 联系人合并 | /api/v1/accounts/{account_id}/actions/contact_merge | base_contact_id、mergee_contact_id |
| 公司管理 | /api/v1/accounts/{account_id}/companies | name、industry、website、custom_attributes |
| 标签管理 | /api/v1/accounts/{account_id}/labels | title、description、color |
| 自定义字段定义 | /api/v1/accounts/{account_id}/custom_attribute_definitions | attribute_key、attribute_model、attribute_display_type |

### 4.4 会话、分配与消息

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 会话列表与详情 | /api/v1/accounts/{account_id}/conversations | status、inbox_id、assignee_id、team_id |
| 会话状态命令 | /api/v1/accounts/{account_id}/conversations/{id}/toggle_status | status、snoozed_until |
| 会话分配 | /api/v1/accounts/{account_id}/conversations/{id}/assignments | assignee_id、team_id |
| 会话标签 | /api/v1/accounts/{account_id}/conversations/{id}/labels | labels |
| 参与者 | /api/v1/accounts/{account_id}/conversations/{id}/participants | user_ids |
| 消息 | /api/v1/accounts/{account_id}/conversations/{id}/messages | content、message_type、private、content_type |
| 附件 | Message 响应和附件接口 | file_type、data_url、file_size、coordinates |

### 4.5 自动化、活动与通知

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 自动化规则 | /api/v1/accounts/{account_id}/automation_rules | event_name、conditions、actions、active |
| Macro | /api/v1/accounts/{account_id}/macros | name、actions、visibility |
| 快捷回复 | /api/v1/accounts/{account_id}/canned_responses | short_code、content |
| Campaign | /api/v1/accounts/{account_id}/campaigns | campaign_type、message、inbox_id、scheduled_at |
| 通知 | /api/v1/accounts/{account_id}/notifications | notification_type、read_at、snoozed_until |
| 通知设置 | /api/v1/accounts/{account_id}/notification_settings | email_flags、push_flags |

### 4.6 报表、SLA、CSAT 与帮助中心

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 运营报表 | /api/v2/accounts/{account_id}/reports | since、until、type、id、business_hours |
| 汇总报表 | /api/v2/accounts/{account_id}/summary_reports | agent、team、inbox、label、channel |
| 实时指标 | /api/v2/accounts/{account_id}/live_reports | metric、group_by、status |
| SLA 策略 | /api/v1/accounts/{account_id}/sla_policies | thresholds、business_hours、only_during_business_hours |
| SLA 结果 | /api/v1/accounts/{account_id}/applied_slas | conversation_id、breach、metric |
| CSAT | /api/v1/accounts/{account_id}/csat_survey_responses | rating、feedback_message、conversation_id |
| 帮助中心 | /api/v1/accounts/{account_id}/portals | name、slug、custom_domain、locale |
| 文章和分类 | /api/v1/accounts/{account_id}/portals/{portal_id}/articles、/api/v1/accounts/{account_id}/portals/{portal_id}/categories | title、content、status、locale、category_id |

### 4.7 Captain、Copilot 与 AI

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 模型与功能偏好 | /api/v1/accounts/{account_id}/captain/preferences | captain_models、captain_features |
| 文本与会话任务 | /api/v1/accounts/{account_id}/captain/tasks | content、operation、conversation_display_id |
| Assistant | /api/v1/accounts/{account_id}/captain/assistants | name、description、config、guardrails |
| Scenario | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/scenarios | title、instruction、tools、enabled |
| FAQ | /api/v1/accounts/{account_id}/captain/assistant_responses | question、answer、status、assistant_id |
| 知识文档 | /api/v1/accounts/{account_id}/captain/documents | name、external_link、pdf_file、assistant_id |
| Copilot | /api/v1/accounts/{account_id}/captain/copilot_threads | message、assistant_id、conversation_id |
| 自定义工具 | /api/v1/accounts/{account_id}/captain/custom_tools | endpoint_url、http_method、auth_type、param_schema |

### 4.8 集成、Webhook、插件与 Bot

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 集成目录 | /api/v1/accounts/{account_id}/integrations/apps | id、name、description、enabled |
| 集成连接 | /api/v1/accounts/{account_id}/integrations/hooks | app_id、inbox_id、status、settings |
| Slack | /api/v1/accounts/{account_id}/integrations/slack | reference_id、channel_id |
| Shopify | /api/v1/accounts/{account_id}/integrations/shopify | contact_id、order_id |
| Linear | /api/v1/accounts/{account_id}/integrations/linear | conversation_id、issue_id、team_id、project_id |
| Dashboard App | /api/v1/accounts/{account_id}/dashboard_apps | title、content.type、content.url |
| Webhook | /api/v1/accounts/{account_id}/webhooks | name、url、inbox_id、subscriptions |
| Agent Bot | /api/v1/accounts/{account_id}/agent_bots | name、description、outgoing_url、bot_type |

### 4.9 Public、Platform 与实时事件

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| Public Contact | /public/api/v1/inboxes/{inbox_identifier}/contacts | identifier、name、email、phone_number |
| Public Conversation | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations | custom_attributes、status |
| Public Message | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/messages | content、echo_id、attachments |
| Public CSAT | /public/api/v1/csat_survey/{id} | rating、feedback_message |
| Platform User | /platform/api/v1/users | name、email、password、custom_attributes |
| Platform Account | /platform/api/v1/accounts | name、locale、timezone |
| Platform Bot | /platform/api/v1/agent_bots | name、outgoing_url、bot_type |
| 实时订阅 | /cable | pubsub_token、account_id、user_id |

### 4.10 身份、资料与安全

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 登录和退出 | /auth/sign_in、/auth/sign_out | email、password、认证头 |
| Google OAuth | /omniauth/google_oauth2/callback | code、email、sso_auth_token |
| SAML 登录 | /api/v1/auth/saml_login、/omniauth/saml/callback | email、target、account_id、RelayState、sso_auth_token |
| Token 验证 | /auth/validate_token | access-token、client、uid |
| 密码重置 | /auth/password | email、reset_password_token、password |
| 个人资料 | /api/v1/profile | name、display_name、email、ui_settings |
| 在线状态 | /api/v1/profile/availability | account_id、availability |
| MFA | /api/v1/profile/mfa | otp_code、backup_code、enabled |
| 登录设备 | /api/v1/profile/sessions | device_name、ip_address、last_activity_at |
| 推送订阅 | /api/v1/notification_subscriptions | subscription_type、push_token |

### 4.11 分配策略、筛选与数据处理

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 分配策略 | /api/v1/accounts/{account_id}/assignment_policies | assignment_order、conversation_priority、enabled |
| Inbox 策略 | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignment_policy | assignment_policy_id |
| 保存筛选 | /api/v1/accounts/{account_id}/custom_filters | name、filter_type、query |
| 批量操作 | /api/v1/accounts/{account_id}/bulk_actions | type、ids、fields、labels |
| 数据导入 | /api/v1/accounts/{account_id}/data_imports | source_provider、access_token、import_types |
| 会话草稿 | /api/v1/accounts/{account_id}/conversations/{conversation_id}/draft_messages | message、has_draft |
| 文章批量操作 | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions | ids、status、category_id |

### 4.12 渠道授权、通话与会议

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 渠道 OAuth | /api/v1/accounts/{account_id}/{provider}/authorization | return_to、success、url |
| Facebook Page | /api/v1/accounts/{account_id}/callbacks | page_id、inbox_name、omniauth_token |
| WhatsApp Signup | /api/v1/accounts/{account_id}/whatsapp/authorization | code、business_id、waba_id、phone_number_id |
| Twilio Channel | /api/v1/accounts/{account_id}/channels/twilio_channel | account_sid、phone_number、medium |
| Voice Inbox | /api/v1/accounts/{account_id}/inboxes | channel.type、phone_number、provider_config |
| WhatsApp CSAT 模板 | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/csat_template | message、button_text、language |
| 通话列表 | /api/v1/accounts/{account_id}/calls | status、direction、inbox_id、agent_id |
| 联系人外呼 | /api/v1/accounts/{account_id}/contacts/{contact_id}/call | inbox_id、conversation_id |
| Conference | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/conference | call_sid、conversation_id |
| WhatsApp Call | /api/v1/accounts/{account_id}/whatsapp_calls | sdp_offer、sdp_answer、recording |

### 4.13 审计、企业设置与计费

| 功能 | API 入口 | 主要字段 |
|---|---|---|
| 审计记录 | /api/v1/accounts/{account_id}/audit_logs | action、audited_changes、username |
| 指标事件 | /api/v1/accounts/{account_id}/reporting_events | name、value、event_start_time、event_end_time |
| SAML 设置 | /api/v1/accounts/{account_id}/saml_settings | sso_url、certificate、role_mappings |
| 账号引导 | /api/v1/accounts/{account_id}/onboarding | onboarding_step、industry、timezone |
| 品牌邮件布局 | /api/v1/accounts/{account_id}/branded_email_layout | branded_email_layout |
| Cloud 额度 | /enterprise/api/v1/accounts/{account_id}/limits | allowed、consumed |
| Cloud 计费 | /enterprise/api/v1/accounts/{account_id} | currency、credits、action_type |
| Email Channel Migration | /platform/api/v1/accounts/{account_id}/email_channel_migrations | migrations、provider、provider_config |

## 5. 身份与权限总表

| 接口类型 | 主要身份 | 数据范围 | 典型用途 |
|---|---|---|---|
| Account API | Account 成员 Token | 当前成员有权访问的 Account、Inbox 和资源 | 工作台与账号管理 |
| Widget API | Widget 会话身份 | 当前网站 Inbox 和当前 Contact | 网站聊天 |
| Public API | Inbox identifier 与 Contact identifier | 指定 Inbox 下的客户数据 | 自有客户界面 |
| Platform API | Platform App Token | Platform App 管理范围 | 多租户账号与用户管理 |
| Webhook | 接收地址及签名校验信息 | 已订阅事件 | 接收业务变化 |
| 实时事件 | pubsub_token 或账号订阅身份 | 当前客户或账号可见事件 | 即时更新会话和消息 |

权限判断同时受以下条件影响：Account 归属、角色、Inbox 成员关系、Feature、企业授权、资源归属和第三方连接状态。

## 6. 字段组织规则

| 字段类别 | 常见字段 | 使用规则 |
|---|---|---|
| 资源标识 | id、account_id、inbox_id、contact_id | ID 必须属于当前资源范围 |
| 公开标识 | identifier、source_id、website_token | 应保持稳定，不应当作临时显示值 |
| 状态字段 | status、message_status、availability_status | 只能使用对应接口允许的枚举 |
| 时间字段 | created_at、updated_at、last_activity_at | 按接口返回格式和 Account 时区解释 |
| 扩展字段 | custom_attributes、additional_attributes、settings | 调用前先确认定义和可写范围 |
| 分页字段 | page、per_page、meta | 列表应根据分页信息继续读取 |
| 敏感字段 | access_token、secret、auth_config | 不应记录、转发或长期展示 |

## 7. 主要业务流程索引

| 业务流程 | 接口顺序 | 详细分册 |
|---|---|---|
| 网站客户开始聊天 | Widget Config → Contact → Conversation → Message | 04、06 |
| 客服处理会话 | Conversation List → Assignment → Message → Toggle Status | 06 |
| 导入和管理客户 | Contact Import/Create → ContactInbox → Label/Company | 05 |
| 执行自动运营 | Automation Rule 或 Campaign → Conversation/Message → Notification | 07 |
| 查看服务质量 | Report → Applied SLA → CSAT | 08 |
| 启用 AI 回答 | Preferences → Assistant → Document/FAQ → Inbox Binding | 09 |
| 启用 Copilot | Assistant → Copilot Thread → Copilot Message | 09 |
| 接入外部系统 | Integration App/Hook 或 Webhook → Event → 业务资源 | 10 |
| 自建客户界面 | Public Contact → Public Conversation → Public Message | 11 |
| 平台化管理 | Platform User → Platform Account → Account User | 11 |
| 安全登录 | Sign In → MFA → Profile → Session Management | 13 |
| 批量迁移数据 | Validate Source → Data Import → Status → Error Logs | 14 |
| 自动分配会话 | Assignment Policy → Inbox Binding → Capacity Check → Assignment | 03、14 |
| 接入外部渠道 | Authorization → Provider Callback → Inbox → Health Check | 04、15 |
| 发起通话 | Contact → Voice Inbox → Call → Conference → Recording | 15 |
| 第三方登录 | Google 授权 → OAuth Callback → 短期 SSO Token → Account Session | 13 |
| 企业单点登录 | SAML Settings → SAML Login → SAML Callback → Account Session | 13、16 |
| 查看审计和明细 | Audit Log 或 Reporting Event → Filter → Page | 16 |

## 8. 分册阅读入口

| 顺序 | 分册 | 使用场景 |
|---:|---|---|
| 1 | [产品功能总览](01-产品功能总览.md) | 了解产品边界和模块 |
| 2 | [API 通用约定与认证](02-API通用约定与认证.md) | 确认接口类型、身份和错误 |
| 3 | [账号、成员、团队与权限 API](03-账号成员团队与权限API.md) | 管理账号组织和权限 |
| 4 | [Inbox、渠道与 Widget API](04-Inbox渠道与Widget-API.md) | 配置渠道和网站聊天 |
| 5 | [联系人、公司与标签 API](05-联系人公司与标签API.md) | 管理客户资料 |
| 6 | [会话、消息与分配 API](06-会话消息与分配API.md) | 处理客服核心流程 |
| 7 | [自动化、活动、通知与模板 API](07-自动化活动通知与模板API.md) | 配置运营能力 |
| 8 | [报表、SLA、帮助中心与搜索 API](08-报表SLA帮助中心与搜索API.md) | 查看服务指标和知识内容 |
| 9 | [Captain、Copilot 与 AI API](09-Captain-Copilot与AI-API.md) | 接入 AI 模型和知识能力 |
| 10 | [集成、Webhook、Agent Bot 与插件 API](10-集成Webhook-AgentBot与插件API.md) | 接入第三方系统和扩展能力 |
| 11 | [Public、Platform 与实时事件 API](11-Public-Platform与实时事件API.md) | 构建客户界面和平台管理能力 |
| 12 | [公共字段、枚举、错误与验收](12-公共字段枚举错误与验收.md) | 统一字段解释和验收标准 |
| 13 | [身份、个人资料、安全与登录会话 API](13-身份资料安全与登录会话API.md) | 登录、MFA、资料、设备和推送订阅 |
| 14 | [分配策略、筛选、导入与批量操作 API](14-分配策略筛选导入与批量操作API.md) | 分配策略、筛选、导入、草稿和批量处理 |
| 15 | [渠道授权、通话、会议与高级 Inbox API](15-渠道授权通话会议与高级Inbox-API.md) | OAuth、WhatsApp、Twilio、模板和语音 |
| 16 | [审计、企业设置、计费与平台迁移 API](16-审计企业设置计费与平台迁移API.md) | Audit、SAML、Cloud Billing 和迁移 |
| 17 | [各渠道创建与更新字段字典](17-各渠道创建更新字段字典.md) | 渠道公共字段、凭证字段、响应和安全规则 |
| 18 | [自动化条件、操作符与动作字典](18-自动化条件操作符与动作字典.md) | 事件、条件、比较方式、动作参数和防循环 |
| 19 | [Webhook 与实时事件字段字典](19-Webhook与实时事件字段字典.md) | 订阅事件、Payload、签名、实时事件和去重 |
| 20 | [报表指标口径、筛选与返回字典](20-报表指标口径筛选与返回字典.md) | 指标定义、时长单位、维度、实时和 Bot 报表 |
| 21 | [角色权限与接口可用性矩阵](21-角色权限与接口可用性矩阵.md) | 基础角色、自定义权限、接口组和身份边界 |
| 22 | [AI 模型、Provider 与 Feature 能力矩阵](22-AI模型Provider与Feature能力矩阵.md) | Provider、Model、Feature、默认路由和凭证要求 |
| 23 | [全接口功能与文档覆盖矩阵](23-全接口功能与文档覆盖矩阵.md) | 从全部接口族、回调和管理入口反查对应文档 |
| 24 | [Feature 开关、版本与安装配置字典](24-Feature开关版本与安装配置字典.md) | 68 个 Account Feature、102 个安装配置和可用性规则 |
| 25 | [Super Admin 与安装级管理功能](25-SuperAdmin与安装级管理功能.md) | Account、User、Token、全局对象、实例状态和推送诊断 |
| 26 | [渠道状态、错误、重新授权与回调字典](26-渠道状态错误重新授权与回调字典.md) | 渠道健康、授权失效、模板、发送状态、回调和恢复流程 |
| 27 | [第三方集成完整字段与授权 API](27-第三方集成完整字段与授权API.md) | 11 类 Integration App、Hook、OAuth 和具体业务动作 |
| 28 | [移动端、推送诊断与客户端入口 API](28-移动端推送诊断与客户端入口API.md) | Browser Push、FCM、订阅、深链和应用域名关联 |
| 29 | [第三方入站 Webhook 完整字段字典](29-第三方入站Webhook完整字段字典.md) | 15 类第三方回调的 Header、Payload、支持事件和校验规则 |
| 30 | [全 API 逐动作路径索引](30-全API逐动作路径索引.md) | 所有业务动作的方法、完整路径、功能和字段分册定位 |
| 31 | [异步任务、进度、终态与补偿规则](31-异步任务进度终态与补偿规则.md) | Message、Import、Document、Campaign、Template、删除与批量结果 |
| 32 | [完整系统一致性还原目标与基线](32-完整系统一致性还原目标与基线.md) | 一致性层级、版本、配置、数据、时间、第三方和证据基线 |
| 33 | [并发、原子性、幂等与状态竞争规则](33-并发原子性幂等与状态竞争规则.md) | 分配、消息、渠道、合并、活动、导入和支付的并发结果 |
| 34 | [字段默认、空值、唯一约束与数据生命周期](34-字段默认空值唯一约束与数据生命周期.md) | 默认值、空值语义、合并与替换、唯一范围、删除和延迟清理 |
| 35 | [权限、Feature、套餐、配置与兼容优先级](35-权限Feature套餐配置与兼容优先级.md) | 十二层可用性判定、范围、版本形态、配置传播和历史兼容 |
| 36 | [异常、超时、重试、限流与第三方差异规则](36-异常超时重试限流与第三方差异规则.md) | 故障分类、重试序列、去重、状态乱序、限流和人工恢复 |
| 37 | [完整系统一致性对照验收规范](37-完整系统一致性对照验收规范.md) | 全功能案例、证据、通过门槛、长周期观察和最终一致性声明 |
| 38 | [API 请求与响应遗漏字段补充字典](38-API请求响应遗漏字段补充字典.md) | 39 个补充唯一字段、读写方向、空值、归属和跨入口一致性 |
| 39 | [列表查询、分页、排序、筛选与容量边界](39-列表查询分页排序筛选与容量边界.md) | 固定页大小、查询字段、排序值、批量、长度、数量和速率上限 |
| 40 | [业务动作、事件、副作用与跨入口一致性](40-业务动作事件副作用与跨入口一致性.md) | 37 个业务变化与通知、自动化、Webhook、Bot、集成、报表和 AI 结果 |
| 41 | [第三方 API 版本、能力漂移与历史兼容](41-第三方API版本能力漂移与历史兼容.md) | Provider 逐动作版本、Token、Scope、回调漂移和旧数据兼容 |
| 42 | [全功能覆盖收口与未决条件清单](42-全功能覆盖收口与未决条件清单.md) | 全覆盖统计、真实环境待验项、证据要求和一致性关门条件 |

## 9. 使用边界

- 文档中的路径、字段和枚举以指定版本为范围；
- Feature、企业授权、账号角色和渠道类型会改变接口可用性；
- 第三方集成还受第三方授权、额度和回调状态影响；
- 响应可能增加新字段，使用方不应依赖字段顺序；
- 创建成功、异步任务完成和第三方执行成功是三个不同结果，应分别确认；
- 完整字段、子资源和业务规则以对应分册为准。

## 10. 文档覆盖状态与继续扩展方向

### 10.1 本轮已经补齐

| 专题 | 已覆盖内容 | 分册 |
|---|---|---|
| 身份安全 | 登录、Google OAuth、SAML、认证头、密码、Profile、MFA、Session 和推送订阅 | 13 |
| 工作流管理 | Assignment Policy、Custom Filter、Bulk Action、Data Import、Draft、Direct Upload | 14 |
| 高级渠道 | OAuth、Facebook、WhatsApp Signup、Twilio、Voice Inbox、CSAT Template | 15 |
| 语音 | Calls、联系人外呼、Conference、WhatsApp Calling、录音 | 15 |
| 企业设置 | Audit Log、Reporting Event、SAML Settings、Onboarding、品牌邮件布局 | 16 |
| Cloud 与平台 | Limits、Billing、Top-up、Account Deletion、Email Channel Migration | 16 |
| 零散接口 | ContactInbox Filter、Year in Review、Portal Instructions、Widget Member/Label、Inbox Assistant | 04、05、08、09 |

### 10.2 本轮继续补齐

| 专题 | 已补充内容 | 分册 |
|---|---|---|
| 渠道字段字典 | Web Widget、Email、API、WhatsApp、LINE、Telegram、SMS、授权型渠道、响应与安全规则 | 17 |
| 自动化字典 | 5 个事件、条件字段、操作符、动作参数、顺序、防重复和循环 | 18 |
| Webhook 与实时事件 | 12 个可订阅事件、Payload、签名、投递规则和实时事件 | 19 |
| 报表指标 | 指标公式、秒数口径、时间维度、营业时间、实时和 Bot 指标 | 20 |
| 权限矩阵 | Administrator、Agent、6 种 Custom Role 权限、Contact、Platform 和 Bot | 21 |
| AI 模型能力 | 3 个 Provider、14 个模型条目、12 个 Feature、默认值、凭证与回退 | 22 |

### 10.3 最后一轮覆盖补充

| 专题 | 已补充内容 | 分册 |
|---|---|---|
| 全接口反查 | 全部账号接口、公共接口、Platform、回调、Super Admin、Well-known 和页面边界 | 23 |
| Feature 与安装配置 | 68 个 Feature、默认状态、企业边界、102 个安装配置、Secret 和联合验收 | 24 |
| Super Admin | Account、User、成员关系、Token、全局 Bot、Platform App、Banner、实例和任务状态 | 25 |
| 渠道故障恢复 | 授权错误阈值、重新授权、WhatsApp Health、模板、消息状态和 Provider 回调 | 26 |
| 第三方集成 | 11 类 App、Hook 生命周期、Slack、Linear、Shopify、Notion、Dialogflow、会议和 CRM | 27 |
| 移动端与推送 | Browser Push、FCM、订阅唯一性、失效清理、诊断、深链和应用域名关联 | 28 |

### 10.4 覆盖收口补充

| 专题 | 已补充内容 | 分册 |
|---|---|---|
| 第三方入站事件 | Facebook、Instagram、TikTok、X/Twitter、WhatsApp、LINE、Telegram、SMS、Twilio、Slack、Shopify、Stripe 和 Firecrawl 字段 | 29 |
| 逐动作路径 | Account、Widget、Public、Platform、Enterprise、Provider Callback、Well-known 和 Super Admin 动作 | 30 |
| 异步最终状态 | Message、Data Import、Document、FAQ、Campaign、Template、SSL、删除、批量、邮件和补偿 | 31 |

### 10.5 完整一致性补充

| 专题 | 已补充内容 | 分册 |
|---|---|---|
| 一致性基线 | L1–L5 目标、版本、配置、Account、Inbox、数据、时间、第三方和证据包 | 32 |
| 并发与幂等 | 原子边界、分配竞争、消息状态、Provider 串行窗口、合并、活动和去重 | 33 |
| 字段与生命周期 | 创建默认值、五类空值、合并与替换、唯一范围、脱敏、解绑和延迟清理 | 34 |
| 权限与兼容 | 十二层判定、身份范围、Feature、套餐、配置优先级、状态限制和历史兼容 | 35 |
| 异常与第三方 | 超时、重试序列、回调确认、消息回声、状态乱序、限流和恢复 | 36 |
| 全量对照验收 | 全域案例、动态字段归一化、证据包、通过门槛、长周期观察和报告 | 37 |

### 10.6 细粒度合同收口

| 专题 | 已补充内容 | 分册 |
|---|---|---|
| 公开接口遗漏字段 | 123 类对象、766 个字段出现位置、273 个唯一字段名称和最后 39 个补充字段 | 38 |
| 查询与容量 | Conversation、Contact、Company、Call、SLA、CSAT、Portal、Captain 的页码、排序、筛选和上限 | 39 |
| 业务副作用 | 37 个业务变化及 Notification、Automation、Webhook、Bot、Integration、Report、CSAT 和 Captain | 40 |
| Provider 版本 | Facebook、Instagram、WhatsApp、TikTok、Shopify、Notion、Microsoft 等逐动作版本和兼容 | 41 |
| 覆盖关门清单 | 第三方、AI、时间、并发、规模、历史数据、移动端和计费的实际证据要求 | 42 |

当前 00 至 42 分册已覆盖 Chatwoot 4.16.0 的产品功能、逐动作接口、公开字段、权限、状态、安装开关、第三方回调、异步结果、查询容量、业务副作用、Provider 版本和完整一致性验收规则。文档层面的已识别缺口已经收口；固定基线内的一致性还必须按 37、42 分册完成实际对照并取得证据。

## 11. webchat 对照与补齐分册

00 至 42 分册继续作为 Chatwoot 4.16.0 固定基线；43 至 48 分册用于说明 webchat 当前满足程度、缺失功能目标、接口字段、迁移兼容和完成验收，不改变前述 Chatwoot 基线统计。

| 分册 | 文档 | 主要内容 |
|---:|---|---|
| 43 | [webchat 现状覆盖与缺失功能总表](43-webchat现状覆盖与缺失功能总表.md) | 已有能力、保留范围、差距、对象映射、优先级和完成判定 |
| 44 | [webchat 统一渠道、会话与 API 补齐规范](44-webchat统一渠道会话与API补齐规范.md) | 国际渠道、Account、Inbox、Contact、Conversation、Message、实时事件和核心 API |
| 45 | [webchat 自动化、SLA、帮助中心与通知补齐规范](45-webchat自动化SLA帮助中心与通知补齐规范.md) | 分配、自动化、Macro、Campaign、通知、SLA、Portal、批量和导入 |
| 46 | [webchat AI 模型与插件体系补齐规范](46-webchat-AI模型与插件体系补齐规范.md) | Provider、Model、Assistant、Copilot、Document、Tool、Webhook、Integration 和 Bot |
| 47 | [webchat 开放平台、安全、审计与企业能力补齐规范](47-webchat开放平台安全审计与企业能力补齐规范.md) | 登录、MFA、OAuth、SAML、Public、Platform、Audit、Push、Feature、套餐和迁移 |
| 48 | [webchat 迁移、兼容与完整验收规范](48-webchat迁移兼容与完整验收规范.md) | 阶段、数据映射、旧接口兼容、第三方、并发、故障、长周期和关门条件 |

## 12. AI 实施步骤文档

[全系统基础层规范](../Chatwoot全系统基础层规范/README.md)定义所有后续模块必须遵守的共同规则；本目录定义“实现什么”；[模块化架构设计文档](../Chatwoot模块化架构设计文档/README.md)定义“如何拆分模块且避免重复和循环依赖”；[日志基础层设计文档](../Chatwoot日志审计与变更追踪设计文档/README.md)定义“如何让每次数据变化可追踪、可关联、可验证且不可静默修改”；[AI 实施步骤文档](../Chatwoot-AI实施步骤文档/README.md)定义“按什么顺序实施和如何确认完成”。实施集包含 33 个编号分册、482 个独立任务、26 个批次、逐任务状态记录、量化基线、第三方实证、拓扑演练、迁移回退、完整验收和唯一完成出口。

| 目标 | 入口 |
|---|---|
| 先读取全系统基础层 | [全系统基础层规范](../Chatwoot全系统基础层规范/README.md) |
| 先完成模块化架构 | [模块化架构设计文档](../Chatwoot模块化架构设计文档/README.md) |
| 完成 L0 日志基础层 | [日志基础层、审计与数据变更追踪设计文档](../Chatwoot日志审计与变更追踪设计文档/README.md) |
| 从第一项开始执行 | [AI 实施总控](../Chatwoot-AI实施步骤文档/00-AI实施总控与完成协议.md) |
| 查看批次和依赖 | [总任务台账](../Chatwoot-AI实施步骤文档/01-总任务台账与依赖顺序.md) |
| 查看每项任务状态 | [AI 全任务状态记录](../Chatwoot-AI实施步骤文档/28-AI全任务状态记录.md) |
| 核对 00 至 48 覆盖 | [功能基线到实施任务追踪矩阵](../Chatwoot-AI实施步骤文档/27-功能基线到实施任务追踪矩阵.md) |
| 冻结实际量化目标 | [实际基线参数与量化目标冻结表](../Chatwoot-AI实施步骤文档/29-实际基线参数与量化目标冻结表.md) |
| 登记第三方真实回执 | [第三方真实环境与最终接口冻结表](../Chatwoot-AI实施步骤文档/30-第三方真实环境与最终接口冻结表.md) |
| 执行拆分合并和回退 | [模块运行拓扑拆分合并灰度回退演练表](../Chatwoot-AI实施步骤文档/31-模块运行拓扑拆分合并灰度回退演练表.md) |
| 汇总全部实际证据 | [全局证据包索引量化评分与完成台账](../Chatwoot-AI实施步骤文档/32-全局证据包索引量化评分与完成台账.md) |
| 执行最终关门 | [全功能关门与完成声明](../Chatwoot-AI实施步骤文档/25-全功能关门未决项清零与完成声明.md) |
