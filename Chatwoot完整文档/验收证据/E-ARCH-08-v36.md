# E-ARCH-08-v36 CopilotThreadRepository IAM/CON 组合边界

原 `CopilotThreadRepository` 在 AIC 数据库中通过 GORM 预加载 IAM `User` 和 CON `Conversation`。本轮保留 AIC 自有的 `CaptainAssistant` 预加载，用户身份改由 IAM 所有者查询，会话摘要改由 CON 按账号查询并组合。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `caeec78`；E-ARCH-08-v35 |
| 阶段分库检查 | 43 个子用例通过 |
| 专项回归 | AIC/IAM/CON 三库隔离下，详情和列表均能组合用户及会话，跨账号会话被拒绝，AIC 不含 IAM/CON 权威表 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 IAM/CON 预加载 |

应用路由已显式接入 IAM 用户仓储和 CON 会话仓储。当所有者不可用或引用不属于当前账号时，详情和列表查询显式返回错误。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `CSATExtensionRepository` 的 CON/CUS/CHN/IAM 预加载边界。
