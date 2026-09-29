# E-ARCH-08-v139 RTE 队列、分配、候选与容量查询

| 验收项 | 结果 |
|---|---|
| 分配查询 | `rte.assignment.get.v1` 通过 CON 会话查询返回 assignee_id/team_id 最小分配摘要 |
| 队列查询 | `rte.queue.get.v1` 通过 CON 状态与分配字段计算 queued，不读取 CON 表 |
| 容量查询 | `rte.capacity.get.v1` 先经 TEN 校验成员，再读取 RTE 自有 CapacityPolicy，支持账号默认策略 |
| 候选查询 | `rte.candidates.list.v1` 经 CHN 校验收件箱、TEN 获取账号成员、CON 分状态统计活跃会话，并组合 RTE 容量 |
| 可用判定 | offline 成员不可用；在线成员按 active_conversations 与 conversation_limit 判定 |
| 账号隔离 | CON/CHN/TEN 请求均携带 account_id，RTE 本地策略也按 account_id+user_id 范围读取 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 RTE 入队与队列事件；ARCH-08 保持 REWORK。
