# E-ARCH-08-v100 CHN 模板事件与独立运行面审计

本轮补齐 CHN 模板合同和第三类领域事件，并对 CHN 声明能力、所有者存储、备份恢复和就绪状态进行集中审计。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `8a6a651`；E-ARCH-08-v99 |
| 新增合同 | `chn.template.manage.v1`、`chn.templates.list.v1` |
| 模板事件 | `chn.template.changed.v1`；同一模板 upsert/delete 聚合版本为 1/2 |
| 声明面 | 7 条命令、5 条查询全部注册；3 类声明事件均存在所有者产出路径 |
| 数据隔离 | CHN 库只含 Inbox、InboxMember、InboxMessageTemplate 和 LocalChangeJournal；不存在 TEN/ENT/IAM/CON/MSG 表 |
| 恢复 | CHN 快照恢复收件箱、模板、命令收据、事件 outbox 和 recovery_complete 就绪门禁 |
| 原子幂等 | 模板业务、命令收据和事件同事务；重启重放不重复模板或事件 |
| 当前标记 | independentRuntimeImplemented=true；acceptanceStatus=runtime-surface-verified |
| 未闭环 | CHN 模块级多实例容量和混合版本升级回退尚待专项验证 |
| 合同治理 | module_contracts 新指纹 `cd93e0bfb4e03917b569edb9e5ba48535474f3455552265d29ec89ed4181476a` 已冻结并通过完整性测试 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `fdae5c358f07d8faefc1f68cfa330744b434d5b226e7da63e1a23420fd28ae70` |

本证据只关闭 CHN 独立运行“声明能力面”及恢复项，不代表 CHN 全部容量/升级验收，更不代表 ARCH-08 整体完成。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
