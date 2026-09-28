# E-ARCH-08-v52 SearchHandler CUS/CON/MSG 搜索读取边界

原 `SearchHandler` 在 SRH 数据库中直接查询 CUS 的 `Contact`、CON 的 `Conversation` 和 MSG 的 `Message`，全局搜索与三个专项入口共包含六条跨所有者读取。本轮将三类搜索分别下沉到所有者仓储，保留原查询字段、模糊匹配和 20/50 条限制；所有者查询失败时返回明确的服务错误，不再静默返回空结果。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `4aec1f9`；E-ARCH-08-v51 |
| 阶段分库检查 | 56 个子用例通过 |
| 专项回归 | SRH/CUS/CON/MSG 分库下，全局搜索和联系人、会话、消息专项入口只返回当前账号匹配数据；SRH 不含 Contact、Conversation、Message 表 |
| 依赖清单 | 新增 SRH→CUS/CON/MSG 同步依赖及 SearchHandler 源码边；依赖摘要 `36f894616f33ad3886fe46d628994e76e607a79d1a3b0bedc79c14758f72da3b` |
| 协作合同 | 新增三个 `SearchForGlobal` 所有者查询绑定；协作摘要 `0df3818f279ee5eb9556859c12747d82b89d4eaa3fcc09a75685f44d30304aea` |
| 完整回归 | `make test` 通过，现有全局搜索接口回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则覆盖 SearchHandler 对三类外部权威表的直接查询 |

SearchHandler 的 CUS/CON/MSG 搜索读取边界已经完成。下一项为 `SearchHandler` 的 KB 文章搜索读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
