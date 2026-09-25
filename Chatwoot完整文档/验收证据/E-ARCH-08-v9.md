# E-ARCH-08-v9 SYS 与 ENT 存储及入口分离

## 当前结论

**ARCH-08 仍为 REWORK。** 系统配置读写从 ENT 仓储和处理入口移至 SYS 所有者仓储与入口；账号功能仍由 ENT 维护。SYS/ENT 分离数据库用例验证配置读写与账号功能各自使用所属库，且不创建对方权威表。依赖源码清单登记了 SYS 入口，架构依赖图校验通过。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v9 |
| baseline_reference | E-ARCH-08-v1 至 v8；Git `52ee8d4` |
| environment | 本地 macOS；SYS/ENT 分离 SQLite 库；容器测试环境 |
| result | 阶段分库 gate 16 个子用例通过；ARCH-08 整体未通过 |
| scope | SYS 配置所有权与入口归属 |

## 本轮验证

- `make test-module-isolation`：16 个阶段分库子用例通过。
- `EX_CHAT_MODULE_ACCEPTANCE=1 go test -race ./test -run '^TestModuleIsolationAcceptance$' -count=1`：通过。
- `make test`、`make test-docker`、`go vet ./cmd/... ./internal/... ./pkg/... ./test/...`：通过。
- 架构依赖图测试通过，`git diff --check` 通过。

剩余仓储跨所有者访问、公开服务合同及 26 模块启停、扩缩、升级、恢复、容量、灰度和回退验收尚未完成。ARCH-02/03/04 保持 REOPENED，ARCH-08 保持 REWORK；历史证据保留。
