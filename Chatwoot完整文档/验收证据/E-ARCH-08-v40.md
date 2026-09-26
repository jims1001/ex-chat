# E-ARCH-08-v40 InboxRepository CON/MSG 投递统计边界

原 `InboxRepository.CheckChannelHealth` 在 CHN 数据库中直接联表查询 Conversation 和 Message。本轮改为先由 CON 按账号和收件箱返回会话 ID，再由 MSG 统计 24 小时投递数、失败数和最近外发状态，CHN 只组合健康结果。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `fe1aed8`；E-ARCH-08-v39 |
| 阶段分库检查 | 47 个子用例通过 |
| 专项回归 | CHN/TEN/CON/MSG 分库下，连续 3 条失败外发消息被判定为不健康，CHN 不含 Conversation/Message 表 |
| 依赖清单 | 本轮复用现有 CON/MSG 所有者查询；依赖摘要保持 `0d326344465397bbc651fbce06879fe19bf683bf15d51188d8aa8c5c311002b0` |
| 协作合同 | 进程内所有者端口组合；摘要保持 `93b0298edced86759832801a5086083e885fd024d03ba571d28a624c291b89a0` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 CHN 对 Message/Conversation 的查询 |

应用路由已显式向 CHN 收件箱仓储接入 CON 会话仓储和 MSG 消息仓储。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `AutomationService` 分配坐席/团队时的 TEN/IAM 访问边界。
