# E-ARCH-08-v165 EXT 插件安装与禁用

| 验收项 | 结果 |
|---|---|
| 安装目标 | Target 必须为匹配账号，app_id 必须指向 EXT 全局目录应用 |
| 重新启用 | 已禁用 Installation 原位恢复 installed，不创建重复事实 |
| 禁用边界 | 按 account_id + id 读取，外账号 Installation 返回不存在 |
| 原子与幂等 | Installation、CommandReceipt 和 installed/disabled 事件同事务；重放单写 |
| 数据最小化 | Settings 和 PlatformApp Token 均不进入响应或事件 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。EXT 尚有 Bot、Webhook、清理、容量、回退和恢复验收，ARCH-08 保持 REWORK。
