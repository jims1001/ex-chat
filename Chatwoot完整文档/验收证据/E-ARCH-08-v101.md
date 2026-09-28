# E-ARCH-08-v101 CHN 多实例容量与升级回退

本轮使用 CHN 真实模板命令验证多实例扩缩时的幂等单写，并验证 v1/v2 混合运行和回退后的事实可解释性。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `584c85b`；E-ARCH-08-v100 |
| PostgreSQL 多实例 | 8 个独立 CHN Runtime 同时提交同一 `chn.template.manage.v1` 命令，8 个请求全部成功 |
| 单写结果 | 仅产生 1 条 InboxMessageTemplate、1 条 command_receipt、1 条 `chn.template.changed.v1` outbox |
| 升级阶段 | release-2 接受 v1/v2 合同，使用 v2 请求写入新模板事实 |
| 回退阶段 | 切回仅接受 v1 的 release-1 后，同命令从持久化收据重放且 v1 查询可读取模板 |
| 版本门禁 | 回退实例拒绝 v2 请求；新事实、收据和事件各保持 1 条 |
| 当前标记 | acceptanceStatus=runtime-capacity-upgrade-verified；postgresMultiInstanceCommandVerified=true；mixedVersionUpgradeRollbackVerified=true |
| Docker 验证 | PostgreSQL/Redis 容器内完整测试通过，包含 CHN 八实例真实竞争；临时容器、网络和卷已清理 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker、`go vet ./...` 及差异检查全部通过 |

本证据关闭 CHN 模块级命令并发和版本升级回退项。CHN 尚未完成真实 Provider、高负载指标和最终提取切换；其他模块仍待逐项实现，因此 ARCH-08 保持 REWORK。
