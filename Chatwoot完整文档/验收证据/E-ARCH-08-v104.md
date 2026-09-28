# E-ARCH-08-v104 CUS 独立联系人更新

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `6b8b3ee`；E-ARCH-08-v103 |
| 新增合同 | `cus.contact.update.v1`、`cus.contact.changed.v1` |
| 所有权 | account_id 与 contact id 同时约束；跨账号目标返回 resource_not_found |
| 写入边界 | 仅姓名、邮箱、电话、标识、头像和 blocked 可写；PubsubToken 等字段被拒绝 |
| 版本及原子性 | ObjectVersion 从 2 增至 3；联系人、收据和 outbox 同事务 |
| 幂等 | 运行时重建后重放，版本、收据和事件均不重复 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据仅关闭 CUS 联系人更新及变更事件。CUS 其余能力和 ARCH-08 仍为 REWORK。
