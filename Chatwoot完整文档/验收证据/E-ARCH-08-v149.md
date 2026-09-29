# E-ARCH-08-v149 TKT 容量、回退、事件投递与恢复

| 验收项 | 结果 |
|---|---|
| 完整声明面 | 5 Command、4 Query、3 Event 和联系人合并消费全部覆盖 |
| PostgreSQL 多实例 | 8 个 TKT 实例并发执行同一匿名化命令，只有 1 条命令收据和一次业务执行 |
| 混合版本 | v2 写入的匿名化事实可由 v1 重放并读取；v1 拒绝 v2 请求版本 |
| 自动事件投递 | 工单创建事件由后台 fanout 投递，outbox 与 delivery receipt 完成 |
| 所有者备份 | Ticket、Comment、Activity、StatusHistory、Attachment、Watcher 和 LocalChangeJournal 均进入 TKT 快照 |
| 恢复门禁 | 恢复前 readiness 拒绝；generation 匹配的完整恢复后通过，且不创建外部所有者表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过。TKT 行运行验收关闭；下一项进入 QLT 独立运行公开合同。ARCH-08 保持 REWORK。
