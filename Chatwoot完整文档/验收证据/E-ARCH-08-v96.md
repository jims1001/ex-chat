# E-ARCH-08-v96 CHN 独立通道授权

本轮实现 ENT 最小权益查询和 CHN 通道授权写入。供应商密钥只写入 CHN 所有者数据库，合同响应和持久化幂等收据只记录授权结果。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `8c3ece8`；E-ARCH-08-v95 |
| ENT 查询 | `ent.capability.check.v1` 返回账号、能力名和允许结果 |
| CHN 命令 | `chn.channel.authorize.v1` 校验权益后保存 ProviderConfig、WebhookURL 和 HMAC 要求 |
| 密钥边界 | 供应商 access token 不出现在 HTTP 响应或命令收据中 |
| 原子幂等 | 配置与收据同事务，运行时重启重放不重复更新 |
| 故障 | 权益未开通返回 `feature_unavailable`/503；ENT 停机返回依赖不可用，均无失败收据 |
| 错误语义 | `feature_unavailable` 明确映射 HTTP 503 |
| 专项测试 | `TestCHNChannelAuthorizeUsesENTAndKeepsSecretsLocal` 通过 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `eb89011cc5c46b60a3a344aff1103a0e67bdfea94073b63a4ffb7b0ad9d70532` |

CHN 健康更新和账号通道撤销仍待接入；其他模块业务能力尚未全部提取。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
