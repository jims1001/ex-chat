# E-ARCH-08-v156 KB 文章创建、发布与归档

| 验收项 | 结果 |
|---|---|
| 作者边界 | 数字 Actor 经 TEN 成员合同校验，非本账号成员拒绝 |
| 结构所有权 | Portal 与 Category 均按 account_id 读取，且 Category 必须属于目标 Portal |
| 创建约束 | title/slug/content 必填，账号门户内 slug 不得重复，初始状态固定 draft |
| 状态边界 | publish 只接受 draft/archived；archive 拒绝相同状态并使用当前状态条件更新 |
| 原子与幂等 | Article、CommandReceipt 和 changed/published/archived 事件同事务；重放单写 |
| 数据最小化 | 事件不含文章正文，仅含文章、门户、分类和状态标识 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均纳入本批验收。KB 尚有删除、门户、账号清理和独立运行验收，ARCH-08 保持 REWORK。
