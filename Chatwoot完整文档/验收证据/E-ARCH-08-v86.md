# E-ARCH-08-v86 IAM/TEN 独立业务查询与依赖恢复

本轮将首组真实业务查询接入独立模块运行时：IAM 提供身份查询，TEN 提供成员列表，并通过 IAM HTTP 合同组合身份资料。两个模块使用各自数据库；TEN 不再需要 IAM 的表或进程内仓储实例。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `452de01`；E-ARCH-08-v85 |
| IAM 能力 | `iam.identity.get.v1` 从 IAM 独立库按 ID 读取用户并返回无密码、令牌及确认密钥的最小身份视图 |
| TEN 能力 | `ten.members.list.v1` 只从 TEN 独立库读取 AccountUser/CustomRole，再逐项调用 IAM 身份合同组合结果 |
| 依赖配置 | `MODULE_ENABLE_BUSINESS_CONTRACTS=true` 显式启用；`MODULE_DEPENDENCIES_JSON` 提供依赖地址和专用凭据 |
| 停止与恢复 | IAM 监听停止后 TEN 返回 `dependency_unavailable`/503；IAM 在同一地址恢复后 TEN 再次返回完整成员视图 |
| 超时 | IAM 慢响应受 TEN 的 `MODULE_MAX_TIMEOUT_MS` 限制，请求在上限内结束，不无限等待 |
| 数据隔离 | IAM 和 TEN 分别执行所属模块迁移；TEN 成员仓储新增不预加载 IAM User 的所有者查询 |
| 阶段分库检查 | 84 个子用例全部通过 |
| 完整及容器回归 | `make test`、`make test-docker` 通过，Docker 临时资源已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `a886ec1f6a9009093131e0446fed3061730193c390a9621120140799db781dba` |

这里只迁移了 IAM/TEN 的一条真实查询链路，两个模块在 `module_contracts.json` 中登记的其余 Command/Query/Event 尚未全部接入，因此仍保持 `independentRuntimeImplemented=false`。下一项补齐跨进程 Command 的持久化幂等收据，再迁移 TEN 成员写入命令。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
