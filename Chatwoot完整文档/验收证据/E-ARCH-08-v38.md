# E-ARCH-08-v38 QARepository IAM 用户组合边界

原 `QARepository` 从 QLT 数据库预加载抽样质检人、任务质检人、二次复核人、被质检坐席、申诉人、审核人和申诉活动参与人。本轮保留 QLT 自有的评分表、评分项、任务、评估、申诉和活动预加载，所有用户身份改由 IAM 所有者查询组合。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `c99934f`；E-ARCH-08-v37 |
| 阶段分库检查 | 45 个子用例通过 |
| 专项回归 | QLT/IAM 分库下，抽样规则、任务、双人复核、坐席、申诉、审核和活动用户组合通过，QLT 不含 User 表 |
| 依赖清单 | QLT 新增 IAM 同步查询边；依赖摘要 `332c4a73afb38f612710178245d74d714935cf345072003e33e9414a59ea4ca1` |
| 协作合同 | 复用 `iam.identity.get.v1`；摘要保持 `93b0298edced86759832801a5086083e885fd024d03ba571d28a624c291b89a0` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复用户预加载 |

应用路由已显式向 QA 仓储接入 IAM 用户仓储。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `PortalRepository` 的 IAM 文章作者预加载边界。
