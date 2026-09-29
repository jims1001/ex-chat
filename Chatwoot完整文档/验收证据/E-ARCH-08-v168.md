# E-ARCH-08-v168 EXT 容量、回退与事件投递

| 验收项 | 结果 |
|---|---|
| PostgreSQL 容量 | 8 个实例并发同一插件安装，最终单 Installation、单 CommandReceipt、单 Event |
| 升级回退 | v2 安装后由 v1 重放并读取；回退实例拒绝 v2 请求 |
| 自动投递 | ext.plugin.installed.v1 自动投递至 AUD，投递状态和收据闭环 |
| 秘密隔离 | Settings 和 PlatformApp Token 不进入并发、回退或事件响应 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。EXT 尚有备份恢复验收，ARCH-08 保持 REWORK。
