# E-ARCH-08-v176 AUT 规则创建、修改与启停命令

| 验收项 | 结果 |
|---|---|
| 创建 | 账号目标匹配，规则和冻结版本 1 同事务写入 |
| 修改 | 只更新本账号规则；定义变化创建新冻结版本，名称描述变化不增加版本 |
| 启停 | `active` 独立切换，本账号目标检查 |
| 原子与幂等 | 规则事实、命令收据和 `aut.rule.changed.v1` 事件同事务；重放单写 |
| 跨账号 | 外账号规则修改返回不存在，未产生本账号副作用 |

专项测试 `TestAUTRuleCommandsFreezeVersionsAndKeepAccountBoundary` 通过；模块运行时、模块隔离、完整 Go 回归、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。AUT 执行命令与运行级验收未完成，ARCH-08 保持 REWORK。
