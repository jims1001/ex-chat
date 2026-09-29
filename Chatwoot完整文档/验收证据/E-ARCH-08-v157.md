# E-ARCH-08-v157 KB 删除、门户管理与账号内容清理

| 验收项 | 结果 |
|---|---|
| 文章删除 | 目标必须为 Article，按 account_id + id 读取和删除；外账号文章返回不存在 |
| 门户创建 | 目标账号必须等于命令账号，名称和 slug 必填并受长度限制 |
| 门户更新 | 目标 Portal 必须属于命令账号，只接受显式可更新字段 |
| 账号清理 | 目标账号必须匹配，按 Article → Category → Portal 顺序仅清理该账号事实 |
| 原子与幂等 | 所有事实、CommandReceipt 和 deleted/portal.changed 事件同事务；重放单写 |
| 声明覆盖 | KB 的 6 Command、5 Query、5 Event 均已有生产或读取路径 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。KB 尚有容量、回退、投递和恢复验收，ARCH-08 保持 REWORK。
