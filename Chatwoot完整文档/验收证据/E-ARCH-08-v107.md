# E-ARCH-08-v107 CUS 独立公司写入

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `c3444ee`；E-ARCH-08-v106 |
| 新增能力 | `cus.company.update.v1` create/update；`cus.company.changed.v1` |
| 写入边界 | 显式字段白名单；自定义和附加属性必须是 JSON object；不使用关联 Save |
| 所有权 | 创建目标必须是本账号；更新同时约束 account_id 与 company id |
| 原子幂等 | 公司、command_receipt 和 event_outbox 同事务；重启重放不重复 |
| 安全响应 | 公司安全视图不返回自定义与附加属性内容 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据关闭 CUS 公司创建/资料更新基础动作。关联、删除等动作以及 CUS 其余能力仍待验收；ARCH-08 保持 REWORK。
