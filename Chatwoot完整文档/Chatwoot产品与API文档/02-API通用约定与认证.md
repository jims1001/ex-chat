# API 通用约定与认证

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 接口类型

| 接口面 | 根路径 | 调用方 | 认证方式 |
|---|---|---|---|
| Account API | /api/v1/accounts/{account_id} | 管理员、客服、内部应用 | 用户会话令牌或 API Access Token |
| API V2 | /api/v2/accounts/{account_id} | 报表等新版接口 | 用户会话令牌或 API Access Token |
| Widget API | /api/v1/widget | 官方网站聊天组件 | Website Token、客户令牌和可选身份 HMAC |
| Public Client API | /public/api/v1 | 自定义客户聊天界面 | Inbox Identifier、Contact Identifier 和客户令牌 |
| Platform API | /platform/api/v1 | SaaS 控制平面 | Platform App Token |
| Enterprise API | /enterprise/api/v1 | 企业云功能 | 对应企业身份与授权 |
| Provider Callback | Provider 专用路径 | 第三方渠道 | Provider 规定的校验方式 |
| Outbound Webhook | 管理员配置的外部 URL | 客户系统 | HMAC、时间戳和 Delivery ID |
| Realtime | WebSocket Endpoint | Dashboard、Widget | 用户或 PubSub 上下文 |

## 2. 请求头

| Header | 必填场景 | 说明 |
|---|---:|---|
| api_access_token | 使用 API Token 时 | User、Agent Bot 或 Platform App 的 Token；不同 Token 不能混用接口面 |
| access-token | Dashboard 会话 | 登录会话令牌 |
| client | Dashboard 会话 | 客户端标识 |
| uid | Dashboard 会话 | 登录用户标识，通常为邮箱 |
| Content-Type | 有请求体时 | application/json 或 multipart/form-data |
| Accept | 建议 | application/json |
| Idempotency-Key | 接收方自行扩展时 | 当前并非所有接口原生支持，不能假设全局有效 |

## 3. Path、Query 与 Body

| 位置 | 使用方式 |
|---|---|
| Path | 资源归属和资源 ID，例如 account_id、conversation_id |
| Query | 分页、搜索、排序、状态和日期范围 |
| Body | 创建、更新、批量条件和业务命令参数 |
| Header | 身份、内容类型、签名和请求关联信息 |

## 4. 常用 Query 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| page | integer | 页码，通常从 1 开始 |
| sort | string | 资源支持的排序方式 |
| search | string | 关键词；部分接口使用 q 或 search_key |
| status | string | 资源状态过滤 |
| inbox_id | integer | Inbox 过滤 |
| assignee_type | string | 分配对象过滤 |
| team_id | integer | Team 过滤 |
| labels | string 或 array | 标签过滤，具体格式按接口定义 |
| from | integer 或 date | 起始时间 |
| to | integer 或 date | 结束时间 |

## 5. 通用字段类型

| 类型 | 约定 |
|---|---|
| ID | 通常为整数；公开标识可能为字符串或 UUID |
| 时间 | 可能为 Unix 秒、Unix 浮点秒或 ISO 8601，按字段说明解析 |
| boolean | true 或 false，不应使用字符串替代 |
| object | JSON 对象；未知新字段应忽略并保留兼容性 |
| array | 列表；空列表与 null 含义不同 |
| nullable | 可以返回 null，不应强制转换为空字符串或 0 |
| enum | 只能使用接口列出的值；客户端应兼容新增值 |

## 6. 列表响应

列表可能使用以下形式：

| 形式 | 说明 |
|---|---|
| 直接数组 | 响应主体就是资源数组 |
| payload + meta | payload 保存资源，meta 保存分页或计数 |
| data + meta | 部分新版接口使用 data |
| 业务对象 | 报表或统计按指标返回，不使用普通资源列表 |

常见 meta 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| count | integer | 符合条件的总数或当前统计数 |
| current_page | integer | 当前页 |
| total_pages | integer | 总页数，部分接口不返回 |
| per_page | integer | 每页数量，部分接口不返回 |

## 7. 认证与权限

### 7.1 Account API

调用者必须同时满足：

- Token 有效；
- 是目标 Account 的成员；
- 角色或自定义权限允许该动作；
- 如果资源属于 Inbox，还需要对应 Inbox 访问权限；
- Account 已启用相关 Feature；
- Enterprise 功能具有有效授权。

### 7.2 Widget 与 Public Client

客户身份只能访问自己的 Contact、Conversation 和 Message。Inbox Identifier、Contact Identifier 或客户令牌不能用于读取其他客户数据。开启强制身份 HMAC 后，未验证身份不能使用受保护能力。

### 7.3 Platform API

Platform App 只能管理已授权给该应用的资源。Platform Token 不等同于 Account 用户 Token。

## 8. 状态码

| 状态码 | 含义 | 客户端处理 |
|---:|---|---|
| 200 | 查询或更新成功 | 读取响应主体 |
| 201 | 创建成功 | 保存返回资源 ID |
| 202 | 已接受异步处理 | 继续查询状态或等待事件 |
| 204 | 成功且无响应主体 | 不解析 JSON |
| 400 | 请求格式或业务参数错误 | 修正请求 |
| 401 | 未认证或 Token 无效 | 重新认证，不盲目重试 |
| 403 | 已认证但无权限 | 停止请求并提示权限问题 |
| 404 | 资源不存在或不属于当前范围 | 核对 Account 和资源 ID |
| 409 | 状态或唯一性冲突 | 重新获取最新状态 |
| 422 | 字段校验或业务规则失败 | 展示字段错误并修正 |
| 429 | 触发限流 | 按 Retry-After 或退避策略重试 |
| 500 | 服务异常 | 使用请求关联信息排查 |
| 502/503/504 | 外部服务或系统暂时不可用 | 只对安全请求进行退避重试 |

## 9. 错误字段

| 字段 | 类型 | 说明 |
|---|---|---|
| error | string 或 object | 单一错误或结构化错误 |
| errors | array 或 object | 多项字段错误 |
| message | string | 面向调用方的错误说明 |
| code | string | 稳定错误代码，只有部分接口提供 |
| success | boolean | 部分命令接口返回的结果标志 |

客户端不应只依赖自然语言 message 判断业务分支，应优先使用 HTTP 状态码和稳定 code。

## 10. 幂等与重试

| 操作 | 建议 |
|---|---|
| 查询 | 网络失败时可安全重试 |
| 更新状态 | 重试前重新读取最新状态 |
| 创建 Contact | 使用稳定 identifier，并先查询已有对象 |
| 创建 Conversation | 保存返回 ID，超时后先查询再决定是否重建 |
| 创建 Message | 使用 echo_id 或 source_id 关联本地请求和服务端消息 |
| 删除 | 将 404 视为资源已不存在，但需区分越权 |
| Webhook 接收 | 以 Delivery ID 或 Provider Event ID 去重 |
| 外部副作用 | 只有目标系统支持幂等时才能自动重试 |

## 11. 兼容性规则

- 响应新增字段属于兼容变化；
- 枚举可能新增值，客户端应提供未知值回退；
- 不应依赖对象字段顺序；
- 不应假设所有列表使用相同外层结构；
- 时间字段必须按具体说明解析；
- 删除、状态切换和异步操作应以最终查询结果为准；
- 升级前应对实际使用的路径、字段、权限和事件做合同测试。

登录、MFA、Profile、登录设备和 SAML 登录详见：[身份、个人资料、安全与登录会话 API](13-身份资料安全与登录会话API.md)。

公开接口补充字段见 [API 请求与响应遗漏字段补充字典](38-API请求响应遗漏字段补充字典.md)；不同列表的 page、per_page、排序、筛选和容量边界见 [列表查询、分页、排序、筛选与容量边界](39-列表查询分页排序筛选与容量边界.md)。
