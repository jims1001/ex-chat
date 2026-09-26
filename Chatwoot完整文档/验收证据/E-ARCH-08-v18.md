# E-ARCH-08-v18 RTE 容量策略 TEN/CHN 边界

## 本次子项结论

已关闭 `AgentCapacityPolicyRepository` 对 TEN 账号成员、IAM 用户和 CHN 收件箱的直接读取、预加载、计数及写入。RTE 仓储现在只保存容量策略、旧版容量规则和每收件箱限制。

策略详情及列表由 handler 组合：RTE 返回策略和限制，TEN 返回成员计数及成员关系并通过 IAM 补齐用户，CHN 返回账号范围内的收件箱摘要。成员添加、移除和策略删除时的引用清理由 TEN 执行；收件箱限制创建和更新前由 CHN 校验账号归属。

RTE 的 Create/Update 显式排除 `AccountUsers` 和 `InboxCapacityLimits` 关联，避免 GORM 隐式写入外部聚合。跨账号成员和收件箱均在 RTE 写入前拒绝。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `834d35a`；E-ARCH-08-v17 |
| 验证时间 | 2026-09-26T07:30:00+00:00 |
| 阶段分库竞争检查 | 26 个子用例通过 |
| 专项回归 | 策略组合、成员生命周期、收件箱限制、跨账号拒绝、删除清理及 CHN 故障通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增 RTE/TEN/IAM/CHN 分库用例覆盖详情组合、IAM 用户补齐、TEN 成员计数和分配、CHN 收件箱补齐、跨账号用户和收件箱拒绝、限制更新、策略删除时 TEN 引用清理，以及 CHN 停止后的显式错误。RTE 数据库未创建 `AccountUser`、`User` 或 `Inbox` 权威表。

## 合同与剩余边界

协作合同登记 RTE 对 TEN 成员查询与写入、CHN 收件箱查询的调用。合同摘要为 `5a5002d13cf16ae73eaf7d1bd3afefaf7ca26b963808714ade4f848897d13874`。ARCH-04 保持 REOPENED。

本次只关闭容量策略链路。静态候选审计仍列出其他跨所有者访问，下一项为 RPT `AppliedSLARepository` 的 CON 会话访问边界。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
