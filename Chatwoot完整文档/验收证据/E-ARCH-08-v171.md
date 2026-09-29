# E-ARCH-08-v171 NTF 通知创建、更新与已读命令

| 验收项 | 结果 |
|---|---|
| 创建边界 | 账号目标必须匹配，目标用户经 TEN `ten.permissions.check.v1` 验证 |
| 类型约束 | 仅接受 assignment、mention、creation、SLA breach、system alert 五类冻结类型 |
| 更新边界 | 按 `account_id + notification_id + user_id` 更新未来延后时间或取消延后 |
| 已读边界 | 按账号锁定 NTF 自有通知，验证所属成员后仅首次写入 `read_at` |
| 幂等与原子性 | 三条命令重复请求均复用收据；事实、收据和 created/read 事件同事务 |
| 跨租户隔离 | 外账号通知返回不存在，非成员创建被拒绝且不留事实或收据 |
| 存储隔离 | NTF 数据库不创建 Account、User、Conversation、Message 外部所有者表 |

专项测试 `TestNTFNotificationCommandsValidateTENOwnershipAndStayAtomic` 通过；模块运行时、模块隔离、完整 Go 回归、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均随本批执行通过。NTF 尚有偏好、设备、投递、清理、容量、版本回退、事件自动投递和恢复验收，ARCH-08 保持 REWORK。
