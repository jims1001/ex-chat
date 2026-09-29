# E-ARCH-08-v144 RTE 容量、回退、事件投递与恢复

| 验收项 | 结果 |
|---|---|
| PostgreSQL 多实例 | 8 个 RTE 实例并发执行同一容量命令，仅产生 1 条策略、1 条命令收据和 2 条事件 |
| 混合版本 | v2 运行时写入后，v1 回退运行时可重放同一命令并读回容量事实；v1 拒绝 v2 请求版本 |
| 自动事件投递 | RTE 容量事件由后台 fanout 投递，目标 inbox 幂等接收，outbox 和 delivery receipt 均完成 |
| 所有者备份 | AgentCapacityPolicy、InboxCapacityLimit、AssignmentPolicy、CapacityPolicy 和 LocalChangeJournal 全部进入 RTE 快照 |
| 恢复门禁 | 目标运行时恢复前拒绝 readiness，匹配 generation 的完整恢复后通过 |
| 存储隔离 | 恢复不创建 Account、Inbox、Conversation、Message、User 或 Team 外部所有者表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过。RTE 行运行验收关闭；下一项进入 TKT 独立运行公开合同。ARCH-08 保持 REWORK。
