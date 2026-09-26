# E-ARCH-08-v27 AttachmentRepository MSG 所有权边界

原 `crm_ops_advanced_repository.go` 实际混放了 CUS `CompanyRepository`、RPT `SLARepository`、EXT `AgentBotRepository` 和附件仓储，并不存在统一的 `CRMOpsAdvancedRepository` 类型。本轮已将四个仓储物理拆分为独立文件，避免文件级所有权混淆。

`Attachment` 的权威所有者按模型索引确认为 MSG，`AttachmentRepository` 的组件合同及 MIG/ORC 调用边均由 MED 修正为 MSG。创建附件前会在 MSG 库校验 `account_id + message_id`，查询也必须携带账号，防止跨租户消息挂载或读取附件。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `fbaea38`；E-ARCH-08-v26 |
| 阶段分库检查 | 35 个子用例通过 |
| 专项回归 | MSG 独立库附件创建、账号范围查询、跨账号消息拒绝及外部表禁用通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `f11a971b7b1a438e5c93e04df40e51afdc8c6702672749f53c7c3ddb9273529a`。ARCH-03/04 保持 REOPENED；独立服务启停、恢复、扩缩、升级和备份验收尚未完成。下一项为 `OpsRepository` 的 CON/TEN/CUS/MSG 跨所有者访问边界。ARCH-08 整体仍为 REWORK。
