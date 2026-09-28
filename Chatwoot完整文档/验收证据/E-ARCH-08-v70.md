# E-ARCH-08-v70 AdvancedHandler 自动化规则 AUT 所有者边界

收尾检查发现 `AdvancedHandler` 的 companion 文件 `automation_rule_handler.go` 仍由 ORC 直接读写 AUT 的 `AutomationRule` 和 `AutomationRuleExecution`。本轮把规则列表、创建、详情、更新、删除、克隆及执行历史查询全部移入 `AutomationService`；更新时禁止修改 `id` 和 `account_id`，避免规则跨租户迁移。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `c611c09`；E-ARCH-08-v69 |
| 阶段分库检查 | 73 个子用例通过 |
| 专项回归 | AUT 独立库下规则增查改删、执行历史筛选和账号隔离通过；更新无法改写账号；AUT 库无 CON Conversation 或 CUS Contact 权威表 |
| 依赖清单 | 新增 `automation_rule_handler.go` 的 ORC→AUT AutomationService 源码边；依赖摘要 `04d1c001ff9831f80683220f6f3587163187171a05087e99e6ef92466313d253` |
| 模块合同 | 新增 `aut.rule.manage.v1` 命令及 `aut.rule.changed.v1` 事件；模块合同摘要 `276292d05d5b55e3244f3ee23891fcba00b240e2e450267f49c4d98c80e3b277` |
| 协作合同 | 复用 AutomationService 的 AUT 组件绑定；协作摘要保持 `195f278a52063a3ec726f75f38c12ddadfc9e9476abfe2014620d9ad57f4988d` |
| 完整回归 | `make test` 通过；73 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；`automation_rule_handler.go` 已无直接数据库访问 |

自动化规则与执行记录的 AUT 边界已经完成。数据库句柄暂时保留，因为会话参与者和自定义筛选器 companion handler 仍在使用；下一项为 `conversation_participant_handler.go` 的 CON `ConversationParticipant` 访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
