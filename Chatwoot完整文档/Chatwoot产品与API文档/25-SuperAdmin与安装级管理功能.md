# Chatwoot Super Admin 与安装级管理功能

> 导航：[Feature 开关、版本与安装配置字典](24-Feature开关版本与安装配置字典.md)｜[移动端、推送诊断与客户端入口 API](28-移动端推送诊断与客户端入口API.md)

## 1. 模块定位

Super Admin 管理整个 Chatwoot 实例，而 Account Administrator 只管理一个 Account。该区域包含全部 Account 和 User、安装配置、全局 Bot、Platform App、Token 归属、实例状态、推送诊断和版本状态，因此权限高于所有 Account 角色。

Super Admin 入口主要返回管理页面或执行页面表单动作，不属于普通 Account JSON API。调用时使用 Super Admin 浏览器会话和防跨站请求校验；不能使用 account_id、api_access_token 或 Widget Token 替代。

## 2. 权限边界

| 身份 | 可使用范围 |
|---|---|
| Super Admin | /super_admin 下的安装级功能 |
| Account Administrator | 当前 Account 的成员、Inbox、Feature 可见结果和业务配置，不能进入 Super Admin |
| Agent | 当前 Account 的会话和被授权资源 |
| Platform App | 只使用 Platform API，不自动获得 Super Admin 页面权限 |
| Contact 或 Widget | 不能使用任何安装级入口 |

Super Admin 与普通 Account 成员可以是同一个 User，但两种身份边界仍然分开。最高权限操作必须确认目标 Account、User、Bot 或 Token 的真实 ID。

### 2.1 登录与退出

| 动作 | 方法与路径 | 主要字段 | 结果 |
|---|---|---|---|
| 打开登录页 | GET /super_admin/sign_in | 无 | 返回最高管理身份登录页 |
| 提交登录 | POST /super_admin/sign_in | super_admin.email、super_admin.password | 成功后建立独立浏览器会话并进入 User 管理 |
| 标准退出 | DELETE /super_admin/sign_out | 无 | 结束当前最高管理会话 |
| 页面兼容退出 | GET /super_admin/logout | 无 | 结束会话并返回首页 |

邮箱不存在和密码错误统一显示无效凭证，不向请求方暴露账号是否存在。登录使用 Super Admin 会话，不返回 Account API Token；登录后的修改、删除和测试操作仍必须通过防跨站请求校验。公共设备使用后必须显式退出。

## 3. 功能入口总表

| 方法 | 路径 | 功能 | 结果形式 |
|---|---|---|---|
| GET、POST | /super_admin/sign_in | 打开或提交 Super Admin 登录 | 登录页和重定向 |
| DELETE | /super_admin/sign_out | 标准退出 | 重定向 |
| GET | /super_admin/logout | 页面兼容退出 | 重定向 |
| GET | /super_admin | 安装总览 | 管理页面 |
| GET、POST、PATCH、DELETE | /super_admin/accounts | Account 管理 | 页面和重定向结果 |
| POST | /super_admin/accounts/{id}/seed | 为 Account 初始化演示或基础数据 | 异步受理结果 |
| POST | /super_admin/accounts/{id}/reset_cache | 刷新 Account 缓存 | 操作结果 |
| GET、POST、PATCH、DELETE | /super_admin/users | User 管理 | 页面和重定向结果 |
| DELETE | /super_admin/users/{id}/avatar | 删除 User 头像 | 操作结果 |
| GET、POST、DELETE | /super_admin/account_users | Account 与 User 关系管理 | 页面和重定向结果 |
| GET | /super_admin/access_tokens | Token 列表和详情 | 只读页面 |
| GET、POST、PATCH | /super_admin/installation_configs | 安装配置查看和更新 | 页面和重定向结果 |
| GET、POST | /super_admin/app_config | 按类别查看和保存应用配置 | 页面和保存结果 |
| GET、POST、PATCH、DELETE | /super_admin/agent_bots | 全局 Agent Bot 管理 | 页面和重定向结果 |
| GET、POST、PATCH、DELETE | /super_admin/platform_apps | Platform App 管理 | 页面和重定向结果 |
| GET、POST、PATCH、DELETE | /super_admin/platform_banners | 平台公告管理 | 页面和重定向结果 |
| GET | /super_admin/instance_status | 实例版本与依赖状态 | 管理页面 |
| GET、POST | /super_admin/push_diagnostics | 查询订阅并发送测试推送 | 管理页面和测试结果 |
| POST | /super_admin/push_diagnostics/destroy_subscriptions | 删除选中推送订阅 | 操作结果 |
| GET | /super_admin/settings | 版本设置 | 管理页面 |
| GET | /super_admin/settings/refresh | 刷新版本状态 | 刷新结果 |
| GET、POST | /installation/onboarding | 首次安装引导 | 页面和完成结果 |
| GET | /monitoring | 后台任务监控 | 受保护页面 |

## 4. 安装总览

Super Admin 首页用于快速判断实例规模和近 30 天活动。

| 信息 | 含义 |
|---|---|
| Account 数量 | 实例内全部租户数量 |
| User 数量 | 实例内全部用户数量 |
| Inbox 数量 | 全部 Account 的 Inbox 总量 |
| Conversation 数量 | 全部 Account 的会话总量 |
| 30 天会话趋势 | 按日期聚合的近期会话创建量 |

总览是安装范围统计，不受某个 Account 的 Inbox 访问权限过滤。它适合发现增长和异常，不替代 20 分册中的业务报表。

## 5. Account 管理

### 5.1 列表和筛选

| 字段或筛选 | 说明 |
|---|---|
| id | Account 唯一 ID |
| name | Account 名称 |
| locale | 默认语言 |
| status | active 或 suspended |
| users_count | Account 成员数 |
| conversations_count | 会话数 |
| active | 仅查看正常 Account |
| suspended | 仅查看已暂停 Account |
| recently_created | 查看近期创建的 Account |
| marked_for_deletion | 查看进入删除流程的 Account |

### 5.2 创建和更新字段

| 字段组 | 主要字段 | 说明 |
|---|---|---|
| 基本信息 | name、locale、timezone | 影响界面默认值和时间计算 |
| 状态 | status | suspended 会限制 Account 的正常业务使用 |
| Feature | feature_flags | 只启用该 Account 能力，完整 Key 见 24 分册 |
| 限制 | limits | 企业和 Cloud 计划允许的资源上限 |
| Captain | captain_models、captain_features | Account 级模型覆盖和 AI Feature 偏好 |

更新 Account 前应分别确认 Feature、计划和限制。Feature 允许使用能力，limit 决定可使用数量，两者不能互相替代。

### 5.3 初始化、缓存和删除

- seed 受理 Account 初始化任务，返回成功不等于全部数据已经可见；
- reset_cache 用于清除 Account 配置缓存，使近期变更重新加载；
- 删除 Account 是高风险操作，通常进入异步清理流程；
- 删除前必须确认 Account ID、名称、成员、Inbox 和保留要求；
- suspended 适合临时停用，不能等同于删除。

## 6. User 管理

### 6.1 列表字段和筛选

| 字段或筛选 | 说明 |
|---|---|
| id | User ID |
| name | 用户姓名 |
| display_name | 面向客户显示的名称 |
| email | 登录邮箱 |
| confirmed_at | 邮箱确认时间；空值表示未确认 |
| type | 普通 User 或 Super Admin 身份类型 |
| super_admin | 只查看最高权限用户 |
| confirmed | 只查看已确认用户 |
| unconfirmed | 只查看未确认用户 |
| recently_created | 查看近期创建用户 |

### 6.2 创建和更新字段

| 字段 | 类型 | 规则 |
|---|---|---|
| name | string | 必填的用户名称 |
| display_name | string | 可选的客户可见名称 |
| email | string | 登录身份，必须保持唯一和规范化 |
| password | string | 创建或明确重置时提交，不应在任何页面回显 |
| confirmed_at | datetime | 表示邮箱已确认 |
| type | enum | 提升为 Super Admin 前必须进行二次确认 |
| avatar | file | 用户头像；可以通过独立动作删除 |

删除 User 可能影响多个 Account、会话分配、团队关系和审计归属。只需要移出某个 Account 时，应删除 AccountUser 关系，而不是删除全局 User。

## 7. AccountUser 关系

AccountUser 表示一个 User 在某个 Account 中的成员身份。

| 字段 | 说明 |
|---|---|
| account_id | 目标 Account |
| user_id | 目标 User |
| role | administrator 或 agent |
| custom_role_id | 可选的自定义角色 |
| availability | 用户在该 Account 的可用状态 |

同一 User 可以加入多个 Account，且每个 Account 的 role 可以不同。移除关系只取消该 Account 的访问，不应删除 User 在其他 Account 的成员关系。

## 8. Access Token 查询

Access Token 页面用于查明 Token 的归属和风险范围，不提供普通业务 Token 的随意分发功能。

| 字段 | 说明 |
|---|---|
| id | Token 记录 ID |
| token | 敏感凭证，只能在必要范围查看 |
| owner_type | User、Agent Bot 或 Platform App 等所有者类型 |
| owner_id | 所有者 ID |
| created_at | 创建时间 |
| updated_at | 最近更新时间 |

Token 泄露时应在对应所有者范围进行轮换或撤销，并检查 Webhook、外部集成和访问记录。不能只修改显示名称来处理泄露。

## 9. 安装配置管理

安装配置页面只显示允许手工维护的配置。字段如下：

| 字段 | 说明 |
|---|---|
| name | 配置 Key，不应改名 |
| value | 新配置值 |
| display_title | 页面展示名称 |
| description | 用途、默认值或单位 |
| type | text、boolean、number、secret 或结构化对象 |
| locked | 是否由系统管理 |

更新 secret 时只提交新值；空白是否代表“不改变”或“清除”必须以页面提示为准。102 个 Key、默认值和管理状态见 24 分册。

## 10. App Config 分类入口

Super Admin 可按功能类别维护所需应用配置。

| 类别 | 主要用途 |
|---|---|
| general | 安装名称、品牌、注册和通用设置 |
| facebook | Facebook 应用和 Webhook |
| instagram | Instagram 应用和 Webhook |
| tiktok | TikTok 应用和 API 版本 |
| whatsapp_embedded | WhatsApp Embedded Signup |
| microsoft | Microsoft 邮箱授权 |
| google | Google 邮箱授权和登录 |
| email | 入站邮件和支持邮箱 |
| slack | Slack OAuth |
| linear | Linear OAuth |
| notion | Notion OAuth |
| shopify | Shopify OAuth |
| captain | AI 模型、Endpoint、抓取和同步 |

类别只是管理分组，不改变各配置 Key 的安装范围。

## 11. 全局 Agent Bot

Super Admin Agent Bot 与 Account API 中的 Bot 使用相同业务对象，但管理范围是整个实例。

| 字段 | 说明 |
|---|---|
| name | Bot 名称 |
| description | 用途说明 |
| outgoing_url | 接收会话事件的目标地址 |
| bot_type | Bot 类型 |
| account_id | 可选的 Account 归属 |
| avatar | Bot 头像 |
| access_token | Bot 身份凭证，按敏感信息处理 |

创建 Bot 不会自动绑定 Inbox。需要在目标 Account 和 Inbox 中明确建立绑定，并验证 outgoing_url、超时、重试和幂等。

## 12. Platform App

Platform App 用于获得 Platform API 身份。

| 字段 | 说明 |
|---|---|
| name | Platform App 名称 |
| access_token | Platform API 认证 Token |
| created_at | 创建时间 |
| updated_at | 更新时间 |

Platform App 能管理的资源范围较大，应为不同系统使用不同 App，不应多个外部系统共用同一 Token。

## 13. Platform Banner

平台公告主要用于 Cloud 环境向用户展示安装级通知。

| 字段 | 类型 | 说明 |
|---|---|---|
| banner_message | string | 公告正文 |
| banner_type | enum | info、warning 或 error |
| active | boolean | 是否展示 |

同一时间存在多个 active Banner 时，应确认页面展示优先级和文案冲突。error 只用于确实会影响业务的高优先级问题。

## 14. 实例状态

| 状态项 | 说明 |
|---|---|
| version | 当前 Chatwoot 版本 |
| revision | 当前构建修订标识 |
| edition | 社区版或企业版 |
| migrations | 数据结构升级是否完成 |
| primary_data_service | 主数据服务是否可用 |
| cache_service | 缓存服务是否可用 |
| cache_service_version | 缓存服务版本 |
| cache_clients | 当前连接数 |
| cache_memory | 当前内存使用信息 |

/health 适合外部健康探测，instance_status 适合 Super Admin 判断具体依赖和版本状态。健康探测成功不表示每个渠道授权和第三方 Provider 都健康。

## 15. 版本设置

settings 页面展示当前版本与可用更新状态；刷新动作重新获取版本信息。

| 结果 | 说明 |
|---|---|
| current | 当前已是最新版本 |
| update_available | 存在更新版本 |
| unknown | 暂时无法确认版本状态 |

刷新版本状态不执行升级，也不改变 Account Feature。升级前还需要核对企业授权、配置变化、第三方 API 版本和数据升级状态。

## 16. 推送诊断

Super Admin 可以按 User ID 或 email 查询浏览器和 FCM 订阅，选择一个或多个订阅发送测试通知。

| 输入字段 | 说明 |
|---|---|
| user_query | User ID 或 email |
| subscription_ids | 要测试的订阅 ID 数组 |
| push_title | 可选测试标题 |
| push_body | 可选测试正文 |

| 结果字段 | 说明 |
|---|---|
| id | 订阅 ID |
| type | browser_push、fcm 或 fcm_via_hub |
| device | 浏览器 Endpoint 主机或移动设备 ID 尾部 |
| token_tail | Endpoint 或 Push Token 尾部六位 |
| status | success、failure 或 skipped |
| message | Provider 返回、跳过原因或错误摘要 |

测试成功表示推送服务已接受请求，不保证用户一定看到通知；还要考虑设备权限、系统通知设置、应用状态和 Token 是否刚刚失效。

## 17. 删除推送订阅

删除动作接收选中的 subscription_ids，只清理对应 User 的失效订阅。应先通过测试结果、设备尾号和最近更新时间确认目标，避免删除仍在使用的设备。

删除订阅不会关闭 User 的通知偏好。设备再次注册后可以生成或更新订阅。

## 18. 首次安装引导

/installation/onboarding 用于尚未完成实例初始化时收集最高管理身份和必要安装信息。完成后应关闭重复引导入口，并立即验证：

- Super Admin 能登录；
- 安装名称和公开地址正确；
- 邮件发送和密码重置可用；
- /health 和 instance_status 正常；
- 注册策略符合预期；
- 默认 Account 和 User 没有多余访问关系。

## 19. 后台任务监控

/monitoring 展示异步任务的队列、失败、重试和执行状态。它用于判断导入、发送、Webhook、报表、删除、AI 文档同步等后台流程是否完成。

该页面可能包含 Account ID、对象 ID、目标地址和错误摘要，应只对最高权限身份开放。重试任务前要先判断原请求是否具有幂等性，避免重复消息、重复 Webhook 或重复导入。

## 20. 高风险操作清单

| 操作 | 主要风险 | 执行前确认 |
|---|---|---|
| 提升 Super Admin | 获得全实例权限 | User ID、email、审批记录 |
| 删除 User | 影响多个 Account 和分配关系 | 所属 Account、会话、团队、替代人 |
| 删除 Account | 租户数据进入清理流程 | Account ID、保留要求、备份和审批 |
| 调整 Feature | 页面和 API 能力突然变化 | 计划、角色、依赖配置和回退方案 |
| 替换 Secret | 渠道或集成立即失效 | 新凭证、回调、权限范围和轮换窗口 |
| 删除推送订阅 | 指定设备不再接收通知 | User、设备尾号、测试结果 |
| 操作失败任务 | 可能重复产生副作用 | 幂等键、已产生结果和重试次数 |

## 21. 验收建议

- 普通 Account Administrator 无法进入 /super_admin；
- Account 暂停后业务访问受限，恢复后原有数据仍在；
- User 从一个 Account 移除后，其他 Account 成员关系不受影响；
- locked 安装配置不会出现在可编辑列表；
- Secret 不在列表、错误或审计内容中完整回显；
- Platform App Token 只能用于 Platform API，不能伪装 Super Admin 页面会话；
- 推送测试结果能区分 success、failure 和 skipped；
- 实例健康、渠道健康和第三方授权健康分别验证，不能相互替代。
