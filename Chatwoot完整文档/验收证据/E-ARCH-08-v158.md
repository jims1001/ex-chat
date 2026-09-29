# E-ARCH-08-v158 KB 容量、回退、事件投递与恢复

| 验收项 | 结果 |
|---|---|
| 声明面 | 6 Command、5 Query 均注册，5 Event 均有生产路径 |
| PostgreSQL 容量 | 8 个独立运行实例并发同一门户创建，最终单 Portal、单 CommandReceipt、单 Event |
| 升级回退 | v2 创建门户后由 v1 重放，并由 v1 Query 读取；回退实例拒绝 v2 请求 |
| 自动投递 | `kb.knowledge.published.v1` 从 KB outbox 自动投递至 AUD，投递收据和状态闭环 |
| 备份恢复 | Portal、Category、Article 和 LocalChangeJournal 全量快照恢复 |
| 恢复门禁 | 恢复前 readiness 拒绝，匹配代次恢复后通过 |
| 隔离 | KB 运行和恢复均不创建 IAM/TEN/MED/CON/MSG 外部所有者表 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。KB 行满足独立运行验收，ARCH-08 继续处理 BIL 及后续模块。
