# E-ARCH-08-v76 MigrationService CUS 联系人匹配边界

`MigrationService.importConversations` 原先从 MIG 数据库直接按邮箱或标识查询 CUS `Contact`。本轮改为通过 CUS `ContactRepository.FindByEmail` / `FindByIdentifier` 完成账号范围匹配；所有者查询失败时明确记录错误并停止该条导入，只有明确未找到时才创建账号内联系人。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `a7801c0`；E-ARCH-08-v75 |
| 阶段分库检查 | 79 个子用例通过 |
| 专项回归 | MIG/CUS/CHN/CON 独立库导入通过；覆盖邮箱命中、标识命中、跨账号同邮箱拒绝及账号内回退创建；MIG 无 Contact/Conversation 表 |
| 依赖清单 | MIG→CUS ContactRepository 依赖已存在；依赖清单摘要 `2313cda54c4172b90e9753a16d3f80aae693caae351505d53762d3d1aada1361`；依赖清单合同摘要 `0ead677bd70ffdf2830af74964f01858962531956481727c96a4ff76d834c499` |
| 协作合同 | 新增 FindByEmail、FindByIdentifier 的 CUS 查询绑定；协作摘要 `5583053bb559200de2aab97af57466ea82f90e8da0f6190b567e5e214a0aa872`；模块合同摘要保持 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；79 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；MigrationService 已增加禁止直接按邮箱/标识查询 Contact 的门禁 |

MigrationService 导入会话时的 CUS 联系人邮箱及标识匹配边界已经完成。后续扫描确认下一项为 `MigrationService.importContacts` 导入联系人时的 CUS 邮箱、标识、电话及姓名查重边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
