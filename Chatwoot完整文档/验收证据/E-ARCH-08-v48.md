# E-ARCH-08-v48 ReportService EXT AgentBotInbox 绑定读取边界

原 `ReportService.GetBotMetrics` 在 RPT 数据库中直接读取 EXT 所有的 `AgentBotInbox`，用于确定机器人收件箱范围。本轮改为由 EXT 按账号返回绑定的收件箱 ID，RPT 再与 CHN 返回的同账号收件箱取交集，随后仅对该范围内的 CON 会话和 MSG 消息做报表聚合。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `a6c8810`；E-ARCH-08-v47 |
| 阶段分库检查 | 52 个子用例通过 |
| 专项回归 | RPT/EXT/CHN/CON/MSG/CUS/TEN/IAM 分库下，机器人会话数和消息数聚合正确；EXT 绑定必须通过账号范围并与 CHN 收件箱归属交叉校验；RPT 不含 `AgentBotInbox` 及其他外部权威表 |
| 依赖清单 | 新增 RPT→EXT 同步依赖及 ReportService 源码边；依赖摘要 `628daf9c68e737c9672217156b4be5bf459d23648c7d2412491e4d0222a1ad8c` |
| 协作合同 | 复用 EXT `AgentBotRepository` 的账号级绑定查询；协作摘要保持 `c4fe032703a4060bdae0fdd5b3d2db507650db7176bfa4e5cdb3b477217c4e8a` |
| 完整回归 | `make test` 通过，包含高级报表、营业时间和 v2 完整对齐用例 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则覆盖 ReportService 对 `AgentBotInbox` 的直接查询 |

ReportService 的 EXT `AgentBotInbox` 绑定读取边界已经完成。下一项为 `DataImportService` 的 CUS 联系人存在性访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
