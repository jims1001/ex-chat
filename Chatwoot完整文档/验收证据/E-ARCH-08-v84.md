# E-ARCH-08-v84 独立模块运行基础单元

本轮为全部 26 个逻辑模块增加统一的独立运行入口。每个实例只读取 `MODULE_*` 配置，使用独立监听地址和数据库凭据，并在就绪检查中验证数据库连通性及本模块全部权威表。该基础单元尚未承载业务公开合同，因此不把模块标记为独立业务运行完成。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `e7f4cfc`；E-ARCH-08-v83 |
| 独立入口 | `cmd/module-server`；`MODULE_ID` 选择 IAM 至 AUD 共 26 个模块 |
| 配置隔离 | 仅接受 `MODULE_ADDR`、`MODULE_DB_DRIVER`、`MODULE_DB_PATH`/`MODULE_DB_DSN`、`MODULE_CONTRACT_VERSION`；不回退共享 `DB_PATH`/`DB_DSN` |
| 健康检查 | `/health` 返回模块、基础单元和合同版本；`/ready` 校验数据库及本模块权威表，缺表返回 503 |
| 专项测试 | 26 个模块逐一使用独立 SQLite 库迁移并通过健康/就绪检查；真实 TCP 监听启动与关闭通过；共享配置回退和缺表拒绝通过 |
| 阶段分库检查 | 84 个子用例全部通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 在 PostgreSQL/Redis 环境通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `e24336f474b75f94f15006c16fb8cb578702893697e0de88b8af0ca445b793f1` |

`architecture/module_runtime.json` 已将 26 个模块的 `runtimeShellImplemented` 标记为真，同时继续保留 `independentRuntimeImplemented=false` 和 `acceptanceStatus=pending`。下一项接入独立运行时的公开合同传输、认证、版本、超时和错误语义，随后才能逐模块迁移业务处理器。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
