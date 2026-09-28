# E-ARCH-08-v83 直接存储所有权审计门禁

原 `scripts/audit-module-storage.py` 只统计仓储文件中的外部领域类型引用，会误报 DTO/端口类型，也会漏掉 handler/service、字符串表名和预加载。本轮增加 `--direct --fail-on-violation` 模式，按文件消费者、模型所有者和实际 GORM 调用识别直接跨所有者访问，并提供内置自测。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `9052ba5`；E-ARCH-08-v82 |
| 审计覆盖 | repository/service/handler；类型变量 First/Find/Create/Save/Delete、Model、Table/Joins、Preload |
| 防误报 | 仓储端口 `repo.Create` 不视为直接存储；L0 对 AUD 中央日志的合法实现被显式允许 |
| 门禁结果 | `DIRECT_STORAGE_AUDIT_SELF_TEST=PASS`；`DIRECT_STORAGE_VIOLATIONS=0` |
| 阶段分库检查 | 84 个子用例通过 |
| 完整回归 | `make test` 通过；新审计已纳入 Go 合同测试 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |

当前门禁覆盖范围内的已识别直接跨所有者存储访问为零。该结论不替代独立进程运行验收。下一项转为模块独立配置、数据库凭据、监听入口及健康/就绪接线。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
