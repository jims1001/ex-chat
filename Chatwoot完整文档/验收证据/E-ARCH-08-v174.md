# E-ARCH-08-v174 NTF 容量、回退、事件投递与恢复

| 验收项 | 结果 |
|---|---|
| PostgreSQL 容量 | 8 个独立运行时竞争同一通知创建命令，保持单事实、单收据、单事件 |
| 升级回退 | v2 创建后由 v1 运行时重放并读取，v1 回退拒绝新的 v2 请求 |
| 自动事件投递 | `ntf.notification.created.v1` 经认证 HTTP 自动投递至 AUD 并记录投递收据 |
| 快照范围 | Notification、NotificationSetting、NotificationSubscription、PushDeliveryLog、LocalChangeJournal |
| 恢复门禁 | 恢复前 readiness 拒绝，匹配代次恢复后通过 |
| 存储隔离 | 恢复目标库不创建 Account、User、Inbox、Conversation、Message 外部所有者表 |

专项容量、回退、事件自动投递和恢复测试通过；模块运行时、模块隔离、完整 Go 回归、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均随本批执行通过。NTF 行关闭，ARCH-08 继续处理 AUT 及后续模块。
