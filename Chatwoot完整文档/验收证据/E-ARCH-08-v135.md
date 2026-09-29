# E-ARCH-08-v135 MSG 已读位置事实与事件

| 验收项 | 结果 |
|---|---|
| 存储模型 | 不新增临时跨模块表；以 MSG LocalChangeJournal 的 `ReadPosition` 聚合事件流保存持久化已读事实 |
| 引用校验 | message_id 必须属于目标账号与会话，user_id 必须通过 TEN 成员查询 |
| 单调性 | 按 conversation_id:user_id 独立聚合；拒绝游标倒退，相同位置直接返回且不重复发布事件 |
| 原子与幂等 | 命令收据与 `msg.read_position.changed.v1` outbox 同事务，命令重放只产生一个事实事件 |
| 账号隔离 | 消息、会话和用户三个范围同时校验，其他账号记录不可作为游标 |
| 数据最小化 | 事件只含 conversation_id、user_id 和 message_id，不复制消息正文或 IAM 用户实体 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MSG 邮件发送及邮件投递事件；ARCH-08 保持 REWORK。
