# E-ARCH-08-v54 WidgetHandler CON/MSG 消息读取边界

原 `WidgetHandler.UpdateMessage` 从 API 数据库直接联表读取 MSG 的 `Message` 和 CON 的 `Conversation`。本轮改为由 MSG `MessageRepository.FindByID` 按账号读取消息，再由 CON `ConversationRepository.FindWidgetMessageScope` 校验会话的账号、收件箱和可选访客归属；保留原有 404、访客消息编辑限制及开发模式交互消息兼容行为。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `0cc0a62`；E-ARCH-08-v53 |
| 阶段分库检查 | 57 个子用例通过 |
| 专项回归 | CON/MSG 分库下，当前账号、收件箱及访客可定位消息；跨账号、跨收件箱和跨访客查询均不返回目标；两库均无对方权威表 |
| 依赖清单 | API 已有 CON/MSG 同步依赖；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `MessageRepository.FindByID` 与 `ConversationRepository.FindWidgetMessageScope` 查询绑定；协作摘要 `44b1a6cfeb79ebeecb0842eb859ce0af56029d311f23427bd8452c5ec4aa6f0e` |
| 完整回归 | `make test` 通过，Widget 消息更新与安全隔离回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止 WidgetHandler 恢复 CON/MSG 联表 |

WidgetHandler 的 CON/MSG 消息读取边界已经完成。下一项为 `WidgetHandler` 的 CHN/IAM 收件箱成员读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
