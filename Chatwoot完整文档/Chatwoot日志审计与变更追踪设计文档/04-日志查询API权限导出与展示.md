# 日志查询 API、权限、导出与展示

> 日志任务：LOG-05。查询入口属于日志基础层的受控访问面，不改变任何业务对象或历史记录。

## 1. 查询目标

日志查询必须回答：

- 一个对象从创建到当前经历了哪些变化；
- 某个操作主体在指定时间内改变了哪些数据；
- 一次请求、自动化、插件、Provider 回调或迁移产生了哪些变化；
- 一次跨模块流程每个步骤的先后关系和补偿结果；
- 某个高风险动作是否经过授权、是否影响多个 Account；
- 账本是否完整，是否存在序列断层或校验失败。

## 2. 接口分层

| 接口 | 身份 | 范围 | 用途 |
|---|---|---|---|
| Account Audit API | Account Administrator 或获准自定义角色 | 当前 Account | 日常审计和对象追踪 |
| Object Timeline API | 有权查看该对象且具备审计权限的成员 | 单对象 | 会话、客户、消息、工单等时间线 |
| Security Audit API | 安全管理权限 | 当前 Account 或本人 | 登录、MFA、Session、Token 和拒绝事件 |
| Platform Audit API | Platform App 的审计 Scope | 获准管理的 Account | 多租户平台操作追踪 |
| Installation Audit API | 安装管理身份 | 平台级和安装配置 | 安装配置、版本、全局对象和高风险操作 |
| Compliance Export API | 合规导出权限和审批 | 批准的 Account、对象和时间 | 异步合规导出 |
| Integrity Verify API | 完整性验证权限 | 指定分区或 checkpoint | 验证摘要链和签名 |

Chatwoot 基线 Audit Log 可以继续使用 `/api/v1/accounts/{account_id}/audit_logs`。完整 Data Change Ledger 查询建议作为 extension 合同使用独立资源，例如 `/api/v1/accounts/{account_id}/data_changes`，不能改变现有 Audit Log 的字段语义。

### 2.1 API 动作合同

| 方法 | 路径 | 功能 | 结果形态 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/audit_logs | Chatwoot Audit Log 兼容视图 | 分页业务审计摘要，每条可带 change_id |
| GET | /api/v1/accounts/{account_id}/data_changes | 查询数据变化 | 稳定游标列表、indexed_until、ledger_checkpoint |
| GET | /api/v1/accounts/{account_id}/data_changes/{change_id} | 查询单条变化 | Data Change Record、受控 Diff、关联与完整性状态 |
| GET | /api/v1/accounts/{account_id}/audit_objects/{object_type}/{object_id}/timeline | 查询对象时间线 | 按 object_version 排序的变化与纠正链 |
| GET | /api/v1/accounts/{account_id}/audit_processes/{correlation_id}/timeline | 查询跨模块流程 | Process、Command、Change、Event、Delivery 和补偿 |
| GET | /api/v1/accounts/{account_id}/security_audit_logs | 查询安全审计 | 登录、MFA、Session、Token、权限拒绝和敏感访问 |
| POST | /api/v1/accounts/{account_id}/audit_exports | 创建异步导出 | export_id、status、冻结条件和预计范围 |
| GET | /api/v1/accounts/{account_id}/audit_exports/{export_id} | 查询导出终态 | 状态、记录数、摘要、checkpoint、下载有效期和错误 |
| POST | /api/v1/accounts/{account_id}/audit_integrity/verifications | 创建完整性验证 | verification_id、分区、checkpoint 和受理状态 |
| GET | /api/v1/accounts/{account_id}/audit_integrity/verifications/{verification_id} | 查询验证结果 | verified、断层、摘要冲突、影响范围和时间 |
| POST | /api/v1/accounts/{account_id}/data_changes/{change_id}/corrections | 追加受控纠正 | 新 correction_change_id，不改变原记录 |
| GET | /platform/api/v1/accounts/{account_id}/data_changes | Platform 授权范围查询 | 与 Account 查询同一字段语义，受 Platform Scope 限制 |

写入业务对象的接口不得直接调用这些查询路径制造 Data Change；数据变化只能由权威模块通过 L0 原子采集产生。

## 3. 列表查询字段

| 字段 | 说明 |
|---|---|
| account_id | 必须来自认证范围，不能仅相信 Path |
| since/until | occurred_at 时间范围 |
| source_module | IAM、TEN、CON、MSG 等模块 |
| object_type/object_id | 对象时间线 |
| action | create、update、merge、delete 等 |
| actor_type/actor_id | 操作主体 |
| source_type/source_id | API、插件、规则、迁移、Provider 等来源 |
| correlation_id | 跨模块流程 |
| request_id/command_id/event_id | 请求和事件定位 |
| changed_field | 查询影响指定字段的记录 |
| result | applied、compensated、corrected 等 |
| data_classification | 仅允许在权限范围内筛选 |
| page/cursor | 稳定分页位置 |
| sort | 默认 occurred_at + change_id 稳定排序 |

时间范围、Account 和分页为强制边界。高成本查询必须异步执行或缩小范围。

## 4. 详情响应字段

详情应包含：

- Data Change Record 顶层字段；
- 经过权限过滤的 diff；
- 关联的业务 Audit 摘要；
- parent_change_id、child_change_ids；
- original_change_id、compensation_change_ids；
- correlation_id 下相邻流程步骤；
- 完整性验证状态和 checkpoint；
- 日志纠正链；
- 来源对象当前是否仍存在；
- 冷归档或法律保留状态。

详情不能因为查询者有普通对象查看权限就自动展示受限 before/after。

### 4.1 导出请求字段

| 字段 | 必填 | 说明 |
|---|---|---|
| since/until | 是 | 冻结导出时间范围 |
| object_types | 否 | 限定对象类型 |
| actions | 否 | 限定变化动作 |
| actor_ids | 否 | 限定操作主体 |
| correlation_ids | 否 | 限定业务流程 |
| fields | 否 | 允许导出的字段集合，不得超出权限 |
| purpose | 是 | 合规、调查、客户请求或内部复核用途 |
| approval_id | 按范围 | restricted、大范围或跨 Account 导出的批准记录 |
| format | 是 | 允许的结构化或可阅读格式 |
| encryption_recipient | 按策略 | 导出加密接收对象或密钥引用 |

### 4.2 完整性验证请求字段

| 字段 | 必填 | 说明 |
|---|---|---|
| partition_id | 是 | 验证分区 |
| checkpoint_id | 否 | 指定检查点；为空时使用最新稳定检查点 |
| since/until | 否 | 受控验证范围 |
| verify_archive | 否 | 是否包含冷归档 |
| purpose | 是 | 例行、发布、事故或合规验证 |

### 4.3 纠正请求字段

| 字段 | 必填 | 说明 |
|---|---|---|
| corrected_fields | 是 | 允许纠正的描述、分类或展示字段，不改变原业务事实 |
| reason | 是 | 纠正原因 |
| evidence_ref | 是 | 支撑纠正的证据索引 |
| approval_id | 是 | 受控批准记录 |
| visibility | 是 | 纠正后的展示范围 |

纠正结果必须返回新的 change_id、corrected_change_id、occurred_at 和完整性状态。

## 5. 权限矩阵

| 身份 | 可看范围 | before/after |
|---|---|---|
| 普通 Agent | 默认不可进入完整审计；可看与自己会话相关的活动摘要 | 不展示受限值 |
| Administrator | 当前 Account 常规业务变化 | 按字段分级遮蔽 |
| Custom Role | 仅明确授予的模块、对象和动作 | 按 Scope 与字段分级 |
| Security Auditor | 身份、安全、权限、Token 和高风险访问 | Secret 永不展示 |
| Compliance Auditor | 批准范围和时间 | 受控完整值或脱敏导出 |
| Platform App | 授权 Account 和 Scope | 默认遮蔽，不能读取 Account 未授权内容 |
| Installation Admin | 平台级变化和安装配置 | Account 业务详情仍需额外权限 |
| Plugin | 默认无日志查询；只获取自己的 invocation/delivery | 不提供其他插件或用户完整记录 |
| Contact/Widget/Public | 不提供内部审计 | 不适用 |

## 6. 对象时间线

对象时间线应按 object_version 展示：

1. 创建；
2. 字段变化；
3. 状态变化；
4. 关系添加或移除；
5. 自动化、插件或 Provider 来源；
6. 合并、迁移、补偿和删除；
7. 纠正日志；
8. 当前对象版本。

允许把多个底层变化生成易读摘要，但摘要是展示投影，原 Data Change Record 仍是权威来源。

## 7. 跨模块流程时间线

按 correlation_id 展示：

- ORC process 和 step；
- 每步 command_id；
- 各模块 change_id；
- 业务 Event；
- Provider/Plugin/Webhook Delivery；
- 超时、重试和人工处理；
- 补偿 change_id；
- 流程最终状态。

显示顺序优先使用业务 occurred_at，并保留 recorded_at、ingested_at 以识别投递延迟。

## 8. 日志导出

1. 提交 Account、对象、时间、字段和用途；
2. 验证导出权限和必要审批；
3. 冻结查询条件、合同版本和数据分级；
4. 创建异步 Export Job；
5. 从不可变账本读取，应用遮蔽和保留策略；
6. 生成记录数、首尾 change_id、摘要和 checkpoint；
7. 提供短期有效下载地址；
8. 下载行为生成 Access Log；
9. 到期自动清理导出副本；
10. 导出任务本身生成 Audit Log。

## 9. 查看日志也要审计

以下读取必须生成 Access Log：

- 查看 restricted before/after；
- 查询 Security Audit；
- 执行大范围或跨 Account 查询；
- 导出日志；
- 解档 Cold Archive；
- 查看完整性失败详情；
- 查看 Token、证书或支付相关摘要；
- 修改保留、遮蔽或查询权限。

Access Log 记录查询人、范围、用途、字段等级、时间、结果和审批，不记录返回的完整敏感内容。

## 10. 分页和一致性

- 使用 occurred_at + change_id 或稳定 cursor；
- 新增日志不能让已读取页面无限重复；
- 对象时间线可按 object_version 分页；
- 查询返回 ledger_checkpoint，便于后续一致读取；
- Audit Index 延迟时应明确 indexed_until；
- 完整性要求高的导出直接按冻结 checkpoint 读取；
- 查询索引失败不能改变 Ledger。

## 11. LOG-05 实施步骤

1. 冻结 Account、Object、Security、Platform、Installation、Export 和 Integrity 七类接口边界；
2. 为每类接口确定认证身份、Account 范围、对象范围、时间范围和最大查询成本；
3. 冻结列表筛选、稳定游标、排序、indexed_until 和 ledger_checkpoint 字段；
4. 冻结详情、Diff、关联变化、纠正链、完整性状态和归档状态的展示合同；
5. 按角色、Scope、对象权限和字段分级建立 before/after 遮蔽矩阵；
6. 建立按 object_version 的对象时间线和按 correlation_id 的跨模块流程时间线；
7. 建立异步导出申请、审批、冻结条件、生成、下载、到期和清理流程；
8. 为受限查看、跨 Account 查询、导出、解档和完整性失败详情生成 Access Log；
9. 验证索引延迟、冷数据解档、查询超时、分页新增记录和重复翻页；
10. 验证普通对象权限不会自动获得敏感 Diff 权限；
11. 验证 Chatwoot Audit Log 兼容视图与 Data Change Ledger 能通过 change_id 对应；
12. 验证所有查询和导出入口只读，不能更改、纠正或删除历史。

## 12. LOG-05 完成判定

- [ ] Account、Object、Security、Platform、Installation、Export 和 Integrity 接口边界完整；
- [ ] 所有查询强制 Account、时间和权限范围；
- [ ] before/after 按身份和字段等级展示；
- [ ] 对象和跨模块流程时间线可完整还原；
- [ ] 导出具备审批、摘要、有效期和下载审计；
- [ ] 查看受限日志本身会生成 Access Log；
- [ ] 分页、索引延迟和冷数据解档行为明确；
- [ ] 查询入口不能修改历史记录。
