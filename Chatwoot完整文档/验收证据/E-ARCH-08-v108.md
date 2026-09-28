# E-ARCH-08-v108 CUS 独立标签合同

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `cf83f33`；E-ARCH-08-v107 |
| 新增合同 | `cus.label.get.v1`、`cus.label.manage.v1`、`cus.label.changed.v1` |
| 查询边界 | 单项和列表均按 account_id；跨账号单项返回 resource_not_found |
| 写入动作 | create/update 使用显式白名单，不使用含外部关联的 Save |
| 原子幂等 | 标签、command_receipt 和 event_outbox 同事务；重启重放不重复 |
| 结果 | 创建后更新保持 1 条标签、2 条收据、2 条顺序事件 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据关闭 CUS 标签查询和基础管理动作；联系人合并、删除、匿名化及完整运行验收仍待完成，ARCH-08 保持 REWORK。
