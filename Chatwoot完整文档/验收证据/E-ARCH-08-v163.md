# E-ARCH-08-v163 BIL 容量、回退、事件投递与恢复

| 验收项 | 结果 |
|---|---|
| PostgreSQL 容量 | 8 个实例并发同一 checkout，最终单 Order、单 CommandReceipt、单 Event |
| 升级回退 | v2 checkout 后由 v1 重放并读取订单；回退实例拒绝 v2 请求 |
| 自动投递 | `bil.order.changed.v1` 从 BIL outbox 自动投递至 AUD，投递状态和收据闭环 |
| 备份恢复 | AccountBilling、AccountSubscription、Order、SubscriptionPlan 和 LocalChangeJournal 全量恢复 |
| 恢复门禁 | 恢复前 readiness 拒绝，匹配代次恢复后通过 |
| 隔离 | BIL 运行和恢复均不创建 TEN/CUS/CON/IAM 外部所有者表 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。BIL 行满足独立运行验收，ARCH-08 继续处理 EXT 及后续模块。
