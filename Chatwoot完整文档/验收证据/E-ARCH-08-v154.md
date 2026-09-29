# E-ARCH-08-v154 QLT 容量、回退、事件投递与恢复

| 验收项 | 结果 |
|---|---|
| 完整声明面 | 4 Command、3 Query、3 Event 全部覆盖 |
| PostgreSQL 多实例 | 8 个 QLT 实例并发同一申诉裁决，只有 1 条收据、1 条活动和 1 个事件 |
| 混合版本 | v2 裁决由 v1 重放并读取；v1 拒绝 v2 请求版本 |
| 自动事件投递 | 质检变更事件由后台 fanout 投递，outbox 与 delivery receipt 完成 |
| 所有者备份 | 八类 QLT 模型和 LocalChangeJournal 均进入快照 |
| 恢复门禁 | 恢复前拒绝；generation 匹配的完整恢复后通过且不创建外部所有者表 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过。QLT 行运行验收关闭；下一项进入 KB。ARCH-08 保持 REWORK。
