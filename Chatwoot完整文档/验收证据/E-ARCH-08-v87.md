# E-ARCH-08-v87 Command 持久化幂等与 TEN 成员写入

本轮为独立模块 Command 增加所有者数据库内的持久化幂等收据，并接入首个真实写命令 `ten.membership.add.v1`。业务写入与收据处于同一事务，成功结果可在运行时重建后回放，失败则整体回滚。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `5d9f2f2`；E-ARCH-08-v86 |
| 幂等存储 | 使用各模块本地 `LocalChangeJournal`，键只保存 SHA-256 摘要，不保存原始幂等键 |
| 原子性 | Command 收据与业务写入共用 GORM 事务；业务失败时成员和收据均回滚，同一键可安全重试 |
| 重放 | 相同幂等键和相同业务载荷返回首次持久化结果，运行时对象重建后仍不重复写入 |
| 冲突 | 相同幂等键配合不同目标或输入返回 `state_conflict`/409 |
| TEN 命令 | `ten.membership.add.v1` 先经 IAM 独立合同验证用户，再只向 TEN 库写 AccountUser；账号目标必须与合同账号一致 |
| 敏感边界 | 回放结果只包含 TEN 成员字段与 IAM 安全身份视图，不包含密码及令牌 |
| 阶段分库检查 | 84 个子用例全部通过 |
| 完整及容器回归 | `make test`、`make test-docker` 通过，Docker 临时资源已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `36134251b8b0be640a1ca92ea1258c327af6d3f312ef6b306d3533b731c6d751` |

TEN 仍只有一个 Command 和一个 Query 接入，IAM/TEN 的其余能力及 Event 尚未迁移，不能标记模块整体完成。下一项增加多实例并发幂等竞争验收，并接入 TEN 成员列表的批量 IAM 查询以消除逐项网络调用。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
