# E-ARCH-08-v64 CRMOpsAdvancedHandler 草稿读取所有者边界

原 `CRMOpsAdvancedHandler.GetDraft` 在 ORC 数据库中直接查询 MSG `DraftMessage`。本轮新增 MSG `MessageRepository.FindDraft`，按账号、会话和用户完整范围读取草稿；未找到或读取失败时继续返回空消息，保持原接口语义，保存和删除仍由 MSG 所有者命令执行。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `46bfa88`；E-ARCH-08-v63 |
| 阶段分库检查 | 67 个子用例通过 |
| 专项回归 | MSG 独立库下仅精确账号、会话、用户范围可读取草稿；其他账号、会话或用户均不返回；MSG 库不含 CON 权威表；保存、读取、空文本清理及 DELETE 回归通过 |
| 依赖清单 | 复用 ORC→MSG `MessageRepository` 源码边；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `MessageRepository.FindDraft` 读取合同；协作摘要 `4f540c8bf33d8a9f788d5789e1171df593778c466030cd865912642242028a80` |
| 完整回归 | `make test` 通过；67 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器、网络及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 ORC 对 DraftMessage 的直接查询 |

CRMOpsAdvancedHandler.GetDraft 的 MSG 草稿读取边界已经完成。下一项为 `CRMOpsAdvancedHandler` 联系人备注的 CUS/IAM 访问及用户预加载边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
