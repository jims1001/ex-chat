# E-ARCH-08-v17 RoutingService 休眠恢复边界

## 本次子项结论

已关闭 `RoutingService.ResumeSnoozedConversations` 对 CON 会话表的直接访问。到期会话扫描和状态恢复由 `ConversationRepository.ResumeExpiredSnoozed` 在 CON 所有者内完成；RTE 只调用状态变更合同并广播已恢复结果。

CON 使用带账号、状态和到期时间条件的更新。重复调度或竞争执行时，只有实际更新成功的会话会返回给 RTE，因此不会重复广播已恢复结果。未到期休眠会话和非休眠会话保持不变。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `9957a91`；E-ARCH-08-v16 |
| 验证时间 | 2026-09-26T07:22:00+00:00 |
| 阶段分库竞争检查 | 25 个子用例通过 |
| 专项回归 | 到期恢复、未到期保持、重复执行、跨账号批次及 CON 故障通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增 RTE/CON 分库用例覆盖两个账号的到期会话恢复、未来会话和普通会话不变、第二次执行返回空结果、RTE 无 CON 权威表，以及 CON 停止后返回明确错误。

## 合同与剩余边界

协作合同新增 RTE 对 CON `ConversationRepository.ResumeExpiredSnoozed` 的状态变更调用，合同摘要为 `a38504a0bb3e4d7393f566fe1a431bb0c1aea49da988b9eb6d7df0af6d0eea64`。ARCH-04 保持 REOPENED。

本次只关闭休眠恢复链路。下一项为 `AgentCapacityPolicyRepository` 的 TEN 账号成员和 CHN 收件箱预加载、写入与计数边界。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
