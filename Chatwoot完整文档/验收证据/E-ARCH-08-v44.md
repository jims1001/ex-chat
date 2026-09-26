# E-ARCH-08-v44 SLAService CHN/CON/MSG 所有权边界

原 `SLAService` 在 RPT 数据库中直接查询 Inbox、Conversation 和 Message，并通过 `Preload("Inbox")` 隐式读取 CHN 数据。本轮改为分别通过 CHN、CON、MSG 所有者仓储读取收件箱摘要、SLA 候选会话和消息时间线；RPT 只保存 SLA 策略、应用结果、事件和违约记录。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `9f6e250`；E-ARCH-08-v43 |
| 阶段分库检查 | 51 个子用例通过 |
| 专项回归 | RPT/CHN/CON/MSG 分库下，SLA 截止时间、策略、收件箱和消息时间线组合正确，RPT 不含 Inbox/Conversation/Message 表 |
| 依赖清单 | 登记 RPT→CHN/CON/MSG 同步依赖；依赖摘要 `8d6094d62d82b4917fe4d7179061c2e60ee3e6816cc4ff6d20f967370b893991` |
| 协作合同 | 新增 RPT→CON `FindSLACandidates` 查询绑定；协作摘要 `c4fe032703a4060bdae0fdd5b3d2db507650db7176bfa4e5cdb3b477217c4e8a` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 RPT 对 Inbox/Message 的直接查询和 Inbox 预加载 |

`SLAService` 的收件箱、候选会话和消息访问已转到所有者边界。`ProcessAllBreaches` 仍直接从 RPT 数据库枚举 TEN Account，是下一项。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
