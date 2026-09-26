# E-ARCH-08-v19 RPT Applied SLA 的 CON 会话边界

## 本次子项结论

已关闭 `AppliedSLARepository` 对 CON 会话及关系表的直接预加载、联表筛选和更新。RPT 仓储现在只查询和修改 `AppliedSLA`、`SLAEvent`、`SLAPolicy` 等 RPT 权威数据。

收件箱、团队、负责人和标签筛选先由 `ConversationRepository.FindIDsForAppliedSLAFilter` 在 CON 所有者边界解析为会话 ID，RPT 再按 ID 查询 Applied SLA。列表和导出通过 `ConversationRepository.FindByID` 组合会话摘要；解除 SLA 时由 `ConversationRepository.ResetSLAProjection` 重置 CON 投影，RPT 仓储只删除本地聚合。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `49100af`；E-ARCH-08-v18 |
| 验证时间 | 2026-09-26T07:50:00+00:00 |
| 阶段分库检查 | 27 个子用例通过 |
| 专项回归 | RPT/CON/CUS/CHN 分库组合、标签与收件箱筛选、指标、导出、删除重置、跨账号隔离及 CON 故障通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增分库用例确认 RPT 数据库未创建 `Conversation`、`ConversationLabel` 或 `Label` 权威表。账号 1 的筛选和删除不会读取或修改账号 2 的 Applied SLA；CON 连接停止后，带会话筛选的 RPT 请求返回显式错误，不会退化为未筛选结果。

## 合同与剩余边界

协作合同新增 RPT 对 CON 的 `FindByID`、`FindIDsForAppliedSLAFilter` 和 `ResetSLAProjection` 调用，合同摘要为 `a2fb93e94932f2c37d4bf292b76f0bd255c2f07abad6700238ddd455e094b56b`。ARCH-04 保持 REOPENED。

本次只关闭 Applied SLA 的会话访问链路。静态候选审计仍列出其他跨所有者访问，下一项为 AIC `CaptainRepository` 的 CHN 收件箱及 CON/MSG 会话消息访问边界。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
