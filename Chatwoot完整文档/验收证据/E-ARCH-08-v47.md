# E-ARCH-08-v47 ReportService TEN/IAM 坐席及团队边界

原 `ReportService` 在 RPT 数据库中直接读取 AccountUser、User、Team 和 TeamMember，用于坐席指标、团队指标、外发消息分组和实时坐席报表。本轮改为由 TEN 返回账号成员和团队，由 IAM 批量补齐用户资料，RPT 只进行报表聚合和分页。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `fdffe25`；E-ARCH-08-v46 |
| 阶段分库检查 | 52 个子用例通过 |
| 专项回归 | RPT/TEN/IAM/CON/CHN/MSG/CUS 分库下，坐席指标、团队成员计数和按坐席外发消息统计正确；RPT 不含 IAM/TEN 及其他外部权威表 |
| 依赖清单 | 新增 RPT→IAM 并登记 ReportService 到 TEN/IAM 源码边；依赖摘要 `43c6cba8ed6912caf108e0cf57ff787bafa42601fe3d4849f4453571b2ae3d11` |
| 协作合同 | 复用 TEN 账号成员/团队及 IAM 用户查询能力；协作摘要保持 `c4fe032703a4060bdae0fdd5b3d2db507650db7176bfa4e5cdb3b477217c4e8a` |
| 完整回归 | `make test` 通过，包含高级报表、营业时间和 v2 完整对齐用例 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则覆盖 ReportService 对 IAM/TEN 模型的直接查询和 User 预加载 |

ReportService 的 TEN/IAM 坐席及团队读取边界已经完成。下一项为 ReportService 的 EXT AgentBotInbox 绑定读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
