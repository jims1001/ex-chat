# E-ARCH-08-v127 CON 状态、优先级变更与状态事件

| 验收项 | 结果 |
|---|---|
| 状态命令 | `con.status.change.v1` 仅允许 open/pending/resolved/snoozed；snoozed 必须提供未来 RFC3339 时间，其他状态清除休眠时间 |
| 优先级命令 | `con.priority.change.v1` 仅允许 low/medium/high/urgent |
| 账号隔离 | 两个命令均以 account_id 与 conversation_id 联合定位，拒绝修改其他账号会话或 deleting 会话 |
| 原子性 | 状态/优先级、对象版本、命令收据与对应 outbox 在同一事务提交 |
| 幂等 | 两个命令各重放两次，只产生两条收据、两个事件，对象版本由 1 增至 3 |
| 事件边界 | 状态发布 `con.conversation_status.changed.v1`；优先级发布最小 `con.conversation.changed.v1`，均不复制外部实体 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 CON 参与者管理与账号会话匿名化；ARCH-08 保持 REWORK。
