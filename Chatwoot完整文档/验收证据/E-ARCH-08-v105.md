# E-ARCH-08-v105 CUS 联系人身份解析

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `7a70994`；E-ARCH-08-v104 |
| 新增合同 | `cus.identity.resolve.v1` |
| 匹配语义 | 在 account_id 内按邮箱/标识组合、邮箱、标识、电话、姓名顺序匹配 |
| 安全边界 | 空条件 validation_failed；跨账号 resource_not_found；响应不含 PubsubToken |
| 数据边界 | 仅访问 CUS Contact 所有者表，不创建或查询外部模块表 |
| 当前标记 | acceptanceStatus=partial-identity-resolve；独立查询 2 条 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据仅关闭 CUS 身份解析合同。CUS 其余能力和 ARCH-08 仍为 REWORK。
