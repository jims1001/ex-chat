# E-ARCH-08-v150 QLT CSAT、质检任务与评分查询边界

| 验收项 | 结果 |
|---|---|
| CSAT 查询 | 支持按调查 ID 或会话 ID 查询，强制 `account_id` 范围，不返回内部 ReviewNotes |
| 质检任务查询 | 按 `account_id + task_id` 读取 QLT 自有任务，不预加载 CON/TKT/IAM 关联 |
| 评分明细查询 | 先验证账号内 QATask，再按 TaskID 读取无 AccountID 的评分明细，跨账号不可达 |
| 独立数据边界 | QLT 库不创建 Conversation、Ticket、User、Inbox、Contact 外部所有者表 |
| 失败语义 | 缺少查询键返回 validation_failed；跨账号对象统一返回 resource_not_found |

专项测试、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均纳入本批验收。QLT 尚有命令、事件、容量、回退和恢复验收，ARCH-08 保持 REWORK；下一项为调查创建与 CSAT 提交。
