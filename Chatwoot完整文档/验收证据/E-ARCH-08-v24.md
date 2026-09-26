# E-ARCH-08-v24 ContactRepository 跨所有者生命周期边界

`ContactRepository` 已不再直接查询或更新 CON、MSG、TKT、BIL、OPS、RTM、API 权威表。删除联系人先由 CON 检查活动会话并分离历史会话；活跃联系人列表由 CON 返回 ID。合并时，CON、MSG、TKT、BIL、OPS 分别执行可重复的联系人引用迁移，全部成功后 CUS 才合并本地身份、备注和标签并删除被合并联系人。

RTM 和 API 位于下游消费侧，同步调用会破坏无环拓扑，因此 CUS 合并事务写入 `cus.contact.merged.v1` 本地变更日志，由 RTM/API 消费者幂等迁移 Call 和 WidgetEvent 引用。阶段用例在独立 RTM/API 库执行消费动作。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `736660e`；E-ARCH-08-v23 |
| 阶段分库检查 | 32 个子用例通过 |
| 专项回归 | CUS/CON/MSG/TKT/BIL/OPS/RTM/API 八库联系人合并及引用迁移通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `68ed81f484d226222395428b1d048c82b99a81d2d8f1f62a46fad3c00675d0df`。ARCH-03/04 保持 REOPENED；进程外事件投递和消费者恢复仍属于整体运行验收。下一项为 `LabelRepository` 的 CON `ConversationLabel` 访问边界。ARCH-08 整体仍为 REWORK。
