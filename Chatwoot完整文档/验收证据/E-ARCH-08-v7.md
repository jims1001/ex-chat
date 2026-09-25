# E-ARCH-08-v7 标签关联与附件查询分库续跑

## 当前结论

**ARCH-08 仍为 REWORK。** CON 仓储的标签关联写入不再直接检查 CUS 表，而是调用 CUS 标签存在性查询；解除关联只修改 CON 自有关系表。会话附件列表改为调用 MSG 所有者查询。CON/CUS/MSG 分离数据库的用例验证了正确返回、跨账号标签拒绝，以及 CON 库未创建 Label 或 Attachment 表。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v7 |
| baseline_reference | E-ARCH-08-v1 至 v6；Git `aab75a9` |
| environment | 本地 macOS；CON/CUS/MSG 分离 SQLite 库 |
| validation window | 2026-09-25 21:44 UTC 左右 |
| result | 阶段分库 gate 14 个子用例通过；ARCH-08 整体未通过 |
| scope | 进程内所有者端口，非独立服务 API |

其他仓储仍有跨所有者访问；各模块独立服务的接口、启停、扩缩、升级、恢复、容量、灰度和回退尚未通过验收。因此 ARCH-02/03/04 保持 REOPENED，ARCH-08 保持 REWORK；v1–v6 历史证据保留。

## 本轮验证

- `make test-module-isolation`：14 个阶段分库子用例通过。
- `EX_CHAT_MODULE_ACCEPTANCE=1 go test -race ./test -run '^TestModuleIsolationAcceptance$' -count=1`：通过。
- `make test`、`make test-docker`、`go vet ./cmd/... ./internal/... ./pkg/... ./test/...`：通过。
- `git diff --check`：通过。
