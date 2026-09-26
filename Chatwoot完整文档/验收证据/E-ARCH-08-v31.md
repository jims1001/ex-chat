# E-ARCH-08-v31 QARepository TKT/CON 所有权边界

原 `QARepository` 属于 QLT，却在生成抽样任务时直接查询 TKT 工单和 CON 会话表。本轮保留质检抽样入口，由 QLT 只管理抽样规则、评分表和质检任务，候选对象分别通过 TKT 与 CON 所有者查询取得。

TKT 负责筛选已解决或已关闭的最新工单，并返回质检所需的最小候选摘要；CON 负责返回最新会话候选摘要。QLT 根据摘要创建任务，不再保存或查询工单、会话权威表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `42a6ec0`；E-ARCH-08-v30 |
| 阶段分库检查 | 39 个子用例通过 |
| 专项回归 | QLT/TKT/CON 三库隔离下，工单和会话抽样、任务归属及外部表禁用通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `fc8f2af789272aaa0fc1d1cf2035fefda049802f10e160c389beecf01bb8b829`。当前合同仍为进程内所有者适配器，独立服务故障恢复和完整抽样调度运行验收仍属于 ARCH-08 后续工作。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `WidgetRepository` 的 CHN/CUS/CON/OPS/MSG 跨所有者访问边界。
