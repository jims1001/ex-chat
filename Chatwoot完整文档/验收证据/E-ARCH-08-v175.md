# E-ARCH-08-v175 AUT 三类查询与规则版本所有权

| 验收项 | 结果 |
|---|---|
| 规则读取 | `aut.rule.get.v1` 按账号与规则 ID 限制 |
| 冻结版本 | 新增 AUT 所有的 AutomationRuleVersion；按账号、规则 ID 和指定/最新版本读取真实记录 |
| 执行读取 | `aut.execution.get.v1` 按账号与执行 ID 限制，只返回状态摘要 |
| 敏感字段 | 执行 ActionResults 与 Error 不出域 |
| 存储隔离 | AUT 独立库不创建 Account、User、Conversation、Message、Contact 外部所有者表 |
| 所有权索引 | ARCH-02 索引与独立迁移同步纳入 AutomationRuleVersion |

专项测试 `TestAUTQueriesUseOwnedRulesVersionsAndExecutionViews` 与迁移所有权校验通过；模块运行时、模块隔离、完整 Go 回归、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查随本批执行。AUT 命令、事件与运行级验收未完成，ARCH-08 保持 REWORK。
