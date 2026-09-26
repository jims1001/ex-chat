# E-ARCH-08-v35 ConversationCleanup 多所有者命令边界

原 `ConversationCleanup` 虽然对 MSG/RPT/QLT/RTM/TKT/API/AIC/OPS/BIL 使用了分离数据库，但协调器仍直接包含九个所有者的表模型和删除语句。本轮将各模块清理逻辑拆为所有者命令适配器，协调器只保留命令顺序、错误标记和重试语义。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `5b8d6a2`；E-ARCH-08-v34 |
| 阶段分库检查 | 42 个子用例通过 |
| 所有者命令 | MSG/RPT/QLT/RTM/TKT/API/AIC/OPS/BIL 分别持有自己的数据库及模型删除逻辑 |
| 故障与重放 | 所有者失败不被隐藏；已提交所有者不回滚；CON 持久删除标记可恢复后继续重放 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止协调器出现领域模型或查询语句 |

本轮保留现有删除顺序和数据行为，并未将多库命令误报为原子事务。完整的进程外命令、独立服务停止恢复、容量、切换和回退仍需后续验收。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `CopilotThreadRepository` 的 IAM 用户和 CON 会话预加载边界。
