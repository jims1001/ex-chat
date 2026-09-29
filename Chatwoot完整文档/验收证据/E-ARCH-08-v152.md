# E-ARCH-08-v152 QLT 质检任务创建边界

| 验收项 | 结果 |
|---|---|
| 评分卡所有权 | 只读取 `account_id + scorecard_id + active` 的 QLT 自有评分卡 |
| 质检员所有权 | 显式质检员或数字操作人均通过 TEN 成员合同校验 |
| 目标所有权 | conversation 经 CON 查询；ticket 经 TKT 查询；跨账号或不存在目标不可创建 |
| 投影可信度 | AgentID 与目标引用取自所有者安全响应，忽略调用方同名输入 |
| 原子与幂等 | QATask、CommandReceipt、`qlt.quality_review.changed.v1` 同事务；重放单写 |
| 数据最小化 | 事件只含任务编号、目标标识和状态，不含评分卡规则或目标正文 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均纳入本批验收。QLT 尚有申诉审核、完整声明面、容量、回退与恢复验收，ARCH-08 保持 REWORK。
