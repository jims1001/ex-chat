# E-ARCH-08-v95 CHN 独立成员绑定

本轮实现 TEN 最小成员权限查询与 CHN 成员绑定命令。CHN 仅取得指定用户是否属于账号的布尔结论，不接收姓名、邮箱或其他 IAM 身份字段。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `11a405c`；E-ARCH-08-v94 |
| TEN 查询 | `ten.permissions.check.v1` 返回 `account_id/user_id/allowed` |
| CHN 命令 | `chn.member.bind.v1` 支持 `bind` 与 `unbind` |
| 所有权 | CHN 先校验 TEN 账号成员，再按账号读取 CHN 收件箱并修改 InboxMember |
| 原子幂等 | 成员关系和命令收据同事务；重启重放只保留一条关系 |
| 故障 | 非成员返回未找到；TEN 停机返回依赖不可用，两者均不留下收据或关系变更 |
| 恢复 | TEN 在同址恢复后解绑成功 |
| 专项测试 | `TestCHNMemberBindUsesTENMembershipAndRollsBackFailures` 通过 |
| 回归门禁 | 84 个隔离子用例、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 及差异检查全部通过 |
| 运行清单摘要 | `2df223644eb401bf198d055ead7b62b2606397fc0c5b76c07099099a2087f2e6` |

CHN 通道授权、健康更新及账号通道撤销仍待接入独立进程。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
