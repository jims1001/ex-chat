# E-ARCH-08-v43 AutomationService CON/CUS 标签条件边界

原 `AutomationService` 在 AUT 数据库中直接联表 `labels` 和 `conversation_labels` 计算标签条件。本轮改为由 CON 返回账号范围内的会话标签 ID，再由 CUS 组合标签详情，AUT 只使用所有者返回的标签视图判定条件。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `27f0612`；E-ARCH-08-v42 |
| 阶段分库检查 | 50 个子用例通过 |
| 专项回归 | AUT/CON/CUS 分库下，同账号会话标签条件命中，跨账号标签 ID 不命中，AUT 不含 Label/ConversationLabel 表 |
| 依赖清单 | 复用 AUT→CON/CUS 既有依赖；依赖摘要保持 `0d326344465397bbc651fbce06879fe19bf683bf15d51188d8aa8c5c311002b0` |
| 协作合同 | 新增 AUT→CUS `GetConversationLabels` 查询绑定；摘要 `fe4770ce8ba428d24b49d5ff8cf9a7bfc08a4d7993a549a94e2d56360e08e2a9` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 AUT 对 Label/ConversationLabel 的直接联表 |

`AutomationService` 剩余数据库访问仅针对 AUT 自有的 AutomationRule 和 AutomationRuleExecution。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `SLAService` 的 CHN/MSG 收件箱与消息访问边界。
