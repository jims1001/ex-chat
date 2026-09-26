# E-ARCH-08-v45 SLAService TEN 账号枚举边界

原 `SLAService.ProcessAllBreaches` 在 RPT 数据库中直接读取 Account 表，再逐账号执行 SLA 检查。本轮由 TEN `AccountRepository.ListIDs` 提供最小账号 ID 集合，RPT 调度器只消费所有者查询结果。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `20a68d7`；E-ARCH-08-v44 |
| 阶段分库检查 | 51 个子用例通过 |
| 专项回归 | TEN/RPT/CHN/CON/MSG 分库下，由 TEN 枚举账号并产生一条 SLA 违约；RPT 不含 Account/Inbox/Conversation/Message 表 |
| 依赖清单 | 复用 RPT→TEN/CHN/CON/MSG 既有同步依赖并登记 TEN 源码边；依赖摘要保持 `8d6094d62d82b4917fe4d7179061c2e60ee3e6816cc4ff6d20f967370b893991` |
| 协作合同 | `ListIDs` 使用 TEN `ten.account.get.v1` 查询能力；协作摘要保持 `c4fe032703a4060bdae0fdd5b3d2db507650db7176bfa4e5cdb3b477217c4e8a` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 RPT 对 Account 的直接查询 |

`SLAService` 已不再直接访问 TEN、CHN、CON、MSG 权威表。下一项为 `ReportService` 的 CON/CHN/MSG/CUS 报表读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
