# E-ARCH-08-v120 MED 访问与安全状态查询

| 验收项 | 结果 |
|---|---|
| 授权判定 | `med.access.check.v1` 只按账号、主体、范围和有效期判定访问，不返回授权记录或存储位置 |
| 资产安全门禁 | 只有 ready/clean 资产且存在未撤销、未过期授权时返回 `allowed=true` |
| 安全摘要 | `med.security_status.get.v1` 返回资产状态、最新扫描结论和时间，不返回威胁明细 |
| 删除状态 | `med.account_deletion_status.get.v1` 汇总本账号资产、待处理上传、有效授权和扫描记录残留数 |
| 租户隔离 | 跨账号资产按不存在处理；其他账号授权、会话和扫描记录不进入统计 |
| 数据最小化 | 查询结果不含 StorageKey、TokenHash、Threat 或完整 AccessGrant |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MED 资产删除、账号资产清理和 `med.media.deleted.v1`；ARCH-08 保持 REWORK。
