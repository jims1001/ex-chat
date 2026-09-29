# E-ARCH-08-v153 QLT 申诉审核与完整声明面

| 验收项 | 结果 |
|---|---|
| 账号所有权 | QAAppeal 与 QATask 均按 account_id 到达，跨账号申诉返回不存在 |
| 审核人资格 | 数字 Actor 经 TEN 成员合同校验；原质检员被回避规则拒绝 |
| 状态机 | 支持 adjusted/upheld/rejected/need_evidence/under_review；终态拒绝再次裁决 |
| 分数同步 | adjusted 限制 0–100，并按致命错误和 95/85 阈值同步任务结果与状态 |
| 原子与幂等 | 申诉、任务、活动、收据、`qlt.appeal.changed.v1` 同事务；重放单写 |
| 声明面 | 4 Command、3 Query、3 Event 均有明确实现或生产路径 |

专项、模块运行时、隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均纳入本批验收。QLT 尚需多实例、版本回退、事件投递和备份恢复，ARCH-08 保持 REWORK。
