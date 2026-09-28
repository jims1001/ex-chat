# E-ARCH-08-v103 CUS 独立联系人创建

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `6c5455f`；E-ARCH-08-v102 |
| 新增合同 | `cus.contact.create.v1`、`cus.contact.created.v1` |
| 输入边界 | 只接受姓名、邮箱、电话、标识和头像；忽略 PubsubToken、自定义属性等非白名单输入 |
| 原子性 | 联系人、command_receipt 和 event_outbox 在同一事务提交 |
| 幂等 | 运行时重建后重放同一命令，联系人、收据和事件均保持 1 条 |
| 失败边界 | 空身份输入返回 validation_failed，不保留收据或业务事实 |
| 当前标记 | acceptanceStatus=partial-contact-create；outboxAtomicWithCommands=true |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查通过 |

本证据仅关闭 CUS 联系人创建及创建事件。CUS 其余能力和 ARCH-08 仍为 REWORK。
