# E-ARCH-08-v5 会话读取分库续跑

## 结论

**ARCH-08 仍为 REWORK。** 在 v4 会话删除及消息竞争补偿基础上，`ConversationRepository.FindByID` 已改为先读取 CON 自有记录和标签关系，再通过 CHN、CUS、IAM、TEN 所有者查询补齐收件箱、联系人、坐席、团队和标签。分离数据库的新增用例通过；阶段 gate 目前共有 13 个用例。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v5 |
| baseline_reference | E-ARCH-08-v1 至 v4；Git `1791488` |
| environment | 本地 macOS；CON/CHN/CUS/IAM/TEN 分离 SQLite 库；组合模式回归 |
| validation window | 2026-09-25 21:35 UTC 左右 |
| result | 会话详情跨所有者补全通过；ARCH-08 总体验收未通过 |
| scope | 本地进程内所有者端口；无独立服务部署或生产切换 |

## 新增验证

- CON 库只包含 CON 所有者表；详情查询从其他四个所有者库读取所需对象，标签关联仍从 CON 本地表读取。收件箱、联系人和团队使用不递归预加载其他模块的摘要查询。
- 导入会话继续通过 CHN/CUS 所有者校验引用，并保留历史 DisplayID。
- 会话删除、消息创建补偿和故障重试的既有分库用例继续通过。

最终验证：`make test-module-isolation` 13/13、对应 `go test -race`、`make test`、`go vet`、`make test-docker` 与 `git diff --check` 均通过；Docker 临时容器和卷已清理。这些结果不包含独立服务部署验收。

未关闭项仍包括其他会话列表/查询路径和仓储跨所有者访问、进程外公开命令与查询、26 模块独立运行、容量、备份恢复及灰度回退。因此 ARCH-02/03/04 仍为 REOPENED，ARCH-08 仍为 REWORK；v1–v4 历史证据保留。
