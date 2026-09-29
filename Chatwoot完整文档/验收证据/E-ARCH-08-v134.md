# E-ARCH-08-v134 MSG Provider 状态与消息重试

| 验收项 | 结果 |
|---|---|
| Provider 状态 | `msg.provider_status.update.v1` 只允许 sent/delivered/read/failed，按 account_id+message_id 更新 |
| 单调状态 | sent 可转 delivered/read/failed，delivered 只可转 read，read/failed 终止；相同状态直接返回，拒绝倒退 |
| 重试 | `msg.message.retry.v1` 只接受本账号 failed、outgoing、未删除消息，并恢复为 sent |
| 账号隔离 | 其他账号消息统一返回未找到且状态不变 |
| 原子与幂等 | 状态、命令收据与 `msg.message_status.changed.v1` 同事务；重放各只产生一个事件 |
| 数据最小化 | 状态事件只含消息、会话、状态和 retry 操作，不含正文或 Provider 私有响应 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MSG 已读位置更新与事件；ARCH-08 保持 REWORK。
