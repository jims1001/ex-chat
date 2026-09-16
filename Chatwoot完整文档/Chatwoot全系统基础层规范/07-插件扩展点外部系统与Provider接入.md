# 插件、扩展点、外部系统与 Provider 接入

## 1. 扩展原则

扩展能力必须通过公开 Capability、Event Subscription、Provider Adapter 或 ORC 流程接入。插件、Bot、AI 模型、渠道和第三方系统不得读取模块内部存储或创建第二业务状态机。

## 2. 标准扩展点

| 扩展点 | 权威模块 | 典型能力 |
|---|---|---|
| Channel Adapter | CHN、MSG | 授权、入站、出站、模板、状态和重新授权 |
| AI Provider | AIC | 模型调用、流式结果、用量和安全结果 |
| Knowledge Source | KB、AIC | 内容抓取、同步、版本和索引 |
| Custom Tool | AIC、EXT、ORC | AI 受控调用外部业务能力 |
| Integration Connector | EXT | CRM、工单、电商、协作和业务关联 |
| Webhook Subscriber | EXT | 订阅事实事件并可靠投递 |
| Agent Bot | EXT、CON、MSG | 会话事件和受控业务动作 |
| Automation Trigger/Action | AUT | 新触发条件和动作 Capability |
| Notification Channel | NTF | 邮件、推送和外部通知 |
| Importer/Migrator | MIG | 来源验证、映射、检查点和写入 Command |
| Report Exporter | RPT、EXT | 将已授权结果发送到目标系统 |
| Authentication Provider | IAM | 外部身份断言，不决定 Account 权限 |

## 3. 清单合同

| 字段组 | 必填内容 |
|---|---|
| 身份 | plugin/provider_id、name、publisher、version、contract_version |
| 兼容 | min/max system、foundation_version、required_features |
| 权限 | required_scopes、optional_scopes、data_classes、object_ranges |
| 能力 | commands、queries、subscriptions、callbacks |
| 配置 | configuration_schema、secret_fields、defaults、validation |
| 网络 | allowed_domains、protocols、callback、signature |
| 资源 | timeout、concurrency、rate_limit、payload_size、quota |
| 生命周期 | install、activate、pause、upgrade、rollback、uninstall |
| 数据 | private_data、retention、export、deletion、data_location |
| 支持 | health、diagnostics、support、deprecation |

未声明能力、域名、事件、字段或 Scope 一律禁止。

## 4. 联合授权

一次插件或 Provider 动作必须同时满足：

1. 发布者、签名和版本可信；
2. 系统和基础层版本兼容；
3. 安装或连接属于当前 Account；
4. 状态为 active/connected；
5. Account Feature、套餐和额度允许；
6. 操作主体有业务权限；
7. Scope、Inbox、Team 和对象范围允许；
8. 数据分级允许外发；
9. 域名、网络、超时、并发和速率允许；
10. 高风险外部动作已确认并有幂等键。

平台身份或插件身份不能替代实际业务操作者的权限。

## 5. 调用合同

每次外部调用至少包含：invocation_id、installation/connection_id、account_id、actor、capability、contract_version、idempotency_key、correlation_id、deadline、最小 Payload、data_classification 和 callback/result 规则。

结果区分：accepted、succeeded、failed_retryable、failed_permanent、timed_out、result_unknown、cancelled。结果未知时先按 invocation_id 或外部业务键查询。

## 6. 事件订阅和 Webhook

- 只订阅清单声明且安装时授权的 Event；
- Payload 按 Account、Scope 和字段分级过滤；
- 每次投递包含 event_id、delivery_id、event_version、时间戳和签名；
- 接收方按 event_id 幂等；
- 发送方记录尝试、响应摘要、下一重试和终态；
- 连续失败后暂停当前订阅，不影响核心业务；
- 补发受保留窗口、Scope 和删除政策限制；
- 卸载后立即停止新投递并清理私有数据。

## 7. Provider Callback

1. 根据路径和公开标识定位连接；
2. 验证签名、证书、challenge、时间窗和来源；
3. 使用 connection_id + provider_event_id 去重；
4. 保存最小 Callback Receipt；
5. Adapter 转换为标准事实或 Command；
6. 权威模块再次验证 Account、对象和状态；
7. 尽快返回接收结果，后续处理异步完成；
8. 重复、乱序、未知字段和版本漂移按合同处理；
9. 回调不能携带可修改任意 Account 的可信内部 ID。

## 8. 生命周期

### 安装或连接

验证发布者和版本 → 展示 Scope 和数据等级 → 完成授权 → 创建 inactive 实例 → 连接测试 → 真实业务闭环 → active。

### 升级

比较合同、Scope、配置、数据和迁移 → 新 Scope 重新确认 → 独立验证 → Account 灰度 → 观察 → 切换 → 保留受控回退。

### 暂停和卸载

停止新调用 → 处理运行中 Invocation → 撤销外部授权和 Secret → 导出或清理私有数据 → 保留必要审计 → 验证核心业务继续 → 到期移除兼容合同。

## 9. 故障隔离

| 扩展失败 | 基础行为 |
|---|---|
| 渠道 Adapter | 仅对应 Inbox 警告或断开 |
| AI Provider | 回退模型、停止自动回复或转人工 |
| Notification | 保留站内结果，外部投递重试 |
| Integration | 核心会话继续，外部动作失败或补偿 |
| Automation Action | 按 failure_policy 停止该动作或规则 |
| Knowledge Source | 使用最后成功版本并标记过期 |
| Report Exporter | 内部报表仍可查询 |
| Plugin Dashboard | 只影响扩展页面，不影响核心数据 |

## 10. 验收

- [ ] 所有扩展均属于已登记扩展点；
- [ ] 直接内部存储访问为 0；
- [ ] 清单、Scope、数据等级、网络和资源限制完整；
- [ ] 签名、幂等、超时、重试和结果查询通过；
- [ ] 安装、升级、暂停、回退和卸载可执行；
- [ ] 外部失败不回滚已完成核心事实；
- [ ] 新 Scope 和敏感字段会重新授权；
- [ ] 未登记私有扩展入口为 0。
