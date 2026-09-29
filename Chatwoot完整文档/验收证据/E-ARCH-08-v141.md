# E-ARCH-08-v141 RTE 分配领取、转移与释放

| 验收项 | 结果 |
|---|---|
| 领取 | `rte.assignment.claim.v1` 默认使用当前 actor，亦可显式指定 assignee_id |
| 转移 | `rte.assignment.transfer.v1` 接受坐席、团队或两者组合，拒绝空变更 |
| 释放 | `rte.assignment.release.v1` 通过 CON 命令同时清空坐席和团队 |
| 所有者边界 | 三类操作先通过 CON 查询校验账号和会话，再调用 `con.assignment.change.v1`，RTE 不写 CON 表 |
| 幂等 | 每个 RTE 命令派生稳定 CON 命令键；RTE 收据防止重复下游调用和重复事件 |
| 最小事件 | `rte.assignment.changed.v1` 仅含会话、收件箱、坐席、团队和操作类型 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 RTE 容量策略管理与容量/可用性事件；ARCH-08 保持 REWORK。
