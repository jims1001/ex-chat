# E-ARCH-08-v56 WidgetHandler CON 访客会话校验边界

原 `WidgetHandler` 在事件查询、标签读取、标签添加和标签删除入口中直接读取 CON 的 `Conversation`。本轮统一改为调用 CON `ConversationRepository.FindWidgetConversation`，按账号、收件箱和访客联系人三重校验会话归属；消息更新入口同步使用该通用查询。WidgetHandler 不再持有数据库句柄。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `cc946f7`；E-ARCH-08-v55 |
| 阶段分库检查 | 59 个子用例通过 |
| 专项回归 | 独立 CON 库下正常会话可通过校验，跨账号、跨收件箱和跨访客均不返回；Widget 事件和标签越权回归通过 |
| 依赖清单 | API 已有 CON 同步依赖；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 统一为 `ConversationRepository.FindWidgetConversation` → `con.conversation.get.v1`；协作摘要 `a179f7c8bb623ae78f5ff44396e630e74657bc79a5ca154c4ee1c092a444046a` |
| 完整回归 | `make test` 通过，Widget 事件、标签和消息更新接口回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止 WidgetHandler 恢复直接数据库查询 |

WidgetHandler 的 CON 访客会话校验边界已经完成。下一项为 `CRMOpsAdvancedHandler` 批量分配坐席时的 TEN/IAM 访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
