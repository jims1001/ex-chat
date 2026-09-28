# E-ARCH-08-v98 CHN 能力连接与删除状态查询

本轮接入 CHN 剩余三个公开查询。查询只访问 CHN 所有者模型，并把 ProviderConfig 转换为有限状态，不返回供应商密钥或原始配置。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `5a3ce11`；E-ARCH-08-v97 |
| 能力查询 | `chn.capabilities.get.v1` 返回通道类型、能力名和是否已授权 |
| 连接查询 | `chn.connection.status.v1` 返回健康状态、配置/Webhook/HMAC 布尔值及检查时间 |
| 删除状态 | `chn.account_deletion_status.get.v1` 返回收件箱、凭据、成员计数和 ready |
| 密钥边界 | 三个响应均不包含 ProviderConfig、access token、WebsiteToken 或 HMAC Token |
| 状态闭环 | 账号撤销后 credential_count/member_count 为 0 且 ready=true；其他账号保持 ready=false |
| 专项测试 | `TestCHNHealthAndAccountRevokeStayWithinOwnerBoundary` 覆盖写入、查询和撤销闭环 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `b158ff036ee27a010bb802888054283e4bb4371af0cf4f0a5eb94aefb3958d8c` |

CHN 合同清单中的命令和查询均已接入；领域事件 outbox、发布投递和完整模块验收仍待完成。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
