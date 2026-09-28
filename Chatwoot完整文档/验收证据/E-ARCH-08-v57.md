# E-ARCH-08-v57 CRMOpsAdvancedHandler 批量分配坐席所有者边界

原 `CRMOpsAdvancedHandler` 的 `assign_agent` 批量操作在 ORC 数据库中联表读取 TEN `AccountUser` 与 IAM `User`，并直接预加载 CON `Conversation`、CHN `Inbox` 和 IAM `Assignee`。本轮改为 TEN `AccountRepository.FindMember` 校验成员并通过 IAM 补齐坐席，CON `ConversationRepository.ListByIDs` 返回账号范围内会话，CHN `InboxRepository.FindSummaryByID` 补齐收件箱摘要。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `4bdcd3c`；E-ARCH-08-v56 |
| 阶段分库检查 | 60 个子用例通过 |
| 专项回归 | TEN/IAM 分库下账号成员可用于分配，非成员及跨账号成员均被拒绝；两库均无对方权威表；批量与单项操作一致性通过 |
| 依赖清单 | 登记 ORC→TEN `AccountRepository` 源码边；ORC 已声明 TEN/CON/CHN 同步依赖，依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `AccountRepository.FindMember`、`ConversationRepository.ListByIDs`、`InboxRepository.FindSummaryByID` 查询绑定；协作摘要 `d6186d686974ed98b2c336576ca2d52a86445b889786fd4a1dcb459e0aeea9d6` |
| 完整回归 | `make test` 通过，批量操作生命周期及单项一致性回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 TEN/IAM 联表 |

CRMOpsAdvancedHandler 批量分配坐席的 TEN/IAM/CON/CHN 访问边界已经完成。下一项为 `CRMOpsAdvancedHandler` 批量更新状态时的 CON/CHN/IAM 预加载边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
