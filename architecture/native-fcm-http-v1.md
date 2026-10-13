# 原生 FCM HTTP v1（batch51）

## 边界与配置

本批补齐已有 `PushService` 的 FCM 原生发送路径。所有生产构造点已经核对：
只有组合路由创建该服务，再交给既有通知生产者使用。接收人、Account/User
订阅筛选、通知偏好、静音时段、五秒去重和已有 payload 业务内容保持不变；
原生发送补齐下述文档明确的固定交付选项。
这不是全新通知生产者，也不改变独立模块 `ntf.delivery.dispatch.v1`；后者仍有
事务内外发问题，需要另外设计持久化尝试与恢复，不能据此宣称已完成。

唯一原生配置来源是 SYS 持久化的 `FIREBASE_PROJECT_ID` 与
`FIREBASE_CREDENTIALS`，由窄的私有本地 owner port 一次读取；没有环境变量
覆盖、ADC、metadata 服务或默认凭证发现。SYS 与 NTF 数据库可分离。仅
`subscription_type=fcm` 使用此选择：

- 两项都是空/缺省：保留已有 URL / `FCM_GATEWAY_URL` 兼容路径。
- 完整且有效：原生 FCM；目标项目允许与凭证所属项目不同。
- 半配置、坏凭证、坏配置存储或读取失败：关闭发送并记录配置失败；不退回网关。
- 原生认证失败、Provider 拒绝或响应不明：都不退回网关，也不自动重发。

无 SYS owner 的旧式/测试构造保留原网关行为；生产路由的唯一构造和显式 owner
注入受 AST 门禁约束。此次不增加独立 HTTP 配置合同，也不暴露私钥到公开配置。

## 网络与凭证边界

凭证仅接受有限长度 service-account JSON 和至少 2048 位的 PKCS8 RSA 私钥。
仅使用 RS256，JWT 含固定 Firebase messaging scope、官方 token audience、
不超过一小时的有效期。凭证中的非官方 `token_uri` 被拒绝。网络地址固定为：

- `https://oauth2.googleapis.com/token`
- `https://fcm.googleapis.com/v1/projects/{project}/messages:send`

每个请求最多四秒；测试可以注入更短超时及 RoundTripper，不能通过凭证修改目标。
拒绝重定向，不继承 cookie jar；OAuth/发送响应最多 64 KiB。OAuth bearer 仅缓存
于内存，按请求开始时间和 Provider 的有限有效期提前刷新；慢到已过期的首次
兑换结果也不可使用。并发刷新合并，等待者可通过 context 取消。无授权、发送、
配额或网络自动重试。更新配置对后续选择生效，不沿用另一配置的 token。

## 文档 28 §13 字段核对：部分覆盖，不是完整客户端合同

- `notification.title/body`：使用既有生产者授权内容，已覆盖。
- `android.priority=high`：按原产品合同设置，仅原生发送使用。
- iOS default sound：按 FCM HTTP v1 的真实层级写为
  `message.apns.payload.aps.sound=default`（产品表中的 `apns.aps` 是简写）。
  这是 FCM 消息的苹果平台选项，不是另行实现或调用原生 APNs transport。
- `data.payload` 及其嵌套 Notification/primary_actor 结构：未完成。保留现有
  字符串 data 的 account_id/resource_id/resource_type/deep_link；不凭空生成
  Notification ID、不把 ResourceID 假装成 Account 内 Conversation display ID，
  也不为了填结构新增私密消息/对象查询。现有 NotificationType 虽可用于偏好，
  但不能据此宣称整个嵌套客户端合同已对齐。
- `apns.aps.category`：未设置。产品说明只说“每次发送的类别值”，未定义客户端
  注册的 category identifier 或可靠来源。Apple 将它解释为 App 注册的类别，
  不应随机生成，也不能擅自把业务 NotificationType 当成同一契约。
- 生产 `fcm_options.analytics_label`：未设置，产品未给出生产值或映射规则。
  §17 的 `SuperAdminTest` 仅属于超级管理员诊断发送；不能标记普通业务通知。

§17 诊断还规定 `notification.type=test`、选定订阅和不受普通偏好限制等独立
语义。本批没有新增该诊断发送流程，不把这些标记或权限特例套到生产 Dispatch。
以上 Android/iOS 固定选项由官方 FCM/Apple 协议确认；原网关 payload 全部不变。
整体工作仍有上述明确缺项和客户端真实接收/展示验收，不宣称完整实现 §13。

## 结果及持久化

网关的 `delivered` / `failed` 历史语义不变。原生新增三种结果：

- `accepted`：成功响应含有效的目标项目 message name；只表示 Provider 接受。
- `rejected`：本地配置/授权失败（尚未提交）或明确 HTTP 拒绝；ErrorDetail 的
  固定类别区分 configuration_unavailable、authorization_unavailable、
  provider_rejected、provider_throttled、redirect_rejected、token_unregistered。
- `unknown`：发送网络异常、响应丢失/超时、5xx、成功响应缺少有效 message name
  或成功响应读取失败；不能声称安全未发送。

`ProviderMessageID` 保存成功 Provider name，`RetryAfter` 仅保存可解析的秒数
或 HTTP 日期，供后续诊断；本批不会据此调度重试。日志不保存原始 Provider
错误/响应，错误及新结构化日志仅使用静态类别。原有审计中 token/title/body
字段的保留规则不扩张；原生审计与敏感精确删除的 SQL logger 明确静默。

`Dispatch` 返回的成功数量对原生表示 Provider 接受数量，绝不表示设备展示、
送达或用户已读。接受后审计落库失败时，可能返回非零数量和
`PushOutcomePersistenceError`；调用者必须检查 error，不能据此重发。
该错误的 `Outcome` 仅表示已观察到的 Provider 结果；其持久化仍未确认。
当前生产调用点采用 `go pushService.Dispatch(...)`，不读取返回数量或 error；
因此上述类型化错误不会由现有异步生产者反馈给请求方。实现会尽力输出不含
payload/token/SQL 的结构化警告，但该日志也不是持久化结果、调用者处理或
恢复凭据：日志未被保留时，失败可能只剩下被调用者忽略的返回错误。

原生要求可用的 NTF 审计存储。审计写失败立即停止本次调用的后续设备；无回退
或重发。本批没有 outbox、持久化发送尝试、调用方错误处理或重试策略变更。
现有去重仅是进程内五秒缓存；超出窗口后的重复调用，以及进程重启或发送与
落库之间崩溃的场景，都没有持久化结果/不重复发送保证。不宣称崩溃间隙的
exactly-once 或 durable-attempt 保证，也不据此推断现有调用者会自动重试。

仅当 404 / NOT_FOUND 带官方 FcmError 的 `UNREGISTERED` 时，且该结果已经
落库，才删除本次尝试的精确 ID + account + user + type + token + updated_at。
并发换 token、同 token 刷新、其他账号/用户记录保留。INVALID_ARGUMENT、
普通 404、认证/权限问题、429、5xx、超时与不明响应均不删除。清理失败返回
固定错误，已写审计不回滚，也不重发。不会删除站内通知或通知偏好。

## 验证与未完成项

本批验证只用运行时生成的合成 RSA 私钥、合成 token、注入的本地 mock 传输
和独立 SQLite/本地 PostgreSQL 存储。不读取真实 key，不请求 Google token/
发送接口，不建账号/grant，不改线上配置，不部署。

协议测试覆盖 JWT 签名/字段、缓存并发/到期/慢兑换、固定 URL/header/payload、
重定向/响应尺寸/真实 context 超时、Provider 各类结果。存储测试覆盖直连优先、
缺省兼容、配置失败关闭、审计、精确清理、并发替换/刷新、账号/用户隔离、
持久化失败停止和 SQL 脱敏。门禁结果由独立证据记录给出；此文不声称测试已运行。

不包括 APNs/WebPush 原生实现、新重试调度器、权限重设计、共享成员精度审计、
真实设备显示验证或整个通知模块验收。真实上线验证仍需另外授权的 Firebase
项目/API 权限、真实注册设备及客户端接收/展示证据。

参考：产品/API 文档 28 §§9、13、14、16，文档 24 §14；
[Firebase HTTP v1](https://firebase.google.com/docs/cloud-messaging/send/v1-api)、
[发送 API](https://firebase.google.com/docs/reference/fcm/rest/v1/projects.messages/send)、
[服务账号 OAuth](https://developers.google.com/identity/protocols/oauth2/service-account)、
[错误码](https://firebase.google.com/docs/cloud-messaging/error-codes)、
[Token 管理](https://firebase.google.com/docs/cloud-messaging/manage-tokens)、
[Android 优先级](https://firebase.google.com/docs/cloud-messaging/android-message-priority)、
[FCM ApnsConfig](https://firebase.google.com/docs/reference/fcm/rest/v1/projects.messages#ApnsConfig)、
[Apple APS 字段](https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/PayloadKeyReference.html)。
