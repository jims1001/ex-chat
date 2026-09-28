# E-ARCH-08-v106 CUS 独立公司查询

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `0585e76`；E-ARCH-08-v105 |
| 新增合同 | `cus.company.get.v1` |
| 所有权 | account_id 与 company id 同时约束；联系人计数仅查询 CUS Contact |
| 安全摘要 | 返回公司标准资料和联系人计数，排除 CustomAttributes 与 AdditionalAttributes |
| 跨账号 | 返回 resource_not_found |
| 当前标记 | acceptanceStatus=partial-company-query；独立查询 3 条 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据仅关闭 CUS 公司单项查询。CUS 其余能力和 ARCH-08 仍为 REWORK。
