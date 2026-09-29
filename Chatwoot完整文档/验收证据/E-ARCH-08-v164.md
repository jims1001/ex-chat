# E-ARCH-08-v164 EXT 五类查询边界

| 验收项 | 结果 |
|---|---|
| 扩展目录 | 仅返回 PlatformApp ID、名称和时间，不返回平台 Token |
| 安装查询 | 按 account_id 和 ID/AppID 读取，不返回 Settings |
| 机器人查询 | 按账号读取 Bot 和绑定 ID，不预加载 CHN Inbox，不返回 AccessToken/BotConfig |
| 投递查询 | 按账号读取状态元数据，不返回 URL、Payload、ResponseBody |
| 删除状态 | 仅统计六类账号所有者事实，不计全局 PlatformApp |
| 存储隔离 | EXT 独立库不创建 TEN/IAM/CHN/CON/MSG 外部所有者表 |

专项通过；模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。EXT 尚有命令、事件、容量、回退和恢复验收，ARCH-08 保持 REWORK。
