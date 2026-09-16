# 身份、个人资料、安全与登录会话 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

本模块包括账号登录、Token 验证、密码重置、个人资料、在线状态、MFA、登录设备、推送订阅、Google OAuth 和 SAML 登录。身份接口与 Account 业务接口使用不同路径，登录成功后返回的认证头用于后续受保护请求。

## 2. 登录与退出 API

| 方法 | 路径 | 功能 | 是否需要已登录 |
|---|---|---|---|
| POST | /auth/sign_in | 邮箱和密码登录，或提交 MFA 验证 | 否 |
| DELETE | /auth/sign_out | 注销当前登录会话 | 是 |
| GET | /auth/validate_token | 验证当前认证头是否有效 | 是 |
| POST | /auth/password | 发送密码重置邮件 | 否 |
| PUT | /auth/password | 使用重置令牌设置新密码 | 否 |
| POST | /auth/confirmation | 使用确认令牌确认邮箱 | 否 |
| POST | /resend_confirmation | 重新发送邮箱确认邮件 | 否 |

### 2.1 普通登录请求字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 是 | 登录邮箱，匹配时忽略首尾空格和大小写 |
| password | string | 是 | 登录密码 |
| revoke_session_id | integer | 否 | 达到会话数量限制时，指定需要移除的历史会话 |
| revoke_all_sessions | boolean | 否 | 达到限制时移除其他会话 |

### 2.2 MFA 登录字段

首次提交正确的 email 和 password 后，如果账号启用了 MFA，接口返回 206，并提供 mfa_required 和 mfa_token。随后再次调用登录接口。

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| mfa_token | string | 是 | 第一步登录返回的短期 MFA Token |
| otp_code | string | 二选一 | 验证器生成的一次性验证码 |
| backup_code | string | 二选一 | MFA 备用码 |

### 2.3 登录响应

| 位置 | 字段 | 说明 |
|---|---|---|
| Header | access-token | 当前会话访问令牌 |
| Header | client | 当前登录客户端标识 |
| Header | uid | 当前用户邮箱标识 |
| Header | token-type | Token 类型，通常为 Bearer |
| Body | data | 当前用户摘要 |
| Body | mfa_required | 是否需要第二步验证，仅 MFA 第一步返回 |
| Body | mfa_token | 第二步验证所需 Token |
| Body | sessions_limit_reached | 是否达到登录会话数量上限 |
| Body | sessions | 可选择移除的历史会话 |

认证头应作为一组保存和更新。后续响应可能刷新 access-token，不应只保留首次登录的值。

## 3. 密码与邮箱确认

### 3.1 密码重置字段

| 场景 | 字段 | 类型 | 说明 |
|---|---|---|---|
| 请求重置 | email | string | 接收重置邮件的邮箱 |
| 设置新密码 | reset_password_token | string | 重置链接中的令牌 |
| 设置新密码 | password | string | 新密码 |
| 设置新密码 | password_confirmation | string | 新密码确认 |

### 3.2 邮箱确认字段

| 字段 | 类型 | 说明 |
|---|---|---|
| confirmation_token | string | 邮箱确认链接中的令牌 |
| email | string | 重新发送确认邮件时使用 |
| h_captcha_client_response | string 或 null | 启用验证码时使用 |

密码重置和邮箱确认 Token 都是一次性敏感值。成功使用或过期后不得继续使用。

## 4. Profile API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/profile | 当前用户资料 |
| PATCH | /api/v1/profile | 更新资料、密码或界面设置 |
| DELETE | /api/v1/profile/avatar | 删除头像 |
| POST | /api/v1/profile/availability | 更新当前账号中的在线状态 |
| POST | /api/v1/profile/auto_offline | 设置自动离线 |
| PUT | /api/v1/profile/set_active_account | 标记当前活动账号 |
| POST | /api/v1/profile/resend_confirmation | 当前用户重新发送确认邮件 |
| POST | /api/v1/profile/reset_access_token | 重置个人 API Access Token |

### 4.1 Profile 可写字段

| 字段 | 类型 | 说明 |
|---|---|---|
| name | string | 用户姓名 |
| display_name | string 或 null | 对外显示名称 |
| email | string | 邮箱；变更后可能需要重新确认 |
| avatar | file | 头像文件 |
| phone_number | string 或 null | 电话号码 |
| message_signature | string 或 null | 消息签名 |
| ui_settings | object | 当前用户界面偏好 |
| current_password | string | 修改密码时的当前密码 |
| password | string | 新密码 |
| password_confirmation | string | 新密码确认 |

### 4.2 Profile 响应常用字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | User ID |
| uid | string | 用户身份标识，通常为邮箱 |
| name | string | 姓名 |
| display_name | string 或 null | 展示名称 |
| email | string | 邮箱 |
| account_id | integer | 当前活动 Account |
| role | string | 当前活动账号角色 |
| avatar_url | string 或 null | 头像地址 |
| access_token | string | 个人 API Access Token，按响应权限提供 |
| pubsub_token | string | 实时事件订阅 Token |
| ui_settings | object | 界面偏好 |
| confirmed | boolean | 邮箱是否确认 |
| accounts | array | 所属 Account 及成员关系 |
| accounts[].id | integer | Account ID |
| accounts[].role | string | 在该 Account 中的角色 |
| accounts[].permissions | string array | 当前权限集合 |
| accounts[].availability | enum | 用户设置的 online、busy 或 offline |
| accounts[].availability_status | enum | 结合在线状态得到的可用状态 |
| accounts[].auto_offline | boolean | 该 Account 是否自动离线 |

### 4.3 Availability 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| profile.account_id | integer | 是 | 要更新状态的 Account |
| profile.availability | enum | 是 | online、busy 或 offline |
| profile.auto_offline | boolean | 是 | 是否根据活动状态自动切换离线 |

可用状态属于 User 与 Account 的成员关系，同一个用户在不同 Account 中可以具有不同状态。

## 5. MFA 管理 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/profile/mfa | 获取 MFA 状态 |
| POST | /api/v1/profile/mfa | 开始启用 MFA |
| POST | /api/v1/profile/mfa/verify | 验证并正式启用 |
| POST | /api/v1/profile/mfa/backup_codes | 重新生成备用码 |
| DELETE | /api/v1/profile/mfa | 关闭 MFA |

### 5.1 MFA 请求字段

| 字段 | 使用接口 | 说明 |
|---|---|---|
| otp_code | verify、backup_codes、DELETE | 验证器一次性验证码 |
| backup_code | verify、backup_codes、DELETE | 可替代 otp_code 的备用码 |
| password | DELETE | 关闭 MFA 时验证当前密码 |

### 5.2 MFA 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| feature_available | boolean | 当前安装是否提供 MFA |
| enabled | boolean | 当前用户是否已启用 |
| backup_codes_generated | boolean | 是否已生成备用码 |
| provisioning_url | string | 验证器配置地址，仅启用阶段返回 |
| secret | string | 验证器密钥，仅启用阶段返回 |
| backup_codes | string array | 备用码，仅生成时返回 |

provisioning_url、secret 和 backup_codes 只应在启用流程中短暂展示。重新生成备用码后，旧备用码全部失效。

## 6. 登录会话 API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/profile/sessions | 当前用户的有效登录会话 |
| DELETE | /api/v1/profile/sessions/{id} | 注销指定历史会话 |

### 6.1 Session 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Session ID |
| browser_name | string 或 null | 浏览器名称 |
| browser_version | string 或 null | 浏览器版本 |
| device_name | string 或 null | 设备名称 |
| platform_name | string 或 null | 操作系统名称 |
| platform_version | string 或 null | 操作系统版本 |
| ip_address | string 或 null | 最近 IP |
| city | string 或 null | 推断城市 |
| country | string 或 null | 推断国家 |
| country_code | string 或 null | 国家代码 |
| last_activity_at | time | 最后活动时间 |
| created_at | time | 会话创建时间 |
| current | boolean | 是否为当前请求使用的会话 |

当前会话不能通过 Session 删除接口注销，应使用 /auth/sign_out。删除其他会话会同时使其认证 Token 失效。

## 7. 推送订阅 API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/notification_subscriptions | 创建或更新推送订阅 |
| DELETE | /api/v1/notification_subscriptions | 按 push_token 删除订阅 |

| 字段 | 类型 | 说明 |
|---|---|---|
| notification_subscription.subscription_type | enum | browser_push 或 fcm |
| notification_subscription.subscription_attributes | object | 对应推送服务要求的属性 |
| subscription_attributes.endpoint | string | browser_push 的订阅地址 |
| subscription_attributes.p256dh | string | browser_push 公钥 |
| subscription_attributes.auth | string | browser_push 认证信息 |
| subscription_attributes.device_id | string | fcm 的设备标识 |
| subscription_attributes.push_token | string | 设备推送 Token，按订阅类型提供 |
| push_token | string | 删除订阅时使用的 Token |

同一个设备 Token 更新后，应删除旧订阅，避免重复通知。

## 8. Google OAuth 与 SAML 登录

### 8.1 Google OAuth

Google 登录页直接进入 Google 授权，授权完成后返回 Chatwoot。

| 步骤 | 方法与路径 | 主要字段 | 说明 |
|---|---|---|---|
| 打开 Google 授权 | Google 授权地址 | client_id、redirect_uri、response_type=code、scope=email profile | redirect_uri 必须指向已登记的 Chatwoot 回调 |
| Google 回调 | GET /omniauth/google_oauth2/callback | code 和 Provider 返回的身份结果 | 验证成功后进入现有 User 登录或新 User 开通流程 |
| 换取正式会话 | POST /auth/sign_in | email、sso_auth_token | 使用回调生成的短期 Token 建立正式认证头 |

已存在的 User 会进入登录页，并携带 email 和 sso_auth_token。sso_auth_token 有效期为 5 分钟，成功换取会话后立即失效。新 User 只在安装允许注册且邮箱通过注册策略时创建 Account，随后进入密码设置流程。

Google 回调失败、不允许注册或邮箱策略不通过时，返回登录页并携带稳定错误标识。code、sso_auth_token 和密码重置 Token 都不应写入普通分析日志。

### 8.2 SAML（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/auth/saml_login | 根据邮箱定位启用 SAML 的 Account 并进入登录流程 |
| GET、POST | /auth/saml | 进入 Account 对应的 SAML Provider 认证 |
| GET | /omniauth/saml/callback | 接收 SAML 结果并登录或创建 Account 内 User |

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 是 | 已加入 SAML Account 的用户邮箱 |
| target | enum | 否 | web 或 mobile，默认 web |
| account_id | integer | 回调必需 | 用于确定 SAML 设置和成员边界 |
| RelayState | string | 否 | mobile 时把成功或错误结果返回移动端深度链接 |

成功结果是临时重定向。Web 结果携带 email 和 5 分钟有效的 sso_auth_token；mobile 结果进入已配置的移动端深度链接。Account 不存在、未启用 SAML、SAML 设置不存在、邮箱不允许加入目标 Account 或认证结果无效时，返回带稳定错误标识的登录地址或移动端深度链接。

## 9. 安全规则

- 登录认证头、个人 Access Token、MFA Secret、备用码和重置令牌都属于敏感信息；
- 修改密码、重置 Token、移除会话后，应更新保存的认证状态；
- MFA 第一步返回 206 不代表登录完成；
- 401 表示认证失效，403 表示功能或权限不允许；
- 409 sessions_limit_reached 表示需要处理历史会话；
- Profile 的 Account 状态字段只能操作当前用户所属账号；
- Google OAuth、SAML、MFA 和会话数量限制可能受版本、企业授权和全局配置影响。
