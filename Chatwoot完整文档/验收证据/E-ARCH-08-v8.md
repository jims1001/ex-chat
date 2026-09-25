# E-ARCH-08-v8 BIL 订单会话引用分库续跑

## 当前结论

**ARCH-08 仍为 REWORK。** BIL 订单更新在设置会话引用时，改由 CON 所有者查询校验账号与会话存在性，不再读取 BIL 数据库中的 CON 会话表。BIL/CON 分离库用例验证了有效引用可以更新、跨账号引用被拒绝、BIL 库未建立会话表。此端口目前仍为进程内调用，不是独立服务公开接口。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v8 |
| baseline_reference | E-ARCH-08-v1 至 v7；Git `730e36e` |
| environment | 本地 macOS；BIL/CON 分离 SQLite 库 |
| result | 阶段分库 gate 15 个子用例通过；ARCH-08 整体未通过 |
| scope | BIL 订单更新会话引用校验 |

## 本轮验证

- `make test-module-isolation`：15 个阶段分库子用例通过。
- `EX_CHAT_MODULE_ACCEPTANCE=1 go test -race ./test -run '^TestModuleIsolationAcceptance$' -count=1`：通过。
- `make test`、`make test-docker`、`go vet ./cmd/... ./internal/... ./pkg/... ./test/...`：通过。
- `git diff --check`：通过。

其他仓储仍有跨所有者访问；26 模块的独立服务合同、启停、扩缩、升级、恢复、容量、灰度与回退尚未通过验收。ARCH-02/03/04 保持 REOPENED，ARCH-08 保持 REWORK；历史证据保留。
