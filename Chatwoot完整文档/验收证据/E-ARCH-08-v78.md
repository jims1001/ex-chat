# E-ARCH-08-v78 MigrationService CHN 默认收件箱边界

`MigrationService.ensureDefaultInbox` 原先从 MIG 数据库直接查询 CHN `Inbox`，且查询异常也会继续创建默认收件箱。本轮新增 CHN `InboxRepository.FindFirstByAccount`，按账号稳定选择首个收件箱；只有明确未找到时才由 CHN 创建默认收件箱，查询错误直接返回。MigrationService 不再持有数据库句柄。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `b192469`；E-ARCH-08-v77 |
| 阶段分库检查 | 81 个子用例通过 |
| 专项回归 | MIG/CUS/CHN/CON 独立库导入通过；覆盖已有收件箱复用、跨账号拒绝、缺失时创建默认收件箱及外部权威表禁用；MIG 无 Inbox/Conversation 表 |
| 依赖清单 | MIG→CHN InboxRepository 依赖保持；依赖清单摘要 `2313cda54c4172b90e9753a16d3f80aae693caae351505d53762d3d1aada1361`；依赖清单合同摘要 `0ead677bd70ffdf2830af74964f01858962531956481727c96a4ff76d834c499` |
| 协作合同 | 新增 FindFirstByAccount 的 CHN 查询绑定；协作摘要 `5b7e2f0f2248ad3dcb009728f6e61f981673d89d1cee3e971917bce737e17496`；模块合同摘要保持 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；81 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；MigrationService 已禁止重新引入直接数据库访问 |

MigrationService.ensureDefaultInbox 的 CHN 默认收件箱读取边界已经完成。后续扫描确认下一项为 `CopilotHandler` 工具调用中的 BIL 订单及物流查询边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
