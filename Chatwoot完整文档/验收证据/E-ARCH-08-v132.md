# E-ARCH-08-v132 MSG 消息创建与 CON 活动投影

| 验收项 | 结果 |
|---|---|
| 会话引用 | 创建前通过 CON `con.conversation.get.v1` 验证账号、会话存在且不处于 deleting |
| 发送者 | incoming Contact 必须等于会话 contact_id；outgoing User 必须通过 TEN 账号成员校验 |
| 输入边界 | target 必须与 conversation_id 相同；正文非空且限长；incoming 不允许 private |
| 双层幂等 | 命令 idempotency_key 重放单写；不同命令使用同一 echo_id 也只保留一条消息和一个创建事件 |
| 原子性 | Message、命令收据与 `msg.message.created.v1` outbox 同事务提交；依赖停止不产生半写 |
| 事件最小化 | 创建事件不包含正文、echo_id 或发送者实体，只发布活动所需标识与类型 |
| CON 投影 | CON 消费创建事件后更新 last_activity_at/object_version，incoming 只增加一次 unread_count；重复事件去重 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MSG 消息更新、删除和变更/删除事件；ARCH-08 保持 REWORK。
