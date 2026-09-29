# E-ARCH-08-v170 NTF 四类查询边界

| 验收项 | 结果 |
|---|---|
| 通知列表 | 同时限制 account_id/user_id，可选只读未读项 |
| 未读计数 | 同时限制账号、用户和 read_at IS NULL |
| 投递查询 | 按 account_id + id，外账号投递返回不存在 |
| 数据最小化 | 不返回 DeviceToken、Title、Body、ErrorDetail |
| 删除状态 | 仅统计 Notification、Setting、Subscription、PushDeliveryLog |
| 存储隔离 | 不创建 IAM/TEN/CON/MSG 外部所有者表 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。NTF 尚有命令、事件、容量、回退和恢复验收，ARCH-08 保持 REWORK。
