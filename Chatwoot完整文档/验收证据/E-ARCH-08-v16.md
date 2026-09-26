# E-ARCH-08-v16 RoutingService 所有者查询边界

## 本次子项结论

已关闭 `RoutingService` 自动分配和容量检查对 fallback 用户、收件箱成员及会话计数的直接跨所有者存储访问。

路由服务现在通过 CHN 查询收件箱与成员，通过 TEN 成员合同组合 IAM 用户并校验 fallback 用户，通过 TEN 校验 fallback 团队，通过 CON 查询坐席负载与最近分配，通过 MSG 查询离线消息是否已存在。RTE 数据库只读取分配策略和容量策略。

`CheckAgentCapacity` 增加错误返回；CHN、TEN、IAM、CON 或 RTE 查询失败时，API 和自动分配会明确失败，不再把依赖错误解释成“有容量”或“无候选人”。跨账号 fallback 用户、团队均返回未找到，不能写入 CON。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `349620b`；E-ARCH-08-v15 |
| 验证时间 | 2026-09-26T07:13:00+00:00 |
| 阶段分库竞争检查 | 24 个子用例通过 |
| 专项回归 | 自动分配、容量策略、跨账号拒绝、依赖故障及架构合同检查通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增 RTE/CHN/TEN/IAM/CON/MSG 分库用例覆盖账号隔离的负载计数、容量耗尽后的 fallback 用户、fallback 团队、跨账号用户与团队拒绝，以及 CHN 停止后的显式错误。RTE 数据库未创建 IAM、TEN、CHN 或 CON 权威表。

完整回归首次执行发现业务场景夹具仅创建 `InboxMember`，未建立 TEN `AccountUser`。该状态在所有者边界下不是合法可分配成员；夹具已补齐账号成员关系，随后完整回归和容器回归通过。

## 合同与剩余边界

协作合同登记 RTE 对 CHN 收件箱查询、TEN 成员与团队查询、CON 负载与轮询查询、MSG 内容存在性查询的调用。合同摘要为 `d2cc707723a677e34af8c24f72452f2eb2ad9a93f78f16ad1e6be53436434d93`。ARCH-04 保持 REOPENED。

本次只关闭自动分配和容量检查链路。`RoutingService.ResumeSnoozedConversations` 仍直接访问 CON，会作为下一子项处理。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
