# E-ARCH-08-v46 ReportService CON/CHN/MSG/CUS 报表读取边界

原 `ReportService`、报表维度和 v2 报表在 RPT 数据库中直接查询或联表 Conversation、Inbox、Message、Contact、Label 和 ConversationLabel。本轮新增四个所有者的报表查询端口，由 RPT 消费账号范围内的事实并在内存中完成汇总、趋势、实时指标、渠道、标签矩阵、消息分组、流量、年度回顾和下钻聚合。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `50790a3`；E-ARCH-08-v45 |
| 阶段分库检查 | 52 个子用例通过 |
| 专项回归 | RPT/CON/CHN/MSG/CUS 分库下，账号汇总、消息和联系人计数、标签指标及渠道汇总正确；RPT 不含四个所有者的权威表 |
| 依赖清单 | 新增 RPT→CUS，同步登记 ReportService 到 CHN/CON/MSG/CUS 源码边；依赖摘要 `22f85ba7d9bde047ab1307121ffa6e7674126c2060fd6a2d2fc83cbd87e29f75` |
| 协作合同 | 复用 CHN/CON/MSG/CUS 已登记查询能力；协作摘要保持 `c4fe032703a4060bdae0fdd5b3d2db507650db7176bfa4e5cdb3b477217c4e8a` |
| 完整回归 | `make test` 通过，包含高级报表、营业时间和 v2 完整对齐用例 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则覆盖三个 ReportService 文件中的四类直接查询、跨表 JOIN 和预加载 |

指定的 CON/CHN/MSG/CUS 报表读取边界已经完成。ReportService 仍有 TEN/IAM 坐席团队读取和 EXT AgentBotInbox 绑定读取，需继续拆分。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
