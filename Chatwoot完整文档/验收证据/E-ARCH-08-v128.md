# E-ARCH-08-v128 CON 参与者管理与账号会话匿名化

| 验收项 | 结果 |
|---|---|
| 参与者命令 | `con.participant.manage.v1` 支持 add/remove/sync，所有非零 user_id 先通过 TEN 成员校验 |
| 参与者所有权 | ConversationParticipant 只在账号归属会话内增删，跨账号会话不可定位 |
| 匿名化命令 | `con.account_conversations.anonymize.v1` 仅处理目标账号，清除联系人、分配、自定义属性、已读时间及参与者/标签关系 |
| 保留边界 | 会话业务记录、状态、时间线与其他账号数据保留；不访问或删除 CUS/TEN/IAM 数据 |
| 原子与幂等 | 关系变更、对象版本、收据和最小变更事件同事务；重放不重复写入或再次递增版本 |
| 声明面 | CON 的 7 个 Command 与 4 个 Query 均已有独立运行处理器 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 CON 删除事件、完整声明面、事件工作器及运行验收；ARCH-08 保持 REWORK。
