# E-ARCH-08-v110 CUS 联系人合并事件生产端

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `fd126f1`；E-ARCH-08-v109 |
| 新增合同 | `cus.contact.merge.v1`、`cus.contact.merged.v1` |
| 本地合并 | 合并 Contact 字段、自定义属性、ContactInbox、ContactNote、ContactLabel，重复关联去重并删除 mergee |
| 版本 | base ObjectVersion 从 2 增至 3 |
| 模块边界 | CUS 独立库不存在 Conversation、Message、Ticket、Order、Campaign 表 |
| 投影通知 | outbox 只包含 base_contact_id 与 merged_contact_id，不包含 PII；后续由各所有者消费 |
| 原子幂等 | 本地事实、审计、command_receipt 和 event_outbox 同事务；重启重放不重复 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据关闭 CUS 合并生产端。CON/MSG/TKT/BIL/OPS 消费投影尚待逐项完成，CUS 单联系人删除也未完成，因此 ARCH-08 保持 REWORK。
