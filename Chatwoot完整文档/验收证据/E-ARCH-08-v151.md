# E-ARCH-08-v151 QLT 调查创建与 CSAT 提交

| 验收项 | 结果 |
|---|---|
| 会话所有权 | 两条命令均通过 CON `con.conversation.get.v1` 校验账号内会话，不读取 Conversation 表 |
| 调查唯一性 | CSATSurvey 以 account_id+conversation_id 联合唯一；重复创建返回同一调查 |
| 提交校验 | rating 只接受 1–5，feedback_text 去空白且最长 4000 字符 |
| 原子性 | CSAT 事实、CommandReceipt 与 `qlt.csat.received.v1` outbox 同事务 |
| 幂等性 | 相同命令重放仅保留一条调查和一个接收事件；跨账号会话返回不存在 |
| 数据最小化 | 事件仅含调查、会话、评分和审核状态，不含反馈正文 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均纳入本批验收。QLT 尚有质检、申诉、完整事件面、容量、回退和恢复验收，ARCH-08 保持 REWORK。
