# E-ARCH-08-v77 MigrationService CUS 联系人导入查重边界

`MigrationService.importContacts` 原先从 MIG 数据库直接按邮箱、标识、电话或姓名查询 CUS `Contact`。本轮新增 CUS `ContactRepository.FindImportMatch`，保持原有查重优先级，并将查询、创建和更新错误显式写入迁移结果；MIG 不再读取联系人权威表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `927b000`；E-ARCH-08-v76 |
| 阶段分库检查 | 80 个子用例通过 |
| 专项回归 | MIG/CUS 独立库联系人导入通过；覆盖邮箱、标识、电话、姓名命中更新，跨账号同邮箱拒绝并在当前账号创建；MIG 无 Contact 表 |
| 依赖清单 | MIG→CUS ContactRepository 依赖保持；依赖清单摘要 `2313cda54c4172b90e9753a16d3f80aae693caae351505d53762d3d1aada1361`；依赖清单合同摘要 `0ead677bd70ffdf2830af74964f01858962531956481727c96a4ff76d834c499` |
| 协作合同 | 新增 FindImportMatch 的 CUS 查询绑定；协作摘要 `b75698db49d8ba1542d3f52477ae1481078eb6e75a4de0f194cef5a22119b4af`；模块合同摘要保持 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；80 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；MigrationService 已增加禁止恢复联系人直接查重的门禁 |

MigrationService.importContacts 的 CUS 联系人查重边界已经完成。后续扫描确认下一项为 `MigrationService.ensureDefaultInbox` 的 CHN 默认收件箱读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
