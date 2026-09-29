# E-ARCH-08-v140 RTE 入队命令与队列事件

| 验收项 | 结果 |
|---|---|
| 入队边界 | `rte.queue.enter.v1` 通过 CON 查询校验本账号会话和 open/pending 状态 |
| 所有权写入 | RTE 不直接修改 CON 表，通过 `con.assignment.change.v1` 清空 assignee_id/team_id |
| 下游幂等 | 从 RTE 命令键派生稳定 CON 命令键，重复请求不会重复修改 CON |
| 本地原子性 | RTE 命令收据与 `rte.queue.changed.v1` 在 RTE 事务内一起提交 |
| 账号隔离 | 跨账号会话返回 resource_not_found，不产生下游写入或 RTE 事件 |
| 最小事件 | 事件仅含 conversation_id、inbox_id 和 queued，不复制会话实体 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 RTE 分配领取、转移、释放及分配事件；ARCH-08 保持 REWORK。
