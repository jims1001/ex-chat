# E-ARCH-08-v145 TKT 工单、工作流、历史与删除状态查询

| 验收项 | 结果 |
|---|---|
| 工单查询 | `tkt.ticket.get.v1` 按 account_id+id 读取安全摘要，不返回 custom_attributes |
| 工作流查询 | `tkt.workflow.get.v1` 返回当前状态、版本和允许转换，不加载外部实体 |
| 历史查询 | `tkt.history.list.v1` 只组合 TicketStatusHistory 与 TicketActivity，不返回评论正文 |
| 删除状态 | 统计 Ticket、Comment、Activity、StatusHistory、Attachment、Watcher 六类本账号事实 |
| 账号隔离 | 使用其他 account_id 读取同一工单返回 resource_not_found |
| 独立存储 | 查询运行时不创建 Account、User、Contact、Conversation 外部所有者表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 TKT 工单创建及创建事件；ARCH-08 保持 REWORK。
