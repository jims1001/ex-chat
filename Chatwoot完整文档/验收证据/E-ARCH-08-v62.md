# E-ARCH-08-v62 CRMOpsAdvancedHandler 会话 SLA 所有者边界

原 `CRMOpsAdvancedHandler.GetConversationSLA` 在 ORC 数据库中直接查询 CON `Conversation` 并预加载 CHN `Inbox`。本轮改为调用 CON `ConversationRepository.FindByID`：CON 按账号读取会话，并通过已接线的 CHN `InboxRepository.FindSummaryByID` 组合收件箱；SLA 服务继续通过 CHN/MSG 所有者合同计算营业时间和消息时间线。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `aeb01c8`；E-ARCH-08-v61 |
| 阶段分库检查 | 65 个子用例通过 |
| 专项回归 | CON/CHN 分库下当前账号会话和收件箱可组合，SLA 状态保持；跨账号会话不返回；两库均无对方权威表；首次响应、下次响应及营业时间 SLA 回归通过 |
| 依赖清单 | 复用 ORC→CON `ConversationRepository` 和 ORC→CHN `InboxRepository` 源码边；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `ConversationRepository.FindByID` 读取合同，复用 `InboxRepository.FindSummaryByID`；协作摘要 `278ff406b3206825b0992ffa84e2af589d2e6b89d794ec9961bf5b4e6d4fd15c` |
| 完整回归 | `make test` 通过；65 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器、网络及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 ORC 的 Conversation/Inbox 直接预加载 |

CRMOpsAdvancedHandler.GetConversationSLA 的 CON/CHN 访问边界已经完成。下一项为 `CRMOpsAdvancedHandler.UploadAttachment` 的 CON/MSG 会话及消息校验边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
