# E-ARCH-02-v1 模块数据所有权验证证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-02 / v1 |
| evidence_type | E-DATA、E-CON、E-QA |
| task_id | ARCH-02 |
| baseline_reference | 基础层 00、02、05、09；模块化架构 01 |
| environment / topology | 本地源码静态验证；SQLite 全量回归；Docker PostgreSQL 16 + Redis 7 组合部署 |
| started_at / finished_at | 2026-09-16T15:09:00+04:00 / 2026-09-16T15:47:20+04:00 |
| input_scope | `internal/domain`、`internal/handler`、`internal/service`、模块数据所有权表 |
| result | pass；ARCH-02 DONE（等待提交前终审） |
| expires_at | 违规入口修复或对象模型变化时必须复验 |

## 状态轨迹

| 时间 | 原状态 | 新状态 | 原因 |
|---|---|---|---|
| 2026-09-16T15:08:00+04:00 | PENDING | READY | ARCH-01 完成，依赖满足 |
| 2026-09-16T15:09:00+04:00 | READY | RUNNING | 开始代码对象、状态、凭证、关系和直接写入扫描 |
| 2026-09-16T15:10:01+04:00 | RUNNING | REWORK | 扫描确认跨所有者直接写入和逐项所有权索引缺口 |
| 2026-09-16T15:13:30+04:00 | REWORK | RUNNING | 开始逐项修复；AUT→CON 第一组直接写入已迁移到 CON 所有者边界 |
| 2026-09-16T15:28:10+04:00 | RUNNING | VERIFYING | 跨所有者写入修复、112 项索引及静态复扫完成，进入全量回归 |
| 2026-09-16T15:28:22+04:00 | VERIFYING | DONE | `go test ./... -count=1`、`go vet ./...`、索引完整性与差异扫描全部通过 |
| 2026-09-16T15:29:00+04:00 | DONE | REWORK | 独立提交前复审发现剩余跨所有者写入、跨租户引用校验缺口和所有权索引冲突 |
| 2026-09-16T15:36:00+04:00 | REWORK | RUNNING | 修复 AUTH/QLT 写入、owner tenant guard、索引冲突并增加专项测试 |
| 2026-09-16T15:40:00+04:00 | RUNNING | VERIFYING | 本地全量测试、go vet 和专项负向测试通过，开始 Docker 组合环境复验 |
| 2026-09-16T15:41:09+04:00 | VERIFYING | DONE | Docker PostgreSQL/Redis 全量测试通过并自动清理；等待独立终审 |
| 2026-09-16T15:43:00+04:00 | DONE | REWORK | 第二轮独立复审发现导入 tenant guard、L0 口径、MoveInbox 终态和 Order 原归属合同缺口 |
| 2026-09-16T15:44:00+04:00 | REWORK | RUNNING | 按复审逐项修复并扩充跨租户负向测试 |
| 2026-09-16T15:46:00+04:00 | RUNNING | VERIFYING | 第二轮差异全部关闭，第三轮代码终审无新代码问题，本地全量测试与 go vet 通过 |
| 2026-09-16T15:47:20+04:00 | VERIFYING | DONE | 对最新代码重跑 Docker PostgreSQL/Redis 全量测试通过并清理；证据时序已补齐 |

## 量化结果

| 检查项 | 目标 | 实际 | 结果 |
|---|---:|---:|---|
| 领域结构体逐项可定位所有者或基础机制 | 100% | 源码 112、索引 112；111 个归属 26 模块，1 个 L0 共享机制 N/A；缺失 0、额外 0、重复 0 | pass |
| handler/service 数据库持有点已归类 | 100% | 20 个非测试文件全部复扫；读取、owner 内部写入或已改走 owner repository | pass |
| handler/service 直接写入候选已归属 | 100% | 多行静态复扫剩余 55 个候选，均为文件对应模块自身权威对象、关系或日志 | pass |
| 已确认跨所有者直接写入 | 0 | 0 | pass |
| 无人所有和多重所有冲突 | 0 | 0 | pass |

## 已确认的阻断性差异

以下差异均已关闭：

- `internal/service/automation_service.go`：AUT 直接更新 CON 的 Conversation/ConversationLabel。
- `internal/service/sla_service.go` 与 `internal/handler/applied_sla_handler.go`：RPT/SLA 直接更新 CON 的 Conversation。
- `internal/handler/copilot_handler.go` 与 `internal/handler/ai_custom_tool_handler.go`：AIC 直接创建 TKT 的 Ticket/TicketActivity。
- `internal/service/migration_service.go` 与 `internal/service/data_import_service.go`：MIG 直接创建 CUS、CON、MSG、MED、CHN 的目标对象。
- 多个 handler 直接持有 `*gorm.DB` 并跨对象写入，无法证明写入只经过唯一所有者合同。

## 修复与复验要求

1. 建立 112 个当前领域结构体逐项索引；业务对象归入 26 个模块，跨模块 L0 共享机制必须显式 N/A 并登记动态归属规则。
2. 把上述跨所有者写入替换为目标所有者的稳定 Command 或明确的 owner repository/service 接口。
3. 对剩余 194 个写入点逐一归属，所有未证明写入视为违规。
4. 为允许冗余补齐 source_module、source_id、source_version、captured_at 和重建规则。
5. 重新执行静态扫描与相关正常、权限、失败、事务、Change 回归；冲突为 0 后才能从 REWORK 进入 RUNNING/VERIFYING/DONE。

## 返工进度

| 修复组 | 修改 | 验证 | 状态 |
|---|---|---|---|
| AUT → CON | 自动化的 mute、标签关系和最终 activity 更新改由 `ConversationRepository` 执行；标签定义通过 `LabelRepository` 解析；关系写入同时校验 Conversation 与 Label 的 Account 归属 | 自动化相关 Go 测试通过；`automation_service.go` 中直接写 Conversation/ConversationLabel 命中为 0 | fixed |
| RPT/SLA → CON | SLA policy、deadline 与 SLA 状态写入改由 `ConversationRepository` 执行；`AppliedSLARepository` 不再同步修改 Conversation | `go test ./test/... -run 'SLA\|AppliedSLA' -count=1` 通过；相关 SLA service/handler/repository 对 Conversation 的直接写命中为 0 | fixed |
| AIC → TKT/MSG | Copilot 与 Custom Tool 创建工单统一调用 `TicketRepository.Create`，由 TKT 在事务内生成编号、活动和状态历史；会话活动消息改由 `MessageRepository` 创建 | `go test ./internal/handler/... ./test/... -run 'Copilot\|CustomTool\|Ticket' -count=1` 通过；两个 AIC handler 对 Ticket/TicketActivity/Message 的直接创建命中为 0 | fixed |
| MIG → 目标模块 | Contact、Conversation、Message、Attachment、Inbox 的创建和更新分别改由对应 owner repository 执行；MIG 只保留迁移编排和本模块任务状态 | MIG/DataImport 源码中对五类目标对象的直接写入命中为 0；全量 Go 测试通过 | fixed |
| 补充扫描发现项 | 批量会话状态/分配/标签、路由离线消息、草稿、客户备注、集成安装状态及订单更新改由 CON、MSG、CUS、EXT、BIL owner repository 执行 | 跨所有者直接写入复扫命中为 0；剩余直接写候选均为模块自身权威对象或日志 | fixed |
| 112 个领域结构体逐项索引 | 新增 `ARCH-02-领域对象所有权索引.md`，逐项登记定义位置、所有者/基础机制、分类和说明 | 源码 112、索引 112；111 个归属 26 模块、1 个 L0 共享机制 N/A；缺失 0、额外 0、重复 0 | fixed |

## 独立复审返工关闭记录

| 复审发现 | 修复 | 验证 |
|---|---|---|
| AUTH 直接写 Inbox、Conversation、Account；QLT 直接写 Message | 分别改由 `InboxRepository`、`ConversationRepository`、`AccountRepository`、`MessageRepository` 执行 | 对应直接写入静态命中为 0 |
| Message/Ticket/Order 可引用其他 Account 的 Conversation | owner repository 在事务或写入前校验 Account + Conversation；无归属返回错误且不落库 | `TestOwnerRepositoriesRejectCrossTenantReferences` 通过 |
| AgentBot、CustomRole、Macro、Team/TeamMember、LocalChangeJournal 等与冻结边界冲突 | 按模块数据所有权表修正为 EXT、TEN、OPS、TEN、L0；AccountFeature/AccountLimit 同步归 ENT | 112/112 比对：缺失 0、额外 0、重复 0；逐项与冻结表复核 |
| 迁移与关系 owner 行为缺少专项用例 | 新增跨租户标签、保留 migration display_id、历史消息不触发实时 unread/activity 的回归 | `TestConversationLabelOwnerGuardsBothTenants`、`TestImportedRecordsPreserveHistoricalStateWithoutLiveSideEffects` 通过 |

## 可复核验证记录

| 时间 | 命令 | 结果 |
|---|---|---|
| 2026-09-16T15:38+04:00 | `go test ./test -run 'OwnerRepositories\|ConversationLabelOwner\|ImportedRecords' -count=1` | pass，0.756s |
| 2026-09-16T15:46+04:00 | `go test ./... -count=1` | pass；`test` 15.039s，其余 package 全部通过 |
| 2026-09-16T15:46+04:00 | `go vet ./...` | pass，无输出 |
| 2026-09-16T15:47+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.294s，容器/网络/数据卷已清理 |
| 2026-09-16T15:47+04:00 | `git diff --check` | pass，无输出 |
