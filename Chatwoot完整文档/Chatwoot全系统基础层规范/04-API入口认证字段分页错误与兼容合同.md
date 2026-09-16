# API 入口、认证、字段、分页、错误与兼容合同

## 1. API 分层

| 层级 | 责任 |
|---:|---|
| E1 入口传输 | 协议、域名、请求大小、连接和基础限流 |
| E2 身份类型 | Account、Widget、Public、Platform、Admin、Plugin、Callback、Realtime |
| E3 合同版本 | Path、方法、Header、Query、Body、响应和兼容转换 |
| E4 应用授权 | Account、Role、Inbox、Feature、套餐、Scope、对象范围 |
| E5 应用组合 | 调用一个 Command/Query 或启动 ORC |
| E6 领域执行 | 权威模块执行状态和业务规则 |
| E7 事实投递 | Event、Realtime、Webhook、报表、搜索和 AUD |

外部入口不得越过 E3 至 E5 直接写内部数据。

## 2. 接口族

| 接口族 | 根范围 | 身份 | 主要用途 |
|---|---|---|---|
| Account API | /api/v1/accounts/{account_id} | Account 成员或授权 Token | 工作台和账号业务 |
| API V2 | /api/v2/accounts/{account_id} | Account 身份 | 新版报表和资源 |
| Widget API | /api/v1/widget | Website/Contact 身份 | 网站聊天 |
| Public API | /public/api/v1 | Inbox + Contact 身份 | 自建客户界面 |
| Platform API | /platform/api/v1 | Platform App | 多租户控制面 |
| Enterprise API | /enterprise/api/v1 | 企业和 Cloud 身份 | 企业专属能力 |
| Installation API | 安装管理范围 | Installation Admin | 全局配置和诊断 |
| Plugin Capability | 能力路径 | Installation + actor | 插件受控动作 |
| Provider Callback | Provider 专用路径 | 签名/Challenge/证书 | 入站和外部状态 |
| Realtime | 连接端点 | 用户或客户订阅身份 | 即时事件和补读游标 |

## 3. 公共请求上下文

| 字段位置 | 字段 | 规则 |
|---|---|---|
| Header | 认证凭证 | 各身份类型不能混用 |
| Header | Content-Type/Accept | 与请求体和响应格式一致 |
| Header | Idempotency-Key | 创建、支付、发送和不可逆动作使用 |
| Header | Request-ID | 可由入口生成或接受合法值 |
| Header | Contract-Version | 需要显式版本协商时提供 |
| Path | account_id、资源 ID | 必须重新验证归属，不能只信任路径 |
| Query | 筛选、分页、排序、时间范围 | 不能改变资源事实 |
| Body | 创建、更新和命令字段 | 只接受该动作允许的字段 |

## 4. 响应合同

成功响应按动作至少包含：

| 动作 | 必要结果 |
|---|---|
| 查询详情 | 对象、版本、可见字段和更新时间 |
| 查询列表 | 数据、稳定分页和必要 meta |
| 创建/更新 | 资源 ID、对象版本、change_id 和可见结果 |
| 异步受理 | task_id/process_id、status、查询入口和预计下一状态 |
| no_change | 明确未发生变化，不返回虚假 change_id |
| 删除/归档 | 当前生命周期状态、effective_at 和恢复能力 |
| 外部动作 | 内部受理状态与 Provider 最终状态分开 |

## 5. 错误合同

| 字段 | 说明 |
|---|---|
| code | 稳定错误代码，用于业务判断 |
| category | authentication、authorization、validation、conflict、rate_limit、external、internal |
| message | 可读说明，不能作为唯一判断依据 |
| field_errors | 字段路径、错误代码和允许提示 |
| request_id/correlation_id | 定位请求和流程 |
| retryable | 是否允许安全重试 |
| retry_after | 可重试时的等待建议 |
| current_version | 并发冲突时允许返回 |
| dependency | 可公开的依赖类别，不能泄露内部地址或 Secret |

错误状态至少区分：400 格式、401 未认证、403 无权限、404 不存在或不可见、409 冲突、422 业务校验、429 限流、500 内部异常、502/503/504 外部或暂时不可用。

## 6. 分页、排序和筛选

- 页码或游标必须由具体资源固定一种主合同；
- 默认排序必须稳定，必要时追加 object_id/change_id；
- 时间范围必须明确使用哪个时间字段；
- 空页返回明确空集合；
- 不能用当前页长度推断总数；
- 数据变化期间调用方按稳定 ID 去重；
- 高成本查询要求时间范围、最大页大小或异步导出；
- 投影查询返回 updated_at、indexed_until 或 checkpoint。

## 7. 幂等和超时

API 入口拥有 Idempotency Receipt，但业务结果仍由权威模块决定。重复键相同且请求一致时返回原结果；Payload 冲突返回 409。响应超时后使用 request_id、业务键或 task_id 查询，不能直接重复支付、发送、创建或安装。

## 8. 异步合同

异步状态统一至少支持：queued、running、waiting、succeeded、failed、cancelled。返回字段包括：status、total、processed、succeeded_count、failed_count、skipped_count、started_at、finished_at、error_summary、result_ref 和 retry/cancel 能力。

首次返回 202 只表示受理，不表示业务完成。最终状态必须可通过 Query、Event、Webhook 或资源详情确认。

## 9. 兼容和版本

| 变化 | 处理 |
|---|---|
| 新增可选响应字段 | 当前版本兼容追加 |
| 新增可选请求字段 | 兼容追加并定义默认 |
| 新增枚举 | 读取方支持 unknown |
| 删除字段或路径 | 新主版本和弃用期 |
| 改变语义、单位、身份或副作用 | 新主版本 |
| 旧路径 | 转换到当前 Command，不保留旧状态机 |
| Provider 差异 | Adapter 内处理，公共合同保持稳定 |

弃用必须声明开始、替代、告警、停止新接入、只读期和移除时间。

## 10. 限流和容量

限流维度至少包括身份、Account、接口族、资源、插件安装、Provider 连接和高成本动作。已经受理的异步任务不因后续限流丢失；429 返回恢复信息；普通查询和不可逆写入使用不同重试策略。

## 11. 验收

- [ ] 所有外部入口进入同一权威 Command/Query；
- [ ] 身份类型不能混用；
- [ ] Account、Role、Inbox、Feature、套餐和 Scope 联合生效；
- [ ] 请求、响应、错误、分页、幂等和异步字段完整；
- [ ] 超时不会制造重复结果；
- [ ] 旧入口没有第二套业务状态机；
- [ ] 敏感字段不会出现在错误、响应或诊断中；
- [ ] API 模块不拥有业务对象。
