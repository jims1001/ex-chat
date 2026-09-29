# E-ARCH-08-v133 MSG 消息更新、删除与事件

| 验收项 | 结果 |
|---|---|
| 更新命令 | `msg.message.update.v1` 使用 message target 和显式 edit/delete 操作，按 account_id+id 定位 |
| 编辑 | 正文非空且限长，保存 edited_at 并发布 `msg.message.changed.v1`；已删除消息不可编辑 |
| 删除 | 软删除消息、清空正文/翻译/内容属性、删除 MSG 自有附件，并发布 `msg.message.deleted.v1` |
| 账号隔离 | 其他账号消息返回未找到且保持原状 |
| 原子与幂等 | 每次更新、命令收据和对应 outbox 同事务；命令重放不重复修改或发布事件 |
| 事件最小化 | 更新/删除事件只含消息、会话、操作与删除状态，不包含正文和内部属性 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MSG Provider 状态更新、重试与状态事件；ARCH-08 保持 REWORK。
