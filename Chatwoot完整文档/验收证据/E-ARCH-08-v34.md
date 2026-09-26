# E-ARCH-08-v34 ConversationRepository CHN 收件箱移动边界

`ConversationRepository` 的其他 CUS/IAM/TEN/MSG/MED 访问已在早期分库验收中通过所有者合同完成。本轮定位到唯一剩余的直接跨所有者查询：`MoveInbox` 会从 CON 库查询 CHN `Inbox` 表。

现在源收件箱和目标收件箱的账号归属均由 CHN 所有者仓储校验，CON 只更新自己的会话记录。静态禁止规则同步拦截 `ConversationRepository` 今后直接查询 CHN/CUS/IAM/TEN/MSG 权威模型。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `784a4aa`；E-ARCH-08-v33 |
| 阶段分库检查 | 42 个子用例通过 |
| 专项回归 | CON/CHN 分库下，同账号收件箱移动成功，跨账号目标被拒绝，两库不含对方权威表 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |

依赖源清单和协作合同本轮未改动，协作合同摘要保持 `93b0298edced86759832801a5086083e885fd024d03ba571d28a624c291b89a0`。本项只关闭会话移动的 CHN 查询边界；独立运行、故障恢复、容量、切换和回退验收仍未完成。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `ConversationCleanup` 的 MSG/RPT/QLT/RTM/TKT/API/AIC/OPS/BIL 所有者命令边界。
