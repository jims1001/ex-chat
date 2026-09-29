# E-ARCH-08-v166 EXT AgentBot 与收件箱绑定

| 验收项 | 结果 |
|---|---|
| Bot 所有权 | 创建要求匹配账号，更新、删除、绑定和解绑均按 account_id + bot_id |
| CHN 边界 | bind 通过 chn.inbox.get.v1 校验账号内 Inbox，EXT 不读取 Inbox 表 |
| 绑定一致性 | 同一账号 Inbox 绑定原子替换，解绑要求现有精确绑定 |
| 凭据隔离 | AccessToken 和 BotConfig 仅留在 EXT，不进入命令或查询响应 |
| 幂等 | AgentBot/AgentBotInbox 与 CommandReceipt 同事务，命令重放单写 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。EXT 尚有 Webhook、清理、容量、回退和恢复验收，ARCH-08 保持 REWORK。
