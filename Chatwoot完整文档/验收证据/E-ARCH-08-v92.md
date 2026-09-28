# E-ARCH-08-v92 CHN 独立收件箱查询

本轮把首个 CHN 业务查询接入独立模块运行时。`chn.inbox.get.v1` 只从 CHN 所有者数据库读取账号范围内的收件箱，并返回去除连接密钥的公开摘要。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `ed8b066`；E-ARCH-08-v91 |
| 独立合同 | `chn.inbox.get.v1` |
| 账号隔离 | 相同 ID 使用其他 `account_id` 查询返回 `resource_not_found` |
| 数据最小化 | 不返回 `website_token`、`hmac_token`、`provider_config` |
| 物理隔离 | CHN SQLite 仅迁移 CHN 模型及本地日志，不存在 TEN Account、IAM User、CON Conversation 表 |
| 专项测试 | `TestCHNInboxQueryRunsFromOwnerDatabaseOnly` 通过 |
| 阶段分库、完整、Docker 及静态检查 | 84 个隔离子用例、`make test`、`make test-docker`、`go vet ./...`、`git diff --check` 全部通过；容器资源已清理 |
| 运行清单摘要 | `4f8f5690f677c9bda1b5b05339c2d769375fc601b17d1975d51b2c96bfff5903` |

CHN 创建、更新、成员绑定、授权及健康写入合同仍未接入独立进程；其他模块也未完成全部能力提取。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
