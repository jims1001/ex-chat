# E-ARCH-08-v146 TKT 工单创建与创建事件

| 验收项 | 结果 |
|---|---|
| 身份边界 | 创建人与可选 assignee_id 均通过 TEN 成员合同验证 |
| 联系人边界 | 可选 contact_id 通过 CUS 联系人查询验证 |
| 会话边界 | 可选 conversation_id 通过 CON 会话查询验证，并校验会话联系人一致 |
| 所有者写入 | 只在 TKT 库保存 Ticket、TicketActivity 与 TicketStatusHistory |
| 原子性 | 工单、创建活动、初始状态历史、命令收据和创建事件同事务提交 |
| 幂等与隐私 | 重放只产生一份事实；创建事件不包含 description 或 custom_attributes |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 TKT 状态变更与重新打开命令；ARCH-08 保持 REWORK。
