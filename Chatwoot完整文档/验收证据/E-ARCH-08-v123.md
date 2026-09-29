# E-ARCH-08-v123 CON 会话详情与列表查询

| 验收项 | 结果 |
|---|---|
| 会话详情 | `con.conversation.get.v1` 按账号和会话 ID 读取 CON 权威事实 |
| 会话列表 | `con.conversations.list.v1` 支持账号内状态、收件箱、联系人、坐席和团队标识筛选，固定分页上限 50 |
| 标签关系 | `ConversationLabel` 由 CON 所有者库读取并仅返回 label_id，不读取 CUS Label 实体 |
| 数据最小化 | 结果不返回 CustomAttributes、SLAPolicy 外部对象或 IAM/CHN/CUS 关联实体 |
| 租户隔离 | 跨账号详情按不存在处理，列表不混入其他账号会话 |
| 存储隔离 | CON 运行库没有 Account、Inbox、Contact、User、Team、Label 或 Message 表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 CON 状态历史和账号删除状态查询；ARCH-08 保持 REWORK。
