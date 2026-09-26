# E-ARCH-08-v29 PlatformRepository TEN/IAM 所有权边界

原 `PlatformRepository` 属于 EXT，却直接创建、查询、更新和删除 TEN 账号及成员，也直接查询、更新和删除 IAM 用户，并通过 `Preload("User")`、`Preload("Account")` 隐式访问外部所有者表。本轮保留平台 API 合同，由 EXT 平台仓储通过 TEN `AccountRepository` 和 IAM `UserRepository` 完成编排。

账号生命周期、成员关系、账号分页和成员分页由 TEN 执行；用户查询、分页、更新和删除由 IAM 执行；成员用户资料由 TEN 通过 IAM 端口组合。删除用户时由 EXT 先清理 TEN 成员关系，再调用 IAM 删除用户。EXT 仍只保存 PlatformApp 和 AgentBot 等自身数据。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `9b61090`；E-ARCH-08-v28 |
| 阶段分库检查 | 37 个子用例通过 |
| 专项回归 | EXT/TEN/IAM 三库隔离下，账号、用户、成员组合、成员清理和外部表禁用通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `0d1fbd070221cd7998ef12bf6b33fde04dabca8b19a8c569dcdf5fe337a9089f`。删除用户是跨 TEN/IAM 的进程内编排，独立服务下的失败恢复和补偿仍属于 ARCH-08 后续验收。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `PublicRepository` 的 CHN/CUS/CON/MSG 跨所有者访问边界。
