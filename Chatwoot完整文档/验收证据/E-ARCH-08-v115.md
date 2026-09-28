# E-ARCH-08-v115 CUS 单联系人所有者删除

`cus.contact.delete.v1` 已接入 CUS 独立运行时。该命令是联系人删除编排的所有者最终化步骤：CON 先校验不存在 open/pending 会话并分离历史会话，随后 CUS 原子删除自有数据并发布删除事件。

| 验收项 | 结果 |
|---|---|
| 账号边界 | 只能删除 command.account_id 所属联系人，跨账号返回 resource_not_found |
| 本地级联 | ContactInbox、ContactLabel、ContactNote 与 Contact 在同一 CUS 事务中删除 |
| 命令幂等 | 同一 idempotency_key 在运行时重建后重放，仍只有一条 command_receipt |
| 事件原子性 | 业务删除、命令收据和一条 `cus.contact.deleted.v1` outbox 同事务提交 |
| 外部所有者 | CUS 库不创建 Conversation、Message、Ticket、Order 或 CampaignDelivery 表 |

专项用例已验证删除、重放、跨账号拒绝、关系清理和事件数量。完整门禁包括模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查。

本证据不把 CON 活跃会话规则改成 CUS 跨库查询；该规则继续由 CON 所有者命令执行。下一项为 CUS 公开声明面完整性复验与事件自动调度接线；ARCH-08 保持 REWORK。
