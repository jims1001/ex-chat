# E-ARCH-08-v147 TKT 状态变更与重新打开

| 验收项 | 结果 |
|---|---|
| 状态机 | open/pending/resolved/closed 只允许显式定义的转换 |
| 重新打开 | `tkt.ticket.reopen.v1` 只接受 resolved 或 closed 工单并转为 open |
| 操作人边界 | 数字 actor 必须通过 TEN 成员合同验证 |
| 并发控制 | 支持 expected_version，底层更新同时限定当前 Version 并递增 |
| 原子性 | Ticket、StatusHistory、Activity、命令收据和状态事件同事务提交 |
| 幂等 | 同一命令重放不重复递增版本、不重复写历史或事件 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 TKT 评论添加与评论事件；ARCH-08 保持 REWORK。
