# E-ARCH-08-v55 WidgetHandler CHN/IAM 收件箱成员读取边界

原 `WidgetHandler.ListInboxMembers` 从 API 数据库直接联表读取 IAM 的 `User` 和 CHN 的 `InboxMember`。本轮改为由 CHN `InboxRepository.ListMemberIDs` 在账号和收件箱范围内返回成员 ID，再由 IAM `UserRepository.FindByIDs` 读取公开用户资料；响应继续保留姓名、显示名、头像、角色和在线状态字段。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `bf3d732`；E-ARCH-08-v54 |
| 阶段分库检查 | 58 个子用例通过 |
| 专项回归 | CHN/IAM 分库下只返回当前账号收件箱成员；其他账号收件箱成员不返回；两库均无对方权威表 |
| 依赖清单 | API 已有 CHN/IAM 同步依赖；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `InboxRepository.ListMemberIDs` 与 `UserRepository.FindByIDs` 查询绑定；协作摘要 `1185ba58fc15048e909901c05d4f378cfd4c55c634b3157761194731096331fe` |
| 完整回归 | `make test` 通过，Widget 接口与现有业务回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止 WidgetHandler 恢复 IAM/CHN 联表 |

WidgetHandler 的 CHN/IAM 收件箱成员读取边界已经完成。下一项为 `WidgetHandler` 的 CON 访客会话校验边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
