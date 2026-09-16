# E-ARCH-02-v1 模块数据所有权验证证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-02 / v1 |
| evidence_type | E-DATA、E-CON、E-QA |
| task_id | ARCH-02 |
| baseline_reference | 基础层 00、02、05、09；模块化架构 01 |
| environment / topology | 本地源码静态验证；组合部署 |
| started_at / finished_at | 2026-09-16T15:09:00+04:00 / 2026-09-16T15:10:01+04:00 |
| input_scope | `internal/domain`、`internal/handler`、`internal/service`、模块数据所有权表 |
| result | fail；ARCH-02 REWORK |
| expires_at | 违规入口修复或对象模型变化时必须复验 |

## 状态轨迹

| 时间 | 原状态 | 新状态 | 原因 |
|---|---|---|---|
| 2026-09-16T15:08:00+04:00 | PENDING | READY | ARCH-01 完成，依赖满足 |
| 2026-09-16T15:09:00+04:00 | READY | RUNNING | 开始代码对象、状态、凭证、关系和直接写入扫描 |
| 2026-09-16T15:10:01+04:00 | RUNNING | REWORK | 扫描确认跨所有者直接写入和逐项所有权索引缺口 |

## 量化结果

| 检查项 | 目标 | 实际 | 结果 |
|---|---:|---:|---|
| 领域结构体逐项可定位所有者 | 100% | 发现 112 个领域结构体，现表仅按主对象大类登记，尚无逐项机器可核对索引 | fail |
| handler/service 直接持有数据库 | 0 | 20 个非测试文件 | fail |
| handler/service 直接写入点 | 0 或全部证明为本模块所有者内部写入 | 194 处非测试写入点，尚未全部归属证明 | fail |
| 已确认跨所有者直接写入 | 0 | 至少 22 处关键命中 | fail |
| 无人所有和多重所有冲突 | 0 | 尚不能证明为 0 | fail |

## 已确认的阻断性差异

- `internal/service/automation_service.go`：AUT 直接更新 CON 的 Conversation/ConversationLabel。
- `internal/service/sla_service.go` 与 `internal/handler/applied_sla_handler.go`：RPT/SLA 直接更新 CON 的 Conversation。
- `internal/handler/copilot_handler.go` 与 `internal/handler/ai_custom_tool_handler.go`：AIC 直接创建 TKT 的 Ticket/TicketActivity。
- `internal/service/migration_service.go` 与 `internal/service/data_import_service.go`：MIG 直接创建 CUS、CON、MSG、MED、CHN 的目标对象。
- 多个 handler 直接持有 `*gorm.DB` 并跨对象写入，无法证明写入只经过唯一所有者合同。

## 修复与复验要求

1. 建立 112 个当前领域结构体到 26 个模块的逐项所有权索引，标明权威/关系/投影/日志/请求 DTO。
2. 把上述跨所有者写入替换为目标所有者的稳定 Command 或明确的 owner repository/service 接口。
3. 对剩余 194 个写入点逐一归属，所有未证明写入视为违规。
4. 为允许冗余补齐 source_module、source_id、source_version、captured_at 和重建规则。
5. 重新执行静态扫描与相关正常、权限、失败、事务、Change 回归；冲突为 0 后才能从 REWORK 进入 RUNNING/VERIFYING/DONE。
