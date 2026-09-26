# E-ARCH-08-v33 TicketRepository TEN/IAM/CUS/CON/RPT 所有权边界

原 `TicketRepository` 在 TKT 库中直接校验 TEN 成员、查询 IAM 用户、CUS 联系人、CON 会话和 RPT SLA 策略，并通过 GORM 跨所有者预加载工单负责人、创建人、评论作者、联系人和会话。本轮将允许的同步引用和详情组合改为所有者合同。

负责人及观察者成员资格由 TEN 校验；用户身份由 IAM 读取；联系人由 CUS 读取；会话摘要由 CON 读取。RPT 位于更高架构层，TKT 不建立反向同步依赖，`sla_policy_id` 作为 RPT 拥有的外部标识保存，TKT 不查询或复制 SLA 策略表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `4155c9f`；E-ARCH-08-v32 |
| 阶段分库检查 | 41 个子用例通过 |
| 专项回归 | TKT/TEN/IAM/CUS/CON/RPT 六库隔离下，引用校验、详情组合、观察者身份、RPT 外部标识及外部表禁用通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

依赖清单摘要为 `abe9b02705121dcbe0f3a7e2541040d485b6e61cf687e733717e3d33159f1226`，协作合同摘要为 `93b0298edced86759832801a5086083e885fd024d03ba571d28a624c291b89a0`。当前合同仍为进程内所有者适配器；独立服务故障恢复和 SLA 外部引用的异步一致性验收仍属于 ARCH-08 后续工作。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `ConversationRepository` 剩余的 CHN/CUS/IAM/TEN/MSG/MED 跨所有者访问边界。
