# Chatwoot 移动端、推送诊断与客户端入口 API

> 导航：[身份、资料、安全与登录会话 API](13-身份资料安全与登录会话API.md)｜[Super Admin 与安装级管理功能](25-SuperAdmin与安装级管理功能.md)

## 1. 功能范围

本分册覆盖浏览器通知、Android/iOS FCM 推送、设备订阅、通知偏好、会话深链、域名与应用关联、移动 SAML 回跳，以及 Super Admin 推送诊断。

推送链路由五部分组成：

1. User 在指定 Account 开启相应 push 通知偏好；
2. 浏览器或移动 App 取得设备推送凭证；
3. 客户端向 Chatwoot 注册 NotificationSubscription；
4. 业务通知满足类型和接收人条件；
5. Chatwoot 通过浏览器推送、FCM 直连或推送转发通道发送。

任一环节关闭，都可能出现“站内有通知，但设备没有推送”。

## 2. 推送订阅 API

| 方法 | 路径 | 功能 | 身份 |
|---|---|---|---|
| POST | /api/v1/notification_subscriptions | 新建或更新当前 User 的设备订阅 | 已登录 User |
| DELETE | /api/v1/notification_subscriptions | 按 push_token 删除当前 User 的移动订阅 | 已登录 User |

该 API 不含 account_id，因为订阅属于 User 设备；是否发送某个 Account 的通知由该 User 在对应 Account 的通知偏好决定。

## 3. POST 请求结构

顶层字段为 notification_subscription。

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| notification_subscription.subscription_type | enum | 是 | browser_push 或 fcm |
| notification_subscription.subscription_attributes | object | 是 | 随订阅类型变化 |

## 4. Browser Push 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| endpoint | string | 是 | 浏览器 Push Service Endpoint，同时作为设备唯一标识 |
| p256dh | string | 是 | 浏览器推送加密公钥 |
| auth | string | 是 | 浏览器推送认证 Secret |

p256dh 和 auth 虽由客户端提交，但属于敏感订阅材料，不应显示在普通页面、Webhook 或错误信息中。

## 5. FCM 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| device_id | string | 是 | 设备稳定标识，同时作为订阅唯一标识 |
| push_token | string | 是 | FCM Push Token |
| device_name | string | 否 | 设备展示名称；存在时可用于诊断 |
| platform | string | 否 | android 或 ios 等平台摘要 |
| app_version | string | 否 | 客户端版本，便于诊断 |

Chatwoot 接受 subscription_attributes 中的附加设备信息，但发送 FCM 的关键字段是 device_id 和 push_token。客户端刷新 Token 后应使用相同 device_id 再次 POST，以更新现有订阅。

## 6. POST 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 订阅 ID |
| identifier | string | browser_push 为 endpoint，fcm 为 device_id |
| user_id | integer | 当前 User ID |
| subscription_type | enum | browser_push 或 fcm |
| subscription_attributes | object | 已保存的订阅属性；客户端不应公开转发 |
| created_at | datetime | 首次创建时间 |
| updated_at | datetime | 最近注册或更新的时间 |

相同 identifier 再次注册时更新原订阅，不创建重复记录。如果同一浏览器 Endpoint 或 device_id 由另一个 User 重新登录并注册，订阅归属会转移到当前 User，避免推送继续发给上一位登录者。

## 7. 删除 FCM 订阅

| 字段 | 位置 | 必填 | 说明 |
|---|---|---:|---|
| push_token | Body 或 Query | 是 | 当前 User 要删除的 FCM Token |

删除只在当前 User 的订阅范围查找，即使提交了其他 User 的 Token，也不能删除对方订阅。目标不存在时仍可返回成功，以便退出登录流程保持幂等。

Browser Push 失效订阅通常在推送服务明确返回 expired、invalid 或 unauthorized 时自动清理；当前 DELETE 入口主要按 FCM push_token 使用。

## 8. 浏览器通知前置条件

| 条件 | 说明 |
|---|---|
| Service Worker | 浏览器必须支持并成功注册 /sw.js |
| PushManager | 浏览器必须支持 Push API |
| Notification permission | 必须为 granted；denied 后不能反复弹出授权 |
| VAPID public key | 页面配置必须提供公钥 |
| VAPID private key | 实例发送端必须有对应私钥 |
| HTTPS | 除本地受信场景外，浏览器推送要求安全来源 |

订阅成功不代表用户已开启所有通知类型。还需检查 Account 通知偏好。

## 9. FCM 发送通道

FCM 订阅有两种发送方式：

| 优先级 | 通道 | 条件 |
|---:|---|---|
| 1 | FCM v1 直连 | FIREBASE_PROJECT_ID 和 FIREBASE_CREDENTIALS 同时存在 |
| 2 | 推送转发通道 | 未配置 Firebase 凭证，且实例允许使用推送转发 |
| 3 | 跳过 | 两种通道均不可用 |

直连配置和转发通道不会同时对同一订阅重复发送。若直连凭证存在，则以直连为准。

## 10. 通知类型

| notification_type | 触发场景 | 推送内容重点 |
|---|---|---|
| conversation_creation | 新会话创建 | 会话号、Inbox 名称、首条消息摘要 |
| conversation_assignment | 会话分配给当前 User | 会话号和最近消息摘要 |
| assigned_conversation_new_message | 已分配会话出现新客户消息 | 会话号、发送者和消息摘要 |
| conversation_mention | 当前 User 在会话中被提及 | 会话号、提及消息摘要 |
| participating_conversation_new_message | 参与会话出现新消息 | 会话号、发送者和消息摘要 |
| sla_missed_first_response | 首次响应 SLA 超时 | 会话号和相关消息摘要 |
| sla_missed_next_response | 后续响应 SLA 超时 | 会话号和最近消息摘要 |
| sla_missed_resolution | 解决 SLA 超时 | 会话号和最近消息摘要 |

每种通知是否发送 Push，由 /api/v1/accounts/{account_id}/notification_settings 中对应的 push 开关决定。

## 11. Account 通知偏好

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/notification_settings | 获取当前 User 在 Account 中的邮件和推送偏好 |
| PATCH | /api/v1/accounts/{account_id}/notification_settings | 更新偏好 |

推送偏好通常按通知类型分别保存，如 push_conversation_creation、push_conversation_assignment、push_assigned_conversation_new_message、push_conversation_mention、push_participating_conversation_new_message 和各 SLA push 项。

通知偏好属于 User 与 Account 的组合。同一 User 可以在 Account A 开启、在 Account B 关闭，而设备订阅仍只有一套。

## 12. 浏览器推送 Payload

| 字段 | 说明 |
|---|---|
| title | 本地化通知标题 |
| tag | 通知类型、会话显示 ID 和通知 ID 的组合，用于避免展示冲突 |
| url | 打开目标会话的 Dashboard URL |

浏览器点击通知后应直接进入 /app/accounts/{account_id}/conversations/{conversation_display_id}。目标 User 仍需通过登录和 Account 成员权限校验。

## 13. FCM Payload

| 区域 | 字段 | 说明 |
|---|---|---|
| notification | title、body | 系统通知栏展示内容 |
| data | payload | 供 App 解析的业务数据 |
| data.notification | id | Notification ID |
| data.notification | notification_type | 通知类型 |
| data.notification | primary_actor_id | 主要对象 ID |
| data.notification | primary_actor_type | 主要对象类型，当前重点为 Conversation |
| data.notification.primary_actor | id | Account 内会话显示 ID |
| android | priority | high |
| apns.aps | sound | default |
| apns.aps | category | 每次发送的类别值 |
| fcm_options | analytics_label | 推送分析标签 |

客户端应以 account_id 和 Conversation 的显示 ID 打开正确页面；不能把 primary_actor_id 直接当作其他 Account 的会话 ID。

## 14. 失效订阅处理

| 场景 | 系统行为 |
|---|---|
| Browser Endpoint expired | 删除订阅 |
| Browser Endpoint invalid | 删除订阅 |
| Browser Endpoint unauthorized | 删除订阅 |
| Browser 限流 | 保留订阅，记录限流摘要 |
| Browser 网络超时 | 保留订阅，等待后续通知机会 |
| FCM 返回明确 Token 错误 | 删除订阅 |
| FCM 临时 5xx | 不应立即把所有设备认定为失效 |

删除失效订阅是设备清理，不应删除 User 的站内 Notification 或通知偏好。

## 15. Super Admin 推送诊断入口

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /super_admin/push_diagnostics | 按 User 查询订阅 |
| POST | /super_admin/push_diagnostics | 向选中订阅发送测试推送 |
| POST | /super_admin/push_diagnostics/destroy_subscriptions | 删除选中订阅 |

### 15.1 查询和测试字段

| 字段 | 说明 |
|---|---|
| user_query | User ID 或 email |
| user_id | 测试或删除的 User ID |
| subscription_ids | 选中的订阅 ID 数组 |
| push_title | 可选测试标题 |
| push_body | 可选测试正文 |

### 15.2 结果字段

| 字段 | 说明 |
|---|---|
| id | 订阅 ID |
| type | browser_push、fcm 或 fcm_via_hub |
| device | Browser Endpoint 主机或 device_id 尾部 |
| token_tail | Endpoint 或 Token 尾部六位 |
| status | success、failure 或 skipped |
| message | 接受结果、HTTP 摘要、错误或跳过原因 |

## 16. 测试状态解释

| status | 含义 | 下一步 |
|---|---|---|
| success | 推送 Endpoint 或 Provider 接受请求 | 再确认设备是否展示通知 |
| failure | 请求已发送但 Provider 拒绝，或发送出现异常 | 根据 message 检查 Token、凭证、网络和 Provider |
| skipped | 缺少 VAPID、Firebase 或可用转发通道 | 补齐安装配置后重试 |

浏览器 success 的准确含义是 Push Service 接受；FCM success 的准确含义是 FCM 返回 2xx。两者都不是用户已读证明。

## 17. 测试推送内容

浏览器测试 Payload 包含 title、唯一 tag 和 Dashboard URL。FCM 测试包含 title、body、notification.type=test、Android high priority、iOS default sound 和 SuperAdminTest 分析标签。

测试消息不创建业务 Conversation Notification，也不受某个 Account 的通知类型偏好限制；它只验证选中设备订阅的发送链路。

## 18. Android 域名关联

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /.well-known/assetlinks.json | 声明 Web 域名允许指定 Android App 处理链接 |

响应字段如下：

| 字段 | 说明 |
|---|---|
| relation | delegate_permission/common.handle_all_urls |
| target.namespace | android_app |
| target.package_name | ANDROID_BUNDLE_ID |
| target.sha256_cert_fingerprints | ANDROID_SHA256_CERT_FINGERPRINT 数组 |

域名必须通过 HTTPS 提供该文件，包名和签名指纹必须与正式 App 完全一致。更换签名证书时需要同步更新指纹。

## 19. iOS 域名关联

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /.well-known/apple-app-site-association | 声明 iOS Universal Link |

| 字段 | 说明 |
|---|---|
| applinks.apps | 空数组 |
| applinks.details[].appID | IOS_APP_ID，通常为 Team ID 与 Bundle ID 组合 |
| applinks.details[].paths | 当前允许 /app/accounts/*/conversations/* |

该关联只允许会话详情链接唤起 App。其他 Dashboard 路径仍按网页处理，除非未来明确扩展 paths。

## 20. iOS App 提示

当实例配置 IOS_APP_IDENTIFIER 时，Dashboard 页面可以向 iOS 浏览器声明对应 App Store App ID，用于展示应用入口。IOS_APP_IDENTIFIER 与 IOS_APP_ID 用途不同：前者是 App Store 标识，后者用于 Universal Link 关联。

## 21. 移动 SAML 回跳

移动端 SAML 登录完成后，通过自定义 Scheme 回到 App。

| 项目 | 说明 |
|---|---|
| 默认 Scheme | chatwootapp |
| 可配置项 | MOBILE_DEEP_LINK_BASE |
| 路径 | auth/saml |
| Query | email、sso_auth_token |

sso_auth_token 是短期登录材料，App 使用后应立即完成身份交换，不应写入分析事件、崩溃报告或可共享链接。

## 22. Widget 移动 WebView

Web Widget Inbox 可通过 allow_mobile_webview 控制 Widget 是否允许在移动 WebView 中展示。

| 值 | 行为 |
|---|---|
| true | 允许在移动 WebView 打开 Widget |
| false | 移动 WebView 不呈现 Widget 内容 |

该设置只影响客户 Widget 的移动 WebView 展示，不影响 Agent 移动 App、FCM 推送或 Dashboard 响应式页面。

## 23. 客户端登录和 Token

移动 App 使用与 Dashboard 相同的用户认证体系，成功后保存 access-token、client、uid 等认证头。设计要求如下：

- 认证头保存在系统安全存储，不写入普通偏好或日志；
- Token 更新后覆盖整组认证头，不能只更新 access-token；
- 退出登录时删除本地认证材料，并按当前 push_token 删除 FCM 订阅；
- 切换 User 后用同一 device_id 重新注册，使订阅归属转移；
- 打开深链前先确认登录状态和 Account 成员关系。

## 24. 故障定位顺序

| 顺序 | 检查项 | 判断 |
|---:|---|---|
| 1 | 站内 Notification 是否创建 | 未创建则检查业务触发和接收人 |
| 2 | Account push 偏好是否开启 | 关闭则不会进入推送发送 |
| 3 | User 是否有有效订阅 | 无订阅则重新注册设备 |
| 4 | 订阅 identifier 是否属于当前 User | 错误归属时重新 POST |
| 5 | Browser VAPID 或 Firebase/转发通道是否可用 | 缺失时诊断结果为 skipped |
| 6 | Super Admin 测试结果 | failure 时查看 Provider 摘要 |
| 7 | 设备系统通知权限 | Provider success 但设备不显示时重点检查 |
| 8 | 深链和域名关联 | 通知显示但点击无法进入正确会话时检查 |

## 25. 验收矩阵

| 场景 | 预期结果 |
|---|---|
| 未登录 POST 订阅 | 401 Unauthorized |
| 相同 Browser Endpoint 再注册 | 更新同一订阅，不重复创建 |
| 同一设备换 User 登录 | 订阅移动到当前 User |
| 删除不存在的 push_token | 返回成功，保持幂等 |
| 尝试删除其他 User Token | 对方订阅不受影响 |
| Account 关闭某通知类型 Push | 站内通知仍可存在，但设备不发送该类型 Push |
| Firebase 凭证存在 | FCM 使用直连通道 |
| Firebase 缺失且转发可用 | 使用推送转发通道 |
| 两种 FCM 通道均不可用 | 诊断为 skipped |
| Browser Endpoint 明确失效 | 自动删除订阅 |
| Android 关联文件 | 包名和正式签名指纹一致 |
| iOS 会话链接 | 匹配允许路径时可唤起 App |
| 移动 SAML 回跳 | Token 不泄露且能完成登录 |
