# E-ARCH-01-v1 全系统模块边界验证证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-01 / v1 |
| evidence_type | E-BAS、E-CON、E-QA |
| task_id / case_id | ARCH-01 / ARCH-01-C01..C07 |
| baseline_reference | 基础层 00–10；模块化架构 00；总控协议 00 |
| environment / topology | 本地源码静态验证；组合部署 |
| foundation / contract | foundation-v1 / module-boundary-v1 |
| started_at / finished_at | 2026-09-16T14:55:00+04:00 / 2026-09-16T15:00:22+04:00 |
| input_scope | 26 个目标模块、ARCH 后 25 个批次（23 个运行实施批次 + QA/CLOSE 两个治理批次）、当前 `internal/handler` 与 `internal/service` 数据库依赖 |
| artifact_reference | 本文件；模块化架构 00 第 2–9 节；目标文档 SHA-256 `76591e8ee896d5186179bd1049955f5f3cfbb5f896c65176a1b7a334e99c14f5`；起始 Git HEAD `ff3e0e09b945fa127a65e9efcaed50823989cecc` |
| reviewer_scope / reviewed_at | 文档结构与边界一致性复验 / 2026-09-16T15:00:22+04:00 |
| expires_at | 模块边界、foundation_version 或目标部署形态变化时失效 |
| final_result | pass；ARCH-01 DONE |

## 验证记录

| case_id | 完成条件 | 验证方法 | 实际结果 | 结论 |
|---|---|---|---|---|
| ARCH-01-C01 | 26 个模块均有唯一职责 | 解析模块总表并检查编号唯一性、职责和禁止边界非空 | 26 行、26 个唯一编号、空职责 0、空禁止边界 0 | pass |
| ARCH-01-C02 | 所有功能均有唯一主责模块 | 核对第 7 节 ARCH 后 25 个批次及兜底归属规则 | 23/23 运行实施批次已映射；QA/CLOSE 两个治理批次明确无业务数据所有权；新增功能按权威写对象归属 | pass |
| ARCH-01-C03 | 无两个模块共同拥有同一状态机 | 对照模块权威状态列与“不负责”边界 | 26/26 权威状态已登记，目标边界未发现共同所有者 | pass |
| ARCH-01-C04 | 公共协议层无业务规则 | 检查第 4 节允许/禁止清单 | 只含 ID、时间、版本、错误、分页、健康等公共语义；业务规则列入禁止清单 | pass |
| ARCH-01-C05 | 核心、可选、增强模块停用边界清楚 | 核对第 6 节和第 8.1 节逐模块降级行为 | 26/26 降级行为非空，核心/增强/可选分类完整 | pass |
| ARCH-01-C06 | 跨模块流程有单一编排者 | 检查 ORC 边界、第 7 节边界说明和兜底规则 | 跨两个及以上权威模块统一归 ORC；单模块命令不经过 ORC | pass |
| ARCH-01-C07 | 清单进入版本管理和变更审批 | `git check-ignore`、版本头和重开规则 | 文档不再被忽略；版本为 module-boundary-v1；变化强制重开 ARCH-01 | pass |

最终结构化复验输出：`overall=PASS`。

独立提交前复审：首次发现 LOG 批次遗漏并 BLOCK；补齐 `LOG → AUD`、拆分 QA/CLOSE 治理口径并更正 Redis 测试描述后再次复审，结论 PASS，无 P0/P1/P2。

## 不适用证据面

ARCH-01 是设计冻结任务，不执行运行时数据写入，因此权限、字段、重复并发、外部回执、Change 和性能指标不适用于本任务；这些项目没有被当作运行时功能通过证据，并将在对应 ARCH/LOG/API/CONS/QA 任务逐项验证。

## 当前实现差异

静态扫描发现 20 个 handler/service 文件持有 `*gorm.DB`。该事实不改变目标模块归属，但说明模块所有权和独立运行尚未验收；已经登记为 ARCH-02、ARCH-03、ARCH-07、ARCH-08、ARCH-09 的后续验证输入，不在 ARCH-01 中隐藏或误报关闭。
