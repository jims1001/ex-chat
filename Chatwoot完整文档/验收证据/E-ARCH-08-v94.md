# E-ARCH-08-v94 CHN 独立收件箱更新

本轮实现 `chn.inbox.update.v1`。命令只访问 CHN 所有者数据，按账号和收件箱 ID 定位目标，并用显式字段白名单防止通用更新覆盖连接密钥。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `6584cfb`；E-ARCH-08-v93 |
| 允许字段 | 名称、头像、欢迎语、工作时间、离线消息、时区及 CSAT 开关 |
| 禁止字段 | WebsiteToken、HMAC Token、ProviderConfig 及其他未声明字段 |
| 账号隔离 | 其他账号对同一收件箱 ID 更新返回未找到 |
| 原子幂等 | 更新和命令收据同事务，重启重放不重复执行；失败命令不保留收据 |
| 专项测试 | `TestCHNInboxUpdateBoundsFieldsAccountAndReplay` 通过 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `d37d2951ff9bac274dc16ed3fb70e08abbfcf2dc07e7b1f88ba805d7d53f42c5` |

CHN 成员绑定、授权和健康更新合同仍待接入。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
