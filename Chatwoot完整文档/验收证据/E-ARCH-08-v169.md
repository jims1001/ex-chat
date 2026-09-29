# E-ARCH-08-v169 EXT 七类所有者数据恢复

| 验收项 | 结果 |
|---|---|
| 快照范围 | AgentBot、AgentBotInbox、DashboardApp、IntegrationInstallation、PlatformApp、Webhook、WebhookDelivery、LocalChangeJournal |
| 完整性 | 七类所有者模型及 outbox 日志恢复后数量一致 |
| 恢复门禁 | 恢复前 readiness 拒绝，匹配 generation 恢复后通过 |
| 存储隔离 | 恢复目标不创建 IAM/TEN/CHN/CON/MSG 外部所有者表 |
| 敏感数据 | Token、Settings、Bot 凭据及 Webhook 内容仅在 EXT 快照内恢复 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。EXT 行满足独立运行验收，ARCH-08 继续处理 NTF 及后续模块。
