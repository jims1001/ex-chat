# E-ARCH-08-v162 BIL 发票签发与完整声明面

| 验收项 | 结果 |
|---|---|
| 发票所有权 | 目标必须为本账号 Order，外账号订单返回不存在 |
| 发票防重 | invoice_number 必填并在账号内防止重复签发 |
| 余额隔离 | invoice 流水不参与 deposit/deduction 余额计算 |
| 原子与幂等 | 发票流水、CommandReceipt 和 invoice.changed 事件同事务；重放单写 |
| 声明覆盖 | BIL 的 5 Command、3 Query、5 Event 均已有生产或读取路径 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。BIL 尚有容量、回退、投递和恢复验收，ARCH-08 保持 REWORK。
