# E-ARCH-08-v91 所有者数据库备份恢复与投影重建

本轮为独立模块运行时增加所有者数据库快照、校验恢复、消费事件投影重建和恢复代际就绪门禁。专项场景以 TEN 独立库验证：恢复操作只处理 TEN 所有模型及本地日志，不创建 IAM 用户表；任一步失败均回滚整个目标库事务。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `b60b35a`；E-ARCH-08-v90 |
| 快照范围 | `database.ModuleModels(module)` 返回的所有者模型及 `LocalChangeJournal`，不接受额外或缺失表 |
| 完整性 | 文件保存 SHA-256 摘要；恢复前校验摘要、模块、代际和精确表集合 |
| 原子恢复 | 清理、数据写回、投影重建及 `recovery_complete` 标记处于同一事务；非法快照不修改目标库 |
| 投影重建 | `event_consumed` 日志保存完整事件正文，并按来源、聚合及版本顺序重放 |
| 就绪门禁 | 设置 `MODULE_RECOVERY_GENERATION` 后，匹配代际恢复标记写入前 `/ready` 返回 503，恢复成功后返回 200 |
| 所有权隔离 | TEN 恢复后账号事实和 Onboarding 投影存在，IAM `users` 表不存在 |
| 防误恢复 | 摘要篡改、错误模块、错误代际及外部表均拒绝，原目标数据保持不变 |
| 阶段分库检查 | `make test-module-isolation` 通过，84 个子用例全部通过 |
| 完整及容器回归 | `make test`、`make test-docker` 通过；Docker PostgreSQL/Redis 临时资源已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `c35b28796ba1d84db9187bdb6174c03b227b9a2b0ac8a8d343f5cc169f1c39c9` |

该证据完成 TEN 首个所有者数据库恢复闭环，但其余 25 个模块尚未逐一完成业务数据恢复、投影对账和生产级提取演练。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
