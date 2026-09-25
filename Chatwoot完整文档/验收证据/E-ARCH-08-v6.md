# E-ARCH-08-v6 会话列表与筛选分库续跑

## 当前结论

**ARCH-08 仍为 REWORK。** 在 v5 会话详情读取基础上，CON 的按联系人查找、会话列表和条件筛选也停止使用跨所有者表的预加载。结果页由 CON 自有记录组成，再通过所有者查询补齐关联对象；标签筛选先向 CUS 查询标签 ID，再只用 CON 本地关联表筛选会话。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v6 |
| baseline_reference | E-ARCH-08-v1 至 v5；Git `8425403` |
| environment | 本地 macOS；CON/CHN/CUS/IAM/TEN 分离 SQLite 库 |
| validation window | 2026-09-25 21:40 UTC 左右 |
| result | 分库列表和标签筛选通过；ARCH-08 总体验收未通过 |
| scope | 本地进程内所有者查询，非独立服务 API |

分库用例验证详情、列表和标签条件筛选在 CON 库没有 Inbox、Contact、User 表的情况下返回关联数据。`FindOpenByContactAndInbox` 使用相同的所有者补全路径。阶段 gate 保持 13 个子用例，现覆盖更多查询行为。

最终验证：`make test-module-isolation` 13/13、对应 `go test -race`、`make test`、`go vet`、`make test-docker`、`git diff --check` 均通过；Docker 临时容器与卷已清理。

列表结果目前按页逐条补全关联对象，尚未做批量查询和容量测量。仍未完成：其他仓储的跨所有者访问、真正的进程外命令和查询、26 模块独立启停、升级与恢复、容量及灰度切换。ARCH-02/03/04 保持 REOPENED，ARCH-08 保持 REWORK；此前证据保留。
