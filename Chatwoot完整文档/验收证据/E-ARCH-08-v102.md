# E-ARCH-08-v102 CUS 独立联系人查询

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `6ff8e01`；E-ARCH-08-v101 |
| 新增运行合同 | `cus.contact.get.v1` |
| 账号边界 | 查询同时约束 account_id 与 contact id；跨账号返回 resource_not_found |
| 安全摘要 | 返回联系人业务字段，不返回 PubsubToken 和 CustomAttributes |
| 数据隔离 | CUS 独立库不存在 Inbox、Conversation、Message 外部表 |
| 当前标记 | independentRuntimeImplemented=true；acceptanceStatus=partial-contact-query |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查通过 |

本证据仅关闭 CUS 联系人查询合同，不代表 CUS 或 ARCH-08 完成。下一项为联系人创建命令和 `cus.contact.created.v1` 事件；ARCH-08 保持 REWORK。
