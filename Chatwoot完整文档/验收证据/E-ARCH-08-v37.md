# E-ARCH-08-v37 CSATExtensionRepository CON/CUS/CHN/IAM 边界

原 `CSATExtensionRepository` 从 QLT 库预加载 CON 会话、CUS 联系人、CHN 收件箱和 IAM 用户，并在收件箱筛选时 JOIN CON 会话表、在坐席指标时 JOIN IAM 用户表。本轮将这些查询全部替换为所有者合同。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `92f5d1d`；E-ARCH-08-v36 |
| 阶段分库检查 | 44 个子用例通过 |
| 详情与列表 | QLT 读取调查后，通过 CON/CUS/CHN/IAM 组合会话、联系人、收件箱、坐席和审核人 |
| 筛选与指标 | 收件箱会话范围由 CON 返回；QLT 只按会话 ID 聚合；坐席名称由 IAM 补齐 |
| 隔离保证 | 跨账号会话被拒绝；QLT 库不含 CON/CUS/CHN/IAM 权威表 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复预加载或跨表 JOIN |

应用路由已显式接入 CON/CUS/CHN/IAM 仓储。QLT 仍只保存 CSAT 调查及审核事实。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `QARepository` 的 IAM 质检人、坐席、申诉人和审核人预加载边界。
