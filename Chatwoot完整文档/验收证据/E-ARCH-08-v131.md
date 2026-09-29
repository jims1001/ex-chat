# E-ARCH-08-v131 MSG 消息与投递查询边界

| 验收项 | 结果 |
|---|---|
| 消息详情 | `msg.message.get.v1` 强制 account_id+id，返回 MSG 安全视图，不水合 IAM/CUS sender 或暴露翻译和内容属性 |
| 消息列表 | `msg.messages.list.v1` 强制账号和会话范围，默认排除私密消息；只有显式 include_private 才包含私密记录 |
| 投递状态 | `msg.delivery_status.get.v1` 只返回消息标识、会话标识、状态和删除时间摘要，不返回正文 |
| 邮件投递 | `msg.email_delivery.get.v1` 返回重试与投递状态，不返回收发邮箱、主题、正文、退信原因或内部错误 |
| 删除状态 | `msg.account_deletion_status.get.v1` 分别统计本账号 Message、Attachment、DraftMessage、EmailLog |
| 存储边界 | 五个 Query 只访问 MSG 所有者表，跨账号详情返回未找到 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MSG 消息创建、创建事件及 CON 会话校验/活动事件边界；ARCH-08 保持 REWORK。
