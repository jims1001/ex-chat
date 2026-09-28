# E-ARCH-08-v93 CHN 独立收件箱创建

本轮新增 TEN 安全账号查询和 CHN 收件箱创建合同。CHN 不读取 TEN 表，创建前通过认证 HTTP 合同确认账号；业务写入与命令幂等收据位于同一 CHN 本地事务。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `9fb0f25`；E-ARCH-08-v92 |
| TEN 查询 | `ten.account.get.v1` 返回 ID、名称、语言、状态及时区安全摘要 |
| CHN 命令 | `chn.inbox.create.v1` 校验目标账号、名称及通道后写入 CHN 所有者库 |
| 幂等 | 运行时重启后重放同一命令，只存在 1 条 Inbox 和 1 条已交付命令收据 |
| 依赖故障 | TEN 停机时新命令返回 `dependency_unavailable`，CHN 无业务行和收据残留 |
| 数据边界 | TEN 与 CHN 使用独立数据库和监听；CHN 不创建 TEN Account 表 |
| 专项测试 | `TestCHNInboxCreateValidatesTENAndPersistsIdempotently` 通过 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `8c6678a4676a17c96f5a960718c803bea3bbcf3b0830d0615f65e191665e0162` |

CHN 更新、成员绑定、授权、健康写入以及 ENT 能力依赖尚未接入独立进程。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
