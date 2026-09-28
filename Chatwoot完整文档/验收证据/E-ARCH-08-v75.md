# E-ARCH-08-v75 EmailService ENT 品牌邮件模板读取边界

`EmailService.RenderTranscriptHTML` 原先从 MSG 数据库直接查询 ENT `BrandedEmailLayout`。本轮通过 ENT `ChannelENTRepository` 读取账号品牌配置；MSG 仍只负责邮件渲染、发送与 `EmailLog`，不再要求存在品牌配置权威表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `888d28d`；E-ARCH-08-v74 |
| 阶段分库检查 | 78 个子用例通过 |
| 专项回归 | ENT 独立库品牌色和 Logo 正确进入 HTML；其他账号使用默认样式且不泄漏品牌；MSG 无 BrandedEmailLayout 表，ENT 无 EmailLog 表 |
| 依赖清单 | 新增 MSG→ENT ChannelENTRepository 依赖并同步运行清单；依赖清单摘要 `2313cda54c4172b90e9753a16d3f80aae693caae351505d53762d3d1aada1361`；依赖清单合同摘要 `0ead677bd70ffdf2830af74964f01858962531956481727c96a4ff76d834c499` |
| 协作合同 | 新增 EmailService 对 GetBrandedEmailLayout 的 ENT 查询绑定；协作摘要 `571be1bd9b73d03e6a71c892c784427f26b70f6aae4d61e338c5fa7a7fdbe812`；模块合同摘要保持 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；78 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；EmailService 已增加禁止通过 `s.db` 查询 BrandedEmailLayout 的门禁 |

EmailService 的 ENT 品牌邮件模板读取边界已经完成。后续扫描确认下一项为 `MigrationService` 导入会话时的 CUS 联系人邮箱及标识匹配边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
