# E-ARCH-08-v99 CHN 领域事件 outbox

本轮将 CHN 独立写命令接入所有者本地事件 outbox。业务数据、幂等收据和事件在同一数据库事务提交，事件载荷只包含资源 ID、操作和安全状态。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `a040c04`；E-ARCH-08-v98 |
| 事件 | `chn.inbox.changed.v1`、`chn.channel_health.changed.v1` |
| 写入覆盖 | 创建、更新、成员绑定/解绑、通道授权、健康更新、账号通道撤销 |
| 原子性 | 业务变更、命令收据及 event_outbox 位于同一 CHN 事务 |
| 排序 | 同一聚合从本地日志最大版本递增，事件带 aggregate_version |
| 密钥边界 | 授权事件只含 authorized=true，不包含 ProviderConfig 或 access token |
| 投递 | 两个 CHN 事件经认证 HTTP 投递到独立 RPT 数据库，消费 inbox 去重，源记录变为 delivered |
| 幂等 | 命令重放不追加重复事件，第二次投递扫描为 0 |
| 专项测试 | CHN 六组专项均校验 outbox 数量；健康/撤销测试覆盖跨进程投递 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `5e9c804a3c5d37ee4cc057c6b400b0872442c97efb550bbb6f7731c2019256de` |

CHN 模板变更事件和完整模块闭环审计尚未完成；其他模块业务能力仍待逐项提取。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
