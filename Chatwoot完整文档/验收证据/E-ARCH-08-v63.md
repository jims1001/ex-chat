# E-ARCH-08-v63 CRMOpsAdvancedHandler 附件目标所有者边界

原 `CRMOpsAdvancedHandler.UploadAttachment` 在 ORC 数据库中直接查询 CON `Conversation`，并在 multipart 与 JSON 两条上传路径中直接查询 MSG `Message`。本轮改为 CON `ConversationRepository.MessageTargetExists` 校验账号内会话，由显式注入的 MSG `MessageRepository.FindByID` 校验账号内消息及其会话归属；附件持久化继续由 MSG `AttachmentRepository` 完成。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `3b3358a`；E-ARCH-08-v62 |
| 阶段分库检查 | 66 个子用例通过 |
| 专项回归 | CON/MSG 分库下当前账号会话与消息可用于附件目标校验；跨账号会话、跨账号消息及消息属于其他会话均被拒绝；两库均无对方权威表；multipart、JSON、权限及文件安全回归通过 |
| 依赖清单 | 复用 ORC→CON `ConversationRepository` 和 ORC→MSG `MessageRepository` 源码边；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `ConversationRepository.MessageTargetExists` 和 `MessageRepository.FindByID` 读取合同；协作摘要 `e2bd1afe808d6e0c7a1f471b2e78dd76241c7bea5b3a352cd3bf1e49af60d84e` |
| 完整回归 | `make test` 通过；66 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器、网络及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 ORC 对 Message 的直接目标查询 |

CRMOpsAdvancedHandler.UploadAttachment 的 CON/MSG 会话及消息校验边界已经完成。下一项为 `CRMOpsAdvancedHandler.GetDraft` 的 MSG 草稿读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
