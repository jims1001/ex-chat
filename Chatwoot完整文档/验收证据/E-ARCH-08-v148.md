# E-ARCH-08-v148 TKT 评论与账号匿名化

| 验收项 | 结果 |
|---|---|
| 评论边界 | `tkt.comment.add.v1` 校验 TEN 成员和本账号工单，只写 TicketComment/TicketActivity |
| 评论隐私 | 命令响应和 `tkt.ticket.commented.v1` 仅含 comment_id、ticket_id、is_private，不含正文 |
| 评论幂等 | 重放只生成一条评论、一条活动和一个事件 |
| 匿名化 | 清除 Ticket、Comment、Activity、StatusHistory、Attachment、Watcher 中的人员引用和敏感内容 |
| 时间线保留 | 工单、评论、活动、状态历史和附件事实保留；Watcher 身份关系删除 |
| readiness | 删除状态保留事实计数，并以 pii_count=0 判断 ready=true；其他账号不受影响 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 TKT 完整声明面及独立运行验收；ARCH-08 保持 REWORK。
