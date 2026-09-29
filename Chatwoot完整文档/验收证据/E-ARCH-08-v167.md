# E-ARCH-08-v167 EXT Webhook 生命周期与完整声明面

| 验收项 | 结果 |
|---|---|
| Webhook 管理 | 创建要求匹配账号；更新和删除按 account_id + id |
| 分发队列 | 只接受本账号 Webhook 和有效 JSON，创建 pending Delivery |
| 结果闭环 | 只更新本账号 Delivery，状态限制 delivered/dead_letter，attempts 单调递增 |
| 账号清理 | 删除六类本账号事实，外账号事实和全局 PlatformApp 保持不变 |
| 秘密隔离 | Webhook Secret、Payload、ResponseBody 不进入响应或事件 |
| 原子与幂等 | Delivery、CommandReceipt 和 delivery.changed 同事务；重放单写 |
| 声明覆盖 | 7 Command、5 Query、4 Event 均已有生产或读取路径 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。EXT 尚有容量、回退、投递和恢复验收，ARCH-08 保持 REWORK。
