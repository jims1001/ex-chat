# E-ARCH-08-v160 BIL 订单创建与更新

| 验收项 | 结果 |
|---|---|
| Checkout | 匹配账号目标，业务订单号和正金额必填，初态固定 pending |
| 订单更新 | 按 account_id + id 读取，只允许状态、承运商、物流号和仓储同步字段 |
| 账号隔离 | 外账号订单更新返回不存在 |
| 原子与幂等 | Order、CommandReceipt 和 order.changed 事件同事务；重放单写 |
| 数据最小化 | 命令响应和事件不返回客户姓名、邮箱、电话、地址和商品明细 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。BIL 尚有支付、退款、发票、容量、回退和恢复验收，ARCH-08 保持 REWORK。
