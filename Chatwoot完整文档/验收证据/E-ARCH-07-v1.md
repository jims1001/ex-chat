# E-ARCH-07-v1 跨模块流程编排证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-07 / v1 |
| evidence_type | E-ARCH、E-CON、E-SEC、E-QA |
| task_id | ARCH-07 |
| baseline_reference | foundation-v1；orchestration-processes-v1；模块化架构 06 |
| environment / topology | 本地 Go 编排合同、机器可读流程注册表、ARCH-03/04 依赖与合同注册表、Docker PostgreSQL/Redis |
| started_at / finished_at | 2026-09-20T11:55:00+04:00 / 2026-09-20T12:21:00+04:00 |
| result | pass；ARCH-07 DONE；最终独立复审 PASS |
| expires_at | 流程、步骤、Command/Query、状态、失败或补偿规则变化时立即失效并重开 |

## 状态轨迹

| 时间 | 原状态 | 新状态 | 原因 |
|---|---|---|---|
| 2026-09-20T11:54:02+04:00 | PENDING | READY | ARCH-06 完成，依赖满足 |
| 2026-09-20T11:55:00+04:00 | READY | RUNNING | 开始冻结流程、步骤、状态、恢复、失败和补偿合同 |
| 2026-09-20T12:05:00+04:00 | RUNNING | VERIFYING | 专项、本地全量、静态与 Docker 验证通过，进入独立复审 |
| 2026-09-20T12:21:00+04:00 | VERIFYING | DONE | 五轮独立复审关闭全部差异；最终本地、静态和 Docker 验证通过 |

## 独立复审

- 最终结论：PASS，P0/P1/P2 均为 0。
- 已关闭流程步骤顺序自报、完成结果单向校验、引用过宽、Account 删除合同语义错配、证据计数与最终树验证时间链差异。

## 机器基线

| 检查项 | 实际值 | 结果 |
|---|---:|---|
| 跨模块流程类型 | 8/8 | pass |
| 唯一编排者 | ORC | pass |
| 流程状态 | 9/9 | pass |
| 失败策略 | 4/4 | pass |
| 必须启用的编排规则 | 9/9 | pass |
| 未登记 Command/Query | 0 | pass |
| Command/Query owner 不匹配 | 0 | pass |
| 依赖环或业务模块反向依赖 ORC | 0 | pass；复用 ARCH-03 自动守卫 |
| ORC 领域权威字段 | 0 | pass |
| 注册表 SHA-256 | `7d79a39a5665afd6e595c98be6a6f51a4c38cb83213aedb3d9f5482f00d2b71c` | pass |

## 正负向验证

- 每个步骤使用 `process_id + process_version + step_id` 生成稳定幂等键，Command 和结果 Query 必须已登记且属于声明模块。
- 补偿策略缺少补偿 Command、不可逆步骤缺少前置条件、非法状态迁移、终态重启、缺少当前步骤的可恢复检查点均被拒绝。
- 步骤结果要求 command_id、correlation_id、change_id、audit_id、attempt 和起止时间，支持完整因果与审计关联。
- Process Intent 只保存稳定引用；反射检查禁止 Contact、Conversation、Message、Payment、Knowledge 等领域正文字段。
- 本证据冻结设计合同和可复用守卫，不声称持久化执行器、定时调度或八类业务流程已全部上线。

## 验证记录

| 时间 | 命令 | 结果 |
|---|---|---|
| 2026-09-20T12:00+04:00 | `go test ./test -run 'TestOrchestration\|TestProcessIntent' -count=1 -v` | pass |
| 2026-09-20T12:04+04:00 | `go test ./... -count=1` | pass；`test` 16.569s |
| 2026-09-20T12:04+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-20T12:05+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 15.199s；容器、网络和数据卷已清理 |
| 2026-09-20T12:19+04:00 | `go test ./... -count=1` | pass；最终工作树 `test` 15.045s |
| 2026-09-20T12:19+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-20T12:20+04:00 | `make test-docker` | pass；最终工作树 PostgreSQL/Redis healthy，`test` 16.458s；容器、网络和数据卷已清理 |
