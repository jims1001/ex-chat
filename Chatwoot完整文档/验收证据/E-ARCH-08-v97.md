# E-ARCH-08-v97 CHN 健康更新与账号通道撤销

本轮完成 CHN 健康观测和账号级通道撤销。健康观测与供应商密钥共同保存在 CHN 所有者库但互不覆盖；撤销清除通道凭据和成员访问，同时保留收件箱记录供历史引用。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `3385acb`；E-ARCH-08-v96 |
| 健康命令 | `chn.health.update.v1` 校验账号/收件箱、状态枚举和 RFC3339 时间 |
| 密钥保持 | 更新 `_health` 保留区后原 ProviderConfig 密钥仍存在，响应和收据不含密钥 |
| 撤销命令 | `chn.account_channels.revoke.v1` 清除 ProviderConfig、Webhook、HMAC 和成员绑定 |
| WebsiteToken | 唯一索引下为每个收件箱生成独立不可预测 `revoked-` 占位，旧公开令牌立即失效 |
| 账号隔离 | 账号 91 撤销不修改账号 92 的凭据及成员 |
| 原子幂等 | 两条命令均与收据同事务；重启重放返回原撤销数量，不重复执行 |
| 专项测试 | `TestCHNHealthAndAccountRevokeStayWithinOwnerBoundary` 通过 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `26cfb73677b29903e09d6dfa77bd2fecbb6b1fdf7ae8e8f1476f725ca8832e47` |

CHN `capabilities.get`、`connection.status` 和账号删除状态查询仍待接入；其他模块也未完成全能力提取。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
