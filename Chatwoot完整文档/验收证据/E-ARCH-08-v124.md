# E-ARCH-08-v124 CON 状态历史与删除状态查询

| 验收项 | 结果 |
|---|---|
| 状态历史 | `con.status_history.get.v1` 先校验账号内会话，再从 CON 本地事件日志按版本顺序生成历史 |
| 历史最小化 | 只返回事件 ID、类型、聚合版本、发生时间和变更字段，不返回事件 payload |
| 删除状态 | `con.account_deletion_status.get.v1` 统计本账号 Conversation 及其 ConversationLabel、ConversationParticipant 残留 |
| 关系归属 | 无账号字段的标签与参与者关系通过本账号 Conversation 子查询限定范围 |
| 租户隔离 | 跨账号历史按会话不存在处理，其他账号三类数据不进入删除状态统计 |
| 声明进度 | CON 的 4 个 Query 已全部接入独立运行时 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 CON 会话创建命令及创建事件；ARCH-08 保持 REWORK。
