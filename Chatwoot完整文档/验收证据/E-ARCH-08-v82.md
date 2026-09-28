# E-ARCH-08-v82 ContactHandler 本地日志边界

`ContactHandler` 在联系人合并成功后通过共享数据库重复写 `LocalChangeJournal`。CUS `ContactRepository.MergeContacts` 已在联系人事务内写本地日志，因此本轮移除处理器数据库字段和重复写入，保留并校正仓储原子日志的 `contact_merge` 动作及兼容原因文本。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `bf85d60`；E-ARCH-08-v81 |
| 阶段分库检查 | 84 个子用例通过 |
| 专项回归 | ContactHandler 不再含 `*gorm.DB`；CUS 合并事务仍生成 contact_merge 本地日志，治理完整性用例通过 |
| 完整回归 | `make test` 通过；84 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |

ContactHandler 的共享日志数据库访问已经移除，本地日志仍由 CUS 所有者事务原子生成。下一项为全量直接存储扫描复核，区分合法的模块自有访问与尚未登记的跨所有者访问。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
