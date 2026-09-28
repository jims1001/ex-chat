# E-ARCH-08-v116 CUS 完整声明面与自动事件投递

CUS 独立运行时已注册 `module_contracts.json` 声明的 7 个 Command 和 5 个 Query。联系人创建、更新、合并、删除，公司更新，标签管理和账号联系人匿名化命令已覆盖声明的 6 个 Event。

| 验收项 | 结果 |
|---|---|
| 合同数量 | CUS 7 Command + 5 Query 全部注册，运行就绪通过 |
| 数据边界 | CUS 库仅含联系人、公司、标签、自定义属性及本地日志，无七类外部表 |
| 投递配置 | `MODULE_EVENT_TARGETS_JSON` 按目标模块配置 URL、Token 和允许事件集，拒绝小写目标及非本模块事件 |
| 调度周期 | `MODULE_EVENT_POLL_INTERVAL_MS` 限制为 10–60000 ms，默认 1000 ms |
| 进程生命周期 | `module-server` 启动事件工作器，收到 SIGINT/SIGTERM 后先取消工作器再关闭 HTTP 服务 |
| 自动投递 | 没有测试主动调用 dispatcher 时，worker 仍自动将 pending outbox 投递到 CON 并写入目标收据 |

完整门禁包括模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查。下一项为 CUS PostgreSQL 多实例容量、混合版本回退和所有者备份恢复；ARCH-08 保持 REWORK。
