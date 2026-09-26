# E-ARCH-08-v15 RTE 策略 fallback 组合边界

## 本次子项结论

已关闭 RTE 分配策略详情和列表对 fallback 用户、团队的跨所有者预加载与关联写入。RTE 仓储只保存并查询 `FallbackAssigneeID`、`FallbackTeamID`；handler 通过 TEN 账号成员查询组合 IAM 用户资料，并通过 TEN 团队摘要查询组合团队资料。

创建和更新策略时先验证 fallback 归属。跨账号用户或团队返回 400 且不修改 RTE；IAM 依赖故障返回 500，不会静默输出不完整详情。RTE 的 Create/Update 显式排除两个关联对象，避免 GORM 将 IAM/TEN 对象隐式写入 RTE。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `810e34b`；E-ARCH-08-v14 |
| 验证时间 | 2026-09-26T06:57:00+00:00 |
| 阶段分库竞争检查 | 23 个子用例通过 |
| 专项回归 | 策略 CRUD、跨账号拒绝、依赖故障及架构合同检查通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增 RTE/TEN/IAM 分库用例覆盖详情与列表组合、创建和更新不写外部对象、跨账号用户拒绝、跨账号团队拒绝、无效写入不落库及 IAM 停止。RTE 数据库未创建 `User`、`Team` 或 `AccountUser` 权威表。

## 合同与剩余边界

协作合同新增 RTE 对 TEN `AccountRepository.FindMember` 和 `TeamRepository.FindSummaryByID` 的查询调用；TEN 的成员端口通过既有 IAM 用户端口补齐身份资料。合同摘要为 `7b235803538d66b94119c3ace5a0eb110379b829291757ed92b19a90b875d4e7`。ARCH-04 保持 REOPENED。

本次只关闭策略详情和列表的 fallback 组合边界。下一项为 `RoutingService` 对 fallback 用户、收件箱成员、会话计数等跨所有者数据的直接访问。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
