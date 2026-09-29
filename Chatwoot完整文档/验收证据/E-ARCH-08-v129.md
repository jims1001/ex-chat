# E-ARCH-08-v129 CON 删除事件与完整声明面

| 验收项 | 结果 |
|---|---|
| 删除入口 | 既有 `con.conversation.update.v1` 使用显式 operation=delete，保持冻结合同不新增临时命令 |
| 所有者清理 | 同事务删除 CON 自有 ConversationLabel、ConversationParticipant 与 Conversation；其他账号不受影响 |
| 删除事件 | 会话删除、命令收据与 `con.conversation.deleted.v1` outbox 原子提交，事件只含会话与显示标识 |
| 重放 | 相同删除命令重放返回原收据，只执行一次删除并产生一个事件 |
| 声明面 | CON 独立运行时注册 7 Command、4 Query，并产生合同声明的 4 类 Event；同时消费 CUS 合并事件 |
| 存储隔离 | CON 独立库不创建 Account/Inbox/Contact/User/Team/Message 外部表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 CON PostgreSQL 多实例、事件自动投递、混合版本回退及所有者备份恢复；ARCH-08 保持 REWORK。
