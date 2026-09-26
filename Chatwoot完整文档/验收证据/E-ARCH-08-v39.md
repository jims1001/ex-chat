# E-ARCH-08-v39 PortalRepository IAM 作者边界

原 `PortalRepository` 在 KB 数据库中预加载文章作者 `User`。本轮保留 KB 自有的门户、分类和文章关联，文章列表、按 slug 详情和按 ID 详情的作者统一由 IAM 所有者查询组合。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `90079c8`；E-ARCH-08-v38 |
| 阶段分库检查 | 46 个子用例通过 |
| 专项回归 | KB/IAM 分库下，文章列表、slug 详情、ID 详情的分类和作者组合通过，KB 不含 User 表 |
| 依赖清单 | KB 新增 IAM 同步查询边；依赖摘要 `0d326344465397bbc651fbce06879fe19bf683bf15d51188d8aa8c5c311002b0` |
| 协作合同 | 复用 `iam.identity.get.v1`；摘要保持 `93b0298edced86759832801a5086083e885fd024d03ba571d28a624c291b89a0` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 Author 预加载 |

应用路由已显式向 KB 仓储接入 IAM 用户仓储。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `InboxRepository` 渠道健康检查中的 CON/MSG 投递统计边界。
