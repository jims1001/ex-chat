# E-ARCH-08-v53 SearchHandler KB 文章搜索读取边界

原 `SearchHandler` 在 SRH 数据库中直接查询 KB 所有的 `Article`，全局搜索和文章专项入口均忽略数据库错误。本轮改为调用 KB `PortalRepository.SearchArticlesForGlobal`，保留账号、标题/正文模糊匹配和 20/50 条限制；KB 查询失败时返回明确服务错误。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `f8c47a9`；E-ARCH-08-v52 |
| 阶段分库检查 | 56 个子用例通过 |
| 专项回归 | SRH/CUS/CON/MSG/KB 分库下，全局搜索和文章专项入口只返回当前账号 KB 文章；SRH 不含 Article、Contact、Conversation、Message 表 |
| 依赖清单 | 新增 SRH→KB 同步依赖及 SearchHandler 到 PortalRepository 源码边；依赖摘要 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `PortalRepository.SearchArticlesForGlobal` → `kb.content.get.v1` 查询绑定；协作摘要 `80eee4df04e8d82a7579b3a861b3234bae6241f67867da6ba0536232fd0d6ca5` |
| 完整回归 | `make test` 通过，现有全局搜索和文章搜索接口回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止 SearchHandler 恢复直接数据库查询 |

SearchHandler 的 KB 文章搜索读取边界已经完成。下一项为 `WidgetHandler` 的 CON/MSG 消息读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
