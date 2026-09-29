# E-ARCH-08-v126 CON 会话更新、分配变更与变更事件

| 验收项 | 结果 |
|---|---|
| 通用更新 | `con.conversation.update.v1` 只允许显式修改 muted 或移动至经 CHN `chn.inbox.get.v1` 验证的同账号收件箱 |
| 分配边界 | `con.assignment.change.v1` 通过 TEN `ten.permissions.check.v1` 校验坐席成员，通过 TEN `ten.permissions.check.v1` 的 team_id 判定校验团队归属 |
| 账号隔离 | 两个命令都以 account_id 与 conversation_id 联合定位；其他账号的会话返回未找到 |
| 清空语义 | assignee_id/team_id 为 null 或 0 时只清空 CON 自有外键；非零值必须先由 TEN 验证 |
| 原子性 | Conversation 版本更新、命令收据与 `con.conversation.changed.v1` outbox 在同一事务提交 |
| 幂等 | 更新和分配命令各重放两次，最终各只有一条收据、一个事件，object_version 只各递增一次 |
| 数据最小化 | 事件只包含会话标识、对象版本和 changed_fields，不复制用户、团队或收件箱实体 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 CON 状态与优先级变更命令及状态事件；ARCH-08 保持 REWORK。
