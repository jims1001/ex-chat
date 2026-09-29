# E-ARCH-08-v161 BIL 支付处理与退款

| 验收项 | 结果 |
|---|---|
| 支付所有权 | 目标必须为本账号 Order，外账号订单返回不存在 |
| 支付结果 | completed 记 deduction 并投递 completed；failed 记独立失败流水并投递 failed |
| 退款边界 | 只接受本账号 completed payment，累计退款不得超过原支付金额 |
| 余额语义 | 成功支付扣减、退款恢复、失败流水不改变余额 |
| 原子与幂等 | 流水、CommandReceipt 和支付事件同事务；重放单写 |
| 数据最小化 | 支付事件只含流水、订单、金额、币种和状态 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。BIL 尚有发票、容量、回退和恢复验收，ARCH-08 保持 REWORK。
