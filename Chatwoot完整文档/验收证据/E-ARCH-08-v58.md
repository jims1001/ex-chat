# E-ARCH-08-v58 CRMOpsAdvancedHandler 批量状态更新所有者边界

原 `CRMOpsAdvancedHandler` 的 `update_status` 批量操作在 ORC 数据库中直接读取 CON `Conversation`，并隐式预加载 CHN `Inbox` 与 IAM `Assignee`。本轮改为 CON `ConversationRepository.ListByIDs` 按账号批量读取会话，CHN `InboxRepository.FindByIDs` 与 IAM `UserRepository.FindByIDs` 分别批量补齐收件箱和当前坐席。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `c150283`；E-ARCH-08-v57 |
| 阶段分库检查 | 61 个子用例通过 |
| 专项回归 | CON/CHN/IAM 分库下只组合当前账号会话、收件箱及坐席；跨账号会话不进入集合，三库均无对方权威表；批量与单项状态更新一致性通过 |
| 依赖清单 | 登记 ORC→IAM `UserRepository` 源码边；ORC 已声明 CON/CHN/IAM 同步依赖，依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `UserRepository.FindByIDs` 查询绑定，复用 `ConversationRepository.ListByIDs` 与 `InboxRepository.FindByIDs`；协作摘要 `7f776e4bbcaf26b3b736de0f90553a6a818ad0b8ceee71dc01d6f65789917853` |
| 完整回归 | `make test` 通过，批量操作生命周期、状态业务缺陷及单项一致性回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 Assignee 隐式预加载 |

CRMOpsAdvancedHandler 批量更新状态的 CON/CHN/IAM 访问边界已经完成。下一项为 `CRMOpsAdvancedHandler` 批量分配团队时的 TEN/CON/CHN/IAM 访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
